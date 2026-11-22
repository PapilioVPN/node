//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestLostAccountAcknowledgementRequiresExecutionReplacement(t *testing.T) {
	target := accountTarget(t)
	pki := testutil.NewPKI(t)
	f := newFixture(t, pki, brokerFixture(t, pki), "all")
	address := allowAccountTraffic(t, f, target.Listener.Addr().String())
	relay, drop := testutil.LostReplies(t, f.native)
	f.config.Instances[0].Xray.API.Address = relay
	f.config.Communications.Requests.Timeout = 200 * time.Millisecond
	c := startNode(t, f)
	awaitNative(t, f.native)
	content := accountConfiguration(t, "managed@example.test")
	result, code := accountMutation(t, c, "http", "add", "", content)
	if code != "deadline_exceeded" || result.Effect != "unknown" || result.Acknowledged || !result.RequiresInstanceRestart {
		t.Fatal(result, code)
	}
	direct, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observed, err := handlers.NewHandlerServiceClient(direct).GetInboundUsers(ctx, &handlers.GetInboundUserRequest{Tag: "members", Email: "managed@example.test"})
	if err != nil || len(observed.GetUsers()) != 1 || observed.Users[0].Email != "managed@example.test" {
		t.Fatal(observed, err)
	}
	tunnel, _, err := accountTunnel(address, managedID, target.Listener.Addr().String())
	if err != nil {
		t.Fatal("unacknowledged addition did not authenticate", err)
	}
	tunnel.Close()
	drop.Store(false)
	for _, transport := range []string{"http", "grpc", "nats"} {
		result, code = accountMutation(t, c, transport, "remove", "managed@example.test", nil)
		if code != "restart_required" || result.Effect != "not_applied" || !result.RequiresInstanceRestart {
			t.Fatal(transport, result, code)
		}
		state := accountControl(t, c, transport)
		if !state.RequiresInstanceRestart || state.RequestInFlight || state.LastOperation == nil || state.LastOperation.Effect != "unknown" {
			t.Fatal(transport, state)
		}
	}
	restartAccountInstance(t, c)
	for _, transport := range []string{"http", "grpc", "nats"} {
		state := accountControl(t, c, transport)
		if state.RequiresInstanceRestart || state.Generation != 2 || state.LastOperation == nil || state.LastOperation.Generation != 1 {
			t.Fatal(transport, state)
		}
	}
	if tunnel, _, err := accountTunnel(address, managedID, target.Listener.Addr().String()); err == nil {
		tunnel.Close()
		t.Fatal("ephemeral account survived native restart")
	}
	result, code = accountMutation(t, c, "http", "add", "", content)
	if code != "" || !result.Acknowledged || result.Effect != "completed" || result.Generation != 2 {
		t.Fatal(result, code)
	}
	tunnel, _, err = accountTunnel(address, managedID, target.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tunnel.Close()
}
func restartAccountInstance(t *testing.T, c clients) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/core/engines/instances/edge-a/executions", nil)
	response, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("restart", response.StatusCode)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, "GET", c.address+"/v1/core/engines/instances/edge-a/readiness", nil)
		response, err := c.http.Do(request)
		if err == nil {
			var state struct {
				APIState string `json:"api_state"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&state)
			response.Body.Close()
			if decodeErr == nil && state.APIState == "ready" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("replacement API did not recover", ctx.Err())
		case <-ticker.C:
		}
	}
}
