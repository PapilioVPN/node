package xray

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func TestAPIConstructionAndCanceledAdmissionCreateNoNativeChannel(t *testing.T) {
	client, err := NewAPIClient(APIConfig{Network: "unix", Address: "/unused/api.sock", RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 1})
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
	if _, err := client.ReadTrafficCounter(t.Context(), selection); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatalf("closed traffic admission created a channel: %v", err)
	}
}
