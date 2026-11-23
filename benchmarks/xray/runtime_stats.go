package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

var errAtCapacity = errors.New("statistics read capacity reached")

func parseRuntimeStatsClients(raw string) ([]int, error) {
	values := strings.Split(raw, ",")
	counts := make([]int, 0, len(values))
	for _, value := range values {
		count, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || count < 1 || count > 256 {
			return nil, fmt.Errorf("runtime statistics client count must be between 1 and 256: %q", value)
		}
		counts = append(counts, count)
	}
	return counts, nil
}

func runtimeStatsHTTP(client *http.Client, address, path string, decode func([]byte, *statsv1.XrayRuntimeStats) error) func(context.Context) (uint32, error) {
	return func(ctx context.Context) (uint32, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+path, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("X-API-Key", key)
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return 0, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return 0, errAtCapacity
		}
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("%s: %s", resp.Status, data)
		}
		var stats statsv1.XrayRuntimeStats
		if err := decode(data, &stats); err != nil {
			return 0, err
		}
		return stats.GetUptimeSeconds(), verifyRuntimeStats(&stats)
	}
}

func decodeNodeStats(data []byte, result *statsv1.XrayRuntimeStats) error {
	return json.Unmarshal(data, result)
}

func decodeReferenceStats(data []byte, result *statsv1.XrayRuntimeStats) error {
	// Both responses carry the ten native fields with the same protobuf tags.
	return proto.Unmarshal(data, result)
}

func runtimeStatsGRPC(node, reference *grpc.ClientConn) (func(context.Context) (uint32, error), func(context.Context) (uint32, error)) {
	client := statsv1.NewXrayStatsServiceClient(node)
	request := &statsv1.XrayRuntimeStatsRequest{Id: "primary"}
	return func(ctx context.Context) (uint32, error) {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
			stats, err := client.GetRuntimeStats(ctx, request)
			if err != nil {
				if grpcstatus.Code(err) == codes.ResourceExhausted {
					return 0, errAtCapacity
				}
				return 0, err
			}
			return stats.GetUptimeSeconds(), verifyRuntimeStats(stats)
		}, func(ctx context.Context) (uint32, error) {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
			var stats statsv1.XrayRuntimeStats
			if err := reference.Invoke(ctx, "/service.NodeService/GetBackendStats", &emptypb.Empty{}, &stats, grpc.StaticMethod()); err != nil {
				if grpcstatus.Code(err) == codes.ResourceExhausted {
					return 0, errAtCapacity
				}
				return 0, err
			}
			return stats.GetUptimeSeconds(), verifyRuntimeStats(&stats)
		}
}

func verifyRuntimeStats(stats *statsv1.XrayRuntimeStats) error {
	if stats.GetGoroutines() == 0 || stats.GetHeapAllocatedBytes() == 0 ||
		stats.GetTotalAllocatedBytes() < stats.GetHeapAllocatedBytes() ||
		stats.GetMallocs() < stats.GetFrees() ||
		stats.GetLiveObjects() != stats.GetMallocs()-stats.GetFrees() {
		return errors.New("invalid Xray runtime statistics")
	}
	return nil
}

func benchmarkRuntimeStats(protocol string, targets []target, rounds int, clientCounts []int, window time.Duration) {
	output := csv.NewWriter(os.Stdout)
	check(output.Write([]string{"transport", "round", "node", "clients", "success", "rejected", "seconds", "goodput_per_sec", "p50_us", "p95_us", "p99_us"}))
	for round := range rounds {
		order := []int{0, 1}
		counts := slices.Clone(clientCounts)
		if round%2 == 1 {
			slices.Reverse(order)
			slices.Reverse(counts)
		}
		for _, index := range order {
			t := targets[index]
			timeout := max(15*time.Second, time.Duration(len(counts))*window+8*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			check(t.call(ctx, start))
			check(waitForRuntimeStatsUptime(ctx, t.stats))
			for _, clients := range counts {
				result, err := pollRuntimeStats(ctx, t.stats, clients, window)
				check(err)
				timings := result.timings
				check(output.Write([]string{protocol, strconv.Itoa(round + 1), t.name, strconv.Itoa(clients),
					strconv.Itoa(len(timings)), strconv.Itoa(result.rejected),
					strconv.FormatFloat(result.elapsed.Seconds(), 'f', 6, 64),
					strconv.FormatFloat(float64(len(timings))/result.elapsed.Seconds(), 'f', 2, 64),
					percentile(timings, 50), percentile(timings, 95), percentile(timings, 99)}))
			}
			check(t.call(ctx, stop))
			cancel()
			if hasChild(t.pid) {
				check(fmt.Errorf("%s stop returned before its child was reaped", t.name))
			}
			output.Flush()
			check(output.Error())
		}
	}
}

func waitForRuntimeStatsUptime(ctx context.Context, call func(context.Context) (uint32, error)) error {
	for {
		uptime, err := call(ctx)
		if err != nil || uptime >= 1 {
			return err
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type runtimeStatsPoll struct {
	timings  []time.Duration
	rejected int
	elapsed  time.Duration
}

func pollRuntimeStats(ctx context.Context, call func(context.Context) (uint32, error), clients int, window time.Duration) (runtimeStatsPoll, error) {
	var ready, done sync.WaitGroup
	ready.Add(clients)
	started := make(chan struct{})
	timings := make([][]time.Duration, clients)
	rejections := make([]int, clients)
	failures := make([]error, clients)
	var deadline time.Time
	for i := range clients {
		done.Go(func() {
			_, err := call(ctx)
			if errors.Is(err, errAtCapacity) {
				err = nil
			}
			ready.Done()
			<-started
			for time.Now().Before(deadline) && err == nil {
				begin := time.Now()
				_, err = call(ctx)
				switch {
				case errors.Is(err, errAtCapacity):
					rejections[i]++
					err = nil
				case err == nil:
					timings[i] = append(timings[i], time.Since(begin))
				}
			}
			failures[i] = err
		})
	}
	ready.Wait()
	begin := time.Now()
	deadline = begin.Add(window)
	close(started)
	done.Wait()
	elapsed := time.Since(begin)

	if err := errors.Join(failures...); err != nil {
		return runtimeStatsPoll{}, err
	}
	all := slices.Concat(timings...)
	if len(all) == 0 {
		return runtimeStatsPoll{}, errors.New("no successful runtime statistics observations")
	}
	slices.Sort(all)
	rejected := 0
	for _, count := range rejections {
		rejected += count
	}
	return runtimeStatsPoll{timings: all, rejected: rejected, elapsed: elapsed}, nil
}
