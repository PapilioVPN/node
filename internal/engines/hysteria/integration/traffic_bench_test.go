package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/hysteria/stats/bridge/grpc/proto/v1"
)

func BenchmarkTrafficAPI(b *testing.B) {
	f := setup(b, "  type: userpass\n  userpass:\n    "+user+": "+password+"\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f.request(b, http.MethodPut, "/execution", nil, 200)
	conn := f.grpcConn(b)
	remote := statsv1.NewHysteriaStatsServiceClient(conn)
	id := &statsv1.HysteriaStatsRequest{Id: "edge"}
	client := f.client(b, user+":"+password, echoTarget(b))
	flow := client.dial(b)
	echo(b, flow, "benchmark traffic")
	native := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}}
	b.Cleanup(native.CloseIdleConnections)
	freshNative := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext, DisableKeepAlives: true}}

	// Use the native endpoint as the reference on the same running child.
	// Origins are deliberately reused for both HTTP paths, including reset
	// and kick. Node must use a fresh connection for those native mutations.
	calls := []struct {
		name, method, nodePath, nativePath string
		nodeBody, nativeBody               []byte
		grpc                               func() error
	}{
		{name: "ListTraffic", method: "GET", nodePath: "/stats/traffic", nativePath: "/traffic",
			grpc: func() error { _, err := remote.ListTraffic(ctx, id); return err }},
		{name: "ListOnlineUsers", method: "GET", nodePath: "/stats/online/users", nativePath: "/online",
			grpc: func() error { _, err := remote.ListOnlineUsers(ctx, id); return err }},
		{name: "GetTraffic", method: "GET", nodePath: "/stats/traffic?user_id=" + user,
			grpc: func() error {
				_, err := remote.GetTraffic(ctx, &statsv1.HysteriaTrafficRequest{Id: "edge", UserId: user})
				return err
			}},
		{name: "Runtime", method: "GET", nodePath: "/stats/runtime",
			grpc: func() error { _, err := remote.GetRuntimeStats(ctx, id); return err }},
		{name: "ResetTrafficEmpty", method: "POST", nodePath: "/stats/traffic/resets", nativePath: "/traffic?clear=1",
			grpc: func() error { _, err := remote.ResetTraffic(ctx, id); return err }},
		{name: "DisconnectAcknowledge", method: "POST", nodePath: "/stats/online/disconnections", nativePath: "/kick",
			nodeBody: []byte(`{"user_ids":["bench-offline-id"]}`), nativeBody: []byte(`["bench-offline-id"]`),
			grpc: func() error {
				_, err := remote.DisconnectUsers(ctx, &statsv1.DisconnectHysteriaUsersRequest{Id: "edge", UserIds: []string{"bench-offline-id"}})
				return err
			}},
	}
	c := benchHTTP{context: ctx, client: f.http.Client(), origin: f.http.URL + instance, header: "X-API-Key", credential: apiKey}
	d := benchHTTP{context: ctx, client: native, origin: "http://" + f.stats, header: "Authorization", credential: statsSecret}
	dFresh := d
	dFresh.client = freshNative
	var initial map[string]struct{ TX, RX uint64 }
	if err := json.Unmarshal(d.call(b, "GET", "/traffic", nil), &initial); err != nil || len(initial) != 1 || initial[user].TX == 0 || initial[user].RX == 0 {
		b.Fatal("reference needs one nonempty user row")
	}
	var online map[string]uint64
	if err := json.Unmarshal(d.call(b, "GET", "/online", nil), &online); err != nil || online[user] != 1 {
		b.Fatal("reference needs one online device")
	}
	for _, op := range calls {
		nativeMethod := op.method
		if strings.HasPrefix(op.nativePath, "/traffic") {
			nativeMethod = "GET"
		}
		if op.name == "ResetTrafficEmpty" {
			d.call(b, nativeMethod, op.nativePath, nil)
		}
		b.Run(op.name+"/HTTP", func(b *testing.B) {
			c.call(b, op.method, op.nodePath, op.nodeBody)
			b.ReportAllocs()
			for b.Loop() {
				c.call(b, op.method, op.nodePath, op.nodeBody)
			}
		})
		b.Run(op.name+"/GRPC", func(b *testing.B) {
			if err := op.grpc(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := op.grpc(); err != nil {
					b.Fatal(err)
				}
			}
		})
		if op.nativePath == "" {
			continue
		}
		b.Run(op.name+"/NativeHTTP", func(b *testing.B) {
			d.call(b, nativeMethod, op.nativePath, op.nativeBody)
			b.ReportAllocs()
			for b.Loop() {
				d.call(b, nativeMethod, op.nativePath, op.nativeBody)
			}
		})
		if op.method == "POST" {
			b.Run(op.name+"/NativeFreshHTTP", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					dFresh.call(b, nativeMethod, op.nativePath, op.nativeBody)
				}
			})
		}
	}
}

type benchHTTP struct {
	context                    context.Context
	client                     *http.Client
	origin, header, credential string
}

func (c benchHTTP) call(b *testing.B, method, path string, body []byte) []byte {
	b.Helper()
	req, err := http.NewRequestWithContext(c.context, method, c.origin+path, bytes.NewReader(body))
	if err != nil {
		b.Fatal(err)
	}
	req.Header.Set(c.header, c.credential)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		b.Fatalf("invalid benchmark response (%d): %v", resp.StatusCode, err)
	}
	return data
}
