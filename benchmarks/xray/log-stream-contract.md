# Xray GetLogs

Open `GET /v1/xray/instances/{id}/logs` or call
`papiliovpn.node.v1.XrayLogsService/GetLogs` with `XrayLogsRequest{id}`.
Both use Node's management listener authentication. Admission requires the exact
configured instance ID and a ready Xray execution. Unknown IDs return HTTP 404
or gRPC `NOT_FOUND`. A configured instance without a ready execution returns
HTTP 409 or gRPC `FAILED_PRECONDITION`. Excess concurrent subscriptions return
HTTP 429 or gRPC `RESOURCE_EXHAUSTED`. A successful stream sends headers even
when Xray has emitted no live events. The HTTP response uses
`application/x-ndjson` and `Cache-Control: no-store`.

The stream observes newly captured stdout and stderr from one child. Xray
controls which messages it writes to each pipe. A log file, syslog destination,
or internal Xray log that never reaches either pipe is outside this operation.
There is no replay or startup history. The operation leaves Xray's configuration
and logging destinations alone.

Each event has these fields.

| Field | Meaning |
| --- | --- |
| `execution` | Execution number assigned by this Node process. A new Node process starts its own numbering. |
| `sequence` | One-based capture order within the execution, shared across both pipes. Order within each pipe is preserved. Order across pipes reflects Node capture order. |
| `source` | `stdout` or `stderr` in HTTP. The gRPC schema uses an enum. These names identify file descriptors, regardless of log severity. |
| `received_unix_nano` | Node's wall-clock capture time in Unix nanoseconds. |
| `message` | Up to 16 KiB of raw line bytes before the LF delimiter. HTTP uses standard base64 and gRPC uses protobuf bytes. |
| `truncated` | True if bytes beyond the first 16 KiB of this line were discarded. Reading resumes at the next line. |

HTTP encodes the three integers as decimal strings to preserve their full width.
The raw message may be empty or contain non-UTF-8 bytes. An unterminated tail can
be framed on process exit, subject to the same best-effort delivery as other
pending records.

Node admits up to 16 log subscriptions across both transports and across
execution transitions. Each subscriber has a 64-event queue, giving at most
16 MiB of queued message bytes in the worst case. Message bytes are shared
between subscribers. Each handler can hold one additional event while it sends.
The pipe readers use up to 16 KiB of line prefix per pipe and Go's bounded copy
buffers. Event metadata, encoded frames, Go allocator overhead, and protocol
and kernel buffers are additional. This budget describes the log capture and
queue, not total Node process RSS.

If a subscriber's queue is full, that subscriber misses the new event. Other
queues still accept it if they have room. The process readers do no network
I/O and never wait on a subscriber to free queue space. Gaps between delivered
sequence numbers reveal omitted events. Messages before subscription or after
the last delivered message cannot be counted from a sequence gap. There is no
acknowledgement, recovery, or delivery guarantee. An HTTP write gets up to two
seconds, after which the handler returns. The stream may close without a final
complete HTTP frame when the write times out.

Stopping Xray, its exit, or Node shutdown ends that execution's subscriptions and
discards queued events. An idle stream ends with EOF. If a gRPC send is already
blocked by flow control, cancellation releases the handler and the client may
see `CANCELLED`. Bytes already handed to the transports can arrive before EOF
or a terminal error. Client cancellation releases its subscription. After an
execution change a client must open a new stream. The old stream cannot receive
the new execution's events.
