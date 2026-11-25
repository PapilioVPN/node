# WireGuard logs and runtime

The log stream emits structured management events and replays the last 64.
It admits 16 subscribers with 128 events each. A full queue cancels that
subscriber. Runtime statistics cover the Node Go process and the uptime of
its confirmed WireGuard execution. Uptime is absent for a recovered legacy
checkpoint without a start timestamp.

Paired HTTPS results in milliseconds, with 120 observations per engine and
operation. Both runtime reads validate a kernel link and read Go memory
statistics. The log measurement opens a fresh HTTPS connection and reads the
first retained start event. Wire formats are Node NDJSON and the pinned
PasarGuard HTTP log stream.

| Operation | Node p50 / p95 | PasarGuard p50 / p95 |
| --- | ---: | ---: |
| Log time to first event | 2.284 / 2.779 | 2.319 / 3.920 |
| Runtime stats | 0.512 / 0.813 | 0.619 / 0.922 |

Use `--diagnostics` with the [matched HTTPS fixture](../interfaces/lifecycle.md#reproduce).
[Raw timings](results/logs-runtime-https.csv)
