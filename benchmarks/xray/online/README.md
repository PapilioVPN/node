# Xray online accounts and IPs

[Benchmark dashboard](../README.md)

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

[Full HTTP rounds](results/2026-11-23-online-http.csv) · [Full gRPC rounds](results/2026-11-23-online-grpc.csv) · [Initial HTTP rounds](results/2026-11-23-online-initial-http.csv) · [Initial gRPC rounds](results/2026-11-23-online-initial-grpc.csv)

### Focused repeat of the two count workloads

The final binary ran the two count workloads again in seven 550 ms rounds
each. PapilioVPN led on all three median metrics in the focused repeat. The
direction of the differences changed between the full sweep and this repeat.

| Transport | Clients | p50 µs | p95 µs | Reads/s |
| --- | ---: | ---: | ---: | ---: |
| HTTP | 8 | 1306 / 1504 | 2855 / 3536 | 5499 / 4632 |
| gRPC | 1 | 487 / 559 | 1149 / 1327 | 1741 / 1550 |

[Focused HTTP rounds](results/2026-11-23-online-count-http.csv) · [Focused gRPC rounds](results/2026-11-23-online-count-grpc.csv)

### Reproduce

Build the binaries and descriptor using the [lifecycle setup](../instances/README.md#reproduce). Run
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

[Per-round measurements](results/2026-11-23-online-users.csv) · [Fixture source](online_users_test.go)

Set `XRAY_BINARY` to the absolute path of the pinned executable and run on
an available logical CPU.

```sh
work=$(mktemp -d)
CGO_ENABLED=0 go test -c -trimpath -o "$work/bench" ./benchmarks/xray/online
GOMAXPROCS=1 XRAY_BENCH_ONLINE_USERS="$XRAY_BINARY" \
  taskset -c "${XRAY_BENCH_SERVER_CPU:-2}" "$work/bench" -test.run '^TestMeasureOnlineUsers$' -test.v
```
