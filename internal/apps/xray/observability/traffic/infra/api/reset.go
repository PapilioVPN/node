package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Reset atomically swaps exactly one existing counter, without preflight/retry.
// The old value cannot be reconstructed by a later observation after reply loss.
func (r Reader) Reset(ctx context.Context, selection domain.Selection) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validators.Selection(selection); err != nil {
		return 0, err
	}
	name := string(selection.Scope) + ">>>" + selection.Name + ">>>traffic>>>" + string(selection.Direction)
	var remote peer.Peer
	response, err := r.client.GetStats(ctx, &stats.GetStatsRequest{Name: name, Reset_: true}, grpc.Peer(&remote))
	if err != nil {
		mapped := nativeError(ctx, err)
		if errors.Is(err, rpc.ErrClosed) || status.Code(err) == codes.NotFound || status.Code(err) == codes.Unimplemented || remote.Addr == nil {
			return 0, mapped
		}
		return 0, errors.Join(domain.ErrResetUnknown, mapped)
	}
	if response == nil || response.Stat == nil || response.Stat.Name != name {
		return 0, errors.Join(domain.ErrResetUnknown, domain.ErrUnavailable)
	}
	return response.Stat.Value, nil
}
