// Package rpc owns configured local Xray API channels shared by native consumers.
package rpc

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	ErrClosed          = errors.New("native Xray API connection closed")
	ErrInvalidArgument = errors.New("invalid local Xray API target")
)

type Config struct{ Network, Address string }

// Binding borrows an instance-owned client. Nil Client means no API configured.
type Binding struct {
	ID     string
	Client *Client
}

// Client implements the native SDK connection interface using one lazy channel.
// Its instance owner closes it after draining operations, not at engine restart.
type Client struct {
	config     Config
	mu         sync.Mutex
	connection *grpc.ClientConn
	closed     bool
	closeOnce  sync.Once
	closeErr   error
}

func New(config Config) (*Client, error) {
	switch config.Network {
	case "tcp":
		address, err := netip.ParseAddrPort(config.Address)
		if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 || address.Addr().Zone() != "" {
			return nil, ErrInvalidArgument
		}
	case "unix":
		if !filepath.IsAbs(config.Address) || len(config.Address) > 107 || strings.ContainsRune(config.Address, 0) {
			return nil, ErrInvalidArgument
		}
	default:
		return nil, ErrInvalidArgument
	}
	return &Client{config: config}, nil
}
func (c *Client) channel() (*grpc.ClientConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.connection == nil {
		config := c.config
		connection, err := grpc.NewClient("passthrough:///"+config.Address,
			grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry(),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, config.Network, config.Address)
			}),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<10)))
		if err != nil {
			return nil, err
		}
		c.connection = connection
	}
	return c.connection, nil
}
func (c *Client) Invoke(ctx context.Context, method string, args, reply any, options ...grpc.CallOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	connection, err := c.channel()
	if err != nil {
		return err
	}
	return connection.Invoke(ctx, method, args, reply, options...)
}
func (c *Client) NewStream(ctx context.Context, description *grpc.StreamDesc, method string, options ...grpc.CallOption) (grpc.ClientStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection, err := c.channel()
	if err != nil {
		return nil, err
	}
	return connection.NewStream(ctx, description, method, options...)
}
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		connection := c.connection
		c.mu.Unlock()
		if connection != nil {
			c.closeErr = connection.Close()
		}
	})
	return c.closeErr
}
