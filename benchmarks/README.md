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
