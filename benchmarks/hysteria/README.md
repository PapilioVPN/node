# Hysteria 2 server in Node

The opt-in Linux engine manages named Hysteria server processes. Each capability
owns its application service and HTTP and gRPC adapters under
`internal/engines/hysteria/{instances,configs,stats,logs}`. Node composes them
through the Hysteria Fx module on its existing management listeners.

The native fixture is the official Linux amd64 executable from
[`app/v2.14.0`](https://github.com/HyNetworks/hysteria/releases/tag/app/v2.14.0),
source revision `c2b181e0b0e72b077e4b0b7297735af14d1f9929`.
Its asset SHA-256 is
`fc25edff9948708cb00a4ff01f3bccde677660aca050133bb1ad46c6860362d2`.
The runtime checks the reported version and revision on each cold launch.
Those strings are a compatibility check, not a binary integrity check.

## Enable a server

Provision the verified binary, server YAML, and referenced TLS files before
starting Node. The binary is not bundled in the standard image or archive.
Add the following to `config.yml`, or use the commented section of the
[example settings](../../deploy/config.example.yml).

```yaml
hysteria:
  instances:
    - id: edge
      executable: bin/hysteria
      config_file: hysteria-edge.yml
      runtime_directory: /tmp
```

Node resolves relative settings paths against its `config.yml` directory. The
standard Compose file mounts `./config` at `/etc/node` and provides writable
`/tmp`. The Node user must be able to execute the binary, read the source and TLS
files, and write the runtime directory. The process needs Linux procfs visibility
of its own child. On a native install, an existing private directory owned by
the Node user can replace `/tmp`.

Use a unique ID and distinct UDP and Stats listeners for each instance. Keep
the Stats listener on numeric IPv4 loopback with its own private secret. Remote
Node management requires TLS with an API key or a client CA. Client traffic goes
to the Hysteria UDP listener, not to Node's management listeners. Without any
instances, HTTP routes and gRPC services are not registered. Starting Node does
not start the Hysteria child. Start it with the execution operation below.

## Operations

HTTP suffixes are relative to `/v1/hysteria/instances/{id}`. gRPC services
are in `papiliovpn.node.v1`. The two transports call the same capability
services and use Node's existing go-bridge authentication at registration.

| Operation | HTTP | gRPC | Result and native effect |
| --- | --- | --- | --- |
| Status | `GET` base | `HysteriaInstancesService.GetStatus` | Observed lifecycle state, version and confirmed digest while ready |
| Start or replace | `PUT /execution` | `HysteriaInstancesService.Start` | Exact YAML bytes or configured file, waits for readiness |
| Stop | `DELETE /execution` | `HysteriaInstancesService.Stop` | Reaps child before success. Repeated Stop is safe |
| Candidate check | `POST /config-validations` | `HysteriaConfigsService.ValidateConfig` | Node preflight result, no launch or application |
| Live logs | `GET /logs` | `HysteriaLogsService.GetLogs` | NDJSON or server stream of allowlisted events from this execution |
| Runtime | `GET /stats/runtime` | `HysteriaStatsService.GetRuntimeStats` | Execution number, readiness timestamp and whole seconds since readiness |
| All traffic | `GET /stats/traffic` | `HysteriaStatsService.ListTraffic` | One native `GET /traffic`, ordered by ID |
| Traffic for one ID | `GET /stats/traffic?user_id=…` | `HysteriaStatsService.GetTraffic` | Selects one ID from one native snapshot. No entry returns not found |
| Traffic reset | `POST /stats/traffic/resets` | `HysteriaStatsService.ResetTraffic` | One native `GET /traffic?clear=1`, returns pre-clear snapshot with `reset_scope=instance` |
| Online users | `GET /stats/online/users` | `HysteriaStatsService.ListOnlineUsers` | One native `GET /online`, device counts by native auth ID |
| Disconnect request | `POST /stats/online/disconnections` | `HysteriaStatsService.DisconnectUsers` | One native `POST /kick`, acknowledges registration rather than disconnection |

The HTTP Start and ValidateConfig bodies are raw YAML (JSON is also a YAML
representation). An omitted body or omitted gRPC `config_yaml` selects the
configured file. A present empty gRPC value is invalid. HTTP disconnect takes
`{"user_ids":["<stable Core ID>"]}`. The reset has no selector and takes no body.
Stats integers are decimal strings in HTTP JSON and native-width integers in
protobuf. Native TX counts client upload and RX counts client download. Counters
are in bytes, local to one execution, and volatile. The runtime operation does
not report Go heap, CPU or RSS and makes no Traffic Stats API call.
[Native TCP and UDP accounting](https://github.com/HyNetworks/hysteria/tree/app%2Fv2.14.0/core/server)

## Readiness, validation and native limits

- `Start` writes the exact source bytes to a temporary mode-0600 YAML file. The
  child runs in the original config directory for relative paths. Node waits for
  the pinned executable's post-`server.NewServer` event and an authenticated
  `/online` response on the same child's owned TCP listener. Linux procfs
  confirms the listener inode belongs to the child before Node sends its secret.
  The native application constructs the QUIC listener before that event. This is
  a local readiness observation, not an end-to-end check of a client credential,
  Core, an outbound, or continually fresh state. A TCP banner alone is not
  readiness. [Pinned CLI](https://github.com/HyNetworks/hysteria/blob/app%2Fv2.14.0/app/cmd/server.go)
  [Pinned QUIC startup](https://github.com/HyNetworks/hysteria/blob/app%2Fv2.14.0/core/server/server.go)
- SHA-256 is published only after readiness and temporary source removal. It
  identifies the exact submitted YAML bytes, not referenced certs, ACL files,
  or application state. Replaying the same bytes against the active execution
  keeps that process and digest. Changed bytes use a full stop and restart after
  preflight. A subsequently failed native start has no rollback to the previous
  execution. When start fails or is canceled, the child is terminated and reaped
  and the temporary file is removed. Stop sends SIGTERM and escalates if needed.
  There is no automatic restart, lease, or persisted applied state.
- The pinned CLI exposes **no server validation-only mode**. Its internal
  builder opens UDP, can configure native resources and starts the Stats HTTP
  listener. Node never calls it from `ValidateConfig`. The API returns
  `scope=node_preflight` and checks a single YAML mapping, unambiguous managed
  keys, listener shape, loopback Stats address and secret, supported auth shape,
  and readability and parsing of the local TLS pair and optional client CA.
  It does not prove native acceptance of all settings, certificate validity or
  trust on the client, port availability, API or Core reachability, or traffic.
  A test shows invalid native `obfs` passes preflight and is rejected by Start.
  [Pinned CLI](https://github.com/HyNetworks/hysteria/blob/app%2Fv2.14.0/app/cmd/server.go)
- This managed profile uses one numeric UDP listen address per instance (or the
  native `:443` default), a file-backed TLS pair, and a nonempty native Stats
  secret on a numeric IPv4 loopback listener. It does not manage Realms, port
  hopping, ACME, Mimic, command authentication, file masquerade or separate
  masquerade TCP listeners. Other native options outside the managed projection
  pass through unchanged and are evaluated by the child during Start. A
  successful preflight does not validate all of those options.
- Native reset clears the entire in-memory table for the selected execution, not
  a user, an online map or other Node instances. Single-ID reads still transfer
  and decode the full table. The native API has no cursor or targeted lookup.
  Reads are limited to 1 MiB of native JSON and at most eight concurrent native
  calls across the module. Reset and kick are not retried after dispatch. A
  lost, invalid or out-of-bound mutation response can mean the effect is already
  applied. Node reports an unknown mutation effect instead of implying rollback.
  [Native Traffic Stats API](https://hy2.app/docs/advanced/Traffic-Stats-API/)
- A device is one authenticated Hysteria client connection, not a TCP proxy
  stream. Native `kick` inserts one pending marker per exact ID. The next
  accounted traffic for that ID consumes it and closes that connection. An
  idle device may remain online. An acknowledgement neither proves that all
  devices disconnected nor blocks later authentication. Multiple disconnect
  requests can coalesce before the marker is consumed. Core owns revocation.
  [Pinned traffic logger](https://github.com/HyNetworks/hysteria/blob/app%2Fv2.14.0/extras/trafficlogger/http.go)
- Logs contain fixed names such as `client_connected` and `client_disconnected`,
  execution and sequence numbers, and a server observation timestamp. Node
  never publishes native diagnostic text, addresses, identity fields, config
  contents, or token values. No backlog is kept. The 16 simultaneous subscribers
  each have a 64-event queue, with sequence gaps on overrun. Streams end with
  execution termination or management shutdown.

### Core HTTP authentication

Set the Hysteria config's `auth.type` to `http` and `auth.http.url` to the
Core-owned callback. The pinned client on the server sends a JSON HTTP
`POST` carrying `addr` (remote host and port), `auth` (the credential), and
`tx` (an unsigned negotiation value, not the traffic counter). Core returns
HTTP 200 with `{"ok":true,"id":"<stable opaque account ID>"}` or
`{"ok":false,"id":""}`. Bind the policy to the credential at Core and use the
returned ID for usage, online observations and disconnect. Native shared
password mode reports all accepted sessions under the same ID, `user`. Use
`userpass` or Core HTTP auth for distinct IDs. Do not return the
credential as the ID. The native mode does not inject arbitrary auth headers.
Node accepts HTTPS with verification or plaintext HTTP to numeric loopback.
The successful authorization, usage attribution, kick and reconnect-denial
tests use a contract fixture. They do not establish those effects against Core.
[Pinned HTTP authenticator](https://github.com/HyNetworks/hysteria/blob/app%2Fv2.14.0/extras/auth/http.go)

The live Core revision `a572c2a46fd223248c5d29e3f593bdbd2c764a74` was tested with
fresh PostgreSQL and Redis in an isolated Docker network. Core reported healthy
and exposed no Hysteria authorization operation in its route inventory. A POST
to the fixture callback path `/internal/hysteria-auth` returned 404. Core's
administrative password login returned 422 for the native Hysteria request body
and is not a substitute. Node started the official server. The official client
was rejected during authentication, no online device or traffic row appeared,
and Stop succeeded. Successful access, usage attribution and policy-driven
revocation against Core require a dedicated Core callback before they can be
tested end to end.

## Reproduction and results

Run on Linux with a verified official binary. These commands create a private
network namespace with only loopback. Tests start the official server and
client executables, and the client forwards TCP through QUIC to the local
fixture. The reply is deliberately twice the upload size, which also tests the
directions of the native counters.

```sh
export NODE_TEST_HYSTERIA=/absolute/path/to/verified/hysteria-linux-amd64
unshare -Urn bash -ec '
  ip link set lo up
  go test -race ./internal/engines/hysteria/... -count=3
  go test -race ./internal/bootstrap ./internal/settings -run Hysteria -count=3
  go test ./internal/engines/hysteria/integration -run "^$" \
    -bench "^(BenchmarkLifecycle|BenchmarkTrafficAPI)$" -benchmem -benchtime=200ms -count=3
'
```

The native suite passed three repetitions with the race detector. Both slices
started with public behavior failures (missing HTTP routes and an unregistered
gRPC statistics service) before implementation. The suite checks occupied native
sockets, cancellation and reaping,
a ready-only exact digest, validation without application, replacing and
replaying sources, bidirectional traffic, two streams on one device, two
devices for one ID, whole-execution reset across two IDs, native kick and
successful reconnect, independent Core denial, logs ending at Stop, and listener
cleanup. It checks both public transports with the same pinned native executable.

The bootstrap test loads `config.yml`, starts a real child with the authenticated
gRPC listener, checks the exact digest and HTTP management authentication, opens
both public log streams, and shuts down Node without sending a separate Stop.
It verifies both streams end, both native sockets are released, and the temporary
source is removed. The disabled case checks HTTP 404 and an unimplemented gRPC
service. These tests also catch descriptor name collisions between engines at
process initialization.

A local Linux amd64 package build also passed. In a container from the archive
with no external network, no added capabilities, and a read-only config mount,
Node started the operator-supplied executable over HTTP, returned `ready` over
gRPC, and stopped it over HTTP. The HTTP Start digest matched the mounted YAML.

The table shows the median of three **run means** in ms per operation, not a
request percentile. Linux amd64, Go 1.27.4, AMD Ryzen 7 3750H. Node HTTP and
direct HTTP used local plaintext keep-alive. Public gRPC used TLS and go-bridge
API-key auth. Native comparison uses the exact same server process. Read rows
use one nonempty traffic row or one online device. Reset measures an empty
table and disconnect measures registration for one offline ID. Actual reset
and disconnect effects are covered by the integration tests, not timed in this
table. The full allocation data and noisy repeats are in
[raw Go output](results-linux-amd64.txt).

| Operation | Node HTTP | Node gRPC | Direct native HTTP | Direct fresh TCP control |
| --- | ---: | ---: | ---: | ---: |
| Status | 0.236 | 0.302 | — | — |
| Same-source Start | 0.259 | 0.310 | — | — |
| ValidateConfig | 0.547 | 0.623 | — | — |
| Cold Start plus Stop | 37.259 | 37.020 | — | — |
| Runtime | 0.239 | 0.294 | — | — |
| ListTraffic | 0.601 | 0.633 | 0.208 | — |
| GetTraffic for one ID | 0.615 | 0.650 | — | — |
| ListOnlineUsers | 0.510 | 0.637 | 0.545 | — |
| Reset empty traffic table | 0.781 | 0.949 | 0.228 | 0.482 |
| Disconnect acknowledgement | 0.752 | 0.823 | 0.199 | 0.435 |

Direct Hysteria led for traffic listing, reset and disconnect registration.
Online measurements were noisy. Node HTTP had a slightly lower median in this
run, but the direct samples varied and do not support a latency claim.
Node adds a management hop, execution ownership and typed projection. The fresh TCP control accounts for
part of the mutation gap and does not include Node's procfs identity checks.
There is no direct native equivalent of Node's scoped runtime, candidate check,
single-ID read or confirmed lifecycle. The pinned PasarGuard baseline has Xray
and WireGuard backends and no Hysteria backend to benchmark.
[Reference backend directory](https://github.com/PasarGuard/node/tree/739eab94a9343d9f0035baf7162796844109f4c4/backend)

## Build and CI integration

The shared Checks job verifies the binary checksum, builds the engine and
bootstrap race-test executables, and runs both as the runner user in a private
Linux network namespace with only loopback enabled. The regular repository
checks still run separately. The versioned gRPC schemas can be regenerated with
`go generate ./internal/engines/hysteria/...` using protoc 33.0,
protoc-gen-go 1.36.12 and protoc-gen-go-grpc 1.6.2.
