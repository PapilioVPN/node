package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
)

// ReadTrafficCounters uses one QueryStats, sharing the native channel and bounds
// with other reads. Xray offers no pagination; its internal scan ignores context.
func (c *Counters) ReadTrafficCounters(ctx context.Context, scope domain.TrafficScope) (domain.TrafficCounters, error) {
	if err := ctx.Err(); err != nil {
		return domain.TrafficCounters{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.TrafficCounters{}, err
	}
	defer done()
	client, err := c.API.StatsClient()
	if err != nil {
		return domain.TrafficCounters{}, err
	}
	prefix := string(scope) + ">>>"
	response, err := client.QueryStats(ctx, &native.QueryStatsRequest{Pattern: prefix, Reset_: false})
	if cause := ctx.Err(); cause != nil {
		return domain.TrafficCounters{}, errors.Join(cause, err)
	}
	if err != nil {
		switch status.Code(err) {
		case codes.Unimplemented:
			err = errors.Join(domain.ErrTrafficUnsupported, err)
		case codes.ResourceExhausted:
			err = errors.Join(domain.ErrAPIResourceLimit, err)
		case codes.DeadlineExceeded:
			err = errors.Join(context.DeadlineExceeded, err)
		}
		return domain.TrafficCounters{}, fmt.Errorf("read Xray traffic counters: %w", err)
	}
	if response == nil {
		return domain.TrafficCounters{}, domain.ErrInvalidTrafficResponse
	}
	result := domain.TrafficCounters{Items: make([]domain.TrafficCounterEntry, 0, len(response.Stat))}
	for _, stat := range response.Stat {
		if err := ctx.Err(); err != nil {
			return domain.TrafficCounters{}, err
		}
		if stat == nil {
			return domain.TrafficCounters{}, domain.ErrInvalidTrafficResponse
		}
		// QueryStats matches substrings, so a name from another scope can also match.
		name, matches := strings.CutPrefix(stat.Name, prefix)
		if !matches {
			continue
		}
		var direction domain.TrafficDirection
		switch {
		case strings.HasSuffix(name, ">>>traffic>>>uplink"):
			name = strings.TrimSuffix(name, ">>>traffic>>>uplink")
			direction = domain.Uplink
		case strings.HasSuffix(name, ">>>traffic>>>downlink"):
			name = strings.TrimSuffix(name, ">>>traffic>>>downlink")
			direction = domain.Downlink
		default:
			continue
		}
		if name == "" {
			return domain.TrafficCounters{}, domain.ErrInvalidTrafficResponse
		}
		result.Items = append(result.Items, domain.TrafficCounterEntry{Name: name, Direction: direction, Bytes: stat.Value})
	}
	if err := ctx.Err(); err != nil {
		return domain.TrafficCounters{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}
