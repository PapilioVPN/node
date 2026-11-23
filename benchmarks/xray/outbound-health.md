# Xray outbound health

## API and native behavior

- HTTP exposes `GET /v1/xray/instances/{id}/outbounds/health` with an optional
  `tag` query parameter. gRPC exposes `XrayOutboundsService.GetOutboundHealth`
  with the instance ID and optional tag. An omitted tag reads the entire
  observation in Xray's order. A supplied tag is matched exactly. An empty
  supplied tag is invalid. An absent exact match returns one `no_sample` row
  with the requested tag and no sample fields. An empty observation returns
  zero rows for the collection read. Absence leaves outbound membership
  unknown. [Public protobuf schema](../../internal/engines/xray/outbounds/bridge/grpc/proto/v1/outbounds.proto)
- `healthy` carries the recorded delay in milliseconds, including zero.
  `failed` carries the ordinary Observatory's raw failed-probe value
  `99999999`. The value is a sentinel. Last try and last seen are native
  Unix seconds. Last seen is omitted until the first successful probe and
  survives a later failed probe. The native time fields describe the stored
  sample. Xray updates these fields in its own background loop, so a
  collection can contain observations from different probe times.
  [Xray observer](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/observatory/observer.go)
- Both transports share one service and one `GetOutboundStatus` invocation
  per read on the active execution's existing native connection. A native
  snapshot is bounded at 1 MiB on the Node side. Admission is immediate,
  with 32 concurrent reads and a one-second deadline. Both selection modes
  decode the whole snapshot and use linear work and memory in its size.
  Node checks that the connection still belongs to the active execution
  after the RPC. Node reads the native record without scheduling probes.
  [Native RPC](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/observatory/command/command.go)
- `internal/engines/xray/module.go` adds the outbounds module to HTTP and
  gRPC composition. `configs/runconfig` registers `ObservatoryService`
  alongside `StatsService` when the local Xray JSON has an ordinary
  `observatory` block. It recognizes Xray's Java- and Python-style
  comments for this check. Native validation still checks the full file.
  Without ordinary Observatory the engine starts and the health API
  returns HTTP 409 or gRPC FailedPrecondition. An enabled Observatory
  with zero samples returns a successful empty observation.
  [Configuration preparation](../../internal/engines/xray/configs/runconfig/runconfig.go) · [Real Xray integration tests](../../internal/engines/xray/outbounds/integration/health_test.go)

## Paired results

