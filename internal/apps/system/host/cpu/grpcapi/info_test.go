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

func TestGRPCInfoPreservesAllFieldsAndAbsentTopology(t *testing.T) {
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{InfoReader: infoReaderFunc(func(context.Context) ([]domain.Processor, error) { return sampleProcessors(), nil })}}
	got, err := handler.GetHostCPUInfo(context.Background(), &cpuv1.GetHostCPUInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.LogicalCount != 2 || got.PackageCount != nil || got.PhysicalCoreCount != nil || len(got.Processors) != 2 || got.ObservedAt.CheckValid() != nil || got.DurationNs < 0 {
		t.Fatalf("invalid inventory metadata: %v", got)
	}
	row := got.Processors[0]
	if row.Id != 4 || row.GetVendorId() != "Vendor" || row.GetFamily() != "6" || row.GetModel() != "23" || row.GetModelName() != "Model: processor" || row.Stepping == nil || *row.Stepping != "0" || row.GetMicrocode() != "0x123" || row.PackageId == nil || *row.PackageId != 0 || row.CoreId == nil || *row.CoreId != 3 || row.ReportedCores == nil || *row.ReportedCores != 4 || row.ReportedSiblings == nil || *row.ReportedSiblings != 8 || row.ReportedMhz == nil || *row.ReportedMhz != 0 || row.CacheBytes == nil || *row.CacheBytes != 1<<64-1024 || !slices.Equal(row.ReportedFeatures, []string{"f1", "f2"}) || !slices.Equal(row.ReportedBugs, []string{"b1"}) {
		t.Fatalf("lost metadata/presence: %v", row)
	}
	missing := got.Processors[1]
	if missing.Id != 9 || missing.VendorId != nil || missing.ModelName != nil || missing.PackageId != nil || missing.CoreId != nil || missing.CacheBytes != nil || missing.ReportedMhz != nil {
		t.Fatalf("fabricated metadata: %v", missing)
	}
}

func TestGRPCInfoPreservesCancellationAndSanitizesCause(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private cpuinfo path"), codes.Unavailable}} {
		handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{InfoReader: infoReaderFunc(func(context.Context) ([]domain.Processor, error) { return sampleProcessors(), test.cause })}}
		got, err := handler.GetHostCPUInfo(context.Background(), &cpuv1.GetHostCPUInfoRequest{})
		if got != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private cpuinfo path") {
			t.Fatalf("invalid inventory failure: %v %v", got, err)
		}
	}
}
