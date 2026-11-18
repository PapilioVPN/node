// Package netlink owns read-only native interface observations.
package netlink

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
	native "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	maxInterfaces = 16384
	maxAddresses  = 65536
)

type source interface {
	LinkList() ([]native.Link, error)
	AddrList(native.Link, int) ([]native.Addr, error)
	SetSocketTimeout(time.Duration) error
	Close()
}

// Reader opens one request-owned route socket in the current network namespace.
// Timeout comes from the existing common request setting, not a new option.
// Context checks surround synchronous SDK calls. Socket timeouts bound individual
// waits, not an interruptible/atomic kernel dump. No process-wide SDK setting changes.
type Reader struct {
	Timeout time.Duration
	dial    func() (source, error)
}

func (reader Reader) open(ctx context.Context) (source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var conn source
	var err error
	if reader.dial != nil {
		conn, err = reader.dial()
	} else {
		conn, err = native.NewHandle(unix.NETLINK_ROUTE)
	}
	if err != nil {
		return nil, err
	}
	if err := reader.prepare(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (reader Reader) prepare(ctx context.Context, conn source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timeout := reader.Timeout
	if timeout <= 0 {
		return errors.New("interface read requires a positive socket timeout")
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		timeout = min(timeout, remaining)
	}
	// The SDK uses a microsecond timeval; round up instead of changing the common
	// request contract. Context checks still reject an expired observation.
	timeout = max(timeout, time.Microsecond)
	return conn.SetSocketTimeout(timeout)
}

func (reader Reader) List(ctx context.Context) (domain.Snapshot, error) {
	conn, err := reader.open(ctx)
	if err != nil {
		return domain.Snapshot{}, err
	}
	defer conn.Close()
	links, err := conn.LinkList()
	if err != nil {
		return domain.Snapshot{}, errors.Join(ctx.Err(), fmt.Errorf("dump interfaces: %w", err))
	}
	if len(links) > maxInterfaces {
		return domain.Snapshot{}, errors.New("interface inventory exceeds capacity")
	}
	if err := reader.prepare(ctx, conn); err != nil {
		return domain.Snapshot{}, err
	}
	addresses, err := conn.AddrList(nil, native.FAMILY_ALL)
	if err != nil {
		return domain.Snapshot{}, errors.Join(ctx.Err(), fmt.Errorf("dump interface addresses: %w", err))
	}
	if len(addresses) > maxAddresses {
		return domain.Snapshot{}, errors.New("interface address inventory exceeds capacity")
	}
	if err := ctx.Err(); err != nil {
		return domain.Snapshot{}, err
	}
	result := domain.Snapshot{Interfaces: make([]domain.Interface, 0, len(links)), UnmatchedAddresses: []domain.Address{}}
	positions := make(map[uint32]int, len(links))
	for _, link := range links {
		if err := ctx.Err(); err != nil {
			return domain.Snapshot{}, err
		}
		row, err := interfaceRecord(link)
		if err != nil {
			return domain.Snapshot{}, err
		}
		if _, ok := positions[row.Index]; ok {
			return domain.Snapshot{}, errors.New("duplicate interface index")
		}
		positions[row.Index] = len(result.Interfaces)
		result.Interfaces = append(result.Interfaces, row)
	}
	for _, address := range addresses {
		if err := ctx.Err(); err != nil {
			return domain.Snapshot{}, err
		}
		row, err := addressRecord(address)
		if err != nil {
			return domain.Snapshot{}, err
		}
		if index, ok := positions[row.InterfaceIndex]; ok {
			result.Interfaces[index].Addresses = append(result.Interfaces[index].Addresses, row)
		} else {
			result.UnmatchedAddresses = append(result.UnmatchedAddresses, row)
		}
	}
	return result, nil
}

func (reader Reader) Counters(ctx context.Context) ([]domain.InterfaceCounters, error) {
	conn, err := reader.open(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	links, err := conn.LinkList()
	if err != nil {
		return nil, errors.Join(ctx.Err(), fmt.Errorf("dump interface counters: %w", err))
	}
	if len(links) > maxInterfaces {
		return nil, errors.New("interface counter inventory exceeds capacity")
	}
	rows := make([]domain.InterfaceCounters, 0, len(links))
	seen := map[uint32]bool{}
	for _, link := range links {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attrs, err := linkAttributes(link)
		if err != nil {
			return nil, err
		}
		id := uint32(attrs.Index)
		if seen[id] {
			return nil, errors.New("duplicate interface index")
		}
		seen[id] = true
		rows = append(rows, domain.InterfaceCounters{Index: id, Name: attrs.Name, Counters: counterRecord(attrs.Statistics)})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
