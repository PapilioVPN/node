# Targeted WireGuard peer changes

Each authenticated TLS 1.3 connection drives a Linux WireGuard interface with
1,000 peers. An update alternates one peer's allowed prefix. A replay repeats
the current value. A removal deletes an existing peer, which the fixture restores
outside the timed interval. The fixture checks the kernel peer and prefix set
outside timing in each workload.

Node uses a single-peer request or submits one independent peer delta. The
pinned PasarGuard Node uses `SyncUser` or receives one complete `SyncUsersChunked`
message. The reference tracks peers in memory. Node reads the live WireGuard
device before an upsert or delta and rejects an exact prefix held by an unnamed
peer. A single removal uses the kernel's idempotent removal flag without a dump.
These timings compare shared effects with different freshness guarantees.
[Pinned reference implementation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/user_partial_sync.go)
· [Linux WireGuard netlink specification](https://docs.kernel.org/netlink/specs/wireguard.html)

| Operation | Node p50, ms | PasarGuard p50, ms | Node p95, ms | PasarGuard p95, ms |
| --- | ---: | ---: | ---: | ---: |
| Change one peer | 5.603 | 1.299 | 13.275 | 2.098 |
| Replay one peer | 6.115 | 1.177 | 10.235 | 2.193 |
| Remove one peer | 1.127 | 1.604 | 3.793 | 2.646 |
| Change one peer in a 100-peer chunk | 6.257 | 1.933 | 12.080 | 3.672 |
| Replay a 100-peer chunk | 5.734 | 2.189 | 9.207 | 5.926 |
| Change one peer in a 1,000-peer chunk | 9.519 | 10.458 | 15.684 | 17.572 |
| Replay a 1,000-peer chunk | 7.918 | 9.537 | 11.481 | 15.956 |

Each cell uses 40 requests across four alternating blocks. Linux
`6.17.0-41-generic`, Go `1.27.4`, `GOMAXPROCS=2`, two logical CPUs on an AMD
Ryzen 7 3750H and an isolated network namespace. [Raw durations](results/peer-delta-https.csv)

In a separate local stage probe, reading and decoding the 1,000-peer kernel
snapshot took 2.03–2.66 ms per operation over three runs. Planning one prefix
change from that snapshot took 4.88–5.91 µs per operation with zero allocations.
These are run means for the in-process stages.

Use the setup in [the lifecycle benchmark](lifecycle.md#reproduce), select two
logical CPUs, and add `--peer-delta` after the PasarGuard binary path.

## Netlink receive optimization

The dedicated dump socket receives into a reusable 32 KiB buffer. Linux can
pack more peers per datagram, and Node decodes the datagrams in place and
clears them after use. The receiver validates the kernel sender, message
sequence, completion status, and interruption flag before the snapshot can
authorize a write. Previous peer and prefix counts only size the allocations.
[Linux netlink dump allocation](https://github.com/torvalds/linux/blob/v6.17/net/netlink/af_netlink.c#L2143-L2178)

The 1,000-peer stage probe uses three runs per build with one allowed prefix
per peer. The median run mean fell from 2.763 to 1.630 ms per dump, and heap
allocations fell from about 620 to 125 kB and from 1,338 to 62 per dump.

The three-build HTTPS fixture compares the optimized reader with Node
`6b50378` and the same pinned PasarGuard build. The table combines two runs
with four alternating blocks each and 80 requests per operation and build.

| Operation | Prior Node p50, ms | Node p50, ms | PasarGuard p50, ms | Prior Node p95, ms | Node p95, ms | PasarGuard p95, ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Change one peer | 4.303 | 2.558 | 0.911 | 8.374 | 5.062 | 1.684 |
| Replay one peer | 3.701 | 2.388 | 0.694 | 5.406 | 4.012 | 1.721 |
| Remove one peer | 0.806 | 0.820 | 1.038 | 2.110 | 2.412 | 3.097 |
| Change one peer in a 100-peer chunk | 4.352 | 2.665 | 1.441 | 7.208 | 4.072 | 3.852 |
| Replay a 100-peer chunk | 4.008 | 2.767 | 1.309 | 5.918 | 4.670 | 3.043 |
| Change one peer in a 1,000-peer chunk | 6.416 | 5.716 | 7.273 | 13.529 | 8.467 | 14.154 |
| Replay a 1,000-peer chunk | 5.784 | 4.976 | 7.573 | 10.192 | 7.799 | 16.443 |

Single-peer updates had an optimized p95 of 18.436 ms in the first run and
3.944 ms in the second. The combined table includes every sample. The live
dump still reads the whole device because WireGuard netlink selects the
interface by index or name. [First run](results/peer-delta-receive-https.csv)
· [Second run](results/peer-delta-receive-repeat-https.csv)

To repeat the three-build comparison, pass the previous Node binary after
`--peer-delta`.
