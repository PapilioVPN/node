package native

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
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

func (reader *PeerStatsReader) ReadPeerStats(ctx context.Context, name string) (_ []peers.Stats, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !reader.mu.TryLock() {
		return nil, status.ErrBusy
	}
	defer reader.mu.Unlock()
	if reader.closed {
		return nil, status.ErrUnavailable
	}
	defer func() {
		if err != nil {
			_ = reader.closeDump()
		}
	}()
	if reader.dump == nil {
		reader.dump, err = openPeerDump()
		if err != nil {
			return nil, err
		}
		if err := reader.dump.SetDeadline(peerDeadline(ctx)); err != nil {
			return nil, err
		}
		reader.family, err = reader.dump.GetFamily("wireguard")
		if err != nil {
			return nil, peerStatsError(err)
		}
	}
	encoder := gennetlink.NewAttributeEncoder()
	encoder.String(unix.WGDEVICE_A_IFNAME, name)
	data, err := encoder.Encode()
	if err != nil {
		return nil, err
	}
	snapshot := peerStatsSnapshot{name: name, peers: make([]peers.Stats, 0, reader.dump.peerCount)}
	if err := reader.dump.query(ctx, reader.family, data, snapshot.decode); err != nil {
		return nil, peerStatsError(err)
	}
	if !snapshot.seenName {
		return nil, errPeerSnapshot
	}
	reader.dump.peerCount = len(snapshot.peers)
	return snapshot.peers, nil
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
	name     string
	seenName bool
	peers    []peers.Stats
}

func (snapshot *peerStatsSnapshot) decode(data []byte) error {
	attributes := snapshotAttributes{remaining: data}
	for attributes.next() {
		switch attributes.kind {
		case unix.WGDEVICE_A_IFNAME:
			value := attributes.value
			if len(value) != len(snapshot.name)+1 || value[len(value)-1] != 0 || string(value[:len(value)-1]) != snapshot.name {
				return errPeerSnapshot
			}
			snapshot.seenName = true
		case unix.WGDEVICE_A_PEERS:
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
	var peer peers.Stats
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
