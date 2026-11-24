# Live VLESS inbound accounts

## Management contract

| Operation | HTTP | gRPC on `XrayAccountsService` |
| --- | --- | --- |
| Add | `POST /v1/xray/instances/{id}/inbounds/{tag}/accounts` with JSON `email` and `uuid` | `AddVLESSAccount` |
| Remove | `DELETE /v1/xray/instances/{id}/inbounds/{tag}/accounts` with JSON `email` | `RemoveVLESSAccount` |
| Count | `GET /v1/xray/instances/{id}/inbounds/{tag}/accounts/count` | `CountVLESSAccounts` |

HTTP mutations return 204 and gRPC mutations return an empty message. Count
returns the native signed 64-bit count of named users for the exact VLESS
inbound. Xray omits accounts configured with an empty email from this count.
Add accepts a canonical UUID string and uses the native default level and
empty flow. Xray compares email case-insensitively. Its effective VLESS
identifier ignores UUID bytes 6 and 7, so Node rejects collisions after applying
that same comparison. Remove selects one account by email. Mutations apply to
the current execution, and removing an account affects authentication on new
connections. Node keeps request credentials and raw native error messages out
of management responses and its logs. External Xray log files follow the
operator's Xray configuration.
[Public schema](../../internal/engines/xray/accounts/bridge/grpc/proto/v1/accounts.proto) ·
[Xray VLESS validator](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/proxy/vless/validator.go)

Both APIs use one membership service. The Xray module wires it into the
management listeners and `configs/runconfig` enables `HandlerService` on
the existing private readiness socket. The first account operation per
execution loads the native inbound types and configured account identities
in one `ListInbounds` call. It keeps fingerprints for matching, including
the effective IDs of anonymous configured accounts. The private socket
and the Node account adapter own live changes to that inventory. The
execution's active connection identifies the inventory. When Xray restarts,
the next account operation reads the replacement execution's configuration.
A warm mutation makes one `AlterInbound` call. A warm count makes one
`GetInboundUsersCount` call. Counts come from Xray and its scan grows with the
number of named users. The initial inventory receives at most 16 MiB and
supports up to 1,024 inbound records and 65,536 VLESS account records.
[Native HandlerService](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/proxyman/command/command.go) ·
[Node adapter](../../internal/engines/xray/accounts/native/client.go)

There is one active account mutation at a time, up to 32 admitted account
operations total and a one-second operation deadline. An uncertain native
acknowledgement returns an unknown outcome and fences mutations for that
inbound until execution replacement. Native counts remain available. Duplicate
email or effective UUID and removal of an absent email have distinct results.
The operations provide the primitives for later synchronization work.
[Native boundary tests](../../internal/engines/xray/accounts/native/client_test.go)

With real Xray v26.11.9, both HTTP and gRPC exercised count 0 → 1 → 0, valid
VLESS traffic to a loopback destination, and rejection of fresh connections
after removal. The fixture uses a scoped loopback allowance in the freedom
outbound's final rules and disables native log output. It also checks
protocol mismatch, duplicate identifiers, case-insensitive removal, credential
redaction and, over gRPC, replacement of the execution.
[Real Xray integration](../../internal/engines/xray/accounts/integration/accounts_test.go)

## Benchmark method

