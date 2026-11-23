package integration_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func verifyOnlineIPs(t *testing.T, ctx context.Context, router http.Handler, client statsv1.XrayStatsServiceClient, proxy, target string) {
	t.Helper()
	const name = "case>>>name"
	const base = "/v1/xray/instances/primary/stats/online/"
	const source = "127.0.0.2" // Xray excludes 127.0.0.1 from online statistics.

	get := func(path string, code int) []byte {
		t.Helper()
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, base+path, nil))
		if res.Code != code || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: HTTP %d %s", path, res.Code, res.Body)
		}
		return res.Body.Bytes()
	}
	for _, route := range []string{"count", "ips"} {
		get(route+"?name=absent", http.StatusNotFound)
		get(route+"?name=a&name=b", http.StatusBadRequest)
	}
	absent := &statsv1.XrayOnlineRequest{Id: "primary", Name: "absent"}
	_, err := client.GetOnlineIPCount(ctx, absent)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing online count: %v", err)
	}
	_, err = client.ListOnlineIPs(ctx, absent)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing online IPs: %v", err)
	}

	before := time.Now().Unix()
	first := connectProxy(t, ctx, proxy, target, source, name)
	second := connectProxy(t, ctx, proxy, target, source, name)
	req := &statsv1.XrayOnlineRequest{Id: "primary", Name: name}
	deadline := time.Now().Add(2 * time.Second)
	var timestamp int64
	for {
		result, err := client.ListOnlineIPs(ctx, req)
		require(t, err)
		timestamp = result.GetLastSeenUnixSeconds()[source]
		if timestamp >= before && timestamp <= time.Now().Unix() && len(result.GetLastSeenUnixSeconds()) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected one tracked source IP with a recent timestamp, received %v", result)
		}
		time.Sleep(5 * time.Millisecond)
	}
	count, err := client.GetOnlineIPCount(ctx, req)
	require(t, err)
	if count.GetName() != name || count.GetCount() != 1 {
		t.Fatalf("expected one distinct IP for two connections, received %v", count)
	}

	query := "?name=" + url.QueryEscape(name)
	var httpCount statsv1.XrayOnlineCount
	require(t, json.Unmarshal(get("count"+query, http.StatusOK), &httpCount))
	if httpCount.GetName() != name || httpCount.GetCount() != 1 {
		t.Fatalf("HTTP online count %v", &httpCount)
	}
	var httpIPs statsv1.XrayOnlineIPs
	require(t, json.Unmarshal(get("ips"+query, http.StatusOK), &httpIPs))
	if httpIPs.GetName() != name || len(httpIPs.GetLastSeenUnixSeconds()) != 1 || httpIPs.GetLastSeenUnixSeconds()[source] < timestamp {
		t.Fatalf("HTTP online IPs %v", &httpIPs)
	}

	require(t, first.Close())
	count, err = client.GetOnlineIPCount(ctx, req)
	require(t, err)
	if count.GetCount() != 1 {
		t.Fatalf("expected the remaining connection to preserve the IP, received %v", count)
	}
	require(t, second.Close())
	deadline = time.Now().Add(2 * time.Second)
	for {
		count, err := client.GetOnlineIPCount(ctx, req)
		require(t, err)
		if count.GetCount() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected zero tracked IPs after disconnect, received %v", count)
		}
		time.Sleep(5 * time.Millisecond)
	}
	body := get("ips"+query, http.StatusOK)
	var empty struct {
		IPs map[string]int64 `json:"last_seen_unix_seconds"`
	}
	require(t, json.Unmarshal(body, &empty))
	if empty.IPs == nil || len(empty.IPs) != 0 {
		t.Fatalf("expected an empty tracked IP object, received %s", body)
	}
}

func connectProxy(t *testing.T, ctx context.Context, proxy, target, source, name string) net.Conn {
	t.Helper()
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}
	conn, err := dialer.DialContext(ctx, "tcp4", proxy)
	require(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		require(t, conn.SetDeadline(deadline))
	}
	auth := base64.StdEncoding.EncodeToString([]byte(name + ":fixture"))
	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", target, target, auth)
	require(t, err)
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	require(t, err)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxy CONNECT returned %s", response.Status)
	}
	return conn
}
