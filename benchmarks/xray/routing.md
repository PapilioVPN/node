# Xray routing reads

## Wire behavior

- HTTP serves `GET /v1/xray/instances/{id}/routing/rules` and
  `GET /v1/xray/instances/{id}/routing/balancer?tag=...` with JSON and
  `Cache-Control: no-store`. The gRPC service is
  `papiliovpn.node.v1.XrayRoutingService` with `ListRoutingRules` and
  `GetBalancerInfo`. Both use the same read service.
- Each accepted read sends one native RPC over the current execution's
  connection. The rule list keeps native order, empty tags and repeated tags.
  Rules addressed to a balancer can have an empty outbound tag in `ListRule`.
- Balancer responses carry `override_target` and `reported_targets.tags`
  independently. Xray can report an empty override or an empty list of
  targets. Node retains the distinction between an empty value and an absent
  report. A configured router returns `Unknown` with
  `app/router: cannot find tag` for an unknown tag in v26.11.9. Node maps that
  exact outcome to HTTP 404 and gRPC `NotFound`. The default router reports
  neither field and returns success. [Pinned command](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/router/command/command.go) · [Pinned router](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/router/balancing.go)
- Read admission allows 16 simultaneous list calls and 64 balancer calls for
  the configured instance. The native deadline is one second, limited by the
  caller's earlier deadline. Native receive limits are 4 MiB for rules and
  256 KiB for a balancer. Exceeding a limit returns an error for the whole read.
- The change in `internal/engines/xray/module.go` composes the two management
  adapters. The change in `internal/engines/xray/configs/runconfig/runconfig.go`
  adds the native routing service on the same private socket as statistics.
  The integration test exercises shared config validation, startup, both APIs,
  connection reuse and restart with stock Xray.

## Paired benchmark on 2026-11-23

The measured Node builds use base `710604b` plus this increment.
Both servers ran the same stock Xray 26.11.9 binary at `676eaf6` with an idle
fixture. The reference is PasarGuard 0.5.7 at
`739eab94a9343d9f0035baf7162796844109f4c4`, whose Xray Go SDK is pinned at
`1b1647cd9ddf`. The two management binaries and the benchmark client used
Go 1.27.3, `CGO_ENABLED=0`, `-trimpath` and `GOMAXPROCS=1`. Linux 6.17
ran on an AMD Ryzen 7 3750H. Servers and Xray children were pinned to CPU 2
and the client to CPU 6. Listeners and processes ran in isolated Linux network
and PID namespaces.

The fixtures had 4 or 512 active rules with port conditions, repeated
outbound tags and blank tags on balancer rules. PapilioVPN's fixture included
an equivalent list row for the API rule that PasarGuard prepends. Both
balancers reported the same two ordered round-robin targets and a nonempty
override seeded before measurement. The fixture verified every returned tag,
list position and override. It used one outstanding request per client and
shared persistent connections within each product and transport. Management
traffic used TLS 1.3 and API key authentication. HTTP/1.1 carried JSON on
PapilioVPN and protobuf on PasarGuard. Both gRPC endpoints used protobuf.

The initial sweep used seven 450 ms windows, alternating product and client
count order. Focused cases used seven 550 ms windows and ran one operation at
one client level per fixture invocation for the lagging cases. Each table is the median of per-round
measurements. Cells show PapilioVPN / PasarGuard. Goodput is successful
verified requests divided by actual elapsed seconds. The harness counts
admission rejections and aborts on other errors. CPU time is process user plus
system time from Linux `/proc` at 100 ticks/s. It includes warmup and poll
coordination, so it is approximate at this window length. The workstation had
concurrent load during the runs.

### Initial sweep

