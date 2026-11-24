package native

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/mdlayher/genetlink"
	gennetlink "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

const (
	maxSnapshotPeers = 4096
	maxSnapshotIPs   = maxSnapshotPeers * peers.MaxAllowedIPs
	maxPeerBatch     = 48 << 10
)

var errPeerSnapshot = errors.New("wireguard peer snapshot is incomplete or exceeds its limits")

type peerSnapshot struct {
	self       [32]byte
	peers      []peers.Peer
	allowedIPs []netip.Prefix
}

func (control *Control) SyncPeers(ctx context.Context, owner lifecycle.Link, desired []peers.Peer) (peers.Result, error) {
	snapshot, err := control.peerState(ctx, owner, desired)
	if err != nil {
		return peers.Result{}, err
	}
	updates, removals, result := peers.Diff(snapshot.peers, desired)
	return control.applyPeerChanges(ctx, owner.Index, updates, removals, result)
}

func (control *Control) applyPeerChanges(ctx context.Context, index int, updates, removals []peers.Change, result peers.Result) (peers.Result, error) {
	if err := ctx.Err(); err != nil {
		return peers.Result{}, err
	}
	for _, batch := range [][]peers.Change{removals, updates} {
		if err := control.writePeers(ctx, index, batch); err != nil {
			return peers.Result{}, errors.Join(peers.ErrUnconfirmed, err)
		}
	}
	return result, nil
}

func (control *Control) checkPeerLink(ctx context.Context, owner lifecycle.Link) error {
	link, err := control.Inspect(ctx, owner)
	if err != nil {
		return err
	}
	if !link.Up || link.Name != owner.Name {
		return status.ErrUnavailable
	}
	return nil
}

func (control *Control) peerState(ctx context.Context, owner lifecycle.Link, desired []peers.Peer) (peerSnapshot, error) {
	if err := control.checkPeerLink(ctx, owner); err != nil {
		return peerSnapshot{}, err
	}
	allowedIPCount := 0
	for i := range desired {
		allowedIPCount += len(desired[i].AllowedIPs)
	}
	snapshot, err := control.readPeers(ctx, owner.Index, len(desired), allowedIPCount)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return peerSnapshot{}, context.DeadlineExceeded
	}
	if err != nil {
		return peerSnapshot{}, err
	}
	for i := range desired {
		if desired[i].PublicKey == snapshot.self {
			return peerSnapshot{}, peers.ErrInvalid
		}
	}
	return snapshot, nil
}

func (control *Control) readPeers(ctx context.Context, index, expectedPeers, expectedIPs int) (snapshot peerSnapshot, err error) {
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	deadline := peerDeadline(ctx)
	if control.family.ID == 0 {
		if err := control.prepareWireGuard(ctx, deadline); err != nil {
			return snapshot, err
		}
	}
	if control.peerDump == nil {
		control.peerDump, err = gennetlink.Dial(genetlink.Protocol, nil)
		if err != nil {
			return peerSnapshot{}, err
		}
	}
	defer func() {
		if err != nil {
			_ = control.peerDump.Close()
			control.peerDump = nil
		}
	}()
	// A multipart dump shares the operation deadline across all datagrams.
	if err := control.peerDump.SetDeadline(deadline); err != nil {
		return peerSnapshot{}, err
	}
	encoder := gennetlink.NewAttributeEncoder()
	encoder.Uint32(unix.WGDEVICE_A_IFINDEX, uint32(index))
	data, err := encoder.Encode()
	if err != nil {
		return peerSnapshot{}, err
	}
	req, err := genetlink.NewConn(control.peerDump).Send(genetlink.Message{
		Header: genetlink.Header{Command: unix.WG_CMD_GET_DEVICE, Version: control.family.Version},
		Data:   data,
	}, control.family.ID, gennetlink.Request|gennetlink.Dump)
	if err != nil {
		return peerSnapshot{}, err
	}
	snapshot.peers = make([]peers.Peer, 0, expectedPeers)
	snapshot.allowedIPs = make([]netip.Prefix, 0, expectedIPs)
	seen := false
	for message, err := range control.peerDump.ReceiveIter() {
		if err != nil {
			return peerSnapshot{}, err
		}
		if message.Header.Type != req.Header.Type || message.Header.Flags&gennetlink.DumpInterrupted != 0 || len(message.Data) < 4 ||
			gennetlink.Validate(req, []gennetlink.Message{message}) != nil {
			return peerSnapshot{}, errPeerSnapshot
		}
		decodeErr := snapshot.decode(message.Data[4:])
		clear(message.Data)
		if decodeErr != nil {
			return peerSnapshot{}, decodeErr
		}
		seen = true
	}
	if !seen || snapshot.self == [32]byte{} {
		return peerSnapshot{}, errPeerSnapshot
	}
	return snapshot, ctx.Err()
}

