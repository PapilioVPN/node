package native

import (
	"context"
	"encoding/binary"
	"errors"
	"syscall"

	"github.com/mdlayher/genetlink"
	gennetlink "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// Linux packs subsequent dump messages into larger datagrams when recvmsg is
// given a larger buffer. This socket is serialized by the lifecycle owner.
type peerDumpSocket struct {
	*genetlink.Conn
	raw         syscall.RawConn
	buffer      [32 << 10]byte
	peerCount   int
	prefixCount int
}

func openPeerDump() (*peerDumpSocket, error) {
	conn, err := genetlink.Dial(nil)
	if err != nil {
		return nil, err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return &peerDumpSocket{Conn: conn, raw: raw}, nil
}

func (dump *peerDumpSocket) query(ctx context.Context, family genetlink.Family, data []byte, decode func([]byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A multipart dump shares the operation deadline across all datagrams.
	if err := dump.SetDeadline(peerDeadline(ctx)); err != nil {
		return err
	}
	request, err := dump.Send(genetlink.Message{
		Header: genetlink.Header{Command: unix.WG_CMD_GET_DEVICE, Version: family.Version},
		Data:   data,
	}, family.ID, gennetlink.Request|gennetlink.Dump)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := dump.receive()
		if err != nil {
			clear(dump.buffer[:])
			return err
		}
		data := dump.buffer[:n]
		done, err := decodePeerDatagram(request.Header, data, decode)
		clear(data)
		if err != nil {
			return err
		}
		if done {
			return ctx.Err()
		}
	}
}

func (dump *peerDumpSocket) receive() (int, error) {
	var n int
	var sender unix.Sockaddr
	var receiveErr error
	err := dump.raw.Read(func(fd uintptr) bool {
		for {
			n, sender, receiveErr = unix.Recvfrom(int(fd), dump.buffer[:], unix.MSG_TRUNC)
			if receiveErr != unix.EINTR {
				return receiveErr != unix.EAGAIN
			}
		}
	})
	if err != nil {
		return 0, err
	}
	if receiveErr != nil {
		return 0, receiveErr
	}
	address, ok := sender.(*unix.SockaddrNetlink)
	if !ok || address.Pid != 0 || address.Groups != 0 || n == 0 || n > len(dump.buffer) {
		return 0, errPeerSnapshot
	}
	return n, nil
}

func decodePeerDatagram(request gennetlink.Header, data []byte, decode func([]byte) error) (bool, error) {
	messages, err := syscall.ParseNetlinkMessage(data)
	if err != nil || len(messages) == 0 {
		return false, errPeerSnapshot
	}
	var size int
	for _, message := range messages {
		size += (int(message.Header.Len) + 3) &^ 3
	}
	if size != len(data) {
		return false, errPeerSnapshot
	}
	for i, message := range messages {
		header := message.Header
		if header.Seq != request.Sequence || header.Pid != request.PID && header.Pid != 0 || header.Flags&unix.NLM_F_DUMP_INTR != 0 {
			return false, errPeerSnapshot
		}
		switch header.Type {
		case unix.NLMSG_DONE, unix.NLMSG_ERROR:
			if len(message.Data) < 4 || i != len(messages)-1 {
				return false, errPeerSnapshot
			}
			status := int32(binary.NativeEndian.Uint32(message.Data))
			if status < 0 {
				return false, unix.Errno(-int64(status))
			}
			if header.Type != unix.NLMSG_DONE || status != 0 {
				return false, errPeerSnapshot
			}
			return true, nil
		default:
			if header.Type != uint16(request.Type) || len(message.Data) < 4 || message.Data[0] != unix.WG_CMD_GET_DEVICE {
				return false, errPeerSnapshot
			}
			if err := decode(message.Data[4:]); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}
