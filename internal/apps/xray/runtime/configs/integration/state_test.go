//go:build integration && linux

package integration_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestDesiredSourcesApplyConflictReapplyAndNodeRecovery(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := nodeFixture(t, pki, broker, transport)
			f.config.Instances[0].Xray.StateDirectory = testutil.NewPrivateDirectory(t)
			client := startNode(t, f)
			awaitTraffic(t, f)
			assertExecutionRevision(t, client, transport, "", "1")
			metadata := stateCall(t, client, transport, "status", 0, nil)
			if metadata.Error != "" || metadata.Metadata.Revision != "0" || metadata.Metadata.SourceCount != 0 {
				t.Fatalf("baseline %+v", metadata)
			}
			var managed map[string]any
			if err := json.Unmarshal(f.original, &managed); err != nil {
				t.Fatal(err)
			}
			managed["env"] = map[string]string{"xray.location.cert": filepath.Dir(pki.Client.CA)}
			managed["outbounds"] = append(managed["outbounds"].([]any), map[string]any{"tag": "unused-cert-outbound", "protocol": "freedom", "streamSettings": map[string]any{"security": "tls", "tlsSettings": map[string]any{"certificates": []any{map[string]any{"certificateFile": filepath.Base(pki.Client.CA), "usage": "verify"}}}}})
			managedContent, _ := json.Marshal(managed)
			sources := []domain.Source{{Name: "base.json", Content: managedContent}, {Name: "tail-extra.yaml", Content: []byte("log:\n  loglevel: none\n")}}
			exerciseStateReplacement(t, client, transport, sources, 0, "1", "2")
			awaitTraffic(t, f)
			rejected := stateCall(t, client, transport, "replace", 1, []domain.Source{{Name: "bad.json", Content: []byte(`{"inbounds":[{"protocol":"does-not-exist"}]}`)}})
			if rejected.Error != "native_rejected" || rejected.Persistence.Published || rejected.Execution != nil {
				t.Fatalf("invalid source effects %+v", rejected)
			}
			stale := stateCall(t, client, transport, "replace", 0, sources)
			if stale.Error != "revision_conflict" || stale.Execution != nil || stale.Persistence.Published {
				t.Fatalf("stale revision effects %+v", stale)
			}
			applied := stateCall(t, client, transport, "apply", 1, nil)
			if applied.Error != "" || applied.Persistence.Published || !applied.Persistence.Durable || applied.Execution == nil || applied.Execution.Status.Generation != "3" {
				t.Fatalf("reapply %+v", applied)
			}
			awaitTraffic(t, f)
			original, err := os.ReadFile(filepath.Join(f.directory, "active.json"))
			if err != nil || !bytes.Equal(original, f.original) {
				t.Fatal("operator baseline was overwritten")
			}
			client.stop()
			assertWorkEmpty(t, f)
			recovered := startNode(t, f)
			awaitTraffic(t, f)
			// A clean stop replaces the child on startup and advances its durable generation.
			assertExecutionRevision(t, recovered, transport, "1", "4")
			desired := stateCall(t, recovered, transport, "get", 0, nil)
			assertDesired(t, desired, sources, "1")
			// After a real Node restart, execution must again use the persisted sources.
			// A new desired revision switches real native traffic to a blackhole outbound.
			var native map[string]any
			if err := json.Unmarshal(f.original, &native); err != nil {
				t.Fatal(err)
			}
			native["outbounds"] = []any{map[string]any{"protocol": "blackhole"}}
			blocked, _ := json.Marshal(native)
			exerciseStateReplacement(t, recovered, transport, []domain.Source{{Name: "blocked.json", Content: blocked}}, 1, "2", "5")
			if err := traffic(f.proxy, f.target.URL); err == nil {
				t.Fatal("new configuration did not affect actual traffic")
			}
			recovered.stop()
			assertWorkEmpty(t, f)
			again := startNode(t, f)
			awaitProxy(t, f)
			assertExecutionRevision(t, again, transport, "2", "6")
			if traffic(f.proxy, f.target.URL) == nil {
				t.Fatal("blackhole desired source was not restored after Node restart")
			}
			desired = stateCall(t, again, transport, "get", 0, nil)
			if desired.Metadata.Revision != "2" {
				t.Fatal("latest desired revision lost on Node restart")
			}
			// Applying freedom after recovery requires stopping the restored blackhole
			// child first; its generation demonstrates one restored owned execution.
			exerciseStateReplacement(t, again, transport, sources, 2, "3", "7")
			awaitTraffic(t, f)
			again.stop()
			assertWorkEmpty(t, f)
		})
	}
}
func exerciseStateReplacement(t *testing.T, client clients, transport string, sources []domain.Source, expected uint64, revision, generation string) {
	t.Helper()
	change := stateCall(t, client, transport, "replace", expected, sources)
	if change.Error != "" || !change.Persistence.Published || !change.Persistence.Durable || change.Persistence.Metadata.Revision != revision || change.Execution == nil || change.Execution.Effect != "completed" || change.Execution.Status == nil || change.Execution.Status.Revision != revision || change.Execution.Status.Generation != generation {
		t.Fatalf("change=%+v", change)
	}
	desired := stateCall(t, client, transport, "get", 0, nil)
	assertDesired(t, desired, sources, revision)
	metadata := stateCall(t, client, transport, "status", 0, nil)
	if metadata.Error != "" || metadata.Metadata != desired.Metadata || len(metadata.Sources) != 0 {
		t.Fatal("metadata projection fetched source content or disagrees")
	}
}
func assertDesired(t *testing.T, desired stateResponse, sources []domain.Source, revision string) {
	t.Helper()
	if desired.Error != "" || desired.Metadata.Revision != revision || !desired.Metadata.Durable || desired.Metadata.SourceCount != len(sources) || len(desired.Metadata.SHA256) != 64 || len(desired.Sources) != len(sources) {
		t.Fatalf("desired=%+v", desired)
	}
	for i, s := range sources {
		if desired.Sources[i].Name != s.Name || !bytes.Equal(desired.Sources[i].Content, s.Content) {
			t.Fatal("source name/order/bytes changed")
		}
	}
}
func assertWorkEmpty(t *testing.T, f fixture) {
	t.Helper()
	entries, err := os.ReadDir(f.config.Instances[0].Xray.WorkDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("staged execution sources leaked: %v %v", entries, err)
	}
}

