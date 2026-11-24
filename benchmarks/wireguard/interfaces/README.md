# WireGuard interface status

[Start and Stop benchmark](lifecycle.md) · [Configuration validation benchmark](validation.md) · [Node restart recovery benchmark](recovery.md)

Each operation reads the same Linux WireGuard interface and verifies its name,
MTU, and administrative `IFF_UP` flag. Node makes a fresh, targeted
`RTM_GETLINK` request through a reused route Netlink socket. HTTP and gRPC
include serialization, loopback transport, and response decoding over warm
connections. The reference launches `ip -j -d link show dev wg-node-test` for
each read, checks the link kind, and extracts the same three fields.

| Configured peers | Node HTTP, µs/op | Node gRPC, µs/op | `ip`, µs/op |
| ---: | ---: | ---: | ---: |
| 0 | 213.2 (174.8–267.1) | 257.5 (223.1–418.1) | 3,057.5 (2,705.2–5,120.0) |
| 1,000 | 197.8 (175.5–256.8) | 336.1 (253.1–393.2) | 3,398.2 (3,084.0–4,844.1) |

Each cell reports the median and range of five run means. Linux
`6.17.0-41-generic`, Go `1.27.4`, AMD Ryzen 7 3750H, iproute2 `6.16.0`,
wireguard-tools `1.0.20210914`. The benchmark uses one logical CPU and one
request at a time in a separate network namespace. The ranges overlap across
peer counts. Results apply to sequential loopback requests without TLS.
[Raw Go benchmark output](results/linux-amd64.txt)

The [pinned PasarGuard Node API](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto)
offers `GetBaseInfo`, which returns only backend lifecycle state and versions.
This measurement uses the Linux command-line reference for per-interface reads.

## Reproduce

Use Linux with kernel WireGuard, `ip`, `wg`, and permission to create a user
and network namespace. Select a logical CPU allowed by your CPU affinity.

```bash
NODE_TEST_WIREGUARD=1 go test -race ./internal/engines/wireguard/interfaces/integration

work=$(mktemp -d)
go test -c -o "$work/wireguard.test" ./internal/engines/wireguard/interfaces/integration
NODE_TEST_WIREGUARD_PARENT_NETNS=$(readlink /proc/self/ns/net) GOMAXPROCS=1 \
  taskset -c 2 unshare --user --map-root-user --net "$work/wireguard.test" \
  -test.run '^$' -test.bench '^BenchmarkInterfaceStatus$' -test.benchmem \
  -test.benchtime=300ms -test.count=5
rm -r "$work"
```
