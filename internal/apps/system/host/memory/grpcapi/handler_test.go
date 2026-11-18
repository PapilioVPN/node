package grpcapi_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/memory/grpcapi"
	memoryv1 "github.com/PapilioVPN/node/internal/apps/system/host/memory/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReadPreservesUint64MemoryAndTimingWithoutExtraReads(t *testing.T) {
	calls := 0
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Snapshot, error) {
		calls++
		return sampleMemory(), nil
	})}}
	result, err := handler.GetHostMemory(context.Background(), &memoryv1.GetHostMemoryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := []uint64{result.TotalBytes, result.AvailableBytes, result.UsedBytes, result.FreeBytes, result.ActiveBytes, result.InactiveBytes, result.BuffersBytes, result.CachedBytes, result.SharedBytes, result.SlabBytes}
	if calls != 1 || !slices.Equal(got, []uint64{1<<64 - 1, 3, 1<<64 - 4, 1, 2, 4, 8, 16, 32, 64}) || result.UsedPercent != 99.5 || result.ObservedAt.CheckValid() != nil || result.DurationNs < 0 {
		t.Fatalf("invalid gRPC memory: %+v; reads=%d", result, calls)
	}
}

func TestReadPreservesCancellationAndSanitizesNativeFailure(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{
		{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
		{errors.New("private-native-cause"), codes.Unavailable},
	} {
		handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Snapshot, error) { return domain.Snapshot{}, test.cause })}}
		result, err := handler.GetHostMemory(context.Background(), &memoryv1.GetHostMemoryRequest{})
		if result != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private-native-cause") {
			t.Fatalf("invalid failure: %v %v", result, err)
		}
	}
}
