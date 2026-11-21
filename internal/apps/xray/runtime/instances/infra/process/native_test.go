//go:build integration && linux

package process

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeXrayTrafficStopRestartAndIndependentInstance(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the checksum-verified official executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "native-instance-proof") }))
	defer target.Close()
	create := func() (*Process, string) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		proxy := listener.Addr().String()
		apiListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		apiAddress := apiListener.Addr().String()
		_, port, _ := net.SplitHostPort(proxy)
		var numericPort int
		json.Unmarshal([]byte(port), &numericPort)
		contents, err := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": apiAddress, "services": []string{"StatsService"}}, "stats": map[string]any{}, "inbounds": []any{map[string]any{"tag": "proxy", "listen": "127.0.0.1", "port": numericPort, "protocol": "http", "settings": map[string]any{}}}, "outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
		p, err := New(Config{Binary: binary, Configuration: path, StopTimeout: time.Second, API: &APIConfig{Network: "tcp", Address: apiAddress}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := p.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		listener.Close()
		apiListener.Close()
		return p, proxy
	}
	first, proxyA := create()
	second, proxyB := create()
	for _, p := range []*Process{first, second} {
		if err := p.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		nativeReady(t, p)
	}
	request := func(address string) error {
		proxy, _ := url.Parse("http://" + address)
		transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get(target.URL)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && string(body) != "native-instance-proof" {
			t.Fatalf("unexpected traffic %q", body)
		}
		return err
	}
	for _, address := range []string{proxyA, proxyB} {
		if err := request(address); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := request(proxyA); err == nil {
		t.Fatal("stopped engine still accepted traffic")
	}
	if err := request(proxyB); err != nil {
		t.Fatal("stopping first affected second", err)
	}
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	nativeReady(t, first)
	if err := request(proxyA); err != nil {
		t.Fatal(err)
	}
	if first.Observe().Generation != 2 || second.Observe().Generation != 1 {
		t.Fatal("execution identity lost")
	}
	// External termination is observed without an explicit status RPC to the engine.
	first.mu.Lock()
	child := first.current.command.Process
	first.mu.Unlock()
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return first.Observe().State == "exited" })
	if first.Observe().Failure == nil {
		t.Fatal("unexpected termination cause missing")
	}
	if err := request(proxyB); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Process{first, second} {
		contents, err := os.ReadFile(p.config.Configuration)
		if err != nil || !strings.Contains(string(contents), `"protocol":"http"`) {
			t.Fatal("native config modified", err)
		}
	}
}

func nativeReady(t *testing.T, p *Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		probe, cancelProbe := context.WithTimeout(ctx, 200*time.Millisecond)
		ready, err := p.Check(probe)
		cancelProbe()
		if ready && err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Xray API unavailable: %v %+v", err, p.Observe())
		case <-tick.C:
		}
	}
}
