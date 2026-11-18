package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
)

type readerFixture struct {
	calls     int
	cause     error
	selection domain.Selection
}

func (reader *readerFixture) Count(context.Context) (uint64, error) {
	reader.calls++
	return 42, reader.cause
}
func (reader *readerFixture) Read(context.Context, int32) (domain.Process, error) {
	reader.calls++
	return domain.Process{PID: 42, StartTicks: 9007199254740993}, reader.cause
}
func (reader *readerFixture) List(_ context.Context, selection domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = selection
	return domain.Page{Entries: []domain.Entry{{PID: 42, Issue: "not_found"}}}, reader.cause
}

func TestSelectionsAreValidatedBeforeIO(t *testing.T) {
	reader := &readerFixture{}
	service := services.Service{Reader: reader, TicksPerSecond: 100}
	for _, pid := range []int32{-1, 0} {
		if _, err := service.Read(context.Background(), pid); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	for _, selection := range []domain.Selection{{AfterPID: -1}, {Limit: 257}} {
		if _, err := service.List(context.Background(), selection); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if reader.calls != 0 {
		t.Fatal("invalid selection reached native I/O")
	}
	result, err := service.List(context.Background(), domain.Selection{})
	if err != nil || reader.selection.Limit != 128 || result.Page.Entries[0].Issue != "not_found" || result.TicksPerSecond != 100 || result.ObservedAt.IsZero() || result.Duration < 0 {
		t.Fatalf("invalid observation: %+v %v", result, err)
	}
}

func TestOperationsPreserveCausesAndRejectCanceledResults(t *testing.T) {
	cause := errors.New("native cause")
	reader := &readerFixture{cause: cause}
	service := services.Service{Reader: reader, TicksPerSecond: 100}
	if _, err := service.Read(context.Background(), 42); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if _, err := service.Count(context.Background()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if _, err := service.List(context.Background(), domain.Selection{}); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	reader.cause = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Read(ctx, 42); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.Count(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.List(ctx, domain.Selection{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
