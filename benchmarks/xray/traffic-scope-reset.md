# Xray scoped traffic reset

Enable `xray.reset_engine_stats: true` before calling this operation. Use
`POST /v1/xray/instances/{id}/stats/traffic/{scope}/scope-resets?max_counters=N`
or `XrayStatsService.ResetTrafficScope` with the instance ID, scope and limit.
The scope is `user`, `inbound` or `outbound`. The limit is 1–1000
directional counters. A user with both directions uses two slots.

Node first reads the matching counters from the active execution. It selects
only keys with the requested scope prefix and a traffic direction suffix.
Counter names may contain `>>>`. An excessive selection or duplicate key is
rejected before the first reset. Discovery accepts at most 4 MiB of native
response data. A report accepts names up to 4096 bytes each and 512 KiB in
total. An empty selection returns a complete report with zero rows.

Each selected counter has one exact native reset. Counters added after
selection wait for a later request. Each successful native reset returns
the bytes present when that counter was cleared. Traffic can continue between
resets, so a report can contain values observed at different times. Rows are
ordered by name, with uplink first. The default execution order follows the
report. Set `xray.reset_scope_concurrency` to 2–8 to allow separate resets
to overlap. An accepted request reserves up to its configured parallelism
from the shared 16-slot reset budget and runs for up to 5 seconds.

Check the overall outcome and each counter effect. Acknowledged results
include `previous_bytes`, including zero. A disappeared counter is
`missing`. A rejected call can be `not_applied`. An unconfirmed call is
`unknown`, and counters left alone are `not_attempted`. When a failure
is observed, Node stops admitting new work and preserves the outcomes of
calls already in flight. Reconcile an unknown outcome before repeating
the request.

| Overall outcome | HTTP status | gRPC response |
| --- | ---: | --- |
| Complete | 200 | `COMPLETE` and results |
| Interrupted, with known effects so far | 409 | `INTERRUPTED` and results |
| At least one effect is unknown | 502 | `UNKNOWN` and results |

If discovery or validation fails before a report exists, the API returns its
usual error. An overly large selection returns HTTP 422 or gRPC
`ResourceExhausted`. A formed report travels in the gRPC response body even
when interrupted so a large report fits within ordinary gRPC message limits.

## Measurements

Both management binaries and the client were built with Go 1.27.4 and
`CGO_ENABLED=0`. Both products used Xray v26.11.9 over the same Linux amd64
host, with TLS 1.3, authenticated management, one server CPU (logical CPU 2)
and client CPU 6. Each row reports the median of three per-round metrics.
The runner alternates product order between rounds. It seeds nonzero
uplink and downlink data for every account and validates the whole report
before timing repeated resets of the now-zero counters. These measurements
cover the public API and client decoding.

