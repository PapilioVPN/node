package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	outboundsv1 "github.com/PapilioVPN/node/internal/engines/xray/outbounds/bridge/grpc/proto/v1"
	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const healthPath = "/v1/xray/instances/primary/outbounds/health"

type observation struct {
	Tag                 string `json:"tag"`
	Status              string `json:"status"`
	DelayMilliseconds   *int64 `json:"delay_milliseconds"`
	LastSeenUnixSeconds *int64 `json:"last_seen_unix_seconds"`
	LastTryUnixSeconds  *int64 `json:"last_try_unix_seconds"`
}

func TestWithoutObservatory(t *testing.T) {
	f := startFixture(t, `{"log":{"loglevel":"none"}}`)
	f.get(t, "", http.StatusConflict)
	_, err := f.health.GetOutboundHealth(t.Context(), &outboundsv1.XrayOutboundHealthRequest{Id: instanceID})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("outbound health without Observatory = %v, want FailedPrecondition", err)
	}
}

func TestEmptyObservation(t *testing.T) {
	f := startFixture(t, `{"log":{"loglevel":"none"}, "observatory": {}}`)
	all := f.get(t, "", http.StatusOK)
	if all == nil || len(all) != 0 {
		t.Fatalf("HTTP empty snapshot = %#v, want []", all)
	}
	request := &outboundsv1.XrayOutboundHealthRequest{Id: instanceID}
	reply, err := f.health.GetOutboundHealth(t.Context(), request)
	require(t, err)
	if len(reply.Observations) != 0 {
		t.Fatalf("gRPC empty snapshot = %v", reply)
	}
	tag := "unknown"
	checkSample(t, f.get(t, "?tag=unknown", http.StatusOK), tag, "no_sample")
	request.Tag = &tag
	reply, err = f.health.GetOutboundHealth(t.Context(), request)
	require(t, err)
	checkSample(t, fromGRPC(reply), tag, "no_sample")
}

func TestOrdinaryObservatorySamples(t *testing.T) {
	var successful atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successful.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(local.Close)
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	t.Cleanup(failed.Close)
	failedAddress := failed.Listener.Addr().(*net.TCPAddr).String()
	const goodTag = "sample-good &+/#"
	const badTag = "sample-bad"
	const unobservedTag = "unobserved"
	before := time.Now().Unix()
	f := startFixture(t, fmt.Sprintf(`{
		# "observatory": null
		"log": {"loglevel":"none"},
		/* Observe only the two selected outbounds. */
		"Observatory": {"subjectSelector":["sample-"], "probeURL":%q, "probeInterval":"1h", "enableConcurrency":true},
		"routing": {"rules":[{"type":"field","port":"1","ruleTag":"integration","outboundTag":%q}]},
		// The failed outbound reaches a loopback server that closes before HTTP headers.
		"outbounds": [
			{"tag":%q, "protocol":"freedom"},
			{"tag":%q, "protocol":"freedom", "settings":{"redirect":%q}},
			{"tag":%q, "protocol":"freedom"}
		]
	}`, local.URL+"/probe#fixture", goodTag, goodTag, badTag, failedAddress, unobservedTag))
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	request := &outboundsv1.XrayOutboundHealthRequest{Id: instanceID}
	for {
		reply, err := f.health.GetOutboundHealth(ctx, request)
		require(t, err)
		if len(reply.Observations) == 2 {
			checkAll(t, fromGRPC(reply), goodTag, badTag, before)
			break
		}
		select {
		case <-time.After(15 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("timed out waiting for both native samples")
		}
	}
	checkAll(t, f.get(t, "", http.StatusOK), goodTag, badTag, before)
	rules, err := f.routing.ListRoutingRules(ctx, &routingv1.XrayRoutingRulesRequest{Id: instanceID})
	require(t, err)
	if len(rules.GetRules()) != 1 || rules.Rules[0].GetRuleTag() != "integration" || rules.Rules[0].GetOutboundTag() != goodTag {
		t.Fatalf("routing rules with Observatory enabled: %v", rules)
	}
	for _, tc := range []struct{ tag, state string }{
		{goodTag, "healthy"}, {badTag, "failed"},
		{unobservedTag, "no_sample"}, {"sample-good", "no_sample"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			httpSamples := f.get(t, "?tag="+url.QueryEscape(tc.tag), http.StatusOK)
			checkSample(t, httpSamples, tc.tag, tc.state)
			reply, err := f.health.GetOutboundHealth(ctx, &outboundsv1.XrayOutboundHealthRequest{Id: instanceID, Tag: &tc.tag})
			require(t, err)
			rpcSamples := fromGRPC(reply)
			checkSample(t, rpcSamples, tc.tag, tc.state)
			httpValue, err := json.Marshal(httpSamples)
			require(t, err)
			rpcValue, err := json.Marshal(rpcSamples)
			require(t, err)
			if string(httpValue) != string(rpcValue) {
				t.Fatalf("HTTP result %s, gRPC result %s", httpValue, rpcValue)
			}
		})
	}
	if hits := successful.Load(); hits != 1 {
		t.Fatalf("probe destination received %d requests, want exactly one background probe", hits)
	}

	f.get(t, "?tag=", http.StatusBadRequest)
	f.get(t, "?tag=one&tag=two", http.StatusBadRequest)
	f.get(t, "?tag=%zz", http.StatusBadRequest)
	empty := ""
	_, err = f.health.GetOutboundHealth(ctx, &outboundsv1.XrayOutboundHealthRequest{Id: instanceID, Tag: &empty})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("explicit empty tag = %v, want InvalidArgument", err)
	}
}

