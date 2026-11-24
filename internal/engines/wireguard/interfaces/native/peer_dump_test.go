package native

import (
	"encoding/binary"
	"errors"
	"testing"

	gennetlink "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

func TestPeerDumpChecksCompletion(t *testing.T) {
	request := gennetlink.Header{Type: 45, Sequence: 3, PID: 72}
	message := func(kind gennetlink.HeaderType, flags gennetlink.HeaderFlags, sequence uint32, payload []byte) []byte {
		t.Helper()
		data, err := (gennetlink.Message{Header: gennetlink.Header{
			Type: kind, Flags: flags, Sequence: sequence, PID: request.PID, Length: uint32(unix.NLMSG_HDRLEN + len(payload)),
		}, Data: payload}).MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	encoder := gennetlink.NewAttributeEncoder()
	key := [32]byte{1}
	encoder.Bytes(unix.WGDEVICE_A_PUBLIC_KEY, key[:])
	attrs, err := encoder.Encode()
	if err != nil {
		t.Fatal(err)
	}
	device := message(request.Type, gennetlink.Multi, request.Sequence, append([]byte{unix.WG_CMD_GET_DEVICE, 1, 0, 0}, attrs...))
	done := message(gennetlink.Done, gennetlink.Multi, request.Sequence, make([]byte, 4))
	failure := -int32(unix.ENODEV)
	status := binary.NativeEndian.AppendUint32(nil, uint32(failure))
	for _, tc := range []struct {
		name string
		tail []byte
		want error
	}{
		{"complete", done, nil},
		{"interrupted at completion", message(gennetlink.Done, gennetlink.Multi|gennetlink.DumpInterrupted, request.Sequence, make([]byte, 4)), errPeerSnapshot},
		{"kernel errno", message(gennetlink.Done, gennetlink.Multi, request.Sequence, status), unix.ENODEV},
		{"wrong sequence", message(gennetlink.Done, gennetlink.Multi, request.Sequence+1, make([]byte, 4)), errPeerSnapshot},
		{"truncated completion", done[:len(done)-1], errPeerSnapshot},
		{"trailing bytes", append(append([]byte{}, done...), 0), errPeerSnapshot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var snapshot peerSnapshot
			completed, err := snapshot.decodeDatagram(request, append(append([]byte{}, device...), tc.tail...))
			if !errors.Is(err, tc.want) || completed != (tc.want == nil) {
				t.Fatalf("completion = %v, error = %v, want %v", completed, err, tc.want)
			}
			if completed && snapshot.self != key {
				t.Fatal("completion lost the device public key")
			}
		})
	}
}
