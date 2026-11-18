// Package proc adapts native Linux procfs reads in the current PID namespace.
package proc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	"github.com/prometheus/procfs"
	"github.com/tklauser/go-sysconf"
	"golang.org/x/sync/errgroup"
)

const maxInventory = 1 << 20

type Reader struct {
	fs             procfs.FS
	pageSize       uint64
	TicksPerSecond uint64
}

func New() (*Reader, error) {
	fs, err := procfs.NewFS("/proc")
	if err != nil {
		return nil, fmt.Errorf("open process source: %w", err)
	}
	rate, err := sysconf.Sysconf(sysconf.SC_CLK_TCK)
	if err != nil {
		return nil, fmt.Errorf("read process tick rate: %w", err)
	}
	if rate <= 0 {
		return nil, errors.New("invalid process tick rate")
	}
	return &Reader{fs: fs, pageSize: uint64(os.Getpagesize()), TicksPerSecond: uint64(rate)}, nil
}

// Count enumerates directory entries once, without statting each process. Like
// List, it includes only visible process leaders, not individual task threads.
func (reader *Reader) Count(ctx context.Context) (uint64, error) {
	rows, err := reader.inventory(ctx)
	return uint64(len(rows)), err
}

// Read uses a targeted native lookup and one stat record, never a full scan.
func (reader *Reader) Read(ctx context.Context, pid int32) (domain.Process, error) {
	if err := ctx.Err(); err != nil {
		return domain.Process{}, err
	}
	process, err := reader.fs.Proc(int(pid))
	if err != nil {
		return domain.Process{}, errors.Join(ctx.Err(), classify(err))
	}
	stat, err := process.Stat()
	if err != nil {
		return domain.Process{}, errors.Join(ctx.Err(), classify(err))
	}
	if err := ctx.Err(); err != nil {
		return domain.Process{}, err
	}
	return convert(stat, reader.pageSize)
}

// List enumerates once, selects a bounded PID page, then reads one stat per
// selected process. Linux procfs has no bulk process-stat read. Eight concurrent
// reads are joined before returning; no unselected PID is inspected. Ordinary
// proc I/O is synchronous, so cancellation is checked around native reads.
func (reader *Reader) List(ctx context.Context, selection domain.Selection) (domain.Page, error) {
	rows, err := reader.inventory(ctx)
	if err != nil {
		return domain.Page{}, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].PID < rows[j].PID })
	start := sort.Search(len(rows), func(i int) bool { return rows[i].PID > int(selection.AfterPID) })
	end := min(start+int(selection.Limit), len(rows))
	page, err := readPage(ctx, rows[start:end], reader.pageSize, procfs.Proc.Stat)
	if err != nil {
		return domain.Page{}, err
	}
	if end < len(rows) {
		cursor := int32(rows[end-1].PID)
		page.NextAfterPID = &cursor
	}
	return page, nil
}

func (reader *Reader) inventory(ctx context.Context) (procfs.Procs, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := reader.fs.AllProcs()
	if err != nil {
		return nil, errors.Join(ctx.Err(), fmt.Errorf("enumerate processes: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The native SDK materializes directory names before this supported bound.
	if len(rows) > maxInventory {
		return nil, domain.ErrResourceExhausted
	}
	for _, row := range rows {
		if row.PID <= 0 || row.PID > math.MaxInt32 {
			return nil, errors.New("invalid native process identity")
		}
	}
	return rows, nil
}

func readPage(ctx context.Context, rows procfs.Procs, pageSize uint64, stat func(procfs.Proc) (procfs.ProcStat, error)) (domain.Page, error) {
	page := domain.Page{Entries: make([]domain.Entry, len(rows))}
	group, workCtx := errgroup.WithContext(ctx)
	group.SetLimit(8)
	for i, row := range rows {
		if workCtx.Err() != nil {
			break
		}
		group.Go(func() error {
			if err := workCtx.Err(); err != nil {
				return err
			}
			entry := domain.Entry{PID: int32(row.PID)}
			native, err := stat(row)
			if err == nil && native.PID != row.PID {
				err = errors.New("mismatched native process identity")
			}
			if err == nil {
				var process domain.Process
				process, err = convert(native, pageSize)
				if err == nil {
					entry.Process = &process
				}
			}
			if err != nil {
				switch {
				case errors.Is(err, os.ErrNotExist):
					entry.Issue = "not_found"
				case errors.Is(err, os.ErrPermission):
					entry.Issue = "permission_denied"
				default:
					entry.Issue = "unavailable"
				}
			}
			page.Entries[i] = entry
			return workCtx.Err()
		})
	}
	if err := group.Wait(); err != nil {
		return domain.Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	return page, nil
}

func classify(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return errors.Join(domain.ErrNotFound, err)
	case errors.Is(err, os.ErrPermission):
		return errors.Join(domain.ErrPermissionDenied, err)
	default:
		return err
	}
}

func convert(stat procfs.ProcStat, pageSize uint64) (domain.Process, error) {
	if stat.PID <= 0 || stat.PID > math.MaxInt32 || stat.PPID < 0 || stat.PPID > math.MaxInt32 {
		return domain.Process{}, errors.New("invalid native process identity")
	}
	if stat.NumThreads < 0 || uint64(stat.NumThreads) > math.MaxUint32 || stat.Priority < math.MinInt32 || stat.Priority > math.MaxInt32 || stat.Nice < math.MinInt32 || stat.Nice > math.MaxInt32 {
		return domain.Process{}, errors.New("invalid native process scheduling values")
	}
	if stat.RSS < 0 || pageSize == 0 || uint64(stat.RSS) > math.MaxUint64/pageSize {
		return domain.Process{}, errors.New("invalid native resident memory")
	}
	if !utf8.ValidString(stat.Comm) || len(stat.State) != 1 || !utf8.ValidString(stat.State) {
		return domain.Process{}, errors.New("invalid native process text")
	}
	return domain.Process{PID: int32(stat.PID), ParentPID: int32(stat.PPID), Name: stat.Comm, State: stat.State, Threads: uint32(stat.NumThreads), Priority: int32(stat.Priority), Nice: int32(stat.Nice), StartTicks: stat.Starttime, UserTicks: uint64(stat.UTime), SystemTicks: uint64(stat.STime), VirtualBytes: uint64(stat.VSize), ResidentBytes: uint64(stat.RSS) * pageSize}, nil
}
