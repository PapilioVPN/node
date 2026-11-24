package runtime

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
	"time"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func runtimeStatsHTTP(client *http.Client, address, path string, decode func([]byte, *statsv1.XrayRuntimeStats) error) func(context.Context) (uint32, error) {
	return func(ctx context.Context) (uint32, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+path, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("X-API-Key", fixture.Key)
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
			return 0, fixture.ErrAtCapacity
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

func GRPC(node, reference *grpc.ClientConn) (func(context.Context) (uint32, error), func(context.Context) (uint32, error)) {
	client := statsv1.NewXrayStatsServiceClient(node)
	request := &statsv1.XrayRuntimeStatsRequest{Id: "primary"}
	return func(ctx context.Context) (uint32, error) {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
			stats, err := client.GetRuntimeStats(ctx, request)
			if err != nil {
				if grpcstatus.Code(err) == codes.ResourceExhausted {
					return 0, fixture.ErrAtCapacity
				}
				return 0, err
			}
			return stats.GetUptimeSeconds(), verifyRuntimeStats(stats)
		}, func(ctx context.Context) (uint32, error) {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
			var stats statsv1.XrayRuntimeStats
			if err := reference.Invoke(ctx, "/service.NodeService/GetBackendStats", &emptypb.Empty{}, &stats, grpc.StaticMethod()); err != nil {
				if grpcstatus.Code(err) == codes.ResourceExhausted {
					return 0, fixture.ErrAtCapacity
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

func Benchmark(protocol string, targets [2]fixture.Target[fixture.ReadCall], rounds int, clientCounts []int, window time.Duration) {
	output := csv.NewWriter(os.Stdout)
	fixture.Check(output.Write([]string{"transport", "round", "node", "clients", "success", "rejected", "seconds", "goodput_per_sec", "p50_us", "p95_us", "p99_us"}))
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
			fixture.Check(t.Call(ctx, fixture.Start))
			fixture.Check(waitForRuntimeStatsUptime(ctx, t.Calls))
			for _, clients := range counts {
				result, err := fixture.Poll(ctx, t.Calls, clients, window)
				fixture.Check(err)
				timings := result.Timings
				fixture.Check(output.Write([]string{protocol, strconv.Itoa(round + 1), t.Name, strconv.Itoa(clients),
					strconv.Itoa(len(timings)), strconv.Itoa(result.Rejected),
					strconv.FormatFloat(result.Elapsed.Seconds(), 'f', 6, 64),
					strconv.FormatFloat(float64(len(timings))/result.Elapsed.Seconds(), 'f', 2, 64),
					fixture.Percentile(timings, 50), fixture.Percentile(timings, 95), fixture.Percentile(timings, 99)}))
			}
			fixture.Check(t.Call(ctx, fixture.Stop))
			cancel()
			if fixture.HasChild(t.PID) {
				fixture.Check(fmt.Errorf("%s stop returned before its child was reaped", t.Name))
			}
			output.Flush()
			fixture.Check(output.Error())
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

func HTTP(client *http.Client, node, reference string) (fixture.ReadCall, fixture.ReadCall) {
	return runtimeStatsHTTP(client, node, "/v1/xray/instances/primary/stats/runtime", decodeNodeStats),
		runtimeStatsHTTP(client, reference, "/stats/backend", decodeReferenceStats)
}
