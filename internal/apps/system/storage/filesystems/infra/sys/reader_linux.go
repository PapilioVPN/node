// Package sys adapts native mountinfo and Linux statfs observations.
package sys

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	"github.com/prometheus/procfs"
	"golang.org/x/sys/unix"
)

type Reader struct {
	fs     procfs.FS
	statfs func(string, *unix.Statfs_t) error
}

func New() (*Reader, error) {
	fs, err := procfs.NewFS("/proc")
	if err != nil {
		return nil, err
	}
	return &Reader{fs: fs, statfs: unix.Statfs}, nil
}

// List reads one native mountinfo file. It performs no path or capacity probes.
// The SDK materializes that file before the supported 16384-mount bound is checked.
func (reader *Reader) List(ctx context.Context, selection domain.Selection) (domain.Page, error) {
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	native, err := reader.fs.GetMounts()
	if err != nil {
		return domain.Page{}, errors.Join(ctx.Err(), err)
	}
	if len(native) > 16384 {
		return domain.Page{}, domain.ErrResourceExhausted
	}
	rows := make([]domain.Mount, 0, len(native))
	seen := make(map[uint32]struct{}, len(native))
	for _, record := range native {
		if err := ctx.Err(); err != nil {
			return domain.Page{}, err
		}
		row, err := mount(record)
		if err != nil {
			return domain.Page{}, err
		}
		if _, exists := seen[row.ID]; exists {
			return domain.Page{}, errors.New("duplicate native mount identity")
		}
		seen[row.ID] = struct{}{}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	start := sort.Search(len(rows), func(i int) bool { return rows[i].ID > selection.AfterID })
	end := min(start+int(selection.Limit), len(rows))
	page := domain.Page{Mounts: rows[start:end]}
	if end < len(rows) {
		cursor := rows[end-1].ID
		page.NextAfterID = &cursor
	}
	return page, nil
}

// Usage is one targeted native statfs, independent of mount inventory size.
// Synchronous filesystem I/O, especially remote filesystems, is not interrupted
// by Go context cancellation; checks surround the syscall and reject late results.
func (reader *Reader) Usage(ctx context.Context, path string) (domain.Usage, error) {
	if err := ctx.Err(); err != nil {
		return domain.Usage{}, err
	}
	var stat unix.Statfs_t
	if err := reader.statfs(path, &stat); err != nil {
		cause := err
		switch {
		case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR):
			cause = errors.Join(domain.ErrNotFound, err)
		case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
			cause = errors.Join(domain.ErrPermissionDenied, err)
		}
		return domain.Usage{}, errors.Join(ctx.Err(), cause)
	}
	if err := ctx.Err(); err != nil {
		return domain.Usage{}, err
	}
	if stat.Bsize <= 0 || stat.Blocks > math.MaxUint64/uint64(stat.Bsize) || stat.Bfree > stat.Blocks || stat.Bavail > stat.Bfree || stat.Ffree > stat.Files {
		return domain.Usage{}, errors.New("invalid native filesystem capacity")
	}
	blockSize := uint64(stat.Bsize)
	return domain.Usage{Path: path, FilesystemType: uint64(stat.Type), Flags: uint64(stat.Flags), BlockSizeBytes: blockSize, TotalBytes: stat.Blocks * blockSize, FreeBytes: stat.Bfree * blockSize, AvailableBytes: stat.Bavail * blockSize, UsedBytes: (stat.Blocks - stat.Bfree) * blockSize, ReservedBytes: (stat.Bfree - stat.Bavail) * blockSize, InodesTotal: stat.Files, InodesFree: stat.Ffree, InodesUsed: stat.Files - stat.Ffree}, nil
}

// Linux mountinfo escapes exactly these four bytes. Quotes and other literal
// bytes must survive; interpreting the value as a Go quoted string would corrupt them.
var unescape = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

func mount(native *procfs.MountInfo) (domain.Mount, error) {
	if native == nil || native.MountID <= 0 || uint64(native.MountID) > math.MaxUint32 || native.ParentID < 0 || uint64(native.ParentID) > math.MaxUint32 {
		return domain.Mount{}, errors.New("invalid native mount identity")
	}
	major, minor, ok := strings.Cut(native.MajorMinorVer, ":")
	majorID, majorErr := strconv.ParseUint(major, 10, 32)
	minorID, minorErr := strconv.ParseUint(minor, 10, 32)
	if !ok || majorErr != nil || minorErr != nil {
		return domain.Mount{}, errors.New("invalid native mount device")
	}
	row := domain.Mount{ID: uint32(native.MountID), ParentID: uint32(native.ParentID), DeviceMajor: uint32(majorID), DeviceMinor: uint32(minorID), Root: unescape.Replace(native.Root), Path: unescape.Replace(native.MountPoint), Filesystem: native.FSType, Source: unescape.Replace(native.Source)}
	for _, text := range []string{row.Root, row.Path, row.Filesystem, row.Source} {
		if !utf8.ValidString(text) {
			return domain.Mount{}, errors.New("invalid native mount text")
		}
	}
	_, mountReadOnly := native.Options["ro"]
	_, filesystemReadOnly := native.SuperOptions["ro"]
	row.ReadOnly = mountReadOnly || filesystemReadOnly
	return row, nil
}
