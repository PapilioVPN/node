package grpcapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRuntimeGRPCMissingIdentityAndNativeErrorMapping(t *testing.T) {
	h := Handler{Timeout: time.Second}
	if _, err := h.GetRuntimeStatistics(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{domain.ErrCapacity, codes.ResourceExhausted}, {domain.ErrExecutionChanged, codes.Aborted}, {domain.ErrUnsupported, codes.FailedPrecondition}, {context.Canceled, codes.Canceled}, {errors.New("private source data"), codes.Unavailable}} {
		err := failure(test.cause)
		if status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
}
