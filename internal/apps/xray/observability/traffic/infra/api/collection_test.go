package api

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"testing"
)

type queryConnection struct {
	connectionFixture
	records []*stats.Stat
}

func (c *queryConnection) Invoke(_ context.Context, method string, input, output any, _ ...grpc.CallOption) error {
	c.calls++
	if method != "/xray.app.stats.command.StatsService/QueryStats" {
		c.t.Fatal(method)
	}
	req := input.(*stats.QueryStatsRequest)
	if req.Reset_ || req.Pattern != "user>>>" {
		c.t.Fatal(req)
	}
	output.(*stats.QueryStatsResponse).Stat = c.records
	return nil
}
func TestCollectionFiltersExactScopeAndOpaqueNames(t *testing.T) {
	c := &queryConnection{connectionFixture: connectionFixture{t: t}, records: []*stats.Stat{
		{Name: "inbound>>>user>>>collision>>>traffic>>>uplink", Value: 11},
		{Name: "user>>>A>>>B>>>traffic>>>downlink", Value: 0},
		{Name: "user>>>A>>>B>>>traffic>>>uplink", Value: -1},
		{Name: "user>>>A>>>B>>>online", Value: 9},
	}}
	got, err := New(c).List(context.Background(), domain.User)
	if err != nil || len(got) != 2 || got[0].Selection.Name != "A>>>B" || got[0].Bytes != 0 || got[1].Bytes != -1 || c.calls != 1 {
		t.Fatalf("%+v %v calls=%d", got, err, c.calls)
	}
}
