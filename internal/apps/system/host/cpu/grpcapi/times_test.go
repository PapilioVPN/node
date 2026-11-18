package grpcapi_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi"
	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTimesGRPCPreservesAllCountersAndSelection(t *testing.T) {
	for _, perCPU := range []bool{false, true} {
		calls := 0
		handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: timesReaderFunc(func(ctx context.Context, selection bool) (domain.Times, error) {
			if selection != perCPU {
				t.Error("lost selection")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Error("unbounded read")
			}
			calls++
			return sampleTimes(perCPU), nil
		})}}
		got, err := handler.GetHostCPUTimes(context.Background(), &cpuv1.GetHostCPUTimesRequest{PerCpu: perCPU})
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 || got.PerCpu != perCPU || got.TicksPerSecond != 100 || len(got.Counters) != 1 || got.ObservedAt.CheckValid() != nil || got.DurationNs < 0 {
			t.Fatalf("lost times metadata: %v; calls=%d", got, calls)
		}
		row := got.Counters[0]
		if row.Cpu != "cpu9" || !slices.Equal([]uint64{row.UserTicks, row.NiceTicks, row.SystemTicks, row.IdleTicks, row.IowaitTicks, row.IrqTicks, row.SoftirqTicks, row.StealTicks, row.GuestTicks, row.GuestNiceTicks}, []uint64{1<<64 - 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
			t.Fatalf("lost raw counters: %v", row)
		}
	}
}

func TestTimesGRPCFailureDoesNotLeakNativeCausesOrPartialFacts(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private-stat-path"), codes.Unavailable}} {
		handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: timesReaderFunc(func(context.Context, bool) (domain.Times, error) { return sampleTimes(false), test.cause })}}
		got, err := handler.GetHostCPUTimes(context.Background(), &cpuv1.GetHostCPUTimesRequest{})
		if got != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private-stat-path") {
			t.Fatalf("failure: %v %v", got, err)
		}
	}
}
