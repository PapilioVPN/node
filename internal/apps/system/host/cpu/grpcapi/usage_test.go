package grpcapi_test

import (
	"context"
	"errors"
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

func TestGRPCUsagePreservesStateAndValuePresence(t *testing.T) {
	interval := int64(1)
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: usageReader()}}
	result, err := handler.GetHostCPUUsage(context.Background(), &cpuv1.GetHostCPUUsageRequest{PerCpu: true, IntervalNs: &interval})
	if err != nil {
		t.Fatal(err)
	}
	if !result.PerCpu || result.RequestedIntervalNs != 1 || result.TicksPerSecond != 100 || len(result.Cpus) != 6 || result.StartedAt.CheckValid() != nil || result.ObservedAt.CheckValid() != nil || result.SampleIntervalNs < 1 || result.DurationNs < result.SampleIntervalNs {
		t.Fatalf("invalid sample metadata: %v", result)
	}
	for i, want := range []cpuv1.CPUUsageState{cpuv1.CPUUsageState_CPU_USAGE_STATE_MEASURED, cpuv1.CPUUsageState_CPU_USAGE_STATE_NO_PROGRESS, cpuv1.CPUUsageState_CPU_USAGE_STATE_COUNTER_DECREASED, cpuv1.CPUUsageState_CPU_USAGE_STATE_COUNTER_OVERFLOW, cpuv1.CPUUsageState_CPU_USAGE_STATE_ADDED, cpuv1.CPUUsageState_CPU_USAGE_STATE_REMOVED} {
		row := result.Cpus[i]
		if row.State != want {
			t.Fatalf("state %d: %v", i, row)
		}
		if i == 0 {
			if row.Percent == nil || *row.Percent != 25 || row.BusyTicks == nil || *row.BusyTicks != 25 || row.TotalTicks == nil || *row.TotalTicks != 100 {
				t.Fatalf("lost measured values: %v", row)
			}
		} else if row.Percent != nil || row.BusyTicks != nil || row.TotalTicks != nil {
			t.Fatalf("fabricated unknown values: %v", row)
		}
	}
	if result.Cpus[4].Cpu != "cpu5" || result.Cpus[5].Cpu != "cpu4" {
		t.Fatal("lost hotplug identity")
	}
}

func TestGRPCUsageRejectsZeroAndCancelsPositiveWait(t *testing.T) {
	handler := grpcapi.Handler{Timeout: time.Second}
	for _, interval := range []int64{0, -1} {
		got, err := handler.GetHostCPUUsage(context.Background(), &cpuv1.GetHostCPUUsageRequest{IntervalNs: &interval})
		if got != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid interval: %v %v", got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := handler.GetHostCPUUsage(ctx, &cpuv1.GetHostCPUUsageRequest{}); got != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("canceled sample: %v %v", got, err)
	}
	handler.Service.TimesReader = usageReader()
	handler.Timeout = 10 * time.Millisecond
	interval := int64(time.Hour)
	got, err := handler.GetHostCPUUsage(context.Background(), &cpuv1.GetHostCPUUsageRequest{IntervalNs: &interval})
	if got != nil || status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("unbounded sample: %v %v", got, err)
	}
}

func TestGRPCUsageDefaultsAndSanitizedNativeFailure(t *testing.T) {
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{TimesReader: usageReader()}}
	got, err := handler.GetHostCPUUsage(context.Background(), &cpuv1.GetHostCPUUsageRequest{})
	if err != nil || got.PerCpu || got.RequestedIntervalNs != int64(domain.DefaultSampleInterval) {
		t.Fatalf("invalid defaults: %v %v", got, err)
	}
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private-stat-source"), codes.Unavailable}} {
		handler.Service.TimesReader = timesReaderFunc(func(context.Context, bool) (domain.Times, error) { return domain.Times{}, test.cause })
		got, err := handler.GetHostCPUUsage(context.Background(), &cpuv1.GetHostCPUUsageRequest{})
		if got != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private-stat-source") {
			t.Fatalf("invalid failure: %v %v", got, err)
		}
	}
}