| Transport | Operation | Clients | p50 µs | p95 µs | Success/s |
| --- | --- | ---: | ---: | ---: | ---: |
| HTTP | List 4 | 1 | 355.3 / 356.2 | 646.9 / 648.2 | 2371.6 / 2356.5 |
| HTTP | List 4 | 8 | 1138.8 / 1193.8 | 2512.7 / 2894.4 | 6241.3 / 5914.8 |
| HTTP | List 512 | 1 | 1531.0 / 1190.2 | 3417.3 / 2996.4 | 595.8 / 715.4 |
| HTTP | List 512 | 8 | 6521.1 / 6286.6 | 11593.9 / 10877.9 | 1170.8 / 1236.2 |
| HTTP | Balancer | 1 | 347.8 / 372.0 | 673.6 / 654.0 | 2398.6 / 2299.7 |
| HTTP | Balancer | 8 | 1091.8 / 1195.7 | 2559.9 / 2919.6 | 6363.3 / 5704.3 |
| gRPC | List 4 | 1 | 400.0 / 488.1 | 948.6 / 1164.5 | 1918.9 / 1676.4 |
| gRPC | List 4 | 8 | 1177.2 / 1325.5 | 2485.9 / 3704.1 | 6084.9 / 4883.9 |
| gRPC | List 512 | 1 | 1103.3 / 1166.5 | 3297.7 / 3573.4 | 702.7 / 595.4 |
| gRPC | List 512 | 8 | 6097.4 / 6609.1 | 10006.8 / 12238.2 | 1294.8 / 1136.8 |
| gRPC | Balancer | 1 | 416.5 / 422.3 | 816.2 / 807.2 | 2079.2 / 2060.6 |
| gRPC | Balancer | 8 | 1192.3 / 1287.0 | 2615.0 / 3069.5 | 5866.6 / 5147.1 |

PapilioVPN led on all three metrics in eight of twelve cases. Both
512-rule HTTP workloads trailed. The single-client HTTP and gRPC balancer
reads also trailed on p95. A preliminary domain-condition fixture timed
out during the 512-rule reference startup. The technical fixture was changed
to port conditions for both products before collecting this matrix.

### Focused repeats and optimization

| Phase | Transport | Operation | Clients | p50 µs | p95 µs | Success/s |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| repeat | HTTP | List 512 | 1 | 1516.0 / 1166.4 | 3407.9 / 3183.5 | 584.9 / 692.4 |
| repeat | HTTP | List 512 | 8 | 15159.9 / 9290.6 | 35744.1 / 23565.8 | 460.8 / 732.1 |
| repeat | HTTP | Balancer | 1 | 361.8 / 394.6 | 788.4 / 749.4 | 2270.2 / 2135.8 |
| repeat | gRPC | Balancer | 1 | 422.6 / 481.9 | 956.5 / 989.1 | 1839.5 / 1618.9 |
| optimized | HTTP | List 4 | 1 | 399.6 / 381.1 | 800.5 / 1021.2 | 2099.6 / 2009.8 |
| optimized | HTTP | List 4 | 8 | 1083.1 / 1197.0 | 2981.2 / 3717.2 | 6118.1 / 5293.4 |
| optimized | HTTP | List 512 | 1 | 1349.5 / 1289.0 | 4012.1 / 3408.8 | 579.4 / 646.9 |
| optimized | HTTP | List 512 | 8 | 7144.7 / 8555.9 | 18024.6 / 20791.7 | 968.7 / 823.7 |
| optimized-repeat | HTTP | List 4 | 1 | 330.3 / 368.7 | 826.4 / 918.8 | 2288.7 / 2035.8 |

The single-client gRPC balancer led on all three metrics in its isolated
repeat. The single-client HTTP balancer repeat measured management CPU at
232.2 / 236.0 µs per read, Xray at 153.1 / 161.7 and client at 179.2 / 191.1.
Its p95 remained 788.4 / 749.4 µs, with higher goodput for PapilioVPN.

The 512-rule HTTP repeat put the largest relative CPU cost in the client.
PapilioVPN's JSON response is decoded by the benchmark client and PasarGuard's
response uses protobuf. The Node change writes the ordered JSON body directly
and uses the standard library for tag escaping when needed. The byte-for-byte
test covers empty, repeated, Unicode, quoted and control-character tags.

