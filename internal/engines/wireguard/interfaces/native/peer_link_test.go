package native

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

func TestPeerLinkReceipt(t *testing.T) {
	owner := lifecycle.Link{Index: 23, Name: "wg-test", Alias: "owned"}
	request := netlink.Header{Sequence: 1, PID: 21}
	for _, tc := range []struct {
		name  string
		value string
		want  error
	}{
		{name: "ready"},
		{name: "down", want: status.ErrUnavailable},
		{name: "renamed", value: "wg-other", want: status.ErrUnavailable},
		{name: "wrong-alias", value: "other", want: lifecycle.ErrConflict},
		{name: "wrong-kind", value: "dummy", want: lifecycle.ErrConflict},
		{name: "wrong-index", want: status.ErrUnavailable},
		{name: "wrong-sequence", want: status.ErrUnavailable},
		{name: "missing", want: status.ErrNotFound},
		{name: "short", want: status.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, alias, kind := owner.Name, owner.Alias, "wireguard"
			header := netlink.Header{Type: unix.RTM_NEWLINK, Sequence: request.Sequence, PID: request.PID}
			info := make([]byte, unix.SizeofIfInfomsg)
			binary.NativeEndian.PutUint32(info[4:], uint32(owner.Index))
			binary.NativeEndian.PutUint32(info[8:], unix.IFF_UP)
			switch tc.name {
			case "down":
				clear(info[8:12])
			case "renamed":
				name = tc.value
			case "wrong-alias":
				alias = tc.value
			case "wrong-kind":
				kind = tc.value
			case "wrong-index":
				info[4]++
			case "wrong-sequence":
				header.Sequence++
			}
			attrs := netlink.NewAttributeEncoder()
			attrs.String(unix.IFLA_IFNAME, name)
			attrs.String(unix.IFLA_IFALIAS, alias)
			attrs.Nested(unix.IFLA_LINKINFO, func(attrs *netlink.AttributeEncoder) error {
				attrs.String(unix.IFLA_INFO_KIND, kind)
				return nil
			})
			encoded, err := attrs.Encode()
			if err != nil {
				t.Fatal(err)
			}
			payload := append(info, encoded...)
			if tc.name == "missing" {
				header.Type = unix.NLMSG_ERROR
				payload = make([]byte, 20)
				code := -int32(unix.ENODEV)
				binary.NativeEndian.PutUint32(payload, uint32(code))
			}
			header.Length = uint32(unix.NLMSG_HDRLEN + len(payload))
			datagram, err := (netlink.Message{Header: header, Data: payload}).MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "short" {
				datagram = bytes.Clone(datagram[:len(datagram)-1])
			}
			if err := checkPeerLinkReceipt(datagram, request, owner); !errors.Is(err, tc.want) {
				t.Fatalf("received %v, want %v", err, tc.want)
			}
		})
	}
}
