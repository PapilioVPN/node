package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	configsv1 "github.com/PapilioVPN/node/internal/engines/hysteria/configs/bridge/grpc/proto/v1"
	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type status struct {
	State   string `json:"state"`
	Version string `json:"hysteria_version"`
	Action  string `json:"start_action"`
	Digest  string `json:"applied_config_sha256"`
}

func readStatus(t *testing.T, data []byte) status {
	t.Helper()
	var s status
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNativeLifecycleHTTP(t *testing.T) {
	f := setup(t, "")
	if s := readStatus(t, f.request(t, http.MethodGet, "", nil, 200)); s.State != "stopped" || s.Digest != "" {
		t.Fatalf("initial status: %+v", s)
	}
	valid := f.request(t, http.MethodPost, "/config-validations", f.source, 200)
	if !bytes.Contains(valid, []byte(`"valid":true`)) {
		t.Fatal("valid config failed Node preflight")
	}
	rejected := f.request(t, http.MethodPost, "/config-validations", []byte("auth: [fixture-rejected-secret"), 200)
	if !bytes.Contains(rejected, []byte(`"valid":false`)) || bytes.Contains(rejected, []byte("fixture-rejected-secret")) {
		t.Fatal("invalid config result or secret disclosure")
	}
	revision := sha256.Sum256(f.source)
	want := hex.EncodeToString(revision[:])
	if s := readStatus(t, f.request(t, http.MethodPut, "/execution", nil, 200)); s.State != "ready" || s.Version != "v2.14.0" || s.Action != "started" || s.Digest != want {
		t.Fatalf("started status: %+v", s)
	}
	if s := readStatus(t, f.request(t, http.MethodPut, "/execution", f.source, 200)); s.State != "ready" || s.Action != "heartbeat" || s.Digest != want {
		t.Fatalf("repeated status: %+v", s)
	}
	f.request(t, http.MethodPut, "/execution", []byte("not valid YAML: ["), 400)
	if s := readStatus(t, f.request(t, http.MethodGet, "", nil, 200)); s.State != "ready" || s.Digest != want {
		t.Fatalf("rejection changed active status: %+v", s)
	}
	if s := readStatus(t, f.request(t, http.MethodDelete, "/execution", nil, 200)); s.State != "stopped" || s.Digest != "" {
		t.Fatalf("stopped status: %+v", s)
	}
	f.request(t, http.MethodDelete, "/execution", nil, 200)
	assertClean(t, f)
}

func TestNativeStartRejectsOccupiedListeners(t *testing.T) {
	for _, native := range []string{"udp", "stats"} {
		t.Run(native, func(t *testing.T) {
			f := setup(t, "")
			if native == "udp" {
				l, err := net.ListenPacket("udp4", f.udp)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = l.Close() })
			} else {
				l, err := net.Listen("tcp4", f.stats)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = l.Close() })
			}
			body := f.request(t, http.MethodPut, "/execution", f.source, 503)
			if bytes.Contains(body, []byte(statsSecret)) || bytes.Contains(body, []byte(password)) || bytes.Contains(body, []byte(f.dir)) {
				t.Fatal("start leaked private details")
			}
			if s := readStatus(t, f.request(t, http.MethodGet, "", nil, 200)); s.State != "failed" || s.Digest != "" {
				t.Fatalf("failed status: %+v", s)
			}
			entries, err := os.ReadDir(f.work)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed start retained a private candidate")
			}
		})
	}
}

