// Package proc adapts the pinned native bulk diskstats reader.
package proc

import (
	"context"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
	"github.com/prometheus/procfs/blockdevice"
)

type Reader struct {
	read func() ([]blockdevice.Diskstats, error)
}

func New() (*Reader, error) {
	fs, err := blockdevice.NewFS("/proc", "/sys")
	if err != nil {
		return nil, err
	}
	return &Reader{read: fs.ProcDiskstats}, nil
}

// List performs one bulk native read, with no per-device serial/udev/sysfs work.
// Native parsing materializes the file before the supported 16384-record bound.
// Pages re-observe current devices; they are not an atomic cross-page snapshot.
func (reader *Reader) List(ctx context.Context, selection domain.Selection) (domain.Page, error) {
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	native, err := reader.read()
	if err != nil {
		return domain.Page{}, errors.Join(ctx.Err(), err)
	}
	if len(native) > 16384 {
		return domain.Page{}, domain.ErrResourceExhausted
	}
	rows := make([]domain.Device, 0, len(native))
	ids := make(map[[2]uint32]struct{}, len(native))
	names := make(map[string]struct{}, len(native))
	for _, record := range native {
		if err := ctx.Err(); err != nil {
			return domain.Page{}, err
		}
		row, err := device(record)
		if err != nil {
			return domain.Page{}, err
		}
		key := [2]uint32{row.Major, row.Minor}
		if _, exists := ids[key]; exists {
			return domain.Page{}, errors.New("duplicate native device identity")
		}
		ids[key] = struct{}{}
		if _, exists := names[row.Name]; exists {
			return domain.Page{}, errors.New("duplicate native device name")
		}
		names[row.Name] = struct{}{}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	start := sort.Search(len(rows), func(i int) bool { return rows[i].Name > selection.AfterName })
	end := min(start+int(selection.Limit), len(rows))
	page := domain.Page{Devices: rows[start:end]}
	if end < len(rows) {
		name := rows[end-1].Name
		page.NextAfterName = &name
	}
	return page, nil
}
func device(native blockdevice.Diskstats) (domain.Device, error) {
	if native.DeviceName == "" || !utf8.ValidString(native.DeviceName) || (native.IoStatsCount != 14 && native.IoStatsCount != 18 && native.IoStatsCount != 20) {
		return domain.Device{}, errors.New("invalid native device I/O record")
	}
	row := domain.Device{Major: native.MajorNumber, Minor: native.MinorNumber, Name: native.DeviceName, ReadsCompleted: native.ReadIOs, ReadsMerged: native.ReadMerges, ReadSectors: native.ReadSectors, ReadMilliseconds: native.ReadTicks, WritesCompleted: native.WriteIOs, WritesMerged: native.WriteMerges, WriteSectors: native.WriteSectors, WriteMilliseconds: native.WriteTicks, InProgress: native.IOsInProgress, ActiveMilliseconds: native.IOsTotalTicks, WeightedMilliseconds: native.WeightedIOTicks}
	if native.IoStatsCount >= 18 {
		row.Discard = &domain.Discard{Completed: native.DiscardIOs, Merged: native.DiscardMerges, Sectors: native.DiscardSectors, Milliseconds: native.DiscardTicks}
	}
	if native.IoStatsCount >= 20 {
		row.Flush = &domain.Flush{Completed: native.FlushRequestsCompleted, Milliseconds: native.TimeSpentFlushing}
	}
	return row, nil
}
