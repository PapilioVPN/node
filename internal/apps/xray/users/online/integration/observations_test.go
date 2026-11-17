//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	trafficv1 "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/grpcapi/proto/v1"
	infov1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/grpcapi/proto/v1"
	onlinev1 "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestOnlineObservationsOverMTLSFollowRealConnectionLifetimes(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
			second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
			for _, instance := range []*settings.Instance{&first, &second} {
				testutil.ConfigureXrayAPI(t, instance, network, []string{"StatsService"})
				content, err := os.ReadFile(instance.Xray.ConfigFile)
				if err != nil {
					t.Fatal(err)
				}
				var config map[string]json.RawMessage
				if err := json.Unmarshal(content, &config); err != nil {
					t.Fatal(err)
				}
				var inbounds []map[string]json.RawMessage
				if err := json.Unmarshal(config["inbounds"], &inbounds); err != nil {
					t.Fatal(err)
				}
				inbounds[0]["tag"] = json.RawMessage(`"proxy"`)
				inbounds[0]["settings"] = json.RawMessage(`{"accounts":[{"user":"reader@local","pass":"test-password"}]}`)
				config["inbounds"], err = json.Marshal(inbounds)
				if err != nil {
					t.Fatal(err)
				}
				config["policy"] = json.RawMessage(`{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true,"statsUserOnline":true}}}`)
				content, err = json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			original, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ReadEngineOnline = true
			var server *rpc.Server
			var cleanup struct {
				fx.In
				Closers []io.Closer `group:"module_resources"`
			}
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &cleanup))
			if err := application.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := application.Stop(context.Background()); err != nil {
					t.Error(err)
				}
				if err := server.Close(); err != nil {
					t.Error(err)
				}
				for _, closer := range cleanup.Closers {
					if err := closer.Close(); err != nil {
						t.Error(err)
					}
				}
				testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
				if network == "tcp" {
					testutil.AssertTCPPortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			// Poll only this isolated fixture's startup, never the production adapter.
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for _, id := range []string{"edge-a", "edge-b"} {
				for {
					users, err := client.ListOnlineUsers(ctx, &onlinev1.ListOnlineUsersRequest{InstanceId: id})
					if err == nil {
						if len(users.Emails) != 0 {
							t.Fatalf("fresh instance has online users: %v", users)
						}
						break
					}
					if status.Code(err) != codes.Unavailable {
						t.Fatal(err)
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-ticker.C:
					}
				}
			}
			for _, id := range []string{"edge-a", "edge-b", "missing"} {
				_, countErr := client.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: id, Email: "reader@local"})
				_, ipsErr := client.ListUserOnlineIPs(ctx, &onlinev1.ListUserOnlineIPsRequest{InstanceId: id, Email: "reader@local"})
				for _, err := range []error{countErr, ipsErr} {
					if status.Code(err) != codes.NotFound {
						t.Fatalf("untracked map became zero/empty: %v", err)
					}
				}
			}
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.WriteString(w, "*"); err != nil {
					return
				}
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
				<-r.Context().Done()
			}))
			t.Cleanup(func() { target.CloseClientConnections(); target.Close() })
			started := time.Now().Unix()
			firstFlow := holdOnlineFlow(t, firstAddress, target, "127.0.0.2")
			awaitOnlineCount(t, ctx, client, 1)
			secondFlow := holdOnlineFlow(t, firstAddress, target, "127.0.0.2")
			awaitOnlineCount(t, ctx, client, 1)
			thirdFlow := holdOnlineFlow(t, firstAddress, target, "127.0.0.3")
			awaitOnlineCount(t, ctx, client, 2)
			for range 2 {
				before := time.Now().UnixMilli()
				ips, err := client.ListUserOnlineIPs(ctx, &onlinev1.ListUserOnlineIPsRequest{InstanceId: "edge-a", Email: "reader@local"})
				if err != nil || ips.InstanceId != "edge-a" || ips.Email != "reader@local" || len(ips.Items) != 2 || ips.ObservedAtUnixMs < before {
					t.Fatalf("active IPs: %v, %v", ips, err)
				}
				for i, ip := range []string{"127.0.0.2", "127.0.0.3"} {
					if ips.Items[i].Ip != ip || ips.Items[i].LastSeenUnixSeconds < started || ips.Items[i].LastSeenUnixSeconds > time.Now().Unix() {
						t.Fatalf("native IP/timestamp: %v", ips)
					}
				}
				users, err := client.ListOnlineUsers(ctx, &onlinev1.ListOnlineUsersRequest{InstanceId: "edge-a"})
				if err != nil || !reflect.DeepEqual(users.Emails, []string{"reader@local"}) {
					t.Fatalf("online users: %v, %v", users, err)
				}
			}
			firstFlow()
			awaitOnlineCount(t, ctx, client, 2) // Same-IP second reference remains.
			secondFlow()
			awaitOnlineCount(t, ctx, client, 1)
			remaining, err := client.ListUserOnlineIPs(ctx, &onlinev1.ListUserOnlineIPsRequest{InstanceId: "edge-a", Email: "reader@local"})
			if err != nil || len(remaining.Items) != 1 || remaining.Items[0].Ip != "127.0.0.3" {
				t.Fatalf("wrong IP removed: %v, %v", remaining, err)
			}
			thirdFlow()
			awaitOnlineCount(t, ctx, client, 0)
			empty, err := client.ListUserOnlineIPs(ctx, &onlinev1.ListUserOnlineIPsRequest{InstanceId: "edge-a", Email: "reader@local"})
			if err != nil || len(empty.Items) != 0 {
				t.Fatalf("registered empty map: %v, %v", empty, err)
			}
			for _, id := range []string{"edge-a", "edge-b"} {
				users, err := client.ListOnlineUsers(ctx, &onlinev1.ListOnlineUsersRequest{InstanceId: id})
				if err != nil || len(users.Emails) != 0 {
					t.Fatalf("disconnected/untouched user is online: %v, %v", users, err)
				}
			}
			_, err = client.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: "edge-b", Email: "reader@local"})
			if status.Code(err) != codes.NotFound {
				t.Fatalf("other instance map created: %v", err)
			}
			_, err = client.GetRuntimeInfo(ctx, &infov1.GetRuntimeInfoRequest{InstanceId: "edge-a"})
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("online grant expanded info: %v", err)
			}
			_, err = client.ListTrafficCounters(ctx, &trafficv1.ListTrafficCountersRequest{InstanceId: "edge-a", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER})
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("online grant expanded traffic: %v", err)
			}
			current, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil || !bytes.Equal(original, current) {
				t.Fatalf("online observations mutated configuration: %v", err)
			}
		})
	}
}
