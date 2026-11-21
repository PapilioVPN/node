package services

import (
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type restartFunc func(context.Context) (domain.NativeResult, error)

func (f restartFunc) Reopen(ctx context.Context) (domain.NativeResult, error) { return f(ctx) }

type statusFunc func(context.Context, string) (instances.Status, error)

func (f statusFunc) Status(ctx context.Context, id string) (instances.Status, error) {
	return f(ctx, id)
}
func running(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}
func serviceFixture(t *testing.T, r restartFunc, status StatusReader) *Service {
	t.Helper()
	service, err := New([]Binding{{ID: "a", Restarter: r}, {ID: "disabled"}}, status)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
