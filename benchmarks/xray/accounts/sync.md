# Live VLESS synchronization

All operations select one existing VLESS inbound. They share the account service,
the execution-scoped inventory, and the existing native add/remove operations.
The inventory retains case-folded email selectors and effective UUID fingerprints.
Temporary UUID input is used to construct the native AddUser operation. Replies
contain only acknowledgement counts and fixed status codes.

HTTP replies use JSON with Cache-Control no-store. Successful gRPC delivery and
HTTP 200 return an outcome envelope for a valid request. Invalid inputs return
ordinary transport errors. Clients must inspect the envelope.

| Outcome | Meaning |
| --- | --- |
| `confirmed` | Every planned effect was acknowledged, including plans with no changes. |
| `partial` | The described operations were acknowledged and a later operation stopped before dispatch. |
| `unknown` | A dispatched native operation lacked a definitive acknowledgement. The preceding counts still describe confirmed effects. |
| `not_applied` | The request stopped before any effect was attempted. |

A dropped management response leaves the caller without an outcome. A lost
native acknowledgement fences mutations on that inbound until execution
replacement. Count remains available. Changes apply to the active Xray
execution. Removing a user denies fresh connections.

## SyncUser

HTTP `PUT /v1/xray/instances/{id}/inbounds/{tag}/accounts/membership` or
gRPC `SyncUser`. Supply `email` and optionally `uuid`. Omit `uuid` to request
absence. Repeating the same effective UUID is a no-op. A rotation removes the
old record first, then adds the new one. A collision with another account is
rejected before removal. A warm request makes at most two native calls and uses
constant additional space. The existing account deadline is one second.

## SyncUsers

HTTP `PUT /v1/xray/instances/{id}/inbounds/{tag}/accounts` or gRPC
`SyncUsers`. The JSON `accounts` array and protobuf `snapshot` message are
required, including when requesting an empty snapshot. Every entry requires
an email and UUID. The limit is 1,000 desired accounts and 2 MiB for the HTTP
body. Duplicate case-folded emails, duplicate effective UUIDs and collisions
with retained credentials stop the plan before any native write. Anonymous
configured credentials remain in Xray and reserve their effective UUIDs.

The service plans against the active execution under the account mutation
lock. It removes obsolete or changed names in sorted order, then adds missing
or changed accounts in input order. A UUID may move to a different email when
its prior owner is removed by the same plan. The warm path uses one native
write per changed record, with two for a rotation, and performs no inventory
RPC per account. Planning takes space proportional to the desired and removed
sets. The application deadline is five seconds. On a known partial result a
new snapshot request can continue convergence against the acknowledged state.
An unacknowledged write uses the inbound fence described above.

## SyncUsersChunked

HTTP `PUT /v1/xray/instances/{id}/inbounds/{tag}/accounts/membership/stream`
accepts newline-delimited JSON, with one `index`, `last`, and `accounts`
object per line. gRPC `SyncUsersChunked` accepts the corresponding client
stream. Repeat the exact instance ID and inbound tag in every gRPC message
and set a client deadline of at most 30 seconds. The HTTP server limits the
stream to 30 seconds, each frame to 512 KiB, and the body to 2 MiB.

Indices are consecutive and zero-based. A frame may have up to 100 accounts.
An empty frame is valid only as the final marker. Send up to 100 frames and
1,000 accounts in total. Mark the last frame and finish the HTTP body or
half-close the gRPC stream. A nonfinal EOF, gaps, repeated indices, repeated
emails or effective UUIDs, changed scopes, extra frames and interrupted
delivery fail before any live write. At most eight streams are admitted,
and the live mutation lock is acquired only after complete receipt.

Each row specifies a desired change for its email. An omitted UUID removes
that account. Other accounts stay in place. The completed stream uses the
same acknowledgement and fencing rules as other sync operations. The
application plans in space proportional to the explicit changes and makes
one native write for each changed add or removal, with two for a rotation.

## Benchmark method

