//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestTrafficCounterRPCRequiresIndependentGrant(t *testing.T) {
	for _, granted := range []bool{false, true} {
		t.Run(map[bool]string{false: "info-only", true: "stats-only"}[granted], func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			cfg := certificates.Config()
			cfg.Communications.GRPC.Permissions.ReadEngineInfo = !granted
			cfg.Communications.GRPC.Permissions.ReadEngineStats = granted
			var server *rpc.Server
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
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
			})
			client := xrayClient(t, server, certificates)
			_, err := client.GetTrafficCounter(t.Context(), &xrayv1.GetTrafficCounterRequest{
				InstanceId: "missing", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_INBOUND,
				Name: "proxy", Direction: xrayv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK,
			})
			expected := codes.PermissionDenied
			if granted {
				expected = codes.NotFound
			}
			if status.Code(err) != expected {
				t.Fatalf("independent stats grant: %v; want %v", err, expected)
			}
			_, err = client.ListTrafficCounters(t.Context(), &xrayv1.ListTrafficCountersRequest{
				InstanceId: "missing", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_INBOUND,
			})
			if status.Code(err) != expected {
				t.Fatalf("independent collection grant: %v; want %v", err, expected)
			}
		})
	}
}

func TestTrafficCountersOverMTLSPreserveNativeUsageAndInstanceIsolation(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
			second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
			for _, instance := range []*settings.Instance{&first, &second} {
				configureXrayAPI(t, instance, network, []string{"StatsService"})
				configureTrafficCounters(t, instance)
			}
			original, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ReadEngineStats = true
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
				assertInstancePortsReleased(t, firstAddress, secondAddress)
				if network == "tcp" {
					assertInstancePortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			client := xrayClient(t, server, certificates)
			request := &xrayv1.GetTrafficCounterRequest{InstanceId: "edge-a", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, Name: "proxy", Direction: xrayv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK}
			// Only this isolated test polls readiness. Production makes one RPC.
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for _, id := range []string{"edge-a", "edge-b"} {
				request.InstanceId = id
				for {
					result, err := client.GetTrafficCounter(ctx, request)
					if err == nil {
						if result.Bytes != 0 {
							t.Fatalf("fresh inbound has usage: %v", result)
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
			request.InstanceId = "edge-a"
			request.Scope, request.Name = xrayv1.TrafficScope_TRAFFIC_SCOPE_USER, "reader@local"
			if result, err := client.GetTrafficCounter(ctx, request); result != nil || status.Code(err) != codes.NotFound {
				t.Fatalf("absent user counter became zero: %v, %v", result, err)
			}
			emptyUsers, err := client.ListTrafficCounters(ctx, &xrayv1.ListTrafficCountersRequest{InstanceId: "edge-a", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_USER})
			if err != nil || len(emptyUsers.GetItems()) != 0 || emptyUsers.InstanceId != "edge-a" || emptyUsers.ObservedAtUnixMs == 0 {
				t.Fatalf("absent users must be a successful empty collection: %v, %v", emptyUsers, err)
			}
			if err := xrayTraffic(ctx, url.UserPassword("reader@local", "test-password").String()+"@"+firstAddress); err != nil {
				t.Fatal(err)
			}
			for scope, name := range map[xrayv1.TrafficScope]string{
				xrayv1.TrafficScope_TRAFFIC_SCOPE_USER:     "reader@local",
				xrayv1.TrafficScope_TRAFFIC_SCOPE_INBOUND:  "proxy",
				xrayv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND: "direct",
			} {
				collectionRequest := &xrayv1.ListTrafficCountersRequest{InstanceId: "edge-a", Scope: scope}
				before := time.Now().UnixMilli()
				collection, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil || collection.InstanceId != "edge-a" || collection.Scope != scope || collection.ObservedAtUnixMs < before {
					t.Fatalf("native collection metadata: %v, %v", collection, err)
				}
				for _, direction := range []xrayv1.TrafficDirection{xrayv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK, xrayv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK} {
					found := false
					for _, item := range collection.Items {
						if item.Name == name && item.Direction == direction {
							found = true
							if item.Bytes <= 0 {
								t.Fatalf("collection lost traffic: %v", item)
							}
						}
					}
					if !found {
						t.Fatalf("native collection omitted %s/%s: %v", name, direction, collection)
					}
				}
				repeatedCollection, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil || len(repeatedCollection.Items) != len(collection.Items) {
					t.Fatalf("repeated native collection: %v, %v", repeatedCollection, err)
				}
				for i, item := range collection.Items {
					repeated := repeatedCollection.Items[i]
					if repeated.Name != item.Name || repeated.Direction != item.Direction || repeated.Bytes < item.Bytes {
						t.Fatalf("collection reset/reordered counters: %v, %v", collection, repeatedCollection)
					}
				}
				collectionRequest.InstanceId = "edge-b"
				untouched, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil {
					t.Fatal(err)
				}
				if scope == xrayv1.TrafficScope_TRAFFIC_SCOPE_USER && len(untouched.Items) != 0 {
					t.Fatalf("other users affected: %v", untouched)
				}
				for _, item := range untouched.Items {
					if item.Name == name && item.Bytes != 0 {
						t.Fatalf("collection crossed instance isolation: %v", untouched)
					}
				}
				for _, direction := range []xrayv1.TrafficDirection{xrayv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK, xrayv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK} {
					request.Scope, request.Name, request.Direction, request.InstanceId = scope, name, direction, "edge-a"
					before := time.Now().UnixMilli()
					result, err := client.GetTrafficCounter(ctx, request)
					if err != nil || result.InstanceId != "edge-a" || result.Scope != scope || result.Name != name || result.Direction != direction || result.Bytes <= 0 || result.ObservedAtUnixMs < before {
						t.Fatalf("native usage: %v, %v", result, err)
					}
					repeated, err := client.GetTrafficCounter(ctx, request)
					if err != nil || repeated.Bytes < result.Bytes {
						t.Fatalf("read reset native counter: %v, %v", repeated, err)
					}
					request.InstanceId = "edge-b"
					other, err := client.GetTrafficCounter(ctx, request)
					if scope == xrayv1.TrafficScope_TRAFFIC_SCOPE_USER {
						if other != nil || status.Code(err) != codes.NotFound {
							t.Fatalf("other instance user affected: %v, %v", other, err)
						}
					} else if err != nil || other.Bytes != 0 {
						t.Fatalf("other instance traffic affected: %v, %v", other, err)
					}
				}
			}
			if _, err := client.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("stats grant expanded to info: %v", err)
			}
			current, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("read changed configuration: %v", err)
			}
		})
	}
}

func configureTrafficCounters(t *testing.T, instance *settings.Instance) {
	t.Helper()
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	var inbounds, outbounds []map[string]json.RawMessage
	if err := json.Unmarshal(config["inbounds"], &inbounds); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(config["outbounds"], &outbounds); err != nil {
		t.Fatal(err)
	}
	inbounds[0]["tag"] = json.RawMessage(`"proxy"`)
	inbounds[0]["settings"] = json.RawMessage(`{"accounts":[{"user":"reader@local","pass":"test-password"}]}`)
	outbounds[0]["tag"] = json.RawMessage(`"direct"`)
	config["inbounds"], err = json.Marshal(inbounds)
	if err != nil {
		t.Fatal(err)
	}
	config["outbounds"], err = json.Marshal(outbounds)
	if err != nil {
		t.Fatal(err)
	}
	config["policy"] = json.RawMessage(`{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true}},"system":{"statsInboundUplink":true,"statsInboundDownlink":true,"statsOutboundUplink":true,"statsOutboundDownlink":true}}`)
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
