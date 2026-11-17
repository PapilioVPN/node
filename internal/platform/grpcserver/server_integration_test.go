//go:build integration

package grpcserver_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/system/info/grpcapi"
	nodev1 "github.com/PapilioVPN/node/internal/modules/system/info/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRequestDeadlineAndMessageLimit(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config().Communications.GRPC
	cfg.RequestTimeout = 30 * time.Millisecond
	server, err := rpc.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodev1.RegisterNodeServiceServer(server.Registrar(), grpcapi.Handler{Info: infoReader(func(ctx context.Context) (domain.NodeInfo, error) {
		<-ctx.Done()
		return domain.NodeInfo{}, ctx.Err()
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	client, err := grpc.NewClient(server.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = nodev1.NewNodeServiceClient(client).GetNodeInfo(ctx, &nodev1.GetNodeInfoRequest{})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("configured request deadline was not enforced: %v", err)
	}
	large := &nodev1.GetNodeInfoRequest{}
	large.ProtoReflect().SetUnknown(bytes.Repeat([]byte{0x08, 0x01}, cfg.MaxReceiveBytes))
	_, err = nodev1.NewNodeServiceClient(client).GetNodeInfo(ctx, large)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("configured message limit was not enforced: %v", err)
	}
}

func TestStopForcesOpenHealthStreamAndJoins(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	server, err := rpc.NewServer(certificates.Config().Communications.GRPC, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })
	address := server.Addr().String()
	client, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	watch, err := healthv1.NewHealthClient(client).Watch(ctx, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := watch.Recv(); err != nil {
		t.Fatal(err)
	}
	stop, cancelStop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancelStop()
	if err := server.Stop(stop); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("open stream did not reach bounded force-stop: %v", err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("force-stop leaked listener: %v", err)
	}
	listener.Close()
	// The final NOT_SERVING notification may precede the stream's close.
	for {
		if _, err := watch.Recv(); err != nil {
			break
		}
	}
}

type infoReader func(context.Context) (domain.NodeInfo, error)

func (f infoReader) GetNodeInfo(ctx context.Context) (domain.NodeInfo, error) { return f(ctx) }
