//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/app/proxyman"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	xhttp "github.com/xtls/xray-core/proxy/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNativeBoundedInboundProjectionsOverAllTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			if f.config.Communications.GRPC != nil {
				f.config.Communications.GRPC.MaxSendMessageSize = 256 << 10
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			connection, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			native := handlers.NewHandlerServiceClient(connection)
			current := 0
			for _, count := range []int{0, 1, 50, 51} {
				for ; current < count; current++ {
					reserved := reserveTCP(t)
					port := uint32(reserved.Addr().(*net.TCPAddr).Port)
					reserved.Close()
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					_, err := native.AddInbound(ctx, &handlers.AddInboundRequest{Inbound: &core.InboundHandlerConfig{Tag: fmt.Sprintf("proxy-%03d", current), ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{Listen: xnet.NewIPOrDomain(xnet.LocalHostIP), PortList: &xnet.PortList{Range: []*xnet.PortRange{{From: port, To: port}}}}), ProxySettings: serial.ToTypedMessage(&xhttp.ServerConfig{Accounts: map[string]string{"private-account": "private-password"}})}})
					cancel()
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, kind := range []string{"summaries", "records", "tags"} {
					cursor := ""
					seen := 0
					observed := ""
					for {
						data := catalogPage(t, c, transport, kind, cursor)
						var r struct {
							Inbounds []json.RawMessage `json:"inbounds"`
							Tags     []string          `json:"tags"`
							Next     string            `json:"next_cursor"`
							Observed string            `json:"observed_at"`
							Error    string            `json:"error"`
						}
						if err := json.Unmarshal(data, &r); err != nil || r.Error != "" {
							t.Fatal(string(data), err)
						}
						if observed != "" && observed != r.Observed {
							t.Fatal("continuation refreshed the observation")
						}
						observed = r.Observed
						size := len(r.Inbounds)
						if kind == "tags" {
							size = len(r.Tags)
						}
						if size > 50 {
							t.Fatal("unbounded page", size)
						}
						seen += size
						if kind == "summaries" && (bytes.Contains(data, []byte("private-password")) || bytes.Contains(data, []byte("configuration")) || bytes.Contains(data, []byte("private-account"))) {
							t.Fatal("credential leaked into light projection", string(data))
						}
						cursor = r.Next
						if cursor == "" {
							break
						}
					}
					if seen != count {
						t.Fatal(transport, kind, count, seen)
					}
				}
			}
			observed := compatibility(t, c, transport, "proxy-000")
			if observed.Generation != 1 || observed.Inbound.Protocol != "http" || observed.Inbound.LiveAccounts || observed.Inbound.Reason != "no_user_manager" || observed.ObservedAt.IsZero() {
				t.Fatal(observed)
			}
		})
	}
}
