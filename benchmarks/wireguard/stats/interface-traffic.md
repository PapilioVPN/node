# WireGuard interface traffic

`GET /v1/wireguard/interfaces/{name}/traffic` and
`WireGuardStatsService.GetInterfaceTraffic` return the kernel's RX and TX byte
counters for the current link. Node uses one targeted route Netlink request
through a reused statistics socket. The number of peers does not affect the
size of the response or the number of kernel requests. The isolated tunnel
integration test sends traffic in both directions and compares both APIs with
`ip -j -s link`.

The HTTPS comparison sends PasarGuard's `GetStats` request with type `Outbound`
and reset disabled. Both implementations read the link counters. PasarGuard
subtracts its baseline and omits zero-valued rows. Node returns both raw
counters. The fixture checks the respective responses on idle links with
zero and 1,000 peers. It measures a single request at a time over warm
connections. The application order alternates across four blocks of 40
requests. The measured times include transport and serialization. [Pinned
PasarGuard implementation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/stats.go#L42)

| Peers | Node p50, ms | PasarGuard p50, ms | Node p95, ms | PasarGuard p95, ms |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 0.570 | 0.666 | 1.037 | 1.170 |
| 1,000 | 0.528 | 0.590 | 0.761 | 0.855 |

Node had the lower median in seven of eight paired blocks. At 1,000 peers
the second block favored PasarGuard. The run used Go `1.27.4` on Linux
`6.17.0-41-generic` on one logical CPU of an AMD Ryzen 7 3750H.
[Raw HTTPS timings](results/interface-traffic-https.csv)

Build Node and the pinned reference using the [lifecycle benchmark setup](../interfaces/lifecycle.md#reproduce).
Run the same fixture with `--interface-traffic` in a separate user and network
namespace. Use a single logical CPU for both binaries.

```bash
NODE_TEST_WIREGUARD_PARENT_NETNS=$(readlink /proc/self/ns/net) \
  taskset -c 2 unshare -Urn \
  python3 benchmarks/wireguard/interfaces/measure-lifecycle.py \
  "$work/node" "$work/pasarguard" --interface-traffic
```
