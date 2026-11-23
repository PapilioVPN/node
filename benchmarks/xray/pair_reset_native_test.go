package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	"google.golang.org/grpc"
)

func TestMeasureNativePairReset(t *testing.T) {
	executable := os.Getenv("XRAY_BENCH_NATIVE_PAIR_RESET")
	if executable == "" {
		t.Skip("set XRAY_BENCH_NATIVE_PAIR_RESET to the pinned Xray executable")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "traffic fixture")
	}))
	defer target.Close()
	out := csv.NewWriter(os.Stdout)
	requireStatsFixture(t, out.Write([]string{"accounts", "round", "method", "p50_us", "p95_us"}))
	for _, accounts := range []int{1, 512} {
		t.Run(strconv.Itoa(accounts), func(t *testing.T) {
			measureNativePairReset(t, executable, target.URL, accounts, out)
		})
	}
	out.Flush()
	requireStatsFixture(t, out.Error())
}

func measureNativePairReset(t *testing.T, executable, endpoint string, accounts int, out *csv.Writer) {
	owner, _, _, proxy := startStatsFixture(t, executable, accounts, false, map[string]bool{
		"statsUserUplink": true, "statsUserDownlink": true,
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, err := owner.Start(ctx, "primary")
	requireStatsFixture(t, err)
	conn, err := owner.NativeConnection("primary")
	requireStatsFixture(t, err)
	requireStatsFixture(t, seedTraffic(ctx, endpoint, proxy, accounts))

	const service = "/xray.app.stats.command.StatsService/"
	const key = "user>>>account-0000>>>"
	// QueryStats uses the same field numbers for its pattern and reset flag.
	query := &native.ResetCounterRequest{Name: key, Reset_: true}
	requests := [2]*native.ResetCounterRequest{
		{Name: key + "traffic>>>uplink", Reset_: true},
		{Name: key + "traffic>>>downlink", Reset_: true},
	}
	paths := []struct {
		name string
		call trafficCall
	}{
		{"exact", func(ctx context.Context) (uint32, error) {
			for _, request := range requests {
				var reply native.NamedCounterResponse
				if err := conn.Invoke(ctx, service+"GetStats", request, &reply, grpc.StaticMethod()); err != nil {
					return 0, err
				}
				if reply.GetStat().GetName() != request.Name {
					return 0, fmt.Errorf("unexpected exact counter: %v", reply.GetStat())
				}
			}
			return 2, nil
		}},
		{"substring", func(ctx context.Context) (uint32, error) {
			var reply native.QueryStatsResponse
			if err := conn.Invoke(ctx, service+"QueryStats", query, &reply, grpc.StaticMethod()); err != nil {
				return 0, err
			}
			if len(reply.GetStats()) != 2 {
				return 0, fmt.Errorf("substring query returned %d counters", len(reply.GetStats()))
			}
			return 2, nil
		}},
	}
	for round := range 5 {
		for step := range paths {
			path := paths[(round+step)%len(paths)]
			result, err := pollRuntimeStats(ctx, path.call, 1, 250*time.Millisecond)
			requireStatsFixture(t, err)
			requireStatsFixture(t, out.Write([]string{strconv.Itoa(accounts), strconv.Itoa(round + 1), path.name,
				percentile(result.timings, 50), percentile(result.timings, 95)}))
		}
	}
}
