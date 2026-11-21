//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeConfigurationCompilationOverIndependentTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := nodeFixture(t, pki, broker, transport)
			client := startNode(t, f)
			awaitTraffic(t, f)
			exerciseCompilation(t, client, transport, f)
		})
	}
}

type compilationResponse struct {
	InstanceID string    `json:"instance_id"`
	Content    []byte    `json:"content"`
	SHA256     string    `json:"sha256"`
	PreparedAt time.Time `json:"prepared_at"`
	DurationNS string    `json:"duration_ns"`
}

func exerciseCompilation(t *testing.T, client clients, transport string, f fixture) {
	t.Helper()
	// Keep the new artifact's endpoint reserved until compilation returns. Client
	// connection setup cannot accidentally consume the prospective engine port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	numericPort, _ := strconv.Atoi(port)
	var native map[string]any
	if err := json.Unmarshal(f.original, &native); err != nil {
		t.Fatal(err)
	}
	native["inbounds"].([]any)[0].(map[string]any)["port"] = numericPort
	content, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	sources := []domain.Source{{Name: "base.json", Content: content}, {Name: "extra.yaml", Content: []byte("log:\n  loglevel: none\n")}}
	result := compileSources(t, client, transport, sources)
	digest := sha256.Sum256(result.Content)
	if result.InstanceID != "edge-a" || result.SHA256 != hex.EncodeToString(digest[:]) || result.PreparedAt.IsZero() {
		t.Fatal("compiled artifact identity/timing is invalid")
	}
	duration, err := strconv.ParseInt(result.DurationNS, 10, 64)
	if err != nil || duration < 0 {
		t.Fatal("compiled duration lost integer precision")
	}
	if err := traffic(f.proxy, f.target.URL); err != nil {
		t.Fatal("compilation interrupted original instance")
	}
	path := filepath.Join(f.directory, "compiled.pb")
	if err := os.WriteFile(path, result.Content, 0600); err != nil {
		t.Fatal(err)
	}
	artifact, err := process.New(process.Config{Binary: f.config.Instances[0].Xray.Binary, Configuration: path, StopTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := artifact.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := artifact.Start(ctx); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if traffic(address, f.target.URL) == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("compiled protobuf did not serve real traffic: %+v", artifact.Observe())
		case <-tick.C:
		}
	}
	if err := artifact.Close(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(filepath.Join(f.directory, "active.json"))
	if err != nil || !bytes.Equal(current, f.original) {
		t.Fatal("compilation changed operator source")
	}
	entries, err := os.ReadDir(f.config.Instances[0].Xray.WorkDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("compiled staging leaked: %v %v", entries, err)
	}
	if err := traffic(f.proxy, f.target.URL); err != nil {
		t.Fatal("artifact execution affected the original instance")
	}
	empty := compileSources(t, client, transport, []domain.Source{{Name: "empty.pb"}})
	emptyDigest := sha256.Sum256(nil)
	if len(empty.Content) != 0 || empty.SHA256 != hex.EncodeToString(emptyDigest[:]) {
		t.Fatal("empty native protobuf changed across the public transport")
	}
}

func compileSources(t *testing.T, client clients, transport string, sources []domain.Source) compilationResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if transport == "grpc" {
		input := &configsv1.CompileConfigurationRequest{InstanceId: "edge-a"}
		for _, source := range sources {
			input.Sources = append(input.Sources, &configsv1.ConfigurationSource{Name: source.Name, Content: source.Content})
		}
		result, err := configsv1.NewConfigurationsServiceClient(client.grpc).CompileConfiguration(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.PreparedAt == nil || result.PreparedAt.CheckValid() != nil {
			t.Fatal("compiled timestamp invalid")
		}
		return compilationResponse{result.InstanceId, result.Content, result.Sha256, result.PreparedAt.AsTime(), strconv.FormatInt(result.DurationNs, 10)}
	}
	var wireSources []map[string]any
	for _, source := range sources {
		wireSources = append(wireSources, map[string]any{"name": source.Name, "content": source.Content})
	}
	payload, _ := json.Marshal(map[string]any{"instance_id": "edge-a", "sources": wireSources})
	var data []byte
	if transport == "http" {
		request, _ := http.NewRequestWithContext(ctx, "POST", client.address+"/v1/xray/runtime/config-compilations", bytes.NewReader(payload))
		response, err := client.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP compilation status=%d", response.StatusCode)
		}
	} else {
		message, err := client.nats.RequestWithContext(ctx, "fixture.configs.xray.runtime.configs.v1.compile", payload)
		if err != nil {
			t.Fatal(err)
		}
		data = message.Data
	}
	var result compilationResponse
	if err := json.Unmarshal(data, &result); err != nil || result.InstanceID == "" {
		t.Fatalf("compilation returned no artifact: %v", err)
	}
	return result
}
