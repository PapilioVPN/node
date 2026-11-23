package grpc

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestCPUWithoutAValidSample(t *testing.T) {
	if _, err := getCPU(cpu.NewSampler())(t.Context(), &emptypb.Empty{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("gRPC CPU error = %v, want Unavailable", err)
	}
}

func TestStorageUnavailableForMissingConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-volume")
	if _, err := getStorage(path)(t.Context(), &emptypb.Empty{}); status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), path) {
		t.Fatalf("gRPC storage error = %v, want Unavailable without a path", err)
	}
}

func TestSystemStatsReturnsAvailableSections(t *testing.T) {
	sampler := cpu.NewSampler()
	path := filepath.Join(t.TempDir(), "missing-volume")
	snapshot, err := getSystemStats(sampler, path)(t.Context(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.UptimeSeconds == nil || snapshot.GetMemory().GetTotalBytes() == 0 || snapshot.Cpu != nil || snapshot.Storage != nil {
		t.Fatalf("gRPC system stats = %v, want uptime and memory while CPU and storage are unavailable", snapshot)
	}
}
