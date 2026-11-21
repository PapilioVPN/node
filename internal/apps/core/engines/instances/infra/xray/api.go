//go:build linux

package xray

import (
	"context"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type APIConfig struct {
	Network string
	Address string
}

// api is owned by this adapter until another actual capability needs the same
// native resource. One lazy channel is reused across probes and engine restarts.
type api struct {
	config     *APIConfig
	mu         sync.Mutex
	connection *grpc.ClientConn
	closed     bool
}

func newAPI(config *APIConfig) (*api, error) {
	if config != nil {
		switch config.Network {
		case "tcp":
			address, err := netip.ParseAddrPort(config.Address)
			if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 {
				return nil, domain.ErrInvalidArgument
			}
		case "unix":
			if !filepath.IsAbs(config.Address) || len(config.Address) > 107 || strings.ContainsRune(config.Address, 0) {
				return nil, domain.ErrInvalidArgument
			}
		default:
			return nil, domain.ErrInvalidArgument
		}
		copied := *config
		config = &copied
	}
	return &api{config: config}, nil
}

func (a *api) check(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return false, domain.ErrClosed
	}
	if a.config == nil {
		a.mu.Unlock()
		return false, domain.ErrUnsupported
	}
	if a.connection == nil {
		config := *a.config
		connection, err := grpc.NewClient("passthrough:///"+config.Address,
			grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry(),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, config.Network, config.Address)
			}),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<10)))
		if err != nil {
			a.mu.Unlock()
			return false, err
		}
		a.connection = connection
	}
	client := stats.NewStatsServiceClient(a.connection)
	a.mu.Unlock()
	_, err := client.GetSysStats(ctx, &stats.SysStatsRequest{})
	return err == nil, err
}

func (a *api) close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.connection != nil {
		return a.connection.Close()
	}
	return nil
}
