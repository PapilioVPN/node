package native

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"slices"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	gennetlink "github.com/mdlayher/netlink"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type deviceIdentity struct {
	publicKey [32]byte
	port      uint16
	index     uint32
}

func (identity *deviceIdentity) decode(data []byte) error {
	fields := snapshotAttributes{remaining: data}
	for fields.next() {
		switch fields.kind {
		case unix.WGDEVICE_A_IFINDEX:
			if len(fields.value) != 4 {
				return errPeerSnapshot
			}
			identity.index = binary.NativeEndian.Uint32(fields.value)
		case unix.WGDEVICE_A_PUBLIC_KEY:
			if err := readKey(fields.value, &identity.publicKey); err != nil {
				return err
			}
		case unix.WGDEVICE_A_LISTEN_PORT:
			if len(fields.value) != 2 {
				return errPeerSnapshot
			}
			identity.port = binary.NativeEndian.Uint16(fields.value)
		}
	}
	return fields.err
}

func (control *Control) readIdentity(ctx context.Context, index int) (identity deviceIdentity, err error) {
	if control.family.ID == 0 {
		if err := control.prepareWireGuard(ctx, wireGuardDeadline(ctx)); err != nil {
			return identity, err
		}
	}
	if control.peerDump == nil {
		control.peerDump, err = openPeerDump()
		if err != nil {
			return identity, err
		}
	}
	defer func() {
		if err != nil {
			_ = control.peerDump.Close()
			control.peerDump = nil
		}
	}()
	encoder := gennetlink.NewAttributeEncoder()
	encoder.Uint32(unix.WGDEVICE_A_IFINDEX, uint32(index))
	data, err := encoder.Encode()
	if err != nil {
		return identity, err
	}
	if err := control.peerDump.query(ctx, control.family, data, identity.decode); err != nil {
		return identity, err
	}
	if identity.index != uint32(index) || identity.publicKey == [32]byte{} {
		return identity, errPeerSnapshot
	}
	return identity, nil
}

func (control *Control) UpdateConfiguration(ctx context.Context, owner lifecycle.Link, previous lifecycle.FixedConfig, desired lifecycle.Config, field lifecycle.LiveField) (status.Interface, error) {
	link, err := control.lookup(ctx, owner)
	if err != nil {
		return status.Interface{}, err
	}
	observed := interfaceStatus(link)
	if !observed.Up || observed.MTU != desired.MTU {
		return status.Interface{}, status.ErrUnavailable
	}
	if field == lifecycle.LiveAddresses {
		return control.updateAddresses(ctx, owner, link, previous.Addresses, desired.Addresses)
	}
	before, err := control.readIdentity(ctx, owner.Index)
	if err != nil {
		return status.Interface{}, err
	}
	if previous.ListenPort != 0 && previous.ListenPort != before.port {
		return status.Interface{}, status.ErrUnavailable
	}
	want := before
	encoder := gennetlink.NewAttributeEncoder()
	encoder.Uint32(unix.WGDEVICE_A_IFINDEX, uint32(owner.Index))
	switch field {
	case lifecycle.LivePort:
		want.port = desired.ListenPort
		encoder.Uint16(unix.WGDEVICE_A_LISTEN_PORT, desired.ListenPort)
	case lifecycle.LivePrivateKey:
		want.publicKey = desired.PublicKey()
		peers, err := control.ReadPeerSet(ctx, owner)
		if err != nil {
			return status.Interface{}, err
		}
		for _, peer := range peers.Peers {
			if peer.PublicKey == want.publicKey {
				return status.Interface{}, lifecycle.ErrConflict
			}
		}
		encoder.Bytes(unix.WGDEVICE_A_PRIVATE_KEY, desired.PrivateKey[:])
	default:
		return status.Interface{}, lifecycle.ErrConflict
	}
	data, err := encoder.Encode()
	if err != nil {
		return status.Interface{}, err
	}
	defer clear(data)
	if err := control.prepareWireGuard(ctx, wireGuardDeadline(ctx)); err != nil {
		return status.Interface{}, err
	}
	if err := ctx.Err(); err != nil {
		return status.Interface{}, err
	}
	writeErr := control.setWireGuard(data)
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), socketTimeout)
	defer cancel()
	after, readErr := control.readIdentity(readCtx, owner.Index)
	if readErr == nil {
		if field == lifecycle.LivePort && desired.ListenPort == 0 && after.port != 0 && writeErr == nil {
			want.port = after.port
		}
		if after == want {
			confirmed, err := control.Inspect(readCtx, owner)
			if err == nil && confirmed.Up && confirmed.MTU == desired.MTU {
				return confirmed, nil
			}
			return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, err)
		}
		if after == before {
			return status.Interface{}, errors.Join(status.ErrUnavailable, writeErr, ctx.Err())
		}
	}
	return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, writeErr, readErr)
}

