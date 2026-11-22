//go:build linux

// Package process executes a closed set of native Xray credential commands.
package process

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

type Config struct{ Binary, Directory, WorkDirectory string }
type Executor struct {
	mu       sync.Mutex
	slots    chan struct{}
	active   int
	closed   bool
	lifetime context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}
type Runner struct {
	config Config
	owner  *Executor
	slots  chan struct{}
}

func NewExecutor() *Executor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Executor{slots: make(chan struct{}, domain.MaxParallel), lifetime: ctx, cancel: cancel, done: make(chan struct{})}
}
func New(config Config, owner *Executor) (*Runner, error) {
	if strings.ContainsRune(config.Binary, 0) || strings.ContainsRune(config.Directory, 0) || strings.ContainsRune(config.WorkDirectory, 0) || owner == nil || !filepath.IsAbs(config.Binary) || !filepath.IsAbs(config.Directory) || (config.WorkDirectory != "" && !filepath.IsAbs(config.WorkDirectory)) {
		return nil, domain.ErrInvalidArgument
	}
	return &Runner{config: config, owner: owner, slots: make(chan struct{}, domain.MaxInstanceParallel)}, nil
}
func (r *Runner) acquire(parent context.Context) (context.Context, context.CancelFunc, error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	o := r.owner
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil, nil, domain.ErrClosed
	}
	select {
	case o.slots <- struct{}{}:
	default:
		o.mu.Unlock()
		return nil, nil, domain.ErrCapacity
	}
	select {
	case r.slots <- struct{}{}:
	default:
		<-o.slots
		o.mu.Unlock()
		return nil, nil, domain.ErrCapacity
	}
	o.active++
	o.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, domain.ExecutionTimeout)
	joined := make(chan struct{})
	stop := context.AfterFunc(o.lifetime, func() { cancel(); close(joined) })
	return ctx, func() {
		if !stop() {
			<-joined
		}
		cancel()
		o.mu.Lock()
		defer o.mu.Unlock()
		<-r.slots
		<-o.slots
		o.active--
		if o.closed && o.active == 0 {
			close(o.done)
		}
	}, nil
}
func (o *Executor) Close(ctx context.Context) error {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		o.cancel()
		if o.active == 0 {
			close(o.done)
		}
	}
	o.mu.Unlock()
	select {
	case <-o.done:
		return nil
	default:
	}
	select {
	case <-o.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type output struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (o *output) Len() int      { return o.buffer.Len() }
func (o *output) Bytes() []byte { return o.buffer.Bytes() }
func (o *output) Write(p []byte) (int, error) {
	if len(p) > domain.MaxOutputBytes-o.Len() {
		o.overflow = true
		o.cancel()
		return 0, domain.ErrCapacity
	}
	return o.buffer.Write(p)
}

// run never logs argv/stdout/stderr or wraps them in errors. The caller owns
// parsing and clearing stdout. Native credential commands do not spawn children.
func (r *Runner) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := output{cancel: cancel}
	command := exec.CommandContext(ctx, r.config.Binary, args...)
	command.Dir = r.config.Directory
	command.Env = []string{}
	command.Stdout = &stdout
	command.Stderr = io.Discard
	command.WaitDelay = 100 * time.Millisecond
	err := command.Run()
	if stdout.overflow {
		clear(stdout.Bytes())
		return nil, domain.ErrCapacity
	}
	if ctx.Err() != nil {
		clear(stdout.Bytes())
		return nil, ctx.Err()
	}
	if err != nil {
		clear(stdout.Bytes())
		if _, ok := err.(*exec.ExitError); ok {
			return nil, domain.ErrRejected
		}
		return nil, domain.ErrUnavailable
	}
	return stdout.Bytes(), nil
}
