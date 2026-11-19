package natsapi_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
)

type logindFixture struct {
	calls     int
	cause     error
	selection domain.Selection
	id        string
	basic     bool
}

func (reader *logindFixture) List(_ context.Context, input domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = input
	next := uint32(1)
	row := sampleSummary()
	if reader.basic {
		row.LeaderPID = nil
		row.Idle = nil
	}
	return domain.Page{Sessions: []domain.Summary{row}, Extended: !reader.basic, NextOffset: &next}, reader.cause
}
func (reader *logindFixture) Read(_ context.Context, id string) (domain.Details, error) {
	reader.calls++
	reader.id = id
	started := uint64(9007199254740993)
	remote := false
	return domain.Details{Summary: sampleSummary(), StartedUnixUS: &started, Remote: &remote}, reader.cause
}

type recordsFixture struct {
	calls     int
	cause     error
	selection domain.Selection
}

func (reader *recordsFixture) List(_ context.Context, input domain.Selection) (domain.RecordPage, error) {
	reader.calls++
	reader.selection = input
	row := domain.LoginRecord{PID: 42, RecordID: "p1", User: "test", StartedUnixUS: -500000, Terminal: "pts/1"}
	return domain.RecordPage{Records: []domain.LoginRecord{row, row}}, reader.cause
}
func sampleSummary() domain.Summary {
	leader := uint32(0)
	idle := false
	return domain.Summary{ID: "s1", UID: 0, User: "test", LeaderPID: &leader, Idle: &idle}
}
