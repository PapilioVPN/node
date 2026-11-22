package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type bulkConnection struct {
	calls   []string
	names   *stats.GetAllOnlineUsersResponse
	details *stats.GetUsersStatsResponse
}

func (c *bulkConnection) Invoke(_ context.Context, method string, input, output any, _ ...grpc.CallOption) error {
	c.calls = append(c.calls, method)
	switch method {
	case stats.StatsService_GetAllOnlineUsers_FullMethodName:
		proto.Merge(output.(proto.Message), c.names)
	case stats.StatsService_GetUsersStats_FullMethodName:
		r := input.(*stats.GetUsersStatsRequest)
		if r.IncludeTraffic || r.Reset_ {
			panic("presence must not read or reset traffic")
		}
		proto.Merge(output.(proto.Message), c.details)
	default:
		panic("unexpected per-user RPC")
	}
	return nil
}
func (*bulkConnection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}
func TestBulkCallGrowthAndExactProjection(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			c := &bulkConnection{names: &stats.GetAllOnlineUsersResponse{}, details: &stats.GetUsersStatsResponse{}}
			for i := n - 1; i >= 0; i-- {
				email := fmt.Sprintf("email-%04d", i)
				c.names.Users = append(c.names.Users, "user>>>"+email+">>>online")
				c.details.Users = append(c.details.Users, &stats.UserStat{Email: email, Ips: []*stats.OnlineIPEntry{{Ip: "native", LastSeen: math.MinInt64}}})
			}
			names, err := New(c).Names(context.Background())
			if err != nil || len(names) != n || len(c.calls) != 1 {
				t.Fatal(names, err, c.calls)
			}
			c.calls = nil
			rows, err := New(c).Records(context.Background())
			if err != nil || len(rows) != n || len(c.calls) != 2 {
				t.Fatal(rows, err, c.calls)
			}
			for i, row := range rows {
				if row.Email != fmt.Sprintf("email-%04d", i) || row.IP.LastSeenUnixSeconds != math.MinInt64 {
					t.Fatal(row)
				}
			}
			t.Logf("records=%d names_proto_bytes=%d details_proto_bytes=%d native_calls=2", n, proto.Size(c.names), proto.Size(c.details))
		})
	}
}
func TestNativeBulkLossyIdentityIsExplicit(t *testing.T) {
	c := &bulkConnection{names: &stats.GetAllOnlineUsersResponse{Users: []string{"user>>> A>>>b >>>online"}}}
	rows, err := New(c).Names(context.Background())
	if err != nil || rows[0].Email != " A>>>b " {
		t.Fatal(rows, err)
	}
	c.calls = nil
	if _, err := New(c).Records(context.Background()); !errors.Is(err, domain.ErrIdentityUnsupported) || len(c.calls) != 1 {
		t.Fatal(err, c.calls)
	}
}
func TestBulkRejectsMalformedAndExcessWithoutPartialData(t *testing.T) {
	for _, names := range [][]string{{"wrong"}, {"user>>>online"}, {"user>>>>>>online"}, {"user>>>\xff>>>online"}, make([]string, domain.MaxRecords+1)} {
		c := &bulkConnection{names: &stats.GetAllOnlineUsersResponse{Users: names}}
		if rows, err := New(c).Names(context.Background()); err == nil || len(rows) != 0 {
			t.Fatal(rows, err)
		}
	}
	for _, user := range []*stats.UserStat{nil, {Email: "other"}, {Email: "a", Ips: []*stats.OnlineIPEntry{nil}}, {Email: "a", Ips: []*stats.OnlineIPEntry{{Ip: ""}}}, {Email: "a", Traffic: &stats.TrafficUserStat{}}, {Email: "a", Ips: []*stats.OnlineIPEntry{{Ip: "x"}, {Ip: "x"}}}} {
		c := &bulkConnection{names: &stats.GetAllOnlineUsersResponse{Users: []string{"user>>>a>>>online"}}, details: &stats.GetUsersStatsResponse{Users: []*stats.UserStat{user}}}
		if rows, err := New(c).Records(context.Background()); err == nil || len(rows) != 0 {
			t.Fatal(rows, err)
		}
	}
}
