package bridge

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/host/services"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestStorageUnavailableForMissingConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-volume")
	router := chi.NewRouter()
	server := grpc.NewServer()
	t.Cleanup(server.Stop)
	if err := Register(router, server, services.NewCPUSampler(), path); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/storage?path=/", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" ||
		strings.Contains(response.Body.String(), path) {
		t.Fatalf("HTTP storage response = %d, %v, %q", response.Code, response.Header(), response.Body.String())
	}
	if _, err := getStorage(path)(t.Context(), &emptypb.Empty{}); status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), path) {
		t.Fatalf("gRPC storage error = %v, want Unavailable without a path", err)
	}
}
