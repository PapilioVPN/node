package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWireGuardHTTPPeerDeltaChunks(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("requires an isolated network namespace")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-Urn", os.Args[0], "-test.run=^TestWireGuardHTTPPeerDeltaChunks$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated HTTP peer chunks: %v\n%s", err, output)
		}
		return
	}
	f := startFixture(t, true)
	oldKey, newKey := [32]byte{10, 1}, [32]byte{11, 1}
	oldPeer := base64.StdEncoding.EncodeToString(oldKey[:])
	newPeer := base64.StdEncoding.EncodeToString(newKey[:])
	source := fmt.Appendf(nil, `{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"],"initial_peers":[{"public_key":%q,"allowed_ips":["192.0.2.2/32"]}]}`, oldPeer)
	executionRequest(t, f, http.MethodPut, source, http.StatusOK)
	first := fmt.Sprintf("{\"index\":0,\"remove\":[%q]}\n", oldPeer)
	last := fmt.Sprintf("{\"index\":1,\"last\":true,\"upsert\":[{\"public_key\":%q,\"allowed_ips\":[\"192.0.2.2/32\"]}]}\n", newPeer)
	before := wgOutput(t, "allowed-ips")

	for _, invalid := range []string{first, last, first + last + first, first + "{\"index\":1,\"last\":true"} {
		assertChunkResponse(t, sendPeerChunks(t, f, strings.NewReader(invalid)), http.StatusBadRequest)
		if got := wgOutput(t, "allowed-ips"); got != before {
			t.Fatalf("invalid stream changed peers: %q", got)
		}
	}
	assertChunkResponse(t, sendPeerChunks(t, f, strings.NewReader(first+last+strings.Repeat(" ", lifecycle.MaxConfigBytes))), http.StatusRequestEntityTooLarge)
	if got := wgOutput(t, "allowed-ips"); got != before {
		t.Fatalf("oversized stream changed peers: %q", got)
	}

	reader, writer := io.Pipe()
	t.Cleanup(func() { writer.Close() })
	result := chunkRequest(t, f, reader)
	if _, err := io.WriteString(writer, first+last); err != nil {
		t.Fatal(err)
	}
	if got := wgOutput(t, "allowed-ips"); got != before {
		t.Fatalf("peers changed before client EOF: %q", got)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	resp := waitChunkResponse(t, result)
	if resp.StatusCode != http.StatusOK {
		assertChunkResponse(t, resp, http.StatusOK)
	}
	defer resp.Body.Close()
	var reply peerSyncReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil || reply != (peerSyncReply{Added: 1, Removed: 1}) {
		t.Fatalf("peer chunk result = %+v, %v", reply, err)
	}
	applied := wgOutput(t, "allowed-ips")
	if !strings.Contains(applied, newPeer+"\t192.0.2.2/32") || strings.Contains(applied, oldPeer) {
		t.Fatalf("unexpected kernel peers after stream: %q", applied)
	}

	reader, writer = io.Pipe()
	t.Cleanup(func() { writer.Close() })
	result = chunkRequest(t, f, reader)
	if _, err := fmt.Fprintf(writer, "{\"index\":0,\"last\":true,\"remove\":[%q]}\n", newPeer); err != nil {
		t.Fatal(err)
	}
	if err := writer.CloseWithError(io.ErrUnexpectedEOF); err != nil {
		t.Fatal(err)
	}
	<-result // no clean EOF was sent, so an HTTP response is not required
	if got := wgOutput(t, "allowed-ips"); got != applied {
		t.Fatalf("aborted upload changed peers: %q", got)
	}
	client := f.grpcClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	legacy, err := client.ApplyPeerDeltaStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Send(&interfacesv1.WireGuardPeerDeltaChunk{Name: interfaceName, Last: true, Remove: [][]byte{newKey[:]}}); err != nil {
		t.Fatal(err)
	}
	if reply, err := legacy.CloseAndRecv(); status.Code(err) != codes.OK || reply.GetRemoved() != 1 {
		t.Fatalf("gRPC after aborted HTTP = %v, %v", reply, err)
	}
}

func sendPeerChunks(t *testing.T, f fixture, body io.Reader) *http.Response {
	t.Helper()
	return waitChunkResponse(t, chunkRequest(t, f, body))
}

type chunkReply struct {
	response *http.Response
	err      error
}

func chunkRequest(t *testing.T, f fixture, body io.Reader) <-chan chunkReply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, f.address+interfaceURL+"/peers/chunks", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	done := make(chan chunkReply, 1)
	go func() {
		resp, err := f.http.Do(req)
		done <- chunkReply{resp, err}
	}()
	return done
}

func waitChunkResponse(t *testing.T, done <-chan chunkReply) *http.Response {
	t.Helper()
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result.response
}

func assertChunkResponse(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("peer stream HTTP %d, want %d (%s)", resp.StatusCode, want, body)
	}
}
