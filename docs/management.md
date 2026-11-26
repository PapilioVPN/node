# PapilioVPN Node · Management APIs

[PapilioVPN Node](../README.md) · [PasarGuard operation mapping](pasarguard-coverage.md)

## Capabilities

| Area | Available operations |
| --- | --- |
| Node readiness | Check whether the Node management listeners are serving over HTTP and gRPC. |
| Host uptime | Read the elapsed time since Linux boot in whole seconds over HTTP and gRPC. |
| Host memory | Read total and available RAM, plus total and free swap, in bytes over HTTP and gRPC. |
| Host CPU | Read aggregate CPU utilization over HTTP and gRPC. |
| Host storage | Check space and read-only status for the filesystem backing a configured path. |
| Host system stats | Fetch uptime, CPU count and usage, memory, storage, and received and transmitted bytes per second in one response. |
| Xray instance | Start, stop, and read state and versions over HTTP and gRPC. Start accepts typed initial accounts over both transports and supports keep-alive, compatible live configuration changes, and restart recovery. |
| Xray config validation | Check a proposed Xray JSON configuration or the configured file over HTTP and gRPC. |
| Xray runtime statistics | Read the active instance’s uptime, memory allocation, goroutines, and garbage collection counters over HTTP and gRPC. |
| Xray traffic resets | Read and clear one counter, both directions of a named account or tag, or a bounded scope over HTTP and gRPC when explicitly enabled. |
| Xray traffic counters | Read upload and download bytes for one or all accounts, inbound tags, or outbound tags over HTTP and gRPC. |
| Xray online accounts and IPs | List online accounts, count distinct source IPs, or read their last-seen times over HTTP and gRPC. |
| Xray routing | Read active rules, test a route, edit rules and inspect or override a balancer over HTTP and gRPC. |
| Xray accounts | Synchronize named accounts, full lists, or chunked deltas across selected inbounds over HTTP and gRPC, with confirmed membership restored on managed restart. Optional durable recovery also survives a Node restart. Sync skips inbounds listed in `exclude_inbounds` when the execution starts. Supports VLESS, VMess, Trojan, Shadowsocks, supported Shadowsocks 2022 servers, Hysteria, MASQUE, and Xray WireGuard. VLESS also supports direct add, remove, and count. |
| Xray log stream | Follow live stdout and stderr for the active Xray execution over HTTP and gRPC. |
| Xray outbound health | Read all recorded Observatory samples or select an outbound by tag over HTTP and gRPC. |
| WireGuard interface | Read basic or detailed status, validate configuration, start with initial peers, stop, apply compatible live changes, and recover across Node restarts. HTTP and gRPC. |
| WireGuard peers | List live membership, reconcile the full set, upsert or remove one peer, or apply a bounded set of changes by public key over HTTP and gRPC. |
| WireGuard statistics | Read traffic, usage, last handshake, peer activity and observed endpoint IPs over HTTP and gRPC. |
| WireGuard diagnostics | Follow lifecycle events, read runtime statistics, and probe an operator-configured egress target over HTTP and gRPC. |
| WireGuard host networking | Optionally manage IPv4 forwarding rules, NAT and policy routing for a managed interface. |
| OpenVPN server | Manage the execution, validate profiles, list sessions and counters, disconnect sessions, follow logs, and apply signed certificate revocations over HTTP and gRPC. |
| Hysteria 2 server | Manage named Linux servers, preflight candidate YAML, follow redacted logs, observe runtime, traffic and online device counts, reset instance counters, and request disconnection over HTTP and gRPC. [Setup and operation contract](../benchmarks/hysteria/README.md) |

<a name="management"></a>

## Management APIs

HTTP `GET /v1/host/uptime` returns `uptime_seconds`. gRPC `HostService.GetUptime` returns the same quantity using the [Host API contract](../internal/host/bridge/grpc/proto/v1/host.proto). Both read the boot clock visible to Node's Linux time namespace.

HTTP `GET /v1/host/memory` and gRPC `HostService.GetMemory` read the Linux `/proc/meminfo` visible to Node. Counts are in bytes. `available_bytes` is the kernel's estimate of RAM available without swapping and is present when the kernel reports it. `used_bytes` is total minus available and is omitted if the kernel does not report a valid available value.

