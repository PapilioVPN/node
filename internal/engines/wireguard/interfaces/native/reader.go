// Package native reads WireGuard link attributes from Linux route Netlink.
package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const socketTimeout = 250 * time.Millisecond

type Reader struct {
	mu     sync.Mutex
	handle *netlink.Handle
}

func (reader *Reader) Open(ctx context.Context) error {
	handle, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("open wireguard route socket: %w", err)
	}
	if err := handle.SetSocketTimeout(socketTimeout); err != nil {
		handle.Close()
		return fmt.Errorf("configure wireguard route socket: %w", err)
	}
	if err := ctx.Err(); err != nil {
		handle.Close()
		return err
	}
	reader.mu.Lock()
	reader.handle = handle
	reader.mu.Unlock()
	return nil
}

func (reader *Reader) Close(context.Context) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.handle != nil {
		reader.handle.Close()
		reader.handle = nil
	}
	return nil
}

func (reader *Reader) Read(ctx context.Context, name string) (status.Interface, error) {
	link, err := reader.readLink(ctx, name)
	if err != nil {
		return status.Interface{}, err
	}
	return interfaceStatus(link), nil
}

func (reader *Reader) ReadInterfaceTraffic(ctx context.Context, name string) (traffic.Counters, error) {
	link, err := reader.readLink(ctx, name)
	if err != nil {
		return traffic.Counters{}, err
	}
	counters := link.Attrs().Statistics
	if counters == nil {
		return traffic.Counters{}, status.ErrUnavailable
	}
	return traffic.Counters{RXBytes: counters.RxBytes, TXBytes: counters.TxBytes}, nil
}

func (reader *Reader) readLink(ctx context.Context, name string) (netlink.Link, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// One socket has one request in flight. Admission also protects the
	// handle's internal lookup mode and serializes reads with Close.
	if !reader.mu.TryLock() {
		return nil, status.ErrBusy
	}
	defer reader.mu.Unlock()
	if reader.handle == nil {
		return nil, status.ErrUnavailable
	}
	link, err := reader.handle.LinkByName(name)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		var missing netlink.LinkNotFoundError
		switch {
		case errors.As(err, &missing):
			return nil, status.ErrNotFound
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.ETIMEDOUT):
			return nil, errors.Join(context.DeadlineExceeded, err)
		default:
			return nil, fmt.Errorf("read wireguard link: %w", err)
		}
	}
	attrs := link.Attrs()
	if link.Type() != "wireguard" || attrs.Name != name {
		return nil, status.ErrNotFound
	}
	return link, nil
}

func interfaceStatus(link netlink.Link) status.Interface {
	attrs := link.Attrs()
	return status.Interface{
		Name: attrs.Name, MTU: uint32(attrs.MTU), Up: attrs.Flags&net.FlagUp != 0,
		Index: attrs.Index, Alias: attrs.Alias,
	}
}
