package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

type process struct {
	command    *exec.Cmd
	socketPath string
	exited     chan struct{}
	cleanupErr error
}

func startProcess(config Config) (child, error) {
	dir, err := os.MkdirTemp(config.RuntimeDirectory, "node-xray-")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(dir, "api.sock")
	// Linux sockaddr_un has 108 bytes including the terminating NUL.
	if len(socket) >= 108 {
		return nil, errors.Join(errors.New("xray readiness socket path is too long"), os.RemoveAll(dir))
	}

	// Xray applies the last API block when merging configs. The private listener
	// belongs to this particular execution and exposes the readiness method.
	var overlay struct {
		API struct {
			Tag      string   `json:"tag"`
			Listen   string   `json:"listen"`
			Services []string `json:"services"`
		} `json:"api"`
	}
	overlay.API.Tag = "papiliovpn-node-api"
	overlay.API.Listen = socket
	overlay.API.Services = []string{"StatsService"}
	data, err := json.Marshal(overlay)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}

	cmd := exec.Command(config.Executable, "run", "-format", "json", "-c", config.ConfigFile, "-c", "stdin:")
	cmd.Stdin = bytes.NewReader(data)
	// Linux sends the signal when the creating thread dies. Keep that thread
	// alive until Wait returns so the runtime cannot end it during normal use.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	p := &process{command: cmd, socketPath: socket, exited: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- errors.Join(err, os.RemoveAll(dir))
			return
		}
		started <- nil
		// Both clean exit and signal termination count as a reaped process.
		_ = cmd.Wait()
		p.cleanupErr = os.RemoveAll(dir)
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
