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
