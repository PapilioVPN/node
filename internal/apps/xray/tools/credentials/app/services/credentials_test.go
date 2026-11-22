package services

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func TestTargetValidationOneInvocationAndPrivateFailure(t *testing.T) {
	calls := 0
	g := generator{uuid: func(context.Context, domain.UUIDInput) (domain.UUIDResult, error) {
		calls++
		return domain.UUIDResult{UUID: "fixture"}, nil
	}}
	s, err := New([]Binding{{ID: "a", Generator: g}, {ID: "disabled"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.UUID(context.Background(), "a", domain.UUIDInput{})
	if err != nil || first.InstanceID != "a" || first.Tool != "xray.uuid" || first.CompletedAt.IsZero() || first.Duration < 0 || calls != 1 {
		t.Fatal("selection or metadata")
	}
	for _, id := range []string{"", "missing", "disabled"} {
		if _, err := s.UUID(context.Background(), id, domain.UUIDInput{}); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	empty := ""
	if _, err := s.UUID(context.Background(), "a", domain.UUIDInput{Name: &empty}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.UUID(ctx, "a", domain.UUIDInput{}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("canceled request dispatched")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	g.uuid = func(context.Context, domain.UUIDInput) (domain.UUIDResult, error) {
		cancel()
		return domain.UUIDResult{UUID: "fixture-private"}, nil
	}
	s.bindings["a"] = g
	result, err := s.UUID(ctx, "a", domain.UUIDInput{})
	if !errors.Is(err, context.Canceled) || result.UUID != "" {
		t.Fatal("partial artifact on cancellation")
	}
}
