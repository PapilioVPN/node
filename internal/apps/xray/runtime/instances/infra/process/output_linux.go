//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"golang.org/x/sys/unix"
)

// Console output is a sealed, kernel-bounded prefix, not a pipe that can deliver
// SIGPIPE after Node dies. At capacity writes fail without growing a disk file;
// Logs must expose capacity loss explicitly, never claim a complete tail.
func newOutput() (*os.File, error) {
	fd, err := unix.MemfdCreate("xray-console", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "xray-console")
	if err := file.Truncate(domain.MaxOutputBytes); err != nil {
		file.Close()
		return nil, err
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
func openOutput(r executionRecord) (*os.File, error) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/fd/1", r.PID))
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	var st unix.Stat_t
	seals, sealErr := unix.FcntlInt(file.Fd(), unix.F_GET_SEALS, 0)
	if unix.Fstat(int(file.Fd()), &st) != nil || uint64(st.Dev) != r.OutputDevice || st.Ino != r.OutputInode || info.Size() != domain.MaxOutputBytes || sealErr != nil || seals&(unix.F_SEAL_GROW|unix.F_SEAL_SHRINK) != (unix.F_SEAL_GROW|unix.F_SEAL_SHRINK) {
		file.Close()
		return nil, domain.ErrOwnership
	}
	return file, nil
}
func (p *Process) ReadOutput(ctx context.Context, request domain.OutputRequest) (domain.OutputPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.OutputPage{}, err
	}
	if request.Limit > domain.MaxOutputRead || request.Offset > domain.MaxOutputBytes {
		return domain.OutputPage{}, domain.ErrInvalidArgument
	}
	p.mu.Lock()
	run := p.lastOutput
	snapshot := p.snapshot
	p.mu.Unlock()
	if run == nil || run.output == nil {
		return domain.OutputPage{}, domain.ErrUnsupported
	}
	if request.ExecutionID != "" && request.ExecutionID != snapshot.ExecutionID {
		return domain.OutputPage{}, domain.ErrExecutionChanged
	}
	var position uint64
	var err error
	if run.adopted {
		position, err = outputPosition(run.pid)
		if err == nil {
			run.position.Store(min(position, domain.MaxOutputBytes))
		}
	} else {
		var pos int64
		pos, err = run.output.Seek(0, io.SeekCurrent)
		position = uint64(max(0, pos))
	}
	if err != nil {
		if snapshot.State == domain.Running {
			return domain.OutputPage{}, err
		}
		position = run.finalOutput
	}
	position = min(position, domain.MaxOutputBytes)
	if request.Offset > position {
		return domain.OutputPage{}, domain.ErrInvalidArgument
	}
	limit := request.Limit
	if limit == 0 {
		limit = domain.MaxOutputRead
	}
	length := min(uint64(limit), position-request.Offset)
	data := make([]byte, length)
	n, err := run.output.ReadAt(data, int64(request.Offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return domain.OutputPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.OutputPage{}, err
	}
	return domain.OutputPage{ExecutionID: snapshot.ExecutionID, Generation: snapshot.Generation, Offset: request.Offset, NextOffset: request.Offset + uint64(n), Data: data[:n], CapacityReached: position == domain.MaxOutputBytes, PositionUncertain: run.adopted && snapshot.State != domain.Running, Reattached: run.adopted, Exited: snapshot.State != domain.Running, ObservedAt: time.Now().UTC()}, nil
}

func closeOutput(f *os.File) error {
	err := f.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
