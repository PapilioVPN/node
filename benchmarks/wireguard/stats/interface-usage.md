# WireGuard interface usage and reset

Node exposes `GetInterfaceUsage` and `ResetInterfaceUsage` in
`WireGuardStatsService` and as `GET /v1/wireguard/interfaces/{name}/usage`
and `POST /v1/wireguard/interfaces/{name}/usage/resets`. The first successful
sample establishes the baseline and returns zero. A read returns bytes since
that baseline. A reset returns the preceding period and establishes the next
baseline from the same sample. A new link or counter rollback starts a fresh
period. Node requires managed mode for reset and keeps the baseline in memory.

Each operation takes one live route Netlink sample of the link counters.
The integration test exchanges traffic on a real WireGuard tunnel and checks
the initial baseline, nonzero usage, reset and the next period through both
transports. The raw counters remain available through `GetInterfaceTraffic`.

The common workload uses warm HTTPS connections and idle links with 0 or
1,000 peers. Node reads or resets its link usage period. PasarGuard at
`739eab94a9343d9f0035baf7162796844109f4c4` uses `GetStats` with `Outbound`
and the matching reset flag. Node returns JSON with two zero counters and
PasarGuard returns an empty protobuf response for zero usage. Each case
includes four alternating blocks of 40 timed requests per implementation.
The benchmark measures HTTPS request/response latency with serialization.
[Reference link baseline](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/pkg/stats/interface_counters.go#L20)

| Run | Operation | Peers | Node p50, ms | PasarGuard p50, ms | Node p95, ms | PasarGuard p95, ms |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| First | Read | 0 | 0.743 | 0.897 | 1.070 | 1.096 |
| First | Read | 1,000 | 0.379 | 0.604 | 0.740 | 0.980 |
| First | Reset | 0 | 0.614 | 0.882 | 0.805 | 1.109 |
| First | Reset | 1,000 | 0.489 | 0.587 | 0.787 | 0.756 |
| Repeat | Read | 0 | 0.302 | 0.364 | 0.795 | 0.637 |
| Repeat | Read | 1,000 | 0.297 | 0.397 | 0.453 | 0.650 |
| Repeat | Reset | 0 | 0.296 | 0.338 | 0.770 | 1.020 |
| Repeat | Reset | 1,000 | 0.364 | 0.416 | 0.789 | 0.708 |

Node had a lower median in 29 of 32 paired blocks. At 1,000 peers, reset
p95 favored Node in five of eight individual blocks and PasarGuard in the
pooled requests of each run. At zero peers the read p95 changed order
between runs. Both runs used Go `1.27.4` on Linux `6.17.0-41-generic` and
one logical CPU of an AMD Ryzen 7 3750H.
[First run](results/interface-usage-https.csv) · [Repeat](results/interface-usage-repeat-https.csv)

Use the [pinned lifecycle setup](../interfaces/lifecycle.md#reproduce) with
the following command.

```bash
NODE_TEST_WIREGUARD_PARENT_NETNS=$(readlink /proc/self/ns/net) \
  taskset -c 2 unshare -Urn \
  python3 benchmarks/wireguard/interfaces/measure-lifecycle.py \
  "$work/node" "$work/pasarguard" --interface-usage
```
