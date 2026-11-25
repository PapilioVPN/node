package native

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/mdlayher/genetlink"
	gennetlink "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// PeerStatsReader owns one bounded, read-only WireGuard dump socket.
type PeerStatsReader struct {
	mu     sync.Mutex
	dump   *peerDumpSocket
	family genetlink.Family
	closed bool
}

func (reader *PeerStatsReader) Close(context.Context) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.closed = true
	return reader.closeDump()
}

func (reader *PeerStatsReader) closeDump() error {
	if reader.dump == nil {
		return nil
	}
	err := reader.dump.Close()
	reader.dump = nil
	return err
}

func (reader *PeerStatsReader) ReadPeerStats(ctx context.Context, name string) (_ []traffic.PeerStats, err error) {
	snapshot := peerStatsSnapshot{name: name}
	if err := reader.readSnapshot(ctx, &snapshot); err != nil {
		return nil, err
	}
	return snapshot.peers, nil
}

func (reader *PeerStatsReader) ReadMembership(ctx context.Context, name string) ([]peers.Member, error) {
	snapshot := peerStatsSnapshot{name: name, membership: &peerSnapshot{publicOnly: true}}
	if err := reader.readSnapshot(ctx, &snapshot); err != nil {
		return nil, err
	}
	result := make([]peers.Member, len(snapshot.membership.peers))
	for i, peer := range snapshot.membership.peers {
		result[i] = peers.Member{PublicKey: peer.PublicKey, AllowedIPs: peer.AllowedIPs}
	}
	return result, nil
}

func (reader *PeerStatsReader) ReadPeerTraffic(ctx context.Context, name string, key [32]byte) (traffic.PeerTrafficSample, error) {
	snapshot := peerStatsSnapshot{name: name, target: key}
	if err := reader.readSnapshot(ctx, &snapshot); err != nil {
		return traffic.PeerTrafficSample{}, err
	}
	if !snapshot.found {
		return traffic.PeerTrafficSample{}, traffic.ErrPeerNotFound
	}
	if snapshot.sample.LinkIndex == 0 {
		return traffic.PeerTrafficSample{}, errPeerSnapshot
	}
	return snapshot.sample, nil
}

func (reader *PeerStatsReader) readSnapshot(ctx context.Context, snapshot *peerStatsSnapshot) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !reader.mu.TryLock() {
		return status.ErrBusy
	}
	defer reader.mu.Unlock()
	if reader.closed {
		return status.ErrUnavailable
	}
	defer func() {
		if err != nil {
			_ = reader.closeDump()
		}
	}()
	if reader.dump == nil {
		reader.dump, err = openPeerDump()
		if err != nil {
			return err
		}
		if err := reader.dump.SetDeadline(peerDeadline(ctx)); err != nil {
			return err
		}
		reader.family, err = reader.dump.GetFamily("wireguard")
		if err != nil {
			return peerStatsError(err)
		}
	}
	encoder := gennetlink.NewAttributeEncoder()
	encoder.String(unix.WGDEVICE_A_IFNAME, snapshot.name)
	data, err := encoder.Encode()
	if err != nil {
		return err
	}
	if snapshot.identity != nil {
		return peerStatsError(reader.dump.query(ctx, reader.family, data, snapshot.identity.decode))
	}
	if snapshot.membership != nil {
		snapshot.membership.peers = make([]peers.Peer, 0, reader.dump.peerCount)
		snapshot.membership.allowedIPs = make([]netip.Prefix, 0, reader.dump.prefixCount)
	} else if snapshot.target == [32]byte{} {
		snapshot.peers = make([]traffic.PeerStats, 0, reader.dump.peerCount)
	}
	if err := reader.dump.query(ctx, reader.family, data, snapshot.decode); err != nil {
		return peerStatsError(err)
	}
	if !snapshot.seenName {
		return errPeerSnapshot
	}
	if snapshot.membership != nil {
		reader.dump.peerCount = len(snapshot.membership.peers)
		reader.dump.prefixCount = len(snapshot.membership.allowedIPs)
	} else if snapshot.target == [32]byte{} {
		reader.dump.peerCount = len(snapshot.peers)
	}
	return nil
}

