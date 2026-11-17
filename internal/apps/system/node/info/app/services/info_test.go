package services_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/node/info/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/node/info/domain"
)

func TestGetNodeInfo(t *testing.T) {
	service := services.NewInfoService("node-west", "test-version")
	info, err := service.GetNodeInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.NodeID != "node-west" || info.Version != "test-version" || info.APIVersion != "v1" {
		t.Fatalf("unexpected node identity: %+v", info)
	}
	if !slices.Equal(info.Capabilities, []domain.Capability{domain.ReadNodeInfo}) {
		t.Fatalf("advertised unimplemented capabilities: %v", info.Capabilities)
	}
	info.Capabilities[0] = "engine.start"
	again, err := service.GetNodeInfo(context.Background())
	if err != nil || again.Capabilities[0] != "node.info.read" {
		t.Fatalf("caller changed another request's capabilities: %+v, %v", again, err)
	}
}

func TestGetNodeInfoCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := services.NewInfoService("node-west", "test-version").GetNodeInfo(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestGetNodeInfoOwnsDeterministicContributions(t *testing.T) {
	values := []domain.Capability{"z.read", "a.read", "z.read", domain.ReadNodeInfo}
	service := services.NewInfoService("node-west", "test", values...)
	values[1] = "caller.changed"
	first, err := service.GetNodeInfo(context.Background())
	want := []domain.Capability{domain.ReadNodeInfo, "a.read", "z.read"}
	if err != nil || !slices.Equal(first.Capabilities, want) {
		t.Fatalf("unexpected installed operations: %v, %v", first.Capabilities, err)
	}
	first.Capabilities[1] = "response.changed"
	second, err := service.GetNodeInfo(context.Background())
	if err != nil || !slices.Equal(second.Capabilities, want) {
		t.Fatalf("metadata retained caller-owned memory: %v, %v", second.Capabilities, err)
	}
}