| Phase | Clients | Management CPU µs/read | Xray CPU µs/read | Client CPU µs/read |
| --- | ---: | ---: | ---: | ---: |
| repeat | 1 | 711.9 / 694.1 | 434.8 / 434.8 | 745.3 / 496.9 |
| repeat | 8 | 916.0 / 779.8 | 517.2 / 442.3 | 1004.4 / 537.6 |
| optimized | 1 | 576.9 / 758.4 | 437.5 / 449.4 | 742.9 / 496.3 |
| optimized | 8 | 552.5 / 721.4 | 390.1 / 400.8 | 773.5 / 501.0 |

The optimized eight-client workload led on all three metrics in its focused
comparison. At one client, management CPU fell from 711.9 to 576.9 µs per read
while the client cost stayed close to its earlier value. The reference still led
on all three request metrics in that run. In the supplemental four-rule repeat,
PapilioVPN led on all three metrics at one client. Reference execution costs
moved between windows, so the component benchmark isolates the encoding change.

### Encoding cost

The component benchmark writes to `io.Discard` and measures seven 220 ms runs
under Go 1.26.11 on CPU 0. The standard version reproduces the initial
transport projection and `json.Encoder` call. The direct path uses a single
fresh response buffer and saves encoding time and an allocation. That buffer
increases measured allocated bytes per operation.

| Rules | Encoder | µs/op | B/op | Allocations/op |
| ---: | --- | ---: | ---: | ---: |
| 4 | standard | 0.915 | 152 | 2 |
| 4 | direct | 0.293 | 208 | 1 |
| 512 | standard | 100.884 | 18456 | 2 |
| 512 | direct | 34.051 | 24576 | 1 |

The matrix and repeats contain 294 timed windows and 338,948
verified requests. They recorded zero admission rejections.
[Per-round CSV](results/2026-11-23-routing-rounds.csv) ·
[Encoding CSV](results/2026-11-23-routing-encoding.csv)

## Bounded response buffers and focused repeat