HTTP `GET /v1/host/cpu` and gRPC `HostService.GetCPU` serve one shared aggregate Linux CPU sample, updated about once per second. It includes `busy_percent`, `window_milliseconds`, `observed_at`, and `steal_percent` when Linux reports it. I/O wait and stolen time are excluded from busy time. While a valid interval is unavailable, HTTP returns `503` and gRPC returns `UNAVAILABLE`.

HTTP `GET /v1/host/storage` and gRPC `HostService.GetStorage` report `path`, `total_bytes`, `used_bytes`, `available_bytes`, and `read_only`. `available_bytes` excludes space reserved for privileged use. Set `host.storage_path` to an absolute path on the volume you want to monitor. The default is `/` in Node's mount namespace. In Docker, use the path visible inside the container. When the configured path cannot be inspected, HTTP returns `503` and gRPC returns `UNAVAILABLE`.

HTTP `GET /v1/host/stats` and gRPC `HostService.GetSystemStats` return `uptime_seconds`, `cpu`, `memory`, and `storage` using the individual operations' data and units. They also return `logical_cpu_count` from Linux and a `network` sample with `rx_bytes_per_second`, `tx_bytes_per_second`, `window_milliseconds`, and `observed_at`. Node samples interfaces in its network namespace in the background and excludes loopback. Unavailable fields are omitted, including `cpu` and `network` until their first valid samples.

