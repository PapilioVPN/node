package natsapi_test

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
)

type readerFixture struct {
	calls     int
	cause     error
	selection domain.Selection
	path      string
}

func (reader *readerFixture) List(_ context.Context, selection domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = selection
	cursor := uint32(42)
	return domain.Page{Mounts: []domain.Mount{{ID: 42, ParentID: 1, DeviceMajor: 8, DeviceMinor: 1, Path: "/path with spaces", Root: "/", Filesystem: "ext4", Source: "/dev/test", ReadOnly: true}}, NextAfterID: &cursor}, reader.cause
}
func (reader *readerFixture) Usage(_ context.Context, path string) (domain.Usage, error) {
	reader.calls++
	reader.path = path
	return domain.Usage{Path: path, TotalBytes: 9007199254740993, FreeBytes: 40960, AvailableBytes: 28672, ReservedBytes: 12288, InodesTotal: 100, InodesFree: 60, InodesUsed: 40}, reader.cause
}
