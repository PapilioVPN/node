package services_test

import (
	"context"
	"errors"
	"testing"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type preparer func(context.Context, string, string, string, []configs.Source) ([]byte, error)

func (p preparer) Prepare(ctx context.Context, id, tag, email string, sources []configs.Source) ([]byte, error) {
	return p(ctx, id, tag, email, sources)
}
func TestPreparationFailureCancellationAndReplacementDoNotSubmit(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancel", "replace"} {
		t.Run(mode, func(t *testing.T) {
			e := &execution{generation: 1, state: instances.Running}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			s, err := services.New([]services.Binding{{ID: "a", Client: client{add: func(_ context.Context, tag string, content []byte) (domain.NativeResult, error) {
				calls++
				if tag != "members" || string(content) != "native" {
					t.Fatal(tag, content)
				}
				return domain.NativeResult{Email: "selected", Effect: domain.Completed, Acknowledged: true}, nil
			}}}}, e)
			if err != nil {
				t.Fatal(err)
			}
			p := preparer(func(context.Context, string, string, string, []configs.Source) ([]byte, error) {
				switch mode {
				case "failure":
					return nil, domain.ErrInvalidArgument
				case "cancel":
					cancel()
				case "replace":
					e.replace()
				}
				return []byte("native"), nil
			})
			sources, err := services.NewSources(s, p)
			if err != nil {
				t.Fatal(err)
			}
			result, err := sources.Add(ctx, "a", "members", "selected", nil)
			if mode == "success" {
				if err != nil || calls != 1 || !result.Acknowledged {
					t.Fatal(result, err, calls)
				}
			} else if err == nil || calls != 0 || result.Effect != domain.NotApplied {
				t.Fatal(result, err, calls)
			}
			if mode == "replace" && !errors.Is(err, domain.ErrExecutionChanged) {
				t.Fatal(err)
			}
		})
	}
}
