package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/go-chi/chi/v5"
)

func TestCPUWithoutAValidSample(t *testing.T) {
	router := chi.NewRouter()
	Register(router, cpu.NewSampler(), "/")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/cpu", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP CPU status = %d, headers = %v", response.Code, response.Header())
	}
}

func TestStorageUnavailableForMissingConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-volume")
	router := chi.NewRouter()
	Register(router, cpu.NewSampler(), path)
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
	Register(router, cpu.NewSampler(), path)
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
