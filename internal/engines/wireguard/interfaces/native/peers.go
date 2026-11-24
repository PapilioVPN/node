package native

import (
	"context"
	"errors"
	"net/netip"
	"os"

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
	link, err := control.Inspect(ctx, owner)
	if err != nil {
		return peers.Result{}, err
	}
	if !link.Up || link.Name != owner.Name {
		return peers.Result{}, status.ErrUnavailable
	}
	snapshot, err := control.readPeers(ctx, owner.Index, len(desired))
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return peers.Result{}, context.DeadlineExceeded
		}
		return peers.Result{}, err
	}
	for i := range desired {
		if desired[i].PublicKey == snapshot.self {
			return peers.Result{}, peers.ErrInvalid
		}
	}
	updates, removals, result := peers.Diff(snapshot.peers, desired)
	if err := ctx.Err(); err != nil {
		return peers.Result{}, err
	}
	for _, batch := range [][]peers.Change{removals, updates} {
		if err := control.writePeers(ctx, owner.Index, batch); err != nil {
			return peers.Result{}, errors.Join(peers.ErrUnconfirmed, err)
		}
	}
	return result, nil
}

func (control *Control) readPeers(ctx context.Context, index, expected int) (snapshot peerSnapshot, err error) {
	if err := ctx.Err(); err != nil {
		return snapshot, err
	}
	if control.family.ID == 0 {
		if err := control.prepareWireGuard(ctx); err != nil {
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
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = wireGuardDeadline(ctx)
	}
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
	snapshot.peers = make([]peers.Peer, 0, expected)
	snapshot.allowedIPs = make([]netip.Prefix, 0, expected)
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

func (snapshot *peerSnapshot) decode(data []byte) error {
	decoder, err := gennetlink.NewAttributeDecoder(data)
	if err != nil {
		return err
	}
	for decoder.Next() {
		switch decoder.Type() {
		case unix.WGDEVICE_A_PUBLIC_KEY:
			decoder.Do(func(data []byte) error { return readKey(data, &snapshot.self) })
		case unix.WGDEVICE_A_PEERS:
			decoder.Nested(snapshot.decodePeers)
		}
	}
	return decoder.Err()
}

func (snapshot *peerSnapshot) decodePeers(decoder *gennetlink.AttributeDecoder) error {
	for decoder.Next() {
		var peer peers.Peer
		start := len(snapshot.allowedIPs)
		decoder.Nested(func(fields *gennetlink.AttributeDecoder) error {
			for fields.Next() {
				switch fields.Type() {
				case unix.WGPEER_A_PUBLIC_KEY:
					fields.Do(func(data []byte) error { return readKey(data, &peer.PublicKey) })
				case unix.WGPEER_A_PRESHARED_KEY:
					fields.Do(func(data []byte) error { return readKey(data, &peer.PresharedKey) })
				case unix.WGPEER_A_ALLOWEDIPS:
					fields.Nested(func(prefixes *gennetlink.AttributeDecoder) error {
						for prefixes.Next() {
							if len(snapshot.allowedIPs) == maxSnapshotIPs {
								return errPeerSnapshot
							}
							prefixes.Nested(func(fields *gennetlink.AttributeDecoder) error {
								prefix, err := decodeAllowedIP(fields)
								if err == nil {
									snapshot.allowedIPs = append(snapshot.allowedIPs, prefix)
								}
								return err
							})
						}
						return prefixes.Err()
					})
				}
			}
			return fields.Err()
		})
		if err := decoder.Err(); err != nil {
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
	return decoder.Err()
}

func readKey(data []byte, key *[32]byte) error {
	if len(data) != len(key) {
		return errPeerSnapshot
	}
	*key = [32]byte(data)
	return nil
}

func decodeAllowedIP(fields *gennetlink.AttributeDecoder) (netip.Prefix, error) {
	var address netip.Addr
	bits := -1
	for fields.Next() {
		switch fields.Type() {
		case unix.WGALLOWEDIP_A_IPADDR:
			fields.Do(func(data []byte) error {
				address, _ = netip.AddrFromSlice(data)
				return nil
			})
		case unix.WGALLOWEDIP_A_CIDR_MASK:
			bits = int(fields.Uint8())
		}
	}
	if err := fields.Err(); err != nil {
		return netip.Prefix{}, err
	}
	prefix := netip.PrefixFrom(address, bits)
	if !prefix.IsValid() {
		return netip.Prefix{}, errPeerSnapshot
	}
	return prefix.Masked(), nil
}

func (control *Control) writePeers(ctx context.Context, index int, changes []peers.Change) error {
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
		if err := control.prepareWireGuard(ctx); err != nil {
			return err
		}
		encoder := gennetlink.NewAttributeEncoder()
		encoder.Uint32(unix.WGDEVICE_A_IFINDEX, uint32(index))
		encoder.Nested(unix.WGDEVICE_A_PEERS, func(list *gennetlink.AttributeEncoder) error {
			for i := range changes[:end] {
				change := &changes[i]
				list.Nested(uint16(i), func(peer *gennetlink.AttributeEncoder) error {
					encodePeerChange(peer, change)
					return nil
				})
			}
			return nil
		})
		data, err := encoder.Encode()
		if err != nil {
			return err
		}
		err = control.setWireGuard(data)
		clear(data)
		if err != nil {
			return err
		}
		changes = changes[end:]
	}
	return nil
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
