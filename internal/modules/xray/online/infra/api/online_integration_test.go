//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userdomain "github.com/PapilioVPN/node/internal/modules/xray/online/domain"
	trafficdomain "github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

// Controlled native servers verify SDK behavior; real flows run in bootstrap.
type onlineServer struct {
	native.UnimplementedStatsServiceServer
	count func(context.Context, *native.GetStatsRequest) (*native.GetStatsResponse, error)
	ips   func(context.Context, *native.GetStatsRequest) (*native.GetStatsOnlineIpListResponse, error)
	users func(context.Context, *native.GetAllOnlineUsersRequest) (*native.GetAllOnlineUsersResponse, error)
}

func (s onlineServer) GetStatsOnline(ctx context.Context, r *native.GetStatsRequest) (*native.GetStatsResponse, error) {
	return s.count(ctx, r)
}
func (s onlineServer) GetStatsOnlineIpList(ctx context.Context, r *native.GetStatsRequest) (*native.GetStatsOnlineIpListResponse, error) {
	return s.ips(ctx, r)
}
func (s onlineServer) GetAllOnlineUsers(ctx context.Context, r *native.GetAllOnlineUsersRequest) (*native.GetAllOnlineUsersResponse, error) {
	return s.users(ctx, r)
}
func (onlineServer) GetSysStats(context.Context, *native.SysStatsRequest) (*native.SysStatsResponse, error) {
	return &native.SysStatsResponse{}, nil
}

func TestOnlineNativeReadsPreserveExactIdentitiesAndPrecision(t *testing.T) {
	const name = "user>>> Mixed>>>@Example >>>online"
	var counts, ips, users atomic.Int32
	address, connections := testutil.XrayStatsAPI(t, onlineServer{
		count: func(_ context.Context, r *native.GetStatsRequest) (*native.GetStatsResponse, error) {
			counts.Add(1)
			if r.Name != name || r.Reset_ {
				return nil, status.Error(codes.InvalidArgument, "wrong identity/reset")
			}
			return &native.GetStatsResponse{Stat: &native.Stat{Name: name, Value: math.MaxInt64}}, nil
		},
		ips: func(_ context.Context, r *native.GetStatsRequest) (*native.GetStatsOnlineIpListResponse, error) {
			ips.Add(1)
			if r.Name != name || r.Reset_ {
				return nil, status.Error(codes.InvalidArgument, "wrong identity/reset")
			}
			return &native.GetStatsOnlineIpListResponse{Name: name, Ips: map[string]int64{"2001:db8::1": math.MaxInt64, "192.0.2.1": math.MinInt64}}, nil
		},
		users: func(context.Context, *native.GetAllOnlineUsersRequest) (*native.GetAllOnlineUsersResponse, error) {
			users.Add(1)
			return &native.GetAllOnlineUsersResponse{Users: []string{name, "user>>>   >>>online", name}}, nil
		},
	})
	client := apiClient(t, address, time.Second, 4096)
	before := time.Now()
	count, err := client.ReadUserOnlineStats(t.Context(), " Mixed>>>@Example ")
	if err != nil || count.OnlineIPCount != math.MaxInt64 || count.ObservedAt.Before(before) || count.ObservedAt.After(time.Now()) {
		t.Fatalf("count: %+v, %v", count, err)
	}
	addresses, err := client.ReadUserOnlineIPs(t.Context(), " Mixed>>>@Example ")
	got := map[string]int64{}
	for _, item := range addresses.Items {
		got[item.IP] = item.LastSeenUnixSeconds
	}
	if err != nil || !reflect.DeepEqual(got, map[string]int64{"2001:db8::1": math.MaxInt64, "192.0.2.1": math.MinInt64}) || addresses.ObservedAt.Before(before) {
		t.Fatalf("IPs: %+v, %v", addresses, err)
	}
	online, err := client.ReadOnlineUsers(t.Context())
	if err != nil || !reflect.DeepEqual(online.Emails, []userdomain.UserEmail{" Mixed>>>@Example ", "   ", " Mixed>>>@Example "}) || online.ObservedAt.Before(before) {
		t.Fatalf("users: %+v, %v", online, err)
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	if counts.Load() != 1 || ips.Load() != 1 || users.Load() != 1 || connections.Opened.Load() != 1 {
		t.Fatalf("native calls count=%d ips=%d users=%d channels=%d", counts.Load(), ips.Load(), users.Load(), connections.Opened.Load())
	}
}

func TestOnlineNativeCollectionsHaveOneCallAtEverySize(t *testing.T) {
	for _, size := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var ipCalls, userCalls atomic.Int32
			addresses := make(map[string]int64, size)
			names := make([]string, size)
			for i := range size {
				addresses[fmt.Sprintf("2001:db8::%x", i)] = int64(i)
				names[i] = fmt.Sprintf("user>>>reader-%04d>>>online", i)
			}
			address, _ := testutil.XrayStatsAPI(t, onlineServer{
				ips: func(context.Context, *native.GetStatsRequest) (*native.GetStatsOnlineIpListResponse, error) {
					ipCalls.Add(1)
					return &native.GetStatsOnlineIpListResponse{Name: "user>>>reader>>>online", Ips: addresses}, nil
				},
				users: func(context.Context, *native.GetAllOnlineUsersRequest) (*native.GetAllOnlineUsersResponse, error) {
					userCalls.Add(1)
					return &native.GetAllOnlineUsersResponse{Users: names}, nil
				},
			})
			client := apiClient(t, address, time.Second, 256*1024)
			ips, err := client.ReadUserOnlineIPs(t.Context(), "reader")
			if err != nil || len(ips.Items) != size || ips.ObservedAt.IsZero() {
				t.Fatalf("IPs size %d: %+v, %v", size, ips, err)
			}
			users, err := client.ReadOnlineUsers(t.Context())
			if err != nil || len(users.Emails) != size || users.ObservedAt.IsZero() {
				t.Fatalf("users size %d: %+v, %v", size, users, err)
			}
			for _, item := range ips.Items {
				if addresses[item.IP] != item.LastSeenUnixSeconds {
					t.Fatal("IP timestamp changed")
				}
			}
			for i, email := range users.Emails {
				if string(email) != fmt.Sprintf("reader-%04d", i) {
					t.Fatal("email changed")
				}
			}
			if ipCalls.Load() != 1 || userCalls.Load() != 1 {
				t.Fatalf("collection call growth: ips=%d users=%d", ipCalls.Load(), userCalls.Load())
			}
		})
	}
}

