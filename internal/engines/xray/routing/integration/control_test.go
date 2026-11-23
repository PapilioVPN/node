package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func verifyRoutingControl(t *testing.T, ctx context.Context, router http.Handler, client routingv1.XrayRoutingServiceClient, balancer *routingv1.XrayBalancerInfoRequest) {
	t.Helper()
	request := &routingv1.XrayTestRouteRequest{Id: "primary", Network: "tcp", TargetIp: "192.0.2.1", TargetPort: 443}
	selection, err := client.TestRoute(ctx, request)
	require(t, err)
	if selection.GetOutboundTag() != "direct" {
		t.Fatalf("gRPC route = %v, want direct", selection)
	}
	selection = new(routingv1.XrayRouteSelection)
	post := func(body string, code int) []byte {
		t.Helper()
		return routingRequest(t, ctx, router, http.MethodPost, base+"/tests", body, code)
	}
	require(t, json.Unmarshal(post(`{"network":"tcp","target_ip":"2001:db8::1","target_port":443}`, http.StatusOK), selection))
	if selection.GetOutboundTag() != "direct" {
		t.Fatalf("HTTP route = %v, want direct", selection)
	}
	post(`{}`, http.StatusNotFound)
	post(`null`, http.StatusBadRequest)
	post(`{"target_ip":"bad"}`, http.StatusBadRequest)
	request.Network = ""
	_, err = client.TestRoute(ctx, request)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("no matching route = %v", err)
	}
	request.TargetPort = 65536
	_, err = client.TestRoute(ctx, request)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid port = %v", err)
	}

	checkOverride := func(want string) {
		t.Helper()
		result, err := client.GetBalancerInfo(ctx, balancer)
		require(t, err)
		if result.OverrideTarget == nil || result.GetOverrideTarget() != want {
			t.Fatalf("gRPC observed override = %v, want %q", result, want)
		}
		body := routingRequest(t, ctx, router, http.MethodGet, base+"/balancer?tag="+url.QueryEscape(balancer.Tag), "", http.StatusOK)
		httpResult := new(routingv1.XrayBalancerInfo)
		require(t, json.Unmarshal(body, httpResult))
		if httpResult.OverrideTarget == nil || httpResult.GetOverrideTarget() != want {
			t.Fatalf("HTTP observed override = %s, want %q", body, want)
		}
	}
	put := func(tag, body string, code int) {
		t.Helper()
		routingRequest(t, ctx, router, http.MethodPut, base+"/balancer/override?tag="+url.QueryEscape(tag), body, code)
	}
	set := func(tag string, target *string, code codes.Code) {
		t.Helper()
		_, err := client.OverrideBalancerTarget(ctx, &routingv1.XrayOverrideBalancerTargetRequest{
			Id: "primary", Tag: tag, Target: target,
		})
		if status.Code(err) != code {
			t.Fatalf("gRPC override %q = %v, want %s", tag, err, code)
		}
	}

	put(balancer.Tag, `{"target":"pool-b"}`, http.StatusNoContent)
	checkOverride("pool-b")
	selection, err = client.TestRoute(ctx, &routingv1.XrayTestRouteRequest{Id: "primary", Network: "udp"})
	require(t, err)
	if selection.GetOutboundTag() != "pool-b" {
		t.Fatalf("route through HTTP override = %v", selection)
	}
	put(balancer.Tag, `{"target":""}`, http.StatusNoContent)
	checkOverride("")

	set(balancer.Tag, new("pool-a"), codes.OK)
	checkOverride("pool-a")
	selection = new(routingv1.XrayRouteSelection)
	require(t, json.Unmarshal(post(`{"network":"udp"}`, http.StatusOK), selection))
	if selection.GetOutboundTag() != "pool-a" {
		t.Fatalf("route through gRPC override = %v", selection)
	}
	set(balancer.Tag, new(""), codes.OK)
	checkOverride("")

	put("missing", `{"target":"pool-b"}`, http.StatusNotFound)
	put(balancer.Tag, `{}`, http.StatusBadRequest)
	set("missing", new("pool-a"), codes.NotFound)
	set(balancer.Tag, nil, codes.InvalidArgument)
	checkOverride("")
}

func routingRequest(t *testing.T, ctx context.Context, router http.Handler, method, path, body string, want int) []byte {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body)))
	if response.Code != want || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s: HTTP %d %s, want %d", method, path, response.Code, response.Body, want)
	}
	return response.Body.Bytes()
}
