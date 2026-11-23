package bootstrap

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	bridgehttp "github.com/PapilioVPN/go-bridge/api/http/server"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func newHTTPRouter(config settings.Management, readiness *health.Server) *chi.Mux {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return bridgehttp.RequireAPIKey(config.APIKey, next)
	})
	router.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		response, err := readiness.Check(r.Context(), &healthpb.HealthCheckRequest{})
		if err != nil || response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			http.Error(w, "node is not serving", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return router
}

func newHTTPServer(config settings.Management, tlsConfig *tls.Config, router *chi.Mux) *http.Server {
	return &http.Server{
		Addr:              config.HTTPAddress,
		Handler:           router,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}
}

func serveHTTP(server *http.Server, listener net.Listener) error {
	if server.TLSConfig != nil {
		return server.ServeTLS(listener, "", "")
	}
	return server.Serve(listener)
}
