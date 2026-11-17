//go:build integration

package testutil

import (
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/nats-io/nats-server/v2/server"
)

// NewNATSBroker starts an isolated official broker with certificate mapping and
// ACLs restricted to fixture subjects. It never reads a deployment configuration.
func NewNATSBroker(t testing.TB, files mtls.Files, handshakeFirst bool) *server.Server {
	t.Helper()
	identity, err := mtls.Server(files)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TLSConfig: identity, TLSTimeout: 1, TLSMap: true, TLSHandshakeFirst: handshakeFirst,
		Users: []*server.User{{Username: "CN=client", Permissions: &server.Permissions{
			Publish:   &server.SubjectPermission{Allow: []string{"fixture.>", "_INBOX.>"}},
			Subscribe: &server.SubjectPermission{Allow: []string{"fixture.>", "_INBOX.>"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	go broker.Start()
	t.Cleanup(func() { broker.Shutdown(); broker.WaitForShutdown() })
	if !broker.ReadyForConnections(3 * time.Second) {
		t.Fatal("fixture NATS broker did not start")
	}
	return broker
}
