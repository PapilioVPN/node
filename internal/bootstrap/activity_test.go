package bootstrap

import (
	"net"
	"net/http"
	"testing"
	"time"

	bridgerpc "github.com/PapilioVPN/go-bridge/api/grpc/client"
	"github.com/PapilioVPN/node/internal/settings"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestLeaseActivityRequiresAuthenticatedRequests(t *testing.T) {
	files, clientTLS := testTLS(t, false)
	config := settings.Management{APIKey: "test-key", TLS: files, RenewLeaseOnAuthRequest: true}
	activity := newManagementActivity(config)
	readiness := newReadiness()
	readiness.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	tlsConfig, err := loadTLS(config)
	if err != nil {
		t.Fatal(err)
	}
	server, err := newGRPCServer(config, tlsConfig, readiness, activity)
	if err != nil {
		t.Fatal(err)
	}
	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(grpcListener)
	t.Cleanup(server.Stop)

	httpServer := newHTTPServer(config, tlsConfig, newHTTPRouter(config, readiness, activity))
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go serveHTTP(httpServer, httpListener)
	t.Cleanup(func() { _ = httpServer.Close() })
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	url := "https://" + httpListener.Addr().String() + "/healthz"

	base := time.Now().Add(-time.Second)
	expiry := func() time.Time { return activity.Extend(base, time.Minute) }
	checkHTTP(t, t.Context(), client, url, "", http.StatusUnauthorized)
	call := func(key string) error {
		connection, err := bridgerpc.NewClient(bridgerpc.Config{
			Target: grpcListener.Addr().String(), APIKey: key, TLS: clientTLS,
		})
		if err != nil {
			return err
		}
		defer connection.Close()
		_, err = healthpb.NewHealthClient(connection).Check(t.Context(), &healthpb.HealthCheckRequest{})
		return err
	}
	if err := call("wrong-key"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated gRPC request: %v", err)
	}
	if !expiry().Equal(base) {
		t.Fatal("unauthenticated request extended the lease")
	}
	checkHTTP(t, t.Context(), client, url, config.APIKey, http.StatusNoContent)
	afterHTTP := expiry()
	if !afterHTTP.After(base) {
		t.Fatal("authenticated HTTP request did not extend the lease")
	}
	if err := call(config.APIKey); err != nil {
		t.Fatal(err)
	}
	if !expiry().After(afterHTTP) {
		t.Fatal("authenticated gRPC request did not extend the lease")
	}
	if !activity.Extend(time.Time{}, time.Minute).IsZero() {
		t.Fatal("request created a lease")
	}
	if newManagementActivity(settings.Management{APIKey: config.APIKey}) != nil {
		t.Fatal("request renewal is enabled by default")
	}
}
