//go:build integration

package grpcapi_test

import (
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
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
