# Benchmarks

## Host system statistics

The 2026-11-23 run compared the public `GetSystemStats` paths in PapilioVPN Node `9e4ce12` and PasarGuard Node `0.5.7` at `739eab9`. Both p50 and p95 were lower for PapilioVPN Node in the four tested configurations. The figures are in milliseconds. [PapilioVPN revision](https://github.com/PapilioVPN/node/commit/9e4ce12e3f1ba6f7d1dd6309fd45d77852241631) · [PasarGuard revision](https://github.com/PasarGuard/node/commit/739eab94a9343d9f0035baf7162796844109f4c4)

| Transport | Concurrent clients | PapilioVPN p50 | PapilioVPN p95 | PasarGuard p50 | PasarGuard p95 |
| --- | ---: | ---: | ---: | ---: | ---: |
| HTTP | 1 | 0.30 | 1.31 | 0.77 | 1.84 |
| HTTP | 8 | 1.17 | 3.45 | 2.82 | 5.50 |
| gRPC | 1 | 0.31 | 1.26 | 0.89 | 2.05 |
| gRPC | 8 | 1.50 | 4.10 | 2.96 | 6.41 |

[Per-round measurements](host/results/2026-11-23-system-stats.csv) include sample counts, p50, p95, p99 and completed polls per second. The table uses the median of seven per-round percentiles. Latencies in the CSV are in microseconds.

### Workload and limits

- The client ran on Linux 6.17, amd64, on an AMD Ryzen 7 3750H. The servers and client were built with Go 1.27.3, `CGO_ENABLED=0` and `-trimpath`. Processes used `GOMAXPROCS=1`. Both servers were pinned to logical CPU 2 and the client to CPU 4 on a different physical core.
- The fixture used isolated Linux PID and network namespaces with loopback. The client reused TLS 1.3 connections, supplied an API key and decoded each response. The HTTP test used HTTP/1.1 for both servers. Each PasarGuard listener ran with an idle Xray 26.3.27 backend. Request logs were discarded.
- The client called `/v1/host/stats` and `papiliovpn.node.v1.HostService/GetSystemStats` on PapilioVPN Node. It called `/stats/system` and `service.NodeService/GetSystemStats` on PasarGuard. Each transport and concurrency level ran in seven rounds of 350 ms, with rotated workload order. The benchmark confirmed that both implementations reported the same total RAM before timing.
- PapilioVPN Node returns Linux boot uptime and filesystem capacity, reads RAM during each call and publishes CPU samples about once a second. PasarGuard returns backend uptime and bandwidth figures, reads RAM on its collection interval and includes I/O wait and steal in its CPU busy figure. The comparison measures the actual latency of two public monitoring APIs with different response fields and freshness. [PapilioVPN protobuf contract](../internal/host/bridge/grpc/proto/v1/host.proto) · [PasarGuard collection and response](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/controller.go#L363-L426) · [PasarGuard CPU calculation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/pkg/sysstats/sys.go)

## Native Host reads

Five alternating runs on the same pinned core measured the effect of changing how Node reads `/proc/meminfo`. Revisions were `8170432` before and `9e4ce12` after. The table gives the median of Go's per-run mean timings. Aggregate OS reads cover uptime, memory and storage through the Host service before the CPU sampler starts.

| Operation | Before | After | Bytes per operation | Allocations per operation |
| --- | ---: | ---: | ---: | ---: |
| Live RAM | 34.304 µs | 20.365 µs | 6,808 → 32 | 13 → 2 |
| Aggregate OS reads | 38.547 µs | 24.039 µs | 6,968 → 192 | 18 → 7 |

[Per-run native measurements](host/results/2026-11-23-native.csv) include iteration counts and allocations.

Run the native benchmarks from the repository root on Linux.

```sh
GOMAXPROCS=1 go test ./benchmarks/host -run '^$' -bench . -benchmem -benchtime=250ms -count=5
```

## Xray instance lifecycle

On 2026-11-23, PapilioVPN Node had lower p50 and p95 latencies in all six tested transport and operation pairs. The table shows the median of five per-round percentiles, in milliseconds. Each round includes 30 cold starts, 360 status reads while ready, and 30 joined stops per server.

| Transport | Operation | PapilioVPN p50 | PapilioVPN p95 | PasarGuard p50 | PasarGuard p95 |
| --- | --- | ---: | ---: | ---: | ---: |
| HTTP | Start | 29.768 | 42.699 | 1028.732 | 1044.833 |
| HTTP | Status | 0.217 | 1.244 | 0.324 | 2.879 |
| HTTP | Stop | 3.270 | 5.641 | 4.520 | 7.577 |
| gRPC | Start | 31.724 | 39.090 | 1029.851 | 1042.816 |
| gRPC | Status | 0.290 | 1.547 | 0.531 | 2.236 |
| gRPC | Stop | 3.694 | 5.474 | 5.244 | 9.254 |

[HTTP per-round results](xray/results/2026-11-23-http.csv) · [gRPC per-round results](xray/results/2026-11-23-grpc.csv)

### Workload and limits

- Both Node binaries and the benchmark client were built with Go 1.27.3, `CGO_ENABLED=0`, and `-trimpath`. The host ran Linux 6.17 on an AMD Ryzen 7 3750H. Both servers and their children used `GOMAXPROCS=1` and were pinned to CPU 2. The client used CPU 4 on a different physical core. The fixture used isolated Linux PID and network namespaces with loopback.
- Both servers ran the same Xray v26.11.9 executable and started from the same JSON with one idle loopback SOCKS inbound, one direct outbound, and zero users. Each product supplied its own native management configuration. The public client used a single persistent TLS 1.3 connection per server and transport, provided an API key, and decoded each response. HTTP used HTTP/1.1.
- `Start` measures a cold start from no running process to successful native `GetSysStats`. The client sends the configuration to PasarGuard and the instance ID to PapilioVPN, where the configuration is already on disk. PasarGuard's native readiness loop probes once per second. PapilioVPN uses a private Unix socket with short reconnect delays. [PasarGuard readiness loop](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/jobs.go#L43-L67)
- `Status` compares reads of the running lifecycle state. PasarGuard also returns version fields. `Stop` measures from the request until a successful response, then the client independently checks that Linux lists no remaining child process. PapilioVPN sends SIGTERM with a bounded SIGKILL fallback. PasarGuard sends SIGKILL first. The workload contains no active client traffic. The reference version is PasarGuard 0.5.7 at `739eab94a9343d9f0035baf7162796844109f4c4`. [PasarGuard stop implementation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/core.go#L308-L352)

### Reproduce

Use Linux with unprivileged user namespaces, Go 1.27.3, protoc, OpenSSL, curl, unzip, and iproute2. Run from the repository root. Set `XRAY_BENCH_SERVER_CPU` and `XRAY_BENCH_CLIENT_CPU` to distinct physical cores if CPUs 2 and 4 are unavailable.

```bash
work=$(mktemp -d)
ref=739eab94a9343d9f0035baf7162796844109f4c4
mkdir "$work/reference"
curl -fsSL "https://codeload.github.com/PasarGuard/node/tar.gz/$ref" |
  tar -xz --strip-components=1 -C "$work/reference"
curl -fsSL https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-linux-64.zip \
  -o "$work/xray.zip"
printf '%s  %s\n' b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82 "$work/xray.zip" | sha256sum -c
unzip -q "$work/xray.zip" -d "$work/xray"
chmod +x "$work/xray/xray"
(cd "$work/reference" && GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/pasarguard" ./cmd/node)
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/node" ./cmd/node
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/bench" ./benchmarks/xray
protoc -I "$work/reference" --descriptor_set_out="$work/reference.pb" "$work/reference/common/service.proto"
bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
  "$work/bench" "$work/reference.pb" "$work/results"
```

## Xray config validation

The table reports the median of five per-run mean timings on 2026-11-23, in milliseconds. The reference is direct `xray run -test` on the same Xray v26.11.9 binary, JSON, and management overlay. The pinned PasarGuard revision has no equivalent standalone management operation. [PasarGuard gRPC contract](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto#L218-L243) · [PasarGuard HTTP routes](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/rest/service.go#L25-L65)

| Configuration | Direct Xray | Node HTTP | Node gRPC |
| --- | ---: | ---: | ---: |
| Valid | 22.72 | 23.56 | 23.59 |
| Rejected protocol | 20.09 | 21.88 | 23.97 |

[Per-run measurements](xray/results/2026-11-23-config-validation.csv) include iteration counts and Go allocations in the benchmark process. The fixture used Linux amd64, Go 1.26.11, an AMD Ryzen 7 3750H, and CPU 2 with `GOMAXPROCS=1`. HTTP and gRPC used persistent loopback connections without TLS. The direct CLI path reused the prepared overlay. The API paths include request decoding, configuration preparation, native execution, and response decoding. Per-run timing ranges overlap, so the figures describe end-to-end cost for these two small files.

Run from the repository root with the Xray v26.11.9 executable and adjust the CPU affinity to match your machine.

```bash
NODE_TEST_XRAY=/absolute/path/to/xray GOMAXPROCS=1 taskset -c 2 \
  go test ./internal/engines/xray/configs/integration -run '^$' \
  -bench '^BenchmarkValidateConfig$' -benchmem -benchtime=300ms -count=5
```

## Xray lifecycle after startup error reporting

The rerun uses the Xray lifecycle fixture above with five rounds, six cold starts and joined stops per round, and 12 status reads per start. The table shows the median of the per-round p50s in milliseconds for successful calls. The occupied-port and readiness-timeout cases were verified separately in integration tests.

| Transport | Operation | PapilioVPN p50 | PasarGuard p50 |
| --- | --- | ---: | ---: |
| HTTP | Start | 37.776 | 1034.452 |
| HTTP | Status | 0.341 | 0.459 |
| HTTP | Stop | 4.480 | 5.510 |
| gRPC | Start | 32.929 | 1028.577 |
| gRPC | Status | 0.390 | 0.450 |
| gRPC | Stop | 4.008 | 4.961 |

Use `XRAY_BENCH_CYCLES=6` with the reproduction command in the lifecycle section. [HTTP per-round results](xray/results/2026-11-23-startup-details-http.csv) · [gRPC per-round results](xray/results/2026-11-23-startup-details-grpc.csv)

## Xray runtime statistics before the rewrite

This run compares PapilioVPN `GetRuntimeStats` and PasarGuard `GetBackendStats`. Both read the same ten fields through one native `GetSysStats` call on Xray v26.11.9. The table shows the median of seven per-round percentiles in milliseconds.

| Transport | Concurrent clients | PapilioVPN p50 | PapilioVPN p95 | PasarGuard p50 | PasarGuard p95 |
| --- | ---: | ---: | ---: | ---: | ---: |
| HTTP | 1 | 0.825 | 2.286 | 0.785 | 2.283 |
| HTTP | 8 | 1.917 | 4.454 | 2.026 | 4.724 |
| gRPC | 1 | 1.320 | 4.016 | 1.224 | 3.542 |
| gRPC | 8 | 2.447 | 4.748 | 2.647 | 5.531 |

PapilioVPN had lower p50 and p95 with eight clients on both transports in this run. PasarGuard had lower single-client p50 on both transports. The per-round distributions overlap. The test compares the public HTTP APIs using their respective JSON and protobuf encodings.

In a local diagnostic with 4,000 reads per path on one active Unix connection, median latency was 150 µs for a direct native RPC and 164 µs through the Node statistics service. This isolates the service's contribution from TLS and public response encoding.

The CPU affinity, TLS, fixture and build settings match the lifecycle fixture above. The client waited until the native Xray statistics uptime was at least one second, warmed each connection, then measured a 450 ms polling window. Server and client-count order alternate across rounds. Use `XRAY_BENCH_RUNTIME_STATS=1 XRAY_BENCH_ROUNDS=7` with the lifecycle reproduction command.

[HTTP per-round results](xray/results/2026-11-23-runtime-stats-http.csv) · [gRPC per-round results](xray/results/2026-11-23-runtime-stats-grpc.csv)

## Xray runtime statistics after the rewrite

Both nodes used Xray v26.11.9 on the same pinned single-core TLS fixture. The benchmark measures actual successful reads per second and percentiles for successful reads. Capacity rejections are counted. The 450 ms sweep ran three alternating rounds per transport and concurrency level with one outstanding request per client. Each cell is the median of per-round measurements. The reference revision and TLS setup match the lifecycle fixture.

The rewrite executes an uncontended read inline. Overlapping requests wait for the next sample, which starts after the collected requests entered the queue. Tests exercise freshness, cancellation, restart, and shutdown. One active native read plus 64 waiting reads used two native RPCs in the controlled test.

### Full load sweep

PapilioVPN had lower p50 in all 12 cases and lower p95 and higher successful reads per second in 11 of 12. Both nodes returned zero capacity rejections. The single-client gRPC case had lower p50 and higher p95 for PapilioVPN, with 843 successful reads/s versus 977 for PasarGuard.

| Transport | Clients | Node success/s | PasarGuard success/s | Node p50 ms | PasarGuard p50 ms | Node p95 ms | PasarGuard p95 ms | Node rejections |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| HTTP | 1 | 1268 | 1188 | 0.673 | 0.730 | 1.558 | 1.704 | 0 |
| HTTP | 8 | 4566 | 4047 | 1.606 | 1.786 | 3.125 | 4.062 | 0 |
| HTTP | 16 | 6778 | 5054 | 2.138 | 2.806 | 4.083 | 5.857 | 0 |
| HTTP | 32 | 8592 | 5322 | 3.350 | 5.374 | 7.266 | 10.968 | 0 |
| HTTP | 64 | 10338 | 5591 | 5.738 | 10.544 | 10.450 | 19.358 | 0 |
| HTTP | 128 | 11778 | 5592 | 10.013 | 21.650 | 18.052 | 34.142 | 0 |
| gRPC | 1 | 843 | 977 | 0.870 | 0.887 | 3.370 | 2.328 | 0 |
| gRPC | 8 | 4269 | 3567 | 1.775 | 2.000 | 3.493 | 4.371 | 0 |
| gRPC | 16 | 6185 | 4182 | 2.436 | 3.524 | 4.747 | 6.423 | 0 |
| gRPC | 32 | 8682 | 4408 | 3.434 | 6.643 | 6.346 | 11.754 | 0 |
| gRPC | 64 | 11004 | 5855 | 5.644 | 9.978 | 9.712 | 17.538 | 0 |
| gRPC | 128 | 10246 | 6351 | 11.931 | 19.803 | 20.113 | 32.504 | 0 |

[HTTP rounds](xray/results/2026-11-23-runtime-stats-rewrite-http.csv) · [gRPC rounds](xray/results/2026-11-23-runtime-stats-rewrite-grpc.csv)

### Focused single-client run

Nine alternating rounds used 450 ms polling windows.

| Transport | Clients | Node success/s | PasarGuard success/s | Node p50 ms | PasarGuard p50 ms | Node p95 ms | PasarGuard p95 ms | Node rejections |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| HTTP | 1 | 2619 | 2315 | 0.324 | 0.360 | 0.749 | 0.693 | 0 |
| gRPC | 1 | 1044 | 964 | 0.793 | 0.899 | 1.837 | 2.215 | 0 |

[HTTP rounds](xray/results/2026-11-23-runtime-stats-rewrite-single-http.csv) · [gRPC rounds](xray/results/2026-11-23-runtime-stats-rewrite-single-grpc.csv)

### Longer single-client run

Five alternating rounds with two-second polling windows, using the same Node binary as the load sweep.

| Transport | Node success/s | PasarGuard success/s | Node p50 ms | PasarGuard p50 ms | Node p95 ms | PasarGuard p95 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| HTTP | 931 | 1015 | 0.834 | 0.851 | 2.691 | 2.642 |
| gRPC | 1068 | 936 | 0.807 | 0.917 | 1.934 | 2.345 |

[HTTP rounds](xray/results/2026-11-23-runtime-stats-rewrite-long-http.csv) · [gRPC rounds](xray/results/2026-11-23-runtime-stats-rewrite-long-grpc.csv)

### Where time is spent inside Node

The diagnostic used one connection to the same Xray instance. It rotated the
three paths across 15 rounds of 250 reads per path. The direct RPC and service
both used a one-second local deadline. The HTTP measurement invoked Node's
router, API-key middleware, and JSON handler with an in-process response
recorder. Network and TLS latency lie outside this diagnostic.

| Path | Median per-round p50 µs | Median per-round p95 µs |
| --- | ---: | ---: |
| Native Xray RPC | 168.318 | 410.906 |
| Node statistics service | 172.345 | 383.133 |
| Node in-process HTTP handler | 197.553 | 456.462 |

Taking the median of differences within each round, the service added 1.503 µs
at p50 compared with the native RPC. The HTTP path added 26.770 µs at p50 and
47.099 µs at p95 compared with the service. These figures describe the local
effects of the tested paths. The end-to-end comparison also includes each
product's network, TLS, and response format.

[Raw rounds](xray/results/2026-11-23-runtime-stats-rewrite-components.csv) · [Diagnostic source](xray/components/main.go)

### Reproduce the rewrite measurements

Build the two nodes, the client, and the descriptor using the lifecycle reproduction
steps. Run the same command with one row of environment settings below. Use a
separate output directory for each run.

| Workload | `XRAY_BENCH_RUNTIME_STATS` | `XRAY_BENCH_RUNTIME_STATS_CLIENTS` | `XRAY_BENCH_RUNTIME_STATS_WINDOW` | `XRAY_BENCH_ROUNDS` |
| --- | --- | --- | --- | --- |
| Full sweep | 1 | 1,8,16,32,64,128 | 450ms | 3 |
| Focused single-client | 1 | 1 | 450ms | 9 |
| Longer single-client | 1 | 1 | 2s | 5 |

Run the in-process diagnostic with the same Xray executable and the server CPU
from the lifecycle fixture.

```bash
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/components" ./benchmarks/xray/components
GOMAXPROCS=1 taskset -c "${XRAY_BENCH_SERVER_CPU:-2}" "$work/components" "$work/xray/xray"
```

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

[HTTP rounds](xray/results/2026-11-23-traffic-http.csv) · [gRPC rounds](xray/results/2026-11-23-traffic-grpc.csv)

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

[Initial HTTP sweep](xray/results/2026-11-23-traffic-initial-http.csv) · [Focused HTTP rounds](xray/results/2026-11-23-traffic-focus-http.csv) · [512-account HTTP repeat](xray/results/2026-11-23-traffic-repeat-http.csv) · [Focused gRPC rounds](xray/results/2026-11-23-traffic-repeat-grpc.csv)

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

[HTTP rounds](xray/results/2026-11-23-traffic-delivery-http.csv) · [gRPC rounds](xray/results/2026-11-23-traffic-delivery-grpc.csv)

Use the current traffic runner with `XRAY_BENCH_TRAFFIC_NAMES=1` or `512`,
`XRAY_BENCH_TRAFFIC_CLIENTS=1,8`, `XRAY_BENCH_TRAFFIC_WINDOW=250ms`,
and `XRAY_BENCH_ROUNDS=3`.

### Runtime statistics with the concurrency gates

The runtime follow-up measured the new gate at 1 and 8 clients with three
250 ms polling windows per transport and client count. Each pair shows
PapilioVPN / PasarGuard. The single-client throughput was lower on both
transports in this follow-up.

| Transport | Clients | Success/s | p50 ms | p95 ms |
| --- | ---: | ---: | ---: | ---: |
| HTTP | 1 | 702 / 972 | 1.214 / 0.859 | 3.061 / 2.319 |
| HTTP | 8 | 3099 / 3091 | 2.077 / 2.269 | 5.668 / 5.025 |
| gRPC | 1 | 738 / 874 | 0.969 / 1.008 | 3.914 / 2.666 |
| gRPC | 8 | 3512 / 3007 | 2.104 / 2.317 | 4.177 / 4.886 |

[HTTP rounds](xray/results/2026-11-23-runtime-stats-concurrency-gates-http.csv) · [gRPC rounds](xray/results/2026-11-23-runtime-stats-concurrency-gates-grpc.csv)

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

[Before](xray/results/2026-11-23-traffic-list-sharing-before.csv) · [After](xray/results/2026-11-23-traffic-list-sharing-after.csv)

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

[HTTP rounds](xray/results/2026-11-23-traffic-list-sharing-matrix-http.csv) · [gRPC rounds](xray/results/2026-11-23-traffic-list-sharing-matrix-grpc.csv)

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
GOTOOLCHAIN=go1.27.3 GOMAXPROCS=1 taskset -c 2 go test ./internal/engines/xray/stats/native \
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

[Focused before](xray/results/2026-11-23-traffic-lean-focused-before.csv) ·
[Focused after](xray/results/2026-11-23-traffic-lean-focused-after.csv)

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

[HTTP rounds](xray/results/2026-11-23-traffic-lean-matrix-http.csv) ·
[gRPC rounds](xray/results/2026-11-23-traffic-lean-matrix-grpc.csv)

## Xray online IP statistics

The paired workload used the lifecycle fixture with Xray v26.11.9 for both
servers and PasarGuard at `739eab94a9343d9f0035baf7162796844109f4c4`. Each engine
had one authenticated HTTP CONNECT tunnel open throughout the benchmark, with
`statsUserOnline` enabled and `127.0.0.2` as the source. The client checked every
count against one and every list against the source IP and a positive native Unix
timestamp. Both HTTP endpoints returned equivalent data over HTTP/1.1 with TLS.
PapilioVPN used JSON and PasarGuard used protobuf. Both gRPC endpoints used
protobuf over TLS. Node makes one targeted Xray RPC per request.

### Full measurement

Each figure is the median of seven 450 ms rounds. Cells give PapilioVPN /
PasarGuard. Latency is in microseconds and goodput counts successful reads per
second. The test observed zero rejected requests.

| Transport | Operation | Clients | p50 µs | p95 µs | Reads/s |
| --- | --- | ---: | ---: | ---: | ---: |
| HTTP | Count IPs | 1 | 703 / 763 | 1859 / 2092 | 1171 / 1104 |
| HTTP | Count IPs | 8 | 1877 / 1895 | 4618 / 4491 | 3556 / 3678 |
| HTTP | List IPs | 1 | 721 / 779 | 1857 / 1873 | 1178 / 1084 |
| HTTP | List IPs | 8 | 1795 / 2143 | 3905 / 4923 | 3992 / 3282 |
| gRPC | Count IPs | 1 | 810 / 751 | 1972 / 1774 | 1095 / 1134 |
| gRPC | Count IPs | 8 | 1692 / 2490 | 3672 / 5533 | 4141 / 2888 |
| gRPC | List IPs | 1 | 1168 / 1399 | 3158 / 3885 | 757 / 579 |
| gRPC | List IPs | 8 | 2437 / 2919 | 5164 / 6490 | 2923 / 2328 |

PapilioVPN led on all three metrics in six of eight workloads. It trailed on
HTTP count p95 and goodput with eight clients, and on all three gRPC count metrics
with one client. The initial five-round sweep led on all three metrics in four
of eight workloads, including both of the count workloads that trailed in the
seven-round sweep. The initial build also sent a timeout header to Xray. The
final build uses a local one-second deadline and still cancels the stream. The
pinned native handlers process these reads without consulting the server context.
The two full sweeps had different round counts and absolute latencies, so they
do not isolate the effect of the deadline change. [Xray handlers](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/stats/command/command.go#L49-L80)

[Full HTTP rounds](xray/results/2026-11-23-online-http.csv) · [Full gRPC rounds](xray/results/2026-11-23-online-grpc.csv) · [Initial HTTP rounds](xray/results/2026-11-23-online-initial-http.csv) · [Initial gRPC rounds](xray/results/2026-11-23-online-initial-grpc.csv)

### Focused repeat of the two count workloads

The final binary ran the two count workloads again in seven 550 ms rounds
each. PapilioVPN led on all three median metrics in the focused repeat. The
direction of the differences changed between the full sweep and this repeat.

| Transport | Clients | p50 µs | p95 µs | Reads/s |
| --- | ---: | ---: | ---: | ---: |
| HTTP | 8 | 1306 / 1504 | 2855 / 3536 | 5499 / 4632 |
| gRPC | 1 | 487 / 559 | 1149 / 1327 | 1741 / 1550 |

[Focused HTTP rounds](xray/results/2026-11-23-online-count-http.csv) · [Focused gRPC rounds](xray/results/2026-11-23-online-count-grpc.csv)

### Reproduce

Build the binaries and descriptor using the [lifecycle setup](#reproduce). Run
the paired workload with the same Go toolchain and CPU affinity. This workload
covers a one-entry IP list.

```sh
XRAY_BENCH_TRAFFIC_NAMES=1 XRAY_BENCH_TRAFFIC_CASE=online \
XRAY_BENCH_TRAFFIC_CLIENTS=1,8 XRAY_BENCH_TRAFFIC_WINDOW=450ms XRAY_BENCH_ROUNDS=7 \
bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
  "$work/bench" "$work/reference.pb" "$work/results-online"
```

For the focused repeat, use `XRAY_BENCH_TRAFFIC_CASE=online-count/one` and
`XRAY_BENCH_TRAFFIC_WINDOW=550ms`. Run once with
`XRAY_BENCH_TRANSPORTS=http` and `XRAY_BENCH_TRAFFIC_CLIENTS=8`, and once with
`XRAY_BENCH_TRANSPORTS=grpc` and `XRAY_BENCH_TRAFFIC_CLIENTS=1`.

## Xray online account list

`ListOnlineUsers` returns the account names with at least one tracked source
IP. Each request uses one native `GetAllOnlineUsers` call. Node bounds the
native response at 1 MiB and shares a 16-read admission limit with the per-account
IP list. Xray walks its registered online maps once and only emits entries with
a positive IP count. Node strips the key prefix and suffix in place and keeps
the response order unspecified.

The fixture used one Xray v26.11.9 execution with 1,000 configured accounts and
0, 100, or 1,000 established CONNECT tunnels. It checked the exact account set
before timing and checked the account count on every timed read. All paths used
the same instance. The direct baseline called Xray over its Unix socket. The
other paths included the extra loopback hop, API-key validation, TLS and response
encoding and decoding. The client, Node handlers and Xray were pinned to logical
CPU 2 with `GOMAXPROCS=1`. The host ran Linux 6.17 on an AMD Ryzen 7 3750H and
the fixture client and Node used Go 1.27.3.

Cells show median p50 µs / p95 µs / successful reads per second across five
350 ms rounds. No requests were rejected. The pinned PasarGuard presence APIs
operate on one named account, so the direct Xray results give the native cost
for this workload.

| Active accounts | Clients | Native Xray | Node HTTP | Node gRPC |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 1 | 173 / 476 / 4375 | 731 / 1235 / 1293 | 675 / 1192 / 1408 |
| 0 | 8 | 670 / 1264 / 10564 | 2283 / 4353 / 3284 | 1642 / 3710 / 4299 |
| 100 | 1 | 279 / 569 / 2771 | 703 / 1154 / 1320 | 744 / 1375 / 1236 |
| 100 | 8 | 1074 / 3371 / 6328 | 3311 / 7578 / 2106 | 2652 / 5776 / 2715 |
| 1000 | 1 | 643 / 1321 / 1328 | 1458 / 2918 / 616 | 1271 / 2036 / 698 |
| 1000 | 8 | 3268 / 9106 / 1936 | 8425 / 15605 / 877 | 6831 / 14225 / 1062 |

[Per-round measurements](xray/results/2026-11-23-online-users.csv) · [Fixture source](xray/online_users_test.go)

Set `XRAY_BINARY` to the absolute path of the pinned executable and run on
an available logical CPU.

```sh
work=$(mktemp -d)
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go test -c -trimpath -o "$work/bench" ./benchmarks/xray
GOMAXPROCS=1 XRAY_BENCH_ONLINE_USERS="$XRAY_BINARY" \
  taskset -c "${XRAY_BENCH_SERVER_CPU:-2}" "$work/bench" -test.run '^TestMeasureOnlineUsers$' -test.v
```

## Xray routing and outbound health

- [Routing rule and balancer reads](xray/routing.md) includes paired round data,
  focused repeats for the slower cases, and the JSON encoding measurements.
- [Route evaluation and balancer overrides](xray/routing-control.md) and
  [rule changes](xray/routing-rule-writes.md) include the paired control benchmarks.
- [Live VLESS accounts](xray/vless-accounts.md) covers additions, removals,
  and native membership counts.
- [Recorded outbound health](xray/outbound-health.md) includes the paired results,
  contract differences, and raw samples for the individual requests.

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

[Per-round CSV](xray/results/2026-11-23-traffic-reset.csv) · [Benchmark source](xray/reset_test.go)

Set `XRAY_BINARY` to the absolute path of the pinned Xray executable.

```sh
work=$(mktemp -d)
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go test -c -trimpath -o "$work/bench" ./benchmarks/xray
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
an unrelated counter whose key contains the pattern. [Native diagnostic data](xray/results/2026-11-23-traffic-reset-pair-native.csv)

[Paired per-round results](xray/results/2026-11-23-traffic-reset-pair.csv)

Build both nodes, the benchmark client, and the reference descriptor using
the [lifecycle setup](#reproduce). Run the paired fixture for each size.

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
  taskset -c 2 go test ./benchmarks/xray -run '^TestMeasureNativePairReset$' -count=1 -v
```

## Xray integration performance recheck

The baseline `a51a787` combines the routing controls, VLESS account operations,
and traffic resets. Both targets and the driver used Go 1.27.4, stock Xray
v26.11.9 and the pinned PasarGuard revision `739eab9`. The driver used CPU 6.
Both servers and Xray children used CPU 2. Each row represents five
alternating rounds using 450 ms windows for runtime and reset or 550 ms
windows for TestRoute. No measured requests were rejected.

Each cell shows Node / PasarGuard. The first two values are latency in
microseconds. The third is successful requests per second.

| Operation | API | Clients | Baseline p50 · p95 · reads/s | Trial p50 · p95 · reads/s |
| --- | --- | ---: | --- | --- |
| TestRoute | HTTP | 1 | 479.4 / 526.6 · 964.7 / 960.3 · 1765.4 / 1728.8 | 314.1 / 339.8 · 359.0 / 460.9 · 2989.2 / 2726.8 |
| TestRoute | HTTP | 8 | 1309.3 / 1352.5 · 3182.4 / 3179.7 · 5336.1 / 5146.9 | 929.4 / 1062.6 · 2591.7 / 2768.1 · 7279.8 / 6476.6 |
| Runtime stats | HTTP | 1 | 462.4 / 505.1 · 983.9 / 1028.4 · 1777.0 / 1662.3 | 375.6 / 395.7 · 687.2 / 505.0 · 2360.8 / 2332.3 |
| Runtime stats | HTTP | 8 | 993.3 / 1236.2 · 2220.9 / 3155.5 · 7019.0 / 5610.0 | 854.2 / 1161.4 · 1764.9 / 2983.1 · 8102.8 / 6077.3 |
| Runtime stats | gRPC | 1 | 452.0 / 533.9 · 771.5 / 1043.5 · 1928.6 / 1629.2 | 462.7 / 515.9 · 732.9 / 829.5 · 1999.0 / 1723.2 |
| Runtime stats | gRPC | 8 | 1050.4 / 1312.1 · 2499.8 / 3014.9 · 6847.9 / 5470.4 | 1001.5 / 1428.0 · 2106.2 / 3266.3 · 7297.9 / 4933.7 |
| Reset pair, 1 account | HTTP | 1 | 1162.3 / 799.9 · 3594.6 / 3315.1 · 692.5 / 889.0 | 480.5 / 339.5 · 628.5 / 471.6 · 1933.2 / 2689.1 |
| Reset pair, 1 account | HTTP | 8 | 3053.6 / 1867.9 · 6069.7 / 4590.0 · 2316.6 / 3745.9 | 1286.8 / 1010.1 · 3228.1 / 2567.4 · 5407.6 / 6912.9 |
| Reset pair, 1 account | gRPC | 1 | 1081.3 / 852.6 · 2735.8 / 2686.9 · 792.2 / 949.8 | 508.9 / 364.6 · 720.6 / 504.1 · 1745.3 / 2561.4 |
| Reset pair, 1 account | gRPC | 8 | 2780.3 / 2064.8 · 5804.9 / 4382.1 · 2588.3 / 3498.9 | 1418.6 / 1067.5 · 3245.2 / 2726.1 · 4980.7 / 6391.8 |
| Reset pair, 512 accounts | HTTP | 1 | 731.0 / 592.5 · 1244.9 / 1061.6 · 1216.6 / 1516.3 | 449.0 / 392.4 · 541.1 / 546.4 · 2075.3 / 2362.9 |
| Reset pair, 512 accounts | HTTP | 8 | 1873.7 / 1666.1 · 4025.5 / 3451.7 · 3838.1 / 4358.6 | 1298.1 / 1390.4 · 3229.8 / 3114.3 · 5346.7 / 5203.1 |
| Reset pair, 512 accounts | gRPC | 1 | 856.7 / 702.4 · 1659.8 / 1176.5 · 1055.0 / 1300.4 | 523.7 / 478.8 · 999.6 / 782.2 · 1623.2 / 1849.3 |
| Reset pair, 512 accounts | gRPC | 8 | 2055.7 / 2024.3 · 4094.6 / 4092.8 · 3517.6 / 3557.2 | 1508.9 / 1467.0 · 3549.2 / 2933.8 · 4505.1 / 4961.9 |

The retained TestRoute change keeps the native request and its routing context
in one allocation and detects extra JSON input without decoding an extra
value. The trial led on all three metrics at both tested HTTP client counts.
The remaining trial changes reused a runtime reply and reused the reset
request and reply while adjusting native call options. The baseline already
led in the four runtime cases. The reset trial still trailed on at least one
metric in each case. PasarGuard's own timing shifted between runs on the
shared workstation. Both statistics paths retain the baseline implementation.

[Per-round measurements](xray/results/2026-11-23-integrated-control-retest.csv)

Use the [routing fixture](xray/routing-control.md#benchmark) with
`XRAY_BENCH_ROUTING_RULES=4`, `XRAY_BENCH_ROUTING_OPERATION=test`,
`XRAY_BENCH_ROUTING_CLIENTS=1,8`, `XRAY_BENCH_ROUTING_WINDOW=550ms` and
`XRAY_BENCH_TRANSPORTS=http` for the retained change. Build both nodes and
the driver with Go 1.27.4. Set `XRAY_BENCH_ROUNDS=5` and
`XRAY_BENCH_CLIENT_CPU=6`. Use the corresponding runtime statistics and named
pair reset fixtures above for those workloads.
