# Xray routing rule changes

Add and remove share the active execution's mutation gate and outcome handling
with [balancer overrides](routing-control.md). Each accepted request invokes
one native mutation RPC. An unknown result on a dispatched change fences
later mutations on that execution. HTTP reports 502 and gRPC reports Aborted.
The rule list remains available for independent observation. The running
configuration file stays untouched and a new execution starts from its
configured source.

| Operation | HTTP | gRPC contract |
| --- | --- | --- |
| Add a single rule | POST /v1/xray/instances/{id}/routing/rules | XrayRoutingService.AddRoutingRule |
| Remove by exact tag | DELETE /v1/xray/instances/{id}/routing/rules?tag=... | XrayRoutingService.RemoveRoutingRule |

HTTP add takes `{"rule":{...},"mode":"append"}` with a native
`routing.rules[]` JSON object. gRPC takes the same source as `rule_json`
bytes and an enum mode. Omitted mode selects append. Replace requires the
replace mode and `confirm_replace: true` in the same request. Each successful
write returns HTTP 204 or an empty gRPC acknowledgement.

- Append adds the rule at the end and retains the previous rule order, balancer
  state and overrides. The existing rule with the highest priority still wins.
- Replace installs the single rule as the entire active rule set and clears
  all existing balancers and overrides. The running domain strategy and
  initialized routing scripts are outside this native operation. A replacement
  rule requires an outbound destination because the request supplies one rule
  and supplies no new balancer definitions.
- Remove accepts a nonempty tag. A successful response covers the already
  absent case. The native API supplies no removed-count field.
- The matching Xray SDK compiles the input JSON and packages `xray.app.router.Config`.
  An add accepts at most 32 KiB of source and 1 MiB of compiled routing data.
  Rule tags in the add and remove requests are limited to 4096 bytes.
  Attribute patterns are checked before dispatch because the native router
  compiles them using `regexp.MustCompile` when constructing its conditions.
- The official SDK pin is `676eaf66f8f0` from Xray 26.11.9. Node's Go pin is
  1.27.3 to meet the SDK's Go requirement. The rule compiler is linked into
  Node and the live Xray router still runs in the supervised child process. The
  measured Linux amd64 executable grew from 21.8 MB to 48.5 MB with the SDK.
  [Pinned CLI compiler](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/main/commands/all/api/rules_add.go) ·
  [Pinned router](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/router/router.go)

## Verification

The branch passed `go vet ./...`, `go test -race ./...` with stock Xray
26.11.9, module verification and a Linux amd64 build. The real fixture
checks append precedence, explicit confirmation, replace effects, duplicate
rejection and present/absent removal on both management APIs. Controlled
gRPC tests cover a lost acknowledgement, cross-operation fencing and
serialization.

## Paired benchmark