PasarGuard `739eab9` uses one native `QueryStats(reset=true)` call. Xray
matches substrings in that RPC. For example, a name containing `user>>>`
can match a user query even when it belongs to an inbound. In this fixture
the selected counters coincide because their names are distinct and
unambiguous. Node uses one read plus one exact `GetStats(reset=true)` per
selected counter to keep the effect within the requested scope and report
each acknowledgement. The table compares latency on the shared fixture,
with different failure and selection guarantees.
[Xray stats handlers](https://github.com/XTLS/Xray-core/blob/676eaf66f8f05d4d558ee7a74b4736623d3feeb9/app/stats/command/command.go#L164-L184)

### Default mode

The initial sweep used three 400 ms windows per product. Each cell shows
Node / PasarGuard.

| Transport | Counters | API clients | Native parallelism | p50 ms | p95 ms | Success/s |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| http | 2 | 1 | 1 | 0.832 / 0.467 | 1.435 / 0.865 | 1071.6 / 1893.2 |
| http | 2 | 8 | 1 | 2.078 / 1.091 | 4.317 / 2.734 | 3388.8 / 6492.1 |
| grpc | 2 | 1 | 1 | 0.827 / 0.484 | 1.299 / 0.826 | 1094.3 / 1838.3 |
| grpc | 2 | 8 | 1 | 2.053 / 1.263 | 4.621 / 2.757 | 3346.0 / 5674.8 |
| http | 100 | 1 | 1 | 18.745 / 0.825 | 23.408 / 1.989 | 52.6 / 1042.2 |
| http | 100 | 8 | 1 | 61.004 / 2.614 | 72.652 / 5.767 | 131.4 / 2686.7 |
| grpc | 100 | 1 | 1 | 14.001 / 0.649 | 17.520 / 1.268 | 71.2 / 1364.9 |
| grpc | 100 | 8 | 1 | 46.988 / 2.887 | 53.622 / 7.505 | 165.2 / 2102.7 |
| http | 1000 | 1 | 1 | 151.187 / 2.831 | 170.625 / 4.324 | 6.4 / 334.7 |
| grpc | 1000 | 1 | 1 | 139.291 / 2.820 | 151.356 / 4.600 | 7.0 / 335.0 |

[Per-round CSV](results/2026-11-23-traffic-scope-reset-default.csv)

### Parallel repeat

The final binary was measured at one and eight native calls per request,
with one API client. Each window was 600 ms for 100 counters and 1.2 s for
1000 counters. Each cell shows Node / PasarGuard.

| Transport | Counters | API clients | Native parallelism | p50 ms | p95 ms | Success/s |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| http | 100 | 1 | 1 | 14.563 / 0.810 | 22.204 / 1.666 | 64.8 / 1072.5 |
| grpc | 100 | 1 | 1 | 15.450 / 0.812 | 34.427 / 1.651 | 57.9 / 1071.5 |
| http | 100 | 1 | 8 | 7.325 / 0.642 | 10.388 / 1.213 | 133.0 / 1368.9 |
| grpc | 100 | 1 | 8 | 7.633 / 0.692 | 11.659 / 1.837 | 123.3 / 1124.6 |
| http | 1000 | 1 | 1 | 151.296 / 2.835 | 183.992 / 4.786 | 5.8 / 327.5 |
| grpc | 1000 | 1 | 1 | 134.618 / 2.979 | 172.207 / 5.569 | 7.2 / 306.2 |
| http | 1000 | 1 | 8 | 72.837 / 2.731 | 78.248 / 3.981 | 13.8 / 350.3 |
| grpc | 1000 | 1 | 8 | 81.460 / 3.050 | 186.244 / 5.176 | 10.7 / 296.6 |

[Per-round CSV](results/2026-11-23-traffic-scope-reset-parallel-repeat.csv)

The measured default and repeat windows had zero rejections. In the repeat,
eight-way dispatch improved Node's p50 and throughput on both transports at
both sizes. The HTTP p95 improved at both sizes. At 1000 counters the gRPC
p95 went from 172.207 to 186.244 ms despite its lower p50. PasarGuard led
on all reported latency and throughput metrics. At 1000 counters its p50
was about 3 ms and Node's parallel p50 was about 73–81 ms. The controlled
native test confirms one discovery call plus 1000 individual resets.
Increasing parallelism overlaps those round trips and does not remove them.

### Reproduce

Follow the [lifecycle setup](../README.md#reproduce) to obtain Xray, build
PasarGuard and generate its descriptor. Use Go 1.27.4 for both servers and
the benchmark driver. Run from the repository root and set the counter
count through the number of accounts, with two directions per account.

```bash
XRAY_BENCH_TRAFFIC_CASE=reset-scope/list XRAY_BENCH_TRAFFIC_NAMES=500 \
  XRAY_BENCH_TRAFFIC_CLIENTS=1 XRAY_BENCH_SCOPE_RESET_CONCURRENCY=8 \
  XRAY_BENCH_ROUNDS=3 XRAY_BENCH_TRAFFIC_WINDOW=1200ms \
  XRAY_BENCH_SERVER_CPU=2 XRAY_BENCH_CLIENT_CPU=6 \
  bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
    "$work/bench" "$work/reference.pb" "$work/scope-results"
```

Set `XRAY_BENCH_SCOPE_RESET_CONCURRENCY=1` for sequential execution. For
the initial sweep set the window to 400 ms. Use 1, 50 or 500 accounts.
Run one and eight clients for 1 and 50 accounts and one client for 500.

### Stock Xray tuning

Node now keeps the reset deadline on the client side of the native stream.
The pinned Xray `GetStats` handler does its swap without reading the server
context. Cancellation still reaches the stream and a lost reset reply still
reports an unknown effect. The HTTP and gRPC handlers use the report's
existing previous-byte values and allocate output rows together.

The confirmation run uses stock Xray v26.11.9, 500 accounts, one API client,
eight native resets in flight, and three 1.4 s windows. It compares Node
`5604147` with `92b7b28`. The initial deadline-only trial used the same
settings. Its reference timings shifted between runs, so the table below
uses the confirmation run. The CSV retains all four runs.

| Transport | Node p50 ms before / after | Node p95 ms before / after | Node success/s before / after | PasarGuard p95 ms in tuned run |
| --- | ---: | ---: | ---: | ---: |
| HTTP | 74.813 / 68.449 | 87.758 / 78.517 | 13.17 / 14.28 | 3.937 |
| gRPC | 64.098 / 63.905 | 69.107 / 72.161 | 15.38 / 15.51 | 4.746 |

The HTTP repeat improved on the three measured metrics. The gRPC p95 was
higher in the confirmation run. The underlying count of native requests
is unchanged. Both products had zero rejections in these windows.
[Per-round CSV](results/2026-11-23-traffic-scope-reset-stock-tuning.csv)

For the confirmation run, use the reproduction command above with
`XRAY_BENCH_TRAFFIC_WINDOW=1400ms`. Use stock Xray and a fresh output
directory for each Node revision.

### One native call with the bundled Xray

The package includes `/node-xray`, built with the pinned Xray v26.11.9
SDK. Set `xray.executable: /node-xray` and `xray.native_scope_reset: true`
alongside the existing `xray.reset_engine_stats: true` grant. Use a
writable runtime directory for the private socket. A read-only Docker
container needs a writable mount for this directory, for example
`--tmpfs /tmp:mode=1777` when using `/tmp`. Stock Xray uses the original
bounded path when `native_scope_reset` is omitted.

The bundled service checks the exact prefix and direction suffix in the
child process, verifies the bounds, then swaps the selected counters.
One request uses one native RPC. Traffic can still arrive between distinct
counter swaps. A lost native reply reports an unknown effect and Node
never repeats the request automatically. An explicit bound rejection
confirms that no counter was reset.

The comparison uses the bundled build for Node and the v26.11.9 release
binary for PasarGuard. Both are based on Xray revision `676eaf66f8f0`.
The results measure the complete HTTP and gRPC paths, with one client
and three 900 ms windows per case. Cells show Node / PasarGuard.

| Transport | Counters | p50 ms | p95 ms | Success/s |
| --- | ---: | ---: | ---: | ---: |
| http | 2 | 0.451 / 0.459 | 0.909 / 0.875 | 1918.5 / 1814.4 |
| grpc | 2 | 0.507 / 0.511 | 0.934 / 0.963 | 1719.2 / 1682.4 |
| http | 100 | 1.002 / 0.768 | 2.055 / 2.135 | 899.7 / 1066.8 |
| grpc | 100 | 0.729 / 0.662 | 1.539 / 1.530 | 1217.8 / 1265.5 |
| http | 1000 | 4.922 / 2.850 | 7.899 / 4.377 | 189.2 / 325.2 |
| grpc | 1000 | 2.782 / 3.062 | 5.349 / 4.786 | 316.6 / 311.8 |

At 1000 counters, the one-call path reduced p50 from 68.449 to 4.922 ms
for HTTP and from 63.905 to 2.782 ms for gRPC across these runs. The gRPC
p50 was lower than the reference at 1000 counters. HTTP p50 and both p95
values were higher. The comparison used the same selection in its fixture.
The bundled path guarantees exact scope and preflight bounds.

[Per-round CSV](results/2026-11-23-traffic-scope-reset-native-batch.csv)

Build the companion with `CGO_ENABLED=0 go build -trimpath -o "$work/node-xray" ./cmd/node-xray`.
Set `XRAY_BENCH_NATIVE_SCOPE_RESET=1`,
`XRAY_BENCH_REFERENCE_XRAY="$work/xray/xray"` and
`XRAY_BENCH_TRAFFIC_WINDOW=900ms` in the reproduction command.
Pass `"$work/node-xray"` as the third runner argument.

#### HTTP encoding repeat

The HTTP adapter now writes the same JSON report directly. A wire
regression test covers name escaping, empty reports and absent previous
bytes. Three 1.2 s windows at 1000 counters and one client measured Node
p50 at 5.175 ms before and 4.045 ms after. Node p95 went from 8.410 ms to
6.799 ms. The reference in the tuned run measured 3.064 ms p50 and
5.129 ms p95. Both servers had zero rejections in all windows.
[Per-round data](results/2026-11-23-xray-http-encoding-repeat.csv)

#### Allocation and client decoding

A profile of the HTTP benchmark for 1000 zero-valued counters separated
the time to receive the response body from client decoding and validation.
Node's JSON body was 100,036 bytes and PasarGuard's protobuf body was
34,000 bytes. Their median body-receipt times were 1.944 and 1.979 ms.
Decoding and validation took 1.764 and 0.732 ms respectively.

The previous capacity estimate grew the response buffer at the end.
Reserving space for signed acknowledgements reduced the test case from
221,184 to 122,880 allocated bytes per report, with one allocation
instead of two. Run the allocator measurement with
`go test ./internal/engines/xray/stats/bridge/http -run '^$' -bench BenchmarkScopeReportJSON -benchmem`.

The bundled HTTP repeat used 500 accounts, one client, five 700 ms
windows, server CPU 0 and client CPU 4. The driver retained the phase
instrumentation for both builds. Node p50/p95 went from 3.577/5.379 ms
to 3.006/4.891 ms. PasarGuard measured 2.650/4.065 ms in the tuned run.
Both products processed all requests without rejections.
[Per-round data](results/2026-11-23-scope-http-allocation.csv)

#### Compact native acknowledgements

The bundled engine sends a compact private protobuf reply for selections of
256 or more counters when the caller requests it. The reply preserves each
name, direction, and signed value. The old row reply remains supported in
both directions of an upgrade. The public HTTP and gRPC schemas are unchanged.

Decoding the private reply for 1,000 counters with zero previous values used
113,608 bytes and 2,012 allocations for rows and 60,592 bytes and 1,015
allocations for the compact reply. Their wire sizes were 17,000 and 16,009
bytes. Reproduce with the following command.

```bash
GOMAXPROCS=1 go test ./benchmarks/xray -run '^$' \
  -bench '^BenchmarkScopeResetDecode$' -benchmem -benchtime=200ms -count=5
```

The integrated build at `59ddc16` was measured with the bundled Xray and
PasarGuard `739eab9` with stock Xray v26.11.9. Each row uses the median of
five rounds with one client and a 750 ms window. Server CPU was 0 and client
CPU was 4. Latencies are in milliseconds, PapilioVPN / PasarGuard.

| Transport | Counters | p50 | p95 | Successful requests per second |
| --- | ---: | ---: | ---: | ---: |
| HTTP | 100 | 0.854 / 0.708 | 2.345 / 1.630 | 973.3 / 1194.5 |
| gRPC | 100 | 0.848 / 0.687 | 2.044 / 1.297 | 1017.2 / 1311.2 |
| HTTP | 1000 | 3.362 / 2.733 | 5.737 / 4.253 | 276.3 / 353.1 |
| gRPC | 1000 | 2.315 / 2.845 | 4.741 / 4.368 | 388.2 / 333.9 |

There were no rejected requests. The 1,000-counter gRPC result has a lower
median and higher successful throughput, with a higher p95. The 100-counter
results use the row reply. Earlier compact trials compared the same binaries
in both orders, and the smaller workload showed no consistent end-to-end gain.

[Per-round data](results/2026-11-24-compact-scope-reset.csv)
