package grpcapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/grpcapi"
	runtimev1 "github.com/PapilioVPN/node/internal/apps/system/node/runtime/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNodeGRPCExactValuesAndAbsentResources(t *testing.T) {
	reader := &reader{}
	handler := grpcapi.Handler{Service: services.Service{Reader: reader, Probes: reader}, Timeout: time.Second}
	ctx := context.Background()
	info, err := handler.GetNodeInfo(ctx, &runtimev1.GetNodeInfoRequest{})
	if err != nil || info.Pid != 42 || info.Modified == nil || *info.Modified || info.Revision != nil {
		t.Fatalf("%+v %v", info, err)
	}
	resources, err := handler.GetNodeResources(ctx, &runtimev1.GetNodeResourcesRequest{})
	if err != nil || resources.StartTicks != 9007199254740993 {
		t.Fatalf("%+v %v", resources, err)
	}
	diagnostics, err := handler.GetNodeDiagnostics(ctx, &runtimev1.GetNodeDiagnosticsRequest{})
	if err != nil || diagnostics.Healthy || diagnostics.Http == nil || !diagnostics.Http.Ready || diagnostics.Grpc != nil || diagnostics.Nats.RoundTripNs != nil {
		t.Fatalf("%+v %v", diagnostics, err)
	}
	reader.cause = context.DeadlineExceeded
	if _, err := handler.GetNodeResources(ctx, &runtimev1.GetNodeResourcesRequest{}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatal(err)
	}
}