type onlineRead struct {
	name string
	call func(context.Context, *apiClientSet) error
}

func onlineReads(t *testing.T) []onlineRead {
	t.Helper()
	return []onlineRead{
		{"count", func(ctx context.Context, c *apiClientSet) error {
			v, err := c.ReadUserOnlineStats(ctx, "reader")
			if err != nil && v != (userdomain.UserOnline{}) {
				t.Errorf("partial count: %+v", v)
			}
			return err
		}},
		{"ips", func(ctx context.Context, c *apiClientSet) error {
			v, err := c.ReadUserOnlineIPs(ctx, "reader")
			if err != nil && (v.Items != nil || !v.ObservedAt.IsZero()) {
				t.Errorf("partial IPs: %+v", v)
			}
			return err
		}},
		{"users", func(ctx context.Context, c *apiClientSet) error {
			v, err := c.ReadOnlineUsers(ctx)
			if err != nil && (v.Emails != nil || !v.ObservedAt.IsZero()) {
				t.Errorf("partial users: %+v", v)
			}
			return err
		}},
	}
}

func TestOnlineNativeErrorsMalformedResponsesAndReceiveLimits(t *testing.T) {
	for _, test := range []struct {
		name        string
		nativeError error
		limit       int
		cause       error
	}{
		{"missing", status.Error(codes.NotFound, "private identity"), 4096, userdomain.ErrOnlineObservationNotFound},
		{"unsupported", status.Error(codes.Unimplemented, "private method"), 4096, userdomain.ErrOnlineUnsupported},
		{"native-limit", status.Error(codes.ResourceExhausted, "private capacity"), 4096, engineapi.ErrResourceLimit},
		{"native-deadline", status.Error(codes.DeadlineExceeded, "private endpoint"), 4096, context.DeadlineExceeded},
		{"receive-limit", nil, 1, engineapi.ErrResourceLimit},
		{"malformed", nil, 4096, userdomain.ErrInvalidOnlineResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, _ := testutil.XrayStatsAPI(t, onlineServer{
				count: func(context.Context, *native.GetStatsRequest) (*native.GetStatsResponse, error) {
					return &native.GetStatsResponse{Stat: &native.Stat{Name: "wrong", Value: 7}}, test.nativeError
				},
				ips: func(context.Context, *native.GetStatsRequest) (*native.GetStatsOnlineIpListResponse, error) {
					return &native.GetStatsOnlineIpListResponse{Name: "wrong", Ips: map[string]int64{"private": 7}}, test.nativeError
				},
				users: func(context.Context, *native.GetAllOnlineUsersRequest) (*native.GetAllOnlineUsersResponse, error) {
					return &native.GetAllOnlineUsersResponse{Users: []string{"user>>>valid>>>online", "wrong>>>reader>>>online"}}, test.nativeError
				},
			})
			client := apiClient(t, address, time.Second, test.limit)
			for _, op := range onlineReads(t) {
				if err := op.call(t.Context(), client); !errors.Is(err, test.cause) {
					t.Fatalf("%s failure: %v", op.name, err)
				}
			}
		})
	}
	for _, test := range []struct {
		name  string
		stat  *native.Stat
		cause error
	}{
		{"empty-stat", nil, userdomain.ErrInvalidOnlineResponse},
		{"negative-count", &native.Stat{Name: "user>>>reader>>>online", Value: -1}, userdomain.ErrInvalidOnlineResponse},
		{"registered-zero", &native.Stat{Name: "user>>>reader>>>online"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, _ := testutil.XrayStatsAPI(t, onlineServer{count: func(context.Context, *native.GetStatsRequest) (*native.GetStatsResponse, error) {
				return &native.GetStatsResponse{Stat: test.stat}, nil
			}})
			result, err := apiClient(t, address, time.Second, 4096).ReadUserOnlineStats(t.Context(), "reader")
			if !errors.Is(err, test.cause) || err == nil && (result.OnlineIPCount != 0 || result.ObservedAt.IsZero()) {
				t.Fatalf("zero/invalid distinction: %+v, %v", result, err)
			}
		})
	}
}

