package traffic

import (
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

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"
	"github.com/PapilioVPN/node/benchmarks/xray/online"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type trafficRead struct {
	scope          string
	nodeScope      statsv1.XrayTrafficScope
	referenceScope int32
	name           string
	count          int
}

type Calls func(trafficRead) fixture.ReadCall

func Benchmark(protocol string, targets [2]fixture.Target[Calls], rounds int, clients []int, names int, window time.Duration, selection string) {
	cases := []trafficRead{
		{"user", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, 5, "account-0000", 1},
		{"user", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, 4, "", names},
		{"inbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND, 3, "fixture", 1},
		{"inbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND, 2, "", 1},
		{"outbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_OUTBOUND, 1, "direct", 1},
		{"outbound", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_OUTBOUND, 0, "", 1},
	}
	scopedReset := selection == "reset-scope/list"
	resetPair := selection == "reset-pair/one"
	reset := scopedReset || resetPair
	presence := selection == "online" || strings.HasPrefix(selection, "online-")
	if presence {
		cases = []trafficRead{
			{scope: "online-count", name: "account-0000"},
			{scope: "online-ips", name: "account-0000"},
		}
	}
	if resetPair {
		cases = []trafficRead{{scope: "reset-pair", name: "account-0000"}}
	}
	if scopedReset {
		if names > 500 {
			fixture.Check(fmt.Errorf("scoped reset benchmarks support at most 500 accounts"))
		}
		cases = []trafficRead{{scope: "reset-scope", count: names}}
	}
	if selection != "" && selection != "online" {
		cases = slices.DeleteFunc(cases, func(item trafficRead) bool { return item.label() != selection })
		if len(cases) == 0 {
			fixture.Check(fmt.Errorf("unknown traffic case %q", selection))
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "traffic fixture")
	}))
	defer target.Close()

	for index, t := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		fixture.Check(t.Call(ctx, fixture.Start))
		address := fmt.Sprintf("127.0.0.1:%d", 24440+index)
		if presence {
			tunnel, err := online.OpenTunnel(ctx, address, strings.TrimPrefix(target.URL, "http://"), cases[0].name)
			fixture.Check(err)
			defer tunnel.Close()
		} else {
			fixture.Check(seedTraffic(ctx, target.URL, address, names))
		}
		if reset {
			seeded, err := t.Calls(cases[0])(ctx)
			fixture.Check(fmtError(t.Name, cases[0], err))
			if seeded != 1 {
				fixture.Check(fmt.Errorf("%s reset did not return the nonzero seeded counters", t.Name))
			}
		}
		cancel()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			fixture.Check(t.Call(ctx, fixture.Stop))
			if fixture.HasChild(t.PID) {
				fixture.Check(fmt.Errorf("%s stop returned before Xray was reaped", t.Name))
			}
		}()
	}

	writer := csv.NewWriter(os.Stdout)
	fixture.Check(writer.Write([]string{"transport", "round", "node", "accounts", "scope", "selection", "clients", "success", "rejected", "seconds", "goodput_per_sec", "p50_us", "p95_us", "p99_us"}))
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
					call := t.Calls(spec)
					if reset {
						resetCall := call
						call = func(ctx context.Context) (uint32, error) {
							seeded, err := resetCall(ctx)
							if err == nil && seeded != 0 {
								err = fmt.Errorf("timed reset found a nonzero counter after seeding")
							}
							return seeded, err
						}
					}
					result, err := fixture.Poll(ctx, call, count, window)
					cancel()
					fixture.Check(fmtError(t.Name, spec, err))

					timings := result.Timings
					fixture.Check(writer.Write([]string{
						protocol, strconv.Itoa(round + 1), t.Name, strconv.Itoa(names), spec.scope, selection, strconv.Itoa(count),
						strconv.Itoa(len(timings)), strconv.Itoa(result.Rejected), strconv.FormatFloat(result.Elapsed.Seconds(), 'f', 6, 64),
						fmt.Sprintf("%.2f", float64(len(timings))/result.Elapsed.Seconds()),
						fixture.Percentile(timings, 50), fixture.Percentile(timings, 95), fixture.Percentile(timings, 99),
					}))
				}
			}
		}
		writer.Flush()
		fixture.Check(writer.Error())
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

