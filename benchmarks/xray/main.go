// Command xray compares single-instance lifecycle operations on two running nodes.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const key = "8bc4e708-8b28-4705-8f3e-0bf78145e229"

type operation int

const (
	start operation = iota
	status
	stop
)

var operations = [3]string{"start", "status", "stop"}

type target struct {
	name    string
	pid     string
	call    func(context.Context, operation) error
	stats   func(context.Context) (uint32, error)
	traffic func(trafficRead) trafficCall
	routing routingReads
}

func main() {
	var protocol, node, nodePID, reference, referencePID, cert, descriptor, config string
	var rounds, cycles, reads, trafficNames int
	var runtimeStats bool
	var routing routingWorkload
	var routingConfig string
	var runtimeStatsClients, trafficCase, referenceTrafficConfig string
	var runtimeStatsWindow time.Duration
	flag.StringVar(&protocol, "transport", "", "http or grpc")
	flag.StringVar(&node, "node", "", "PapilioVPN Node address")
	flag.StringVar(&nodePID, "node-pid", "", "PapilioVPN Node PID in this namespace")
	flag.StringVar(&reference, "reference", "", "PasarGuard Node address")
	flag.StringVar(&referencePID, "reference-pid", "", "PasarGuard Node PID in this namespace")
	flag.StringVar(&cert, "cert", "", "trusted fixture certificate")
	flag.StringVar(&descriptor, "reference-proto", "", "PasarGuard service descriptor set")
	flag.StringVar(&config, "config", "", "common Xray JSON configuration")
	flag.IntVar(&rounds, "rounds", 5, "alternating rounds")
	flag.IntVar(&cycles, "cycles", 30, "lifecycle cycles per node per round")
	flag.IntVar(&reads, "reads", 12, "status reads per running instance per cycle")
	flag.BoolVar(&runtimeStats, "runtime-stats", false, "measure backend runtime statistics")
	flag.StringVar(&runtimeStatsClients, "runtime-stats-clients", "1,8", "comma-separated client counts for runtime statistics")
	flag.DurationVar(&runtimeStatsWindow, "runtime-stats-window", 450*time.Millisecond, "polling window for each runtime statistics measurement")
	flag.StringVar(&trafficCase, "traffic-case", "", "run only a traffic case such as user/list or both online reads with online")
	flag.StringVar(&referenceTrafficConfig, "traffic-reference-config", "", "Xray fixture configuration for the reference traffic listener")
	flag.IntVar(&trafficNames, "traffic-names", 0, "seed and measure traffic for this many benchmark accounts")
	flag.IntVar(&routing.rules, "routing-rules", 0, "measure routing reads with this many active rules")
	flag.StringVar(&routing.operation, "routing-operation", "", "run only list or balancer")
	flag.StringVar(&routing.runtimeDir, "routing-runtime-dir", "", "fixture Node runtime directory")
	flag.StringVar(&routing.sampleFile, "routing-samples", "", "write individual successful routing latencies to this CSV")
	flag.StringVar(&routingConfig, "routing-reference-config", "", "equivalent Xray configuration before PasarGuard injects its management rule")
	flag.Parse()
	if (protocol != "http" && protocol != "grpc") || rounds < 1 || cycles < 1 || reads < 1 {
		log.Fatal("specify http or grpc and positive workloads")
	}
	var clientCounts []int
	if trafficNames < 0 || trafficNames > 10000 || (trafficNames > 0 && runtimeStats) {
		log.Fatal("select one benchmark and a traffic account count from 1 to 10000")
	}
	if runtimeStats || trafficNames > 0 || routing.rules > 0 {
		if runtimeStatsWindow <= 0 {
			log.Fatal("runtime statistics window must be positive")
		}
		counts, err := parseRuntimeStatsClients(runtimeStatsClients)
		check(err)
		clientCounts = counts
	}

	if routing.rules != 0 && (routing.rules < 2 || runtimeStats || trafficNames != 0 || routingConfig == "" || routing.runtimeDir == "" ||
		(routing.operation != "" && routing.operation != "list" && routing.operation != "balancer")) {
		log.Fatal("routing comparison requires at least two rules, its fixture paths and an optional list or balancer operation")
	}

	tlsConfig := &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS13}
	if !tlsConfig.RootCAs.AppendCertsFromPEM(read(cert)) {
		log.Fatal("invalid fixture certificate")
	}
	files := new(descriptorpb.FileDescriptorSet)
	check(proto.Unmarshal(read(descriptor), files))
	registry, err := protodesc.NewFiles(files)
	check(err)
	desc, err := registry.FindDescriptorByName("service.NodeService")
	check(err)
	methods := desc.(protoreflect.ServiceDescriptor).Methods()
	if trafficNames > 0 {
		if referenceTrafficConfig == "" {
			log.Fatal("traffic comparison requires a separate reference listener configuration")
		}
		config = referenceTrafficConfig
	}
	if routing.rules > 0 {
		config = routingConfig
	}
	refStart := dynamicpb.NewMessage(methods.ByName("Start").Input())
	refStart.Set(refStart.Descriptor().Fields().ByName("config"), protoreflect.ValueOfString(string(read(config))))
	payload, err := proto.Marshal(refStart)
	check(err)

	targets := []target{{name: "papiliovpn", pid: nodePID}, {name: "pasarguard", pid: referencePID}}
	switch protocol {
	case "http":
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		if runtimeStats || trafficNames > 0 || routing.rules > 0 {
			transport.MaxIdleConnsPerHost = slices.Max(clientCounts)
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		targets[0].call = httpCall(client, node, [3]string{"PUT", "GET", "DELETE"},
			[3]string{"/v1/xray/instances/primary/execution", "/v1/xray/instances/primary", "/v1/xray/instances/primary/execution"},
			nil, verifyNodeJSON)
		targets[1].call = httpCall(client, reference, [3]string{"POST", "GET", "PUT"},
			[3]string{"/start", "/info", "/stop"}, payload, func(op operation, data []byte) error {
				return verifyReference(op, methods.ByName("GetBaseInfo").Output(), data)
			})
		if routing.rules > 0 {
			targets[0].routing = routingHTTP(client, node, false)
			targets[1].routing = routingHTTP(client, reference, true)
		}
		if runtimeStats {
			targets[0].stats = runtimeStatsHTTP(client, node, "/v1/xray/instances/primary/stats/runtime", decodeNodeStats)
			targets[1].stats = runtimeStatsHTTP(client, reference, "/stats/backend", decodeReferenceStats)
		}
		if trafficNames > 0 {
			targets[0].traffic, targets[1].traffic = trafficHTTP(client, node, reference)
		}
	case "grpc":
		nodeConn, err := grpc.NewClient(node, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		check(err)
		defer nodeConn.Close()
		refConn, err := grpc.NewClient(reference, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		check(err)
		defer refConn.Close()
		nodeClient := instancesv1.NewXrayInstancesServiceClient(nodeConn)
		targets[0].call = func(ctx context.Context, op operation) error {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
			calls := [...]func(context.Context, *instancesv1.XrayInstanceRequest, ...grpc.CallOption) (*instancesv1.XrayInstanceStatus, error){
				nodeClient.Start, nodeClient.GetStatus, nodeClient.Stop,
			}
			resp, err := calls[op](ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
			if err != nil {
				return err
			}
			want := instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY
			if op == stop {
				want = instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPED
			}
			if resp.GetId() != "primary" || resp.GetState() != want {
				return fmt.Errorf("unexpected PapilioVPN status %v", resp)
			}
			return nil
		}
		targets[1].call = func(ctx context.Context, op operation) error {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
			method := methods.ByName([3]protoreflect.Name{"Start", "GetBaseInfo", "Stop"}[op])
			req := dynamicpb.NewMessage(method.Input())
			if op == start {
				req = refStart
			}
			resp := dynamicpb.NewMessage(method.Output())
			if err := refConn.Invoke(ctx, "/service.NodeService/"+string(method.Name()), req, resp); err != nil {
				return err
			}
			if op != stop && !resp.Get(resp.Descriptor().Fields().ByName("started")).Bool() {
				return errors.New("PasarGuard reported an inactive Xray process")
			}
			return nil
		}
		if routing.rules > 0 {
			targets[0].routing = routingGRPC(nodeConn, false)
			targets[1].routing = routingGRPC(refConn, true)
		}
		if runtimeStats {
			targets[0].stats, targets[1].stats = runtimeStatsGRPC(nodeConn, refConn)
		}
		if trafficNames > 0 {
			targets[0].traffic, targets[1].traffic = trafficGRPC(nodeConn, refConn)
		}
	}

	if routing.rules > 0 {
		routing.rounds = rounds
		routing.clients = clientCounts
		routing.window = runtimeStatsWindow
		benchmarkRouting(protocol, targets, routing)
		return
	}
	if trafficNames > 0 {
		benchmarkTraffic(protocol, targets, rounds, clientCounts, trafficNames, runtimeStatsWindow, trafficCase)
		return
	}
	if runtimeStats {
		benchmarkRuntimeStats(protocol, targets, rounds, clientCounts, runtimeStatsWindow)
		return
	}

	output := csv.NewWriter(os.Stdout)
	check(output.Write([]string{"transport", "round", "node", "operation", "samples", "p50_us", "p95_us", "p99_us"}))
	for round := range rounds {
		order := []int{0, 1}
		if round%2 == 1 {
			slices.Reverse(order)
		}
		for _, index := range order {
			t := targets[index]
			exercise(t, 1, nil)
			samples := [3][]time.Duration{
				make([]time.Duration, 0, cycles), make([]time.Duration, 0, cycles*reads), make([]time.Duration, 0, cycles),
			}
			for range cycles {
				exercise(t, reads, &samples)
			}
			for i, timings := range samples {
				slices.Sort(timings)
				check(output.Write([]string{protocol, strconv.Itoa(round + 1), t.name, operations[i], strconv.Itoa(len(timings)),
					percentile(timings, 50), percentile(timings, 95), percentile(timings, 99)}))
			}
			output.Flush()
			check(output.Error())
		}
	}
}

func exercise(t target, reads int, samples *[3][]time.Duration) {
	call := func(op operation) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		started := time.Now()
		err := t.call(ctx, op)
		elapsed := time.Since(started)
		cancel()
		if err != nil {
			log.Fatalf("%s %s: %v", t.name, operations[op], err)
		}
		if samples != nil {
			samples[op] = append(samples[op], elapsed)
		}
	}
	call(start)
	if !hasChild(t.pid) {
		log.Fatalf("%s start returned without a child", t.name)
	}
	for range reads {
		call(status)
	}
	call(stop)
	if hasChild(t.pid) {
		log.Fatalf("%s stop returned before its child was reaped", t.name)
	}
}

func hasChild(pid string) bool {
	paths, err := filepath.Glob("/proc/" + pid + "/task/*/children")
	check(err)
	if len(paths) == 0 {
		log.Fatalf("cannot observe server %s children", pid)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		check(err)
		if strings.TrimSpace(string(data)) != "" {
			return true
		}
	}
	return false
}

func httpCall(client *http.Client, address string, methods, paths [3]string, payload []byte, verify func(operation, []byte) error) func(context.Context, operation) error {
	return func(ctx context.Context, op operation) error {
		var body io.Reader
		if op == start && payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, methods[op], "https://"+address+paths[op], body)
		if err != nil {
			return err
		}
		req.Header.Set("X-API-Key", key)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %s", resp.Status, data)
		}
		return verify(op, data)
	}
}

func verifyNodeJSON(op operation, data []byte) error {
	var response struct{ ID, State string }
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	want := "ready"
	if op == stop {
		want = "stopped"
	}
	if response.ID != "primary" || response.State != want {
		return fmt.Errorf("unexpected PapilioVPN status %s", data)
	}
	return nil
}

func verifyReference(op operation, descriptor protoreflect.MessageDescriptor, data []byte) error {
	if op == stop {
		return nil
	}
	response := dynamicpb.NewMessage(descriptor)
	if err := proto.Unmarshal(data, response); err != nil {
		return err
	}
	if !response.Get(descriptor.Fields().ByName("started")).Bool() {
		return errors.New("PasarGuard reported an inactive Xray process")
	}
	return nil
}

func percentile(sorted []time.Duration, p int) string {
	return strconv.FormatFloat(float64(sorted[(len(sorted)*p+99)/100-1])/float64(time.Microsecond), 'f', 2, 64)
}

func read(path string) []byte {
	data, err := os.ReadFile(path)
	check(err)
	return data
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
