package process

import (
	"context"
	"crypto/ecdh"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func testRunner(t *testing.T) (*Runner, *Executor) {
	t.Helper()
	owner := NewExecutor()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if owner.Close(ctx) != nil {
			t.Error("owner failed to close")
		}
	})
	r, err := New(Config{Binary: "/not-executed", Directory: t.TempDir()}, owner)
	if err != nil {
		t.Fatal(err)
	}
	return r, owner
}
func repeated(n int) []byte { return []byte(strings.Repeat("x", n)) }

func validECHKeys() []byte {
	vector := func(value []byte) []byte {
		out := make([]byte, 2+len(value))
		binary.BigEndian.PutUint16(out, uint16(len(value)))
		copy(out[2:], value)
		return out
	}
	seed := make([]byte, 32)
	key, _ := ecdh.X25519().NewPrivateKey(seed)
	body := []byte{0, 0, 0x20}
	body = append(body, vector(key.PublicKey().Bytes())...)
	body = append(body, vector([]byte{0, 1, 0, 1})...)
	name := []byte("example.test")
	body = append(body, 0, byte(len(name)))
	body = append(body, name...)
	body = append(body, 0, 0)
	config := append([]byte{0xfe, 0x0d}, vector(body)...)
	return append(vector(seed), vector(config)...)
}
