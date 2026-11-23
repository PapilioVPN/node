package main

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative traffic_reference.proto

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type trafficRead struct {
	scope          string
	nodeScope      statsv1.XrayTrafficScope
	referenceScope int32
	name           string
	count          int
}

type trafficCall func(context.Context) (uint32, error)

func benchmarkTraffic(protocol string, targets []target, rounds int, clients []int, names int, window time.Duration, selection string) {
	cases := []trafficRead{
		{"user", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, 5, "account-0000", 1},
		{"user", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, 4, "", names},
		{"inbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND, 3, "fixture", 1},
		{"inbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND, 2, "", 1},
		{"outbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_OUTBOUND, 1, "direct", 1},
		{"outbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_OUTBOUND, 0, "", 1},
	}
	online := selection == "online" || strings.HasPrefix(selection, "online-")
	if online {
		cases = []trafficRead{
			{scope: "online-count", name: "account-0000"},
			{scope: "online-ips", name: "account-0000"},
		}
	}
	if selection != "" && selection != "online" {
		cases = slices.DeleteFunc(cases, func(item trafficRead) bool { return item.label() != selection })
		if len(cases) == 0 {
			check(fmt.Errorf("unknown traffic case %q", selection))
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "traffic fixture")
	}))
	defer target.Close()

	for index, t := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		check(t.call(ctx, start))
		address := fmt.Sprintf("127.0.0.1:%d", 24440+index)
		if online {
			tunnel, err := openOnlineTunnel(ctx, address, strings.TrimPrefix(target.URL, "http://"), cases[0].name)
			check(err)
			defer tunnel.Close()
		} else {
			check(seedTraffic(ctx, target.URL, address, names))
		}
		cancel()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			check(t.call(ctx, stop))
			if hasChild(t.pid) {
				check(fmt.Errorf("%s stop returned before Xray was reaped", t.name))
			}
		}()
	}

	writer := csv.NewWriter(os.Stdout)
	check(writer.Write([]string{"transport", "round", "node", "accounts", "scope", "selection", "clients", "success", "rejected", "seconds", "goodput_per_sec", "p50_us", "p95_us", "p99_us"}))
	for round := range rounds {
		order := []int{0, 1}
		counts := slices.Clone(clients)
		if round%2 == 1 {
			slices.Reverse(order)
			slices.Reverse(counts)
		}

		for _, spec := range cases {
			selection := "list"
			if spec.name != "" {
				selection = "one"
			}
			for _, count := range counts {
				for _, index := range order {
					t := targets[index]
					ctx, cancel := context.WithTimeout(context.Background(), window+15*time.Second)
					result, err := pollRuntimeStats(ctx, t.traffic(spec), count, window)
					cancel()
					check(fmtError(t.name, spec, err))

					timings := result.timings
					check(writer.Write([]string{
						protocol, strconv.Itoa(round + 1), t.name, strconv.Itoa(names), spec.scope, selection, strconv.Itoa(count),
						strconv.Itoa(len(timings)), strconv.Itoa(result.rejected), strconv.FormatFloat(result.elapsed.Seconds(), 'f', 6, 64),
						fmt.Sprintf("%.2f", float64(len(timings))/result.elapsed.Seconds()),
						percentile(timings, 50), percentile(timings, 95), percentile(timings, 99),
					}))
				}
			}
		}
		writer.Flush()
		check(writer.Error())
	}
}

func (spec trafficRead) label() string {
	if spec.name == "" {
		return spec.scope + "/list"
	}
	return spec.scope + "/one"
}

func fmtError(node string, spec trafficRead, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s %s %q: %w", node, spec.scope, spec.name, err)
}

func seedTraffic(ctx context.Context, target, address string, names int) error {
	for i := range names {
		name := fmt.Sprintf("account-%04d", i)
		proxy := &url.URL{Scheme: "http", Host: address, User: url.UserPassword(name, "fixture")}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		response, err := transport.RoundTrip(request)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK || string(body) != "traffic fixture" {
			return fmt.Errorf("seed traffic for %s: %s %q", name, response.Status, body)
		}
	}
	return nil
}