// snapshotAttributes borrows one kernel message. Keep its values within the
// decode call so the raw datagram, including keys, can be cleared afterwards.
type snapshotAttributes struct {
	remaining []byte
	kind      uint16
	value     []byte
	err       error
}

func (attributes *snapshotAttributes) next() bool {
	const header = 4
	b := attributes.remaining
	if len(b) == 0 || attributes.err != nil {
		return false
	}
	if len(b) < header {
		attributes.err = errPeerSnapshot
		return false
	}
	size := int(binary.NativeEndian.Uint16(b))
	aligned := (size + 3) &^ 3
	if size < header || aligned > len(b) {
		attributes.err = errPeerSnapshot
		return false
	}
	attributes.kind = binary.NativeEndian.Uint16(b[2:]) &^ (gennetlink.Nested | gennetlink.NetByteOrder)
	attributes.value = b[header:size:size]
	attributes.remaining = b[aligned:]
	return true
}

func (snapshot *peerSnapshot) decode(data []byte) error {
	attributes := snapshotAttributes{remaining: data}
	for attributes.next() {
		switch attributes.kind {
		case unix.WGDEVICE_A_PUBLIC_KEY:
			if err := readKey(attributes.value, &snapshot.self); err != nil {
				return err
			}
		case unix.WGDEVICE_A_PEERS:
			if err := snapshot.decodePeers(attributes.value); err != nil {
				return err
			}
		}
	}
	return attributes.err
}

func (snapshot *peerSnapshot) decodePeers(data []byte) error {
	list := snapshotAttributes{remaining: data}
	for list.next() {
		start := len(snapshot.allowedIPs)
		peer, err := snapshot.decodePeer(list.value)
		if err != nil {
			return err
		}
		if peer.PublicKey == [32]byte{} {
			return errPeerSnapshot
		}
		last := len(snapshot.peers) - 1
		end := len(snapshot.allowedIPs)
		if last >= 0 && snapshot.peers[last].PublicKey == peer.PublicKey {
			start -= len(snapshot.peers[last].AllowedIPs)
			snapshot.peers[last].AllowedIPs = snapshot.allowedIPs[start:end:end]
			continue
		}
		if len(snapshot.peers) == maxSnapshotPeers {
			return errPeerSnapshot
		}
		peer.AllowedIPs = snapshot.allowedIPs[start:end:end]
		snapshot.peers = append(snapshot.peers, peer)
	}
	return list.err
}

func (snapshot *peerSnapshot) decodePeer(data []byte) (peers.Peer, error) {
	var peer peers.Peer
	fields := snapshotAttributes{remaining: data}
	for fields.next() {
		switch fields.kind {
		case unix.WGPEER_A_PUBLIC_KEY:
			if err := readKey(fields.value, &peer.PublicKey); err != nil {
				return peers.Peer{}, err
			}
		case unix.WGPEER_A_PRESHARED_KEY:
			if err := readKey(fields.value, &peer.PresharedKey); err != nil {
				return peers.Peer{}, err
			}
		case unix.WGPEER_A_ALLOWEDIPS:
			prefixes := snapshotAttributes{remaining: fields.value}
			for prefixes.next() {
				if len(snapshot.allowedIPs) == maxSnapshotIPs {
					return peers.Peer{}, errPeerSnapshot
				}
				prefix, err := decodeAllowedIP(prefixes.value)
				if err != nil {
					return peers.Peer{}, err
				}
				snapshot.allowedIPs = append(snapshot.allowedIPs, prefix)
			}
			if prefixes.err != nil {
				return peers.Peer{}, prefixes.err
			}
		}
	}
	return peer, fields.err
}

