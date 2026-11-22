package grpcapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorSanitizationAndPresence(t *testing.T) {
	for _, test := range []struct {
		err  error
		code codes.Code
	}{{domain.ErrInvalidArgument, codes.InvalidArgument}, {domain.ErrCapacity, codes.ResourceExhausted}, {domain.ErrUnsupported, codes.FailedPrecondition}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private credential"), codes.Unavailable}} {
		err := failure(test.err)
		if status.Code(err) != test.code || status.Convert(err).Message() == "private credential" {
			t.Fatal(err)
		}
	}
	p := present(domain.Page{Generation: 1 << 63, ObservedAt: time.Now(), Records: []domain.Record{{StartOffset: 42, NextOffset: 99, Redacted: true, Partial: true}}, Gaps: []domain.Gap{{Reason: "position_uncertain"}}})
	if p.Generation != 1<<63 || p.Records[0].StartOffset != 42 || !p.Records[0].Partial || p.Gaps[0].ExtentKnown {
		t.Fatal(p)
	}
	if _, err := (Handler{}).TailLogs(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
