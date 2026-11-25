# Full WireGuard peer synchronization

The fixture sends complete desired sets of 1, 100 and 1,000 peers to
Node's HTTPS peer endpoint and PasarGuard's `SyncUsers`. Keys, IPv4
prefixes and intended kernel state are identical. In the changed workload,
successive requests alternate one peer's allowed prefix. In the replay
workload, every request repeats the original list. The harness checks the
kernel's complete peer and prefix set outside timing after each phase.

The initial Node build reconciled from a live kernel dump. PasarGuard uses its
in-memory peer store for replay and recreates the full kernel peer set when any
peer changes. These timings describe the shared desired sets and their
different reconciliation effects. The separate native integration test
checks handshake continuity for an unchanged peer.
[PasarGuard full sync implementation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/user_full_sync.go)

| Peers | Workload | Node p50, ms | PasarGuard p50, ms | Node p95, ms | PasarGuard p95, ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | Replay | 0.896 | 1.150 | 1.646 | 3.646 |
| 1 | One change | 0.945 | 2.060 | 2.090 | 9.411 |
| 100 | Replay | 2.220 | 1.494 | 5.747 | 3.777 |
| 100 | One change | 2.029 | 46.278 | 4.638 | 129.826 |
| 1,000 | Replay | 9.735 | 8.859 | 13.940 | 15.798 |
| 1,000 | One change | 9.351 | 486.357 | 15.572 | 950.921 |

Each cell uses 50 requests across five alternating blocks. Both servers
use Go `1.27.4`, `GOMAXPROCS=2` and the same authenticated TLS 1.3 fixture
with warm loopback connections. Linux `6.17.0-41-generic`, two logical
CPUs on an AMD Ryzen 7 3750H. Host routing and NAT for the reference are
disabled. [Raw request durations](results/peer-sync-https.csv)

## Replay decoder optimization

The optimized decoder reads attributes inside the live netlink datagram.
It copies the keys and prefixes used by reconciliation into typed values.
Profiling the 1,000-peer kernel read found about 17,462 allocations per
operation with the previous decoder and 1,317 with the new decoder.
Integration coverage checks that replay also corrects an out-of-band kernel
change and preserves the unchanged peer's handshake.

The replay fixture alternates the previous Node build at `5178517`, the
optimized build and the pinned PasarGuard build on the same two CPUs.
Each cell is the median of 50 authenticated HTTPS requests.

| Peers | Previous Node, ms | Optimized Node, ms | PasarGuard, ms |
| ---: | ---: | ---: | ---: |
| 1 | 0.725 | 0.499 | 0.460 |
| 100 | 1.482 | 1.169 | 1.029 |
| 1,000 | 7.498 | 6.114 | 6.132 |

[Raw replay request durations](results/peer-replay-https.csv)

## Replay request and comparison costs

The HTTP handler decodes the bounded request body directly using the existing
JSON semantics. When the live and desired peers match in order, reconciliation
finishes without building a key index. A different kernel order or a difference
in a peer falls through to keyed reconciliation. Both paths read the kernel on
every request. Integration tests check correction of both prefix and PSK drift.

The three-build fixture uses `2d8cb42` as the previous Node build. The newer
Node build also uses the peer operation's single deadline for all kernel reads
and writes. The measurements below cover replay with no peer writes.

| Peers | Previous Node p50, ms | Node p50, ms | PasarGuard p50, ms | Previous Node p95, ms | Node p95, ms | PasarGuard p95, ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 1.434 | 1.147 | 0.899 | 3.190 | 5.678 | 2.588 |
| 100 | 2.242 | 1.584 | 1.750 | 5.345 | 3.035 | 4.317 |
| 1,000 | 9.327 | 7.656 | 10.511 | 15.637 | 61.305 | 19.228 |

Each column contains 50 requests across five alternating blocks. An earlier
run of the same replay changes, before the write deadline adjustment, measured
Node p50 at 0.874, 1.584 and 7.107 ms and PasarGuard at 0.721, 1.269 and
7.939 ms. The relative result at 100 peers varied. Four 1,000-peer Node
requests exceeded 50 ms in the first block of the final run and contribute
to the reported tail. The reference answers replay from its in-memory store
while Node verifies the live peer set and repairs drift.

[Final request durations](results/peer-replay-overhead-https.csv)
· [Earlier request durations](results/peer-replay-overhead-preliminary-https.csv)

