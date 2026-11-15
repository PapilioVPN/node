package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/modules/system/app"
)

func TestGetNodeInfo(t *testing.T) {
	service := app.NewInfoService("node-west", "test-version")
	info, err := service.GetNodeInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.NodeID != "node-west" || info.Version != "test-version" || info.APIVersion != "v1" {
		t.Fatalf("unexpected node identity: %+v", info)
	}
	if len(info.Capabilities) != 2 || info.Capabilities[0] != "node.info.read" || info.Capabilities[1] != "instance.status.read" {
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
	_, err := app.NewInfoService("node-west", "test-version").GetNodeInfo(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