func TestOnlineNativeSharedAdmissionCancellationDeadlineAndClose(t *testing.T) {
	for _, op := range onlineReads(t) {
		for _, mode := range []string{"cancel", "deadline", "close"} {
			t.Run(op.name+"/"+mode, func(t *testing.T) {
				started := make(chan struct{}, 1)
				wait := func(ctx context.Context) error {
					started <- struct{}{}
					<-ctx.Done()
					return status.FromContextError(ctx.Err()).Err()
				}
				address, _ := testutil.XrayStatsAPI(t, onlineServer{
					count: func(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsResponse, error) {
						return nil, wait(ctx)
					},
					ips: func(ctx context.Context, _ *native.GetStatsRequest) (*native.GetStatsOnlineIpListResponse, error) {
						return nil, wait(ctx)
					},
					users: func(ctx context.Context, _ *native.GetAllOnlineUsersRequest) (*native.GetAllOnlineUsersResponse, error) {
						return nil, wait(ctx)
					},
				})
				timeout := 2 * time.Second
				if mode == "deadline" {
					timeout = 200 * time.Millisecond
				}
				client := apiClient(t, address, timeout, 4096)
				ctx, cancel := context.WithCancel(t.Context())
				finished := make(chan struct{})
				var readError error
				go func() { defer close(finished); readError = op.call(ctx, client) }()
				defer func() { cancel(); _ = client.Close(); <-finished }()
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("online native call did not start")
				}
				if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
					t.Fatalf("runtime bypassed online admission: %v", err)
				}
				if _, err := client.ReadTrafficCounters(t.Context(), trafficdomain.UserTraffic); !errors.Is(err, engineapi.ErrBusy) {
					t.Fatalf("traffic bypassed online admission: %v", err)
				}
				for _, other := range onlineReads(t) {
					if err := other.call(t.Context(), client); !errors.Is(err, engineapi.ErrBusy) {
						t.Fatalf("%s bypassed admission: %v", other.name, err)
					}
				}
				switch mode {
				case "cancel":
					cancel()
				case "close":
					if err := client.Close(); err != nil {
						t.Fatal(err)
					}
				}
				<-finished
				if readError == nil || mode == "cancel" && !errors.Is(readError, context.Canceled) || mode == "deadline" && !errors.Is(readError, context.DeadlineExceeded) {
					t.Fatalf("lost interruption: %v", readError)
				}
				cancel()
				if err := op.call(ctx, client); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled admission: %v", err)
				}
				if mode != "close" {
					if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
						t.Fatalf("admission leaked: %v", err)
					}
				}
			})
		}
	}
}
