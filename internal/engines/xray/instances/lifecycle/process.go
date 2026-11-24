package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
)

type process struct {
	command       *exec.Cmd
	socketPath    string
	engineVersion string
	executable    os.FileInfo
	exited        chan struct{}
	exit          processExit
}

type processExit struct {
	err        error
	cleanupErr error
	code       int
}

// The lifecycle control gate serializes calls to a launcher.
type processLauncher struct {
	identity os.FileInfo
	version  string
}

func (launcher *processLauncher) start(ctx context.Context, config Config, source []byte, logs *live.Execution) (child, error) {
	run, err := runconfig.PrepareSource(source, config.RuntimeDirectory)
	if err != nil {
		return nil, err
	}

	identity, _ := os.Stat(config.Executable)
	version := launcher.readVersion(ctx, config.Executable, identity)
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, run.Cleanup())
	}
	cmd := exec.Command(config.Executable, run.Args...)
	cmd.Stdin = bytes.NewReader(run.Overlay)
	stdout := &outputWriter{run: logs, source: live.Stdout}
	stderr := &outputWriter{run: logs, source: live.Stderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Bound the join if a descendant inherits an output pipe.
	cmd.WaitDelay = 250 * time.Millisecond
	// Linux sends the signal when the creating thread dies. Keep that thread
	// alive until Wait returns so the runtime cannot end it during normal use.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	p := &process{command: cmd, socketPath: run.Socket, engineVersion: version, executable: identity, exited: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- errors.Join(err, run.Cleanup())
			return
		}
		started <- nil
		waitErr := cmd.Wait()
		stdout.finish()
		stderr.finish()
		p.exit = processExit{err: waitErr, cleanupErr: run.Cleanup(), code: cmd.ProcessState.ExitCode()}
		close(p.exited)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return p, nil
}

func (process *process) socket() string { return process.socketPath }

func (process *process) version() string { return process.engineVersion }

func (process *process) executableMatches(path string) bool {
	current, err := os.Stat(path)
	return err == nil && sameExecutable(process.executable, current)
}

func (process *process) wait() processExit {
	<-process.exited
	return process.exit
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
