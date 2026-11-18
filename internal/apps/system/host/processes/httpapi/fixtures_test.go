package httpapi_test

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
)

type readerFixture struct {
	cause     error
	calls     int
	selection domain.Selection
	pid       int32
}

func (reader *readerFixture) Count(context.Context) (uint64, error) {
	reader.calls++
	return 9007199254740993, reader.cause
}
func (reader *readerFixture) Read(_ context.Context, pid int32) (domain.Process, error) {
	reader.calls++
	reader.pid = pid
	return sampleProcess(), reader.cause
}
func (reader *readerFixture) List(_ context.Context, selection domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = selection
	process := sampleProcess()
	cursor := int32(43)
	return domain.Page{Entries: []domain.Entry{{PID: 42, Process: &process}, {PID: 43, Issue: "permission_denied"}}, NextAfterPID: &cursor}, reader.cause
}
func sampleProcess() domain.Process {
	return domain.Process{PID: 42, ParentPID: 1, Name: "test", State: "S", Threads: 2, Priority: 20, Nice: -1, StartTicks: 9007199254740993, UserTicks: 18446744073709551615, VirtualBytes: 9007199254740993, ResidentBytes: 4096}
}
