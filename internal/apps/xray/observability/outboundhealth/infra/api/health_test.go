package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/xtls/xray-core/app/observatory"
	command "github.com/xtls/xray-core/app/observatory/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestReadRetainsNativeEvidence(t *testing.T) {
	c := &connection{result: &command.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{{OutboundTag: "a", Alive: true, Delay: 7, LastTryTime: 123, LastSeenTime: 120}, {OutboundTag: "burst", HealthPing: &observatory.HealthPingMeasurementResult{All: 3, Fail: 1, Average: 1234567}}}}}}
	got, err := New(c).Read(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("native observations lost: %+v %v", got, err)
	}
	if c.calls != 1 || got[0].DelayMS != 7 || got[0].LastTryUnixSec != 123 || got[1].HealthPing.AverageNS != 1234567 {
		t.Fatal(got, c.calls)
	}
}

func TestNativeCallGrowthAndMalformedBounds(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000, 4096} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			items := make([]*observatory.OutboundStatus, n)
			for i := range items {
				items[i] = &observatory.OutboundStatus{OutboundTag: fmt.Sprint(i), Delay: math.MinInt64, LastTryTime: math.MaxInt64}
			}
			c := &connection{result: &command.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: items}}}
			got, err := New(c).Read(context.Background())
			if err != nil || len(got) != n || c.calls != 1 {
				t.Fatal(len(got), err, c.calls)
			}
			t.Logf("records=%d native RPCs=%d protobuf bytes=%d", n, c.calls, proto.Size(c.result))
		})
	}
	cases := map[string]*command.GetOutboundStatusResponse{
		"missing-result": {},
		"nil-record":     {Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{nil}}},
		"invalid-tag":    {Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{{OutboundTag: string([]byte{255})}}}},
		"record-bound":   {Status: &observatory.ObservationResult{Status: make([]*observatory.OutboundStatus, 4097)}},
		"byte-bound":     {Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{{LastErrorReason: strings.Repeat("private", domain.MaxNativeBytes/7+1)}}}},
	}
	unknown := &command.GetOutboundStatusResponse{Status: &observatory.ObservationResult{}}
	unknown.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	cases["unknown-outer"] = unknown
	unknownObservation := &command.GetOutboundStatusResponse{Status: &observatory.ObservationResult{}}
	unknownObservation.Status.ProtoReflect().SetUnknown([]byte{0x10, 0x01})
	cases["unknown-observation"] = unknownObservation
	unknownStatus := &observatory.OutboundStatus{}
	unknownStatus.ProtoReflect().SetUnknown([]byte{0x40, 0x01})
	cases["unknown-status"] = &command.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{unknownStatus}}}
	h := &observatory.HealthPingMeasurementResult{}
	h.ProtoReflect().SetUnknown([]byte{0x40, 0x01})
	cases["unknown-health"] = &command.GetOutboundStatusResponse{Status: &observatory.ObservationResult{Status: []*observatory.OutboundStatus{{HealthPing: h}}}}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := New(&connection{result: response}).Read(context.Background())
			if err == nil || got != nil {
				t.Fatal(got, err)
			}
		})
	}
}
func TestErrorsPreserveNativeCauses(t *testing.T) {
	for _, tt := range []struct{ native, errorKind error }{
		{status.Error(codes.Unimplemented, "private"), domain.ErrUnsupported},
		{status.Error(codes.ResourceExhausted, "private"), domain.ErrCapacity},
		{status.Error(codes.Canceled, "private"), context.Canceled},
		{status.Error(codes.DeadlineExceeded, "private"), context.DeadlineExceeded},
		{status.Error(codes.Unavailable, "private"), domain.ErrUnavailable},
		{rpc.ErrClosed, domain.ErrClosed},
	} {
		c := &connection{result: &command.GetOutboundStatusResponse{}, err: tt.native}
		got, err := New(c).Read(context.Background())
		if !errors.Is(err, tt.native) || !errors.Is(err, tt.errorKind) || got != nil {
			t.Fatal(got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &connection{}
	if _, err := New(c).Read(ctx); !errors.Is(err, context.Canceled) || c.calls != 0 {
		t.Fatal(err, c.calls)
	}
}
