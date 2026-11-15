//go:build integration

package bootstrap_test

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/PapilioVPN/node/internal/bootstrap"
	nodev1 "github.com/PapilioVPN/node/internal/modules/system/info/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestRuntimeMTLSAndShutdown(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	var server *rpc.Server
	application := bootstrap.New(certificates.Config(), "integration-version", fx.Populate(&server), fx.NopLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := application.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	address := server.Addr().String()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	result, err := nodev1.NewNodeServiceClient(connection).GetNodeInfo(ctx, &nodev1.GetNodeInfoRequest{})
	if err != nil || result.GetNodeId() != "test-node" || result.GetVersion() != "integration-version" {
		t.Fatalf("real gRPC operation failed: %v, %v", result, err)
	}
	if !slices.Contains(result.GetCapabilities(), "xray.binary.info.read") {
		t.Fatalf("Xray module did not contribute its installed operation: %v", result.GetCapabilities())
	}
	if !slices.Contains(result.GetCapabilities(), "xray.runtime.info.read") {
		t.Fatalf("Xray runtime operation is absent: %v", result.GetCapabilities())
	}
	for _, capability := range []string{"xray.user.online.read", "xray.user.online.ips.list", "xray.users.online.list"} {
		if !slices.Contains(result.GetCapabilities(), capability) {
			t.Fatalf("online operation absent: %s", capability)
		}
	}
	if !slices.Contains(result.GetCapabilities(), "xray.traffic.counter.list") {
		t.Fatalf("Xray traffic collection operation is absent: %v", result.GetCapabilities())
	}
	if !slices.Contains(result.GetCapabilities(), "xray.traffic.counter.read") {
		t.Fatalf("Xray traffic operation is absent: %v", result.GetCapabilities())
	}
	if !slices.Contains(result.GetCapabilities(), "xray.traffic.counter.reset") {
		t.Fatalf("reset operation missing from installed capabilities: %v", result.GetCapabilities())
	}
	if !slices.Contains(result.GetCapabilities(), "xray.traffic.counter.reset_collection") {
		t.Fatalf("scope reset missing from installed capabilities: %v", result.GetCapabilities())
	}
	probe, err := healthv1.NewHealthClient(connection).Check(ctx, &healthv1.HealthCheckRequest{Service: nodev1.NodeService_ServiceDesc.ServiceName})
	if err != nil || probe.GetStatus() != healthv1.HealthCheckResponse_SERVING {
		t.Fatalf("runtime not ready: %v, %v", probe, err)
	}
	for _, name := range []string{"missing", "untrusted", "server-untrusted"} {
		t.Run(name, func(t *testing.T) {
			tlsConfig := certificates.ClientTLS(t)
			if name == "missing" {
				tlsConfig.Certificates = nil
			} else if name == "untrusted" {
				tlsConfig.Certificates = testutil.NewCertificates(t).ClientTLS(t).Certificates
			} else {
				tlsConfig.RootCAs = testutil.NewCertificates(t).ClientTLS(t).RootCAs
			}
			client, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			deadline, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			if _, err := nodev1.NewNodeServiceClient(client).GetNodeInfo(deadline, &nodev1.GetNodeInfoRequest{}); err == nil {
				t.Fatal("untrusted client reached application")
			}
		})
	}
	if err := application.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("shutdown leaked listener: %v", err)
	}
	listener.Close()
}

func TestCanceledStartupClosesOwnedResource(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var address string
	err := bootstrap.Run(ctx, certificates.Config(), "test", fx.NopLogger,
		fx.Invoke(func(lifecycle fx.Lifecycle, server *rpc.Server) {
			lifecycle.Append(fx.Hook{OnStart: func(context.Context) error {
				address = server.Addr().String()
				cancel()
				return context.Canceled
			}})
		}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation lost: %v", err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("canceled startup leaked listener: %v", err)
	}
	listener.Close()
}

func TestStartupRollback(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	var server *rpc.Server
	failed := errors.New("later startup hook failed")
	application := bootstrap.New(certificates.Config(), "test", fx.Populate(&server), fx.NopLogger,
		fx.Invoke(func(lifecycle fx.Lifecycle) {
			lifecycle.Append(fx.Hook{OnStart: func(context.Context) error { return failed }})
		}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := application.Start(ctx); !errors.Is(err, failed) {
		t.Fatalf("startup cause lost: %v", err)
	}
	listener, err := net.Listen("tcp", server.Addr().String())
	if err != nil {
		t.Fatalf("rollback leaked listener: %v", err)
	}
	listener.Close()
}