Both targets ran the verified Xray v26.11.9 executable, commit `676eaf6`.
PasarGuard Node 0.5.7 was built from commit `739eab9`. Both Node binaries and
the driver were built with Go 1.27.3 and CGO disabled. Both public gRPC APIs
used grpc-go 1.85.0 and protobuf-go 1.36.12. PasarGuard's Xray SDK pin was
`v1.260327.1-0.20261021115930-1b1647cd9ddf`. PapilioVPN Node consumed the
v26.11.9 native wire contract over the execution's private Unix socket.
[PasarGuard revision](https://github.com/PasarGuard/node/commit/739eab94a9343d9f0035baf7162796844109f4c4) · [Xray release](https://github.com/XTLS/Xray-core/releases/tag/v26.11.9)

Each target received the same ordinary Observatory configuration and 17
freedom outbounds. A prefix selector observed 16 of them. A local HTTP 204
destination provided 15 healthy samples. One freedom outbound redirected
its probe to a loopback listener that closed before returning HTTP headers.
The remaining tag was outside the selector. The interval was one hour,
so measurements read stable observations after the first round of probes.
The single selection used `sample-00`. The collection returned all 16
observed tags, including the failed sample. The driver checked tag
membership, status, delay and native timestamp invariants on each success.

The driver used one and eight closed-loop clients with a warm-up call per
client. It alternated target, selection and client-count order across seven
550 ms rounds. Both servers and their Xray children were pinned to CPU 2.
The driver was pinned to CPU 4, a separate physical core. All ran with
`GOMAXPROCS=1` on the same Ryzen 7 3750H Linux 6.17.0-41-generic host in an
isolated loopback network namespace. The host also ran other workloads.
HTTP used persistent HTTP/1.1 over TLS and gRPC used a persistent multiplexed
TLS connection.

Values are medians of the seven per-round metrics on 2026-11-23. Each
measurement cell gives PapilioVPN / PasarGuard. The median p50 and p95
were lower and the median successful request rate was higher in all eight
matched workloads. No requests were rejected in the measured rounds.
Absolute latency reflects the shared host and the tested response sizes.

| Transport | Selection | Clients | p50 µs | p95 µs | Successful reads/s |
| --- | --- | ---: | ---: | ---: | ---: |
| HTTP | One | 1 | 1336 / 2525 | 3935 / 5987 | 552 / 330 |
| HTTP | One | 8 | 3081 / 14110 | 6329 / 27586 | 2366 / 502 |
| HTTP | All | 1 | 1553 / 2916 | 3019 / 5947 | 581 / 312 |
| HTTP | All | 8 | 4172 / 11669 | 7450 / 26123 | 1931 / 584 |
| gRPC | One | 1 | 595 / 1257 | 1363 / 2797 | 1324 / 702 |
| gRPC | One | 8 | 1664 / 6381 | 3315 / 12197 | 4349 / 1136 |
| gRPC | All | 1 | 736 / 1259 | 1579 / 2546 | 1176 / 691 |
| gRPC | All | 8 | 1778 / 6901 | 3625 / 13446 | 4000 / 999 |

[HTTP rounds](results/2026-11-23-outbound-health-http.csv) ·
[gRPC rounds](results/2026-11-23-outbound-health-grpc.csv) ·
[HTTP per-request CSV, gzip](results/2026-11-23-outbound-health-http-samples.csv.gz) ·
[gRPC per-request CSV, gzip](results/2026-11-23-outbound-health-grpc-samples.csv.gz)

### Contract differences

PasarGuard's `GetOutboundsLatency` uses its current backend and an
optional `name`. It reads Xray's loopback `/debug/vars` metrics, decodes
the Observatory map, filters protocols and sorts tags. Its rows include
`alive`, raw delay, both native timestamps and additional `link` and
`source` fields. Protocol exclusions include blackhole, DNS and loopback.
The benchmark used freedom for all tags so both transports returned the
same sampled membership. Its HTTP endpoint `GET /stats/latency` carries
a protobuf body and reply. PapilioVPN uses a query parameter and JSON.
The gRPC endpoints both use protobuf with their respective schemas.
[PasarGuard backend](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/latency.go) · [PasarGuard HTTP handler](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/rest/stats.go)

PasarGuard returns an empty list for an absent exact name when its
Observatory map has data, and an error when the map is empty. PapilioVPN
exposes explicit `no_sample` for an exact miss, empty collection for an
enabled Observatory with zero records, and an unavailable-feature error
when the execution has no ordinary Observatory. The timing compares the
overlapping recorded-read behavior. The no-sample and unavailable-feature
contracts were exercised by integration tests.

## Reproduce

Build the two binaries and reference descriptor with the
[lifecycle benchmark setup](../README.md#reproduce). Use the same verified
Xray executable and run from the repository root.

```bash
XRAY_BENCH_OUTBOUND_HEALTH=1 XRAY_BENCH_ROUNDS=7 XRAY_BENCH_OUTBOUND_WINDOW=550ms \
  bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
    "$work/bench" "$work/reference.pb" "$work/outbound-health"
```

The output directory contains a summary CSV and a compressed per-request
CSV for each transport. Durations in the per-request CSV are integer
nanoseconds. Set `XRAY_BENCH_OUTBOUND_CASE` to `one` or `all`,
`XRAY_BENCH_OUTBOUND_CLIENTS` to one client count, and
`XRAY_BENCH_TRANSPORTS` to one transport to repeat an individual workload.
