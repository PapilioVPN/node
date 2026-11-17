//go:build integration && linux

package integration_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	onlinev1 "github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi/proto/v1"
)

// holdOnlineFlow keeps one real proxied response open until its idempotent close.
// Explicit loopback source addresses avoid Xray's native 127.0.0.1 exclusion.
func holdOnlineFlow(t *testing.T, proxyAddress string, target *httptest.Server, source string) func() {
	t.Helper()
	proxyURL, err := url.Parse("http://" + proxyAddress)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL.User = url.UserPassword("reader@local", "test-password")
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}, Timeout: time.Second}
	// The target's native test client supplies trust for its isolated certificate.
	transport := target.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	transport.DialContext = dialer.DialContext
	transport.DisableKeepAlives = true
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		cancel()
		transport.CloseIdleConnections()
		t.Fatal(err)
	}
	closeFlow := sync.OnceFunc(func() { cancel(); _ = response.Body.Close(); transport.CloseIdleConnections() })
	t.Cleanup(closeFlow)
	var content [1]byte
	if _, err := io.ReadFull(response.Body, content[:]); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || content[0] != '*' {
		t.Fatalf("proxy flow failed: %s", response.Status)
	}
	return closeFlow
}

func awaitOnlineCount(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, want int64) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := client.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: "edge-a", Email: "reader@local"})
		if err != nil {
			t.Fatal(err)
		}
		if result.OnlineIpCount == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("online count = %d; want %d: %v", result.OnlineIpCount, want, ctx.Err())
		case <-ticker.C:
		}
	}
}
