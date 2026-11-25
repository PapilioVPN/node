# WireGuard performance and coverage audit

## Integrated baseline

Node `0528b5d` and [pinned PasarGuard](https://github.com/PasarGuard/node/tree/739eab94a9343d9f0035baf7162796844109f4c4)
ran 11 scenarios with the same isolated fixtures over HTTPS on Linux amd64.
CPU affinity was 2 and 3, with one Go scheduler slot for light operations
and two for peer mutations. Peer reads use an active tunnel where the
fixture requires traffic. The fixture validates actual effects, including
peer membership, stop cleanup and policy rules. The transports use each
implementation's own schema.

Numbers below are p50 and nearest-rank p95 in milliseconds. A row states
its sample count per implementation. Routing and initial-peer samples are
small and report medians only. The comparison captures this fixture and
machine. It neither bounds tail latency nor measures every possible input.
The raw output includes the standalone kernel deletion control.

Node had a lower median in 37 of 41 rows and a lower p95 in 27 of the 37 rows with at least 20 samples.

[Raw baseline](results/performance-baseline-https.csv) · [Reproduction](interfaces/lifecycle.md#reproduce)

### Lifecycle

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `cold_start` | 100 | 3.175 / 22.354 | 5.651 / 23.007 |
| `same_start` | 100 | 0.817 / 1.835 | 0.912 / 1.992 |
| `stop` | 100 | 59.977 / 95.311 | 60.948 / 98.811 |

### Full peer sync

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `peers_1_noop` | 50 | 0.456 / 0.731 | 0.360 / 0.959 |
| `peers_1_changed` | 50 | 0.538 / 0.818 | 0.762 / 1.389 |
| `peers_100_noop` | 50 | 0.694 / 1.660 | 0.976 / 2.465 |
| `peers_100_changed` | 50 | 0.817 / 1.489 | 20.420 / 28.591 |
| `peers_1000_noop` | 50 | 2.905 / 4.261 | 5.889 / 8.483 |
| `peers_1000_changed` | 50 | 2.738 / 3.958 | 191.364 / 459.711 |

### Peer deltas

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `delta_1_change` | 40 | 0.555 / 0.959 | 0.567 / 1.015 |
| `delta_1_replay` | 40 | 0.489 / 11.669 | 0.629 / 1.275 |
| `delta_100_change` | 40 | 0.633 / 1.014 | 1.180 / 2.041 |
| `delta_100_replay` | 40 | 0.711 / 1.628 | 1.164 / 1.981 |
| `delta_1000_change` | 40 | 2.729 / 3.529 | 6.776 / 9.758 |
| `delta_1000_replay` | 40 | 2.629 / 3.814 | 6.293 / 8.299 |
| `delta_1_remove` | 40 | 2.085 / 3.126 | 0.786 / 1.474 |

### Raw interface traffic

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `traffic_0_peers` | 160 | 0.263 / 0.474 | 0.310 / 0.516 |
| `traffic_1000_peers` | 160 | 0.273 / 0.603 | 0.305 / 0.564 |

### Interface usage

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `usage_read_0_peers` | 160 | 0.248 / 0.591 | 0.318 / 0.580 |
| `usage_reset_0_peers` | 160 | 0.239 / 0.403 | 0.298 / 0.524 |
| `usage_read_1000_peers` | 160 | 0.275 / 0.715 | 0.336 / 0.508 |
| `usage_reset_1000_peers` | 160 | 0.238 / 0.486 | 0.324 / 0.511 |

### Sampled peer usage

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `peer_read_1_peers` | 160 | 0.205 / 0.827 | 0.247 / 0.626 |
| `group_read_1_peers` | 160 | 0.183 / 0.259 | 0.225 / 0.368 |
| `peer_reset_1_peers` | 160 | 0.194 / 0.265 | 0.215 / 0.345 |
| `group_reset_1_peers` | 160 | 0.186 / 0.304 | 0.218 / 0.314 |
| `peer_read_1000_peers` | 160 | 0.232 / 0.724 | 0.238 / 0.742 |
| `group_read_1000_peers` | 160 | 0.183 / 0.362 | 0.212 / 0.339 |
| `peer_reset_1000_peers` | 160 | 0.202 / 0.351 | 0.212 / 0.319 |
| `group_reset_1000_peers` | 160 | 0.189 / 0.317 | 0.214 / 0.382 |

### Peer activity and endpoint IPs

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `activity_1_peers` | 160 | 0.384 / 0.894 | 0.382 / 0.678 |
| `endpoint_ips_1_peers` | 160 | 0.292 / 0.506 | 0.362 / 0.622 |
| `activity_1000_peers` | 160 | 0.190 / 0.595 | 0.248 / 0.724 |
| `endpoint_ips_1000_peers` | 160 | 0.189 / 0.321 | 0.211 / 0.435 |

### Egress probe

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `egress_probe` | 120 | 1.248 / 1.921 | 1.023 / 2.018 |

### Diagnostics

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `runtime_stats` | 120 | 0.556 / 0.948 | 0.574 / 0.936 |
| `log_first_event` | 120 | 2.337 / 3.653 | 2.489 / 4.596 |

### IPv4 host routing

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `routing_start` | 4 | 10.173 | 36.749 |
| `routing_stop` | 4 | 104.849 | 112.511 |

### Initial peers

| Workload | Samples per build | Node | PasarGuard |
| --- | ---: | ---: | ---: |
| `initial_1_peers` | 3 | 2.369 | 5.062 |
| `initial_100_peers` | 3 | 17.988 | 292.181 |

## Follow-up comparisons

- Byte-identical HTTP full sync was faster in p50 and p95 at counts 1, 100,
  and 1,000 against both builds in the six-order replay. Link ownership and
  explicit reconciliation remain verified. [Full sync](interfaces/peers.md#byte-identical-http-replay)
- Targeted accounting reduces removal p50 from 2.178 to 2.003 ms and p95
  from 3.939 to 3.403 ms. The reference reports 0.728 and 1.330 ms. Node
  captures the last available counters and the kernel sends the full device
  dump even when only one peer is decoded. The reference removes the peer
  without that final capture. The 100-peer change also trailed the
  reference at p95. [Linux WireGuard Netlink specification](https://docs.kernel.org/netlink/specs/wireguard.html#get-device)
  · [Peer deltas](interfaces/peer-delta.md#targeted-accounting-before-one-peer-is-removed)
- A direct HTTP/1.1 egress connection did not outperform the current Node
  probe. The three-build comparison was 0.722 / 1.245 ms for current Node,
  0.737 / 1.656 ms for the candidate, and 0.871 / 1.828 ms for PasarGuard.
  The previous probe remains in use. [Raw experiment](results/performance-probe-experiment-https.csv)
- A lower-allocation HTTP activity serializer did not resolve the tail
  gap in the tested HTTPS requests. The previous serializer remains in use. [Raw experiment](results/performance-activity-experiment-https.csv)
- Equivalent PasarGuard operations are unavailable for dry-run validation,
  independent detailed status and membership reads, or restart recovery
  with Node's checkpoint contract. The native measurements for these
  features are available in the [interface benchmark index](interfaces/README.md).

## Behavior coverage against pinned PasarGuard

| Reference behavior | Node behavior |
| --- | --- |
| Start, same-source attach, stop, and runtime readiness | Covers lifecycle and exposes the confirmed source hash. Same-source Start repairs an unhealthy owned link or recreates a missing link from the submitted source, which restores its initial peers. Controllers resync peers added later when the reply reports recreation. |
| Request-based `keep_alive` watchdog | A successful Start renews the configured management lease. Status reads have no renewal effect. |
| `GetBaseInfo` returns running state and Node and WireGuard-tools versions | WireGuard status includes the Node version, kernel release, and optional WireGuard-tools version. |
| Per-email full sync, streamed single sync, and chunk aggregation before applying | Node exposes full and individual operations by peer key. It also collects contiguous gRPC delta chunks and applies the validated set after a clean half-close. |
| One interface-level default preshared key | Node accepts a file-backed default PSK and explicit per-peer overrides. |
| Runtime, link and peer usage, logs, online state, observed IPs, and outbound probe | Node exposes counterparts with its own contracts. WireGuard reports its current endpoint, and the outbound probe is opt-in. |
| IPv4 forwarding setup, egress discovery, NAT, and policy routing | Node supports scoped rules, optional default-route egress discovery and explicit opt-in sysctl changes. The image includes `nft`. Existing firewall policy remains authoritative. |
| System statistics | Node exposes host statistics through its host capability. WireGuard runtime counters have their own operation. |
| Shared Xray routing RPCs | PasarGuard returns Unimplemented for its standalone WireGuard backend. |

[Reference service](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto)
· [Reference lifecycle](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/controller.go)
· [Reference WireGuard configuration](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/config.go)
· [Node management API](../../docs/management.md)

## Direct removal and follow-up checks

The optional sampled removal policy keeps the kernel effect and uses
previously accounted usage. The 1,000-peer median fell by more than 70%
against final accounting in two runs. The p95 comparison was mixed.
[Removal timings and accounting policy](interfaces/peer-removal.md)

The previous implementations of peer activity and 100-peer changes were
also rechecked over HTTPS. The 100-peer change led at p95 in this recheck.
Activity p95 at 1,000 peers remained higher.

| Workload | Node p50 / p95, ms | PasarGuard p50 / p95, ms |
| --- | ---: | ---: |
| Activity, 1 peer | 0.207 / 0.700 | 0.206 / 0.715 |
| Activity, 1,000 peers | 0.188 / 0.647 | 0.241 / 0.532 |
| Change a 100-peer chunk | 0.949 / 1.669 | 1.224 / 2.336 |

[Activity recheck](stats/results/peer-activity-removal-check-https.csv)
· [Delta recheck](interfaces/results/peer-delta-removal-check-https.csv)

A candidate decoded peer delta JSON directly from the bounded request body
using the Go standard library. In the controlled 100-peer decode benchmark,
median allocation fell from 53,709 to 27,210 bytes per operation. In the
three-build HTTPS run, the 100-peer change became slower than the existing
Node implementation at both measured percentiles. The candidate was
discarded.

| 100-peer change | p50, ms | p95, ms |
| --- | ---: | ---: |
| Current buffered Node | 0.767 | 1.414 |
| Streaming candidate | 0.874 | 1.722 |
| PasarGuard | 1.059 | 2.259 |

[Controlled decode benchmark](interfaces/results/peer-delta-decoding-linux-amd64.txt)
· [HTTPS experiment](interfaces/results/peer-delta-stream-experiment-https.csv)
