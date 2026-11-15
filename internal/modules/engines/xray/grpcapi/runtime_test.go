package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
)

type runtimeReader func(context.Context, instances.InstanceID) (domain.InstanceRuntimeInfo, error)

func (f runtimeReader) GetRuntimeInfo(ctx context.Context, id instances.InstanceID) (domain.InstanceRuntimeInfo, error) {
	return f(ctx, id)
}

func TestRuntimeInfoChecksIdentityGrantAndShapeBeforeApplication(t *testing.T) {
	for _, test := range []struct {
		ctx   context.Context
		grant bool
		code  codes.Code
	}{
		{t.Context(), true, codes.Unauthenticated}, {verifiedContext(t), false, codes.PermissionDenied},
	} {
		h := grpcapi.Handler{AllowInfoRead: test.grant}
		if _, err := h.GetRuntimeInfo(test.ctx, nil); status.Code(err) != test.code {
			t.Fatalf("authorization order: %v", err)
		}
	}
	h := grpcapi.Handler{AllowInfoRead: true}
	for _, request := range []*xrayv1.GetRuntimeInfoRequest{nil, {}, {InstanceId: " "}} {
		if _, err := h.GetRuntimeInfo(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("shape reached application: %v", err)
		}
	}
}

func TestRuntimeInfoPreservesSelectionUnitsAndErrors(t *testing.T) {
	ctx := verifiedContext(t)
	observed := time.UnixMilli(1700000000123).UTC()
	h := grpcapi.Handler{AllowInfoRead: true, Runtime: runtimeReader(func(received context.Context, id instances.InstanceID) (domain.InstanceRuntimeInfo, error) {
		if received != ctx || id != " edge " {
			t.Fatal("identity/context changed")
		}
		return domain.InstanceRuntimeInfo{InstanceID: id, Runtime: domain.RuntimeInfo{Goroutines: math.MaxUint32, GCCount: 3, AllocatedBytes: math.MaxUint64, TotalAllocatedBytes: 5, SysBytes: 6, Mallocs: 7, Frees: 8, LiveObjects: 9, TotalGCPauseNS: 10, UptimeSeconds: math.MaxUint32, ObservedAt: observed}}, nil
	})}
	response, err := h.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: " edge "})
	if err != nil {
		t.Fatal(err)
	}
	i := response.GetRuntime()
	if response.InstanceId != " edge " || i.GetGoroutines() != math.MaxUint32 || i.GcCount != 3 || i.AllocatedBytes != math.MaxUint64 || i.TotalAllocatedBytes != 5 || i.SysBytes != 6 || i.Mallocs != 7 || i.Frees != 8 || i.LiveObjects != 9 || i.TotalGcPauseNs != 10 || i.UptimeSeconds != math.MaxUint32 || i.ObservedAtUnixMs != observed.UnixMilli() {
		t.Fatalf("wire facts changed: %v", response)
	}
	for cause, expected := range map[error]codes.Code{
		instances.ErrNotFound:                 codes.NotFound,
		domain.ErrAPIDisabled:                 codes.FailedPrecondition,
		domain.ErrRuntimeInfoUnsupported:      codes.FailedPrecondition,
		domain.ErrAPIBusy:                     codes.ResourceExhausted,
		domain.ErrAPIResourceLimit:            codes.ResourceExhausted,
		context.Canceled:                      codes.Canceled,
		context.DeadlineExceeded:              codes.DeadlineExceeded,
		errors.New("native private endpoint"): codes.Unavailable,
	} {
		h.Runtime = runtimeReader(func(context.Context, instances.InstanceID) (domain.InstanceRuntimeInfo, error) {
			return domain.InstanceRuntimeInfo{Runtime: domain.RuntimeInfo{Goroutines: 99}}, fmt.Errorf("private endpoint: %w", cause)
		})
		if response, err := h.GetRuntimeInfo(ctx, &xrayv1.GetRuntimeInfoRequest{InstanceId: "edge"}); response != nil || status.Code(err) != expected || strings.Contains(err.Error(), "private") {
			t.Fatalf("mapped error: %v, %v; want %v", response, err, expected)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	h.Runtime = runtimeReader(func(context.Context, instances.InstanceID) (domain.InstanceRuntimeInfo, error) {
		cancel()
		return domain.InstanceRuntimeInfo{}, nil
	})
	if response, err := h.GetRuntimeInfo(canceled, &xrayv1.GetRuntimeInfoRequest{InstanceId: "edge"}); response != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("cancellation became success: %v, %v", response, err)
	}
}
