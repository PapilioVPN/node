package main

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"time"

	outboundsv1 "github.com/PapilioVPN/node/internal/engines/xray/outbounds/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const (
	exactOutbound  = "sample-00"
	failedOutbound = "sample-15"
	observedCount  = 16
	failedDelay    = 99999999
)

type httpOutbound struct {
	Tag                 string `json:"tag"`
	Status              string `json:"status"`
	DelayMilliseconds   *int64 `json:"delay_milliseconds"`
	LastSeenUnixSeconds *int64 `json:"last_seen_unix_seconds"`
	LastTryUnixSeconds  *int64 `json:"last_try_unix_seconds"`
}

func outboundHealthHTTP(client *http.Client, node, reference string) (func(string) trafficCall, func(string) trafficCall) {
	return func(tag string) trafficCall {
			endpoint := "https://" + node + "/v1/xray/instances/primary/outbounds/health"
			if tag != "" {
				endpoint += "?tag=" + url.QueryEscape(tag)
			}
			return func(ctx context.Context) (uint32, error) {
				data, err := trafficHTTPRead(ctx, client, endpoint, nil)
				if err != nil {
					return 0, err
				}
				var result struct {
					Observations []httpOutbound `json:"observations"`
				}
				if err := json.Unmarshal(data, &result); err != nil {
					return 0, err
				}
				return validateOutbounds(tag, len(result.Observations), func(i int) (string, bool, int64, int64, int64) {
					row := result.Observations[i]
					return row.Tag, row.Status == "healthy", value(row.DelayMilliseconds, -1),
						value(row.LastSeenUnixSeconds, 0), value(row.LastTryUnixSeconds, 0)
				})
			}
		}, func(tag string) trafficCall {
			endpoint := "https://" + reference + "/stats/latency"
			payload, err := proto.Marshal(&ReferenceLatencyRequest{Name: tag})
			check(err)
			return func(ctx context.Context) (uint32, error) {
				data, err := trafficHTTPRead(ctx, client, endpoint, payload)
				if err != nil {
					return 0, err
				}
				var result ReferenceLatencyResponse
				if err := proto.Unmarshal(data, &result); err != nil {
					return 0, err
				}
				return validateReferenceOutbounds(tag, &result)
			}
		}
}

func outboundHealthGRPC(node, reference *grpc.ClientConn) (func(string) trafficCall, func(string) trafficCall) {
	client := outboundsv1.NewXrayOutboundsServiceClient(node)
	return func(tag string) trafficCall {
			request := &outboundsv1.XrayOutboundHealthRequest{Id: "primary"}
			if tag != "" {
				request.Tag = &tag
			}
			return func(ctx context.Context) (uint32, error) {
				response, err := client.GetOutboundHealth(metadata.AppendToOutgoingContext(ctx, "x-api-key", key), request)
				if err != nil {
					return 0, trafficCallError(err)
				}
				return validateOutbounds(tag, len(response.Observations), func(i int) (string, bool, int64, int64, int64) {
					row := response.Observations[i]
					return row.Tag, row.Status == outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_HEALTHY, value(row.DelayMilliseconds, -1),
						row.GetLastSeenUnixSeconds(), row.GetLastTryUnixSeconds()
				})
			}
		}, func(tag string) trafficCall {
			request := &ReferenceLatencyRequest{Name: tag}
			return func(ctx context.Context) (uint32, error) {
				var result ReferenceLatencyResponse
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
				if err := reference.Invoke(ctx, "/service.NodeService/GetOutboundsLatency", request, &result, grpc.StaticMethod()); err != nil {
					return 0, trafficCallError(err)
				}
				return validateReferenceOutbounds(tag, &result)
			}
		}
}

func value(value *int64, absent int64) int64 {
	if value == nil {
		return absent
	}
	return *value
}

func validateReferenceOutbounds(tag string, result *ReferenceLatencyResponse) (uint32, error) {
	return validateOutbounds(tag, len(result.Latencies), func(i int) (string, bool, int64, int64, int64) {
		row := result.Latencies[i]
		return row.Name, row.Alive, row.Delay, row.LastSeenTime, row.LastTryTime
	})
}

