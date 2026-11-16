//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi/proto/v1"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi/proto/v1"
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
			_, err := client.GetTrafficCounter(t.Context(), &trafficv1.GetTrafficCounterRequest{
				InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND,
				Name: "proxy", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK,
			})
			expected := codes.PermissionDenied
			if granted {
				expected = codes.NotFound
			}
			if status.Code(err) != expected {
				t.Fatalf("independent stats grant: %v; want %v", err, expected)
			}
			_, err = client.ListTrafficCounters(t.Context(), &trafficv1.ListTrafficCountersRequest{
				InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND,
			})
			if status.Code(err) != expected {
				t.Fatalf("independent collection grant: %v; want %v", err, expected)
			}
			if _, err := client.GetTrafficPair(t.Context(), &trafficv1.GetTrafficPairRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user"}); status.Code(err) != expected {
				t.Fatalf("independent pair read grant: %v", err)
			}
			if _, err := client.ResetTrafficPair(t.Context(), &trafficv1.ResetTrafficPairRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user"}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("read grant expanded to pair reset: %v", err)
			}
			_, err = client.ResetTrafficCounter(t.Context(), &trafficv1.ResetTrafficCounterRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, Name: "proxy", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK})
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("read grant expanded to reset: %v", err)
			}
			if _, err := client.ResetTrafficCounters(t.Context(), &trafficv1.ResetTrafficCountersRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, MaxCounters: 1}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("read grant expanded to scope reset: %v", err)
			}
		})
	}
}