func peerStatsError(err error) error {
	switch {
	case errors.Is(err, unix.ENODEV), errors.Is(err, unix.EOPNOTSUPP):
		return status.ErrNotFound
	case errors.Is(err, os.ErrDeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return err
	}
}

type peerStatsSnapshot struct {
	name       string
	seenName   bool
	peers      []traffic.PeerStats
	target     [32]byte
	sample     traffic.PeerTrafficSample
	found      bool
	membership *peerSnapshot
	identity   *deviceIdentity
}

func (snapshot *peerStatsSnapshot) decode(data []byte) error {
	attributes := snapshotAttributes{remaining: data}
	for attributes.next() {
		switch attributes.kind {
		case unix.WGDEVICE_A_IFINDEX:
			if len(attributes.value) != 4 {
				return errPeerSnapshot
			}
			snapshot.sample.LinkIndex = binary.NativeEndian.Uint32(attributes.value)
		case unix.WGDEVICE_A_IFNAME:
			value := attributes.value
			if len(value) != len(snapshot.name)+1 || value[len(value)-1] != 0 || string(value[:len(value)-1]) != snapshot.name {
				return errPeerSnapshot
			}
			snapshot.seenName = true
		case unix.WGDEVICE_A_PEERS:
			if snapshot.membership != nil {
				if err := snapshot.membership.decodePeers(attributes.value); err != nil {
					return err
				}
				continue
			}
			if snapshot.target != [32]byte{} {
				if !snapshot.found {
					if err := snapshot.selectTraffic(attributes.value); err != nil {
						return err
					}
				}
				continue
			}
			list := snapshotAttributes{remaining: attributes.value}
			for list.next() {
				if err := snapshot.appendPeer(list.value); err != nil {
					return err
				}
			}
			if list.err != nil {
				return list.err
			}
		}
	}
	return attributes.err
}

func (snapshot *peerStatsSnapshot) appendPeer(data []byte) error {
	const (
		rx = 1 << iota
		tx
		handshake
	)
	var peer traffic.PeerStats
	var fields uint8
	attributes := snapshotAttributes{remaining: data}
	for attributes.next() {
		switch attributes.kind {
		case unix.WGPEER_A_PUBLIC_KEY:
			if err := readKey(attributes.value, &peer.PublicKey); err != nil {
				return err
			}
		case unix.WGPEER_A_RX_BYTES:
			if len(attributes.value) != 8 {
				return errPeerSnapshot
			}
			peer.RXBytes = binary.NativeEndian.Uint64(attributes.value)
			fields |= rx
		case unix.WGPEER_A_TX_BYTES:
			if len(attributes.value) != 8 {
				return errPeerSnapshot
			}
			peer.TXBytes = binary.NativeEndian.Uint64(attributes.value)
			fields |= tx
		case unix.WGPEER_A_LAST_HANDSHAKE_TIME:
			value, err := decodeHandshake(attributes.value)
			if err != nil {
				return err
			}
			peer.LastHandshake = value
			fields |= handshake
		case unix.WGPEER_A_ENDPOINT:
			address, err := decodeEndpointIP(attributes.value)
			if err != nil {
				return err
			}
			peer.EndpointIP = address
		}
	}
	if attributes.err != nil || peer.PublicKey == [32]byte{} {
		return errPeerSnapshot
	}
	// A continuation carries the previous public key and more prefixes.
	if last := len(snapshot.peers) - 1; last >= 0 && snapshot.peers[last].PublicKey == peer.PublicKey {
		if fields != 0 {
			return errPeerSnapshot
		}
		return nil
	}
	if fields != rx|tx|handshake || len(snapshot.peers) == maxSnapshotPeers {
		return errPeerSnapshot
	}
	snapshot.peers = append(snapshot.peers, peer)
	return nil
}

func decodeEndpointIP(data []byte) (netip.Addr, error) {
	if len(data) >= 2 {
		switch binary.NativeEndian.Uint16(data[:2]) {
		case unix.AF_INET:
			if len(data) == unix.SizeofSockaddrInet4 {
				return netip.AddrFrom4([4]byte(data[4:8])), nil
			}
		case unix.AF_INET6:
			if len(data) == unix.SizeofSockaddrInet6 {
				return netip.AddrFrom16([16]byte(data[8:24])).Unmap(), nil
			}
		}
	}
	return netip.Addr{}, errPeerSnapshot
}

func decodeHandshake(data []byte) (time.Time, error) {
	if len(data) != 16 {
		return time.Time{}, errPeerSnapshot
	}
	seconds := int64(binary.NativeEndian.Uint64(data))
	nanos := int64(binary.NativeEndian.Uint64(data[8:]))
	if seconds == 0 && nanos == 0 {
		return time.Time{}, nil
	}
	// Keep the timestamp in the range supported by the public wire formats.
	if seconds < -62135596800 || seconds > 253402300799 || nanos < 0 || nanos >= int64(time.Second) {
		return time.Time{}, errPeerSnapshot
	}
	return time.Unix(seconds, nanos).UTC(), nil
}
