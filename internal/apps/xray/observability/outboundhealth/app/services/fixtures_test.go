package services

import (
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type readerFunc func(context.Context) ([]domain.Record, error)

func (f readerFunc) Read(ctx context.Context) ([]domain.Record, error) { return f(ctx) }

type statusFunc func(context.Context, string) (instances.Status, error)

func (f statusFunc) Status(ctx context.Context, id string) (instances.Status, error) {
	return f(ctx, id)
}
func running(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 7}}, nil
}
func newService(t *testing.T, records []domain.Record) (*Service, *int) {
	t.Helper()
	calls := new(int)
	s, err := New([]Binding{{ID: "edge", Reader: readerFunc(func(context.Context) ([]domain.Record, error) {
		*calls++
		return append([]domain.Record{}, records...), nil
	})}}, statusFunc(running))
	if err != nil {
		t.Fatal(err)
	}
	return s, calls
}
