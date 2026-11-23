package integration_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	statsgrpc "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc"
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	statshttp "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func verifyTrafficPairReset(t *testing.T, ctx context.Context, router http.Handler, client statsv1.XrayStatsServiceClient, seed func(*testing.T)) {
	t.Helper()
	const name = "case>>>name"
	const user = statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER
	const inbound = statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND
	read := func(scope statsv1.XrayTrafficScope, name string) *statsv1.XrayTrafficUsage {
		t.Helper()
		got, err := client.GetTraffic(ctx, &statsv1.XrayTrafficRequest{Id: "primary", Scope: scope, Name: name})
		require(t, err)
		return got
	}
	post := func(name string) *httptest.ResponseRecorder {
		t.Helper()
		res := httptest.NewRecorder()
		path := "/v1/xray/instances/primary/stats/traffic/user/pair-resets?name=" + url.QueryEscape(name)
		router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodPost, path, nil))
		if res.Code != http.StatusOK || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("pair reset HTTP %d %s", res.Code, res.Body)
		}
		return res
	}
	checkCleared := func(other *statsv1.XrayTrafficUsage) {
		t.Helper()
		current := read(user, name)
		if current.UplinkBytes == nil || current.DownlinkBytes == nil || current.GetUplinkBytes() != 0 || current.GetDownlinkBytes() != 0 {
			t.Fatalf("pair reset left nonzero or absent counters: %v", current)
		}
		if got := read(inbound, "shared"); !proto.Equal(other, got) {
			t.Fatalf("pair reset changed another scope: before %v, after %v", other, got)
		}
	}

	seed(t)
	previous, other := read(user, name), read(inbound, "shared")
	if previous.GetUplinkBytes() <= 0 || previous.GetDownlinkBytes() <= 0 {
		t.Fatalf("fixture did not seed both directions: %v", previous)
	}
	type direction struct {
		Effect   string `json:"effect"`
		Previous *int64 `json:"previous_bytes"`
	}
	var httpReply struct{ Uplink, Downlink direction }
	require(t, json.Unmarshal(post(name).Body.Bytes(), &httpReply))
	checkHTTP := func(reply direction, previous int64) {
		t.Helper()
		if reply.Effect != "acknowledged" || reply.Previous == nil || *reply.Previous != previous {
			t.Fatalf("HTTP direction = %+v, expected acknowledgement for %d bytes", reply, previous)
		}
	}
	checkHTTP(httpReply.Uplink, previous.GetUplinkBytes())
	checkHTTP(httpReply.Downlink, previous.GetDownlinkBytes())
	checkCleared(other)

	seed(t)
	previous, other = read(user, name), read(inbound, "shared")
	request := &statsv1.XrayTrafficPairResetRequest{Id: "primary", Scope: user, Name: name}
	grpcReply, err := client.ResetTrafficPair(ctx, request)
	require(t, err)
	checkGRPC := func(reply *statsv1.XrayTrafficDirectionReset, previous int64) {
		t.Helper()
		if reply.GetEffect() != statsv1.XrayTrafficDirectionReset_ACKNOWLEDGED ||
			reply.PreviousBytes == nil || reply.GetPreviousBytes() != previous {
			t.Fatalf("gRPC direction = %v, expected acknowledgement for %d bytes", reply, previous)
		}
	}
	checkGRPC(grpcReply.GetUplink(), previous.GetUplinkBytes())
	checkGRPC(grpcReply.GetDownlink(), previous.GetDownlinkBytes())
	checkCleared(other)

	zero, err := client.ResetTrafficPair(ctx, request)
	require(t, err)
	checkGRPC(zero.GetUplink(), 0)
	checkGRPC(zero.GetDownlink(), 0)

	request.Name = "missing"
	missing, err := client.ResetTrafficPair(ctx, request)
	require(t, err)
	for _, reply := range []*statsv1.XrayTrafficDirectionReset{missing.GetUplink(), missing.GetDownlink()} {
		if reply.GetEffect() != statsv1.XrayTrafficDirectionReset_MISSING || reply.PreviousBytes != nil {
			t.Fatalf("missing gRPC direction returned %v", reply)
		}
	}
	httpReply = struct{ Uplink, Downlink direction }{}
	require(t, json.Unmarshal(post("missing").Body.Bytes(), &httpReply))
	if httpReply.Uplink.Effect != "missing" || httpReply.Downlink.Effect != "missing" ||
		httpReply.Uplink.Previous != nil || httpReply.Downlink.Previous != nil {
		t.Fatalf("missing HTTP directions returned %+v", httpReply)
	}
}