func (f fixture) get(t *testing.T, query string, code int) []observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.http.URL+healthPath+query, nil)
	require(t, err)
	resp, err := f.http.Client().Do(req)
	require(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != code || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("GET %s = %s, headers %v, want %d and no-store", query, resp.Status, resp.Header, code)
	}
	if code != http.StatusOK {
		return nil
	}
	var result struct {
		Observations []observation `json:"observations"`
	}
	require(t, json.NewDecoder(resp.Body).Decode(&result))
	return result.Observations
}

func fromGRPC(reply *outboundsv1.XrayOutboundHealth) []observation {
	result := make([]observation, len(reply.Observations))
	for i, row := range reply.Observations {
		state := map[outboundsv1.XrayOutboundHealthStatus]string{
			outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_HEALTHY:   "healthy",
			outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_FAILED:    "failed",
			outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_NO_SAMPLE: "no_sample",
		}[row.Status]
		result[i] = observation{row.Tag, state, row.DelayMilliseconds, row.LastSeenUnixSeconds, row.LastTryUnixSeconds}
	}
	return result
}

func checkAll(t *testing.T, rows []observation, goodTag, badTag string, before int64) {
	t.Helper()
	if len(rows) != 2 {
		t.Fatalf("list = %+v, want two recorded outbounds", rows)
	}
	for _, tc := range []struct{ tag, state string }{{goodTag, "healthy"}, {badTag, "failed"}} {
		i := slices.IndexFunc(rows, func(row observation) bool { return row.Tag == tc.tag })
		if i == -1 {
			t.Fatalf("missing sample for %q in %+v", tc.tag, rows)
		}
		checkSample(t, rows[i:i+1], tc.tag, tc.state)
		if tried := *rows[i].LastTryUnixSeconds; tried < before || tried > time.Now().Unix() {
			t.Fatalf("native last try %d is outside fixture lifetime", tried)
		}
	}
}

func checkSample(t *testing.T, rows []observation, tag, state string) {
	t.Helper()
	if len(rows) != 1 || rows[0].Tag != tag || rows[0].Status != state {
		t.Fatalf("observation = %+v, want %q in state %q", rows, tag, state)
	}
	row := rows[0]
	switch state {
	case "healthy":
		if row.DelayMilliseconds == nil || *row.DelayMilliseconds < 0 || *row.DelayMilliseconds == 99999999 ||
			row.LastTryUnixSeconds == nil || row.LastSeenUnixSeconds == nil || *row.LastTryUnixSeconds != *row.LastSeenUnixSeconds {
			t.Fatalf("invalid healthy observation: %+v", row)
		}
	case "failed":
		if row.DelayMilliseconds == nil || *row.DelayMilliseconds != 99999999 ||
			row.LastTryUnixSeconds == nil || row.LastSeenUnixSeconds != nil {
			t.Fatalf("invalid failed observation: %+v", row)
		}
	case "no_sample":
		if row.DelayMilliseconds != nil || row.LastTryUnixSeconds != nil || row.LastSeenUnixSeconds != nil {
			t.Fatalf("no-sample row unexpectedly has native values: %+v", row)
		}
	}
}
