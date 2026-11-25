package http

import (
	"bufio"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
)

func peerDeltaChunks(managed *lifecycle.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/x-ndjson" {
			if r.ProtoMajor == 1 {
				w.Header().Set("Connection", "close")
			}
			http.Error(w, "wireguard peer chunks require application/x-ndjson", http.StatusUnsupportedMediaType)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), lifecycle.PeerDeltaStreamTimeout)
		defer cancel()
		deadline, _ := ctx.Deadline()
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(deadline); err != nil {
			writeError(w, err)
			return
		}
		stream := bufio.NewScanner(http.MaxBytesReader(w, r.Body, lifecycle.MaxConfigBytes))
		stream.Buffer(nil, lifecycle.MaxConfigBytes+1)
		name := interfaceName(r)
		result, err := managed.ApplyPeerDeltaChunks(ctx, func() (lifecycle.PeerDeltaChunk, error) {
			return readPeerChunk(stream, name)
		})
		if err == nil {
			_ = controller.SetReadDeadline(time.Time{})
		} else if r.ProtoMajor == 1 {
			// The request can still have unread chunks. Do not reuse its connection.
			w.Header().Set("Connection", "close")
		}
		switch {
		case errors.Is(err, lifecycle.ErrPeerDeltaStreamSize):
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		case errors.Is(err, lifecycle.ErrPeerDeltaStreamBusy):
			http.Error(w, err.Error(), http.StatusTooManyRequests)
		default:
			writePeerDelta(w, result, err)
		}
	}
}

func readPeerChunk(stream *bufio.Scanner, name string) (lifecycle.PeerDeltaChunk, error) {
	scanned := stream.Scan()
	var tooLarge *http.MaxBytesError
	err := stream.Err()
	switch {
	case errors.As(err, &tooLarge):
		return lifecycle.PeerDeltaChunk{}, lifecycle.ErrPeerDeltaStreamSize
	case errors.Is(err, os.ErrDeadlineExceeded):
		return lifecycle.PeerDeltaChunk{}, context.DeadlineExceeded
	case err != nil:
		return lifecycle.PeerDeltaChunk{}, peers.ErrInvalid
	case !scanned:
		return lifecycle.PeerDeltaChunk{}, io.EOF
	}
	var frame struct {
		Index *uint32 `json:"index"`
		Last  bool    `json:"last"`
		peers.DeltaInput
	}
	if err := jsonv2.Unmarshal(stream.Bytes(), &frame, json.DefaultOptionsV1(), jsonv2.RejectUnknownMembers(true)); err != nil || frame.Index == nil {
		return lifecycle.PeerDeltaChunk{}, peers.ErrInvalid
	}
	return lifecycle.PeerDeltaChunk{Index: *frame.Index, Last: frame.Last, Name: name, DeltaInput: frame.DeltaInput}, nil
}
