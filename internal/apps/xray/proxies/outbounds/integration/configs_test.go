//go:build integration && linux

package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRestartPlanUsesExistingConfigsWithDefaultAndConnectionEffects(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			streamTarget := tunnelTarget(t)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
			defer target.Close()
			f := newFixture(t, pki, broker, transport)
			address := configureProbe(t, f, false)
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			f.config.Instances[0].Xray.StateDirectory = t.TempDir()
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if f.config.Communications.HTTP != nil {
				f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			proxy, _ := url.Parse("http://" + address)
			tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			traffic := &http.Client{Transport: tr, Timeout: time.Second}
			requireTraffic(t, traffic, target.URL, true)
			stream, upstream := tunnel(t, address, streamTarget)
			tunnelPayload(t, stream, upstream, "before-config-restart")
			plan := planReplacement(t, c, transport, "baseline", candidate(t, "baseline", "blackhole"), "preserve", "terminate")
			if plan.Mode != "configs_restart" {
				t.Fatal(plan)
			}
			rejected := applyReplacement(t, c, transport, plan.ID)
			if rejected.Outcome != "not_applied" || rejected.Steps[0].Attempted {
				t.Fatal(rejected)
			}
			original, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			var configuration map[string]any
			if err := json.Unmarshal(original, &configuration); err != nil {
				t.Fatal(err)
			}
			configuration["outbounds"] = []any{map[string]any{"tag": "baseline", "protocol": "blackhole"}, map[string]any{"tag": "backup", "protocol": "freedom"}}
			replacement, _ := json.Marshal(configuration)
			change := replaceConfiguration(t, c, transport, 0, replacement)
			if change.Error != "" || !change.Persistence.Durable || !change.Persistence.Published || change.Persistence.Metadata.Revision != 1 || change.Execution == nil || change.Execution.Status.Generation != 2 {
				t.Fatal(change)
			}
			awaitNative(t, f.native)
			requireTraffic(t, traffic, target.URL, false)
			// The desired first outbound remains the default after restart; the installed
			// freedom backup is not silently selected. Restart terminates old streams.
			stream.SetReadDeadline(time.Now().Add(time.Second))
			var b [1]byte
			if n, err := stream.Read(b[:]); n != 0 || err == nil {
				t.Fatal("old connection survived execution replacement", n, err)
			}
			after, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil || !bytes.Equal(after, original) {
				t.Fatal("operator baseline rewritten", err)
			}
			restored := replaceConfiguration(t, c, transport, 1, original)
			if restored.Error != "" || restored.Persistence.Metadata.Revision != 2 {
				t.Fatal(restored)
			}
			awaitNative(t, f.native)
			requireTraffic(t, traffic, target.URL, true)
		})
	}
}
