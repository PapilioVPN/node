package integration_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWireGuardPeerStreamAndDefaultKey(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("requires an isolated network namespace")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-Urn", os.Args[0], "-test.run=^TestWireGuardPeerStreamAndDefaultKey$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated peer stream: %v\n%s", err, output)
		}
		return
	}
	initial, omitted, cleared := [32]byte{3, 3}, [32]byte{4, 4}, [32]byte{5, 5}
	key := [32]byte{6, 6}
	keyFile := filepath.Join(t.TempDir(), "peer-key")
	encodedKey := base64.StdEncoding.EncodeToString(key[:])
	if err := os.WriteFile(keyFile, []byte(encodedKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f := startFixtureConfigured(t, true, fmt.Sprintf("  peer_preshared_key_file: %q\n", keyFile))
	initialName := base64.StdEncoding.EncodeToString(initial[:])
	payload := fmt.Appendf(nil, `{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"],"initial_peers":[{"public_key":%q}]}`, initialName)
	executionRequest(t, f, http.MethodPut, payload, http.StatusOK)
	before := wgOutput(t, "preshared-keys")
	if !strings.Contains(before, initialName+"\t"+encodedKey) {
		t.Fatal("initial peer did not receive the default key")
	}

	client := f.grpcClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	stream, err := client.ApplyPeerDeltaStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := &interfacesv1.WireGuardPeerDeltaChunk{Name: interfaceName, Upsert: []*interfacesv1.WireGuardPeer{{PublicKey: omitted[:]}}}
	if err := stream.Send(first); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.CloseAndRecv(); status.Code(err) != codes.InvalidArgument || wgOutput(t, "preshared-keys") != before {
		t.Fatalf("incomplete stream returned %v or wrote peers", err)
	}

	stream, err = client.ApplyPeerDeltaStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(first); err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&interfacesv1.WireGuardPeerDeltaChunk{
		Name: interfaceName, Index: 1, Last: true, Upsert: []*interfacesv1.WireGuardPeer{
			{PublicKey: cleared[:], PresharedKey: make([]byte, 32)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if wgOutput(t, "preshared-keys") != before {
		t.Fatal("stream applied changes before the client half-closed")
	}
	result, err := stream.CloseAndRecv()
	if err != nil || result.GetAdded() != 2 {
		t.Fatalf("peer stream: %v, %v", result, err)
	}
	applied := wgOutput(t, "preshared-keys")
	for _, want := range []string{
		base64.StdEncoding.EncodeToString(omitted[:]) + "\t" + encodedKey,
		base64.StdEncoding.EncodeToString(cleared[:]) + "\t(none)",
	} {
		if !strings.Contains(applied, want) {
			t.Fatal("stream did not apply the requested preshared-key policy")
		}
	}
}
