package api

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type connectionFixture struct {
	calls  int
	err    error
	result *stats.SysStatsResponse
}

func (c *connectionFixture) Invoke(ctx context.Context, method string, input, output any, _ ...grpc.CallOption) error {
	c.calls++
	if method != "/xray.app.stats.command.StatsService/GetSysStats" {
		return errors.New("unexpected native operation")
	}
	if c.err != nil {
		return c.err
	}
	proto.Merge(output.(*stats.SysStatsResponse), c.result)
	return nil
}
func (c *connectionFixture) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("statistics must be one unary read")
}
func TestNativeStatisticsPreserveAllFieldsAndIntegerWidths(t *testing.T) {
	row := &stats.SysStatsResponse{NumGoroutine: math.MaxUint32, NumGC: math.MaxUint32, Alloc: math.MaxUint64, TotalAlloc: math.MaxUint64 - 1, Sys: math.MaxUint64 - 2, Mallocs: math.MaxUint64 - 3, Frees: math.MaxUint64 - 4, LiveObjects: math.MaxUint64 - 5, PauseTotalNs: math.MaxUint64 - 6, Uptime: math.MaxUint32}
	connection := &connectionFixture{result: row}
	reader := New(connection)
	result, err := reader.Read(context.Background())
	if err != nil || connection.calls != 1 || result.Goroutines != row.NumGoroutine || result.GCCycles != row.NumGC || result.HeapAllocatedBytes != row.Alloc || result.HeapTotalAllocatedBytes != row.TotalAlloc || result.RuntimeSysBytes != row.Sys || result.Allocations != row.Mallocs || result.Frees != row.Frees || result.LiveObjects != row.LiveObjects || result.GCPauseTotalNS != row.PauseTotalNs || result.APIUptimeSeconds != row.Uptime {
		t.Fatalf("lossy native observation: %+v %v calls=%d", result, err, connection.calls)
	}
	for _, test := range []struct{ cause, want error }{{status.Error(codes.Unimplemented, "private native details"), domain.ErrUnsupported}, {status.Error(codes.ResourceExhausted, "private native details"), domain.ErrCapacity}, {rpc.ErrClosed, domain.ErrClosed}, {context.Canceled, context.Canceled}} {
		connection.err = test.cause
		result, err := reader.Read(context.Background())
		if !errors.Is(err, test.want) || !errors.Is(err, test.cause) || result != (domain.Statistics{}) {
			t.Fatalf("native failure lost: %+v %v", result, err)
		}
	}
}
