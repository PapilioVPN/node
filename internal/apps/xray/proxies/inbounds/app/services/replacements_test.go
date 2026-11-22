package services_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
)

type inspector struct{ err error }

func (inspector) Describe(b []byte) (domain.Summary, error) {
	return domain.Summary{Tag: "a", Protocol: "http"}, nil
}
func (inspector) Fingerprint(b []byte) (string, error) {
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

type replacementNative struct {
	current               []byte
	calls                 []string
	removeError, addError error
	afterRemove           func()
}

func (n *replacementNative) Records(context.Context) ([]domain.Record, error) {
	n.calls = append(n.calls, "read")
	return []domain.Record{{Tag: "a", Configuration: n.current}}, nil
}
func (n *replacementNative) Tags(context.Context) ([]string, error) { return []string{"a"}, nil }
func (n *replacementNative) Remove(context.Context, string) (domain.NativeResult, error) {
	n.calls = append(n.calls, "remove")
	if n.afterRemove != nil {
		n.afterRemove()
	}
	if n.removeError != nil {
		return domain.NativeResult{Effect: domain.Unknown}, n.removeError
	}
	return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
}
func (n *replacementNative) Add(_ context.Context, b []byte) (domain.NativeResult, error) {
	n.calls = append(n.calls, "add")
	if n.addError != nil {
		return domain.NativeResult{Effect: domain.Unknown}, n.addError
	}
	n.current = b
	return domain.NativeResult{Tag: "a", Effect: domain.Completed, Acknowledged: true}, nil
}
func replacementService(t *testing.T) (*services.Service, *services.Catalog, *replacementNative, *execution) {
	t.Helper()
	n := &replacementNative{current: []byte("old")}
	e := &execution{generation: 1}
	s, err := services.New([]services.Binding{{ID: "edge-a", Client: n}}, e)
	if err != nil {
		t.Fatal(err)
	}
	return s, services.NewCatalog(s, inspector{}), n, e
}
func TestReplacementRequiresReviewedInterruptionAndRejectsStaleCandidates(t *testing.T) {
	for _, scenario := range []string{"restart", "disconnect", "generation", "configuration", "local_mutation", "success"} {
		t.Run(scenario, func(t *testing.T) {
			s, c, n, e := replacementService(t)
			input := domain.ReplacementInput{Tag: "a", Configuration: []byte("new"), AllowInterruption: scenario != "restart", DisconnectExisting: scenario == "disconnect"}
			p, err := c.Plan(context.Background(), "edge-a", input)
			if err != nil {
				t.Fatal(err)
			}
			input.Configuration[0] = 'X' // admitted plans must own the candidate
			n.calls = nil
			switch scenario {
			case "generation":
				e.next()
			case "configuration":
				n.current = []byte("external")
			case "local_mutation":
				_, err = s.Add(context.Background(), "edge-a", []byte("old"))
				if err != nil {
					t.Fatal(err)
				}
				n.calls = nil
			}
			result, err := c.Replace(context.Background(), "edge-a", p.Token)
			switch scenario {
			case "success":
				if err != nil || result.Effect != domain.Completed || string(n.current) != "new" || fmt.Sprint(n.calls) != "[read remove add]" {
					t.Fatal(result, err, n.calls, string(n.current))
				}
				replay, err := c.Replace(context.Background(), "edge-a", p.Token)
				if !errors.Is(err, domain.ErrStale) || replay.Effect != domain.NotApplied {
					t.Fatal(replay, err)
				}
			case "restart", "disconnect":
				if !errors.Is(err, domain.ErrUnsupported) || len(n.calls) != 0 || p.Mode != "restart" || p.Fallback != "replace_configuration" {
					t.Fatal(p, result, err, n.calls)
				}
			default:
				if !errors.Is(err, domain.ErrStale) || result.Effect != domain.NotApplied {
					t.Fatal(result, err, n.calls)
				}
				for _, call := range n.calls {
					if call != "read" {
						t.Fatal("stale plan dispatched mutation", n.calls)
					}
				}
			}
		})
	}
}
func TestReplacementPartialAndUnknownEffectsFenceExecutionAndRetainSteps(t *testing.T) {
	for _, phase := range []string{"remove", "add", "canceled_after_remove", "generation_after_remove"} {
		t.Run(phase, func(t *testing.T) {
			s, c, n, e := replacementService(t)
			p, err := c.Plan(context.Background(), "edge-a", domain.ReplacementInput{Tag: "a", Configuration: []byte("new"), AllowInterruption: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch phase {
			case "remove":
				n.removeError = errors.New("private native failure")
			case "add":
				n.addError = errors.New("private native failure")
			case "canceled_after_remove":
				n.afterRemove = cancel
			case "generation_after_remove":
				n.afterRemove = e.next
			}
			result, err := c.Replace(ctx, "edge-a", p.Token)
			if err == nil || !result.RequiresInstanceRestart || result.Replacement == nil || !result.Replacement.Remove.Attempted {
				t.Fatal(result, err)
			}
			if phase != "remove" && !result.Replacement.Remove.Acknowledged {
				t.Fatal(result)
			}
			if (phase == "canceled_after_remove" || phase == "generation_after_remove") && result.Replacement.Add.Attempted {
				t.Fatal("add after cancellation or execution replacement")
			}
			status, err := s.Status(context.Background(), "edge-a")
			if err != nil || status.LastOperation == nil || status.LastOperation.Replacement == nil {
				t.Fatal(status, err)
			}
			status.LastOperation.Replacement.Token = "changed"
			status, _ = s.Status(context.Background(), "edge-a")
			if status.LastOperation.Replacement.Token != p.Token {
				t.Fatal("borrowed last-operation progress")
			}
			if phase != "generation_after_remove" {
				_, err = s.Tags(context.Background(), "edge-a")
				if !errors.Is(err, domain.ErrUncertain) {
					t.Fatal(err)
				}
				e.next()
			}
			if _, err = s.Tags(context.Background(), "edge-a"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func (i inspector) Candidate(b []byte) (domain.Summary, string, error) {
	if i.err != nil {
		return domain.Summary{}, "", i.err
	}
	s, err := i.Describe(b)
	if err != nil {
		return s, "", err
	}
	h, err := i.Fingerprint(b)
	return s, h, err
}

func TestReplacementStructuralRejectionDoesNotReadOrMutateEngine(t *testing.T) {
	s, _, native, _ := replacementService(t)
	catalog := services.NewCatalog(s, inspector{err: domain.ErrInvalidArgument})
	_, err := catalog.Plan(context.Background(), "edge-a", domain.ReplacementInput{Tag: "a", Configuration: []byte("foreign account"), AllowInterruption: true})
	if !errors.Is(err, domain.ErrInvalidArgument) || len(native.calls) != 0 {
		t.Fatal("invalid candidate reached native engine", native.calls, err)
	}
}
