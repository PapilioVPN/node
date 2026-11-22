package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExactCountPreservesIdentityAbsenceAndWidth(t *testing.T) {
	email := " A>>>b@example "
	name := "user>>>" + email + ">>>online"
	c := &connection{reply: &stats.GetStatsResponse{Stat: &stats.Stat{Name: name, Value: math.MaxInt64}}}
	r := New(c)
	result, err := r.Count(context.Background(), email)
	if err != nil || result.Count == nil || *result.Count != math.MaxInt64 || c.calls != 1 || c.name != name || c.method != stats.StatsService_GetStatsOnline_FullMethodName {
		t.Fatalf("%+v %v %+v", result, err, c)
	}
	c.err = status.Error(codes.NotFound, "private identity")
	result, err = r.Count(context.Background(), email)
	if err != nil || result.Count != nil || result.State() != domain.Untracked {
		t.Fatal(result, err)
	}
	c.err = nil
	for _, invalid := range []*stats.GetStatsResponse{{}, {Stat: &stats.Stat{Name: name, Value: -1}}, {Stat: &stats.Stat{Name: "wrong", Value: 0}}} {
		c.reply = invalid
		if _, err := r.Count(context.Background(), email); !errors.Is(err, domain.ErrUnavailable) {
			t.Fatal("malformed native success", err)
		}
	}
}

func TestExactIPsOneCallBoundsAndNativeLastSeen(t *testing.T) {
	for _, size := range []int{0, 1, 50, 51} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ips := map[string]int64{}
			for i := range size {
				ips[fmt.Sprintf("native-%02d", i)] = math.MinInt64 + int64(i)
			}
			c := &connection{reply: &stats.GetStatsOnlineIpListResponse{Name: "user>>>a>>>online", Ips: ips}}
			result, err := New(c).IPs(context.Background(), "a")
			if c.calls != 1 || c.method != stats.StatsService_GetStatsOnlineIpList_FullMethodName {
				t.Fatal(c)
			}
			if size > domain.MaxIPs {
				if !errors.Is(err, domain.ErrCapacity) || len(result.IPs) != 0 {
					t.Fatal(result, err)
				}
				return
			}
			if err != nil || !result.Tracked || len(result.IPs) != size {
				t.Fatal(result, err)
			}
			for i, row := range result.IPs {
				if row.Address != fmt.Sprintf("native-%02d", i) || row.LastSeenUnixSeconds != math.MinInt64+int64(i) {
					t.Fatal(row)
				}
			}
		})
	}
	c := &connection{reply: &stats.GetStatsOnlineIpListResponse{Name: "wrong"}}
	if _, err := New(c).IPs(context.Background(), "a"); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal(err)
	}
	c.err = status.Error(codes.NotFound, "private")
	result, err := New(c).IPs(context.Background(), "a")
	if err != nil || result.Tracked || result.State() != domain.Untracked {
		t.Fatal(result, err)
	}
}

func TestNativeFailuresAndCanceledInput(t *testing.T) {
	for _, test := range []struct{ cause, want error }{
		{status.Error(codes.Unimplemented, "private"), domain.ErrUnsupported},
		{status.Error(codes.Unavailable, "private"), domain.ErrUnavailable},
		{status.Error(codes.ResourceExhausted, "private"), domain.ErrCapacity},
		{status.Error(codes.Canceled, "private"), context.Canceled},
		{status.Error(codes.DeadlineExceeded, "private"), context.DeadlineExceeded},
	} {
		c := &connection{err: test.cause}
		count, err := New(c).Count(context.Background(), "a")
		if !errors.Is(err, test.want) || !errors.Is(err, test.cause) || count.Count != nil {
			t.Fatal(count, err)
		}
		ips, err := New(c).IPs(context.Background(), "a")
		if !errors.Is(err, test.want) || !reflect.DeepEqual(ips, domain.Visitors{}) {
			t.Fatal(ips, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &connection{}
	if _, err := New(c).Count(ctx, "a"); !errors.Is(err, context.Canceled) || c.calls != 0 {
		t.Fatal(err, c.calls)
	}
}
