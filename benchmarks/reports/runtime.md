# Xray runtime statistics

[Benchmark dashboard](../README.md)

The dashboard uses the retained runtime implementation from the
[integrated recheck](instances.md#xray-integration-performance-recheck).

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

[HTTP per-round results](../xray/results/2026-11-23-runtime-stats-http.csv) · [gRPC per-round results](../xray/results/2026-11-23-runtime-stats-grpc.csv)

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

[HTTP rounds](../xray/results/2026-11-23-runtime-stats-rewrite-http.csv) · [gRPC rounds](../xray/results/2026-11-23-runtime-stats-rewrite-grpc.csv)

### Focused single-client run

Nine alternating rounds used 450 ms polling windows.

| Transport | Clients | Node success/s | PasarGuard success/s | Node p50 ms | PasarGuard p50 ms | Node p95 ms | PasarGuard p95 ms | Node rejections |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| HTTP | 1 | 2619 | 2315 | 0.324 | 0.360 | 0.749 | 0.693 | 0 |
| gRPC | 1 | 1044 | 964 | 0.793 | 0.899 | 1.837 | 2.215 | 0 |

[HTTP rounds](../xray/results/2026-11-23-runtime-stats-rewrite-single-http.csv) · [gRPC rounds](../xray/results/2026-11-23-runtime-stats-rewrite-single-grpc.csv)

### Longer single-client run

Five alternating rounds with two-second polling windows, using the same Node binary as the load sweep.

| Transport | Node success/s | PasarGuard success/s | Node p50 ms | PasarGuard p50 ms | Node p95 ms | PasarGuard p95 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| HTTP | 931 | 1015 | 0.834 | 0.851 | 2.691 | 2.642 |
| gRPC | 1068 | 936 | 0.807 | 0.917 | 1.934 | 2.345 |

[HTTP rounds](../xray/results/2026-11-23-runtime-stats-rewrite-long-http.csv) · [gRPC rounds](../xray/results/2026-11-23-runtime-stats-rewrite-long-grpc.csv)

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

[Raw rounds](../xray/results/2026-11-23-runtime-stats-rewrite-components.csv) · [Diagnostic source](../xray/components/main.go)

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

## Runtime statistics with the concurrency gates

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

[HTTP rounds](../xray/results/2026-11-23-runtime-stats-concurrency-gates-http.csv) · [gRPC rounds](../xray/results/2026-11-23-runtime-stats-concurrency-gates-grpc.csv)
