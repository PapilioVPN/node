package lifecycle

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
)

type process struct {
	command    *exec.Cmd
	socketPath string
	exited     chan struct{}
	cleanupErr error
}

func startProcess(config Config) (child, error) {
	run, err := runconfig.Prepare(config.ConfigFile, config.RuntimeDirectory)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(config.Executable, run.Args...)
	cmd.Stdin = bytes.NewReader(run.Overlay)
	// Linux sends the signal when the creating thread dies. Keep that thread
	// alive until Wait returns so the runtime cannot end it during normal use.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	p := &process{command: cmd, socketPath: run.Socket, exited: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- errors.Join(err, run.Cleanup())
			return
		}
		started <- nil
		// Both clean exit and signal termination count as a reaped process.
		_ = cmd.Wait()
		p.cleanupErr = run.Cleanup()
		close(p.exited)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return p, nil
}

func (process *process) socket() string { return process.socketPath }

func (process *process) wait() error {
	<-process.exited
	return process.cleanupErr
}

func (process *process) terminate() error {
	return ignoreDone(process.command.Process.Signal(syscall.SIGTERM))
}

func (process *process) kill() error { return ignoreDone(process.command.Process.Kill()) }

func ignoreDone(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
