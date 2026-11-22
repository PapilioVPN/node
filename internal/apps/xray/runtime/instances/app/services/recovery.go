package services

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Retainer interface {
	SetRetention(context.Context, string, bool) error
}

func (s *Service) List(ctx context.Context, after string, limit uint32) (domain.InstancePage, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstancePage{}, err
	}
	if err := validators.Inventory(after, limit); err != nil {
		return domain.InstancePage{}, err
	}
	if limit == 0 {
		limit = 50
	}
	offset := sort.Search(len(s.inventory), func(i int) bool { return s.inventory[i].binding.ID > after })
	end := min(len(s.inventory), offset+int(limit))
	result := domain.InstancePage{Items: make([]domain.Status, 0, end-offset)}
	for _, entry := range s.inventory[offset:end] {
		if err := ctx.Err(); err != nil {
			return domain.InstancePage{}, err
		}
		result.Items = append(result.Items, observe(entry))
	}
	if end < len(s.inventory) && len(result.Items) > 0 {
		result.NextAfter = s.inventory[end-1].binding.ID
	}
	return result, nil
}
func (s *Service) SetRetention(ctx context.Context, id string, r domain.RetentionRequest) (domain.Control, error) {
	entry, err := s.selectInstance(ctx, id)
	if err != nil {
		return domain.Control{Effect: domain.NotApplied}, err
	}
	result := domain.Control{Effect: domain.NotApplied}
	if entry.binding.Retainer == nil {
		return result, domain.ErrUnsupported
	}
	select {
	case entry.mutation <- struct{}{}:
	default:
		return result, domain.ErrBusy
	}
	defer func() { <-entry.mutation }()
	if s.closed.Load() {
		return result, domain.ErrClosed
	}
	err = entry.binding.Retainer.SetRetention(ctx, r.ExecutionID, r.Preserve)
	value := observe(entry)
	result.Status = &value
	if err == nil {
		result.Effect = domain.Completed
	}
	return result, err
}

// StopExpected fences the expiry action under the same admission as manual control.
func (s *Service) StopExpected(ctx context.Context, id, executionID string) (domain.Control, error) {
	return s.controlExpected(ctx, id, true, false, executionID)
}

// Repair keeps a healthy execution. A caller must explicitly permit replacement
// of an unready execution, whose API failure alone is not proof of a crash.
func (s *Service) Repair(ctx context.Context, id string, r domain.RepairRequest) (domain.RepairResult, error) {
	result := domain.RepairResult{Control: domain.Control{Effect: domain.NotApplied}, Action: "none"}
	if err := validators.Repair(r); err != nil {
		return result, err
	}
	entry, err := s.selectInstance(ctx, id)
	if err != nil {
		return result, err
	}
	select {
	case entry.mutation <- struct{}{}:
	default:
		return result, domain.ErrBusy
	}
	defer func() { <-entry.mutation }()
	if s.closed.Load() {
		return result, domain.ErrClosed
	}
	current := observe(entry)
	result.Control.Status = &current
	if r.ExecutionID != "" && current.Execution.ExecutionID != r.ExecutionID {
		return result, domain.ErrConflict
	}
	if !entry.binding.Runtime.APIConfigured() {
		return result, domain.ErrUnsupported
	}
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	if current.Execution.State == domain.Running {
		probe, release := context.WithTimeout(operation, 250*time.Millisecond)
		ready, probeErr := entry.binding.Runtime.Check(probe)
		release()
		if ready && probeErr == nil {
			result.Ready = true
			result.Action = "attached"
			result.Control.Effect = domain.Completed
			return result, nil
		}
		if !r.ReplaceUnready {
			return result, errors.Join(domain.ErrReadiness, probeErr)
		}
	}
	for attempt := uint32(0); attempt < r.MaxAttempts; attempt++ {
		if err := operation.Err(); err != nil {
			return result, err
		}
		if entry.binding.Runtime.Observe().State == domain.Running {
			result.Action = "restarted"
			if err := entry.binding.Runtime.Stop(operation); err != nil {
				result.Control.Effect = domain.Unknown
				return result, err
			}
			result.Control.Effect = domain.Partial
		} else {
			result.Action = "started"
		}
		result.Attempts++
		if err := entry.binding.Runtime.Start(operation); err != nil {
			value := observe(entry)
			result.Control.Status = &value
			if !errors.Is(err, domain.ErrNotApplied) {
				result.Control.Effect = domain.Unknown
				return result, err
			}
			if attempt+1 == r.MaxAttempts {
				return result, err
			}
			continue
		}
		result.Control.Effect = domain.Completed
		probe, release := context.WithTimeout(operation, 2*time.Second)
		ticker := time.NewTicker(250 * time.Millisecond)
		var last error
		for checks := 0; checks < 8; checks++ {
			ready, err := entry.binding.Runtime.Check(probe)
			last = err
			if ready && err == nil {
				result.Ready = true
				break
			}
			if errors.Is(err, domain.ErrUnsupported) || entry.binding.Runtime.Observe().State != domain.Running {
				break
			}
			select {
			case <-probe.Done():
				last = errors.Join(last, probe.Err())
			case <-ticker.C:
				continue
			}
			break
		}
		ticker.Stop()
		release()
		value := observe(entry)
		result.Control.Status = &value
		if result.Ready {
			return result, nil
		}
		if attempt+1 == r.MaxAttempts {
			return result, errors.Join(domain.ErrReadiness, last)
		}
	}
	return result, domain.ErrReadiness
}