func HTTP(client *http.Client, node, reference string) (Calls, Calls) {
	return func(spec trafficRead) fixture.ReadCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return online.HTTP(client, node, spec.scope, spec.name, false)
			}
			if spec.scope == "reset-pair" {
				return nodePairHTTP(client, node, spec.name)
			}
			if spec.scope == "reset-scope" {
				return scopeResetHTTP(client, node, spec.count, false)
			}
			endpoint := "https://" + node + "/v1/xray/instances/primary/stats/traffic/" + spec.scope
			if spec.name != "" {
				endpoint += "?name=" + url.QueryEscape(spec.name)
			}
			return func(ctx context.Context) (uint32, error) {
				body, err := fixture.HTTPRead(ctx, client, endpoint, nil)
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
		}, func(spec trafficRead) fixture.ReadCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return online.HTTP(client, reference, spec.scope, spec.name, true)
			}
			if spec.scope == "reset-pair" {
				return referencePairHTTP(client, reference, spec.name)
			}
			if spec.scope == "reset-scope" {
				return scopeResetHTTP(client, reference, spec.count, true)
			}
			input, err := proto.Marshal(&fixture.ReferenceTrafficRequest{Name: spec.name, Type: spec.referenceScope})
			fixture.Check(err)
			return func(ctx context.Context) (uint32, error) {
				body, err := fixture.HTTPRead(ctx, client, "https://"+reference+"/stats/", input)
				if err != nil {
					return 0, err
				}
				var result fixture.ReferenceTrafficResponse
				if err := proto.Unmarshal(body, &result); err != nil {
					return 0, err
				}
				return validateReferenceTraffic(spec, result.Stats)
			}
		}
}

func GRPC(node, reference *grpc.ClientConn) (Calls, Calls) {
	client := statsv1.NewXrayStatsServiceClient(node)
	return func(spec trafficRead) fixture.ReadCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return online.GRPC(node, spec.scope, spec.name, false)
			}
			if spec.scope == "reset-pair" {
				return nodePairGRPC(client, spec.name)
			}
			if spec.scope == "reset-scope" {
				return nodeScopeResetGRPC(client, spec.count)
			}
			single := &statsv1.XrayTrafficRequest{Id: "primary", Scope: spec.nodeScope, Name: spec.name}
			list := &statsv1.XrayTrafficListRequest{Id: "primary", Scope: spec.nodeScope}
			return func(ctx context.Context) (uint32, error) {
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
				if spec.name != "" {
					result, err := client.GetTraffic(ctx, single)
					if err != nil {
						return 0, fixture.CallError(err)
					}
					return validateNodeTraffic(spec, []*statsv1.XrayTrafficUsage{result})
				}
				results, err := client.ListTraffic(ctx, list)
				if err != nil {
					return 0, fixture.CallError(err)
				}
				return validateNodeTraffic(spec, results.Entries)
			}
		}, func(spec trafficRead) fixture.ReadCall {
			if strings.HasPrefix(spec.scope, "online-") {
				return online.GRPC(reference, spec.scope, spec.name, true)
			}
			if spec.scope == "reset-pair" {
				return referencePairGRPC(reference, spec.name)
			}
			if spec.scope == "reset-scope" {
				return referenceScopeResetGRPC(reference, spec.count)
			}
			request := &fixture.ReferenceTrafficRequest{Name: spec.name, Type: spec.referenceScope}
			return func(ctx context.Context) (uint32, error) {
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
				var result fixture.ReferenceTrafficResponse
				err := reference.Invoke(ctx, "/service.NodeService/GetStats", request, &result, grpc.StaticMethod())
				if err != nil {
					return 0, fixture.CallError(err)
				}
				return validateReferenceTraffic(spec, result.Stats)
			}
		}
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

func validateReferenceTraffic(spec trafficRead, rows []*fixture.ReferenceTrafficStat) (uint32, error) {
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
