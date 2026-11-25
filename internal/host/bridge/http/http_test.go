package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/PapilioVPN/node/internal/host/network"
	"github.com/go-chi/chi/v5"
)

func TestCPUWithoutAValidSample(t *testing.T) {
	router := chi.NewRouter()
	Register(router, cpu.NewSampler(), network.NewSampler(), "/")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/cpu", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP CPU status = %d, headers = %v", response.Code, response.Header())
	}
}

func TestSystemStatsReportsUsedMemoryAndLogicalCPUs(t *testing.T) {
	router := chi.NewRouter()
	Register(router, cpu.NewSampler(), network.NewSampler(), "/")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/stats", nil))
	var snapshot struct {
		LogicalCPUs uint32 `json:"logical_cpu_count"`
		Memory      struct {
			Total uint64  `json:"total_bytes"`
			Used  *uint64 `json:"used_bytes"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || snapshot.LogicalCPUs == 0 || snapshot.Memory.Used == nil || *snapshot.Memory.Used > snapshot.Memory.Total {
		t.Fatalf("host system stats lack CPU count or used memory: %s", response.Body.String())
	}
}

func TestStorageUnavailableForMissingConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-volume")
	router := chi.NewRouter()
	Register(router, cpu.NewSampler(), network.NewSampler(), path)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/storage?path=/", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(response.Body.String(), path) {
		t.Fatalf("HTTP storage response = %d, %v, %q", response.Code, response.Header(), response.Body.String())
	}
}

func TestSystemStatsReturnsAvailableSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-volume")
	router := chi.NewRouter()
	Register(router, cpu.NewSampler(), network.NewSampler(), path)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/stats", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP system stats status = %d, headers = %v", response.Code, response.Header())
	}
	var snapshot struct {
		UptimeSeconds *uint64          `json:"uptime_seconds"`
		CPU           *cpuResponse     `json:"cpu"`
		Memory        *memoryResponse  `json:"memory"`
		Storage       *storageResponse `json:"storage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.UptimeSeconds == nil || snapshot.Memory == nil || snapshot.Memory.TotalBytes == 0 ||
		snapshot.CPU != nil || snapshot.Storage != nil {
		t.Fatalf("HTTP system stats = %s, want uptime and memory while CPU and storage are unavailable", response.Body.String())
	}
}