Use the setup in [the lifecycle benchmark](lifecycle.md#reproduce) with
two allowed logical CPUs in `taskset` and add `--peer-sync` after the
PasarGuard binary path. For the three-build replay fixture, use
`--peer-replay /path/to/previous-node` in place of `--peer-sync`.
The peer fixture also uses `wg`.

## Live replay after the peer ownership redesign

Full `SyncPeers` continues to read the live kernel peer set. The managed
index introduced for targeted writes does not supply the full-sync snapshot.
The fixture compares the redesigned Node with Node `cb19e3e` and the same
pinned reference. Each cell contains 50 requests across five blocks.

| Peers | Prior Node p50, ms | Node p50, ms | PasarGuard p50, ms | Prior Node p95, ms | Node p95, ms | PasarGuard p95, ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 0.669 | 0.934 | 0.396 | 1.183 | 1.306 | 0.966 |
| 100 | 0.905 | 0.943 | 0.930 | 2.445 | 1.849 | 2.120 |
| 1,000 | 4.603 | 4.541 | 5.919 | 9.838 | 6.930 | 9.564 |

The one-peer result still trails the reference. The reference answers replay
from memory and Node checks live kernel state. These measurements retain the
complete replay results for the three builds. [Raw timings](results/peer-replay-managed-https.csv)

## Full sync from confirmed peer state

The normal `SyncPeers` request applies the desired set from the same confirmed
peer index used by targeted mutations. A cold execution or an unknown write
loads the initial state from the kernel. `reconcile: true` explicitly reads
the kernel and repairs out-of-band changes. Each request checks link ownership,
and Node records a changed peer set after the kernel acknowledges the writes.

The first fixture compares the managed path, the prior live-reading build
`57860b2`, and pinned PasarGuard on the same peer sets. It warms each set before
timing 50 replays and 50 one-peer changes. Node's write stays sparse when one
peer changes. PasarGuard's full-sync path reapplies the set.

| Peers | Workload | Prior Node p50, ms | Node p50, ms | PasarGuard p50, ms | Prior Node p95, ms | Node p95, ms | PasarGuard p95, ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | Replay | 0.836 | 0.748 | 0.368 | 1.410 | 1.338 | 0.562 |
| 1 | One change | 0.898 | 0.634 | 0.791 | 1.627 | 1.162 | 1.651 |
| 100 | Replay | 1.455 | 0.814 | 1.046 | 4.269 | 1.691 | 2.424 |
| 100 | One change | 1.615 | 0.830 | 21.160 | 5.093 | 1.409 | 101.545 |
| 1,000 | Replay | 4.990 | 3.154 | 5.630 | 7.294 | 5.265 | 7.429 |
| 1,000 | One change | 4.897 | 3.155 | 179.346 | 6.689 | 4.937 | 211.920 |

The one-peer service-stage probe measured a 38 µs median across three run
means, including a live ownership check at 34 µs. The HTTPS comparison includes
request handling, serialization and transport. [Raw timings](results/peer-sync-confirmed-https.csv)

The repeat used all six server orders to balance run position. Each cell uses
60 warm replays. The 100-peer p95 varied across the two comparisons.

| Peers | Prior Node p50, ms | Node p50, ms | PasarGuard p50, ms | Prior Node p95, ms | Node p95, ms | PasarGuard p95, ms |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 0.780 | 0.675 | 0.416 | 1.361 | 1.268 | 0.842 |
| 100 | 1.076 | 0.738 | 0.991 | 2.939 | 2.687 | 2.049 |
| 1,000 | 5.309 | 2.849 | 5.801 | 12.446 | 5.296 | 7.954 |

Pass the previous Node binary after `--peer-sync` for the three-build fixture
with both workloads, or after `--peer-replay` for replays. [Raw replay timings](results/peer-replay-confirmed-https.csv)

## Byte-identical HTTP replay

The lifecycle owner records one SHA-256 fingerprint for the last accepted
HTTP sync on its confirmed peer state. An exact replay checks ownership and
readiness in the kernel and returns zero changes without decoding and planning
the desired set again. A peer change or an unknown write clears recognition.
Explicit reconcile always decodes the request and reads live membership.

The three-build HTTPS fixture used all six server orders, with 60 warm replays
per build and peer count. Times are milliseconds.

| Peers | Prior Node p50 / p95 | Node p50 / p95 | PasarGuard p50 / p95 |
| ---: | ---: | ---: | ---: |
| 1 | 0.549 / 2.084 | 0.445 / 0.878 | 0.521 / 1.100 |
| 100 | 0.740 / 18.255 | 0.501 / 1.648 | 1.225 / 3.826 |
| 1,000 | 2.898 / 4.251 | 0.891 / 2.101 | 6.467 / 13.820 |

Both Node builds use the same API and live link ownership check. PasarGuard
uses its own protobuf wire format and email-based peer identity.
[Raw timings](results/peer-replay-identical-https.csv)
