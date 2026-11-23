package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	fixtureBalancer = "pool"
	fixtureOverride = "direct"
)

var fixtureTargets = []string{"pool-a", "pool-b"}

type routingReads struct {
	rules    func(context.Context) (*routingv1.XrayRoutingRules, error)
	control  routingControl
	balancer func(context.Context) (string, []string, error)
	override func(context.Context) error
}

func routingHTTP(client *http.Client, address string, reference bool) routingReads {
	base := "https://" + address
	control := routingHTTPControl(client, base, reference)
	if reference {
		tag, err := proto.Marshal(wrapperspb.String(fixtureBalancer))
		check(err)
		return routingReads{
			control: control,
			rules: func(ctx context.Context) (*routingv1.XrayRoutingRules, error) {
				result := new(routingv1.XrayRoutingRules) // Same response field numbers.
				return result, routingHTTPRead(ctx, client, http.MethodGet, base+"/routing/rules", nil, result, false)
			},
			balancer: func(ctx context.Context) (string, []string, error) {
				var result ReferenceBalancerInfo
				err := routingHTTPRead(ctx, client, http.MethodPost, base+"/routing/balancer", tag, &result, false)
				return result.GetOverrideTarget(), result.GetPrincipleTarget(), err
			},
			override: func(ctx context.Context) error {
				return routingHTTPRead(ctx, client, http.MethodPut, base+"/routing/balancer/override",
					overrideFixture(fixtureOverride).ProtoReflect().GetUnknown(), &emptypb.Empty{}, false)
			},
		}
	}
	base += "/v1/xray/instances/primary/routing"
	return routingReads{
		control: control,
		rules: func(ctx context.Context) (*routingv1.XrayRoutingRules, error) {
			result := new(routingv1.XrayRoutingRules)
			return result, routingHTTPRead(ctx, client, http.MethodGet, base+"/rules", nil, result, true)
		},
		balancer: func(ctx context.Context) (string, []string, error) {
			var result routingv1.XrayBalancerInfo
			err := routingHTTPRead(ctx, client, http.MethodGet, base+"/balancer?tag="+fixtureBalancer, nil, &result, true)
			return result.GetOverrideTarget(), result.GetReportedTargets().GetTags(), err
		},
	}
}

func routingHTTPRead(ctx context.Context, client *http.Client, method, endpoint string, payload []byte, result proto.Message, isJSON bool) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", key)
	if len(payload) != 0 {
		if isJSON {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Content-Type", "application/x-protobuf")
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusNoContent:
		if result == nil && len(body) == 0 {
			return nil
		}
		return fmt.Errorf("unexpected HTTP %s", resp.Status)
	case http.StatusOK:
		if isJSON {
			return json.Unmarshal(body, result)
		}
		return proto.Unmarshal(body, result)
	case http.StatusTooManyRequests:
		return errAtCapacity
	default:
		return fmt.Errorf("HTTP %s: %s", resp.Status, body)
	}
}

func routingGRPC(conn *grpc.ClientConn, reference bool) routingReads {
	control := routingGRPCControl(conn, reference)
	if reference {
		const service = "/service.NodeService/"
		list := &emptypb.Empty{}
		tag := wrapperspb.String(fixtureBalancer)
		return routingReads{
			control: control,
			rules: func(ctx context.Context) (*routingv1.XrayRoutingRules, error) {
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
				result := new(routingv1.XrayRoutingRules)
				return result, conn.Invoke(ctx, service+"ListRoutingRules", list, result, grpc.StaticMethod())
			},
			balancer: func(ctx context.Context) (string, []string, error) {
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
				var result ReferenceBalancerInfo
				err := conn.Invoke(ctx, service+"GetBalancerInfo", tag, &result, grpc.StaticMethod())
				return result.GetOverrideTarget(), result.GetPrincipleTarget(), err
			},
			override: func(ctx context.Context) error {
				ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
				return conn.Invoke(ctx, service+"OverrideBalancerTarget", overrideFixture(fixtureOverride), &emptypb.Empty{})
			},
		}
	}
	client := routingv1.NewXrayRoutingServiceClient(conn)
	list := &routingv1.XrayRoutingRulesRequest{Id: "primary"}
	balancer := &routingv1.XrayBalancerInfoRequest{Id: "primary", Tag: fixtureBalancer}
	return routingReads{
		control: control,
		rules: func(ctx context.Context) (*routingv1.XrayRoutingRules, error) {
			return client.ListRoutingRules(metadata.AppendToOutgoingContext(ctx, "x-api-key", key), list)
		},
		balancer: func(ctx context.Context) (string, []string, error) {
			result, err := client.GetBalancerInfo(metadata.AppendToOutgoingContext(ctx, "x-api-key", key), balancer)
			return result.GetOverrideTarget(), result.GetReportedTargets().GetTags(), err
		},
	}
}