func addressFor(prefix netip.Prefix) *netlink.Addr {
	return &netlink.Addr{IPNet: &net.IPNet{IP: prefix.Addr().AsSlice(), Mask: net.CIDRMask(prefix.Bits(), prefix.Addr().BitLen())}}
}

func (control *Control) updateAddresses(ctx context.Context, owner lifecycle.Link, link netlink.Link, previous, desired []netip.Prefix) (status.Interface, error) {
	present, err := control.route.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return status.Interface{}, err
	}
	for _, prefix := range previous {
		if !slices.ContainsFunc(present, func(addr netlink.Addr) bool { return observedPrefix(addr) == prefix }) {
			return status.Interface{}, status.ErrUnavailable
		}
	}
	for _, prefix := range desired {
		if !slices.Contains(previous, prefix) && slices.ContainsFunc(present, func(addr netlink.Addr) bool {
			return observedPrefix(addr).Addr() == prefix.Addr() && !slices.Contains(previous, observedPrefix(addr))
		}) {
			return status.Interface{}, lifecycle.ErrConflict
		}
	}
	var added, removed []netip.Prefix
	apply := func() error {
		for _, prefix := range previous {
			if slices.Contains(desired, prefix) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := control.route.AddrDel(link, addressFor(prefix)); err != nil {
				return err
			}
			removed = append(removed, prefix)
		}
		for _, prefix := range desired {
			if slices.Contains(previous, prefix) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := control.route.AddrAdd(link, addressFor(prefix)); err != nil {
				return err
			}
			added = append(added, prefix)
		}
		return nil
	}
	if err := apply(); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), socketTimeout)
		defer cancel()
		if _, ownershipErr := control.lookup(rollbackCtx, owner); ownershipErr != nil {
			return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, err, ownershipErr)
		}
		var rollbackErr error
		for _, prefix := range added {
			rollbackErr = errors.Join(rollbackErr, control.route.AddrDel(link, addressFor(prefix)))
		}
		for _, prefix := range removed {
			rollbackErr = errors.Join(rollbackErr, control.route.AddrAdd(link, addressFor(prefix)))
		}
		current, listErr := control.route.AddrList(link, netlink.FAMILY_ALL)
		rollbackErr = errors.Join(rollbackErr, listErr)
		if listErr == nil && !matchesAddressEdit(current, previous, desired) {
			rollbackErr = errors.Join(rollbackErr, lifecycle.ErrUnconfirmed)
		}
		if rollbackErr != nil {
			return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, err, rollbackErr)
		}
		return status.Interface{}, errors.Join(status.ErrUnavailable, err)
	}
	verify, cancel := context.WithTimeout(context.WithoutCancel(ctx), socketTimeout)
	defer cancel()
	confirmed, err := control.Inspect(verify, owner)
	if err != nil || !confirmed.Up {
		return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, err)
	}
	present, err = control.route.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, err)
	}
	if !matchesAddressEdit(present, desired, previous) {
		return status.Interface{}, lifecycle.ErrUnconfirmed
	}
	return confirmed, nil
}

func matchesAddressEdit(current []netlink.Addr, expected, replaced []netip.Prefix) bool {
	for _, prefix := range expected {
		if !slices.ContainsFunc(current, func(addr netlink.Addr) bool {
			return observedPrefix(addr) == prefix && addr.Flags&unix.IFA_F_DADFAILED == 0
		}) {
			return false
		}
	}
	for _, prefix := range replaced {
		if !slices.Contains(expected, prefix) && slices.ContainsFunc(current, func(addr netlink.Addr) bool { return observedPrefix(addr) == prefix }) {
			return false
		}
	}
	return true
}