The integrated baseline was `dcd48fd`. The updated HTTP rule handler reuses
response buffers up to 64 KiB. The routing adapter uses a local deadline and
still cancels the native stream. The pinned rule and balancer reads continue
their work independently of the server deadline. [Native methods](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/router/command/command.go#L21-L80)

The single-client comparisons used seven 500 ms rounds before and after the
change, with the same TLS connections, fixture and pins as above. The
updated 512-rule comparison used five 400 ms rounds with eight clients on
each transport. The final single-client repeat used seven one-second
rounds. Cells give PapilioVPN / PasarGuard. Latencies are in
microseconds. No requests were rejected.

| Build | Transport | Operation | Clients | p50 µs | p95 µs | Success/s |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| Before | HTTP | List 512 | 1 | 1207.6 / 1176.8 | 2797.5 / 2756.5 | 729.1 / 738.0 |
| Before | HTTP | Balancer | 1 | 360.4 / 378.4 | 692.4 / 740.9 | 2338.0 / 2151.7 |
| After | HTTP | List 512 | 1 | 1286.1 / 1224.4 | 3013.9 / 2920.8 | 678.4 / 692.3 |
| After | HTTP | Balancer | 1 | 404.6 / 436.2 | 888.4 / 900.6 | 2064.2 / 1958.0 |
| After | HTTP | List 512 | 8 | 5983.9 / 7157.6 | 11172.3 / 12822.4 | 1284.9 / 1055.8 |
| After | gRPC | List 512 | 8 | 5554.1 / 6632.0 | 9710.1 / 11908.3 | 1385.6 / 1157.5 |
| After, longer rounds | HTTP | List 512 | 1 | 1185.2 / 1218.9 | 3056.8 / 3218.7 | 722.8 / 650.2 |

The single-client balancer p95 gap from the earlier run did not reproduce on
the integrated baseline or the updated build. The 512-rule list led on all
three metrics at eight clients on both transports. At one HTTP client, it
trailed in the 500 ms rounds and led in the one-second repeat. In the
500 ms run, measured management CPU
per read was 543 / 671 µs, Xray CPU was 402 / 408 µs and client CPU was
743 / 582 µs. The client uses JSON for PapilioVPN and protobuf for PasarGuard.
These process CPU figures include warmup and measure the whole client path.

The encoding measurement uses five 170 ms runs under Go 1.26.11 on CPU 0.
It gives median Go benchmark values with the same rule tags and output.
The pooled path is the HTTP handler's writer. The direct path allocates
a fresh output buffer.

| Rules | Writer | µs/op | B/op | Allocations/op |
| ---: | --- | ---: | ---: | ---: |
| 4 | direct | 0.275 | 208 | 1 |
| 4 | pooled | 0.149 | 0 | 0 |
| 512 | direct | 22.024 | 24576 | 1 |
| 512 | pooled | 13.998 | 0 | 0 |

Use the fixture command below with `XRAY_BENCH_ROUTING_RULES=512`,
`XRAY_BENCH_ROUTING_OPERATION=list`, `XRAY_BENCH_ROUTING_CLIENTS=1`, and
`XRAY_BENCH_ROUTING_WINDOW=1s` to reproduce the longer comparison.

[Focused round CSV](results/2026-11-23-routing-buffer-tune.csv) · [Encoding CSV](results/2026-11-23-routing-buffer-encoding.csv)

## Verification

`go vet ./...`, `go test -race ./...` with the stock Xray executable,
`go mod tidy -diff`, `go mod verify` and the Node build passed with the
repository's Go 1.26.11 toolchain. The standalone integration checked
the native missing-tag status directly and both public status mappings.

## Reproduce

Run from the repository root. The commands require Go 1.27.3, protoc,
OpenSSL, curl, unzip, iproute2, taskset and Linux unprivileged user namespaces.

```bash
work=$(mktemp -d)
ref=739eab94a9343d9f0035baf7162796844109f4c4
mkdir "$work/reference"
curl -fsSL "https://codeload.github.com/PasarGuard/node/tar.gz/$ref" |
  tar -xz --strip-components=1 -C "$work/reference"
curl -fsSL https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-linux-64.zip -o "$work/xray.zip"
printf '%s  %s\n' b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82 "$work/xray.zip" | sha256sum -c
unzip -q "$work/xray.zip" -d "$work/xray"
chmod +x "$work/xray/xray"
(cd "$work/reference" && GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/pasarguard" ./cmd/node)
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/node" ./cmd/node
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/bench" ./benchmarks/xray
protoc -I "$work/reference" --descriptor_set_out="$work/reference.pb" "$work/reference/common/service.proto"

export XRAY_BENCH_ROUNDS=7 XRAY_BENCH_SERVER_CPU=2 XRAY_BENCH_CLIENT_CPU=6
XRAY_BENCH_ROUTING_RULES=4 bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" "$work/bench" "$work/reference.pb" "$work/small"
XRAY_BENCH_ROUTING_RULES=512 XRAY_BENCH_ROUTING_OPERATION=list bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" "$work/bench" "$work/reference.pb" "$work/large"
XRAY_BENCH_ROUTING_RULES=512 XRAY_BENCH_ROUTING_OPERATION=list XRAY_BENCH_ROUTING_CLIENTS=8 XRAY_BENCH_ROUTING_WINDOW=550ms XRAY_BENCH_TRANSPORTS=http bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" "$work/bench" "$work/reference.pb" "$work/focus"
```

The harness writes per-round CSV and individual successful request latencies
as `{http,grpc}-samples.csv`. Adjust the CPU identifiers to use distinct
physical cores. Use `NODE_TEST_XRAY="$work/xray/xray" go test -race ./...`
to run the real Xray integration with the race detector.

Reproduce the encoding measurement with
`taskset -c 0 go test ./internal/engines/xray/routing/bridge/http -run '^TestEncodeRulesPreservesTags$' -bench '^BenchmarkRoutingRuleEncoding$' -count=7 -benchtime=220ms`.