func TestApplyRetainsDesiredRevisionAfterRealSpawnFailure(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := nodeFixture(t, pki, broker, transport)
			binary := f.config.Instances[0].Xray.Binary
			link := filepath.Join(f.directory, "xray")
			if err := os.Symlink(binary, link); err != nil {
				t.Fatal(err)
			}
			f.config.Instances[0].Xray.Binary = link
			f.config.Instances[0].Xray.StateDirectory = testutil.NewPrivateDirectory(t)
			client := startNode(t, f)
			awaitTraffic(t, f)
			sources := []domain.Source{{Name: "base.json", Content: f.original}}
			exerciseStateReplacement(t, client, transport, sources, 0, "1", "2")
			awaitTraffic(t, f)
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			failed := stateCall(t, client, transport, "apply", 1, nil)
			if failed.Error != "unavailable" || failed.Persistence.Published || !failed.Persistence.Durable || failed.Persistence.Metadata.Revision != "1" || failed.Execution == nil || failed.Execution.Effect != "partial" || failed.Execution.Status.State != "stopped" {
				t.Fatalf("spawn failure lost progress %+v", failed)
			}
			assertDesired(t, stateCall(t, client, transport, "get", 0, nil), sources, "1")
			assertWorkEmpty(t, f)
			if err := os.Symlink(binary, link); err != nil {
				t.Fatal(err)
			}
			retried := stateCall(t, client, transport, "apply", 1, nil)
			if retried.Error != "" || retried.Persistence.Published || retried.Persistence.Metadata.Revision != "1" || retried.Execution == nil || retried.Execution.Status.Generation != "3" {
				t.Fatalf("explicit retry %+v", retried)
			}
			awaitTraffic(t, f)
		})
	}
}
