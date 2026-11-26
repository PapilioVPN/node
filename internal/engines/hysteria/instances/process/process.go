package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	instances "github.com/PapilioVPN/node/internal/engines/hysteria/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
	logs "github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
	statsnative "github.com/PapilioVPN/node/internal/engines/hysteria/stats/native"
	stats "github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
)

const (
	Version     = "v2.14.0"
	Revision    = "c2b181e0b0e72b077e4b0b7297735af14d1f9929"
	signalGrace = 750 * time.Millisecond
)

func (c *candidate) Launch(ctx context.Context, sequence uint64) (instances.Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.backend.checkVersion(ctx); err != nil {
		return nil, err
	}
	path, err := c.writeSource()
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lifetime, cancel := context.WithCancel(context.Background())
	journal := logs.NewJournal(lifetime, sequence)
	output := &events{ready: make(chan struct{}), journal: journal}
	cmd := exec.Command(c.backend.Executable, "server", "--config", path, "--disable-update-check", "--log-format", "json", "--log-level", "info")
	cmd.Dir = filepath.Dir(c.backend.ConfigFile)
	cmd.Env = []string{}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.WaitDelay = signalGrace
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fault.ErrUnavailable
	}
	run := &process{child: cmd.Process, done: make(chan struct{}), lifetime: lifetime, cancel: cancel, journal: journal}
	go func() {
		_ = cmd.Wait()
		cancel()
		run.mu.Lock()
		if run.api != nil {
			run.api.Close()
		}
		run.mu.Unlock()
		close(run.done)
	}()

	if err := run.ready(ctx, output, c); err != nil {
		run.Stop(ctx)
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		run.Stop(ctx)
		return nil, fault.ErrUnavailable
	}
	return run, nil
}

func (c *candidate) writeSource() (string, error) {
	f, err := os.CreateTemp(c.backend.RuntimeDirectory, "hysteria-*.yaml")
	if err != nil {
		return "", fault.ErrUnavailable
	}
	path := f.Name()
	_, err = f.Write(c.source)
	if errors.Join(err, f.Close()) != nil {
		_ = os.Remove(path)
		return "", fault.ErrUnavailable
	}
	return path, nil
}

func (b Backend) checkVersion(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, b.Executable, "version")
	cmd.Env = []string{}
	cmd.WaitDelay = signalGrace
	var out cappedOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fault.ErrUnavailable
	}
	version, revision := false, false
	for line := range strings.SplitSeq(string(out.bytes), "\n") {
		switch strings.TrimSpace(line) {
		case "Version:\t" + Version:
			version = true
		case "CommitHash:\t" + Revision:
			revision = true
		}
	}
	if !version || !revision || out.truncated {
		return fault.ErrUnavailable
	}
	return ctx.Err()
}

type cappedOutput struct {
	bytes     []byte
	truncated bool
}

func (o *cappedOutput) Write(b []byte) (int, error) {
	if len(o.bytes)+len(b) > 4096 {
		o.truncated = true
	}
	o.bytes = append(o.bytes, b[:min(len(b), 4096-len(o.bytes))]...)
	return len(b), nil
}

type process struct {
	child    *os.Process
	done     chan struct{}
	stop     sync.Once
	lifetime context.Context
	cancel   context.CancelFunc
	journal  *logs.Journal
	mu       sync.Mutex
	api      *statsnative.Client
}

func (*process) Version() string            { return Version }
func (p *process) Done() <-chan struct{}    { return p.done }
func (p *process) Context() context.Context { return p.lifetime }
func (p *process) Logs() *logs.Journal      { return p.journal }
func (p *process) Stats() stats.Native {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.api
}

func (p *process) Stop(ctx context.Context) {
	p.stop.Do(func() {
		p.cancel()
		select {
		case <-p.done:
			return
		default:
		}
		_ = p.child.Signal(syscall.SIGTERM)
		timer := time.NewTimer(signalGrace)
		defer timer.Stop()
		select {
		case <-p.done:
			return
		case <-ctx.Done():
		case <-timer.C:
		}
		_ = p.child.Kill()
		<-p.done
	})
}

func (p *process) ready(ctx context.Context, output *events, c *candidate) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return fault.ErrUnavailable
	case <-output.ready:
	}

	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		if api, err := statsnative.OpenOwned(ctx, p.child.Pid, c.prepared.StatsAddress, c.prepared.StatsSecret); err == nil {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.lifetime.Err() != nil {
				api.Close()
				return fault.ErrUnavailable
			}
			p.api = api
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fault.ErrUnavailable
		case <-ticker.C:
		}
	}
}

type events struct {
	ready     chan struct{}
	readyOnce sync.Once
	journal   *logs.Journal
	line      []byte
	truncated bool
}

func (e *events) Write(b []byte) (int, error) {
	size := len(b)
	for len(b) > 0 {
		head, tail, complete := bytes.Cut(b, []byte{'\n'})
		if !e.truncated && len(e.line)+len(head) <= 8192 {
			e.line = append(e.line, head...)
		} else {
			e.truncated = true
		}
		if !complete {
			break
		}
		if !e.truncated {
			e.accept(e.line)
		}
		clear(e.line)
		e.line, e.truncated = e.line[:0], false
		b = tail
	}
	return size, nil
}

func (e *events) accept(line []byte) {
	var row struct {
		Message string `json:"msg"`
	}
	if json.Unmarshal(line, &row) != nil {
		return
	}
	switch row.Message {
	case "server up and running":
		e.readyOnce.Do(func() { close(e.ready) })
	case "client connected":
		e.journal.Publish("client_connected")
	case "client disconnected":
		e.journal.Publish("client_disconnected")
	case "TCP error":
		e.journal.Publish("tcp_error")
	case "UDP error":
		e.journal.Publish("udp_error")
	}
}
