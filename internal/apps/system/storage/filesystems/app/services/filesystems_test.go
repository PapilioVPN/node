package services_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
)

type readerFixture struct {
	calls     int
	selection domain.Selection
	cause     error
}

func (reader *readerFixture) List(_ context.Context, selection domain.Selection) (domain.Page, error) {
	reader.calls++
	reader.selection = selection
	return domain.Page{Mounts: []domain.Mount{{ID: 42}}}, reader.cause
}
func (reader *readerFixture) Usage(_ context.Context, path string) (domain.Usage, error) {
	reader.calls++
	return domain.Usage{Path: path, TotalBytes: 9007199254740993}, reader.cause
}
func TestFilesystemSelectionsAndCanceledObservations(t *testing.T) {
	reader := &readerFixture{}
	service := services.Service{Reader: reader}
	for _, path := range []string{"", "relative", "/a\x00b", "/" + string([]byte{255}), "/" + strings.Repeat("a", 4095)} {
		if _, err := service.Usage(context.Background(), path); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("accepted invalid path: %v", err)
		}
	}
	if _, err := service.List(context.Background(), domain.Selection{Limit: 257}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
	if reader.calls != 0 {
		t.Fatal("invalid input reached native I/O")
	}
	page, err := service.List(context.Background(), domain.Selection{})
	if err != nil || reader.selection.Limit != 128 || len(page.Page.Mounts) != 1 || page.ObservedAt.IsZero() || page.Duration < 0 {
		t.Fatalf("%+v %v", page, err)
	}
	usage, err := service.Usage(context.Background(), "/path with spaces")
	if err != nil || usage.Usage.Path != "/path with spaces" || usage.Usage.TotalBytes != 9007199254740993 {
		t.Fatalf("%+v %v", usage, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.List(ctx, domain.Selection{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := service.Usage(ctx, "/"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reader.cause = errors.New("native cause")
	if _, err := service.List(context.Background(), domain.Selection{}); !errors.Is(err, reader.cause) {
		t.Fatal(err)
	}
	if _, err := service.Usage(context.Background(), "/"); !errors.Is(err, reader.cause) {
		t.Fatal(err)
	}
}
