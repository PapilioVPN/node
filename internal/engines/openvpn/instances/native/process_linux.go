package native

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

type process struct {
	cmd       *exec.Cmd
	listener  *net.UnixListener
	directory string
	done      chan struct{}
	version   string
	control   *management

	mu         sync.Mutex
	closed     bool
	management *net.UnixConn
}

// Start waits for the management state of this child before returning it.
func (d Driver) Start(ctx context.Context, source []byte) (lifecycle.Execution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Revocations != nil {
		if err := d.Revocations.ValidateCurrent(); err != nil {
			return nil, err
		}
	}
	dir, err := os.MkdirTemp(d.RuntimeDirectory, "node-ovpn-")
	if err != nil {
		return nil, errors.Join(lifecycle.ErrStartFailed, err)
	}
	path := filepath.Join(dir, "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, errors.Join(lifecycle.ErrStartFailed, err, os.RemoveAll(dir))
	}
	file := filepath.Join(dir, "server.conf")
	if err := os.WriteFile(file, source, 0600); err != nil {
		return nil, errors.Join(lifecycle.ErrStartFailed, err, listener.Close(), os.RemoveAll(dir))
	}
	args := []string{"--config", file, "--mode", "server", "--management", path, "unix", "--management-client", "--management-hold"}
	if d.Revocations != nil {
		args = append(args, "--ca", d.Revocations.CAFile, "--crl-verify", d.Revocations.Path)
	}
	cmd := exec.Command(d.Executable, args...)
	cmd.Dir = filepath.Dir(d.ConfigFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	run := &process{cmd: cmd, listener: listener, directory: dir, done: make(chan struct{})}
	started := make(chan error, 1)
	go run.startAndWait(started)
	if err := <-started; err != nil {
		<-run.done
		return nil, errors.Join(lifecycle.ErrStartFailed, err)
	}

	err = run.waitReady(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		clean, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return nil, errors.Join(lifecycle.ErrStartFailed, err, run.Stop(clean))
	}
	return run, nil
}

func (p *process) startAndWait(started chan<- error) {
	// Linux associates the parent-death signal with the creating OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := p.cmd.Start()
	started <- err
	if err == nil {
		_ = p.cmd.Wait()
	}
	_ = p.listener.Close()
	p.mu.Lock()
	p.closed = true
	if p.management != nil {
		_ = p.management.Close()
	}
	p.mu.Unlock()
	_ = os.RemoveAll(p.directory)
	close(p.done)
}

func (p *process) Done() <-chan struct{} { return p.done }
func (p *process) Version() string       { return p.version }

func (p *process) Stop(ctx context.Context) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	pid := p.cmd.Process
	if err := pid.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Join(lifecycle.ErrStopFailed, err)
	}
	grace := time.NewTimer(500 * time.Millisecond)
	defer grace.Stop()
	select {
	case <-p.done:
		return nil
	case <-grace.C:
	case <-ctx.Done():
	}
	if err := pid.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Join(lifecycle.ErrStopFailed, err)
	}
	// Confirm reaping even when the requester disconnected mid-stop.
	join := time.NewTimer(time.Second)
	defer join.Stop()
	select {
	case <-p.done:
		return nil
	case <-join.C:
		return lifecycle.ErrStopFailed
	}
}
