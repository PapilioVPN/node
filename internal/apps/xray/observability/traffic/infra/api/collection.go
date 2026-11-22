package api

import (
	"context"
	"sort"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

// List makes one non-destructive native substring scan, then enforces prefix
// and suffix. Embedded separators in an opaque identity remain intact. The
// receive bound limits Node's unary frame, not Xray's scan/allocation or heap.
func (r Reader) List(ctx context.Context, scope domain.Scope) ([]domain.Counter, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if validators.Selection(domain.Selection{Scope: scope, Name: "validation", Direction: domain.Uplink}) != nil {
		return nil, domain.ErrInvalidArgument
	}
	prefix := string(scope) + ">>>"
	response, err := r.client.QueryStats(ctx, &stats.QueryStatsRequest{Pattern: prefix, Reset_: false}, grpc.MaxCallRecvMsgSize(domain.MaxCollectionBytes))
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	if response == nil {
		return nil, domain.ErrUnavailable
	}
	if len(response.Stat) > domain.MaxCounters {
		return nil, domain.ErrCapacity
	}
	result := make([]domain.Counter, 0, len(response.Stat))
	for _, record := range response.Stat {
		if record == nil {
			return nil, domain.ErrUnavailable
		}
		if !strings.HasPrefix(record.Name, prefix) {
			continue
		}
		for _, direction := range []domain.Direction{domain.Downlink, domain.Uplink} {
			suffix := ">>>traffic>>>" + string(direction)
			if !strings.HasSuffix(record.Name, suffix) {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(record.Name, prefix), suffix)
			selection := domain.Selection{Scope: scope, Name: name, Direction: direction}
			if validators.Selection(selection) != nil {
				return nil, domain.ErrUnavailable
			}
			result = append(result, domain.Counter{Selection: selection, Bytes: record.Value})
			break
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i].Selection, result[j].Selection
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Direction < b.Direction
	})
	return result, nil
}
