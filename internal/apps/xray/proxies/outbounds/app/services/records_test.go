package services_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type execution struct {
	mu         sync.Mutex
	generation uint64
}

func (e *execution) Status(ctx context.Context, id string) (instances.Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return instances.Status{ID: id, Execution: instances.Execution{Generation: e.generation, State: instances.Running}}, ctx.Err()
}
func (e *execution) next() { e.mu.Lock(); defer e.mu.Unlock(); e.generation++ }

type client func(context.Context) ([]domain.Record, error)

func (c client) Add(context.Context, []byte) (domain.NativeResult, error) {
	panic("unexpected native addition")
}
func (c client) Remove(context.Context, string) (domain.NativeResult, error) {
	panic("unexpected native removal")
}
func (c client) Records(ctx context.Context) ([]domain.Record, error) { return c(ctx) }
func TestReadFencesExecutionChangesAndRetainsNativeRecords(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			e := &execution{generation: 1}
			calls := 0
			s, err := services.New([]services.Binding{{ID: "a", Client: client(func(context.Context) ([]domain.Record, error) {
				calls++
				if replace {
					e.next()
				}
				return []domain.Record{{Tag: ""}, {Tag: ""}}, nil
			})}}, e)
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Records(context.Background(), "a")
			if replace {
				if !errors.Is(err, domain.ErrExecutionChanged) || result.Outbounds != nil {
					t.Fatal(result, err)
				}
			} else if err != nil || len(result.Outbounds) != 2 || result.Generation != 1 || result.ObservedAt.IsZero() {
				t.Fatal(result, err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}
func TestGlobalAndPerInstanceReadsRejectOverloadWithoutQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 32)
	completed := make(chan error, 32)
	native := client(func(ctx context.Context) ([]domain.Record, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	bindings := []services.Binding{}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		bindings = append(bindings, services.Binding{ID: id, Client: native})
	}
	s, err := services.New(bindings, &execution{generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		for range 8 {
			go func(id string) { _, err := s.Records(ctx, id); completed <- err }(id)
		}
	}
	for range 32 {
		<-entered
	}
	for _, id := range []string{"a", "e"} {
		if _, err := s.Records(context.Background(), id); !errors.Is(err, domain.ErrCapacity) {
			t.Fatal(id, err)
		}
	}
	cancel()
	for range 32 {
		if err := <-completed; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
