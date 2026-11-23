package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	statshttp "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func verifyTrafficScopeReset(t *testing.T, ctx context.Context, router http.Handler, client statsv1.XrayStatsServiceClient, seed func(*testing.T)) {
	t.Helper()
	const name = "case>>>name"
	const user = statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER
	const inbound = statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND
	read := func(scope statsv1.XrayTrafficScope, name string) *statsv1.XrayTrafficUsage {
		t.Helper()
		result, err := client.GetTraffic(ctx, &statsv1.XrayTrafficRequest{Id: "primary", Scope: scope, Name: name})
		require(t, err)
		return result
	}
	post := func(limit, status int) []byte {
		t.Helper()
		res := httptest.NewRecorder()
		path := "/v1/xray/instances/primary/stats/traffic/user/scope-resets?max_counters=" + strconv.Itoa(limit)
		router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodPost, path, nil))
		if res.Code != status || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("scope reset HTTP %d %s", res.Code, res.Body)
		}
		return res.Body.Bytes()
	}

	seed(t)
	before, other := read(user, name), read(inbound, "shared")
	post(1, http.StatusUnprocessableEntity)
	req := &statsv1.XrayTrafficScopeResetRequest{Id: "primary", Scope: user, MaxCounters: 1}
	if _, err := client.ResetTrafficScope(ctx, req); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("gRPC selection bound: %v", err)
	}
	if got := read(user, name); !proto.Equal(before, got) {
		t.Fatalf("a rejected selection changed user traffic: %v -> %v", before, got)
	}

	type counter struct {
		Name, Direction string
		Result          struct {
			Effect   string `json:"effect"`
			Previous *int64 `json:"previous_bytes"`
		}
	}
	var httpResult struct {
		Outcome  string
		Counters []counter
	}
	require(t, json.Unmarshal(post(2, http.StatusOK), &httpResult))
	if httpResult.Outcome != "complete" || len(httpResult.Counters) != 2 {
		t.Fatalf("scope HTTP result = %+v", httpResult)
	}
	for i, direction := range []string{"uplink", "downlink"} {
		row := httpResult.Counters[i]
		want := []int64{before.GetUplinkBytes(), before.GetDownlinkBytes()}[i]
		if row.Name != name || row.Direction != direction || row.Result.Effect != "acknowledged" ||
			row.Result.Previous == nil || *row.Result.Previous != want || want <= 0 {
			t.Fatalf("scope HTTP counter %d = %+v, want %d", i, row, want)
		}
	}
	if got := read(inbound, "shared"); !proto.Equal(other, got) {
		t.Fatalf("scope reset changed inbound traffic: %v -> %v", other, got)
	}

	seed(t)
	before, other = read(user, name), read(inbound, "shared")
	req.MaxCounters = 2
	result, err := client.ResetTrafficScope(ctx, req)
	require(t, err)
	if result.GetOutcome() != statsv1.XrayTrafficScopeResetResult_COMPLETE || len(result.GetCounters()) != 2 {
		t.Fatalf("scope gRPC result = %v", result)
	}
	for i, row := range result.GetCounters() {
		direction := []statsv1.XrayTrafficDirection{
			statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_UPLINK,
			statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_DOWNLINK,
		}[i]
		want := []int64{before.GetUplinkBytes(), before.GetDownlinkBytes()}[i]
		if row.GetName() != name || row.GetDirection() != direction ||
			row.GetResult().GetEffect() != statsv1.XrayTrafficDirectionReset_ACKNOWLEDGED ||
			row.GetResult().PreviousBytes == nil || row.GetResult().GetPreviousBytes() != want || want <= 0 {
			t.Fatalf("scope gRPC counter %d = %v, want %d", i, row, want)
		}
	}
	if got := read(user, name); got.UplinkBytes == nil || got.DownlinkBytes == nil || got.GetUplinkBytes() != 0 || got.GetDownlinkBytes() != 0 {
		t.Fatalf("scope reset did not clear both user counters: %v", got)
	}
	if got := read(inbound, "shared"); !proto.Equal(other, got) {
		t.Fatalf("gRPC scope reset changed inbound traffic: %v -> %v", other, got)
	}
}

func TestInterruptedScopeReturnsTheFullReport(t *testing.T) {
	service := traffic.New(interruptedPairBackend{}, true, 0)
	router := chi.NewRouter()
	statshttp.RegisterTraffic(router, service)
	res := httptest.NewRecorder()
	path := "/v1/xray/instances/primary/stats/traffic/user/scope-resets?max_counters=128"
	router.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil))
	var report struct {
		Outcome  string
		Counters []struct {
			Result struct {
				Effect string
			}
		}
	}
	require(t, json.Unmarshal(res.Body.Bytes(), &report))
	if res.Code != http.StatusBadGateway || report.Outcome != "unknown" || len(report.Counters) != 128 ||
		report.Counters[0].Result.Effect != "acknowledged" || report.Counters[1].Result.Effect != "unknown" ||
		report.Counters[127].Result.Effect != "not_attempted" {
		t.Fatalf("scope reset error HTTP %d, outcome %q, counters %d", res.Code, report.Outcome, len(report.Counters))
	}

	client := startTrafficResetClient(t, service)
	reply, err := client.ResetTrafficScope(t.Context(), &statsv1.XrayTrafficScopeResetRequest{
		Id: "primary", Scope: statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, MaxCounters: 128,
	})
	require(t, err)
	rows := reply.GetCounters()
	if reply.GetOutcome() != statsv1.XrayTrafficScopeResetResult_UNKNOWN || len(rows) != 128 ||
		rows[0].GetResult().GetPreviousBytes() != 42 || rows[1].GetResult().GetEffect() != statsv1.XrayTrafficDirectionReset_UNKNOWN ||
		rows[127].GetResult().GetEffect() != statsv1.XrayTrafficDirectionReset_NOT_ATTEMPTED {
		t.Fatalf("scope reset error gRPC outcome %v, counters %d", reply.GetOutcome(), len(rows))
	}
}
