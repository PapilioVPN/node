package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func verifyTrafficReset(t *testing.T, ctx context.Context, router http.Handler, client statsv1.XrayStatsServiceClient) {
	t.Helper()
	const name = "case>>>name"
	const user = statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER
	const base = "/v1/xray/instances/primary/stats/traffic/user/resets"

	read := func() *statsv1.XrayTrafficUsage {
		t.Helper()
		result, err := client.GetTraffic(ctx, &statsv1.XrayTrafficRequest{Id: "primary", Scope: user, Name: name})
		require(t, err)
		return result
	}
	post := func(name, direction string, code int) []byte {
		t.Helper()
		res := httptest.NewRecorder()
		path := base + "?name=" + url.QueryEscape(name) + "&direction=" + direction
		router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodPost, path, nil))
		if res.Code != code || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("reset HTTP %d %s", res.Code, res.Body)
		}
		return res.Body.Bytes()
	}

	before := read()
	if before.GetUplinkBytes() <= 0 || before.GetDownlinkBytes() <= 0 {
		t.Fatalf("fixture did not register traffic: %v", before)
	}
	var httpResult statsv1.XrayTrafficResetResult
	require(t, json.Unmarshal(post(name, "uplink", http.StatusOK), &httpResult))
	if httpResult.GetPreviousBytes() != before.GetUplinkBytes() {
		t.Fatalf("HTTP previous uplink = %d, want %d", httpResult.GetPreviousBytes(), before.GetUplinkBytes())
	}
	after := read()
	if after.UplinkBytes == nil || after.GetUplinkBytes() != 0 || after.GetDownlinkBytes() != before.GetDownlinkBytes() {
		t.Fatalf("exact HTTP reset changed the wrong counters: %v", after)
	}

	request := &statsv1.XrayTrafficResetRequest{
		Id: "primary", Scope: user, Name: name,
		Direction: statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_DOWNLINK,
	}
	result, err := client.ResetTrafficCounter(ctx, request)
	require(t, err)
	if result.GetPreviousBytes() != after.GetDownlinkBytes() {
		t.Fatalf("gRPC previous downlink = %d, want %d", result.GetPreviousBytes(), after.GetDownlinkBytes())
	}
	after = read()
	if after.DownlinkBytes == nil || after.GetDownlinkBytes() != 0 {
		t.Fatalf("exact gRPC reset did not leave a registered zero: %v", after)
	}
	result, err = client.ResetTrafficCounter(ctx, request)
	require(t, err)
	if result.GetPreviousBytes() != 0 {
		t.Fatalf("second reset returned %d for an empty counter", result.GetPreviousBytes())
	}
	request.Name = "missing"
	_, err = client.ResetTrafficCounter(ctx, request)
	grpcStatus := status.Convert(err)
	if grpcStatus.Code() != codes.NotFound {
		t.Fatalf("missing counter reset = %v", err)
	}
	for _, detail := range grpcStatus.Details() {
		if failure, ok := detail.(*statsv1.XrayTrafficResetFailure); ok && failure.GetEffect() == statsv1.XrayTrafficResetFailure_NOT_APPLIED {
			var body struct {
				Effect string `json:"effect"`
			}
			require(t, json.Unmarshal(post("missing", "downlink", http.StatusNotFound), &body))
			if body.Effect != "not_applied" {
				t.Fatalf("missing HTTP counter reported %q", body.Effect)
			}
			return
		}
	}
	t.Fatalf("missing gRPC reset effect: %v", err)
}
