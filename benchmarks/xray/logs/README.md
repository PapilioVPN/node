# GetLogs comparison

PapilioVPN and PasarGuard both delivered all 17,280 expected fresh access-log
events per transport in this workload. PasarGuard had lower pooled delivery
p50 and p95 on both transports. PapilioVPN and PasarGuard received roughly
500 events/s with one subscriber and 4,006 events/s in aggregate with eight.
The producer sets this rate.

## Contracts

| | PapilioVPN | PasarGuard 739eab9 |
| --- | --- | --- |
| Selection | An exact instance ID and its currently ready execution | The controller's current backend, with no instance selector |
| HTTP | `GET /v1/xray/instances/{id}/logs`, NDJSON with base64 payload | `GET /logs`, `text/event-stream` with flushed plain lines |
| gRPC | `XrayLogsService.GetLogs(XrayLogsRequest)` streams typed `XrayLogEvent` | `service.NodeService.GetLogs(Empty)` streams `Log{detail}` |
| Child output | stdout and stderr, tagged by file descriptor | Scans stdout and can append backend-generated text |
| Start of stream | Fresh events from the selected execution | Replays the current buffer, followed by live events |
| Limits | 16 KiB per event, 16 subscribers, 64 queued events per subscriber | Scanner limit 1 MiB, configurable ring (default 10,000 entries), each subscriber gets space for the snapshot plus the ring capacity |
| Full queue | Drops the newest event for that subscriber. The sequence can expose a gap. | Drops the newest event for that subscriber. The string has no gap marker. |
| End | Execution stop or exit ends its subscriptions and discards the pending queue. | The backend owns the buffer lifetime. Backend shutdown closes it. An in-place Xray restart can reuse the buffer. |

[PapilioVPN API contract](contract.md) ·
[PasarGuard REST](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/rest/log.go) ·
[PasarGuard gRPC](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/rpc/log.go) ·
[PasarGuard stdout capture](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/log.go) ·
[PasarGuard buffer](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/pkg/logstream/buffer.go)

## Workload

The two real management services and their Xray children run in an isolated
Linux network and PID namespace. Each round runs one backend at a time, with
alternating backend order. One client issues 384 marker-bearing HTTP CONNECT
requests to an Xray HTTP inbound with a 2 ms target interval. Xray redirects
the outbound to a local TCP origin. Both backends use the same Xray
configuration and emit one identifiable stdout access line per request. The
subscribers read continuously and run with the same subscriber count.
Management uses TLS 1.3 and API-key authentication for both services. The HTTP
client uses HTTP/1.1 for both. The gRPC client uses generated protobuf decoding
for both wire envelopes.

The producer waits 50 ms after all clients start opening the stream, then
runs the load and allows 300 ms after the final probe for deliveries. Time to
first fresh event starts immediately before each client opens GetLogs and
includes the common 50 ms wait. Time to first wire event includes any replay
or startup line. Delivery delay uses the UTC timestamp on the Xray console
line and the client's wall-clock time after message decoding. Both are on the
same host. Lost counts missing marker deliveries out of probes multiplied by
subscriber count. This metric includes losses anywhere before or after the
pipe and cannot isolate their cause. Delivery percentiles pool all received
marker events over five rounds. First-event columns use medians across
subscribers and rounds. Rates use the median of per-round aggregate received
events divided by the producer window, including deliveries in the final drain.

Both Node executables and the benchmark client were built with Go 1.27.4.
The shared Xray executable was v26.11.9, revision `676eaf6`, binary SHA256
`e87bce586acdb9136aa5b1f1d9c8db9f1ea1d145b6d86368c7f6f662f9f6a66d`.
PapilioVPN's embedded Xray SDK was `676eaf6` and PasarGuard's was
`1b1647c`. PasarGuard used `LOG_BUFFER_SIZE=10000` and `DEBUG=false`.
The host was an AMD Ryzen 7 3750H running Linux 6.17.0-41-generic on amd64.
The server and its Xray child were pinned to logical CPU 6. The driver and
origin were pinned to CPU 7, the sibling on the same core. Go processes used
`GOMAXPROCS=1`.

## Measurements

| Transport | Subscribers | Node | First wire p50 (ms) | First fresh p50 (ms) | Delivery p50 (µs) | Delivery p95 (µs) | Received/s | Lost / expected |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| HTTP | 1 | PapilioVPN | 51.207 | 51.207 | 180.20 | 434.89 | 500.7 | 0 / 1,920 |
| HTTP | 1 | PasarGuard | 1.017 | 51.946 | 112.56 | 288.88 | 500.8 | 0 / 1,920 |
| HTTP | 8 | PapilioVPN | 52.125 | 52.125 | 297.43 | 507.38 | 4,006.7 | 0 / 15,360 |
| HTTP | 8 | PasarGuard | 16.132 | 53.258 | 186.93 | 371.94 | 4,004.5 | 0 / 15,360 |
| gRPC | 1 | PapilioVPN | 51.435 | 51.435 | 216.00 | 539.00 | 500.6 | 0 / 1,920 |
| gRPC | 1 | PasarGuard | 0.759 | 52.263 | 198.44 | 496.19 | 500.6 | 0 / 1,920 |
| gRPC | 8 | PapilioVPN | 51.636 | 51.636 | 303.05 | 594.80 | 4,006.3 | 0 / 15,360 |
| gRPC | 8 | PasarGuard | 4.919 | 52.209 | 282.92 | 500.10 | 4,007.6 | 0 / 15,360 |

