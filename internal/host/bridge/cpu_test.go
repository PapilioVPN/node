package bridge

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PapilioVPN/node/internal/host/services"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestCPUWithoutAValidSample(t *testing.T) {
	sampler := services.NewCPUSampler()
	router := chi.NewRouter()
	server := grpc.NewServer()
	t.Cleanup(server.Stop)
	if err := Register(router, server, sampler, "/"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/host/cpu", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP CPU status = %d, headers = %v", response.Code, response.Header())
	}
	if _, err := getCPU(sampler)(t.Context(), &emptypb.Empty{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("gRPC CPU error = %v, want Unavailable", err)
	}
}