func trafficHTTP(client *http.Client, node, reference string) (func(trafficRead) trafficCall, func(trafficRead) trafficCall) {
	return func(spec trafficRead) trafficCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return onlineHTTP(client, node, spec, false)
			}
			endpoint := "https://" + node + "/v1/xray/instances/primary/stats/traffic/" + spec.scope
			if spec.name != "" {
				endpoint += "?name=" + url.QueryEscape(spec.name)
			}
			return func(ctx context.Context) (uint32, error) {
				body, err := trafficHTTPRead(ctx, client, endpoint, nil)
				if err != nil {
					return 0, err
				}
				if spec.name != "" {
					var result statsv1.XrayTrafficUsage
					if err := json.Unmarshal(body, &result); err != nil {
						return 0, err
					}
					return validateNodeTraffic(spec, []*statsv1.XrayTrafficUsage{&result})
				}
				var results statsv1.XrayTrafficList
				if err := json.Unmarshal(body, &results); err != nil {
					return 0, err
				}
				return validateNodeTraffic(spec, results.Entries)
			}
		}, func(spec trafficRead) trafficCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return onlineHTTP(client, reference, spec, true)
			}
			input, err := proto.Marshal(&ReferenceTrafficRequest{Name: spec.name, Type: spec.referenceScope})
			check(err)
			return func(ctx context.Context) (uint32, error) {
				body, err := trafficHTTPRead(ctx, client, "https://"+reference+"/stats/", input)
				if err != nil {
					return 0, err
				}
				var result ReferenceTrafficResponse
				if err := proto.Unmarshal(body, &result); err != nil {
					return 0, err
				}
				return validateReferenceTraffic(spec, result.Stats)
			}
		}
}

func trafficHTTPRead(ctx context.Context, client *http.Client, endpoint string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusTooManyRequests:
		return nil, errAtCapacity
	default:
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, body)
	}
}

func trafficGRPC(node, reference *grpc.ClientConn) (func(trafficRead) trafficCall, func(trafficRead) trafficCall) {
	client := statsv1.NewXrayStatsServiceClient(node)
	return func(spec trafficRead) trafficCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return onlineGRPC(node, spec, false)
			}
			single := &statsv1.XrayTrafficRequest{Id: "primary", Scope: spec.nodeScope, Name: spec.name}
			list := &statsv1.XrayTrafficListRequest{Id: "primary", Scope: spec.nodeScope}
			return func(ctx context.Context) (uint32, error) {
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
				if spec.name != "" {
					result, err := client.GetTraffic(ctx, single)
					if err != nil {
						return 0, trafficCallError(err)
					}
					return validateNodeTraffic(spec, []*statsv1.XrayTrafficUsage{result})
				}
				results, err := client.ListTraffic(ctx, list)
				if err != nil {
					return 0, trafficCallError(err)
				}
				return validateNodeTraffic(spec, results.Entries)
			}
		}, func(spec trafficRead) trafficCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return onlineGRPC(reference, spec, true)
			}
			request := &ReferenceTrafficRequest{Name: spec.name, Type: spec.referenceScope}
			return func(ctx context.Context) (uint32, error) {
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
				var result ReferenceTrafficResponse
				err := reference.Invoke(ctx, "/service.NodeService/GetStats", request, &result, grpc.StaticMethod())
				if err != nil {
					return 0, trafficCallError(err)
				}
				return validateReferenceTraffic(spec, result.Stats)
			}
		}
}

func trafficCallError(err error) error {
	if grpcstatus.Code(err) == codes.ResourceExhausted {
		return errAtCapacity
	}
	return err
}

func validateNodeTraffic(spec trafficRead, rows []*statsv1.XrayTrafficUsage) (uint32, error) {
	if len(rows) != spec.count {
		return 0, fmt.Errorf("received %d resources, want %d", len(rows), spec.count)
	}
	for _, row := range rows {
		if !validTrafficName(spec, row.GetName()) || row.UplinkBytes == nil || row.DownlinkBytes == nil ||
			row.GetUplinkBytes() <= 0 || row.GetDownlinkBytes() <= 0 {
			return 0, fmt.Errorf("unexpected traffic row %v", row)
		}
	}
	return uint32(len(rows)), nil
}

func validateReferenceTraffic(spec trafficRead, rows []*ReferenceTrafficStat) (uint32, error) {
	expected := 2 * spec.count
	matched := 0
	for _, row := range rows {
		if spec.name == "" && ((spec.scope == "inbound" && row.Name == "API_INBOUND") ||
			(spec.scope == "outbound" && row.Name == "API")) {
			// PasarGuard registers counters for its own management tunnel.
			continue
		}
		if !validTrafficName(spec, row.Name) || row.Link != "traffic" ||
			(row.Type != "uplink" && row.Type != "downlink") || row.Value <= 0 {
			return 0, fmt.Errorf("unexpected reference traffic counter %v", row)
		}
		matched++
	}
	if matched != expected {
		return 0, fmt.Errorf("received %d fixture counters, want %d", matched, expected)
	}
	return uint32(matched / 2), nil
}

func validTrafficName(spec trafficRead, name string) bool {
	if spec.name != "" {
		return name == spec.name
	}
	switch spec.scope {
	case "user":
		return strings.HasPrefix(name, "account-")
	case "inbound":
		return name == "fixture"
	default:
		return name == "direct"
	}
}
