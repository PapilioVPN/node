package services_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
)

type readerFixture struct {
	calls     int
	selection domain.Selection
	cause     error
}

func (reader *readerFixture) List(_ context.Context, selection domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = selection
	return domain.Page{Devices: []domain.Device{{Name: "sda", ReadSectors: 9007199254740993}}}, reader.cause
}
func TestDeviceSelectionAndObservation(t *testing.T) {
	reader := &readerFixture{}
	service := services.Service{Reader: reader}
	for _, selection := range []domain.Selection{{Limit: 257}, {AfterName: strings.Repeat("a", 256)}, {AfterName: string([]byte{255})}, {AfterName: "bad\x00name"}} {
		if _, err := service.List(context.Background(), selection); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if reader.calls != 0 {
		t.Fatal("invalid selection reached native I/O")
	}
	result, err := service.List(context.Background(), domain.Selection{})
	if err != nil || reader.selection.Limit != 128 || result.Page.Devices[0].ReadSectors != 9007199254740993 || result.ObservedAt.IsZero() || result.Duration < 0 {
		t.Fatalf("%+v %v", result, err)
	}
	reader.cause = errors.New("native cause")
	if _, err := service.List(context.Background(), domain.Selection{}); !errors.Is(err, reader.cause) {
		t.Fatal(err)
	}
	reader.cause = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.List(ctx, domain.Selection{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
