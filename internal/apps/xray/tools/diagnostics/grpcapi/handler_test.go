package grpcapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	diagnosticsv1 "github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestTypedReportPrivacyAndFailures(t *testing.T) {
	s, err := services.New([]services.Binding{{ID: "edge"}}, statusReader{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: s, Timeout: time.Second}
	r, err := h.GetInstanceDiagnostics(context.Background(), &diagnosticsv1.GetInstanceDiagnosticsRequest{InstanceId: "edge"})
	if err != nil || r.Execution.Generation != 9007199254740993 || r.Execution.FailureReason != "execution_failed" {
		t.Fatal(r, err)
	}
	data, err := protojson.Marshal(r)
	if err != nil || strings.Contains(string(data), "private") {
		t.Fatal(string(data), err)
	}
	raw, err := proto.Marshal(r)
	if err != nil || strings.Contains(string(raw), "private") {
		t.Fatal("unsafe protobuf", err)
	}
	if _, err := h.GetInstanceDiagnostics(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.GetInstanceDiagnostics(context.Background(), &diagnosticsv1.GetInstanceDiagnosticsRequest{InstanceId: "absent"}); status.Code(err) != codes.NotFound {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := h.GetInstanceDiagnostics(ctx, &diagnosticsv1.GetInstanceDiagnosticsRequest{InstanceId: "edge"}); r != nil || status.Code(err) != codes.Canceled {
		t.Fatal(r, err)
	}
}

func TestReportProtobufByteLimit(t *testing.T) {
	s, err := services.New([]services.Binding{{ID: "edge", Binary: largeBinary{}}}, statusReader{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: s, Timeout: time.Second}
	r, err := h.GetInstanceDiagnostics(context.Background(), &diagnosticsv1.GetInstanceDiagnosticsRequest{InstanceId: "edge"})
	if r != nil || status.Code(err) != codes.ResourceExhausted {
		t.Fatal(r, err)
	}
}