Build and run using [the common fixture setup](../instances/README.md#reproduce).
Both nodes use the same isolated VLESS fixture and same Xray v26.11.9
(`676eaf6`) executable. PasarGuard Node 0.5.7 uses [739eab9](https://github.com/PasarGuard/node/commit/739eab94a9343d9f0035baf7162796844109f4c4)
and was built with Go 1.27.3. PapilioVPN and the driver use Go 1.27.4 with
CGO disabled. Servers and their Xray child use CPU 2, the driver CPU 4, and
each uses GOMAXPROCS=1. The verified release archive SHA-256 is
`b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82`. The driver alternates which server runs first and
uses persistent management TLS connections. Each measurement is timed from
request construction to result, including the complete response. Setup and the
native exact membership check run outside the timed window. A sample is
successful only when the request confirms and Xray reports the expected names
and effective credentials. The round CSV includes the sum of timed request
durations and the success rate. The raw CSV records each elapsed duration in
nanoseconds, with transport, round, target, workload, and success flag.

```bash
XRAY_BENCH_VLESS_ACCOUNTS=1 XRAY_BENCH_ACCOUNT_SYNC=user \
  XRAY_BENCH_ROUNDS=5 XRAY_BENCH_READS=30 \
  bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
    "$work/bench" "$work/reference.pb" "$work/sync-user"
```

HTTP and gRPC each recorded five rounds of 30 samples per target and case.
In the converged case the account already matched. In the behind cases it
needed to be added or removed. All 1,800 samples succeeded. The median of
round p50 and p95 is shown in microseconds, PapilioVPN / PasarGuard.

| Transport | Case | p50 | p95 |
| --- | --- | ---: | ---: |
| HTTP | Converged | 150 / 196 | 213 / 344 |
| HTTP | Behind, add | 421 / 572 | 736 / 1183 |
| HTTP | Behind, remove | 402 / 390 | 717 / 596 |
| gRPC | Converged | 171 / 256 | 362 / 707 |
| gRPC | Behind, add | 590 / 648 | 811 / 1670 |
| gRPC | Behind, remove | 591 / 517 | 1510 / 854 |

[HTTP rounds](results/account-sync/user-http.csv) ·
[HTTP raw CSV](results/account-sync/user-http-samples.csv) ·
[gRPC rounds](results/account-sync/user-grpc.csv) ·
[gRPC raw CSV](results/account-sync/user-grpc-samples.csv)

## Full-list comparison

Both targets converge to the same named VLESS membership. PasarGuard
[rebuilds the Xray configuration and restarts the child](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/user.go#L300-L340).
PapilioVPN updates the running Xray, reusing unchanged credentials. These
measurements cover requested membership and completion. The restart, treatment
of existing traffic and behavior after lost delivery differ.

The converged fixture starts with the requested set. The behind fixture starts
with an equally sized disjoint set. Each transport used three rounds of eight
samples per target and case. The two transports ran concurrently on disjoint
sets of physical cores, HTTP server/driver 2/4 and gRPC server/driver 0/6.
Set `XRAY_BENCH_ACCOUNT_SYNC=users XRAY_BENCH_ROUNDS=3 XRAY_BENCH_READS=8`
and the matching `XRAY_BENCH_TRANSPORTS`, `XRAY_BENCH_SERVER_CPU` and
`XRAY_BENCH_CLIENT_CPU` to reproduce.

The comparison confirmed 384 of 384 requests. The median of round
p50 and p95 is shown in milliseconds, PapilioVPN / PasarGuard.

| Transport | Accounts | Case | p50 | p95 |
| --- | ---: | --- | ---: | ---: |
| HTTP | 100 | converged | 0.66 / 1008.45 | 1.40 / 1012.72 |
| HTTP | 100 | behind-replace | 30.81 / 1008.48 | 33.84 / 1009.90 |
| HTTP | 1000 | converged | 2.91 / 1021.40 | 6.03 / 1041.85 |
| HTTP | 1000 | behind-replace | 315.86 / 1017.23 | 416.04 / 2016.50 |
| gRPC | 100 | converged | 0.54 / 1008.97 | 0.96 / 1017.13 |
| gRPC | 100 | behind-replace | 32.46 / 1008.28 | 37.99 / 1016.91 |
| gRPC | 1000 | converged | 2.39 / 1012.93 | 4.07 / 1024.52 |
| gRPC | 1000 | behind-replace | 328.00 / 1013.33 | 432.46 / 2011.42 |

[HTTP rounds](results/account-sync/users-http.csv) ·
[HTTP raw CSV](results/account-sync/users-http-samples.csv) ·
[gRPC rounds](results/account-sync/users-grpc.csv) ·
[gRPC raw CSV](results/account-sync/users-grpc-samples.csv)

## Chunked-delta comparison

Each stream has 100 or 1,000 explicit changes in 50-account chunks. Half
specify present credentials and half specify removals. Both engines keep a
sentinel excluded from the stream. The behind fixture starts with every
removal present and every addition missing. The converged fixture already
matches the requested state. PasarGuard
[applies chunked payloads as targeted deltas](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/users_chunk.go#L33-L38).
Its converged case still sends native removes for absent accounts and
ignores the native removal errors. PapilioVPN skips those calls. The
comparison covers final membership on complete streams. PapilioVPN also
requires clean EOF after the final marker and reports uncertain effects.

Use the same sampling and CPU settings as the full-list comparison, with
`XRAY_BENCH_ACCOUNT_SYNC=chunked`.

The comparison confirmed 384 of 384 requests. The median of round
p50 and p95 is shown in milliseconds, PapilioVPN / PasarGuard.

| Transport | Accounts | Case | p50 | p95 |
| --- | ---: | --- | ---: | ---: |
| HTTP | 100 | converged | 0.64 / 8.08 | 0.87 / 10.06 |
| HTTP | 100 | behind-delta | 14.86 / 30.35 | 17.57 / 67.41 |
| HTTP | 1000 | converged | 3.06 / 97.39 | 5.23 / 100.39 |
| HTTP | 1000 | behind-delta | 145.52 / 258.59 | 186.60 / 282.59 |
| gRPC | 100 | converged | 0.50 / 9.18 | 0.79 / 13.56 |
| gRPC | 100 | behind-delta | 16.14 / 31.56 | 17.37 / 67.00 |
| gRPC | 1000 | converged | 1.91 / 84.25 | 4.78 / 90.31 |
| gRPC | 1000 | behind-delta | 149.38 / 288.93 | 180.90 / 403.17 |

[HTTP rounds](results/account-sync/chunked-http.csv) ·
[HTTP raw CSV](results/account-sync/chunked-http-samples.csv) ·
[gRPC rounds](results/account-sync/chunked-grpc.csv) ·
[gRPC raw CSV](results/account-sync/chunked-grpc-samples.csv)

### Integrated single-account timing recheck

An optimization trial kept mutation deadlines in Node and propagated
cancellation to the native stream. The five-round confirmation used
24 samples per target and case, stock Xray v26.11.9, server CPU 0 and
client CPU 4. The trial did not show a consistent improvement. In particular,
the remove p95 increased from 1.202 to 1.575 ms over HTTP and from
1.193 to 1.465 ms over gRPC. The integrated implementation preserves
the original deadline behavior. [Per-round data](results/account-sync/2026-11-23-single-native-deadline-trial.csv)
