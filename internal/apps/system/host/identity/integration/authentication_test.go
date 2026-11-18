//go:build integration && linux

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	identityv1 "github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestAllTransportsRejectMissingControllerCertificate(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki, "all")
	client := startConfiguredNode(t, pki, config)
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	identity.Certificates = nil
	transport := &http.Transport{TLSClientConfig: identity}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get(client.HTTPAddress)
	if err == nil {
		response.Body.Close()
		t.Fatal("HTTP accepted a missing controller certificate")
	}
	conn, err := grpc.NewClient(client.GRPC.Target(), grpc.WithTransportCredentials(credentials.NewTLS(identity)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := identityv1.NewHostIdentityServiceClient(conn).GetHostIdentity(ctx, &identityv1.GetHostIdentityRequest{}); err == nil {
		t.Fatal("gRPC accepted a missing controller certificate")
	}
	connection, err := nats.Connect(config.Communications.NATS.Servers[0], nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(time.Second))
	if err == nil {
		connection.Close()
		t.Fatal("broker accepted a missing controller certificate")
	}
}
