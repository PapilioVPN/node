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

func (control *Control) UpdateConfiguration(ctx context.Context, owner lifecycle.Link, previous lifecycle.Checkpoint, desired lifecycle.Config, fields lifecycle.LiveField) (status.Interface, error) {
	if fields&(lifecycle.LiveMTU|lifecycle.LiveAddresses) != 0 && fields&(fields-1) != 0 {
		return control.updateCombined(ctx, owner, previous, desired, fields)
	}
	return control.updateConfiguration(ctx, owner, previous.Fixed, desired, fields)
}

func (control *Control) updateConfiguration(ctx context.Context, owner lifecycle.Link, previous lifecycle.FixedConfig, desired lifecycle.Config, field lifecycle.LiveField) (status.Interface, error) {
	link, err := control.lookup(ctx, owner)
	if err != nil {
		return status.Interface{}, err
	}
	observed := interfaceStatus(link)
	if !observed.Up || observed.Name != owner.Name || observed.MTU != desired.MTU {
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
	if field&lifecycle.LivePort != 0 {
		want.port = desired.ListenPort
		encoder.Uint16(unix.WGDEVICE_A_LISTEN_PORT, desired.ListenPort)
	}
	if field&lifecycle.LivePrivateKey != 0 {
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
		if field&lifecycle.LivePort != 0 && desired.ListenPort == 0 && after.port != 0 && writeErr == nil {
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

// Apply reversible link changes before the WireGuard key/port write. Do not
// retain a previous private key in memory or publish any intermediate state.
func (control *Control) updateCombined(ctx context.Context, owner lifecycle.Link, previous lifecycle.Checkpoint, desired lifecycle.Config, fields lifecycle.LiveField) (status.Interface, error) {
	current, err := control.Inspect(ctx, owner)
	if err != nil {
		return status.Interface{}, err
	}
	if current.Name != owner.Name || !current.Up || current.MTU != previous.MTU {
		return status.Interface{}, status.ErrUnavailable
	}
	var addressesChanged, mtuChanged bool
	rollback := func(cause error) (status.Interface, error) {
		if !addressesChanged && !mtuChanged {
			return status.Interface{}, cause
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifecycle.OperationTimeout)
		defer cancel()
		var err error
		if mtuChanged {
			_, err = control.UpdateMTU(ctx, owner, desired.MTU, previous.MTU)
		}
		if addressesChanged {
			link, lookupErr := control.lookup(ctx, owner)
			if lookupErr != nil {
				err = errors.Join(err, lookupErr)
			} else {
				_, addressErr := control.updateAddresses(ctx, owner, link, desired.Addresses, previous.Fixed.Addresses)
				err = errors.Join(err, addressErr)
			}
		}
		if err != nil {
			return status.Interface{}, errors.Join(lifecycle.ErrUnconfirmed, cause, err)
		}
		return status.Interface{}, cause
	}
	if fields&lifecycle.LiveAddresses != 0 {
		stage := desired
		stage.MTU = previous.MTU
		current, err = control.updateConfiguration(ctx, owner, previous.Fixed, stage, lifecycle.LiveAddresses)
		if err != nil {
			return status.Interface{}, err
		}
		addressesChanged = true
	}
	if fields&lifecycle.LiveMTU != 0 {
		current, err = control.UpdateMTU(ctx, owner, previous.MTU, desired.MTU)
		if err != nil {
			return rollback(err)
		}
		mtuChanged = true
	}
	if deviceFields := fields & (lifecycle.LivePort | lifecycle.LivePrivateKey); deviceFields != 0 {
		current, err = control.updateConfiguration(ctx, owner, previous.Fixed, desired, deviceFields)
		if err != nil {
			return rollback(err)
		}
	}
	return current, nil
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