Both Node binaries and the driver used Go 1.27.3 with CGO disabled. Both
nodes ran the checksum-verified Xray v26.11.9 executable at commit
`676eaf6`. PasarGuard Node 0.5.7 came from `739eab9`. Its compiled Xray
client SDK was `v1.260327.1-0.20261021115930-1b1647cd9ddf`. PapilioVPN uses
the v26.11.9 wire projection. The isolated network namespace used loopback.
Servers and Xray children shared CPU 2 and the driver used CPU 4, a separate
physical core. All processes used `GOMAXPROCS=1`. The host was a shared AMD
Ryzen 7 3750H Linux machine. Responses were read on persistent TLS
connections for both public APIs. HTTP used HTTP/1.1.
[PasarGuard revision](https://github.com/PasarGuard/node/commit/739eab94a9343d9f0035baf7162796844109f4c4) ·
[Xray release](https://github.com/XTLS/Xray-core/releases/tag/v26.11.9)

Each round used one running VLESS inbound and a local freedom outbound with
no proxy traffic. The two nodes started from the same account configuration
and alternated measurement order. One client timed adding 256 distinct
accounts and removing those same 256 accounts. Each target first performed
one warm-up add/remove. The driver checked actual counts via the active
Xray's native API before warm-up and after warm-up add, warm-up removal,
timed additions and timed removals. PasarGuard's debug mode wrote a temporary
fixture config so the driver could find its native API listener. The fixture
used synthetic identities and discarded its temporary configs and logs.

PasarGuard's one-inbound `SyncUser` calls produced the same additions and
removals in this workload. Addition selected the only inbound and supplied
a VLESS UUID. Removal supplied the email with the inbound omitted. In
PasarGuard this is desired membership reconciliation and a gRPC call is a
one-message client stream. In PapilioVPN the calls are separate unary
operations, with preflight and outcome handling. The benchmark covers
successful transitions where the account is absent before addition and
present before removal. Duplicate, absent-removal and failure semantics
differ across the two products.
[PasarGuard reconciliation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/user.go#L236-L298) ·
[PasarGuard gRPC handler](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/rpc/user.go#L17-L40)

PasarGuard exposes no per-inbound membership count. For that operation,
the driver compared Node's public routes to plain gRPC
`GetInboundUsersCount` on the same running Xray's private Unix socket.
Each count window ran for 550 ms with 1 or 8 closed-loop clients and a warm-up
request per client. The driver verified the same count of 256 in every reply.
The native baseline measures the cost of the backend call itself. The public
measurement includes the additional management transport, TLS and admission.
[PasarGuard service schema](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto)

## Results

On 2026-11-23, each figure below used the median of seven per-round metrics.
A slash separates PapilioVPN and the reference named in the row. Both
mutations led PasarGuard on all three median metrics in both transports.
Direct Xray count had lower latency and higher throughput in all count
workloads.

| Transport | Operation | Clients | Reference | p50 µs | p95 µs | Successful requests/s |
| --- | --- | ---: | --- | ---: | ---: | ---: |
| HTTP | Add | 1 | PasarGuard | 785 / 1468 | 1830 / 5133 | 1154 / 503 |
| HTTP | Remove | 1 | PasarGuard | 877 / 981 | 4011 / 4691 | 710 / 558 |
| HTTP | Count | 1 | Direct Xray | 701 / 122 | 2457 / 468 | 1065 / 5074 |
| HTTP | Count | 8 | Direct Xray | 1941 / 382 | 4826 / 1274 | 3597 / 15279 |
| gRPC | Add | 1 | PasarGuard | 575 / 1061 | 1205 / 2142 | 1519 / 837 |
| gRPC | Remove | 1 | PasarGuard | 571 / 753 | 1448 / 1761 | 1632 / 1126 |
| gRPC | Count | 1 | Direct Xray | 590 / 132 | 1503 / 381 | 1427 / 5231 |
| gRPC | Count | 8 | Direct Xray | 1340 / 487 | 2902 / 1418 | 5379 / 12951 |

[HTTP rounds](results/2026-11-23-vless-accounts-http.csv) ·
[gRPC rounds](results/2026-11-23-vless-accounts-grpc.csv) ·
[HTTP raw CSV, gzip](results/2026-11-23-vless-accounts-http-samples.csv.gz) ·
[gRPC raw CSV, gzip](results/2026-11-23-vless-accounts-grpc-samples.csv.gz)

## Reproduce

Build both Node binaries and the client with the
[lifecycle benchmark setup](../reports/instances.md#reproduce). Run from the repository
root with the same verified Xray binary and the built reference descriptor.

```bash
XRAY_BENCH_VLESS_ACCOUNTS=1 XRAY_BENCH_ROUNDS=7 \
  bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
    "$work/bench" "$work/reference.pb" "$work/vless-accounts"
```

Each transport produces one per-round CSV and one compressed per-request CSV.
The raw timings are integer nanoseconds. The fixture settings support
`XRAY_BENCH_ACCOUNT_MUTATIONS`, `XRAY_BENCH_ACCOUNT_COUNT_CLIENTS` and
`XRAY_BENCH_ACCOUNT_COUNT_WINDOW`.