func TestTrafficCountersAndResetOverMTLSPreserveNativeUsageAndInstanceIsolation(t *testing.T) {
	for _, test := range []struct {
		network     string
		concurrency int
	}{{"unix", 1}, {"unix", 2}, {"tcp", 1}, {"tcp", 2}} {
		network, concurrency := test.network, test.concurrency
		t.Run(fmt.Sprintf("%s/C=%d", network, concurrency), func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			// Keep every selected endpoint reserved until composition is complete.
			// The management listener also needs a distinct fixed port: instance
			// Start acknowledges spawning before native listeners are necessarily bound.
			var reservations []net.Listener
			reserve := func(address string) string {
				listener, err := net.Listen("tcp", address)
				if err != nil {
					t.Fatal(err)
				}
				reservations = append(reservations, listener)
				t.Cleanup(func() { _ = listener.Close() })
				return listener.Addr().String()
			}
			first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
			reserve(firstAddress)
			second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
			reserve(secondAddress)
			for _, instance := range []*settings.Instance{&first, &second} {
				configureXrayAPI(t, instance, network, []string{"StatsService"})
				if network == "tcp" {
					reserve(instance.Xray.API.Address)
				}
				configureTrafficCounters(t, instance)
				if concurrency > 1 {
					instance.Traffic = &settings.Traffic{ResetConcurrency: concurrency}
				}
			}
			original, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			cfg := certificates.Config()
			cfg.Communications.GRPC.Listen = reserve(cfg.Communications.GRPC.Listen)
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ReadEngineStats = true
			cfg.Communications.GRPC.Permissions.ResetEngineStats = true
			var server *rpc.Server
			var cleanup struct {
				fx.In
				Closers []io.Closer `group:"module_resources"`
			}
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &cleanup))
			for _, listener := range reservations {
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
			}
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
			request := &trafficv1.GetTrafficCounterRequest{InstanceId: "edge-a", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, Name: "proxy", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK}
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
			request.Scope, request.Name = trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, "reader@local"
			if result, err := client.GetTrafficCounter(ctx, request); result != nil || status.Code(err) != codes.NotFound {
				t.Fatalf("absent user counter became zero: %v, %v", result, err)
			}
			emptyUsers, err := client.ListTrafficCounters(ctx, &trafficv1.ListTrafficCountersRequest{InstanceId: "edge-a", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER})
			if err != nil || len(emptyUsers.GetItems()) != 0 || emptyUsers.InstanceId != "edge-a" || emptyUsers.ObservedAtUnixMs == 0 {
				t.Fatalf("absent users must be a successful empty collection: %v, %v", emptyUsers, err)
			}
			absentPair, err := client.GetTrafficPair(ctx, &trafficv1.GetTrafficPairRequest{InstanceId: "edge-a", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "reader@local"})
			if err != nil || absentPair.Uplink != nil || absentPair.Downlink != nil {
				t.Fatalf("absent pair became usage: %v %v", absentPair, err)
			}
			missingReset, err := client.ResetTrafficPair(ctx, &trafficv1.ResetTrafficPairRequest{InstanceId: "edge-a", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "reader@local"})
			if err != nil || len(missingReset.Items) != 2 {
				t.Fatalf("missing pair reset: %v %v", missingReset, err)
			}
			for _, item := range missingReset.Items {
				if item.State != trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_MISSING || item.Acknowledgement != nil {
					t.Fatalf("missing native pair fabricated facts: %v", item)
				}
			}
			if _, err := client.GetTrafficCounter(ctx, request); status.Code(err) != codes.NotFound {
				t.Fatalf("pair reset created a missing counter: %v", err)
			}
			if err := xrayTraffic(ctx, url.UserPassword("reader@local", "test-password").String()+"@"+firstAddress); err != nil {
				t.Fatal(err)
			}
			for scope, name := range map[trafficv1.TrafficScope]string{
				trafficv1.TrafficScope_TRAFFIC_SCOPE_USER:     "reader@local",
				trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND:  "proxy",
				trafficv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND: "direct",
			} {
				collectionRequest := &trafficv1.ListTrafficCountersRequest{InstanceId: "edge-a", Scope: scope}
				before := time.Now().UnixMilli()
				collection, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil || collection.InstanceId != "edge-a" || collection.Scope != scope || collection.ObservedAtUnixMs < before {
					t.Fatalf("native collection metadata: %v, %v", collection, err)
				}
				for _, direction := range []trafficv1.TrafficDirection{trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK, trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK} {
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
				if scope == trafficv1.TrafficScope_TRAFFIC_SCOPE_USER && len(untouched.Items) != 0 {
					t.Fatalf("other users affected: %v", untouched)
				}
				for _, item := range untouched.Items {
					if item.Name == name && item.Bytes != 0 {
						t.Fatalf("collection crossed instance isolation: %v", untouched)
					}
				}
				for _, direction := range []trafficv1.TrafficDirection{trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK, trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK} {
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
					if scope == trafficv1.TrafficScope_TRAFFIC_SCOPE_USER {
						if other != nil || status.Code(err) != codes.NotFound {
							t.Fatalf("other instance user affected: %v, %v", other, err)
						}
					} else if err != nil || other.Bytes != 0 {
						t.Fatalf("other instance traffic affected: %v, %v", other, err)
					}
				}
			}
			// Seed usage in the other real engine before resetting the selected instance.
			if err := xrayTraffic(ctx, url.UserPassword("reader@local", "test-password").String()+"@"+secondAddress); err != nil {
				t.Fatal(err)
			}
			for scope, name := range map[trafficv1.TrafficScope]string{
				trafficv1.TrafficScope_TRAFFIC_SCOPE_USER:     "reader@local",
				trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND:  "proxy",
				trafficv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND: "direct",
			} {
				for _, direction := range []trafficv1.TrafficDirection{trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK, trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK} {
					request.Scope, request.Name, request.Direction, request.InstanceId = scope, name, direction, "edge-b"
					untouched, err := client.GetTrafficCounter(ctx, request)
					if err != nil || untouched.GetBytes() <= 0 {
						t.Fatalf("other instance usage unavailable: %v %v", untouched, err)
					}
					request.InstanceId = "edge-a"
					before, err := client.GetTrafficCounter(ctx, request)
					if err != nil || before.GetBytes() <= 0 {
						t.Fatalf("earlier reset crossed counter identity: %v %v", before, err)
					}
					resetRequest := &trafficv1.ResetTrafficCounterRequest{InstanceId: request.InstanceId, Scope: scope, Name: name, Direction: direction}
					started := time.Now().UnixMilli()
					acknowledgement, err := client.ResetTrafficCounter(ctx, resetRequest)
					if err != nil || acknowledgement.InstanceId != "edge-a" || acknowledgement.Scope != scope || acknowledgement.Name != name || acknowledgement.Direction != direction || acknowledgement.PreviousBytes < before.Bytes || acknowledgement.AcknowledgedAtUnixMs < started {
						t.Fatalf("native reset acknowledgement: %v %v", acknowledgement, err)
					}
					empty, err := client.GetTrafficCounter(ctx, request)
					if err != nil || empty.Bytes != 0 {
						t.Fatalf("native counter was not reset: %v %v", empty, err)
					}
					repeated, err := client.ResetTrafficCounter(ctx, resetRequest)
					if err != nil || repeated.PreviousBytes != 0 {
						t.Fatalf("zero reset became absence: %v %v", repeated, err)
					}
					request.InstanceId = "edge-b"
					other, err := client.GetTrafficCounter(ctx, request)
					if err != nil || other.Bytes != untouched.Bytes {
						t.Fatalf("reset crossed instance ownership: %v %v", other, err)
					}
				}
			}
			if err := xrayTraffic(ctx, url.UserPassword("reader@local", "test-password").String()+"@"+firstAddress); err != nil {
				t.Fatal(err)
			}
			request.InstanceId, request.Scope, request.Name, request.Direction = "edge-a", trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, "reader@local", trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK
			resumed, err := client.GetTrafficCounter(ctx, request)
			if err != nil || resumed.GetBytes() <= 0 {
				t.Fatalf("traffic did not continue after reset: %v %v", resumed, err)
			}
			// Complete each selected scope in the same two real engine processes.
			for _, scope := range []trafficv1.TrafficScope{trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, trafficv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND} {
				collectionRequest := &trafficv1.ListTrafficCountersRequest{InstanceId: "edge-a", Scope: scope}
				before, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil || len(before.Items) < 2 {
					t.Fatalf("real scope unavailable: %v %v", before, err)
				}
				resetRequest := &trafficv1.ResetTrafficCountersRequest{InstanceId: "edge-a", Scope: scope, MaxCounters: uint32(len(before.Items) - 1)}
				if _, err := client.ResetTrafficCounters(ctx, resetRequest); status.Code(err) != codes.ResourceExhausted {
					t.Fatalf("scope cap not enforced: %v", err)
				}
				unchanged, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil || len(unchanged.Items) != len(before.Items) {
					t.Fatalf("preflight changed scope: %v %v", unchanged, err)
				}
				for i, item := range before.Items {
					if unchanged.Items[i].Bytes != item.Bytes {
						t.Fatal("preflight reset usage before rejecting limit")
					}
				}
				collectionRequest.InstanceId = "edge-b"
				otherBefore, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil {
					t.Fatal(err)
				}
				resetRequest.MaxCounters = uint32(len(before.Items))
				report, err := client.ResetTrafficCounters(ctx, resetRequest)
				if err != nil || report.InstanceId != "edge-a" || report.Scope != scope || report.SelectedAtUnixMs == nil || len(report.Items) != len(before.Items) {
					t.Fatalf("scope reset: %v %v", report, err)
				}
				for i, item := range report.Items {
					if item.Name != before.Items[i].Name || item.Direction != before.Items[i].Direction || item.State != trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED || item.Acknowledgement == nil || item.Acknowledgement.PreviousBytes != before.Items[i].Bytes {
						t.Fatalf("scope reset acknowledgement: %v", item)
					}
				}
				otherAfter, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil || len(otherAfter.Items) != len(otherBefore.Items) {
					t.Fatalf("other scope changed: %v %v", otherAfter, err)
				}
				for i, item := range otherBefore.Items {
					if otherAfter.Items[i].Bytes != item.Bytes {
						t.Fatal("scope reset crossed instance ownership")
					}
				}
				collectionRequest.InstanceId = "edge-a"
				empty, err := client.ListTrafficCounters(ctx, collectionRequest)
				if err != nil || len(empty.Items) != len(before.Items) {
					t.Fatalf("reset removed native counters: %v %v", empty, err)
				}
				for _, item := range empty.Items {
					if item.Bytes != 0 {
						t.Fatalf("scope reset not applied: %v", item)
					}
				}
			}
			if err := xrayTraffic(ctx, url.UserPassword("reader@local", "test-password").String()+"@"+firstAddress); err != nil {
				t.Fatalf("traffic did not continue after scope reset: %v", err)
			}
			for scope, name := range map[trafficv1.TrafficScope]string{trafficv1.TrafficScope_TRAFFIC_SCOPE_USER: "reader@local", trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND: "proxy", trafficv1.TrafficScope_TRAFFIC_SCOPE_OUTBOUND: "direct"} {
				pairRequest := &trafficv1.GetTrafficPairRequest{InstanceId: "edge-a", Scope: scope, Name: name}
				before, err := client.GetTrafficPair(ctx, pairRequest)
				if err != nil || before.Uplink == nil || before.Downlink == nil || before.Uplink.Bytes <= 0 || before.Downlink.Bytes <= 0 || before.Uplink.ObservedAtUnixMs <= 0 || before.Downlink.ObservedAtUnixMs <= 0 {
					t.Fatalf("real pair unavailable: %v %v", before, err)
				}
				pairRequest.InstanceId = "edge-b"
				other, err := client.GetTrafficPair(ctx, pairRequest)
				if err != nil || other.Uplink == nil || other.Downlink == nil {
					t.Fatalf("other pair unavailable: %v %v", other, err)
				}
				report, err := client.ResetTrafficPair(ctx, &trafficv1.ResetTrafficPairRequest{InstanceId: "edge-a", Scope: scope, Name: name})
				if err != nil || report.InstanceId != "edge-a" || report.Scope != scope || report.Name != name || len(report.Items) != 2 {
					t.Fatalf("real pair reset: %v %v", report, err)
				}
				for i, item := range report.Items {
					expected := []*trafficv1.TrafficCounterObservation{before.Uplink, before.Downlink}[i]
					if item.Direction != []trafficv1.TrafficDirection{trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK, trafficv1.TrafficDirection_TRAFFIC_DIRECTION_DOWNLINK}[i] || item.State != trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_ACKNOWLEDGED || item.Acknowledgement == nil || item.Acknowledgement.PreviousBytes != expected.Bytes {
						t.Fatalf("pair acknowledgement lost: %v", item)
					}
				}
				afterOther, err := client.GetTrafficPair(ctx, pairRequest)
				if err != nil || afterOther.Uplink == nil || afterOther.Downlink == nil || afterOther.Uplink.Bytes != other.Uplink.Bytes || afterOther.Downlink.Bytes != other.Downlink.Bytes {
					t.Fatalf("pair reset crossed instance identity: %v %v", afterOther, err)
				}
				pairRequest.InstanceId = "edge-a"
				after, err := client.GetTrafficPair(ctx, pairRequest)
				if err != nil || after.Uplink == nil || after.Downlink == nil || after.Uplink.Bytes != 0 || after.Downlink.Bytes != 0 {
					t.Fatalf("pair reset was not applied: %v %v", after, err)
				}
			}
			if err := xrayTraffic(ctx, url.UserPassword("reader@local", "test-password").String()+"@"+firstAddress); err != nil {
				t.Fatalf("traffic did not continue after pair reset: %v", err)
			}

			if _, err := client.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("stats grant expanded to info: %v", err)
			}
			current, err := os.ReadFile(first.Xray.ConfigFile)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("traffic operations changed configuration: %v", err)
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

func TestResetRPCUsesIndependentGrantAndNoEffectDetails(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.Permissions.ResetEngineStats = true
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
	client := xrayClient(t, server, certificates)
	_, err := client.ResetTrafficCounter(t.Context(), &trafficv1.ResetTrafficCounterRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_INBOUND, Name: "proxy", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("reset grant was not wired: %v", err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("no-effect detail missing: %v", err)
	}
	failure, ok := details[0].(*trafficv1.TrafficCounterResetFailure)
	if !ok || failure.Outcome != trafficv1.TrafficCounterResetOutcome_TRAFFIC_COUNTER_RESET_OUTCOME_NOT_APPLIED {
		t.Fatalf("wrong effect detail: %v", details)
	}
	if _, err := client.GetTrafficCounter(t.Context(), &trafficv1.GetTrafficCounterRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("reset expanded to reads: %v", err)
	}
	_, err = client.ResetTrafficCounters(t.Context(), &trafficv1.ResetTrafficCountersRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, MaxCounters: 1})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("scope reset grant not wired: %v", err)
	}
	details = status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("scope progress detail missing: %v", err)
	}
	progress, ok := details[0].(*trafficv1.TrafficCountersResetFailure)
	if !ok || progress.Progress == nil || len(progress.Progress.Items) != 0 || progress.Progress.SelectedAtUnixMs != nil {
		t.Fatalf("scope preflight invented effects: %v", details)
	}
	if _, err := client.GetTrafficPair(t.Context(), &trafficv1.GetTrafficPairRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("reset grant expanded to pair read: %v", err)
	}
	_, err = client.ResetTrafficPair(t.Context(), &trafficv1.ResetTrafficPairRequest{InstanceId: "missing", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: "user"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("pair reset was not wired: %v", err)
	}
	details = status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("pair progress detail absent: %v", err)
	}
	pairFailure, ok := details[0].(*trafficv1.TrafficPairResetFailure)
	if !ok || pairFailure.Progress == nil || len(pairFailure.Progress.Items) != 2 {
		t.Fatalf("pair progress: %v", details)
	}
	for _, item := range pairFailure.Progress.Items {
		if item.State != trafficv1.TrafficCounterResetState_TRAFFIC_COUNTER_RESET_STATE_NOT_ATTEMPTED || item.Acknowledgement != nil {
			t.Fatalf("missing instance fabricated pair effects: %v", item)
		}
	}
}
