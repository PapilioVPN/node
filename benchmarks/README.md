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