The first-wire differences include pre-load lines and are outside the matched
fresh-event comparison. The loaded rate does not estimate either service's
maximum capacity. The comparison covers normal-speed readers and stdout
access messages. Native tests separately exercise stderr and stalled
HTTP/gRPC readers while Xray and two other subscribers continue.

An HTTP control ran three rounds with and without PapilioVPN's per-write
deadlines. Median round delivery p95 at one/eight subscribers was 347/497 µs
with deadlines and 335/490 µs without. Removing the deadline did not reproduce
PasarGuard's HTTP results. The comparison includes the different encodings
and client decoders. It does not isolate their individual cost. The
no-deadline build was used only for this control because it lets a blocked HTTP
write retain the handler indefinitely.

[HTTP rounds](results/2026-11-23-get-logs-http.csv) ·
[gRPC rounds](results/2026-11-23-get-logs-grpc.csv) ·
[HTTP raw CSV, gzip](results/2026-11-23-get-logs-http-samples.csv.gz) ·
[gRPC raw CSV, gzip](results/2026-11-23-get-logs-grpc-samples.csv.gz) ·
[HTTP deadline control rounds](results/2026-11-23-get-logs-http-deadline-check.csv)

## Reproduce

Use the real Xray v26.11.9 executable and PasarGuard source at
`739eab94a9343d9f0035baf7162796844109f4c4`. From the PapilioVPN repository
root, with `$work/reference` holding that source, run the following.

```bash
go build -o "$work/node" ./cmd/node
go build -o "$work/bench" ./benchmarks/xray
(cd "$work/reference" && go build -o "$work/pasarguard" ./cmd/node)
protoc -I "$work/reference" --descriptor_set_out="$work/reference.pb" \
  "$work/reference/common/service.proto"
bash benchmarks/xray/logs/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
  "$work/bench" "$work/reference.pb" "$work/logs"
```

The runner needs Linux user and network namespaces, `ip`, `taskset`,
`openssl` and Python 3. Use `XRAY_LOG_BENCH_SERVER_CPU` and
`XRAY_LOG_BENCH_CLIENT_CPU` for other CPU placements. The fixture defaults to
five rounds on both transports. Set `XRAY_LOG_BENCH_ROUNDS` to change the
number or `XRAY_LOG_BENCH_TRANSPORTS=http` to run the HTTP-only experiment.
For the no-deadline experiment use a temporary build overlay that removes
both `SetWriteDeadline` calls before writing and the deadline clear after
successful flush. Keep the NDJSON encoding and event-by-event flush intact.

Each transport writes a round CSV and a gzip-compressed CSV with one row per
expected event per subscriber. Raw timestamps are Unix nanoseconds. A blank
receive timestamp marks a missing delivery.

## Integrated HTTP encoding repeat

The HTTP handler now reuses one output buffer per subscriber and writes
the same NDJSON fields and base64 payload directly. The regression test
compares byte-for-byte output with the standard JSON encoder, including
invalid UTF-8 in the payload. The write timeout and flush behavior are
part of the same handler and API contract.

The integrated repeat used stock Xray v26.11.9, server CPU 2 and client CPU
6. Each case used three rounds. Figures are medians of per-round delivery
percentiles in microseconds. The encoding change applies to HTTP.

| Subscribers | Node p50 before / after | Node p95 before / after | PasarGuard p50 / p95, tuned run |
| ---: | ---: | ---: | ---: |
| 1 | 164.0 / 148.2 | 408.5 / 302.7 | 104.7 / 272.1 |
| 8 | 273.6 / 207.1 | 1166.5 / 408.0 | 172.1 / 380.4 |

All expected events were received on both transports. PasarGuard's
HTTP delivery percentiles were lower. The gRPC rerun also varied by
subscriber count, with Node leading at one and trailing at eight in the
tuned window. [Per-round data](../integration/results/2026-11-23-xray-http-encoding-repeat.csv)

## Fan-out diagnostic

A fixture recorded the Node receipt timestamp that already travels on
the wire. At eight subscribers, the p95 from Xray's console timestamp to
Node receipt was 212 µs. From Node receipt to client decoding, p95 was
307 µs. These are independently computed percentiles.

A trial replaced the subscriber map with a small slice. The first
three-round comparison lowered Node's eight-subscriber HTTP p95 from
444 to 390 µs. The reverse-order repeat measured 428 µs for the
existing implementation and 675 µs for the slice. Reference timings
moved between runs as well. Both products delivered every event.
The existing implementation was retained.
[Per-round data](results/2026-11-23-http-log-fanout-trial.csv)
