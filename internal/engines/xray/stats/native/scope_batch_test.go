package native

import (
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native/scopeapi"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestScopeBatchEffects(t *testing.T) {
	for _, test := range []struct {
		name  string
		code  codes.Code
		reply *scopeapi.ScopeResetResult
		want  error
	}{
		{name: "confirmed", reply: &scopeapi.ScopeResetResult{
			Counters: []*scopeapi.Counter{{Name: "team>>>alice", Downlink: true, PreviousBytes: -1}, {Name: "team>>>alice"}}}},
		{name: "limit", reply: &scopeapi.ScopeResetResult{LimitExceeded: true}, want: traffic.ErrTooLarge},
		{name: "unimplemented", code: codes.Unimplemented, want: traffic.ErrUnavailable},
		{name: "lost reply", code: codes.Unavailable, want: traffic.ErrResetUnknown},
		{name: "receive failure", code: codes.ResourceExhausted, want: traffic.ErrResetUnknown},
		{name: "invalid limit response", reply: &scopeapi.ScopeResetResult{
			LimitExceeded: true, Counters: []*scopeapi.Counter{{Name: "alice"}}}, want: traffic.ErrResetUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := make(chan string, 1)
			conn := startResetTestServer(t, func(_ any, stream grpc.ServerStream) error {
				method, _ := grpc.MethodFromServerStream(stream)
				if method != scopeapi.ExactTraffic_ResetScope_FullMethodName {
					return status.Error(codes.Internal, "unexpected fallback")
				}
				var request scopeapi.ScopeResetRequest
				if err := stream.RecvMsg(&request); err != nil {
					return err
				}
				if request.GetScope() != "user" || request.GetMaxCounters() != 2 {
					return status.Error(codes.Internal, "wrong scope or limit")
				}
				calls <- method
				if test.code != codes.OK {
					return status.Error(test.code, "fixture")
				}
				return stream.SendMsg(test.reply)
			})
			client := NewTraffic(resetConnection{conn}, true)
			result, err := client.ResetScope(t.Context(), traffic.ScopeReset{ID: "primary", Scope: traffic.User, MaxCounters: 2}, 1)
			if !errors.Is(err, test.want) || len(calls) != 1 {
				t.Fatalf("native reset called %d times, error %v, expected %v", len(calls), err, test.want)
			}
			if test.want == nil {
				if len(result) != 2 || result[0].Name != "team>>>alice" || result[0].Direction != traffic.Uplink ||
					result[0].Effect != traffic.ResetAcknowledged || result[0].PreviousBytes != 0 ||
					result[1].Direction != traffic.Downlink || result[1].PreviousBytes != -1 {
					t.Fatalf("unexpected reset report %+v", result)
				}
			} else if result != nil {
				t.Fatalf("unexpected effect report %+v", result)
			}
		})
	}
}
