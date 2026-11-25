// Package native reads WireGuard link attributes from Linux route Netlink.
package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
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

func (reader *Reader) ReadConfiguration(ctx context.Context, link status.Interface, device *PeerStatsReader) (status.Configuration, error) {
	addresses, err := reader.readAddresses(ctx, link.Index)
	if err != nil {
		return status.Configuration{}, err
	}
	var identity deviceIdentity
	if err := device.readSnapshot(ctx, &peerStatsSnapshot{name: link.Name, identity: &identity}); err != nil {
		return status.Configuration{}, err
	}
	if identity.index != uint32(link.Index) {
		return status.Configuration{}, status.ErrNotFound
	}
	return status.Configuration{PublicKey: identity.publicKey, ListenPort: identity.port, Addresses: addresses}, nil
}

func (reader *Reader) readAddresses(ctx context.Context, index int) ([]netip.Prefix, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !reader.mu.TryLock() {
		return nil, status.ErrBusy
	}
	if reader.handle == nil {
		reader.mu.Unlock()
		return nil, status.ErrUnavailable
	}
	link := &netlink.Device{LinkAttrs: netlink.LinkAttrs{Index: index}}
	observed, err := reader.handle.AddrList(link, netlink.FAMILY_ALL)
	reader.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.ETIMEDOUT) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("read wireguard addresses: %w", err)
	}
	addresses := make([]netip.Prefix, 0, len(observed))
	for _, address := range observed {
		if prefix := observedPrefix(address); prefix.IsValid() {
			addresses = append(addresses, prefix)
		}
	}
	return addresses, nil
}

func (reader *Reader) ReadInterfaceTraffic(ctx context.Context, name string) (traffic.LinkTrafficSample, error) {
	link, err := reader.readLink(ctx, name)
	if err != nil {
		return traffic.LinkTrafficSample{}, err
	}
	attrs := link.Attrs()
	counters := attrs.Statistics
	if counters == nil {
		return traffic.LinkTrafficSample{}, status.ErrUnavailable
	}
	return traffic.LinkTrafficSample{
		Counters: traffic.Counters{RXBytes: counters.RxBytes, TXBytes: counters.TxBytes},
		Index:    attrs.Index, Alias: attrs.Alias,
	}, nil
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
