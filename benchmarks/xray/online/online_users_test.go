package online

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"
	"github.com/PapilioVPN/node/benchmarks/xray/internal/statsfixture"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestMeasureOnlineUsers(t *testing.T) {
	executable := os.Getenv("XRAY_BENCH_ONLINE_USERS")
	if executable == "" {
		t.Skip("set XRAY_BENCH_ONLINE_USERS to the Xray v26.11.9 binary")
	}
	const total = 1000
	const rounds = 5
	const window = 350 * time.Millisecond
	owner, httpServer, grpcConn, proxy := statsfixture.Start(t, executable, total, false, map[string]bool{"statsUserOnline": true})

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	_, err := owner.Start(ctx, "primary")
	statsfixture.Require(t, err)
	conn, err := owner.NativeConnection("primary")
	statsfixture.Require(t, err)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("traffic fixture"))
	}))
	defer target.Close()

	httpClient := httpServer.Client()
	transport := httpClient.Transport.(*http.Transport)
	transport.MaxIdleConnsPerHost = 8
	grpcClient := statsv1.NewXrayStatsServiceClient(grpcConn)
	request := &statsv1.XrayOnlineUsersRequest{Id: "primary"}
	nativeRequest := &emptypb.Empty{}
	reads := []struct {
		name string
		call func(context.Context) ([]string, error)
	}{
		{"native", func(ctx context.Context) ([]string, error) {
			var reply native.OnlineUsersResponse
			err := conn.Invoke(ctx, "/xray.app.stats.command.StatsService/GetAllOnlineUsers", nativeRequest, &reply,
				grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1<<20))
			return reply.GetUsers(), err
		}},
		{"http", func(ctx context.Context) ([]string, error) {
			body, err := fixture.HTTPRead(ctx, httpClient, httpServer.URL+"/v1/xray/instances/primary/stats/online/users", nil)
			if err != nil {
				return nil, err
			}
			var reply statsv1.XrayOnlineUsers
			err = json.Unmarshal(body, &reply)
			return reply.GetNames(), err
		}},
		{"grpc", func(ctx context.Context) ([]string, error) {
			reply, err := grpcClient.ListOnlineUsers(metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key), request)
			return reply.GetNames(), fixture.CallError(err)
		}},
	}

	out := csv.NewWriter(os.Stdout)
	statsfixture.Require(t, out.Write([]string{"transport", "round", "accounts", "clients", "success", "rejected", "goodput_per_sec", "p50_us", "p95_us", "p99_us"}))
	active := 0
	for _, count := range []int{0, 100, total} {
		for ; active < count; active++ {
			tunnel, err := OpenTunnel(ctx, proxy, strings.TrimPrefix(target.URL, "http://"), statsfixture.Name(active))
			statsfixture.Require(t, err)
			defer tunnel.Close()
		}

		for _, read := range reads {
			names, err := read.call(ctx)
			statsfixture.Require(t, err)
			slices.Sort(names)
			if len(names) != count {
				t.Fatalf("%s reported %d accounts, want %d", read.name, len(names), count)
			}
			for i, got := range names {
				want := statsfixture.Name(i)
				if read.name == "native" {
					want = "user>>>" + want + ">>>online"
				}
				if got != want {
					t.Fatalf("%s account %d is %q, want %q", read.name, i, got, want)
				}
			}
		}

		for round := range rounds {
			for _, clients := range []int{1, 8} {
				for step := range reads {
					read := reads[(round+step)%len(reads)]
					result, err := fixture.Poll(ctx, func(ctx context.Context) (uint32, error) {
						names, err := read.call(ctx)
						if err == nil && len(names) != count {
							err = fmt.Errorf("received %d accounts, want %d", len(names), count)
						}
						return uint32(len(names)), err
					}, clients, window)
					statsfixture.Require(t, err)
					statsfixture.Require(t, out.Write([]string{
						read.name, strconv.Itoa(round + 1), strconv.Itoa(count), strconv.Itoa(clients),
						strconv.Itoa(len(result.Timings)), strconv.Itoa(result.Rejected),
						fmt.Sprintf("%.2f", float64(len(result.Timings))/result.Elapsed.Seconds()),
						fixture.Percentile(result.Timings, 50), fixture.Percentile(result.Timings, 95), fixture.Percentile(result.Timings, 99),
					}))
				}
			}
			out.Flush()
			statsfixture.Require(t, out.Error())
		}
	}
}