// Both stock Xray and the reference setter carry tag and target in fields 1 and 2.
func overrideFixture(target string) *emptypb.Empty {
	request := &emptypb.Empty{}
	fields := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), fixtureBalancer)
	fields = protowire.AppendString(protowire.AppendTag(fields, 2, protowire.BytesType), target)
	request.ProtoReflect().SetUnknown(fields)
	return request
}

func seedRoutingOverride(ctx context.Context, runtimeDir string) error {
	sockets, err := filepath.Glob(filepath.Join(runtimeDir, "node-xray-*", "api.sock"))
	if err != nil {
		return err
	}
	if len(sockets) != 1 {
		return fmt.Errorf("expected one active fixture socket in %s, found %d", runtimeDir, len(sockets))
	}
	socket := url.URL{Scheme: "unix", Path: sockets[0]}
	conn, err := grpc.NewClient(socket.String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Invoke(ctx, "/xray.app.router.command.RoutingService/OverrideBalancerTarget", overrideFixture(fixtureOverride), &emptypb.Empty{})
}

func fixtureRules(total int) [][2]string {
	want := make([][2]string, total)
	want[0] = [2]string{"", "API"}
	for i := 1; i < total; i++ {
		if i%4 != 1 {
			want[i][0] = fmt.Sprintf("rule-%04d", total-i)
		}
		want[i][1] = "direct"
		if i%4 == 1 {
			want[i][1] = "" // A balancer rule carries no outbound tag in ListRule.
		}
	}
	return want
}

type routingWorkload struct {
	rules, rounds int
	clients       []int
	window        time.Duration
	operation     string
	runtimeDir    string
	sampleFile    string
}

func benchmarkRouting(protocol string, targets []target, workload routingWorkload) {
	children := make([]string, len(targets))
	for i, t := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := t.call(ctx, start)
		cancel()
		check(err)
		children[i] = routingChildPID(t.pid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	check(seedRoutingOverride(ctx, workload.runtimeDir))
	check(targets[1].routing.override(ctx))
	cancel()

	want := fixtureRules(workload.rules)
	var samples *csv.Writer
	if workload.sampleFile != "" {
		file, err := os.Create(workload.sampleFile)
		check(err)
		defer file.Close()
		samples = csv.NewWriter(file)
		check(samples.Write([]string{"transport", "round", "node", "operation", "rules", "clients", "latency_ns"}))
	}
	output := csv.NewWriter(os.Stdout)
	check(output.Write([]string{"transport", "round", "node", "operation", "rules", "clients", "success", "rejected", "seconds", "goodput_per_sec", "p50_us", "p95_us", "management_cpu_ticks", "xray_cpu_ticks", "client_cpu_ticks"}))
	for round := range workload.rounds {
		order := []int{0, 1}
		counts := slices.Clone(workload.clients)
		if round%2 == 1 {
			slices.Reverse(order)
			slices.Reverse(counts)
		}
		operations := []string{workload.operation}
		if workload.operation == "" {
			operations = []string{"list", "balancer"}
		}
		for _, clients := range counts {
			for _, op := range operations {
				for _, index := range order {
					t := targets[index]
					rows := workload.rules
					if op != "list" {
						rows = 0
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second+workload.window)
					pids := [3]string{t.pid, children[index], strconv.Itoa(os.Getpid())}
					before := routingCPUTicks(pids)
					result, err := pollRuntimeStats(ctx, routingRead(t.routing, op, want), clients, workload.window)
					after := routingCPUTicks(pids)
					if err == nil && (op == overrideOp || op == clearOp) {
						target, _, verifyErr := t.routing.balancer(ctx)
						if verifyErr != nil || target != overrideDestination(op) {
							err = fmt.Errorf("verify override: got %q, error %v", target, verifyErr)
						}
					}
					cancel()
					if err != nil {
						check(fmt.Errorf("%s %s (%d clients): %w", t.name, op, clients, err))
					}
					base := []string{protocol, strconv.Itoa(round + 1), t.name, op, strconv.Itoa(rows), strconv.Itoa(clients)}
					check(output.Write(append(slices.Clone(base), strconv.Itoa(len(result.timings)), strconv.Itoa(result.rejected),
						strconv.FormatFloat(result.elapsed.Seconds(), 'f', 6, 64),
						strconv.FormatFloat(float64(len(result.timings))/result.elapsed.Seconds(), 'f', 2, 64),
						percentile(result.timings, 50), percentile(result.timings, 95),
						strconv.FormatUint(after[0]-before[0], 10),
						strconv.FormatUint(after[1]-before[1], 10),
						strconv.FormatUint(after[2]-before[2], 10))))
					if samples != nil {
						for _, elapsed := range result.timings {
							check(samples.Write(append(base, strconv.FormatInt(elapsed.Nanoseconds(), 10))))
						}
						samples.Flush()
						check(samples.Error())
					}
					output.Flush()
					check(output.Error())
				}
			}
		}
	}
	ctx, cancel = context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, t := range targets {
		check(t.call(ctx, stop))
	}
}

func routingRead(calls routingReads, op string, want [][2]string) func(context.Context) (uint32, error) {
	if op == probeOp || op == overrideOp || op == clearOp {
		return routingControlRead(calls.control, op)
	}
	if op == "balancer" {
		return func(ctx context.Context) (uint32, error) {
			override, targets, err := calls.balancer(ctx)
			if err != nil {
				return 0, trafficCallError(err)
			}
			if override != fixtureOverride || !slices.Equal(targets, fixtureTargets) {
				return 0, fmt.Errorf("unexpected balancer response: %q %v", override, targets)
			}
			return 0, nil
		}
	}
	return func(ctx context.Context) (uint32, error) {
		result, err := calls.rules(ctx)
		if err != nil {
			return 0, trafficCallError(err)
		}
		if len(result.GetRules()) != len(want) {
			return 0, fmt.Errorf("got %d rules, want %d", len(result.GetRules()), len(want))
		}
		for i, rule := range result.GetRules() {
			if got := [2]string{rule.GetRuleTag(), rule.GetOutboundTag()}; got != want[i] {
				return 0, fmt.Errorf("rule %d = %q, want %q", i, got, want[i])
			}
		}
		return 0, nil
	}
}

func routingChildPID(parent string) string {
	paths, err := filepath.Glob("/proc/" + parent + "/task/*/children")
	check(err)
	var children []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		check(err)
		children = append(children, strings.Fields(string(data))...)
	}
	if len(children) != 1 {
		check(fmt.Errorf("expected exactly one Xray child for %s, got %v", parent, children))
	}
	return children[0]
}

func routingCPUTicks(pids [3]string) [3]uint64 {
	var ticks [3]uint64
	for i, pid := range pids {
		data := string(read("/proc/" + pid + "/stat"))
		fields := strings.Fields(data[strings.LastIndexByte(data, ')')+1:])
		// Fields 14 and 15 are user and system ticks. fields starts at field 3.
		if len(fields) < 13 {
			check(fmt.Errorf("invalid /proc/%s/stat", pid))
		}
		user, err := strconv.ParseUint(fields[11], 10, 64)
		check(err)
		system, err := strconv.ParseUint(fields[12], 10, 64)
		check(err)
		ticks[i] = user + system
	}
	return ticks
}
