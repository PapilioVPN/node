package native

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDecodeEndpointIP(t *testing.T) {
	v6 := make([]byte, unix.SizeofSockaddrInet6)
	binary.NativeEndian.PutUint16(v6, unix.AF_INET6)
	want := netip.MustParseAddr("2001:db8::42")
	address := want.As16()
	copy(v6[8:24], address[:])
	if got, err := decodeEndpointIP(v6); err != nil || got != want {
		t.Fatalf("endpoint = %v, %v", got, err)
	}
	if _, err := decodeEndpointIP(v6[:8]); err == nil {
		t.Fatal("truncated sockaddr was accepted")
	}
}
