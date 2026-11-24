# Xray traffic accounting

[Benchmark dashboard](../README.md)

[Counter reads](#xray-traffic-read-overhead) · [Named pair reset](#xray-named-traffic-pair-reset) ·
[Scoped reset](scope-reset.md#compact-native-acknowledgements)

## Xray traffic counters

The two nodes used the lifecycle fixture's pinned PasarGuard revision and Xray
v26.11.9 binary, TLS settings, Go toolchain, and CPU affinity. The fixture uses
one authenticated loopback HTTP proxy inbound and one direct outbound. It sends
real requests through the proxy under each of 1 or 512 accounts before measuring.
The client verifies both directions of every expected fixture entry. It runs
one outstanding request per client with 1 or 8 clients, 325 ms polling windows,
and three rounds with alternating server order. Results report the median of
per-round successful responses per second and of per-round latency percentiles.
Latencies are in milliseconds. HTTP uses the products' respective JSON and
protobuf representations and gRPC uses protobuf for both.

Both nodes use one Xray `QueryStats` scan per operation to fetch both directions
or the full scope. PapilioVPN narrows the search string for named resources and
checks the exact name after decoding. A list scan visits the engine's counters
once and groups directions by resource name. Go reuses the backing values of
the list entries during response encoding. The measurements use non-destructive
reads on populated counters. The PasarGuard inbound list additionally contains
counters from its private `API_INBOUND` management tunnel. Those list rows are
reported separately from the equivalent results. [PasarGuard API configuration](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/config.go#L950-L1010)

### Main sweep

Each cell shows PapilioVPN / PasarGuard. Both nodes reported zero
capacity rejections in the sweep. The summary counts better medians among the
ten equivalent cases per transport and account count.

| Accounts | Transport | Higher success/s | Lower p50 | Lower p95 |
| ---: | --- | ---: | ---: | ---: |
| 1 | HTTP | 10/10 | 10/10 | 9/10 |
| 1 | gRPC | 9/10 | 9/10 | 9/10 |
| 512 | HTTP | 3/10 | 3/10 | 3/10 |
| 512 | gRPC | 9/10 | 10/10 | 10/10 |

[HTTP rounds](results/2026-11-23-traffic-http.csv) · [gRPC rounds](results/2026-11-23-traffic-grpc.csv)

#### 1 account

| Transport | Scope | Selection | Clients | Success/s | p50 ms | p95 ms |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| HTTP | user | one | 1 | 1271 / 1138 | 0.704 / 0.790 | 1.471 / 1.633 |
| HTTP | user | one | 8 | 3969 / 3662 | 1.796 / 1.905 | 4.079 / 4.702 |
| HTTP | user | list | 1 | 1315 / 1227 | 0.661 / 0.714 | 1.488 / 1.797 |
| HTTP | user | list | 8 | 4022 / 3710 | 1.767 / 1.922 | 4.019 / 4.482 |
| HTTP | inbound | one | 1 | 1250 / 1083 | 0.685 / 0.786 | 1.656 / 2.139 |
| HTTP | inbound | one | 8 | 3955 / 3711 | 1.712 / 1.897 | 4.259 / 4.377 |
| HTTP | outbound | one | 1 | 1145 / 1143 | 0.707 / 0.726 | 2.223 / 1.725 |
| HTTP | outbound | one | 8 | 3974 / 3814 | 1.793 / 1.851 | 3.990 / 4.116 |
| HTTP | outbound | list | 1 | 1320 / 1162 | 0.645 / 0.756 | 1.604 / 1.833 |
| HTTP | outbound | list | 8 | 3909 / 3534 | 1.804 / 1.949 | 3.982 / 4.458 |
| gRPC | user | one | 1 | 529 / 611 | 1.534 / 1.361 | 4.014 / 3.540 |
| gRPC | user | one | 8 | 2697 / 2215 | 2.683 / 3.149 | 5.410 / 6.797 |
| gRPC | user | list | 1 | 605 / 490 | 1.287 / 1.419 | 3.927 / 4.590 |
| gRPC | user | list | 8 | 2673 / 2132 | 2.664 / 3.265 | 5.194 / 7.986 |
| gRPC | inbound | one | 1 | 802 / 594 | 1.040 / 1.351 | 3.055 / 3.792 |
| gRPC | inbound | one | 8 | 2745 / 2396 | 2.559 / 2.905 | 5.374 / 6.201 |
| gRPC | outbound | one | 1 | 632 / 492 | 1.204 / 1.517 | 3.950 / 4.232 |
| gRPC | outbound | one | 8 | 2769 / 2187 | 2.503 / 3.128 | 5.032 / 6.455 |
| gRPC | outbound | list | 1 | 642 / 472 | 1.206 / 1.607 | 3.320 / 4.323 |
| gRPC | outbound | list | 8 | 2510 / 2418 | 2.737 / 2.864 | 5.914 / 6.089 |

#### 512 accounts

| Transport | Scope | Selection | Clients | Success/s | p50 ms | p95 ms |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| HTTP | user | one | 1 | 774 / 737 | 1.138 / 1.208 | 2.747 / 3.128 |
| HTTP | user | one | 8 | 2777 / 2399 | 2.660 / 3.069 | 5.322 / 5.783 |
| HTTP | user | list | 1 | 159 / 208 | 5.959 / 4.558 | 10.115 / 7.293 |
| HTTP | user | list | 8 | 339 / 306 | 22.961 / 26.252 | 34.512 / 37.011 |
| HTTP | inbound | one | 1 | 503 / 663 | 1.512 / 1.263 | 4.304 / 3.327 |
| HTTP | inbound | one | 8 | 1743 / 2093 | 4.045 / 3.533 | 8.257 / 7.077 |
| HTTP | outbound | one | 1 | 479 / 819 | 1.489 / 1.086 | 5.118 / 2.268 |
| HTTP | outbound | one | 8 | 2271 / 2422 | 3.221 / 2.975 | 5.995 / 5.936 |
| HTTP | outbound | list | 1 | 642 / 688 | 1.264 / 1.215 | 3.421 / 3.108 |
| HTTP | outbound | list | 8 | 2296 / 2420 | 3.217 / 3.074 | 6.020 / 5.472 |
| gRPC | user | one | 1 | 1011 / 452 | 0.900 / 1.525 | 2.101 / 5.162 |
| gRPC | user | one | 8 | 2739 / 2347 | 2.700 / 3.144 | 4.747 / 6.016 |
| gRPC | user | list | 1 | 294 / 172 | 3.155 / 5.452 | 5.546 / 8.776 |
| gRPC | user | list | 8 | 427 / 232 | 18.149 / 33.962 | 29.410 / 61.625 |
| gRPC | inbound | one | 1 | 581 / 614 | 1.324 / 1.405 | 3.356 / 3.440 |
| gRPC | inbound | one | 8 | 2874 / 1946 | 2.518 / 3.783 | 4.942 / 6.171 |
| gRPC | outbound | one | 1 | 707 / 565 | 1.249 / 1.494 | 3.170 / 3.888 |
| gRPC | outbound | one | 8 | 2434 / 2068 | 2.941 / 3.649 | 5.410 / 7.116 |
| gRPC | outbound | list | 1 | 928 / 716 | 0.963 / 1.233 | 2.423 / 2.900 |
| gRPC | outbound | list | 8 | 3041 / 2689 | 2.360 / 2.642 | 4.699 / 5.500 |

#### Inbound lists

| Accounts | Transport | Scope | Selection | Clients | Success/s | p50 ms | p95 ms |
| ---: | --- | --- | --- | ---: | ---: | ---: | ---: |
| 1 | HTTP | inbound | list | 1 | 1251 / 1099 | 0.672 / 0.787 | 1.817 / 1.822 |
| 1 | HTTP | inbound | list | 8 | 3940 / 3702 | 1.770 / 1.946 | 3.992 / 4.271 |
| 1 | gRPC | inbound | list | 1 | 612 / 621 | 1.197 / 1.252 | 4.262 / 3.819 |
| 1 | gRPC | inbound | list | 8 | 2661 / 2455 | 2.742 / 2.891 | 5.094 / 6.078 |
| 512 | HTTP | inbound | list | 1 | 491 / 805 | 1.555 / 1.099 | 4.048 / 2.264 |
| 512 | HTTP | inbound | list | 8 | 2096 / 2352 | 3.515 / 3.048 | 6.410 / 6.249 |
| 512 | gRPC | inbound | list | 1 | 824 / 690 | 1.114 / 1.263 | 2.460 / 2.672 |
| 512 | gRPC | inbound | list | 8 | 2622 / 1919 | 2.809 / 3.642 | 5.166 / 8.416 |

### Repeated workloads

The repeat runs used the final binaries and the same fixture. With 512 accounts,
HTTP results varied across runs. In the repeat, PapilioVPN had higher throughput
for eight-client user lists, inbound reads, and outbound reads and lists. Its
single-client outbound list throughput remained lower.

The 512-account HTTP repeat ran all six operations for five 500 ms rounds. The
focused single-account runs used seven 600 ms rounds with one client. The
initial HTTP row used the earlier response encoder that allocated new values
for each entry.

| Workload | Success/s | p50 ms | p95 ms |
| --- | ---: | ---: | ---: |
| Initial HTTP user list, one account and one client | 1010 / 1243 | 0.812 / 0.682 | 2.759 / 1.761 |
| Focused HTTP user list, one account and one client | 1124 / 1110 | 0.743 / 0.759 | 1.924 / 1.948 |
| Focused gRPC user read, one account and one client | 1237 / 1074 | 0.718 / 0.799 | 1.656 / 1.904 |
| HTTP user list, 512 accounts and one client | 266 / 254 | 3.547 / 3.683 | 5.563 / 5.844 |
| HTTP user list, 512 accounts and eight clients | 414 / 355 | 18.433 / 22.562 | 27.555 / 31.745 |
| HTTP inbound read, 512 accounts and one client | 1086 / 1040 | 0.817 / 0.880 | 1.825 / 1.779 |
| HTTP inbound read, 512 accounts and eight clients | 3330 / 2706 | 2.217 / 2.640 | 4.273 / 5.346 |
| HTTP outbound read, 512 accounts and one client | 1126 / 995 | 0.773 / 0.899 | 1.817 / 2.011 |
| HTTP outbound read, 512 accounts and eight clients | 3297 / 3095 | 2.205 / 2.362 | 4.431 / 4.674 |
| HTTP outbound list, 512 accounts and one client | 1027 / 1068 | 0.836 / 0.824 | 1.969 / 2.037 |
| HTTP outbound list, 512 accounts and eight clients | 3254 / 3117 | 2.225 / 2.308 | 4.377 / 4.755 |

[Initial HTTP sweep](results/2026-11-23-traffic-initial-http.csv) · [Focused HTTP rounds](results/2026-11-23-traffic-focus-http.csv) · [512-account HTTP repeat](results/2026-11-23-traffic-repeat-http.csv) · [Focused gRPC rounds](results/2026-11-23-traffic-repeat-grpc.csv)

### Reproduce the traffic measurements

The tables above used the runner at [commit d061a15](https://github.com/PapilioVPN/node/tree/d061a15/benchmarks/xray).
Build the two nodes, client and descriptor using the lifecycle steps. Use a
separate output directory for each row of settings.

The current traffic runner starts and seeds both Xray instances once per run
on separate loopback proxy ports. Both servers and their Xray children share
the configured server CPU. Each measured window exercises one target, then
the same case and client count runs on the other target. The order alternates
by round. Both instances are stopped after the final round.

| Workload | Accounts | Clients | Window | Rounds | Transports | Case |
| --- | ---: | --- | --- | ---: | --- | --- |
| Main sweep, small fixture | 1 | 1,8 | 325ms | 3 | http,grpc | all |
| Main sweep, large fixture | 512 | 1,8 | 325ms | 3 | http,grpc | all |
| Focused HTTP user list | 1 | 1 | 600ms | 7 | http | user/list |
| Focused gRPC user read | 1 | 1 | 600ms | 7 | grpc | user/one |
| Large HTTP repeat | 512 | 1,8 | 500ms | 5 | http | all |

Set `XRAY_BENCH_TRAFFIC_NAMES`, `XRAY_BENCH_TRAFFIC_CLIENTS`,
`XRAY_BENCH_TRAFFIC_WINDOW`, `XRAY_BENCH_ROUNDS` and `XRAY_BENCH_TRANSPORTS`
to the chosen row. For a focused workload, set `XRAY_BENCH_TRAFFIC_CASE` to the
listed case. Leave it unset for all cases. The runner needs Python 3 to
construct the proxy fixture.

## Xray traffic read delivery

Traffic reads use one native Xray query per request. The traffic service
applies independent concurrency limits and a one-second local deadline for
single and collection reads. Native code projects the counters into the
typed application result while preserving missing directions and exact names.

The final implementation ran in three 250 ms rounds on the paired runner.
Both nodes and both Xray children shared server CPU 2. The client ran on
CPU 4. The build used Go 1.27.3, Xray v26.11.9, and the pinned PasarGuard
revision from the lifecycle fixture. Each row summarizes ten equivalent
workloads across 1 and 8 clients. The additional inbound-list rows include
PasarGuard's private management counters and are available in the CSV.

| Accounts | Transport | All three better | Higher success/s | Lower p50 | Lower p95 |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | gRPC | 8/10 | 9/10 | 9/10 | 8/10 |
| 1 | HTTP | 5/10 | 6/10 | 6/10 | 7/10 |
| 512 | gRPC | 8/10 | 9/10 | 10/10 | 8/10 |
| 512 | HTTP | 4/10 | 7/10 | 6/10 | 4/10 |
| **Total** | | **25/40** | **31/40** | **31/40** | **27/40** |

PapilioVPN had higher successful reads per second and lower p50 and p95
together in 25 of 40 equivalent cases. Fifteen cases trailed on at least
one metric, including 13 cases with higher p95.
The one-account HTTP user list with one client returned 1100 versus 1130
successful reads/s. Its p95 was 1.666 ms versus 1.850 ms.
Both targets returned zero capacity rejections during the sweep.

[HTTP rounds](results/2026-11-23-traffic-delivery-http.csv) · [gRPC rounds](results/2026-11-23-traffic-delivery-grpc.csv)

Use the current traffic runner with `XRAY_BENCH_TRAFFIC_NAMES=1` or `512`,
`XRAY_BENCH_TRAFFIC_CLIENTS=1,8`, `XRAY_BENCH_TRAFFIC_WINDOW=250ms`,
and `XRAY_BENCH_ROUNDS=3`.

## Concurrent Xray traffic lists

Overlapping requests for the same execution and scope may share a new
native read. A request waits for a sample initiated after its own admission.
When a reader cancels, other healthy readers can retry under their own
deadlines. A controlled test uses two native reads for one initial request
and eight queued requests. Single-resource reads keep their native path.

The focused HTTP benchmark used the paired runner with five 450 ms
rounds on CPU 2 for both nodes and engines. The client used CPU 4.
Each pair shows successful reads per second and p95 latency in milliseconds.
Reference results come from the same run as the corresponding Node value.

| Accounts | Clients | Node before | PasarGuard before | Node with sharing | PasarGuard with sharing |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 1 | 1240 / 1.630 | 1230 / 1.664 | 1339 / 1.544 | 1148 / 1.884 |
| 1 | 8 | 4117 / 3.822 | 3808 / 4.353 | 4648 / 3.155 | 3838 / 4.076 |
| 512 | 1 | 267 / 5.287 | 255 / 5.660 | 254 / 6.361 | 235 / 6.209 |
| 512 | 8 | 428 / 26.288 | 375 / 30.720 | 697 / 16.666 | 366 / 30.464 |

[Before](results/2026-11-23-traffic-list-sharing-before.csv) · [After](results/2026-11-23-traffic-list-sharing-after.csv)

The full sweep used three 250 ms rounds for the same 40 equivalent
workloads. A workload counts as a win only when Node has higher success/s,
lower p50 and lower p95. No capacity rejections occurred. The CSV also
includes inbound-list rows, where the reference returns an additional
private management counter.

| Accounts | Transport | All three better | Higher success/s | Lower p50 | Lower p95 |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | gRPC | 6/10 | 6/10 | 7/10 | 8/10 |
| 1 | HTTP | 7/10 | 8/10 | 7/10 | 10/10 |
| 512 | gRPC | 8/10 | 10/10 | 10/10 | 8/10 |
| 512 | HTTP | 7/10 | 7/10 | 9/10 | 7/10 |
| **Total** | | **28/40** | **31/40** | **33/40** | **33/40** |

Node trailed on at least one metric in 12 of 40 equivalent workloads.

[HTTP rounds](results/2026-11-23-traffic-list-sharing-matrix-http.csv) · [gRPC rounds](results/2026-11-23-traffic-list-sharing-matrix-grpc.csv)

Use the same paired runner with the delivery settings for the full sweep.
For the focused comparison, set `XRAY_BENCH_TRAFFIC_CASE=user/list`,
`XRAY_BENCH_TRANSPORTS=http`, `XRAY_BENCH_TRAFFIC_WINDOW=450ms`,
and `XRAY_BENCH_ROUNDS=5`. Run once for each of 1 and 512 accounts.

## Xray traffic read overhead

An uncontended list previously allocated a group, an initial sample, and a
completion channel. The initial sample now lives in its group. Readers
allocate a completion channel when they wait on an active sample. The
native named lookup also reuses its exact query prefix when identifying
directions.

The microbenchmark isolates coordination with a single caller and a fixed
in-memory result. The median of five 150 ms runs on CPU 2 was 413 ns with
176 bytes and three allocations per operation before the change. The new
path used 265 ns, 80 bytes and one allocation per operation.

```sh
GOMAXPROCS=1 taskset -c 2 go test ./internal/engines/xray/stats/native \
  -run '^$' -bench '^BenchmarkUncontendedTrafficList$' -benchtime=150ms -count=5
```

The paired public runs used seven 500 ms windows for each operation with
both products and engines on server CPU 2 and the client on CPU 4. Each
cell shows successful responses per second followed by p95 in milliseconds.
The reference was measured again beside each Node build.

| Operation | Accounts | Clients | Node before | PasarGuard before | Node after | PasarGuard after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| gRPC named inbound | 1 | 1 | 935 / 2.559 | 948 / 2.205 | 1095 / 1.873 | 1018 / 1.928 |
| gRPC named inbound | 1 | 8 | 3594 / 4.452 | 3338 / 4.672 | 4088 / 3.831 | 3559 / 4.241 |
| gRPC named inbound | 512 | 1 | 886 / 2.471 | 761 / 2.964 | 554 / 3.745 | 560 / 3.786 |
| gRPC named inbound | 512 | 8 | 2923 / 4.710 | 2769 / 5.196 | 2181 / 5.908 | 1924 / 7.035 |
| HTTP outbound list | 512 | 1 | 871 / 2.824 | 859 / 2.520 | 831 / 2.894 | 731 / 3.192 |

The five focused p95s were lower than the reference after the change.
The 512-account named inbound had lower throughput and higher p50 at one
client. The reference timings also shifted between runs. The microbenchmark
measures the amount of execution saved by the coordination change.

[Focused before](results/2026-11-23-traffic-lean-focused-before.csv) ·
[Focused after](results/2026-11-23-traffic-lean-focused-after.csv)

The full sweep used the delivery settings with three 250 ms windows.
A win requires higher success/s, lower p50 and lower p95 in the same
workload. All 40 comparable workloads completed without capacity rejections.

| Accounts | Transport | All three better | Higher success/s | Lower p50 | Lower p95 |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | gRPC | 7/10 | 8/10 | 9/10 | 8/10 |
| 1 | HTTP | 8/10 | 8/10 | 8/10 | 9/10 |
| 512 | gRPC | 8/10 | 9/10 | 9/10 | 8/10 |
| 512 | HTTP | 9/10 | 9/10 | 9/10 | 9/10 |
| **Total** | | **32/40** | **34/40** | **35/40** | **34/40** |

Node trailed on at least one metric in 8 of 40 equivalent workloads.

[HTTP rounds](results/2026-11-23-traffic-lean-matrix-http.csv) ·
[gRPC rounds](results/2026-11-23-traffic-lean-matrix-grpc.csv)

## Xray exact traffic counter reset

Node sends one targeted native `GetStats(reset=true)` for a selected account
or tag and direction. The pinned PasarGuard reset uses a substring
`QueryStats` that can clear both directions and other matching counters. The
benchmark uses the exact Xray call as the baseline. [PasarGuard reset implementation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/api/stats.go#L41-L49) · [Pinned Xray reset](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/stats/command/command.go#L30-L46)

The fixture creates 1 or 512 registered user downlink counters with local
HTTP proxy traffic and clears one nonzero counter before timing. Every timed
operation resets the same registered zero counter without foreground traffic.
Each path uses the same Xray instance. The native path uses a Unix socket.
The public paths add TLS, loopback transport, API-key validation and wire
encoding. All processes use logical CPU 2, `GOMAXPROCS=1` and pinned Xray
v26.11.9. Node and the fixture client were built with Go 1.27.3 on Linux
6.17, amd64, AMD Ryzen 7 3750H.

Each cell shows median p50 µs / p95 µs / successful requests per second over
five 300 ms rounds. No requests were rejected.

| Registered counters | Clients | Native Xray | Node HTTP | Node gRPC |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1 | 127 / 215 / 6813 | 331 / 696 / 2552 | 415 / 747 / 2225 |
| 1 | 8 | 392 / 928 / 15262 | 1571 / 3505 / 4367 | 1172 / 3084 / 5784 |
| 512 | 1 | 155 / 359 / 5034 | 688 / 1270 / 1187 | 483 / 1160 / 1612 |
| 512 | 8 | 403 / 825 / 16000 | 1776 / 3577 / 3973 | 1391 / 3280 / 5125 |

[Per-round CSV](results/2026-11-23-traffic-reset.csv) · [Benchmark source](reset_test.go)

Set `XRAY_BINARY` to the absolute path of the pinned Xray executable.

```sh
work=$(mktemp -d)
CGO_ENABLED=0 go test -c -trimpath -o "$work/bench" ./benchmarks/xray/traffic
GOMAXPROCS=1 XRAY_BENCH_TRAFFIC_RESET="$XRAY_BINARY"   taskset -c "${XRAY_BENCH_SERVER_CPU:-2}" "$work/bench" -test.run '^TestMeasureTrafficReset$' -test.v
```

## Xray named traffic pair reset

Node resets the uplink and downlink of one account or tag through two
targeted native calls on the same execution. It keeps each directional
acknowledgement, stops after a non-missing failure, and skips the second
call if the execution changes. The pinned PasarGuard uses one substring
`QueryStats(reset=true)` call. The benchmark used unambiguous account
names and verified that each response contained the two selected directions.
It compares outcomes for that fixture. PasarGuard's substring selection and
failure reporting have different guarantees. [Native GetStats and QueryStats](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/stats/command/command.go#L30-L46) · [PasarGuard named reset](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/api/stats.go#L104-L114)

Both products used Xray v26.11.9 with 1 or 512 accounts and the same
local HTTP proxy traffic fixture. The benchmark checked a nonzero seeded
pair on each target before timing repeated resets of that registered zero
pair. HTTP used JSON on Node and protobuf on PasarGuard. Both transports
used TLS and API-key checks with reused connections. The binaries used Go
1.27.3, `CGO_ENABLED=0`, and `-trimpath`. Servers and Xray children were
pinned to CPU 2, and the client to CPU 6 on the same Linux 6.17 Ryzen 7
3750H host. All used `GOMAXPROCS=1`.

Cells give Node / PasarGuard. Values are medians of five 450 ms rounds,
with alternating product and concurrency order. There were zero rejected
requests.

| Accounts | Transport | Clients | p50 µs | p95 µs | Successful requests/s |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | HTTP | 1 | 565 / 383 | 1110 / 670 | 1548 / 2331 |
| 1 | HTTP | 8 | 1620 / 1067 | 3343 / 2702 | 4416 / 6500 |
| 1 | gRPC | 1 | 833 / 503 | 2106 / 968 | 1015 / 1723 |
| 1 | gRPC | 8 | 1747 / 1503 | 3412 / 3239 | 4187 / 4728 |
| 512 | HTTP | 1 | 597 / 595 | 1199 / 1016 | 1437 / 1563 |
| 512 | HTTP | 8 | 1772 / 1959 | 3582 / 5318 | 4150 / 3378 |
| 512 | gRPC | 1 | 643 / 621 | 1338 / 963 | 1310 / 1508 |
| 512 | gRPC | 8 | 1764 / 1859 | 3834 / 3838 | 3968 / 3958 |

Node led on all three metrics with 512 accounts and eight clients on
both transports. The gRPC p95 gap in that case was under 1%. PasarGuard
led in the remaining six cases.

A separate one-client diagnostic called both strategies over the same
native Unix socket. The median per-round p50 was 306 / 146 µs for 1 account and
758 / 577 µs for 512 accounts for two exact calls versus one substring call.
The diagnostic used five 250 ms windows with alternating method order,
Go 1.26.11, and CPU 2. This measures the effect of the two native requests
before the public HTTP and gRPC layers. The substring call can also reset
an unrelated counter whose key contains the pattern. [Native diagnostic data](results/2026-11-23-traffic-reset-pair-native.csv)

[Paired per-round results](results/2026-11-23-traffic-reset-pair.csv)

Build both nodes, the benchmark client, and the reference descriptor using
the [lifecycle setup](../instances/README.md#reproduce). Run the paired fixture for each size.

```sh
for accounts in 1 512; do
  XRAY_BENCH_TRAFFIC_NAMES="$accounts" XRAY_BENCH_TRAFFIC_CASE=reset-pair/one \
  XRAY_BENCH_TRAFFIC_CLIENTS=1,8 XRAY_BENCH_TRAFFIC_WINDOW=450ms \
  XRAY_BENCH_CLIENT_CPU=6 XRAY_BENCH_ROUNDS=5 \
    bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
      "$work/bench" "$work/reference.pb" "$work/reset-pair-$accounts"
done
```

Run the native diagnostic with the pinned Xray binary in `XRAY_BINARY`.

```sh
GOMAXPROCS=1 XRAY_BENCH_NATIVE_PAIR_RESET="$XRAY_BINARY" \
  taskset -c 2 go test ./benchmarks/xray/traffic -run '^TestMeasureNativePairReset$' -count=1 -v
```


### One-call pair reset with the bundled Xray

With `xray.native_scope_reset: true`, `node-xray` handles both exact
directions in one RPC. It returns the previous signed values separately
and preserves the distinction between absent and zero. A lost reply
leaves both directions unconfirmed. The stock executable retains two
targeted calls. If an older companion returns `Unimplemented` before
dispatch, Node uses the two-call path on the same active connection.

The stock baseline and first paired comparison used three 500 ms
windows. The confirmation used five 650 ms windows per case. Node and
PasarGuard used the same Xray v26.11.9 source pin, with the bundled binary
on Node and the official release binary on PasarGuard in one-call runs.
Both nodes and the driver used Go 1.27.4. The server and child used CPU 2
and the driver CPU 6. The seeded counters and the exact directional
response checks match the earlier fixture. There were zero rejections.
Cells show Node / PasarGuard.

| Accounts | Transport | Clients | p50 µs | p95 µs | Success/s |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | http | 1 | 345.0 / 347.4 | 610.6 / 570.6 | 2496.2 / 2540.9 |
| 1 | http | 8 | 984.6 / 1157.2 | 2544.8 / 2799.3 | 7164.0 / 6090.6 |
| 1 | grpc | 1 | 362.1 / 417.6 | 561.4 / 672.0 | 2460.4 / 2148.5 |
| 1 | grpc | 8 | 1139.9 / 1193.7 | 2501.6 / 2677.9 | 6365.3 / 5947.7 |
| 512 | http | 1 | 327.1 / 402.3 | 533.7 / 705.2 | 2687.2 / 2131.3 |
| 512 | http | 8 | 971.6 / 1395.4 | 2557.3 / 3124.1 | 6930.0 / 5118.3 |
| 512 | grpc | 1 | 370.0 / 458.0 | 577.7 / 739.9 | 2506.4 / 1923.6 |
| 512 | grpc | 8 | 1139.8 / 1594.6 | 2500.6 / 3206.1 | 6315.7 / 4607.7 |

The first comparison led on all three metrics in eight cases. The
confirmation led in seven. HTTP at one account and one client had a
similar median and a higher p95 for Node. This fixture compares the same
two counters with unambiguous names. Node uses exact keys and PasarGuard
uses substring selection.

Reproduce with `XRAY_BENCH_TRAFFIC_CASE=reset-pair/one`,
`XRAY_BENCH_TRAFFIC_NAMES=1` or `512`, `XRAY_BENCH_NATIVE_SCOPE_RESET=1`,
`XRAY_BENCH_REFERENCE_XRAY="$work/xray/xray"`, and
`XRAY_BENCH_TRAFFIC_CLIENTS=1,8`. Use the bundled binary as the third
argument to [the runner](../run.sh).
[Per-round data](results/2026-11-23-traffic-reset-pair-bundled.csv)

## Xray scoped traffic reset

The scoped API selects exact traffic counters and reports the effect of
each native reset. The benchmark covers 2, 100 and 1000 counters. The pinned
PasarGuard uses a substring batch operation and was faster on the shared
fixture. Optional bounded concurrency improved Node's p50 in the focused
repeat. [Contract, reproduction and per-round data](scope-reset.md)

[Runtime concurrency-gate comparison](../runtime/README.md#runtime-statistics-with-the-concurrency-gates)
