# PasarGuard operation coverage

This maps the 19 gRPC operations in [PasarGuard Node 0.5.7](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto) to the corresponding PapilioVPN Node capabilities. The clients, request shapes and URLs are not interchangeable. The Node operations below are available over HTTP and gRPC. See the [management reference](management.md) and [benchmark index](../benchmarks/README.md).

| PasarGuard operation | PapilioVPN Xray | PapilioVPN standalone WireGuard |
| --- | --- | --- |
| `Start` | `Start` with proposed source, initial accounts and a lease | `Start` with initial peers, live config and a lease |
| `Stop` | `Stop` | `Stop` |
| `GetBaseInfo` | `GetStatus` | `GetStatus` |
| `GetLogs` | `GetLogs` | `GetLogs` for managed lifecycle events |
| `GetSystemStats` | `HostService.GetSystemStats` | `HostService.GetSystemStats` |
| `GetBackendStats` | `GetRuntimeStats` | `GetRuntimeStats` |
| `GetStats` | `GetTraffic`, `ListTraffic` and explicit counter, pair or scope resets | Peer and interface usage reads and resets |
| `GetOutboundsLatency` | `GetOutboundHealth` reads sampled Observatory delay | `ProbeEgress` measures the configured egress target |
| `GetUserOnlineStats` | `GetOnlineIPCount` | `GetPeerActivity` |
| `GetUserOnlineIpListStats` | `ListOnlineIPs` | `ListPeerEndpointIPs` |
| `SyncUser` | `SyncUserStream` over gRPC, single-account or chunked sync over HTTP | Targeted peer mutations or a bounded peer delta stream |
| `SyncUsers` | `SyncUsers` | `SyncPeers` |
| `SyncUsersChunked` | `SyncUsersChunked` | `ApplyPeerDeltaStream` over gRPC and chunked NDJSON over HTTP |
| `ListRoutingRules` | `ListRoutingRules` | Not a WireGuard backend operation |
| `GetBalancerInfo` | `GetBalancerInfo` | Not a WireGuard backend operation |
| `TestRoute` | `TestRoute` | Not a WireGuard backend operation |
| `AddRoutingRule` | `AddRoutingRule` | Not a WireGuard backend operation |
| `RemoveRoutingRule` | `RemoveRoutingRule` | Not a WireGuard backend operation |
| `OverrideBalancerTarget` | `OverrideBalancerTarget` | Not a WireGuard backend operation |

## Differences clients should account for

- Node reports Linux boot uptime in host stats and engine uptime in runtime stats. Host stats include storage and report the timestamps and windows of CPU and network observations.
- WireGuard peer operations take public keys. Core holds any user-to-key mapping. Use the peer API to change membership on an active interface. Changing `initial_peers` and reissuing Start is not an implicit teardown. Xray's native routing API does not apply to standalone WireGuard.
- Node does not promise PasarGuard’s automatic restart after rejected live changes or to finish a Start after the caller cancels. A rejected change can leave the previous execution ready.
- WireGuard live setting changes preserve the interface and peer set. Node only publishes the new fingerprint after verification. Changes to a key or address can still affect packets in flight. If a partial native change cannot be confirmed, Node omits the fingerprint.
- Enable `management.renew_lease_on_auth_request` to renew active leases on authenticated requests. By default Start renews a lease and other management calls do not.
- Log, sampling, reset and streaming guarantees differ by operation. This mapping is not an API compatibility or across-the-board performance claim.
