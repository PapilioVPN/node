//go:build integration && linux

package integration_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/api"
	"github.com/PapilioVPN/node/internal/testutil"
	stats "github.com/xtls/xray-core/app/stats/command"
)

func TestNativeCollectionsPagesIsolationAndRestart(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	brokerURL := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := echoTarget(t)
			f := newFixture(t, pki, brokerURL, transport)
			exerciseCollectionWorkflow(t, f, target)
		})
	}
}

func exerciseCollectionWorkflow(t *testing.T, f *fixture, target string) {
	t.Helper()
	since := time.Now().Unix()
	var connections []net.Conn
	for _, n := range []int{0, 1, 50, 51} {
		for i := len(connections); i < n; i++ {
			connections = append(connections, tunnel(t, f.proxies["a"], "127.0.0.2", fmt.Sprintf("online-%03d@example.test", i), target))
		}
		for _, operation := range []string{"users", "records"} {
			first := f.page(t, "a", operation, "", "", 0)
			if first.Error != "" || first.Page.Total != n || len(first.Records) != min(n, 50) || !first.Page.Fresh || first.Page.Expires.Sub(first.Observation.ObservedAt) != domain.ObservationTTL {
				t.Fatal(n, operation, first)
			}
			for i, row := range first.Records {
				if row.Email != fmt.Sprintf("online-%03d@example.test", i) {
					t.Fatal(row)
				}
				if operation == "records" && (row.IP != "127.0.0.2" || row.LastSeen < since) {
					t.Fatal(row)
				}
			}
			if n == 51 {
				second := f.page(t, "a", operation, "", first.Page.Next, 0)
				if second.Error != "" || len(second.Records) != 1 || second.Records[0].Email != "online-050@example.test" || second.Page.Fresh || second.Page.ID != first.Page.ID || !second.Observation.ObservedAt.Equal(first.Observation.ObservedAt) {
					t.Fatal(second)
				}
			}
		}
		verifyNativeCalls(t, f, n)
	}
	// Retained pages keep their observation while fresh native reads see expiry.
	retained := f.page(t, "a", "users", "", "", 0)
	connections[50].Close()
	end := time.Now().Add(3 * time.Second)
	for {
		r := f.read(t, "a", "online-050@example.test", false)
		if r.Error == "" && r.Count != nil && *r.Count == "0" {
			break
		}
		if time.Now().After(end) {
			t.Fatal("native expiry", r)
		}
		time.Sleep(10 * time.Millisecond)
	}
	continued := f.page(t, "a", "users", "", retained.Page.Next, 0)
	if continued.Error != "" || continued.Page.Fresh || len(continued.Records) != 1 || continued.Records[0].Email != "online-050@example.test" {
		t.Fatal(continued)
	}
	current := f.page(t, "a", "users", "", "", 0)
	if current.Error != "" || current.Page.Total != 50 {
		t.Fatal(current)
	}
	connections[50] = tunnel(t, f.proxies["a"], "127.0.0.2", "online-050@example.test", target)
	for _, operation := range []string{"users", "records", "ips"} {
		if r := f.page(t, "unsupported", operation, email, "", 0); r.Error != "facility_unsupported" {
			t.Fatal(r)
		}
	}
	// Same email is separately observed in the second engine (Unix API).
	same := tunnel(t, f.proxies["b"], "127.0.0.3", "online-000@example.test", target)
	b := f.page(t, "b", "records", "", "", 1)
	if b.Error != "" || b.Page.Total != 1 || b.Records[0].IP != "127.0.0.3" {
		t.Fatal(b)
	}
	first := f.page(t, "a", "records", "", "", 1)
	if r := f.page(t, "b", "records", "", first.Page.Next, 1); r.Error != "invalid_argument" {
		t.Fatal("cross-instance cursor", r)
	}
	if r := f.page(t, "a", "users", "", first.Page.Next, 1); r.Error != "invalid_argument" {
		t.Fatal("cross-operation cursor", r)
	}
	// Actual IP sets above 50 are paged without one RPC per IP.
	for i := range 51 {
		tunnel(t, f.proxies["a"], fmt.Sprintf("127.0.1.%d", i+1), email, target)
	}
	ips := f.page(t, "a", "ips", email, "", 0)
	if ips.Error != "" || len(ips.IPs) != 50 || ips.Page.Total != 51 {
		t.Fatal(ips)
	}
	last := f.page(t, "a", "ips", email, ips.Page.Next, 0)
	if last.Error != "" || len(last.IPs) != 1 || last.Page.Fresh {
		t.Fatal(last)
	}
	special := "special>>>identity@example.test"
	tunnel(t, f.proxies["a"], "127.0.0.2", special, target)
	names := f.page(t, "a", "users", "", "", 0)
	tail := f.page(t, "a", "users", "", names.Page.Next, 0)
	if tail.Error != "" || len(tail.Records) != 3 || tail.Records[2].Email != special {
		t.Fatal("opaque native email lost", tail)
	}
	if r := f.page(t, "a", "records", "", "", 0); r.Error != "native_identity_unsupported" {
		t.Fatal(r)
	}
	if r := f.read(t, "a", special, true); r.Error != "" || len(r.IPs) != 1 {
		t.Fatal("exact opaque identity failed", r)
	}

	// Restart invalidates retained pages; a new observation sees no old sessions.
	f.restart(t, "a")
	for _, operation := range []string{"users", "records", "ips"} {
		token := first.Page.Next
		size := 1
		account := ""
		if operation == "users" {
			token = retained.Page.Next
			size = 0
		}
		if operation == "ips" {
			token = ips.Page.Next
			size = 0
			account = email
		}
		if r := f.page(t, "a", operation, account, token, size); r.Error != "execution_changed" {
			t.Fatal("stale engine page", r)
		}
	}
	fresh := f.page(t, "a", "users", "", "", 0)
	if fresh.Error != "" || fresh.Page.Total != 0 || fresh.Observation.Generation == first.Observation.Generation {
		t.Fatal(fresh)
	}
	if r := f.page(t, "b", "records", "", "", 0); r.Error != "" || r.Page.Total != 1 {
		t.Fatal("restart leaked", r)
	}
	same.Close()
	if r := f.page(t, "not-configured", "users", "", "", 0); r.Error != "facility_not_configured" {
		t.Fatal(r)
	}
}

func verifyNativeCalls(t *testing.T, f *fixture, size int) {
	t.Helper()
	c := &measuredConnection{ClientConnInterface: f.native["a"], calls: map[string]int{}, bytes: map[string]int{}}
	reader := api.New(c)
	s, _ := services.New([]services.Binding{{ID: "a", Reader: reader, Collection: reader}}, nativeRunning{})
	for _, op := range []func(context.Context, domain.PageRequest) (domain.Page, error){s.List, s.Records} {
		r := domain.PageRequest{Selection: domain.Selection{InstanceID: "a"}}
		count := 0
		for {
			p, err := op(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			count += len(p.Records)
			r.PageToken = p.NextPageToken
			if r.PageToken == "" {
				break
			}
		}
		if count != size {
			t.Fatal(count, size)
		}
	}
	if c.calls[stats.StatsService_GetAllOnlineUsers_FullMethodName] != 2 || c.calls[stats.StatsService_GetUsersStats_FullMethodName] != 1 || len(c.calls) != 2 {
		t.Fatal(c.calls)
	}
	t.Logf("real Xray size=%d calls=%v protobuf_bytes=%v", size, c.calls, c.bytes)
}
