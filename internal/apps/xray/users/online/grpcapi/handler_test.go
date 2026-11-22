package grpcapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestShapeAndSanitizedFailureDistinctions(t *testing.T) {
	h := Handler{Timeout: time.Second}
	if _, err := h.GetUserOnlineStats(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.ListUserOnlineIPs(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	for _, test := range []struct {
		err     error
		code    codes.Code
		message string
	}{
		{domain.ErrNotConfigured, codes.FailedPrecondition, "facility_not_configured"},
		{domain.ErrUnsupported, codes.FailedPrecondition, "facility_unsupported"},
		{domain.ErrNotRunning, codes.Unavailable, "engine_unavailable"},
		{domain.ErrExecutionChanged, codes.Aborted, "execution_changed"},
		{domain.ErrCapacity, codes.ResourceExhausted, "resource_exhausted"},
		{context.Canceled, codes.Canceled, "canceled"},
		{context.DeadlineExceeded, codes.DeadlineExceeded, "deadline_exceeded"},
		{errors.New("private native details"), codes.Unavailable, "unavailable"},
	} {
		err := failure(test.err)
		if status.Code(err) != test.code || status.Convert(err).Message() != test.message || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
}
