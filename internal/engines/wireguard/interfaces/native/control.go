package native

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/mdlayher/genetlink"
	gennetlink "github.com/mdlayher/netlink"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type Control struct {
	route    *netlink.Handle
	generic  *genetlink.Conn
	peerDump *peerDumpSocket
	family   genetlink.Family
}

func (control *Control) Open(ctx context.Context) error {
	route, err := netlink.NewHandle(unix.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	if err := route.SetSocketTimeout(socketTimeout); err != nil {
		route.Close()
		return err
	}
	generic, err := genetlink.Dial(nil)
	if err != nil {
		route.Close()
		return err
	}
	control.route, control.generic = route, generic
	if err := ctx.Err(); err != nil {
		return errors.Join(err, control.Close())
	}
	return nil
}

func (control *Control) Close() error {
	control.route.Close()
	var err error
	if control.peerDump != nil {
		err = control.peerDump.Close()
	}
	if control.generic != nil {
		err = errors.Join(err, control.generic.Close())
	}
	return err
}

func (control *Control) Create(ctx context.Context, owner lifecycle.Link, config lifecycle.Config) (status.Interface, error) {
	if err := ctx.Err(); err != nil {
		return status.Interface{}, err
	}
	link := &netlink.Wireguard{LinkAttrs: netlink.LinkAttrs{
		Name: owner.Name, MTU: int(config.MTU),
	}}
	if err := control.route.LinkAdd(link); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return status.Interface{}, lifecycle.ErrConflict
		}
		return status.Interface{}, err
	}
	// Linux accepts the create request without applying IFLA_IFALIAS.
	// Tag the index returned by LinkAdd before publishing ownership.
	if link.Index == 0 {
		return status.Interface{}, status.ErrUnavailable
	}
	owner.Index = link.Index
	result := interfaceStatus(link)
	if err := control.route.LinkSetAlias(link, owner.Alias); err != nil {
		return result, errors.Join(err, control.route.LinkDel(link))
	}
	if err := control.configure(ctx, owner.Index, config); err != nil {
		return result, err
	}
	for _, prefix := range config.Addresses {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		address := &netlink.Addr{IPNet: &net.IPNet{
			IP: prefix.Addr().AsSlice(), Mask: net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen()),
		}}
		if err := control.route.AddrAdd(link, address); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := control.route.LinkSetUp(link); err != nil {
		return result, err
	}
	confirmed, err := control.Inspect(ctx, owner)
	if err != nil {
		return result, err
	}
	if !confirmed.Up || confirmed.Name != owner.Name || confirmed.MTU != config.MTU {
		return result, status.ErrUnavailable
	}
	addresses, err := control.route.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return result, err
	}
	for _, expected := range config.Addresses {
		found := false
		for _, address := range addresses {
			if observedPrefix(address) == expected && address.Flags&unix.IFA_F_DADFAILED == 0 {
				found = true
				break
			}
		}
		if !found {
			return result, status.ErrUnavailable
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return confirmed, nil
}

func (control *Control) configure(ctx context.Context, index int, config lifecycle.Config) error {
	if err := control.prepareWireGuard(ctx, wireGuardDeadline(ctx)); err != nil {
		return err
	}
	encoder := gennetlink.NewAttributeEncoder()
	encoder.Uint32(unix.WGDEVICE_A_IFINDEX, uint32(index))
	encoder.Bytes(unix.WGDEVICE_A_PRIVATE_KEY, config.PrivateKey[:])
	encoder.Uint16(unix.WGDEVICE_A_LISTEN_PORT, config.ListenPort)
	data, err := encoder.Encode()
	if err != nil {
		return err
	}
	defer clear(data)
	return control.setWireGuard(data)
}

func (control *Control) prepareWireGuard(ctx context.Context, deadline time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if control.generic == nil {
		generic, err := genetlink.Dial(nil)
		if err != nil {
			return err
		}
		control.generic = generic
	}
	if err := control.generic.SetDeadline(deadline); err != nil {
		return err
	}
	if control.family.ID == 0 {
		family, err := control.generic.GetFamily("wireguard")
		if err != nil {
			return err
		}
		control.family = family
	}
	return nil
}

func wireGuardDeadline(ctx context.Context) time.Time {
	end := time.Now().Add(socketTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(end) {
		return deadline
	}
	return end
}

func (control *Control) setWireGuard(data []byte) error {
	_, err := control.generic.Execute(genetlink.Message{
		Header: genetlink.Header{Command: unix.WG_CMD_SET_DEVICE, Version: control.family.Version},
		Data:   data,
	}, control.family.ID, gennetlink.Request|gennetlink.Acknowledge)
	if err != nil {
		_ = control.generic.Close()
		control.generic = nil
		control.family = genetlink.Family{}
	}
	return err
}

func (control *Control) Inspect(ctx context.Context, owner lifecycle.Link) (status.Interface, error) {
	link, err := control.lookup(ctx, owner)
	if err != nil {
		return status.Interface{}, err
	}
	return interfaceStatus(link), ctx.Err()
}

func (control *Control) UpdateMTU(ctx context.Context, owner lifecycle.Link, previous, desired uint32) (status.Interface, error) {
	link, err := control.lookup(ctx, owner)
	if err != nil {
		return status.Interface{}, err
	}
	before := interfaceStatus(link)
	if !before.Up || before.Name != owner.Name || before.MTU != previous {
		return status.Interface{}, status.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return status.Interface{}, err
	}
	changeErr := control.route.LinkSetMTU(link, int(desired))
	observed, readErr := control.Inspect(ctx, owner)
	if readErr != nil {
		// The write may have completed after the request was canceled. Resolve its
		// outcome with one bounded read while lifecycle ownership is still held.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), socketTimeout)
		defer cancel()
		observed, readErr = control.Inspect(readCtx, owner)
	}
	if readErr == nil && observed.Up && observed.Name == owner.Name {
		switch observed.MTU {
		case desired:
			return observed, nil
		case previous:
			return status.Interface{}, errors.Join(status.ErrUnavailable, changeErr, ctx.Err())
		}
	}
	return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, changeErr, readErr)
}

func (control *Control) Remove(ctx context.Context, owner lifecycle.Link) error {
	link, err := control.lookup(ctx, owner)
	if errors.Is(err, status.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := control.route.LinkDel(link); err != nil && !errors.Is(err, unix.ENODEV) {
		return err
	}
	return nil
}

func (control *Control) lookup(ctx context.Context, owner lifecycle.Link) (netlink.Link, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var link netlink.Link
	var err error
	if owner.Index == 0 {
		link, err = control.route.LinkByName(owner.Name)
	} else {
		link, err = control.route.LinkByIndex(owner.Index)
	}
	var missing netlink.LinkNotFoundError
	switch {
	case errors.As(err, &missing), errors.Is(err, unix.ENODEV):
		return nil, status.ErrNotFound
	case err != nil:
		return nil, err
	case owner.Alias != "" && (link.Type() != "wireguard" || link.Attrs().Alias != owner.Alias):
		return nil, lifecycle.ErrConflict
	default:
		return link, nil
	}
}

func observedPrefix(address netlink.Addr) netip.Prefix {
	ip, ok := netip.AddrFromSlice(address.IP)
	if !ok {
		return netip.Prefix{}
	}
	bits, _ := address.Mask.Size()
	return netip.PrefixFrom(ip.Unmap(), bits)
}
