package grpcapi

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	loggersv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLoggerGRPCNilInputAndPrivateErrorMapping(t *testing.T) {
	handler := Handler{}
	if _, err := handler.ReopenLogger(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := handler.GetLoggerControlStatus(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{domain.ErrUncertain, codes.FailedPrecondition}, {domain.ErrCapacity, codes.ResourceExhausted}, {domain.ErrExecutionChanged, codes.Aborted}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private log path"), codes.Unavailable}} {
		err := failure(test.cause)
		if status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
	evidence := operationResult(domain.Operation{Generation: math.MaxUint64, Effect: domain.Unknown, Acknowledged: true, RequiresInstanceRestart: true})
	withDetails, err := status.Convert(failure(domain.ErrUncertain)).WithDetails(evidence)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := withDetails.Details()[0].(*loggersv1.LoggerOperation)
	if !ok || restored.Generation != math.MaxUint64 || !restored.Acknowledged || !restored.RequiresInstanceRestart || restored.Effect != "unknown" {
		t.Fatal("gRPC details lost control evidence")
	}
}
