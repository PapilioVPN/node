package native

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

const maxResetNameBytes = 512 << 10

func (client *Traffic) ResetScope(ctx context.Context, request traffic.ScopeReset, concurrency int) ([]traffic.CounterReset, error) {
	conn, err := client.instances.NativeConnection(request.ID)
	if err != nil {
		return nil, err
	}
	prefix := string(request.Scope) + ">>>"
	counters, err := readTrafficCounters(ctx, conn, prefix, listTrafficOptions)
	if err != nil {
		return nil, err
	}
	report, err := selectScopeCounters(prefix, counters, int(request.MaxCounters))
	if err != nil {
		return nil, err
	}
	if len(report) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := client.checkConnection(request.ID, conn); err != nil {
			return nil, err
		}
	}
	if concurrency > 1 && len(report) > 1 {
		return report, client.resetScopeConcurrent(ctx, request.ID, conn, prefix, report, concurrency)
	}
	for i := range report {
		if err := client.resetScopeCounter(ctx, request.ID, conn, prefix, &report[i]); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (client *Traffic) resetScopeCounter(ctx context.Context, id string, conn *grpc.ClientConn, prefix string, row *traffic.CounterReset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := client.checkConnection(id, conn); err != nil {
		return err
	}
	key := prefix + row.Name + ">>>traffic>>>" + string(row.Direction)
	result, err := resetDirection(ctx, conn, key)
	row.DirectionReset = result
	if result.Effect == traffic.ResetMissing {
		return traffic.ErrNotFound
	}
	return err
}

func (client *Traffic) resetScopeConcurrent(ctx context.Context, id string, conn *grpc.ClientConn, prefix string, report []traffic.CounterReset, concurrency int) error {
	var group errgroup.Group
	group.SetLimit(concurrency)
	var stopped atomic.Bool
	var mu sync.Mutex
	var failures []error
	for i := range report {
		if stopped.Load() {
			break
		}
		group.Go(func() error {
			// Admission can block while a running reset reports a failure.
			if stopped.Load() {
				return nil
			}
			err := client.resetScopeCounter(ctx, id, conn, prefix, &report[i])
			if err != nil {
				stopped.Store(true)
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
			return err
		})
	}
	if group.Wait() != nil {
		return errors.Join(failures...)
	}
	return nil
}

func selectScopeCounters(prefix string, counters []*TrafficCounter, limit int) ([]traffic.CounterReset, error) {
	report := make([]traffic.CounterReset, 0, min(len(counters), limit))
	nameBytes := 0
	for _, counter := range counters {
		key, inScope := strings.CutPrefix(counter.GetName(), prefix)
		if !inScope {
			continue
		}
		name, direction := splitTrafficCounter(key)
		if name == "" {
			continue
		}
		nameBytes += len(name)
		if len(report) == limit || len(name) > traffic.MaxCounterNameBytes || nameBytes > maxResetNameBytes {
			return nil, traffic.ErrTooLarge
		}
		report = append(report, traffic.CounterReset{
			Name: name, Direction: direction,
			DirectionReset: traffic.DirectionReset{Effect: traffic.ResetNotAttempted},
		})
	}
	slices.SortFunc(report, func(a, b traffic.CounterReset) int {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return strings.Compare(string(b.Direction), string(a.Direction))
	})
	for i := 1; i < len(report); i++ {
		if report[i].Name == report[i-1].Name && report[i].Direction == report[i-1].Direction {
			return nil, traffic.ErrUnavailable
		}
	}
	return report, nil
}
