//go:build integration && linux

package bootstrap_test

import (
	"context"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func xrayClient(t *testing.T, server *rpc.Server, certificates testutil.Certificates) xrayv1.XrayServiceClient {
	t.Helper()
	connection, err := grpc.NewClient(server.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return xrayv1.NewXrayServiceClient(connection)
}

func TestXrayBinaryInfoRequiresItsOwnGrant(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.Permissions.ReadInstanceStatus = true
	cfg.Communications.GRPC.Permissions.ControlInstances = true
	cfg.Communications.GRPC.Permissions.ValidateInstanceConfig = true
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	client := xrayClient(t, server, certificates)
	for _, id := range []string{"unknown", ""} {
		if _, err := client.GetBinaryInfo(t.Context(), &xrayv1.GetBinaryInfoRequest{InstanceId: id}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("other instance grants must not authorize engine information: %v", err)
		}
	}
}
