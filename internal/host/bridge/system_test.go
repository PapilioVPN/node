package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	nodev1 "github.com/PapilioVPN/node/api/v1"
	"github.com/PapilioVPN/node/internal/host/services"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestSystemStatsReturnsAvailableSections(t *testing.T) {
	sampler := services.NewCPUSampler()
	path := filepath.Join(t.TempDir(), "missing-volume")
	response := httptest.NewRecorder()
	handleSystemStats(sampler, path).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/stats", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP system stats status = %d, headers = %v", response.Code, response.Header())
	}
	var httpSnapshot nodev1.SystemSnapshot
	if err := json.NewDecoder(response.Body).Decode(&httpSnapshot); err != nil {
		t.Fatal(err)
	}
	checkPartialSystemStats(t, &httpSnapshot)
	rpcSnapshot, err := getSystemStats(sampler, path)(t.Context(), &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	checkPartialSystemStats(t, rpcSnapshot)
}

func checkPartialSystemStats(t *testing.T, snapshot *nodev1.SystemSnapshot) {
	t.Helper()
	if snapshot.UptimeSeconds == nil || snapshot.GetMemory().GetTotalBytes() == 0 || snapshot.Cpu != nil || snapshot.Storage != nil {
		t.Fatalf("system stats = %v, want uptime and memory while CPU and storage are unavailable", snapshot)
	}
}
