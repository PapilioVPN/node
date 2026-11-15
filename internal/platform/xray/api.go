package xray

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

var ErrInvalidAPISettings = errors.New("invalid Xray API settings")

type APIConfig struct {
	Network         string
	Address         string
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxConcurrent   int
}

// APIClient owns one reusable local native channel, not an engine execution.
type APIClient struct {
	mu              sync.Mutex
	closed          bool
	target          string
	maxReceiveBytes int
	connection      *grpc.ClientConn
	stats           native.StatsServiceClient
	timeout         time.Duration
	slots           chan struct{}
	closeOnce       sync.Once
	closeError      error
}

// NewAPIClient has no native effects. A channel is created on its first read,
// avoiding native workers during composition or before a failed Fx startup.
func NewAPIClient(config APIConfig) (*APIClient, error) {
	if config.RequestTimeout <= 0 || config.MaxReceiveBytes <= 0 || config.MaxConcurrent <= 0 {
		return nil, ErrInvalidAPISettings
	}
	var target string
	switch config.Network {
	case "unix":
		if !filepath.IsAbs(config.Address) {
			return nil, ErrInvalidAPISettings
		}
		target = (&url.URL{Scheme: "unix", Path: config.Address}).String()
	case "tcp":
		address, err := netip.ParseAddrPort(config.Address)
		if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 {
			return nil, ErrInvalidAPISettings
		}
		target = "passthrough:///" + config.Address
	default:
		return nil, ErrInvalidAPISettings
	}
	return &APIClient{target: target, maxReceiveBytes: config.MaxReceiveBytes, timeout: config.RequestTimeout, slots: make(chan struct{}, config.MaxConcurrent)}, nil
}

func (c *APIClient) statsClient() (native.StatsServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, grpc.ErrClientConnClosing
	}
	if c.stats != nil {
		return c.stats, nil
	}
	connection, err := grpc.NewClient(c.target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(c.maxReceiveBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("construct Xray API channel: %w", err)
	}
	c.connection = connection
	c.stats = native.NewStatsServiceClient(connection)
	return c.stats, nil
}

// ReadRuntimeInfo performs one bounded GetSysStats call with no application retry.
func (c *APIClient) ReadRuntimeInfo(ctx context.Context) (domain.RuntimeInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.RuntimeInfo{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return domain.RuntimeInfo{}, domain.ErrAPIBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	client, err := c.statsClient()
	if err != nil {
		return domain.RuntimeInfo{}, err
	}
	value, err := client.GetSysStats(ctx, &native.SysStatsRequest{})
	if cause := ctx.Err(); cause != nil {
		return domain.RuntimeInfo{}, errors.Join(cause, err)
	}
	if err != nil {
		switch status.Code(err) {
		case codes.Unimplemented:
			err = errors.Join(domain.ErrRuntimeInfoUnsupported, err)
		case codes.ResourceExhausted:
			err = errors.Join(domain.ErrAPIResourceLimit, err)
		case codes.DeadlineExceeded:
			err = errors.Join(context.DeadlineExceeded, err)
		}
		return domain.RuntimeInfo{}, fmt.Errorf("read Xray runtime information: %w", err)
	}
	return domain.RuntimeInfo{
		Goroutines: value.NumGoroutine, GCCount: value.NumGC,
		AllocatedBytes: value.Alloc, TotalAllocatedBytes: value.TotalAlloc, SysBytes: value.Sys,
		Mallocs: value.Mallocs, Frees: value.Frees, LiveObjects: value.LiveObjects,
		TotalGCPauseNS: value.PauseTotalNs, UptimeSeconds: value.Uptime, ObservedAt: time.Now().UTC(),
	}, nil
}

// Close terminates the channel and interrupts active native calls. Request owners
// join their callers; the management server is drained before final module cleanup.
func (c *APIClient) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		connection := c.connection
		c.mu.Unlock()
		if connection != nil {
			c.closeError = connection.Close()
		}
	})
	return c.closeError
}
