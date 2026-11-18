package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/grpcapi"
	filesystemsv1 "github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFilesystemGRPCPreservesPresenceSelectionAndPrecision(t *testing.T) {
	reader := &readerFixture{}
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	page, err := handler.ListMounts(context.Background(), &filesystemsv1.ListMountsRequest{AfterId: 41, Limit: 1})
	if err != nil || len(page.Mounts) != 1 || page.NextAfterId == nil || *page.NextAfterId != 42 || !page.Mounts[0].ReadOnly || reader.selection.AfterID != 41 || reader.selection.Limit != 1 || page.ObservedAt.CheckValid() != nil {
		t.Fatalf("%v %v", page, err)
	}
	usage, err := handler.GetFilesystemUsage(context.Background(), &filesystemsv1.GetFilesystemUsageRequest{Path: "/path with spaces"})
	if err != nil || usage.Usage.TotalBytes != 9007199254740993 || usage.Usage.ReservedBytes != 12288 || reader.path != "/path with spaces" {
		t.Fatalf("%v %v", usage, err)
	}
	before := reader.calls
	if _, err := handler.GetFilesystemUsage(context.Background(), &filesystemsv1.GetFilesystemUsageRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := handler.ListMounts(context.Background(), &filesystemsv1.ListMountsRequest{Limit: 257}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if reader.calls != before {
		t.Fatal("invalid input reached I/O")
	}
	for _, failure := range []struct {
		cause error
		code  codes.Code
	}{{domain.ErrNotFound, codes.NotFound}, {domain.ErrPermissionDenied, codes.PermissionDenied}, {domain.ErrResourceExhausted, codes.ResourceExhausted}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private path"), codes.Unavailable}} {
		reader.cause = failure.cause
		if result, err := handler.GetFilesystemUsage(context.Background(), &filesystemsv1.GetFilesystemUsageRequest{Path: "/"}); result != nil || status.Code(err) != failure.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("%v %v", result, err)
		}
	}
}
