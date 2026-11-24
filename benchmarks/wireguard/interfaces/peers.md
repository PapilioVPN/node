# Full WireGuard peer synchronization

The fixture sends complete desired sets of 1, 100 and 1,000 peers to
Node's HTTPS peer endpoint and PasarGuard's `SyncUsers`. Keys, IPv4
prefixes and intended kernel state are identical. In the changed workload,
successive requests alternate one peer's allowed prefix. In the replay
workload, every request repeats the original list. The harness checks the
kernel's complete peer and prefix set outside timing after each phase.

Node reconciles from a live kernel dump. PasarGuard uses its in-memory
peer store for replay and recreates the full kernel peer set when any
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

Use the setup in [the lifecycle benchmark](lifecycle.md#reproduce) with
two allowed logical CPUs in `taskset` and add `--peer-sync` after the
PasarGuard binary path. The peer fixture also uses `wg`.
