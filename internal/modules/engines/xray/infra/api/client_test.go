package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func TestAPIConstructionAndCanceledAdmissionCreateNoNativeChannel(t *testing.T) {
	client, err := NewClient(Config{Network: "unix", Address: "/unused/api.sock", RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.ReadRuntimeInfo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission: %v", err)
	}
	if client.connection != nil {
		t.Fatal("construction/canceled admission allocated native workers")
	}
	addition := domain.InboundUserAddition{InboundTag: "in", User: domain.InboundUser{Email: "user", Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "test-only"}}}}
	if _, err := client.AddInboundUser(ctx, addition); !errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrUserMutationUnknown) || client.connection != nil {
		t.Fatalf("canceled addition created native effects/workers: %v", err)
	}
	if _, err := client.ReadInboundUserCount(ctx, "in"); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatalf("canceled count created native workers: %v", err)
	}
	selection := domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink}
	if _, err := client.ReadTrafficCounter(ctx, selection); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatalf("canceled traffic admission created a channel: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("Close before first read: %v", err)
	}
	if client.connection != nil {
		t.Fatal("closed admission created native channel")
	}
	if _, err := client.AddInboundUser(t.Context(), addition); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, domain.ErrUserMutationUnknown) || client.connection != nil {
		t.Fatalf("closed addition created native effects/workers: %v", err)
	}
	if _, err := client.ReadInboundUserCount(t.Context(), "in"); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatalf("closed count created native workers: %v", err)
	}
	if _, err := client.ReadTrafficCounter(t.Context(), selection); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatalf("closed traffic admission created a channel: %v", err)
	}
}
