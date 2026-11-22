//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	stats "github.com/xtls/xray-core/app/stats/command"
)

func TestLostManagementResponseReadbackWithoutReplay(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), transport, fixtureOptions{relay: true})
			f.sendTraffic(t)
			initial := invokeDecode[resetResult](t, f, transport, "reset_status", nil)
			input := resetInput(initial, "user", account, "lost-external-"+transport)
			input["direction"] = "uplink"
			f.relay.pauseAt.Store(1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				switch transport {
				case "http":
					data, _ := json.Marshal(input)
					request, _ := http.NewRequestWithContext(ctx, "POST", f.address+"/v1/xray/observability/instances/edge-a/traffic/counter-resets", bytes.NewReader(data))
					request.Header.Set("Content-Type", "application/json")
					response, err := f.http.Do(request)
					if response != nil {
						response.Body.Close()
					}
					result <- err
				case "grpc":
					revision := initial.Revision
					_, err := f.grpc.ResetTrafficCounter(ctx, &v1.ResetTrafficRequest{InstanceId: "edge-a", Epoch: initial.Epoch, OperationId: input["operation_id"].(string), ExpectedRevision: &revision, Scope: v1.Scope_SCOPE_USER, Name: account, Direction: v1.Direction_DIRECTION_UPLINK})
					result <- err
				case "nats":
					values := map[string]any{}
					for k, v := range input {
						values[k] = v
					}
					values["instance_id"] = "edge-a"
					data, _ := json.Marshal(values)
					_, err := f.nats.RequestWithContext(ctx, "fixture.traffic.xray.observability.traffic.v1.reset_counter", data)
					result <- err
				}
			}()
			t.Cleanup(func() { cancel(); <-done })
			select {
			case <-f.relay.paused:
			case <-time.After(2 * time.Second):
				t.Fatal("real native reset not reached")
			}
			// The native operation is already applied, and its response is held back.
			nativeCtx, nativeCancel := context.WithTimeout(context.Background(), time.Second)
			native, err := f.api.GetStats(nativeCtx, &stats.GetStatsRequest{Name: "user>>>" + account + ">>>traffic>>>uplink"})
			nativeCancel()
			if err != nil || native.Stat.Value != 0 {
				t.Fatal("native effect absent", native, err)
			}
			cancel()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("caller unexpectedly got response")
				}
			case <-time.After(time.Second):
				t.Fatal("caller did not disconnect")
			}
			close(f.relay.release)
			deadline := time.Now().Add(2 * time.Second)
			var report resetResult
			for {
				report = invokeDecode[resetResult](t, f, transport, "reset_status", nil)
				if report.Outcome != "in_progress" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("operation never finished", report)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if report.OperationID != input["operation_id"] || report.Revision != initial.Revision+1 || (report.Outcome != "completed" && report.Outcome != "unknown") {
				t.Fatal(report)
			}
			// New traffic after the first reset must survive identical request replay.
			f.sendTraffic(t)
			duplicate := invokeDecode[resetResult](t, f, transport, "reset_counter", input)
			if duplicate.Revision != report.Revision || f.relay.resets.Load() != 1 {
				t.Fatal("lost response replayed", duplicate)
			}
			value := f.read(t, transport, domain.Selection{Scope: domain.User, Name: account, Direction: domain.Uplink})
			if value.Bytes <= 0 {
				t.Fatal("replay destroyed subsequent traffic", value)
			}
			t.Logf("%s lost response: revision=%s outcome=%s resets=1", transport, fmt.Sprint(report.Revision), report.Outcome)
		})
	}
}
