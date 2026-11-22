package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type preparer func(context.Context, string, string, []configs.Source) (domain.Addition, error)

func (p preparer) Prepare(ctx context.Context, id, tag string, sources []configs.Source) (domain.Addition, error) {
	return p(ctx, id, tag, sources)
}

type additionFunc func(context.Context) (domain.NativeResult, error)

func (f additionFunc) Add(ctx context.Context) (domain.NativeResult, error) { return f(ctx) }

func TestSourcePreparationCannotSubmitAfterFailureOrExecutionReplacement(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "replacement", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := &execution{generation: 1}
			calls := 0
			service := service(t, native{add: func(context.Context) (domain.NativeResult, error) {
				calls++
				return domain.NativeResult{Tag: "a", Effect: domain.Completed, Acknowledged: true}, nil
			}}, e)
			prepares := 0
			s, err := services.NewSources(service, preparer(func(context.Context, string, string, []configs.Source) (domain.Addition, error) {
				prepares++
				switch mode {
				case "rejected":
					return nil, domain.ErrInvalidArgument
				case "replacement":
					e.next()
				case "canceled":
					cancel()
				}
				return additionFunc(func(ctx context.Context) (domain.NativeResult, error) {
					calls++
					return domain.NativeResult{Tag: "a", Effect: domain.Completed, Acknowledged: true}, nil
				}), nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.Add(ctx, "edge-a", "a", nil)
			if prepares != 1 {
				t.Fatal(prepares)
			}
			if mode == "success" {
				if err != nil || calls != 1 || !result.Acknowledged {
					t.Fatal(result, err, calls)
				}
			} else {
				if err == nil || calls != 0 || result.Effect != domain.NotApplied {
					t.Fatal(result, err, calls)
				}
			}
			if mode == "replacement" && !errors.Is(err, domain.ErrExecutionChanged) {
				t.Fatal(err)
			}
		})
	}
}