Set `xray.id`, `xray.executable`, and `xray.config_file` in the [example configuration](../deploy/config.example.yml) to enable one instance. Use Xray v26.11.9 and an operator-owned JSON file. At launch, Node replaces its `api` block with a private Unix socket for readiness. For Docker, use the bundled Xray executable and the configuration mount described in the [installation guide](install.md#enable-xray). Remote Xray management requires TLS with an API key or client CA.

Use `GET /v1/xray/instances/{id}` for status and `PUT` or `DELETE /v1/xray/instances/{id}/execution` with an empty body to start or stop. The [gRPC contract](../internal/engines/xray/instances/bridge/grpc/proto/v1/instances.proto) exposes `XrayInstancesService.Start`, `GetStatus`, and `Stop`. Start waits for the native API and Stop waits for the process to exit. To enable a lease, pass `keep_alive_seconds` in the Start request. A successful Start renews it with a five-second grace period. Omission retains the interval and zero disables it.

A ready status includes `applied_config_sha256`, the lowercase hex SHA-256 of the exact accepted JSON source. Compute it over the gRPC `config_json` bytes, the raw HTTP `config` value, or the configured file bytes when Start uses that file. GetStatus and Start return the same recorded value for the ready execution, including the observed status in a rejected Start response. Heartbeats retain it. The field is absent while an execution is unconfirmed or stopped. Independent runtime mutations have their own observation contracts.

A startup failure returns HTTP `503` with a JSON `reason` or gRPC `UNAVAILABLE` with `XrayStartFailure` details. The reasons are `launch_failed`, `exited_before_ready`, `readiness_timeout`, and `readiness_failed`. The response includes `exit_code` when Xray supplied one after exiting before readiness.

Send a raw JSON object in `POST /v1/xray/instances/{id}/config-validations` or pass its bytes as optional `config_json` to `XrayConfigsService.ValidateConfig`. An HTTP request with no body or a gRPC request that omits the field checks the configured file. A present empty gRPC value or a body consisting only of whitespace is invalid. Proposed JSON is limited to 1 MiB. Node runs one validation at a time with a four-second deadline. A proposal does not change the live instance or open its log destinations. Rejected configurations return `valid: false` with a typed issue and source coordinates when reported. Execution failures and timeouts return errors. [gRPC contract](../internal/engines/xray/configs/bridge/grpc/proto/v1/configs.proto)

Use `GET /v1/xray/instances/{id}/stats/runtime` or `XrayStatsService.GetRuntimeStats`. Each response uses a fresh sample from the active Xray execution. Overlapping reads can share the next native sample. Memory fields report bytes, uptime counts whole seconds since the Xray stats service started, and cumulative GC pause is in nanoseconds. A configured instance that is unavailable returns HTTP `503` or gRPC `UNAVAILABLE`. The pending-read buffer holds 128 requests. When it is full, reads return HTTP `429` or gRPC `RESOURCE_EXHAUSTED`. [gRPC contract](../internal/engines/xray/stats/bridge/grpc/proto/v1/runtime_stats.proto) · [Benchmark and raw results](../benchmarks/xray/runtime/README.md#xray-runtime-statistics-after-the-rewrite)

Use `GET /v1/xray/instances/{id}/stats/traffic/{scope}` with `user`, `inbound`, or `outbound`. Add `?name=...` for one account or tag. The gRPC methods are `XrayStatsService.GetTraffic` and `ListTraffic`. Reads leave the counters intact. A missing direction is omitted and a stored zero is returned as zero. A missing named entry returns HTTP `404` or gRPC `NOT_FOUND`. List order is unspecified. Enable traffic statistics in the Xray policy for the selected scope. [gRPC contract](../internal/engines/xray/stats/bridge/grpc/proto/v1/traffic.proto)

Enable `xray.reset_engine_stats: true` to allow traffic resets through the management API. Use `POST /v1/xray/instances/{id}/stats/traffic/{scope}/resets?name=...&direction=uplink` or `XrayStatsService.ResetTrafficCounter`. Use `downlink` to select the other direction. Xray atomically returns the previous byte count and clears the selected counter. An unknown outcome or a lost response requires reconciliation before a repeat.

Use `POST /v1/xray/instances/{id}/stats/traffic/{scope}/pair-resets?name=...` or `XrayStatsService.ResetTrafficPair` for both directions. Xray clears each direction separately. HTTP returns both outcomes in the response body, including when interrupted. gRPC includes partial progress in the status detail. Previous bytes are present only for acknowledged directions.

Use `POST /v1/xray/instances/{id}/stats/traffic/{scope}/scope-resets?max_counters=...` or `XrayStatsService.ResetTrafficScope` for a whole scope. Set the limit from 1 to 1000 directional counters. If the selection exceeds the limit, it is rejected before any reset. Check the response outcome and the result of each counter. gRPC returns an interrupted or unknown report in the response body. Scoped resets run sequentially by default. Set `xray.reset_scope_concurrency` to 2–8 to allow parallel native resets. [Contract and benchmarks](../benchmarks/xray/traffic/scope-reset.md)

For a one-call reset, use the packaged `/node-xray` executable and set `xray.native_scope_reset: true`. The standard Xray executable uses the sequential or configured parallel path.

Use `GET /v1/xray/instances/{id}/stats/online/count?name=...` or `/stats/online/ips?name=...` under the same instance path. The gRPC methods are `XrayStatsService.GetOnlineIPCount` and `ListOnlineIPs`. Enable `statsUserOnline` at the account's policy level. Timestamps are Unix seconds. An account with an empty tracked IP map returns zero or an empty map. A missing observation returns HTTP `404` or gRPC `NOT_FOUND`. [gRPC contract](../internal/engines/xray/stats/bridge/grpc/proto/v1/online.proto)

Use `GET /v1/xray/instances/{id}/stats/online/users` or `XrayStatsService.ListOnlineUsers` to list account names with at least one source IP currently tracked by Xray. The list has no guaranteed order. When there are no online accounts, HTTP returns `{"names":[]}`.

Use `GET /v1/xray/instances/{id}/routing/rules` and `GET /v1/xray/instances/{id}/routing/balancer?tag=...` to read routing state. The gRPC methods are `XrayRoutingService.ListRoutingRules` and `GetBalancerInfo`. The response reports the override separately from the targets reported by Xray. [gRPC contract](../internal/engines/xray/routing/bridge/grpc/proto/v1/routing.proto)

Use `XrayRoutingService.TestRoute`, `AddRoutingRule`, `RemoveRoutingRule` and `OverrideBalancerTarget` to evaluate and modify the live router. The HTTP routes and confirmation for rule replacement are documented with the benchmark. [Routing control](../benchmarks/xray/routing/rule-writes.md)

Use `XrayAccountsService.AddVLESSAccount`, `RemoveVLESSAccount` and `CountVLESSAccounts` or the HTTP routes under `/v1/xray/instances/{id}/inbounds/{tag}/accounts`. These operations apply to the active execution. [VLESS contract](../benchmarks/xray/accounts/vless.md)

[VLESS synchronization](../benchmarks/xray/accounts/sync.md) · [Xray log stream](../benchmarks/xray/logs/contract.md)

To retain confirmed synchronization after a Node restart, set `xray.state_directory` to an existing directory owned by the Node user with mode `0700`. Mount it as writable persistent storage when running in Docker. Node writes the private account state before acknowledging synchronization and restores it before the next `Start` reports ready. Keep the configured instance ID and its compatible inbounds when restarting.

Use `GET /v1/xray/instances/{id}/outbounds/health` or `XrayOutboundsService.GetOutboundHealth` to read ordinary Observatory samples. Provide a tag for one outbound. The response distinguishes healthy, failed and missing samples. [gRPC contract](../internal/engines/xray/outbounds/bridge/grpc/proto/v1/outbounds.proto)

The example binds both listeners to loopback. HTTP `GET /healthz` returns `204 No Content` when ready. The gRPC health service on port 8081 reports `SERVING` for the empty service name. Set `management.tls.cert_file` and `management.tls.key_file` to enable TLS. Optionally configure `management.tls.client_ca_file` for mutual TLS and `management.api_key` for the `x-api-key` header or metadata. By default, management traffic other than Start does not renew a lease. Set `management.renew_lease_on_auth_request: true` to let authenticated HTTP and gRPC requests renew all active Xray and managed WireGuard leases on this Node. Requests do not create leases or restart stopped engines. Authentication is required to enable this option.

Set `interface` under `wireguard` in the [example configuration](../deploy/config.example.yml). Use `GET /v1/wireguard/interfaces/{name}` or `WireGuardInterfacesService.GetStatus` to read the configured interface in Node’s Linux network namespace. The response contains `name`, `mtu` in bytes, and `up` from the kernel’s administrative `IFF_UP` flag. An absent, unconfigured, or non-WireGuard interface returns HTTP `404` or gRPC `NOT_FOUND`. A concurrent read can return HTTP `429` or gRPC `RESOURCE_EXHAUSTED` while the socket is in use. Remote access requires TLS with an API key or client CA. [gRPC contract](../internal/engines/wireguard/interfaces/bridge/grpc/proto/v1/interfaces.proto)

For the effective configuration, send `?include_configuration=true` over HTTP or set `include_configuration` on the gRPC request. The optional `configuration` object contains the effective `listen_port`, assigned `addresses` and the `public_key` when configured. The kernel read requires `CAP_NET_ADMIN`.

Enable `wireguard.managed: true` for kernel lifecycle management. The Node process needs Linux `CAP_NET_ADMIN` in the interface's network namespace. Send a raw JSON object to `PUT /v1/wireguard/interfaces/{name}/execution` or pass its bytes to `WireGuardInterfacesService.Start`. The source takes a base64-encoded 32-byte `private_key`, one to 16 unicast CIDRs in `addresses`, optional `mtu` (default 1420), and optional `listen_port` (omitted or zero selects a kernel-assigned port). The source limit is 1 MiB. Start and GetStatus include `applied_config_sha256` after confirmation. Repeating the exact source checks the owned link and returns the same hash. Include `initial_peers` using the same peer shape as full sync to install the initial set before confirming Start. For a live change, resend the complete source with one or more changed settings among MTU, private key, addresses and port. Change peers on an already running link with the peer APIs rather than changing `initial_peers`. The link and its current peers stay in place. Start returns the new fingerprint after kernel confirmation. Unsupported live changes or another owner’s interface return HTTP `409` or gRPC `FAILED_PRECONDITION`. If an unsuccessful update leaves the previous configuration confirmed, its fingerprint remains available. When the result cannot be confirmed, GetStatus omits the fingerprint and Stop remains available. Stop uses `DELETE` on the execution resource or the `Stop` RPC. Node removes its own interface on explicit Stop. The default graceful shutdown also removes it.

WireGuard Start accepts optional `keep_alive_seconds` as an HTTP query parameter or a gRPC field. A successful Start renews the lease with a five-second grace period. Omit the field to retain the interval or send zero to disable it. Status reads renew a lease only if request-based renewal is enabled. Node stops the owned link on expiry. The active deadline is reported as `keep_alive_expires_at` and is process-local.

A same-source Start repairs the MTU and administrative state of a still-owned link in place. If the link has disappeared during the current execution, Node recreates it from the submitted source and returns `recreated: true`. This restores the configured initial peers. Controllers can then resync peers added later. Node never replaces a foreign link. WireGuard Start and GetStatus report the Node version and kernel release, plus the version of `wg` when installed. The live `interface_index` lets a controller detect link replacement within the same boot and network namespace after a lost reply.

Send the proposed JSON to `POST /v1/wireguard/interfaces/{name}/config-validations` or pass its bytes to `WireGuardInterfacesService.ValidateConfig`. Validation uses the same input checks as Start with a 1 MiB limit and one concurrent check per configured interface. It also works with read-only interface configuration. A rejected source returns `valid: false` and a typed issue for JSON, private key, addresses, duplicate addresses, MTU, listening port or initial peers. Replies contain issue codes only. Dry-run performs source checks in memory. Port availability and kernel readiness are checked at Start.

Set `wireguard.state_directory` to an existing dedicated directory owned by Node with mode `0700` to enable recovery. Node leaves its owned interface running during a process restart and records its ownership and confirmed source metadata in a `0600` file. The raw private key and JSON stay out of the file. Recovery checks the boot, network namespace, link identity, administrative state and MTU before publishing the recorded fingerprint. An owned interface with an unconfirmed config is available to Stop. Explicit Stop also removes the checkpoint. Recovery of the same live interface applies within one kernel boot and network namespace.

Use `GET /v1/wireguard/interfaces/{name}/peers` or `WireGuardInterfacesService.ListPeers` for live public keys and allowed IPs. Reads work in read-only mode and require `CAP_NET_ADMIN`. The kernel dump is bounded and preshared keys stay private.

Set `wireguard.peer_preshared_key_file` to a private file containing one base64-encoded 32-byte key. Node applies it to initial peers and later peer mutations that omit a PSK. An explicit per-peer key overrides the default. An explicit 32-byte all-zero key removes the PSK. The file is loaded at process startup and its contents are kept out of checkpoints and API responses.

Send the full desired set to `PUT /v1/wireguard/interfaces/{name}/peers` with a `peers` array, or call `WireGuardInterfacesService.SyncPeers` with a present `desired` message. Use an explicit empty set to remove all peers. Each peer has a 32-byte `public_key`, up to 16 IPv4 or IPv6 `allowed_ips` prefixes, and an optional 32-byte `preshared_key`. HTTP keys use base64. Omit the PSK to request a peer without a PSK. The set supports up to 1,000 peers. Keys and normalized prefixes must be unique. Node writes only the differences using bounded kernel batches. A failed native batch can leave partial changes. The next request reads the kernel before retrying. Replies contain counts rather than key material.

Use `PUT /v1/wireguard/interfaces/{name}/peers/{key}` to upsert one peer and `DELETE` on the same resource to remove it. The path key is unpadded base64url. The PUT body takes `allowed_ips` and optional `preshared_key` and replaces both values for that peer. Deleting an absent peer also succeeds with HTTP `204`. The matching gRPC methods are `UpsertPeer` and `RemovePeer`.

Managed WireGuard defaults to `wireguard.peer_removal_usage: final`. Before a peer removal, Node attempts a fresh read for outstanding usage. Set `peer_removal_usage: sampled` to remove peers without waiting for a device dump. This keeps pending usage from the last published sample. Traffic between that sample and removal can go unrecorded. A sample that overlaps a peer removal is discarded. This setting applies to HTTP and gRPC peer removals, including removals within full syncs and deltas. Interface Stop still attempts a final sample.

Send peer mutations on a managed interface through Node. Full sync, targeted upserts, and chunks check link ownership and plan from the initial kernel snapshot and acknowledged Node writes. The first request after restart or an unconfirmed write rebuilds this state. To reconcile after an out-of-band `wg set`, submit the full desired set with `reconcile: true` in the HTTP JSON body or set `reconcile` on the gRPC `SyncPeers` request. That request reads the live kernel configuration before applying changes.

Use `PATCH /v1/wireguard/interfaces/{name}/peers` or `ApplyPeerDelta` to submit one independent chunk with `upsert` peer records and `remove` public keys. Split larger changes into requests of 1 to 1,000 operations. Each key may appear once per chunk. An exact prefix cannot be taken from a peer outside the chunk. New additions must keep the managed set at or below 1,000 peers. A conflict returns HTTP `409` or gRPC `FAILED_PRECONDITION`. Upserts and chunks return `added`, `updated`, and `removed`. If a kernel write has an unconfirmed outcome, the caller can resend the same chunk to reconcile against live state. Each chunk has its own acknowledgment, and the kernel provides no rollback across writes.

Use the gRPC `ApplyPeerDeltaStream` method to collect one delta across chunks. Send the same interface name in every chunk, with contiguous indices starting at zero. Set `last` on the final chunk and half-close the stream. Node validates and begins applying the combined delta only after the clean half-close. The stream requires a deadline of at most 30 seconds and accepts up to 1,000 changes and 1 MiB of protobuf data. One collector can run per interface. Kernel writes still retain the partial-outcome contract of `ApplyPeerDelta`.

Over HTTP, use `PATCH /v1/wireguard/interfaces/{name}/peers/chunks` with `Content-Type: application/x-ndjson`. Send one object per line with a contiguous `index` starting at zero, `upsert` and/or `remove`, and `last: true` on the final object. Use the same peer shapes as `PATCH /peers`. Node waits for a clean body EOF before applying the combined delta and returns `added`, `updated`, and `removed`. Both transports share one collector per interface. The HTTP request is limited to 30 seconds, 1 MiB and 1,000 changes. A malformed, interrupted, or incomplete upload never starts a kernel write. Once admitted, native writes have the same partial-outcome contract as `ApplyPeerDelta`.

Use `GET /v1/wireguard/interfaces/{name}/peers/stats` or `WireGuardInterfacesService.ListPeerStats` to read a live list from the configured interface. The process needs `CAP_NET_ADMIN` in that network namespace. Reads also work when lifecycle management is disabled. Each entry contains `public_key`, cumulative `rx_bytes` and `tx_bytes`, and `last_handshake` when a handshake has occurred. HTTP uses base64 keys, decimal strings for the unsigned 64-bit counters, and a UTC RFC 3339 timestamp. gRPC uses bytes, uint64 counters, and a protobuf timestamp. A successful read preserves the counters and returns an empty list when the interface has no peers. Each read has a two-second deadline and returns up to 4,096 peers. An interrupted or oversized dump returns an error for the request.

The HTTP paths below are relative to `/v1/wireguard/interfaces/{name}`. Peer path keys use unpadded base64url.

| HTTP | RPC | Result |
| --- | --- | --- |
| `GET /peers/{key}/activity` | `WireGuardStatsService.GetPeerActivity` | Sampled traffic activity and last observation time, with a 45-second activity window. |
| `GET /peers/{key}/endpoint-ips` | `WireGuardStatsService.ListPeerEndpointIPs` | The last observed kernel endpoint IP, when available. |
| `GET /logs` | `WireGuardDiagnosticsService.GetLogs` | Recent and live lifecycle events. HTTP uses NDJSON and gRPC uses a server stream. |
| `GET /runtime` | `WireGuardDiagnosticsService.GetRuntimeStats` | Go process counters and uptime for the confirmed WireGuard execution. |
| `GET /egress-probe` | `WireGuardDiagnosticsService.ProbeEgress` | Reachability and HTTP status from one HEAD request over the operator-selected link. Set `wireguard.probe.url`, with optional `interface` and `timeout_ms`. |

For IPv4 host networking, configure `wireguard.routing` with managed mode enabled. The image includes `nft`. Native installs require it on the Node process's PATH. Omit `egress_interface` to use the lowest-metric unambiguous main-table IPv4 default route. IPv4 forwarding must already be enabled or explicitly requested through `enable_ipv4_forwarding: true`. This changes the sysctl in Node’s network namespace and stays enabled after Stop. Node owns scoped firewall and optional policy routing rules and removes them on explicit Stop. [Settings and verification](../benchmarks/wireguard/routing/routing.md)

## OpenVPN server

See the [OpenVPN management contract](openvpn.md) for Linux requirements, APIs and profile rules.
