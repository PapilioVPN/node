# WireGuard status configuration

`GetStatus` includes the kernel's effective public key, listen port and local
addresses when requested. HTTP clients set `?include_configuration=true`
and gRPC clients set `include_configuration` on `WireGuardInterfaceRequest`.
The default path reads only the link status. The detailed path also reads
addresses and drains a WireGuard device dump while skipping peer decoding.

| Idle peers | Basic HTTP, ms/op | Detailed HTTP, ms/op | Basic gRPC, ms/op | Detailed gRPC, ms/op |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 0.150 | 0.182 | 0.146 | 0.187 |
| 1,000 | 0.121 | 1.006 | 0.152 | 0.945 |

Values are medians of five run means from `BenchmarkStatusConfiguration`.
The fixture uses warm loopback connections, one isolated network namespace,
one CPU, and one configured IPv4 address. The integration test separately
compares HTTP and gRPC results with the port, address and key verified after
live changes. PasarGuard has no equivalent detailed read in its
[pinned API](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto).
[Raw results](results/status-configuration-linux-amd64.txt)

## Combined Start without recreating the link

The workload changes the MTU, IPv4 address, listen port and private key together through the public HTTPS Start path. It checks final MTU, address and port on the kernel. It also checks Node's interface index and applied-source hash. The independent native integration test checks the resulting public key, preserved peers and rollback after an occupied port.

| Run | Node p50 / p95 (ms) | PasarGuard 0.5.7 p50 / p95 (ms) |
| --- | ---: | ---: |
| 1 | 20.302 / 28.570 | 95.062 / 119.798 |
| 2 | 24.005 / 32.253 | 92.973 / 110.421 |

Each result pools five alternating blocks with twelve changes per implementation. The fixture uses no peers, a warm authenticated TLS 1.3 connection and an isolated Linux 6.17.0-41 network namespace on an AMD Ryzen 7 3750H. PasarGuard recreates the interface for this change, while Node changes it in place. This is a comparison of time to the requested final settings, not the same lifecycle guarantee.

[Run 1](results/2026-11-25-combined-start-1.csv) · [Run 2](results/2026-11-25-combined-start-2.csv)

```sh
export NODE_TEST_WIREGUARD_PARENT_NETNS="$(readlink /proc/self/ns/net)"
taskset -c 2,3 unshare -Urn python3 benchmarks/wireguard/interfaces/measure-lifecycle.py \
  /absolute/path/to/node /absolute/path/to/pasarguard --combined-start
```
