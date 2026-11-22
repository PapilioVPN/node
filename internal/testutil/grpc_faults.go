//go:build integration && linux

package testutil

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// LostReplies is a test-only TCP relay to the real engine. It forwards HTTP/2
// connection-control frames while dropping response stream frames, so a request
// is genuinely dispatched/applied but its RPC acknowledgement cannot arrive.
// One connection at a time and frames <=64KiB bound its owned work and buffering.
func LostReplies(t *testing.T, target string) (string, *atomic.Bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	drop := &atomic.Bool{}
	drop.Store(true)
	var mu sync.Mutex
	var activeClient, activeServer net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			backend, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", target)
			if err != nil {
				client.Close()
				if ctx.Err() != nil {
					return
				}
				continue
			}
			mu.Lock()
			if ctx.Err() != nil {
				mu.Unlock()
				client.Close()
				backend.Close()
				return
			}
			activeClient, activeServer = client, backend
			mu.Unlock()
			copied := make(chan struct{})
			go func() { defer close(copied); io.Copy(backend, client) }()
			for {
				var header [9]byte
				if _, err := io.ReadFull(backend, header[:]); err != nil {
					break
				}
				length := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
				if length > 64<<10 {
					t.Error("unexpected native HTTP/2 frame size", length)
					break
				}
				payload := make([]byte, length)
				if _, err := io.ReadFull(backend, payload); err != nil {
					break
				}
				if binary.BigEndian.Uint32(header[5:])&0x7fffffff != 0 && drop.Load() {
					continue
				}
				packet := append(header[:], payload...)
				if n, err := client.Write(packet); err != nil || n != len(packet) {
					break
				}
			}
			client.Close()
			backend.Close()
			<-copied
			mu.Lock()
			activeClient, activeServer = nil, nil
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		cancel()
		listener.Close()
		mu.Lock()
		if activeClient != nil {
			activeClient.Close()
		}
		if activeServer != nil {
			activeServer.Close()
		}
		mu.Unlock()
		<-done
	})
	return listener.Addr().String(), drop
}
