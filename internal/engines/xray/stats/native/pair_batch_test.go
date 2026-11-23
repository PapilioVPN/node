package native

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native/scopeapi"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResetPairBatchEffects(t *testing.T) {
	zero := int64(0)
	negative := int64(-32)
	ack := traffic.DirectionReset{Effect: traffic.ResetAcknowledged}
	missing := traffic.DirectionReset{Effect: traffic.ResetMissing}
	unknown := traffic.DirectionReset{Effect: traffic.ResetUnknown}
	for _, test := range []struct {
		name       string
		code       codes.Code
		reply      *scopeapi.PairResetResult
		want       traffic.PairReset
		err        error
		stockCalls int32
	}{
		{name: "known zero and signed bytes",
			reply: &scopeapi.PairResetResult{PreviousUplink: &zero, PreviousDownlink: &negative},
			want:  traffic.PairReset{Uplink: ack, Downlink: traffic.DirectionReset{Effect: traffic.ResetAcknowledged, PreviousBytes: negative}}},
		{name: "missing uplink",
			reply: &scopeapi.PairResetResult{PreviousDownlink: &zero},
			want:  traffic.PairReset{Uplink: missing, Downlink: ack}},
		{name: "lost result without retry", code: codes.Unavailable, err: traffic.ErrResetUnknown,
			want: traffic.PairReset{Uplink: unknown, Downlink: unknown}},
		{name: "old companion", code: codes.Unimplemented, stockCalls: 2,
			want: traffic.PairReset{Uplink: ack, Downlink: ack}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var batchCalls, stockCalls atomic.Int32
			conn := startResetTestServer(t, func(_ any, stream grpc.ServerStream) error {
				method, _ := grpc.MethodFromServerStream(stream)
				switch method {
				case scopeapi.ExactTraffic_ResetPair_FullMethodName:
					batchCalls.Add(1)
					var req scopeapi.PairResetRequest
					if err := stream.RecvMsg(&req); err != nil {
						return err
					}
					if req.GetScope() != "outbound" || req.GetName() != "team>>>tag" {
						return status.Error(codes.InvalidArgument, "wrong pair selector")
					}
					if test.code != codes.OK {
						return status.Error(test.code, "fixture")
					}
					return stream.SendMsg(test.reply)
				case resetCounter:
					stockCalls.Add(1)
					var req ResetCounterRequest
					if err := stream.RecvMsg(&req); err != nil {
						return err
					}
					return stream.SendMsg(&NamedCounterResponse{Stat: &TrafficCounter{Name: req.GetName()}})
				default:
					return status.Error(codes.Unimplemented, "unexpected RPC")
				}
			})
			client := NewTraffic(resetConnection{conn}, true)
			got, err := client.ResetPair(t.Context(), traffic.CounterPair{ID: "primary", Scope: traffic.Outbound, Name: "team>>>tag"})
			if got != test.want || !errors.Is(err, test.err) || batchCalls.Load() != 1 || stockCalls.Load() != test.stockCalls {
				t.Fatalf("pair = %+v, %v with %d batch and %d stock calls, want %+v, %v",
					got, err, batchCalls.Load(), stockCalls.Load(), test.want, test.err)
			}
		})
	}
}
