//go:build integration && linux

package integration_test

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	xhttp "github.com/xtls/xray-core/proxy/http"
	"google.golang.org/protobuf/proto"
)

func TestNativeInboundTrafficOverIndependentTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "inbound-roundtrip") }))
			defer target.Close()
			f := newFixture(t, pki, broker, transport)
			original, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			client := startNode(t, f)
			awaitNative(t, f.native)
			socket, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := socket.Addr().String()
			port := uint32(socket.Addr().(*net.TCPAddr).Port)
			socket.Close()
			tag := "proxy /+? " + transport
			config := &core.InboundHandlerConfig{Tag: tag, ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{Listen: xnet.NewIPOrDomain(xnet.LocalHostIP), PortList: &xnet.PortList{Range: []*xnet.PortRange{{From: port, To: port}}}}), ProxySettings: serial.ToTypedMessage(&xhttp.ServerConfig{})}
			content, err := proto.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			result, code := nativeMutation(t, client, transport, "add", "", content)
			if code != "" || result.Effect != "completed" || !result.Acknowledged || result.Tag != tag || result.Generation != 1 {
				t.Fatalf("add %+v %s", result, code)
			}
			proxyURL, _ := url.Parse("http://" + address)
			tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			traffic := &http.Client{Transport: tr, Timeout: time.Second}
			response, err := traffic.Get(target.URL)
			if err != nil {
				t.Fatal("added inbound cannot carry traffic", err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "inbound-roundtrip" {
				t.Fatalf("traffic %q %v", body, err)
			}
			records, _ := nativeRecords(t, client, transport, false)
			found := false
			for _, record := range records {
				if record.Tag == tag {
					var actual core.InboundHandlerConfig
					if err := proto.Unmarshal(record.Configuration, &actual); err != nil || !proto.Equal(config, &actual) {
						t.Fatalf("native configuration lost settings %v %v", &actual, err)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("added configuration absent")
			}
			_, tags := nativeRecords(t, client, transport, true)
			if !slices.Contains(tags, tag) {
				t.Fatal("native tag projection absent")
			}
			failed, code := nativeMutation(t, client, transport, "add", "", content)
			if code != "unavailable" || failed.Effect != "unknown" || failed.Acknowledged || failed.RequiresInstanceRestart {
				t.Fatalf("duplicate tag falsely treated as rollback %+v %s", failed, code)
			}
			result, code = nativeMutation(t, client, transport, "remove", tag, nil)
			if code != "" || result.Effect != "completed" || !result.Acknowledged {
				t.Fatalf("remove %+v %s", result, code)
			}
			_, tags = nativeRecords(t, client, transport, true)
			if slices.Contains(tags, tag) {
				t.Fatal("removed inbound retained")
			}
			if response, err := traffic.Get(target.URL); err == nil {
				response.Body.Close()
				t.Fatal("removed inbound still accepts new traffic")
			}
			after, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("live mutations changed desired source", err)
			}
		})
	}
}
