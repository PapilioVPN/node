// Package xray owns the shared native Xray channel, admission and deadlines.
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

	observatorynative "github.com/xtls/xray-core/app/observatory/command"
	handlernative "github.com/xtls/xray-core/app/proxyman/command"
	routingnative "github.com/xtls/xray-core/app/router/command"
	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

var ErrInvalidSettings = errors.New("invalid Xray API settings")

type Config struct {
	Network         string
	Address         string
	RequestTimeout  time.Duration
	MaxReceiveBytes int
	MaxConcurrent   int
}

// Client owns one reusable local native channel, not an engine execution.
type Client struct {
	mu              sync.Mutex
	closed          bool
	target          string
	maxReceiveBytes int
	connection      *grpc.ClientConn
	stats           native.StatsServiceClient
	handlers        handlernative.HandlerServiceClient
	routing         routingnative.RoutingServiceClient
	observatory     observatorynative.ObservatoryServiceClient
	timeout         time.Duration
	slots           chan struct{}
	closeOnce       sync.Once
	closeError      error
}

// NewClient has no native effects. A channel is created on its first operation,
// avoiding native workers during composition or before a failed Fx startup.
func NewClient(config Config) (*Client, error) {
	if config.RequestTimeout <= 0 || config.MaxReceiveBytes <= 0 || config.MaxConcurrent <= 0 {
		return nil, ErrInvalidSettings
	}
	var target string
	switch config.Network {
	case "unix":
		if !filepath.IsAbs(config.Address) {
			return nil, ErrInvalidSettings
		}
		target = (&url.URL{Scheme: "unix", Path: config.Address}).String()
	case "tcp":
		address, err := netip.ParseAddrPort(config.Address)
		if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 {
			return nil, ErrInvalidSettings
		}
		target = "passthrough:///" + config.Address
	default:
		return nil, ErrInvalidSettings
	}
	return &Client{target: target, maxReceiveBytes: config.MaxReceiveBytes, timeout: config.RequestTimeout, slots: make(chan struct{}, config.MaxConcurrent)}, nil
}

// StatsClient borrows the cached native statistics stub on the shared channel.
func (c *Client) StatsClient() (native.StatsServiceClient, error) {
	stats, _, err := c.nativeClients()
	return stats, err
}

func (c *Client) nativeClients() (native.StatsServiceClient, handlernative.HandlerServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, grpc.ErrClientConnClosing
	}
	if c.stats != nil {
		return c.stats, c.handlers, nil
	}
	connection, err := grpc.NewClient(c.target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(c.maxReceiveBytes)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("construct Xray API channel: %w", err)
	}
	c.connection = connection
	c.stats = native.NewStatsServiceClient(connection)
	c.handlers = handlernative.NewHandlerServiceClient(connection)
	c.routing = routingnative.NewRoutingServiceClient(connection)
	c.observatory = observatorynative.NewObservatoryServiceClient(connection)
	return c.stats, c.handlers, nil
}

// Close terminates the channel and interrupts active native calls. Request owners
// join their callers; the management server is drained before final module cleanup.
func (c *Client) Close() error {
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

// Begin admits one operation on the existing instance-wide capacity without
// creating a channel. Native payload validation may still fail before first I/O.
func (c *Client) Begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case c.slots <- struct{}{}:
	default:
		return nil, nil, engineapi.ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	return ctx, func() { cancel(); <-c.slots }, nil
}

// HandlerClient reuses the native channel owned by this instance resource.
func (c *Client) HandlerClient() (handlernative.HandlerServiceClient, error) {
	_, client, err := c.nativeClients()
	return client, err
}

// RoutingClient borrows the cached native routing stub on the shared channel.
func (c *Client) RoutingClient() (routingnative.RoutingServiceClient, error) {
	if _, _, err := c.nativeClients(); err != nil {
		return nil, err
	}
	return c.routing, nil
}

// ObservatoryClient borrows the cached native observatory stub on the shared channel.
func (c *Client) ObservatoryClient() (observatorynative.ObservatoryServiceClient, error) {
	if _, _, err := c.nativeClients(); err != nil {
		return nil, err
	}
	return c.observatory, nil
}
