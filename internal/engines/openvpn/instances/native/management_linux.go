package native

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const maxReadinessLines = 256

func (p *process) waitReady(ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := p.listener.SetDeadline(deadline); err != nil {
			return err
		}
	}
	interrupt := context.AfterFunc(ctx, func() { _ = p.listener.Close() })
	conn, err := p.listener.AcceptUnix()
	interrupt()
	if err != nil {
		return errors.Join(err, ctx.Err())
	}
	_ = p.listener.Close()

	err = verifyPeer(conn, p.cmd.Process.Pid)
	if err != nil {
		_ = conn.Close()
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = conn.Close()
		return io.EOF
	}
	p.management = conn
	p.mu.Unlock()
	control := newManagement(conn, uuid.NewString())
	lines := bufio.NewScanner(conn)
	p.version, err = confirmReady(ctx, conn, lines, control)
	if err == nil {
		p.control = control
		go control.read(lines, p.done)
	}
	return err
}

func verifyPeer(conn *net.UnixConn, pid int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var readErr error
	if err := raw.Control(func(fd uintptr) {
		cred, readErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	if int(cred.Pid) != pid {
		return errors.New("unexpected OpenVPN management peer")
	}
	return nil
}

func confirmReady(ctx context.Context, conn *net.UnixConn, lines *bufio.Scanner, control *management) (string, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return "", err
		}
	}
	stopped := make(chan struct{})
	interrupt := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
		close(stopped)
	})
	defer func() {
		if !interrupt() {
			<-stopped
		}
		_ = conn.SetDeadline(time.Time{})
	}()
	// OpenVPN emits the CONNECTED state after initializing TUN and data
	// listeners. Subscribe before releasing its management hold.
	if _, err := io.WriteString(conn, "version\nstate on\nlog on\nhold release\n"); err != nil {
		return "", errors.Join(err, ctx.Err())
	}
	var version string
	for n := 0; n < maxReadinessLines && lines.Scan(); n++ {
		line := lines.Text()
		if strings.HasPrefix(line, "ERROR:") || strings.HasPrefix(line, ">FATAL:") {
			return "", errors.New("OpenVPN rejected management startup")
		}
		if release, ok := strings.CutPrefix(line, "OpenVPN Version: OpenVPN "); ok {
			version, _, _ = strings.Cut(release, " ")
		}
		if state, ok := strings.CutPrefix(line, ">STATE:"); ok {
			_, fields, _ := strings.Cut(state, ",")
			if strings.HasPrefix(fields, "CONNECTED,SUCCESS,") {
				return version, nil
			}
		}
		control.notification(line)
	}
	return "", errors.Join(errors.New("no confirmed OpenVPN management readiness"), lines.Err(), ctx.Err())
}
