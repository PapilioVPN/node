package xray

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
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
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("Close before first read: %v", err)
	}
	if client.connection != nil {
		t.Fatal("closed admission created native channel")
	}
}
