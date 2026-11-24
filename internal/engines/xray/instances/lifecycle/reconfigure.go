package lifecycle

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/liveupdate"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
)

func (service *Service) reconcile(ctx context.Context, run *execution, desired desiredConfig, keepAlive time.Duration) (StartAction, error) {
	healthCtx, cancel := context.WithTimeout(ctx, time.Second)
	healthErr := service.health(healthCtx, run.conn)
	cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}

	same := run.desired.equal(desired)
	if healthErr == nil && same {
		if service.refreshRunning(run, run.desired, keepAlive) {
			return Heartbeat, nil
		}
		return Recovered, nil
	}
	if !same {
		if err := service.checkCandidate(ctx, desired.source); err != nil {
			return "", err
		}
	}
	if healthErr != nil || !service.isCurrent(run) {
		return Recovered, nil
	}
	return service.tryLiveUpdate(ctx, run, desired, keepAlive)
}

func (service *Service) checkCandidate(ctx context.Context, source []byte) error {
	checkCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	issue, err := validation.CheckStartSource(checkCtx, service.config.Executable, service.config.RuntimeDirectory, source)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if issue != nil {
		return &StartError{Reason: ConfigRejected, cause: errors.Join(ErrInvalidStart, err)}
	}
	if err != nil {
		return &StartError{Reason: ConfigCheckFailed, cause: err}
	}
	return nil
}

func (service *Service) tryLiveUpdate(ctx context.Context, run *execution, desired desiredConfig, keepAlive time.Duration) (StartAction, error) {
	if !slices.Equal(run.desired.excluded, desired.excluded) ||
		len(run.desired.source) > runconfig.MaxSourceBytes || len(desired.source) > runconfig.MaxSourceBytes ||
		!run.child.executableMatches(service.config.Executable) {
		return Restarted, nil
	}
	plan, compatible := liveupdate.BuildPlan(run.desired.source, desired.source)
	if !compatible {
		return Restarted, nil
	}

	changeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	change, err := plan.Prepare(changeCtx, service.config.Executable, service.config.RuntimeDirectory, desired.source)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return RestartedAfterLiveFailure, nil
	}
	if !run.child.executableMatches(service.config.Executable) {
		return Restarted, nil
	}

	if err := run.routingWrites.Acquire(changeCtx, 1); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrBusy
	}
	defer run.routingWrites.Release(1)
	if !service.isCurrent(run) {
		return Recovered, nil
	}
	if run.routingUncertain {
		return Restarted, nil
	}
	attempted, err := change.Apply(context.WithValue(changeCtx, configurationWriteKey{}, run), run.conn)
	if err == nil {
		if service.refreshRunning(run, desired.owned(), keepAlive) {
			return LiveUpdated, nil
		}
		return Recovered, nil
	}
	if !attempted {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return RestartedAfterLiveFailure, nil
	}

	// A rejected or unacknowledged step may follow earlier effects. Retire
	// the old execution even if the request that began the change is gone.
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCleanup()
	stopErr := service.stop(cleanupCtx, Failed)
	if stopErr != nil || ctx.Err() != nil {
		return LiveUpdateFailed, &StartError{Reason: LiveApplyFailed, cause: errors.Join(err, stopErr, ctx.Err())}
	}
	return RestartedAfterLiveFailure, nil
}

func (service *Service) isCurrent(run *execution) bool {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.run == run && service.state == Ready
}

func (service *Service) refreshRunning(run *execution, desired desiredConfig, keepAlive time.Duration) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.run != run || service.state != Ready {
		return false
	}
	run.desired = desired
	service.refreshLeaseLocked(run, keepAlive)
	return true
}
