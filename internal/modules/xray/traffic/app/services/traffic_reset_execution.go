package services

import (
	"context"
	"errors"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
)

// resetConcurrentTrafficCounters stops new admission on an observed failure, but
// lets already admitted calls finish under the operation's original deadline.
// Canceling peers on error could discard valid acknowledgements. Wait joins every
// admitted call; each writes only its own selected entry and retained cause.
func resetConcurrentTrafficCounters(ctx context.Context, resetter TrafficCounterResetter, scope domain.TrafficScope, items []domain.TrafficCounterResetEntry, maxConcurrent int) error {
	var group errgroup.Group
	group.SetLimit(min(maxConcurrent, len(items)))
	var stopped atomic.Bool
	causes := make([]error, len(items))
	for i := range items {
		if stopped.Load() {
			break
		}
		if err := ctx.Err(); err != nil {
			causes[i] = err
			break
		}
		group.Go(func() error {
			// Go can wait for a slot while another call fails or the caller cancels.
			// Recheck before invoking the port; skipped entries remain unattempted.
			if stopped.Load() {
				return nil
			}
			if err := ctx.Err(); err != nil {
				causes[i] = err
				stopped.Store(true)
				return err
			}
			item := &items[i]
			acknowledgement, err := resetter.ResetTrafficCounter(ctx, domain.TrafficSelection{Scope: scope, Name: item.Name, Direction: item.Direction})
			if err != nil {
				item.State = domain.TrafficResetNotApplied
				if errors.Is(err, domain.ErrTrafficResetUnknown) {
					item.State = domain.TrafficResetUnknown
				}
				causes[i] = err
				stopped.Store(true)
				return err
			}
			item.State, item.Reset = domain.TrafficResetAcknowledged, &acknowledgement
			return nil
		})
	}
	// Retain every failure in selection order, not only errgroup's first error.
	_ = group.Wait()
	return errors.Join(causes...)
}
