package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"syscall"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// peerLinkSocket is serialized by the lifecycle owner. Unlike a dump, a
// targeted RTM_GETLINK has one reply, so there is no probe or separate ACK.
type peerLinkSocket struct {
	*netlink.Conn
	raw    syscall.RawConn
	buffer [8 << 10]byte
}

func openPeerLink() (*peerLinkSocket, error) {
	conn, err := netlink.Dial(unix.NETLINK_ROUTE, nil)
	if err != nil {
		return nil, err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return &peerLinkSocket{Conn: conn, raw: raw}, nil
}

func (socket *peerLinkSocket) check(ctx context.Context, owner lifecycle.Link) error {
	if err := socket.SetDeadline(wireGuardDeadline(ctx)); err != nil {
		return err
	}
	const skipStats = 1 << 3 // RTEXT_FILTER_SKIP_STATS in linux/rtnetlink.h.
	var body [unix.SizeofIfInfomsg + 8]byte
	binary.NativeEndian.PutUint32(body[4:], uint32(owner.Index))
	attr := body[unix.SizeofIfInfomsg:]
	binary.NativeEndian.PutUint16(attr, uint16(len(attr)))
	binary.NativeEndian.PutUint16(attr[2:], unix.IFLA_EXT_MASK)
	binary.NativeEndian.PutUint32(attr[4:], skipStats)
	request, err := socket.Send(netlink.Message{Header: netlink.Header{Type: unix.RTM_GETLINK, Flags: netlink.Request}, Data: body[:]})
	if err != nil {
		return err
	}
	var n int
	var sender unix.Sockaddr
	var readErr error
	if err := socket.raw.Read(func(fd uintptr) bool {
		for {
			n, sender, readErr = unix.Recvfrom(int(fd), socket.buffer[:], unix.MSG_TRUNC)
			if readErr != unix.EINTR {
				return readErr != unix.EAGAIN
			}
		}
	}); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	address, ok := sender.(*unix.SockaddrNetlink)
	if !ok || address.Pid != 0 || address.Groups != 0 || n <= 0 || n > len(socket.buffer) {
		return status.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return checkPeerLinkReceipt(socket.buffer[:n], request.Header, owner)
}

func checkPeerLinkReceipt(data []byte, request netlink.Header, owner lifecycle.Link) error {
	if len(data) < unix.NLMSG_HDRLEN || int(binary.NativeEndian.Uint32(data)) != len(data) ||
		binary.NativeEndian.Uint32(data[8:]) != request.Sequence || binary.NativeEndian.Uint32(data[12:]) != request.PID {
		return status.ErrUnavailable
	}
	kind := binary.NativeEndian.Uint16(data[4:])
	body := data[unix.NLMSG_HDRLEN:]
	if kind == unix.NLMSG_ERROR && len(body) >= 4 {
		code := int32(binary.NativeEndian.Uint32(body))
		if code == -int32(unix.ENODEV) {
			return status.ErrNotFound
		}
		if code < 0 {
			return unix.Errno(-int64(code))
		}
	}
	if kind != unix.RTM_NEWLINK || len(body) < unix.SizeofIfInfomsg || int(binary.NativeEndian.Uint32(body[4:])) != owner.Index {
		return status.ErrUnavailable
	}
	var name, alias, linkKind []byte
	fields := snapshotAttributes{remaining: body[unix.SizeofIfInfomsg:]}
	for fields.next() {
		switch fields.kind {
		case unix.IFLA_IFNAME:
			name = fields.value
		case unix.IFLA_IFALIAS:
			alias = fields.value
		case unix.IFLA_LINKINFO:
			info := snapshotAttributes{remaining: fields.value}
			for info.next() {
				if info.kind == unix.IFLA_INFO_KIND {
					linkKind = info.value
				}
			}
			if info.err != nil {
				return status.ErrUnavailable
			}
		}
	}
	if fields.err != nil {
		return status.ErrUnavailable
	}
	if !bytes.Equal(bytes.TrimRight(alias, "\x00"), []byte(owner.Alias)) || !bytes.Equal(linkKind, []byte("wireguard\x00")) {
		return lifecycle.ErrConflict
	}
	if !bytes.Equal(bytes.TrimRight(name, "\x00"), []byte(owner.Name)) || binary.NativeEndian.Uint32(body[8:])&unix.IFF_UP == 0 {
		return status.ErrUnavailable
	}
	return nil
}
