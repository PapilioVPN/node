//go:build integration && linux

package integration_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
	"google.golang.org/protobuf/proto"
)

func TestOutboundControlCarriesRealTraffic(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
			defer target.Close()
			f := newFixture(t, pki, broker, transport)
			address := configureProbe(t, f, true)
			c := startNode(t, f)
			awaitNative(t, f.native)
			content, err := proto.Marshal(&core.OutboundHandlerConfig{Tag: "managed", ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
			if err != nil {
				t.Fatal(err)
			}
			result, code := nativeMutation(t, c, transport, "add", "", content)
			if code != "" || result.Effect != "completed" || !result.Acknowledged || result.Tag != "managed" {
				t.Fatal(result, code)
			}
			proxy, _ := url.Parse("http://" + address)
			tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			traffic := &http.Client{Transport: tr, Timeout: time.Second}
			requireTraffic(t, traffic, target.URL, true)
			observation := readOutbounds(t, c, transport)
			found := false
			for _, item := range observation.Outbounds {
				if item.Tag == "managed" {
					found = true
				}
			}
			if !found {
				t.Fatal("added outbound absent")
			}
			result, code = nativeMutation(t, c, transport, "add", "", content)
			if code != "unavailable" || result.Effect != "unknown" || result.Acknowledged || result.RequiresInstanceRestart {
				t.Fatal("duplicate add falsely promises rollback", result, code)
			}
			requireTraffic(t, traffic, target.URL, true)
			for range 2 {
				result, code = nativeMutation(t, c, transport, "remove", "managed", nil)
				if code != "" || result.Effect != "completed" || !result.Acknowledged {
					t.Fatal("native membership removal must accept missing tags", result, code)
				}
			}
			requireTraffic(t, traffic, target.URL, false)
		})
	}
}
func requireTraffic(t *testing.T, client *http.Client, target string, want bool) {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		if want {
			t.Fatal(err)
		}
		return
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if (string(body) == "outbound-traffic") != want {
		t.Fatal("unexpected traffic result", response.StatusCode, string(body))
	}
}
func TestRemovingDefaultDoesNotPromoteAnotherExistingOutbound(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
			defer target.Close()
			f := newFixture(t, pki, broker, transport)
			address := configureProbe(t, f, false)
			c := startNode(t, f)
			awaitNative(t, f.native)
			proxy, _ := url.Parse("http://" + address)
			tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			traffic := &http.Client{Transport: tr, Timeout: time.Second}
			requireTraffic(t, traffic, target.URL, true)
			result, code := nativeMutation(t, c, transport, "remove", "baseline", nil)
			if code != "" || !result.Acknowledged {
				t.Fatal(result, code)
			}
			observation := readOutbounds(t, c, transport)
			if len(observation.Outbounds) != 1 || observation.Outbounds[0].Tag != "backup" {
				t.Fatal(observation)
			}
			requireTraffic(t, traffic, target.URL, false)
			// Equal observed settings must not issue a duplicate Add that would silently
			// choose this handler as a missing default.
			plan := planReplacement(t, c, transport, "backup", observation.Outbounds[0].Configuration, "preserve", "retain")
			if plan.Mode != "unchanged" {
				t.Fatal(plan)
			}
			unchanged := applyReplacement(t, c, transport, plan.ID)
			if unchanged.Outcome != "unchanged" {
				t.Fatal(unchanged)
			}
			requireTraffic(t, traffic, target.URL, false)
			// Native AddHandler selects a missing default before checking duplicate tags.
			// A terminal failure can therefore change routing even without registration.
			duplicate, _ := proto.Marshal(&core.OutboundHandlerConfig{Tag: "backup", ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
			result, code = nativeMutation(t, c, transport, "add", "", duplicate)
			if code != "unavailable" || result.Effect != "unknown" || result.Acknowledged {
				t.Fatal(result, code)
			}
			requireTraffic(t, traffic, target.URL, true)
			content, _ := proto.Marshal(&core.OutboundHandlerConfig{Tag: "new-default", ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
			result, code = nativeMutation(t, c, transport, "add", "", content)
			if code != "" || !result.Acknowledged {
				t.Fatal(result, code)
			}
			requireTraffic(t, traffic, target.URL, true)
		})
	}
}
