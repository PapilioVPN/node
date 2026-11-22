package services

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type SourcePreparer interface {
	Prepare(context.Context, string, string, []configs.Source) (domain.Addition, error)
}

// Sources coordinates preparation and live addition through the same mutation
// admission and receipt owner used for compiled inbound input.
type Sources struct {
	service  *Service
	preparer SourcePreparer
}

func NewSources(service *Service, preparer SourcePreparer) (*Sources, error) {
	if service == nil || preparer == nil {
		return nil, domain.ErrInvalidArgument
	}
	return &Sources{service, preparer}, nil
}
func (s *Sources) Add(ctx context.Context, id, tag string, sources []configs.Source) (domain.Operation, error) {
	if err := validators.Tag(tag); err != nil {
		return domain.Operation{InstanceID: id, Kind: "add", Tag: tag, Effect: domain.NotApplied}, err
	}
	return s.service.mutate(ctx, id, "add", tag, func(_ Client, generation uint64) (domain.NativeResult, error) {
		prepared, err := s.preparer.Prepare(ctx, id, tag, sources)
		if err != nil {
			return domain.NativeResult{Tag: tag, Effect: domain.NotApplied}, err
		}
		if err := ctx.Err(); err != nil {
			return domain.NativeResult{Tag: tag, Effect: domain.NotApplied}, err
		}
		state, err := s.service.status.Status(ctx, id)
		if err != nil {
			return domain.NativeResult{Tag: tag, Effect: domain.NotApplied}, err
		}
		if state.Execution.Generation != generation || state.Execution.State != instances.Running {
			return domain.NativeResult{Tag: tag, Effect: domain.NotApplied}, domain.ErrExecutionChanged
		}
		if prepared == nil {
			return domain.NativeResult{Tag: tag, Effect: domain.NotApplied}, domain.ErrInvalidArgument
		}
		return prepared.Add(ctx)
	})
}
