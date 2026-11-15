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

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// Controlled native servers expose exact request selection and external-call growth.
type trafficListServer struct {
	native.UnimplementedStatsServiceServer
	read func(context.Context, *native.QueryStatsRequest) (*native.QueryStatsResponse, error)
}

func (s trafficListServer) QueryStats(ctx context.Context, r *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
	return s.read(ctx, r)
}
func (trafficListServer) GetSysStats(context.Context, *native.SysStatsRequest) (*native.SysStatsResponse, error) {
	return &native.SysStatsResponse{}, nil
}

func TestTrafficCollectionUsesOneNativeReadAtEachSizeAndSharesChannel(t *testing.T) {
	for _, size := range []int{1, 100, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var calls atomic.Int32
			stats := make([]*native.Stat, size)
			for i := range stats {
				stats[i] = &native.Stat{Name: fmt.Sprintf("user>>>reader-%04d>>>traffic>>>uplink", i), Value: int64(i)}
			}
			address, connections := apiServer(t, trafficListServer{read: func(_ context.Context, r *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
				calls.Add(1)
				if r.Pattern != "user>>>" || r.Reset_ {
					return nil, status.Error(codes.InvalidArgument, "wrong selection/reset")
				}
				return &native.QueryStatsResponse{Stat: stats}, nil
			}})
			client := apiClient(t, address, time.Second, 256*1024)
			before := time.Now()
			result, err := client.ReadTrafficCounters(t.Context(), domain.UserTraffic)
			if err != nil || len(result.Items) != size || result.ObservedAt.Before(before) || result.ObservedAt.After(time.Now()) {
				t.Fatalf("collection: %+v, %v", result, err)
			}
			for i, item := range result.Items {
				if item.Name != fmt.Sprintf("reader-%04d", i) || item.Direction != domain.Uplink || item.Bytes != int64(i) {
					t.Fatalf("item %d: %+v", i, item)
				}
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || connections.opened.Load() != 1 {
				t.Fatalf("native calls=%d, channels=%d", calls.Load(), connections.opened.Load())
			}
		})
	}
}

func TestTrafficCollectionPreservesOpaqueNamesSignedValuesAndDuplicates(t *testing.T) {
	for _, scope := range []domain.TrafficScope{domain.UserTraffic, domain.InboundTraffic, domain.OutboundTraffic} {
		t.Run(string(scope), func(t *testing.T) {
			prefix := string(scope) + ">>>"
			address, _ := apiServer(t, trafficListServer{read: func(_ context.Context, r *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
				if r.Pattern != prefix || r.Reset_ {
					return nil, status.Error(codes.InvalidArgument, "wrong query")
				}
				return &native.QueryStatsResponse{Stat: []*native.Stat{
					{Name: prefix + " Mixed>>>traffic>>>user>>>@Example >>>traffic>>>uplink", Value: math.MinInt64},
					{Name: prefix + "same>>>traffic>>>downlink", Value: math.MaxInt64},
					{Name: prefix + "same>>>traffic>>>downlink", Value: 0},
					{Name: "other>>>" + prefix + "collision>>>traffic>>>uplink", Value: 99},
					{Name: prefix + "same>>>online", Value: 99},
					{Name: prefix + "same>>>traffic>>>unsupported", Value: 99},
				}}, nil
			}})
			result, err := apiClient(t, address, time.Second, 4096).ReadTrafficCounters(t.Context(), scope)
			want := []domain.TrafficCounterEntry{
				{Name: " Mixed>>>traffic>>>user>>>@Example ", Direction: domain.Uplink, Bytes: math.MinInt64},
				{Name: "same", Direction: domain.Downlink, Bytes: math.MaxInt64},
				{Name: "same", Direction: domain.Downlink, Bytes: 0},
			}
			if err != nil || !reflect.DeepEqual(result.Items, want) {
				t.Fatalf("identity/values: %+v, %v", result, err)
			}
		})
	}
}

func TestTrafficCollectionEmptyFailureAndReceiveBounds(t *testing.T) {
	for _, test := range []struct {
		name        string
		response    *native.QueryStatsResponse
		nativeError error
		limit       int
		cause       error
	}{
		{"empty", &native.QueryStatsResponse{}, nil, 4096, nil},
		{"empty-name", &native.QueryStatsResponse{Stat: []*native.Stat{{Name: "user>>>valid>>>traffic>>>uplink", Value: 1}, {Name: "user>>>>>>traffic>>>uplink", Value: 99}}}, nil, 4096, domain.ErrInvalidTrafficResponse},
		{"unsupported", nil, status.Error(codes.Unimplemented, "private endpoint"), 4096, domain.ErrTrafficUnsupported},
		{"server-limit", nil, status.Error(codes.ResourceExhausted, "private endpoint"), 4096, domain.ErrAPIResourceLimit},
		{"receive-limit", &native.QueryStatsResponse{Stat: []*native.Stat{{Name: "user>>>reader>>>traffic>>>uplink", Value: 99}}}, nil, 1, domain.ErrAPIResourceLimit},
		{"remote-deadline", nil, status.Error(codes.DeadlineExceeded, "private endpoint"), 4096, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, _ := apiServer(t, trafficListServer{read: func(context.Context, *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
				return test.response, test.nativeError
			}})
			result, err := apiClient(t, address, time.Second, test.limit).ReadTrafficCounters(t.Context(), domain.UserTraffic)
			if test.cause == nil {
				if err != nil || len(result.Items) != 0 || result.ObservedAt.IsZero() {
					t.Fatalf("empty scope: %+v, %v", result, err)
				}
				return
			}
			if !errors.Is(err, test.cause) || result.Items != nil || !result.ObservedAt.IsZero() {
				t.Fatalf("failure exposed partial facts: %+v, %v", result, err)
			}
		})
	}
}

func TestTrafficCollectionSharesAdmissionAndJoinsInterruptedReads(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			address, _ := apiServer(t, trafficListServer{read: func(ctx context.Context, _ *native.QueryStatsRequest) (*native.QueryStatsResponse, error) {
				started <- struct{}{}
				<-ctx.Done()
				return nil, status.FromContextError(ctx.Err()).Err()
			}})
			timeout := 2 * time.Second
			if mode == "deadline" {
				timeout = 200 * time.Millisecond
			}
			client := apiClient(t, address, timeout, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			var result domain.TrafficCounters
			var readError error
			go func() { result, readError = client.ReadTrafficCounters(ctx, domain.UserTraffic); close(done) }()
			defer func() { cancel(); _ = client.Close(); <-done }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("native query did not start")
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("runtime bypassed admission: %v", err)
			}
			if _, err := client.ReadTrafficCounter(t.Context(), domain.TrafficSelection{}); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatalf("single bypassed admission: %v", err)
			}
			switch mode {
			case "cancel":
				cancel()
			case "close":
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			<-done
			if result.Items != nil || !result.ObservedAt.IsZero() || readError == nil {
				t.Fatalf("interrupted facts: %+v, %v", result, readError)
			}
			if mode == "cancel" && !errors.Is(readError, context.Canceled) || mode == "deadline" && !errors.Is(readError, context.DeadlineExceeded) {
				t.Fatalf("lost context cause: %v", readError)
			}
			if mode != "close" {
				if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
					t.Fatalf("admission leaked: %v", err)
				}
			}
		})
	}
}
