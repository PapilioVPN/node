//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/prometheus/procfs"
	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (uint64, string, error) {
	fs, err := procfs.NewFS("/proc")
	if err != nil {
		return 0, "", err
	}
	p, err := fs.Proc(pid)
	if err != nil {
		return 0, "", err
	}
	s, err := p.Stat()
	return s.Starttime, s.State, err
}
func processToken(pid int, token string) error {
	file, err := os.Open(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10+1))
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return domain.ErrOwnership
	}
	expected := tokenEnvironment + "=" + token
	for _, field := range strings.Split(string(data), "\x00") {
		if field == expected {
			return nil
		}
	}
	return domain.ErrOwnership
}
func pidReady(fd int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	_, err := unix.Poll(fds, 0)
	return fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0, err
}
func (p *Process) restore(ctx context.Context) (bool, error) {
	if p.config.RecoveryDirectory == "" {
		return false, nil
	}
	if p.records == nil {
		store, err := openRecords(p.config.RecoveryDirectory, p.config.ID)
		if err != nil {
			return false, err
		}
		p.records = store
	}
	value := p.records.value
	if value.Generation > p.Observe().Generation {
		p.mu.Lock()
		p.snapshot.Generation = value.Generation
		p.mu.Unlock()
	}
	if value.Pending && value.PID == 0 {
		var err error
		value, err = p.recoverPending(ctx, value)
		if err != nil {
			return false, err
		}

	}
	if value.PID == 0 {
		if value.SourceDirectory != "" {
			return false, p.forget(ctx, value)
		}
		return false, nil
	}
	boot, err := bootIdentity()
	if err != nil {
		return false, err
	}
	if boot != value.BootID {
		return false, p.forget(ctx, value)
	}
	fd, err := unix.PidfdOpen(value.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return false, p.forget(ctx, value)
	}
	if err != nil {
		return false, err
	}
	ready, err := pidReady(fd)
	if err != nil {
		unix.Close(fd)
		return false, err
	}
	if ready {
		unix.Close(fd)
		return false, p.forget(ctx, value)
	}
	ticks, state, err := processIdentity(value.PID)
	if err != nil || state == "Z" || ticks != value.StartTicks {
		unix.Close(fd)
		return false, errors.Join(domain.ErrOwnership, err)
	}
	dev, ino, err := statIdentity(fmt.Sprintf("/proc/%d/exe", value.PID))
	expectedDev, expectedIno, binaryErr := statIdentity(p.config.Binary)
	if err != nil || binaryErr != nil || dev != value.BinaryDevice || ino != value.BinaryInode || dev != expectedDev || ino != expectedIno {
		unix.Close(fd)
		return false, errors.Join(domain.ErrOwnership, err, binaryErr)
	}
	if err := processToken(value.PID, value.Token); err != nil {
		unix.Close(fd)
		return false, errors.Join(domain.ErrOwnership, err)
	}
	var processStat unix.Stat_t
	if err := unix.Stat(fmt.Sprintf("/proc/%d", value.PID), &processStat); err != nil || processStat.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return false, domain.ErrOwnership
	}
	// Verify again after pinning the pidfd and reading the process identity.
	after, _, err := processIdentity(value.PID)
	if err != nil || after != ticks {
		unix.Close(fd)
		return false, domain.ErrOwnership
	}
	output, err := openOutput(value)
	if err != nil {
		unix.Close(fd)
		return false, err
	}
	watch, cancel := context.WithCancel(context.Background())
	run := &execution{pidfd: fd, pid: value.PID, done: make(chan struct{}), watchDone: make(chan struct{}), cancel: cancel, output: output, record: value, adopted: true}
	if value.SourceDirectory != "" {
		r := value
		run.input = &Input{Revision: *value.Revision, Release: func() error { return cleanupSource(context.Background(), p.config.WorkDirectory, r) }}
	}
	if pos, err := outputPosition(value.PID); err == nil {
		run.position.Store(min(pos, domain.MaxOutputBytes))
	}
	p.mu.Lock()
	p.current = run
	p.lastOutput = run
	p.snapshot = domain.Execution{State: domain.Running, Generation: value.Generation, StartedAt: &value.StartedAt, ConfigurationRevision: value.Revision, ExecutionID: value.ExecutionID, IdentityVerified: true, Reattached: true}
	p.mu.Unlock()
	go p.watch(watch, run)
	return true, nil
}
func (p *Process) forget(ctx context.Context, value executionRecord) error {
	if err := cleanupSource(ctx, p.config.WorkDirectory, value); err != nil {
		return errors.Join(domain.ErrCleanupFailed, err)
	}
	value.PID = 0
	value.Pending = false
	value.SourceDirectory = ""
	return p.records.write(value)
}
func outputPosition(pid int) (uint64, error) {
	fs, err := procfs.NewFS("/proc")
	if err != nil {
		return 0, err
	}
	p, err := fs.Proc(pid)
	if err != nil {
		return 0, err
	}
	info, err := p.FDInfo("1")
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(info.Pos, 10, 64)
}
