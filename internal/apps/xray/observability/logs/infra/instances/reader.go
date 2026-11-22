// Package instances adapts the instance-owned private console source.
package instances

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
	native "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Reader struct{ Source native.OutputSource }

func (r Reader) Read(ctx context.Context, execution string, offset uint64, limit uint32) (domain.Chunk, error) {
	p, err := r.Source.ReadOutput(ctx, native.OutputRequest{ExecutionID: execution, Offset: offset, Limit: limit})
	if err != nil {
		for _, pair := range []struct{ from, to error }{{native.ErrExecutionChanged, domain.ErrExecutionChanged}, {native.ErrUnsupported, domain.ErrUnsupported}, {native.ErrInvalidArgument, domain.ErrInvalidArgument}, {native.ErrClosed, domain.ErrClosed}} {
			if errors.Is(err, pair.from) {
				return domain.Chunk{}, errors.Join(pair.to, err)
			}
		}
		return domain.Chunk{}, err
	}
	return domain.Chunk{ExecutionID: p.ExecutionID, Generation: p.Generation, Offset: p.Offset, NextOffset: p.NextOffset, Data: p.Data, CapacityReached: p.CapacityReached, PositionUncertain: p.PositionUncertain, Reattached: p.Reattached, Exited: p.Exited, ObservedAt: p.ObservedAt}, nil
}
