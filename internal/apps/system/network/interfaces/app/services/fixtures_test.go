package services_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
)

type readerFixture struct {
	snapshot            domain.Snapshot
	counters            []domain.InterfaceCounters
	cause               error
	calls, counterCalls int
	context             context.Context
}

func (reader *readerFixture) List(ctx context.Context) (domain.Snapshot, error) {
	reader.calls++
	reader.context = ctx
	return reader.snapshot, reader.cause
}
func (reader *readerFixture) Counters(ctx context.Context) ([]domain.InterfaceCounters, error) {
	reader.counterCalls++
	reader.context = ctx
	return reader.counters, reader.cause
}
