package lifecycle

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
)

func TestPeerDeltaChunksReleaseAdmissionAndRejectIncompleteInput(t *testing.T) {
	control := &peerControlFixture{snapshot: peers.Snapshot{PublicKey: [32]byte{9}}}
	service := New("wg0", nil, control, nil)
	service.applied.Store(&Checkpoint{Link: Link{Name: "wg0", Index: 3, Alias: "fixture"}})
	key := [32]byte{1}
	chunk := PeerDeltaChunk{Name: "wg0", Last: true, DeltaInput: peers.DeltaInput{
		Upsert: []peers.Input{{PublicKey: key[:], AllowedIPs: []string{"192.0.2.1/32"}}},
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sent := false
	_, err := service.ApplyPeerDeltaChunks(ctx, func() (PeerDeltaChunk, error) {
		if sent {
			cancel()
			return PeerDeltaChunk{}, io.EOF
		}
		_, err := service.ApplyPeerDeltaChunks(t.Context(), func() (PeerDeltaChunk, error) {
			t.Fatal("contending stream reached its reader")
			return PeerDeltaChunk{}, io.EOF
		})
		if !errors.Is(err, ErrPeerDeltaStreamBusy) {
			t.Fatalf("contending stream returned %v", err)
		}
		sent = true
		return chunk, nil
	})
	if !errors.Is(err, context.Canceled) || control.reads != 0 || control.writes != 0 {
		t.Fatalf("canceled stream returned %v with %d reads and %d writes", err, control.reads, control.writes)
	}

	oversized := chunk
	oversized.Remove = make([][]byte, peers.MaxPeers)
	if _, err := service.ApplyPeerDeltaChunks(t.Context(), func() (PeerDeltaChunk, error) {
		return oversized, nil
	}); !errors.Is(err, ErrPeerDeltaStreamSize) || control.writes != 0 {
		t.Fatalf("oversized stream returned %v with %d writes", err, control.writes)
	}

	sent = false
	result, err := service.ApplyPeerDeltaChunks(t.Context(), func() (PeerDeltaChunk, error) {
		if sent {
			return PeerDeltaChunk{}, io.EOF
		}
		sent = true
		return chunk, nil
	})
	if err != nil || result != (peers.Result{Added: 1}) || control.writes != 1 {
		t.Fatalf("complete stream returned %+v, %v with %d writes", result, err, control.writes)
	}
}
