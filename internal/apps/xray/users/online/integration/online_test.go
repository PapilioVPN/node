//go:build integration && linux

package integration_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc/status"
)

func grpcMessage(err error) string { return status.Convert(err).Message() }

func TestNativePresenceIPsAndTransportParity(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	url := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := echoTarget(t)
			f := newFixture(t, pki, url, transport)
			for _, id := range []string{"a", "b"} {
				r := f.read(t, id, email, false)
				if r.State != "untracked" || r.Count != nil {
					t.Fatal(r)
				}
			}
			since := time.Now().Unix()
			a1 := tunnel(t, f.proxies["a"], "127.0.0.2", email, target)
			a2 := tunnel(t, f.proxies["a"], "127.0.0.2", email, target)
			a3 := tunnel(t, f.proxies["a"], "127.0.0.3", email, target)
			b := tunnel(t, f.proxies["b"], "127.0.0.3", email, target)
			assertCount(t, f, "a", 2)
			assertCount(t, f, "b", 1)
			ips := f.read(t, "a", email, true)
			if ips.Error != "" || ips.State != "online" || len(ips.IPs) != 2 || ips.IPs[0].IP != "127.0.0.2" || ips.IPs[1].IP != "127.0.0.3" {
				t.Fatal(ips)
			}
			for _, ip := range ips.IPs {
				if ip.LastSeen < since || ip.LastSeen > time.Now().Unix() {
					t.Fatal("native AddIP time lost", ip)
				}
			}
			if ips.Observation.InstanceID != "a" || ips.Observation.Email != email || ips.Observation.Generation != "1" || ips.Observation.Source != "xray.stats.online" || ips.Observation.ObservedAt.IsZero() {
				t.Fatal(ips)
			}
			a1.Close()
			assertCount(t, f, "a", 2)
			a2.Close()
			eventuallyCount(t, f, "a", 1)
			a3.Close()
			eventuallyCount(t, f, "a", 0)
			expired := f.read(t, "a", email, true)
			if expired.State != "offline" || len(expired.IPs) != 0 {
				t.Fatal("expired native references retained", expired)
			}
			assertCount(t, f, "b", 1)
			b.Close()
			eventuallyCount(t, f, "b", 0)
			// A disabled native policy can carry traffic and still has no online map.
			disabled := tunnel(t, f.disabled["a"], "127.0.0.2", "disabled@example.test", target)
			disabled.Close()
			r := f.read(t, "a", "disabled@example.test", false)
			missing := f.read(t, "a", "never-observed@example.test", false)
			if r.State != "untracked" || missing.State != "untracked" || r.Count != nil || missing.Count != nil {
				t.Fatal("invented native policy evidence", r, missing)
			}
			if r := f.read(t, "not-configured", email, false); r.Error != "facility_not_configured" {
				t.Fatal(r)
			}
			f.stop(t, "a")
			if r := f.read(t, "a", email, false); r.Error != "engine_unavailable" {
				t.Fatal("stopped engine became offline", r)
			}
			for id, path := range f.paths {
				data, err := os.ReadFile(path)
				if err != nil || !reflect.DeepEqual(data, f.sources[id]) {
					t.Fatal("observation changed native configuration", err)
				}
			}
		})
	}
}
func assertCount(t *testing.T, f *fixture, id string, want int64) {
	t.Helper()
	r := f.read(t, id, email, false)
	if r.Error != "" || r.Count == nil || *r.Count != fmt.Sprint(want) {
		t.Fatal(id, r)
	}
}
func eventuallyCount(t *testing.T, f *fixture, id string, want int64) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		r := f.read(t, id, email, false)
		if r.Count != nil && *r.Count == fmt.Sprint(want) && r.Error == "" {
			return
		}
		if time.Now().After(end) {
			t.Fatal("native connection references did not expire", r)
		}
		<-ticker.C
	}
}
