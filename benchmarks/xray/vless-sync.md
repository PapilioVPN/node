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

## Benchmark method

Build and run using [the common fixture setup](../README.md#reproduce).
Both nodes use the same isolated VLESS fixture and same Xray v26.11.9
(`676eaf6`) executable. PasarGuard Node 0.5.7 uses [739eab9](https://github.com/PasarGuard/node/commit/739eab94a9343d9f0035baf7162796844109f4c4)
and was built with Go 1.27.3. PapilioVPN and the driver use Go 1.27.4 with
CGO disabled. Servers and their Xray child use CPU 2, the driver CPU 4, and
each uses GOMAXPROCS=1. The driver alternates which server runs first and
uses persistent management TLS connections. Each measurement is timed from
request construction to result, including the complete response. Setup and the
native exact membership check run outside the timed window. A sample is
successful only when the request confirms and Xray reports the expected names
and effective credentials. CSVs contain timings and success flags only.

```bash
XRAY_BENCH_VLESS_ACCOUNTS=1 XRAY_BENCH_ACCOUNT_SYNC=1 \
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
