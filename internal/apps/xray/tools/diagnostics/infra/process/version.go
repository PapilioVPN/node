// Package process inspects only the configured executable's fixed version command.
package process

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

// Reader borrows a process-wide two-child admission channel. No shell, inherited
// environment, configuration content or arbitrary arguments enter the command.
type Reader struct {
	Binary string
	Slots  chan struct{}
}

func (r Reader) Read(ctx context.Context) (domain.Binary, error) {
	if err := ctx.Err(); err != nil {
		return domain.Binary{}, err
	}
	select {
	case r.Slots <- struct{}{}:
	default:
		return domain.Binary{}, domain.ErrCapacity
	}
	defer func() { <-r.Slots }()
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	output := &boundedOutput{cancel: cancel}
	cmd := exec.CommandContext(ctx, r.Binary, "version")
	cmd.Env = []string{}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if output.exceeded {
		return domain.Binary{}, errors.Join(domain.ErrCapacity, err)
	}
	if ctx.Err() != nil {
		return domain.Binary{}, errors.Join(ctx.Err(), err)
	}
	if err != nil {
		return domain.Binary{}, errors.Join(domain.ErrUnavailable, err)
	}
	result, err := parse(output.data)
	if err != nil {
		return domain.Binary{}, err
	}
	at := time.Now().UTC()
	result.Evidence = domain.Evidence{State: "available", Source: "xray.executable.version", ObservedAt: &at}
	result.Scope = "configured_executable"
	return result, nil
}

type boundedOutput struct {
	mu       sync.Mutex
	data     []byte
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > domain.MaxVersionBytes-len(b.data) {
		b.exceeded = true
		b.cancel()
		return 0, domain.ErrCapacity
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

var banner = regexp.MustCompile(`^Xray ([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}) \(Xray, Penetrates Everything\.\) ([a-f0-9]{7,40}|Custom) \((go[0-9]{1,2}\.[0-9]{1,2}(?:\.[0-9]{1,2})?) (linux|darwin|windows|freebsd|openbsd|netbsd|android)/(amd64|arm64|386|arm|mips|mipsle|mips64|mips64le|riscv64|ppc64|ppc64le|s390x|loong64)\)$`)

func parse(data []byte) (domain.Binary, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	fields := banner.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
	if fields == nil {
		return domain.Binary{}, domain.ErrMalformed
	}
	return domain.Binary{Version: fields[1], Build: fields[2], GoVersion: fields[3], OS: fields[4], Architecture: fields[5]}, nil
}
