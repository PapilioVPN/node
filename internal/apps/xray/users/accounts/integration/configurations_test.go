//go:build integration && linux

package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func TestAccountConfigurationArtifactsAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if h := f.config.Communications.HTTP; h != nil {
				h.WriteTimeout = 3 * time.Second
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			target := accountTarget(t)
			runtime := newFixture(t, pki, "", "http")
			address := allowAccountTraffic(t, runtime, target.Listener.Addr().String())
			original, err := os.ReadFile(runtime.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			result := prepareAccounts(t, c, transport, original, [][]byte{accountConfiguration(t, "prepared@example.test")})
			hash := sha256.Sum256(result.Content)
			if result.InstanceID != "edge-a" || result.SHA256 != hex.EncodeToString(hash[:]) || result.PreparedAt.IsZero() {
				t.Fatal(result)
			}
			assertFamilyCount(t, c, transport, 2) // Artifact preparation has no live effect.
			path := filepath.Join(t.TempDir(), "prepared.pb")
			if err := os.WriteFile(path, result.Content, 0600); err != nil {
				t.Fatal(err)
			}
			runtime.config.Instances[0].Xray.Configuration = path
			engine := startNode(t, runtime)
			awaitNative(t, runtime.native)
			assertFamilyCount(t, engine, "http", 1)
			connection, _, err := accountTunnel(address, managedID, target.Listener.Addr().String())
			if err != nil {
				t.Fatal("returned artifact did not authenticate", err)
			}
			connection.Close()
			cleared := prepareAccounts(t, c, transport, original, nil)
			var config core.Config
			if err := proto.Unmarshal(cleared.Content, &config); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, cleared.Content, 0600); err != nil {
				t.Fatal(err)
			}
			restartAccountInstance(t, engine)
			assertFamilyCount(t, engine, "http", 0)
			assertFamilyCount(t, c, transport, 2)
		})
	}
}
