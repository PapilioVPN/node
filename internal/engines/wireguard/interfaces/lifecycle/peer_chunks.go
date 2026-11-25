package lifecycle

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
)

const PeerDeltaStreamTimeout = 30 * time.Second

var (
	ErrPeerDeltaStreamBusy = errors.New("wireguard delta stream is busy")
	ErrPeerDeltaStreamSize = errors.New("wireguard delta stream exceeds its size limit")
)

type PeerDeltaChunk struct {
	peers.DeltaInput
	Name  string
	Index uint32
	Last  bool
	Bytes int // Encoded length where the transport does not bound the body itself.
}

// ApplyPeerDeltaChunks admits one collector across transports without taking
// ownership of the live link until a complete stream has been validated.
func (service *Service) ApplyPeerDeltaChunks(ctx context.Context, receive func() (PeerDeltaChunk, error)) (peers.Result, error) {
	if !service.collectingPeerDelta.CompareAndSwap(false, true) {
		return peers.Result{}, ErrPeerDeltaStreamBusy
	}
	defer service.collectingPeerDelta.Store(false)
	var input peers.DeltaInput
	var name string
	var next uint32
	var size int
	var last bool
	for {
		chunk, err := receive()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return peers.Result{}, err
		}
		count := len(chunk.Upsert) + len(chunk.Remove)
		if last || chunk.Index != next || chunk.Name == "" || count == 0 {
			return peers.Result{}, peers.ErrInvalid
		}
		if next == 0 {
			name = chunk.Name
		} else if chunk.Name != name {
			return peers.Result{}, peers.ErrInvalid
		}
		if chunk.Bytes > MaxConfigBytes-size || count > peers.MaxPeers-len(input.Upsert)-len(input.Remove) {
			return peers.Result{}, ErrPeerDeltaStreamSize
		}
		size += chunk.Bytes
		input.Upsert = append(input.Upsert, chunk.Upsert...)
		input.Remove = append(input.Remove, chunk.Remove...)
		next++
		last = chunk.Last
	}
	if err := ctx.Err(); err != nil {
		return peers.Result{}, err
	}
	if !last {
		return peers.Result{}, peers.ErrInvalid
	}
	return service.ApplyPeerDelta(ctx, name, input)
}