Use the [routing read fixture setup](routing.md#reproduce) to build with Go
1.27.3 and run against PasarGuard 739eab9. Both sides use the same stock
Xray 26.11.9 executable, rule source, order, TLS 1.3, API key and CPU affinity.
PasarGuard compiles rule JSON via its own pinned Xray SDK. On HTTP it wraps the
source in protobuf while Node uses an embedded JSON object. The reference has
no separate replace confirmation field. In the replace case both measured
requests explicitly select the native replacement effect.

The harness excludes fixture re-arming from the timed sample for append and
remove. It alternates append with cleanup for the append measurement, and
seeds the rule before each timed present-tag removal. Success/s here is the
single-client rate over timed operations. The operating system CPU deltas
cover the entire window, including fixture work. An untimed list verifies
the final state. The replace workload installs a rule that keeps the reference's
loopback management inbound reachable and repeatedly replaces the one-rule
state. The genuine transition from multiple rules is covered by the integration
test. The absent-tag workload sends a tag that never appears in the fixture.

The initial matrix uses five 500 ms windows per case. Focused repeats and the
tuned rerun use seven 700 ms windows. Final cases use one-second windows.
The paired rows below show PapilioVPN / PasarGuard, using the median of each
round metric. The fixture alternates product order between rounds.

| Phase | API | Operation | Rules | Clients | p50 µs | p95 µs | Success/s |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| matrix | grpc | append | 4 | 1 | 569.1 / 557.6 | 1185.8 / 1146.1 | 1494.3 / 1563.9 |
| matrix | grpc | remove | 5 | 1 | 383.1 / 442.4 | 751.1 / 807.2 | 2112.6 / 1928.6 |
| matrix | grpc | remove-absent | 4 | 1 | 403.4 / 420.8 | 761.6 / 805.4 | 2191.7 / 2133.1 |
| matrix | grpc | replace | 1 | 1 | 1060.5 / 824.5 | 3187.4 / 1526.4 | 827.1 / 1109.6 |
| matrix | http | append | 4 | 1 | 545.4 / 576.6 | 1345.2 / 1336.2 | 1506.8 / 1465.9 |
| matrix | http | remove | 5 | 1 | 315.6 / 418.6 | 657.0 / 811.9 | 2608.8 / 1981.3 |
| matrix | http | remove-absent | 4 | 1 | 382.7 / 378.4 | 971.2 / 910.3 | 2095.2 / 2073.6 |
| matrix | http | replace | 1 | 1 | 579.6 / 607.0 | 1340.2 / 1350.9 | 1392.8 / 1422.4 |
| repeat | grpc | append | 4 | 1 | 519.4 / 546.8 | 1333.8 / 1079.5 | 1329.7 / 1573.5 |
| repeat | grpc | remove-absent | 4 | 1 | 312.8 / 356.5 | 461.4 / 492.5 | 2951.7 / 2646.4 |
| repeat | grpc | replace | 1 | 1 | 849.4 / 794.6 | 1998.2 / 1980.8 | 1010.4 / 1015.7 |
| repeat | http | append | 4 | 1 | 422.2 / 444.6 | 858.6 / 792.5 | 1892.1 / 1947.9 |
| repeat | http | remove-absent | 4 | 1 | 288.4 / 321.7 | 365.4 / 398.4 | 3250.3 / 2923.7 |
| repeat | http | replace | 1 | 1 | 363.3 / 366.8 | 706.1 / 571.2 | 2369.4 / 2331.1 |
| tuned | grpc | append | 4 | 1 | 950.5 / 1007.3 | 3129.3 / 2769.3 | 856.6 / 844.8 |
| tuned | grpc | remove | 5 | 1 | 1020.5 / 1250.6 | 4505.7 / 5320.7 | 640.1 / 539.0 |
| tuned | grpc | replace | 1 | 1 | 1006.7 / 1057.0 | 3534.7 / 3806.3 | 749.0 / 650.1 |
| tuned | http | append | 4 | 1 | 742.1 / 764.4 | 2553.5 / 1445.4 | 1016.2 / 1131.2 |
| tuned | http | remove | 5 | 1 | 796.0 / 850.8 | 2667.1 / 3036.4 | 885.4 / 864.4 |
| tuned | http | replace | 1 | 1 | 880.9 / 764.7 | 2890.2 / 1843.2 | 902.0 / 1114.2 |
| final | grpc | append | 4 | 1 | 525.3 / 558.3 | 1139.4 / 1302.6 | 1520.6 / 1496.9 |
| final | grpc | remove-absent | 4 | 1 | 307.9 / 366.9 | 565.1 / 729.4 | 2827.9 / 2319.9 |
| final | http | append | 4 | 1 | 558.9 / 541.8 | 1475.9 / 1266.2 | 1436.4 / 1491.8 |
| final | http | remove-absent | 4 | 1 | 283.3 / 331.2 | 410.9 / 405.8 | 3201.3 / 2841.3 |
| final | http | replace | 1 | 1 | 329.9 / 368.6 | 470.8 / 553.4 | 2782.3 / 2417.0 |
| verified | http | append | 4 | 1 | 504.0 / 581.6 | 876.1 / 986.4 | 1791.4 / 1554.3 |

312 windows and 298,533 samples, 0 rejections.

### Deadline cost

Xray's pinned mutation handlers do not use the RPC context to interrupt
rule construction or publication. Forwarding the client deadline still
causes native gRPC to create a timer on each stream. The tuned implementation
keeps the caller's deadline, context cancellation and execution fence in
Node while avoiding that ineffective server timer.

A direct single-client Unix-socket experiment reused the same connection,
Xray process and compiled one-rule replacement, and alternated the two
deadline modes in seven 650 ms windows. CPU came from Xray's Linux process
ticks. The precompiled fixture isolates the native call.

| Deadline mode | p50 µs | p95 µs | Success/s | Xray CPU µs/request |
| --- | ---: | ---: | ---: | ---: |
| Forwarded to Xray | 157.7 | 718.1 | 3838.3 | 122.2 |
| Local to client | 144.2 | 475.2 | 4799.2 | 115.4 |

[Native deadline CSV](results/2026-11-23-routing-native-deadline.csv)

The tuned gRPC replace led in its focused repeat. The tuned gRPC append and
HTTP replace led in the longer one-second reruns. HTTP append led on all three
metrics in the seven-window verification with the final binary after trailing
in the preceding one-second run. HTTP absent-tag removal led in the focused
repeat and trailed by 5.1 µs on p95 in the final run. The variation across
windows limits conclusions about the paired wall-clock differences.

[Per-round CSV](results/2026-11-23-routing-rule-writes-rounds.csv)

Set `XRAY_BENCH_ROUTING_RULES=4`, `XRAY_BENCH_ROUTING_CLIENTS=1` and
`XRAY_BENCH_ROUTING_OPERATION=append|replace|remove|remove-absent`.
Run the harness once for each value. Set `XRAY_BENCH_TRANSPORTS` to
isolate a transport. The harness writes individual successful-request samples
next to the per-round CSV.
