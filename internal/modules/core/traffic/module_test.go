package traffic_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"math"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/core/traffic"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi"
	trafficv1 "github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
)

type backend struct{ reads, lists, resets int }

func (b *backend) ReadTrafficCounter(ctx context.Context, s domain.TrafficSelection) (domain.TrafficCounter, error) {
	b.reads++
	if _, ok := ctx.Deadline(); !ok || s.Scope != domain.UserTraffic || s.Name != " opaque>>>name " {
		panic("lost operation deadline or exact selection")
	}
	return domain.TrafficCounter{Bytes: math.MinInt64, ObservedAt: time.UnixMilli(1700000000123)}, nil
}

func (b *backend) ReadTrafficCounters(context.Context, domain.TrafficScope) (domain.TrafficCounters, error) {
	b.lists++
	return domain.TrafficCounters{Items: []domain.TrafficCounterEntry{
		{Name: "z", Direction: domain.Uplink}, {Name: " opaque>>>name ", Direction: domain.Downlink},
	}, ObservedAt: time.UnixMilli(1700000000123)}, nil
}

func (b *backend) ResetTrafficCounter(_ context.Context, s domain.TrafficSelection) (domain.TrafficCounterReset, error) {
	b.resets++
	if s.Scope != domain.UserTraffic {
		panic("changed scope")
	}
	return domain.TrafficCounterReset{PreviousBytes: math.MaxInt64, AcknowledgedAt: time.UnixMilli(1700000000124)}, nil
}

type bindings struct {
	fx.Out
	Values []traffic.BackendBinding `group:"traffic_backends,flatten"`
}

func trafficAPI(t *testing.T, p settings.GRPCPermissions, values []traffic.BackendBinding) grpcapi.TrafficAPI {
	t.Helper()
	var api grpcapi.TrafficAPI
	app := fx.New(fx.NopLogger, fx.Supply(p), fx.Provide(func() bindings { return bindings{Values: values} }), traffic.Module(true), fx.Populate(&api))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return api
}

func TestTrafficModuleConsumesFocusedPortsWithoutAnEngineModule(t *testing.T) {
	b := &backend{}
	api := trafficAPI(t, settings.GRPCPermissions{ReadEngineStats: true, ResetEngineStats: true}, []traffic.BackendBinding{
		{InstanceID: " edge ", Reader: b, Collection: b, Resetter: b, Timeout: time.Second}, {InstanceID: "disabled"},
	})
	// Controlled unit peer; native bootstrap tests retain actual mTLS verification.
	ctx, cancel := context.WithTimeout(peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}}}), time.Second)
	defer cancel()
	request := &trafficv1.GetTrafficCounterRequest{InstanceId: " edge ", Scope: trafficv1.TrafficScope_TRAFFIC_SCOPE_USER, Name: " opaque>>>name ", Direction: trafficv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK}
	one, err := api.GetTrafficCounter(ctx, request)
	if err != nil || one.Bytes != math.MinInt64 || one.InstanceId != request.InstanceId || one.Name != request.Name || one.ObservedAtUnixMs != 1700000000123 || b.reads != 1 {
		t.Fatalf("single binding lost facts or repeated calls: %v %v", one, err)
	}
	list, err := api.ListTrafficCounters(ctx, &trafficv1.ListTrafficCountersRequest{InstanceId: request.InstanceId, Scope: request.Scope})
	if err != nil || len(list.Items) != 2 || list.Items[0].Name != request.Name || list.Items[1].Name != "z" || b.lists != 1 {
		t.Fatalf("collection binding lost ordering: %v %v", list, err)
	}
	pair, err := api.GetTrafficPair(ctx, &trafficv1.GetTrafficPairRequest{InstanceId: request.InstanceId, Scope: request.Scope, Name: request.Name})
	if err != nil || pair.Uplink == nil || pair.Downlink == nil || pair.Downlink.Bytes != math.MinInt64 || b.reads != 3 {
		t.Fatalf("pair binding changed exact-call path: %v %v", pair, err)
	}
	reset, err := api.ResetTrafficCounter(ctx, &trafficv1.ResetTrafficCounterRequest{InstanceId: request.InstanceId, Scope: request.Scope, Name: request.Name, Direction: request.Direction})
	if err != nil || reset.PreviousBytes != math.MaxInt64 || b.resets != 1 {
		t.Fatalf("single reset binding lost acknowledgement: %v %v", reset, err)
	}
	resets, err := api.ResetTrafficCounters(ctx, &trafficv1.ResetTrafficCountersRequest{InstanceId: request.InstanceId, Scope: request.Scope, MaxCounters: 2})
	if err != nil || len(resets.Items) != 2 || resets.Items[0].Acknowledgement.PreviousBytes != math.MaxInt64 || b.lists != 2 || b.resets != 3 {
		t.Fatalf("scope reset binding changed calls/effects: %v %v", resets, err)
	}
	paired, err := api.ResetTrafficPair(ctx, &trafficv1.ResetTrafficPairRequest{InstanceId: request.InstanceId, Scope: request.Scope, Name: request.Name})
	if err != nil || len(paired.Items) != 2 || paired.Items[1].Acknowledgement.PreviousBytes != math.MaxInt64 || b.resets != 5 {
		t.Fatalf("pair reset binding lost acknowledgement: %v %v", paired, err)
	}
	for id, want := range map[string]codes.Code{"missing": codes.NotFound, "disabled": codes.FailedPrecondition} {
		response, err := api.GetTrafficCounter(ctx, &trafficv1.GetTrafficCounterRequest{InstanceId: id, Scope: request.Scope, Name: request.Name, Direction: request.Direction})
		if response != nil || status.Code(err) != want || b.reads != 3 {
			t.Fatalf("lost missing/disabled distinction: %v", err)
		}
	}
}

func TestTrafficModuleKeepsReadAndResetGrantsIndependent(t *testing.T) {
	ctx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}}})
	reads := trafficAPI(t, settings.GRPCPermissions{ReadEngineStats: true}, nil)
	if _, err := reads.ResetTrafficCounter(ctx, nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal("read grant enabled reset")
	}
	resets := trafficAPI(t, settings.GRPCPermissions{ResetEngineStats: true}, nil)
	if _, err := resets.GetTrafficCounter(ctx, nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal("reset grant enabled observations")
	}
}

func TestTrafficModuleRejectsAmbiguousBackendOwnership(t *testing.T) {
	var api grpcapi.TrafficAPI
	app := fx.New(fx.NopLogger, fx.Supply(settings.GRPCPermissions{}), fx.Provide(func() bindings {
		return bindings{Values: []traffic.BackendBinding{{InstanceID: "same"}, {InstanceID: "same"}}}
	}), traffic.Module(true), fx.Populate(&api))
	if app.Err() == nil {
		t.Fatal("duplicate traffic ownership was silently selected")
	}
}
