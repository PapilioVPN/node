// Package domain defines mounted filesystems and targeted capacity observations.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument   = errors.New("invalid filesystem selection")
	ErrNotFound          = errors.New("filesystem path not found")
	ErrPermissionDenied  = errors.New("filesystem access denied")
	ErrResourceExhausted = errors.New("mount inventory exceeds supported bound")
)

// Mount describes a mount in Node's current mount namespace. A read-only mount
// does not imply its underlying device or other mounts of that filesystem are read-only.
type Mount struct {
	ID          uint32
	ParentID    uint32
	DeviceMajor uint32
	DeviceMinor uint32
	Root        string
	Path        string
	Filesystem  string
	Source      string
	ReadOnly    bool
}
type Selection struct {
	AfterID uint32
	Limit   uint32
}
type Page struct {
	Mounts      []Mount
	NextAfterID *uint32
}

// Usage reports exact statfs units. Free includes reserved blocks; available is
// the unprivileged allowance. Neither a mount scan nor a usage read is an atomic
// host-wide snapshot. Network filesystems may block inside the native syscall.
type Usage struct {
	Path           string
	FilesystemType uint64
	Flags          uint64
	BlockSizeBytes uint64
	TotalBytes     uint64
	FreeBytes      uint64
	AvailableBytes uint64
	UsedBytes      uint64
	ReservedBytes  uint64
	InodesTotal    uint64
	InodesFree     uint64
	InodesUsed     uint64
}
type ListObservation struct {
	Page       Page
	ObservedAt time.Time
	Duration   time.Duration
}
type UsageObservation struct {
	Usage      Usage
	ObservedAt time.Time
	Duration   time.Duration
}
