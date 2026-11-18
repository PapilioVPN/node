package httpapi

import (
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	"time"
)

type mountResponse struct {
	ID          uint32 `json:"id"`
	ParentID    uint32 `json:"parent_id"`
	DeviceMajor uint32 `json:"device_major"`
	DeviceMinor uint32 `json:"device_minor"`
	Root        string `json:"root"`
	Path        string `json:"path"`
	Filesystem  string `json:"filesystem"`
	Source      string `json:"source"`
	ReadOnly    bool   `json:"read_only"`
}
type listResponse struct {
	Mounts      []mountResponse `json:"mounts"`
	NextAfterID *uint32         `json:"next_after_id"`
	ObservedAt  time.Time       `json:"observed_at"`
	DurationNS  int64           `json:"duration_ns,string"`
}

func listResult(result domain.ListObservation) listResponse {
	rows := make([]mountResponse, 0, len(result.Page.Mounts))
	for _, row := range result.Page.Mounts {
		rows = append(rows, mountResponse{ID: row.ID, ParentID: row.ParentID, DeviceMajor: row.DeviceMajor, DeviceMinor: row.DeviceMinor, Root: row.Root, Path: row.Path, Filesystem: row.Filesystem, Source: row.Source, ReadOnly: row.ReadOnly})
	}
	return listResponse{Mounts: rows, NextAfterID: result.Page.NextAfterID, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type usageValues struct {
	Path           string `json:"path"`
	FilesystemType uint64 `json:"filesystem_type,string"`
	Flags          uint64 `json:"flags,string"`
	BlockSizeBytes uint64 `json:"block_size_bytes,string"`
	TotalBytes     uint64 `json:"total_bytes,string"`
	FreeBytes      uint64 `json:"free_bytes,string"`
	AvailableBytes uint64 `json:"available_bytes,string"`
	UsedBytes      uint64 `json:"used_bytes,string"`
	ReservedBytes  uint64 `json:"reserved_bytes,string"`
	InodesTotal    uint64 `json:"inodes_total,string"`
	InodesFree     uint64 `json:"inodes_free,string"`
	InodesUsed     uint64 `json:"inodes_used,string"`
}
type usageResponse struct {
	Usage      usageValues `json:"usage"`
	ObservedAt time.Time   `json:"observed_at"`
	DurationNS int64       `json:"duration_ns,string"`
}

func usageResult(result domain.UsageObservation) usageResponse {
	row := result.Usage
	return usageResponse{Usage: usageValues{Path: row.Path, FilesystemType: row.FilesystemType, Flags: row.Flags, BlockSizeBytes: row.BlockSizeBytes, TotalBytes: row.TotalBytes, FreeBytes: row.FreeBytes, AvailableBytes: row.AvailableBytes, UsedBytes: row.UsedBytes, ReservedBytes: row.ReservedBytes, InodesTotal: row.InodesTotal, InodesFree: row.InodesFree, InodesUsed: row.InodesUsed}, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
