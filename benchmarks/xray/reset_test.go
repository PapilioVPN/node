package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestMeasureTrafficReset(t *testing.T) {
	executable := os.Getenv("XRAY_BENCH_TRAFFIC_RESET")
	if executable == "" {
		t.Skip("set XRAY_BENCH_TRAFFIC_RESET to the Xray v26.11.9 binary")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("traffic fixture"))
	}))
	defer target.Close()
	out := csv.NewWriter(os.Stdout)
	requireStatsFixture(t, out.Write([]string{"transport", "round", "accounts", "clients", "success", "rejected", "goodput_per_sec", "p50_us", "p95_us", "p99_us"}))
	for _, accounts := range []int{1, 512} {
		t.Run(strconv.Itoa(accounts), func(t *testing.T) {
			measureTrafficReset(t, executable, target.URL, accounts, out)
		})
	}
}

func measureTrafficReset(t *testing.T, executable, target string, accounts int, out *csv.Writer) {
	const id = "primary"
	const name = "account-0000"
	owner, server, grpcConn, proxy := startStatsFixture(t, executable, accounts, true, map[string]bool{"statsUserDownlink": true})
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	_, err := owner.Start(ctx, id)
	requireStatsFixture(t, err)
	nativeConn, err := owner.NativeConnection(id)
	requireStatsFixture(t, err)
	requireStatsFixture(t, seedTraffic(ctx, target, proxy, accounts))
	client := server.Client()
	client.Transport.(*http.Transport).MaxIdleConnsPerHost = 8
	grpcClient := statsv1.NewXrayStatsServiceClient(grpcConn)
	nativeRequest := &native.ResetCounterRequest{Name: "user>>>" + name + ">>>traffic>>>downlink", Reset_: true}
	grpcRequest := &statsv1.XrayTrafficResetRequest{
		Id: id, Scope: statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, Name: name,
		Direction: statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_DOWNLINK,
	}
	reads := []struct {
		name string
		call func(context.Context) (int64, error)
	}{
		{"native", func(ctx context.Context) (int64, error) {
			var reply native.NamedCounterResponse
			err := nativeConn.Invoke(ctx, "/xray.app.stats.command.StatsService/GetStats", nativeRequest, &reply,
				grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(8<<10))
			if err == nil && reply.GetStat().GetName() != nativeRequest.Name {
				return 0, fmt.Errorf("unexpected native counter: %v", reply.GetStat())
			}
			return reply.GetStat().GetValue(), err
		}},
		{"http", func(ctx context.Context) (int64, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				server.URL+"/v1/xray/instances/primary/stats/traffic/user/resets?name="+name+"&direction=downlink", nil)
			if err != nil {
				return 0, err
			}
			req.Header.Set("X-API-Key", key)
			res, err := client.Do(req)
			if err != nil {
				return 0, err
			}
			defer res.Body.Close()
			if res.StatusCode == http.StatusTooManyRequests {
				return 0, errAtCapacity
			}
			if res.StatusCode != http.StatusOK {
				return 0, fmt.Errorf("HTTP reset: %s", res.Status)
			}
			var reply struct {
				PreviousBytes *int64 `json:"previous_bytes"`
			}
			if err := json.NewDecoder(res.Body).Decode(&reply); err != nil {
				return 0, err
			}
			if reply.PreviousBytes == nil {
				return 0, fmt.Errorf("HTTP reset omitted previous bytes")
			}
			return *reply.PreviousBytes, nil
		}},
		{"grpc", func(ctx context.Context) (int64, error) {
			reply, err := grpcClient.ResetTrafficCounter(metadata.AppendToOutgoingContext(ctx, "x-api-key", key), grpcRequest)
			return reply.GetPreviousBytes(), trafficCallError(err)
		}},
	}
	first, err := reads[0].call(ctx)
	requireStatsFixture(t, err)
	if first <= 0 {
		t.Fatalf("expected a nonzero seeded counter, got %d", first)
	}
	for round := range 5 {
		for _, clients := range []int{1, 8} {
			for step := range reads {
				read := reads[(round+step)%len(reads)]
				result, err := pollRuntimeStats(ctx, func(ctx context.Context) (uint32, error) {
					previous, err := read.call(ctx)
					if err == nil && previous != 0 {
						err = fmt.Errorf("reset returned %d for empty counter", previous)
					}
					return 0, err
				}, clients, 300*time.Millisecond)
				requireStatsFixture(t, err)
				requireStatsFixture(t, out.Write([]string{
					read.name, strconv.Itoa(round + 1), strconv.Itoa(accounts), strconv.Itoa(clients),
					strconv.Itoa(len(result.timings)), strconv.Itoa(result.rejected),
					fmt.Sprintf("%.2f", float64(len(result.timings))/result.elapsed.Seconds()),
					percentile(result.timings, 50), percentile(result.timings, 95), percentile(result.timings, 99),
				}))
			}
		}
		out.Flush()
		requireStatsFixture(t, out.Error())
	}
}
