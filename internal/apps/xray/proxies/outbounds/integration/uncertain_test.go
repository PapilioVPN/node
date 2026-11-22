//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

func TestLostNativeAcknowledgementRequiresExplicitExecutionReplacement(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
	defer target.Close()
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	address := configureProbe(t, f, true)
	relay, drop := lostReplies(t, f.native)
	f.config.Instances[0].Xray.API.Address = relay
	f.config.Communications.Requests.Timeout = 200 * time.Millisecond
	c := startNode(t, f)
	awaitNative(t, f.native)
	content, _ := proto.Marshal(&core.OutboundHandlerConfig{Tag: "managed", ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
	result, code := nativeMutation(t, c, "http", "add", "", content)
	if code != "deadline_exceeded" || result.Effect != "unknown" || result.Acknowledged || !result.RequiresInstanceRestart {
		t.Fatal(result, code)
	}
	// Observe the real native side effect outside the deliberately broken reply path.
	direct, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	observed, err := command.NewHandlerServiceClient(direct).ListOutbounds(ctx, &command.ListOutboundsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range observed.Outbounds {
		if record.Tag == "managed" {
			found = true
		}
	}
	if !found {
		t.Fatal("native request was not actually applied")
	}
	proxy, _ := url.Parse("http://" + address)
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	traffic := &http.Client{Transport: tr, Timeout: time.Second}
	requireTraffic(t, traffic, target.URL, true)
	drop.Store(false)
	result, code = nativeMutation(t, c, "http", "remove", "managed", nil)
	if code != "restart_required" || result.Effect != "not_applied" || !result.RequiresInstanceRestart {
		t.Fatal(result, code)
	}
	state := outboundControl(t, c)
	if !state.RequiresInstanceRestart || state.RequestInFlight || state.LastOperation == nil || state.LastOperation.Effect != "unknown" {
		t.Fatal(state)
	}
	restartInstance(t, c)
	state = outboundControl(t, c)
	if state.RequiresInstanceRestart || state.Generation != 2 || state.LastOperation == nil || state.LastOperation.Generation != 1 {
		t.Fatal(state)
	}
	requireTraffic(t, traffic, target.URL, false)
	result, code = nativeMutation(t, c, "http", "add", "", content)
	if code != "" || result.Effect != "completed" || !result.Acknowledged || result.Generation != 2 {
		t.Fatal(result, code)
	}
	requireTraffic(t, traffic, target.URL, true)
}

type controlState struct {
	Generation              uint64     `json:"generation,string"`
	RequestInFlight         bool       `json:"request_in_flight"`
	RequiresInstanceRestart bool       `json:"requires_instance_restart"`
	LastOperation           *operation `json:"last_operation"`
}

func outboundControl(t *testing.T, c clients) controlState {
	t.Helper()
	response, err := c.http.Get(c.address + "/v1/xray/proxies/instances/edge-a/outbounds/control-status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result controlState
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != 200 {
		t.Fatal(result, err, response.StatusCode)
	}
	return result
}

func restartInstance(t *testing.T, c clients) {
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
		t.Fatalf("instance restart status=%d", response.StatusCode)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, "GET", c.address+"/v1/core/engines/instances/edge-a/readiness", nil)
		response, err := c.http.Do(request)
		if err == nil {
			var result struct {
				APIState string `json:"api_state"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&result)
			response.Body.Close()
			if decodeErr == nil && result.APIState == "ready" {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("replaced execution's shared native channel did not recover")
		case <-ticker.C:
		}
	}
}
