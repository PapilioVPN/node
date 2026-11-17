//go:build integration

package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/settings"
)

func XrayBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY is required; run make integration")
	}
	return binary
}
func Await(t *testing.T, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("native observation did not complete: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
func ControlledBinary(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controlled-binary")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// ConfiguredXray writes an isolated HTTP-proxy engine configuration.
func ConfiguredXray(t *testing.T, directory, id string) (settings.Instance, string) {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY is required; run make integration")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"http","settings":{}}],"outbounds":[{"protocol":"freedom"}]}`, port)
	path := filepath.Join(directory, id+".json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return settings.Instance{ID: domain.InstanceID(id), Engine: domain.Xray,
		Xray: &settings.Xray{Binary: binary, ConfigFile: path, StopTimeout: time.Second}}, address
}

// XrayTraffic waits for real local HTTP traffic through each proxy.
func XrayTraffic(ctx context.Context, addresses ...string) error {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "configured-instance")
	}))
	defer target.Close()
	for _, address := range addresses {
		proxy, err := url.Parse("http://" + address)
		if err != nil {
			return err
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 200 * time.Millisecond}
		deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
		ticker := time.NewTicker(10 * time.Millisecond)
		for {
			request, err := http.NewRequestWithContext(deadline, http.MethodGet, target.URL, nil)
			if err != nil {
				ticker.Stop()
				cancel()
				return err
			}
			response, err := client.Do(request)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 64))
				response.Body.Close()
				if readErr == nil && response.StatusCode == http.StatusOK && string(body) == "configured-instance" {
					break
				}
			}
			select {
			case <-deadline.Done():
				ticker.Stop()
				cancel()
				return fmt.Errorf("configured instance %s did not proxy local traffic: %w", address, deadline.Err())
			case <-ticker.C:
			}
		}
		ticker.Stop()
		cancel()
	}
	return nil
}

// AssertTCPPortsReleased verifies that owned listeners can be rebound.
func AssertTCPPortsReleased(t *testing.T, addresses ...string) {
	t.Helper()
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("instance listener leaked: %v", err)
		}
		listener.Close()
	}
}

// ConfigureXrayAPI enables only the requested native services in a fixture.
func ConfigureXrayAPI(t *testing.T, instance *settings.Instance, network string, services []string) {
	t.Helper()
	var address string
	if network == "unix" {
		// A short owned directory also respects Linux's Unix-address length bound.
		directory, err := os.MkdirTemp("", "xray-api-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(directory); err != nil {
				t.Error(err)
			}
		})
		address = filepath.Join(directory, "api.sock")
	} else {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address = listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]json.RawMessage
	if err := json.Unmarshal(content, &configuration); err != nil {
		t.Fatal(err)
	}
	api, err := json.Marshal(struct {
		Tag      string   `json:"tag"`
		Listen   string   `json:"listen"`
		Services []string `json:"services"`
	}{"api", address, services})
	if err != nil {
		t.Fatal(err)
	}
	configuration["api"] = api
	configuration["stats"] = json.RawMessage("{}")
	content, err = json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	instance.Xray.API = &settings.XrayAPI{Network: network, Address: address, RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 2}
}
