// Command xray runs paired Xray management benchmarks.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/PapilioVPN/node/benchmarks/xray/accounts"
	"github.com/PapilioVPN/node/benchmarks/xray/instances"
	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"
	"github.com/PapilioVPN/node/benchmarks/xray/logs"
	"github.com/PapilioVPN/node/benchmarks/xray/outbounds"
	"github.com/PapilioVPN/node/benchmarks/xray/routing"
	"github.com/PapilioVPN/node/benchmarks/xray/runtime"
	"github.com/PapilioVPN/node/benchmarks/xray/traffic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func main() {
	var protocol, node, nodePID, reference, referencePID, cert, descriptor, config string
	var rounds, cycles, reads, trafficNames int
	var startPathsDir string
	var runtimeStats, outboundHealth bool
	var outboundCase, outboundSamples string
	var routingWork routing.Workload
	var accountWork accounts.Workload
	var logWork logs.Workload
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
	flag.StringVar(&startPathsDir, "start-paths-dir", "", "write matched gRPC Start path samples to this directory")
	flag.IntVar(&reads, "reads", 12, "status reads per running instance per cycle")
	flag.BoolVar(&runtimeStats, "runtime-stats", false, "measure backend runtime statistics")
	flag.StringVar(&runtimeStatsClients, "runtime-stats-clients", "1,8", "comma-separated client counts for runtime statistics")
	flag.DurationVar(&runtimeStatsWindow, "runtime-stats-window", 450*time.Millisecond, "polling window for each runtime statistics measurement")
	flag.StringVar(&trafficCase, "traffic-case", "", "run only a traffic case such as user/list or both online reads with online")
	flag.StringVar(&referenceTrafficConfig, "traffic-reference-config", "", "Xray fixture configuration for the reference traffic listener")
	flag.IntVar(&trafficNames, "traffic-names", 0, "seed and measure traffic for this many benchmark accounts")
	flag.IntVar(&routingWork.Rules, "routing-rules", 0, "measure routing reads with this many active rules")
	flag.StringVar(&routingWork.Operation, "routing-operation", "", "run one routing operation: list, balancer, test, override, clear, append, replace, remove, or remove-absent")
	flag.StringVar(&routingWork.RuntimeDir, "routing-runtime-dir", "", "fixture Node runtime directory")
	flag.StringVar(&routingWork.SampleFile, "routing-samples", "", "write individual successful routing latencies to this CSV")
	flag.StringVar(&routingConfig, "routing-reference-config", "", "equivalent Xray configuration before PasarGuard injects its management rule")
	flag.BoolVar(&outboundHealth, "outbound-health", false, "measure ordinary Observatory reads")
	flag.StringVar(&outboundCase, "outbound-health-case", "", "run only one or all outbound observations")
	flag.StringVar(&outboundSamples, "outbound-health-samples", "", "path for compressed per-request outbound health CSV")
	flag.StringVar(&accountWork.Sync, "account-sync", "", "measure VLESS synchronization: user, users or chunked")
	flag.StringVar(&accountWork.RuntimeDir, "account-runtime-dir", "", "measure VLESS accounts on the execution in this runtime directory")
	flag.StringVar(&accountWork.Generated, "account-reference-generated", "", "PasarGuard's generated fixture config")
	flag.StringVar(&accountWork.Samples, "account-samples", "", "write per-request account benchmark CSV to this path")
	flag.IntVar(&accountWork.Mutations, "account-mutations", 256, "accounts added and removed per measurement round")
	flag.StringVar(&logWork.Proxy, "logs-proxy", "", "benchmark stdout access logs generated through this Xray proxy")
	flag.StringVar(&logWork.Raw, "logs-samples", "", "write raw per-subscriber log CSV to this gzip path")
	flag.Parse()
	if (protocol != "http" && protocol != "grpc") || rounds < 1 || cycles < 1 || reads < 1 {
		log.Fatal("specify http or grpc and positive workloads")
	}
	if startPathsDir != "" && protocol != "grpc" {
		log.Fatal("Start path comparison requires gRPC")
	}
	var clientCounts []int
	if trafficNames < 0 || trafficNames > 10000 || (trafficNames > 0 && runtimeStats) || (outboundHealth && (runtimeStats || trafficNames > 0)) {
		log.Fatal("select one benchmark and a traffic account count from 1 to 10000")
	}
	if runtimeStats || trafficNames > 0 || routingWork.Rules > 0 || outboundHealth || accountWork.RuntimeDir != "" {
		if runtimeStatsWindow <= 0 {
			log.Fatal("runtime statistics window must be positive")
		}
		counts, err := fixture.ParseClients(runtimeStatsClients)
		fixture.Check(err)
		clientCounts = counts
	}

	if routingWork.Rules != 0 && (routingWork.Rules < 2 || runtimeStats || outboundHealth || trafficNames != 0 || routingConfig == "" || routingWork.RuntimeDir == "" ||
		!routing.SupportedOperation(routingWork.Operation)) {
		log.Fatal("routing comparison requires at least two rules, its fixture paths and a supported routing operation")
	}
	if routing.IsRuleWrite(routingWork.Operation) && (len(clientCounts) != 1 || clientCounts[0] != 1) {
		log.Fatal("routing rule mutations use exactly one client")
	}

	if accountWork.Sync != "" && accountWork.Sync != "user" && accountWork.Sync != "users" && accountWork.Sync != "chunked" {
		fixture.Check(errors.New("invalid account sync operation"))
	}
	if accountWork.RuntimeDir != "" && (runtimeStats || outboundHealth || trafficNames != 0 || routingWork.Rules != 0 ||
		accountWork.Generated == "" || accountWork.Mutations < 1 || accountWork.Mutations > 10000) {
		log.Fatal("account comparison requires its fixture paths, 1 to 10000 mutations, and no other benchmark")
	}
	tlsConfig := &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS13}
	if !tlsConfig.RootCAs.AppendCertsFromPEM(fixture.Read(cert)) {
		log.Fatal("invalid fixture certificate")
	}
	files := new(descriptorpb.FileDescriptorSet)
	fixture.Check(proto.Unmarshal(fixture.Read(descriptor), files))
	registry, err := protodesc.NewFiles(files)
	fixture.Check(err)
	desc, err := registry.FindDescriptorByName("service.NodeService")
	fixture.Check(err)
	methods := desc.(protoreflect.ServiceDescriptor).Methods()
	if trafficNames > 0 {
		if referenceTrafficConfig == "" {
			log.Fatal("traffic comparison requires a separate reference listener configuration")
		}
		config = referenceTrafficConfig
	}
	if routingWork.Rules > 0 {
		config = routingConfig
	}
	configJSON := fixture.Read(config)
	var nodeStartConfig []byte
	if startPathsDir == "" && !runtimeStats && trafficNames == 0 && routingWork.Rules == 0 &&
		!outboundHealth && accountWork.RuntimeDir == "" && logWork.Proxy == "" {
		configJSON = bytes.TrimSpace(configJSON)
		nodeStartConfig = configJSON
	}
	refStart := dynamicpb.NewMessage(methods.ByName("Start").Input())
	refStart.Set(refStart.Descriptor().Fields().ByName("config"), protoreflect.ValueOfString(string(configJSON)))
	payload, err := proto.Marshal(refStart)
	fixture.Check(err)

	targets := [2]fixture.Instance{{Name: "papiliovpn", PID: nodePID}, {Name: "pasarguard", PID: referencePID}}
	var logCalls [2]logs.Stream
	var routeCalls [2]routing.Calls
	var runtimeCalls [2]fixture.ReadCall
	var accountCalls [2]accounts.Calls
	var healthCalls [2]outbounds.Calls
	var trafficCalls [2]traffic.Calls
	switch protocol {
	case "http":
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		if runtimeStats || trafficNames > 0 || routingWork.Rules > 0 || outboundHealth || accountWork.RuntimeDir != "" {
			transport.MaxIdleConnsPerHost = slices.Max(clientCounts)
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport}
		targets[0].Call, targets[1].Call = instances.HTTP(client, node, reference, nodeStartConfig, payload, methods)
		if logWork.Proxy != "" {
			logCalls[0] = logs.HTTP(client, node, false)
			logCalls[1] = logs.HTTP(client, reference, true)
		}
		if routingWork.Rules > 0 {
			routeCalls[0] = routing.HTTP(client, node, false)
			routeCalls[1] = routing.HTTP(client, reference, true)
		}
		if runtimeStats {
			runtimeCalls[0], runtimeCalls[1] = runtime.HTTP(client, node, reference)
		}
		if accountWork.RuntimeDir != "" {
			accountCalls[0], accountCalls[1] = accounts.HTTP(client, node, reference)
		}
		if outboundHealth {
			healthCalls[0], healthCalls[1] = outbounds.HTTP(client, node, reference)
		}
		if trafficNames > 0 {
			trafficCalls[0], trafficCalls[1] = traffic.HTTP(client, node, reference)
		}
	case "grpc":
		nodeConn, err := grpc.NewClient(node, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		fixture.Check(err)
		defer nodeConn.Close()
		refConn, err := grpc.NewClient(reference, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		fixture.Check(err)
		defer refConn.Close()
		targets[0].Call, targets[1].Call = instances.GRPC(nodeConn, refConn, nodeStartConfig, refStart, methods)
		if startPathsDir != "" {
			instances.BenchmarkStartPaths(targets, nodeConn, refConn, methods.ByName("Start"), startPathsDir, rounds, cycles)
			return
		}
		if logWork.Proxy != "" {
			logCalls[0], logCalls[1] = logs.GRPC(nodeConn, refConn)
		}
		if routingWork.Rules > 0 {
			routeCalls[0] = routing.GRPC(nodeConn, false)
			routeCalls[1] = routing.GRPC(refConn, true)
		}
		if runtimeStats {
			runtimeCalls[0], runtimeCalls[1] = runtime.GRPC(nodeConn, refConn)
		}
		if accountWork.RuntimeDir != "" {
			accountCalls[0], accountCalls[1] = accounts.GRPC(nodeConn, refConn)
		}
		if outboundHealth {
			healthCalls[0], healthCalls[1] = outbounds.GRPC(nodeConn, refConn)
		}
		if trafficNames > 0 {
			trafficCalls[0], trafficCalls[1] = traffic.GRPC(nodeConn, refConn)
		}
	}

	if logWork.Proxy != "" {
		logs.Benchmark(protocol, fixture.WithCalls(targets, logCalls), rounds, logWork)
		return
	}
	if routingWork.Rules > 0 {
		routingWork.Rounds = rounds
		routingWork.Clients = clientCounts
		routingWork.Window = runtimeStatsWindow
		routing.Benchmark(protocol, fixture.WithCalls(targets, routeCalls), routingWork)
		return
	}
	if accountWork.RuntimeDir != "" {
		if accountWork.Sync != "" {
			accounts.BenchmarkSync(protocol, fixture.WithCalls(targets, accountCalls), rounds, reads, accountWork)
		} else {
			accounts.Benchmark(protocol, fixture.WithCalls(targets, accountCalls), rounds, clientCounts, runtimeStatsWindow, accountWork)
		}
		return
	}

	if outboundHealth {
		outbounds.Benchmark(protocol, fixture.WithCalls(targets, healthCalls), rounds, clientCounts, runtimeStatsWindow, outboundCase, outboundSamples)
		return
	}
	if trafficNames > 0 {
		traffic.Benchmark(protocol, fixture.WithCalls(targets, trafficCalls), rounds, clientCounts, trafficNames, runtimeStatsWindow, trafficCase)
		return
	}
	if runtimeStats {
		runtime.Benchmark(protocol, fixture.WithCalls(targets, runtimeCalls), rounds, clientCounts, runtimeStatsWindow)
		return
	}

	instances.Benchmark(protocol, targets, rounds, cycles, reads)
}
