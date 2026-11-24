# Xray routing evaluation and override

The capability uses the running instance's private Xray connection. Both
transports reach the same service. Each accepted operation invokes one native
RPC. The probe allows 16 concurrent calls and the override shares a single
mutation slot per configured instance. Both use a one-second client budget
capped by the request context.

| Operation | HTTP | gRPC contract |
| --- | --- | --- |
| Evaluate a route | POST /v1/xray/instances/{id}/routing/tests | XrayRoutingService.TestRoute |
| Set or clear an override | PUT /v1/xray/instances/{id}/routing/balancer/override?tag=... | XrayRoutingService.OverrideBalancerTarget |

The evaluation accepts a connection's inbound tag, network, target IP, target
port, target domain, protocol, user and attributes. It returns the destination
tag and any outbound group tags Xray reports. With the pinned release, no match
maps to HTTP 404 and gRPC NotFound. Evaluation can use the configured resolver,
advance a balancer strategy or trigger a configured webhook. The result
describes the selected route.

Override accepts an explicit target. An empty target clears it. The write
returns an acknowledgement and a separate GetBalancerInfo can observe it.
Xray accepts a target tag without checking that the outbound exists. A
dispatched write with no reliable acknowledgement returns HTTP 502 or gRPC
Aborted and fences mutations on that execution. A new execution has its own
connection and may accept writes. Native errors that establish a missing
balancer or unsupported router return their respective public status.
[Native command](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/router/command/command.go) ·
[Native override](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/router/balancing.go)

## Benchmark

Both products used stock Xray 26.11.9, an idle four-rule fixture, the pinned
PasarGuard 739eab9 reference with its own SDK, and the TLS harness described
in [routing reads](reads.md#reproduce). Binaries used Go 1.27.3, Linux
CPU 2 for servers and Xray and CPU 6 for the client. Tests use a direct
route and check the returned tag. Writes benchmark one client in line with
the mutation admission policy. The set and clear windows repeatedly assign
the same value and verify it through GetBalancerInfo outside the timed window.
The real-engine integration explicitly verifies the transitions in both
protocols. The initial matrix records five 450 ms windows per cell and the
isolated repeat uses seven 700 ms windows. HTTP uses each product's public
representation, JSON for PapilioVPN and protobuf for PasarGuard.

| Phase | API | Operation | Rules | Clients | p50 µs | p95 µs | Success/s |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| final | grpc | clear | 0 | 1 | 431.3 / 495.3 | 779.0 / 971.5 | 2099.6 / 1770.6 |
| final | grpc | override | 0 | 1 | 467.2 / 521.0 | 1052.6 / 1062.2 | 1783.4 / 1604.0 |
| final | http | clear | 0 | 1 | 420.4 / 483.4 | 1055.6 / 1446.3 | 1917.0 / 1537.6 |
| final | http | override | 0 | 1 | 413.9 / 448.0 | 918.7 / 1006.7 | 2016.4 / 1870.5 |
| matrix | grpc | clear | 0 | 1 | 524.7 / 595.4 | 1086.0 / 1052.1 | 1632.2 / 1563.2 |
| matrix | grpc | override | 0 | 1 | 419.3 / 473.2 | 746.8 / 815.2 | 2160.6 / 1946.8 |
| matrix | grpc | test | 0 | 1 | 508.2 / 528.6 | 930.8 / 878.4 | 1763.7 / 1746.5 |
| matrix | grpc | test | 0 | 8 | 1335.7 / 1433.9 | 3226.8 / 3411.3 | 5305.2 / 4714.0 |
| matrix | http | clear | 0 | 1 | 380.5 / 410.5 | 687.9 / 771.8 | 2307.7 / 2110.0 |
| matrix | http | override | 0 | 1 | 372.7 / 416.3 | 703.6 / 711.0 | 2354.1 / 2141.9 |
| matrix | http | test | 0 | 1 | 454.4 / 453.3 | 829.0 / 982.1 | 1970.5 / 1860.8 |
| matrix | http | test | 0 | 8 | 1286.8 / 1455.6 | 2758.7 / 3489.3 | 5574.9 / 4822.4 |
| repeat | grpc | clear | 0 | 1 | 436.1 / 516.2 | 879.9 / 1091.5 | 2008.8 / 1664.4 |
| repeat | grpc | test | 0 | 1 | 458.6 / 503.4 | 768.0 / 903.8 | 1973.2 / 1753.7 |
| repeat | http | test | 0 | 1 | 433.4 / 458.4 | 788.8 / 871.5 | 2079.1 / 1906.4 |

162 windows and 192,927 samples, 0 rejections.

The three cases that lagged on an initial metric led on all three metrics in
the isolated repeat. The representative single-client management CPU costs
in those repeats were 239 / 266 µs for HTTP TestRoute, 263 / 293 µs for gRPC
TestRoute, and 264 / 319 µs for gRPC clear, respectively. CPU includes warmup
and is sampled in 10 ms ticks. The final phase uses five 550 ms windows
with the mutation replay buffer disabled. The fixture records per-request
CSV locally and checks the count against the committed per-round CSV.

[Per-round CSV](results/2026-11-23-routing-control-rounds.csv)

For the benchmark, build the binaries and descriptor as in the routing-reads
instructions and set `XRAY_BENCH_ROUTING_RULES=4`,
`XRAY_BENCH_ROUTING_OPERATION=test|override|clear` and
`XRAY_BENCH_ROUTING_CLIENTS=1,8` for evaluation or `1` for writes.
Set `XRAY_BENCH_TRANSPORTS` to isolate a transport and
`XRAY_BENCH_ROUTING_WINDOW` to select the sampling window. The harness saves
individual successful latency samples next to the per-round CSV.

The [Go 1.27.4 comparison](rule-writes.md#final-toolchain-comparison)
covers these operations on the final toolchain pin and repeats its lagging
cases. This page retains the original Go 1.27.3 measurements.