func validateOutbounds(tag string, count int, entry func(int) (string, bool, int64, int64, int64)) (uint32, error) {
	want := observedCount
	if tag != "" {
		want = 1
	}
	if count != want {
		return 0, fmt.Errorf("got %d outbound observations, want %d", count, want)
	}
	var seen [observedCount]bool
	for i := range count {
		name, alive, delay, lastSeen, lastTry := entry(i)
		if len(name) != len(exactOutbound) || name[:7] != "sample-" || (tag != "" && name != tag) {
			return 0, fmt.Errorf("unexpected outbound %q", name)
		}
		index, err := strconv.Atoi(name[7:])
		if err != nil || index < 0 || index >= observedCount || seen[index] {
			return 0, fmt.Errorf("invalid or duplicate outbound %q", name)
		}
		seen[index] = true
		if lastTry <= 0 || alive != (name != failedOutbound) ||
			(alive && (delay < 0 || delay == failedDelay || lastSeen != lastTry)) ||
			(!alive && (delay != failedDelay || lastSeen != 0)) {
			return 0, fmt.Errorf("invalid sample for %q: alive=%t delay=%d seen=%d tried=%d", name, alive, delay, lastSeen, lastTry)
		}
	}
	return uint32(count), nil
}

func benchmarkOutboundHealth(protocol string, targets []target, rounds int, counts []int, window time.Duration, selected, samplesPath string) {
	if selected != "" && selected != "one" && selected != "all" {
		log.Fatal("outbound health case must be one or all")
	}
	good := serveProbe("127.0.0.1:24442", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer good.Close()
	bad := serveProbe("127.0.0.1:24443", func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			conn.Close()
		}
	})
	defer bad.Close()

	output := csv.NewWriter(os.Stdout)
	check(output.Write([]string{"transport", "round", "node", "selection", "clients", "success", "rejected", "seconds", "goodput_per_sec", "p50_us", "p95_us"}))
	var raw *csv.Writer
	if samplesPath != "" {
		file, err := os.Create(samplesPath)
		check(err)
		defer func() { check(file.Close()) }()
		compressed := gzip.NewWriter(file)
		defer func() { check(compressed.Close()) }()
		raw = csv.NewWriter(compressed)
		check(raw.Write([]string{"transport", "round", "node", "selection", "clients", "duration_ns"}))
	}

	for round := range rounds {
		order := []int{0, 1}
		clientCounts := slices.Clone(counts)
		selections := []string{"one", "all"}
		if round%2 == 1 {
			slices.Reverse(order)
			slices.Reverse(clientCounts)
			slices.Reverse(selections)
		}
		for _, index := range order {
			t := targets[index]
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second+2*time.Duration(len(counts))*window)
			check(t.call(ctx, start))
			check(waitForOutbounds(ctx, t.health("")))
			for _, selection := range selections {
				if selected != "" && selection != selected {
					continue
				}
				tag := ""
				if selection == "one" {
					tag = exactOutbound
				}
				call := t.health(tag)
				for _, clients := range clientCounts {
					result, err := pollRuntimeStats(ctx, call, clients, window)
					check(err)
					timings := result.timings
					fields := []string{protocol, strconv.Itoa(round + 1), t.name, selection, strconv.Itoa(clients)}
					if raw != nil {
						for _, elapsed := range timings {
							check(raw.Write(append(fields, strconv.FormatInt(elapsed.Nanoseconds(), 10))))
						}
					}
					check(output.Write(append(fields, strconv.Itoa(len(timings)), strconv.Itoa(result.rejected),
						strconv.FormatFloat(result.elapsed.Seconds(), 'f', 6, 64),
						strconv.FormatFloat(float64(len(timings))/result.elapsed.Seconds(), 'f', 2, 64),
						percentile(timings, 50), percentile(timings, 95))))
				}
			}
			check(t.call(ctx, stop))
			cancel()
			output.Flush()
			check(output.Error())
			if raw != nil {
				raw.Flush()
				check(raw.Error())
			}
		}
	}
}

func waitForOutbounds(ctx context.Context, call trafficCall) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := call(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		}
	}
}

func serveProbe(address string, handler http.HandlerFunc) *http.Server {
	listener, err := net.Listen("tcp4", address)
	check(err)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() {
		if err := server.Serve(listener); err != http.ErrServerClosed {
			check(err)
		}
	}()
	return server
}