func assertClean(t *testing.T, f *fixture) {
	t.Helper()
	tcp, err := net.Listen("tcp4", f.stats)
	if err != nil {
		t.Fatal("stats listener not released")
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp4", f.udp)
	if err != nil {
		t.Fatal("UDP listener not released")
	}
	defer udp.Close()
	entries, err := os.ReadDir(f.work)
	if err != nil || len(entries) != 0 {
		t.Fatal("runtime directory not empty")
	}
}

func TestNativeLifecycleGRPC(t *testing.T) {
	f := setup(t, "")
	conn := f.grpcConn(t)
	instances := v1.NewHysteriaInstancesServiceClient(conn)
	configs := configsv1.NewHysteriaConfigsServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := configs.ValidateConfig(ctx, &configsv1.ValidateHysteriaConfigRequest{Id: "edge", ConfigYaml: f.source})
	if err != nil || !res.GetValid() || res.GetScope() != "node_preflight" {
		t.Fatalf("validation failed: %v", err)
	}
	_, err = configs.ValidateConfig(ctx, &configsv1.ValidateHysteriaConfigRequest{Id: "edge", ConfigYaml: []byte{}})
	if grpcstatus.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty validation returned %v", err)
	}
	res, err = configs.ValidateConfig(ctx, &configsv1.ValidateHysteriaConfigRequest{Id: "edge", ConfigYaml: append(bytes.Clone(f.source), []byte("obfs: {type: fixture-native-rejection}\n")...)})
	if err != nil || !res.GetValid() {
		t.Fatal("preflight incorrectly claims native obfs validation")
	}
	_, err = instances.Start(ctx, &v1.StartHysteriaRequest{Id: "edge", ConfigYaml: append(bytes.Clone(f.source), []byte("obfs: {type: fixture-native-rejection}\n")...)})
	if grpcstatus.Code(err) != codes.Unavailable || bytes.Contains([]byte(err.Error()), []byte("fixture-native-rejection")) {
		t.Fatal("native config rejection failed or leaked detail")
	}
	result, err := instances.Start(ctx, &v1.StartHysteriaRequest{Id: "edge", ConfigYaml: f.source})
	revision := sha256.Sum256(f.source)
	if err != nil || result.GetAppliedConfigSha256() != hex.EncodeToString(revision[:]) || result.GetStartAction() != v1.HysteriaStartAction_HYSTERIA_START_ACTION_RECOVERED {
		t.Fatalf("start did not confirm source: %v", err)
	}
	result, err = instances.GetStatus(ctx, &v1.HysteriaInstanceRequest{Id: "edge"})
	if err != nil || result.GetState() != v1.HysteriaInstanceState_HYSTERIA_INSTANCE_STATE_READY || result.GetAppliedConfigSha256() != hex.EncodeToString(revision[:]) {
		t.Fatalf("status was not ready: %v", err)
	}
	replacement := append(bytes.Clone(f.source), []byte("# changed exact bytes\n")...)
	revision = sha256.Sum256(replacement)
	result, err = instances.Start(ctx, &v1.StartHysteriaRequest{Id: "edge", ConfigYaml: replacement})
	if err != nil || result.GetAppliedConfigSha256() != hex.EncodeToString(revision[:]) || result.GetStartAction() != v1.HysteriaStartAction_HYSTERIA_START_ACTION_RESTARTED {
		t.Fatalf("replacement was not confirmed: %v", err)
	}
	result, err = instances.Stop(ctx, &v1.HysteriaInstanceRequest{Id: "edge"})
	if err != nil || result.GetState() != v1.HysteriaInstanceState_HYSTERIA_INSTANCE_STATE_STOPPED || result.AppliedConfigSha256 != nil {
		t.Fatalf("stop did not clear confirmation: %v", err)
	}
	assertClean(t, f)
}

func TestCanceledStartReapsAndRemovesCandidate(t *testing.T) {
	var marker string
	f := setupWithExecutable(t, "", func(f *fixture) string {
		wrapper := filepath.Join(f.dir, "server-wrapper")
		marker = filepath.Join(f.dir, "blocked")
		// The first server invocation stops itself deterministically before
		// exec. This tests joining a real canceled child, not QUIC readiness.
		script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = server ] && [ ! -e %q ]; then\n echo $$ > %q\n kill -STOP $$\nfi\nexec %q \"$@\"\n", marker, marker, f.binary)
		if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		return wrapper
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, f.http.URL+instance+"/execution", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", apiKey)
	done := make(chan error, 1)
	go func() {
		resp, err := f.http.Client().Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- err
	}()
	var pid int
	until(t, func() bool {
		data, _ := os.ReadFile(marker)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		return pid > 0
	})
	if s := readStatus(t, f.request(t, http.MethodGet, "", nil, 200)); s.State != "starting" || s.Digest != "" {
		t.Fatalf("premature confirmation: %+v", s)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel returned %v", err)
	}
	until(t, func() bool {
		_, err := os.Stat("/proc/" + strconv.Itoa(pid))
		return errors.Is(err, os.ErrNotExist)
	})
	until(t, func() bool {
		return readStatus(t, f.request(t, http.MethodGet, "", nil, 200)).State == "stopped"
	})
	assertClean(t, f)
	f.request(t, http.MethodPut, "/execution", nil, 200)
	f.request(t, http.MethodDelete, "/execution", nil, 200)
	assertClean(t, f)
}

func until(t testing.TB, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not observed before timeout")
		}
		time.Sleep(15 * time.Millisecond)
	}
}