type interruptedPairBackend struct{}

func (interruptedPairBackend) Read(context.Context, string, traffic.Scope, string) (traffic.Usage, error) {
	panic("unexpected traffic read")
}

func (interruptedPairBackend) List(context.Context, string, traffic.Scope) ([]traffic.Usage, error) {
	panic("unexpected traffic list")
}

func (interruptedPairBackend) Reset(context.Context, traffic.Counter) (int64, error) {
	panic("unexpected single reset")
}

func (interruptedPairBackend) ResetPair(context.Context, traffic.CounterPair) (traffic.PairReset, error) {
	return traffic.PairReset{
		Uplink:   traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: 42},
		Downlink: traffic.DirectionReset{Effect: traffic.ResetUnknown},
	}, traffic.ErrResetUnknown
}

func (interruptedPairBackend) ResetScope(context.Context, traffic.ScopeReset, int) ([]traffic.CounterReset, error) {
	report := make([]traffic.CounterReset, 128)
	for i := range report {
		report[i] = traffic.CounterReset{
			Name: strings.Repeat("x", 80) + strconv.Itoa(i), Direction: traffic.Uplink,
			DirectionReset: traffic.DirectionReset{Effect: traffic.ResetNotAttempted},
		}
	}
	report[0].DirectionReset = traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: 42}
	report[1].Effect = traffic.ResetUnknown
	return report, traffic.ErrResetUnknown
}

func TestInterruptedPairReportsProgressOnBothTransports(t *testing.T) {
	service := traffic.New(interruptedPairBackend{}, true, 0)
	router := chi.NewRouter()
	statshttp.RegisterTraffic(router, service)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/xray/instances/primary/stats/traffic/user/pair-resets?name=alice", nil))
	var body struct {
		Uplink, Downlink struct {
			Effect        string `json:"effect"`
			PreviousBytes *int64 `json:"previous_bytes"`
		}
	}
	require(t, json.Unmarshal(res.Body.Bytes(), &body))
	if res.Code != http.StatusBadGateway || body.Uplink.Effect != "acknowledged" ||
		body.Uplink.PreviousBytes == nil || *body.Uplink.PreviousBytes != 42 ||
		body.Downlink.Effect != "unknown" || body.Downlink.PreviousBytes != nil {
		t.Fatalf("partial HTTP result: %d %s", res.Code, res.Body)
	}

	client := startTrafficResetClient(t, service)
	_, err := client.ResetTrafficPair(t.Context(), &statsv1.XrayTrafficPairResetRequest{
		Id: "primary", Scope: statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, Name: "alice",
	})
	st := status.Convert(err)
	if st.Code() != codes.Unknown || len(st.Details()) != 1 {
		t.Fatalf("expected only the partial gRPC result: %v", err)
	}
	pair, ok := st.Details()[0].(*statsv1.XrayTrafficPairResetResult)
	if !ok || pair.GetUplink().GetEffect() != statsv1.XrayTrafficDirectionReset_ACKNOWLEDGED ||
		pair.GetUplink().GetPreviousBytes() != 42 || pair.GetDownlink().GetEffect() != statsv1.XrayTrafficDirectionReset_UNKNOWN ||
		pair.GetDownlink().PreviousBytes != nil {
		t.Fatalf("partial gRPC result: %v", st.Details())
	}
}

func startTrafficResetClient(t *testing.T, service *traffic.Service) statsv1.XrayStatsServiceClient {
	t.Helper()
	server := grpc.NewServer()
	statsgrpc.Register(server, nil, service, nil)
	listener := bufconn.Listen(64 << 10)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		if err := <-done; err != nil && err != grpc.ErrServerStopped {
			t.Error(err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///interrupted-pair",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	return statsv1.NewXrayStatsServiceClient(conn)
}