func readKey(data []byte, key *[32]byte) error {
	if len(data) != len(key) {
		return errPeerSnapshot
	}
	*key = [32]byte(data)
	return nil
}

func decodeAllowedIP(data []byte) (netip.Prefix, error) {
	fields := snapshotAttributes{remaining: data}
	var address netip.Addr
	bits := -1
	for fields.next() {
		switch fields.kind {
		case unix.WGALLOWEDIP_A_IPADDR:
			address, _ = netip.AddrFromSlice(fields.value)
		case unix.WGALLOWEDIP_A_CIDR_MASK:
			if len(fields.value) != 1 {
				return netip.Prefix{}, errPeerSnapshot
			}
			bits = int(fields.value[0])
		}
	}
	if fields.err != nil {
		return netip.Prefix{}, fields.err
	}
	prefix := netip.PrefixFrom(address, bits)
	if !prefix.IsValid() {
		return netip.Prefix{}, errPeerSnapshot
	}
	return prefix.Masked(), nil
}

func (control *Control) writePeers(ctx context.Context, index int, changes []peers.Change) error {
	if len(changes) == 0 {
		return nil
	}
	// Kernel batches may wait for affected peers to retire. Use the shared
	// operation deadline so individual writes cannot extend the total budget.
	deadline := peerDeadline(ctx)
	for len(changes) != 0 {
		size, end := 0, 0
		for end < len(changes) {
			// Upper bound for one nested peer and its IPv6 prefix attributes.
			size += 88 + 40*len(changes[end].AllowedIPs)
			if size > maxPeerBatch {
				break
			}
			end++
		}
		if err := control.writePeerMessage(ctx, deadline, index, changes[:end]); err != nil {
			return err
		}
		changes = changes[end:]
	}
	return nil
}

func (control *Control) writePeerMessage(ctx context.Context, deadline time.Time, index int, changes []peers.Change) error {
	if err := control.prepareWireGuard(ctx, deadline); err != nil {
		return err
	}
	encoder := gennetlink.NewAttributeEncoder()
	encoder.Uint32(unix.WGDEVICE_A_IFINDEX, uint32(index))
	encoder.Nested(unix.WGDEVICE_A_PEERS, func(list *gennetlink.AttributeEncoder) error {
		for i := range changes {
			list.Nested(uint16(i), func(peer *gennetlink.AttributeEncoder) error {
				encodePeerChange(peer, &changes[i])
				return nil
			})
		}
		return nil
	})
	data, err := encoder.Encode()
	if err != nil {
		return err
	}
	defer clear(data)
	return control.setWireGuard(data)
}

func peerDeadline(ctx context.Context) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return wireGuardDeadline(ctx)
}

func encodePeerChange(encoder *gennetlink.AttributeEncoder, change *peers.Change) {
	encoder.Bytes(unix.WGPEER_A_PUBLIC_KEY, change.PublicKey[:])
	if change.Remove {
		encoder.Uint32(unix.WGPEER_A_FLAGS, unix.WGPEER_F_REMOVE_ME)
		return
	}
	if change.SetPresharedKey {
		encoder.Bytes(unix.WGPEER_A_PRESHARED_KEY, change.PresharedKey[:])
	}
	if !change.ReplaceAllowedIPs {
		return
	}
	encoder.Uint32(unix.WGPEER_A_FLAGS, unix.WGPEER_F_REPLACE_ALLOWEDIPS)
	encoder.Nested(unix.WGPEER_A_ALLOWEDIPS, func(list *gennetlink.AttributeEncoder) error {
		for i, prefix := range change.AllowedIPs {
			list.Nested(uint16(i), func(fields *gennetlink.AttributeEncoder) error {
				family := uint16(unix.AF_INET6)
				if prefix.Addr().Is4() {
					family = unix.AF_INET
				}
				fields.Uint16(unix.WGALLOWEDIP_A_FAMILY, family)
				fields.Bytes(unix.WGALLOWEDIP_A_IPADDR, prefix.Addr().AsSlice())
				fields.Uint8(unix.WGALLOWEDIP_A_CIDR_MASK, uint8(prefix.Bits()))
				return nil
			})
		}
		return nil
	})
}
