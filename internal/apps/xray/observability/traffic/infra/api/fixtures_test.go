package api

import (
	"context"
	"testing"

	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

// connectionFixture controls SDK responses; it is not a live engine.
type connectionFixture struct {
	t         *testing.T
	name      string
	value     int64
	err       error
	malformed string
	calls     int
}

func (c *connectionFixture) Invoke(_ context.Context, method string, input, output any, _ ...grpc.CallOption) error {
	c.t.Helper()
	c.calls++
	if method != "/xray.app.stats.command.StatsService/GetStats" {
		c.t.Fatalf("unexpected native operation %s", method)
	}
	request := input.(*stats.GetStatsRequest)
	if request.Name != c.name || request.Reset_ {
		c.t.Fatalf("non-exact or destructive native request %+v", request)
	}
	if c.err != nil {
		return c.err
	}
	if c.malformed == "missing" {
		return nil
	}
	name := c.name
	if c.malformed == "mismatch" {
		name += "wrong"
	}
	output.(*stats.GetStatsResponse).Stat = &stats.Stat{Name: name, Value: c.value}
	return nil
}
func (c *connectionFixture) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("traffic exact reads must be unary")
}
