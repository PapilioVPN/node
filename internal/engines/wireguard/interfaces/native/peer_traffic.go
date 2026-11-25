package native

import (
	"encoding/binary"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"golang.org/x/sys/unix"
)

func (snapshot *peerStatsSnapshot) selectTraffic(data []byte) error {
	list := snapshotAttributes{remaining: data}
	for list.next() {
		fields := snapshotAttributes{remaining: list.value}
		for fields.next() {
			if fields.kind != unix.WGPEER_A_PUBLIC_KEY {
				continue
			}
			var key [32]byte
			if err := readKey(fields.value, &key); err != nil {
				return err
			}
			if key != snapshot.target {
				break
			}
			if snapshot.targetStats {
				if err := snapshot.appendPeer(list.value); err != nil {
					return err
				}
				snapshot.found = true
				return nil
			}
			counters, err := decodePeerTraffic(list.value)
			if err != nil {
				return err
			}
			snapshot.sample.Counters, snapshot.found = counters, true
			return nil
		}
		if fields.err != nil {
			return fields.err
		}
	}
	return list.err
}

func decodePeerTraffic(data []byte) (traffic.Counters, error) {
	var counters traffic.Counters
	var rx, tx bool
	fields := snapshotAttributes{remaining: data}
	for fields.next() {
		switch fields.kind {
		case unix.WGPEER_A_RX_BYTES:
			if len(fields.value) != 8 || rx {
				return traffic.Counters{}, errPeerSnapshot
			}
			counters.RXBytes, rx = binary.NativeEndian.Uint64(fields.value), true
		case unix.WGPEER_A_TX_BYTES:
			if len(fields.value) != 8 || tx {
				return traffic.Counters{}, errPeerSnapshot
			}
			counters.TXBytes, tx = binary.NativeEndian.Uint64(fields.value), true
		}
	}
	if fields.err != nil || !rx || !tx {
		return traffic.Counters{}, errPeerSnapshot
	}
	return counters, nil
}
