# WireGuard egress probe

The probe issues one HEAD request through the configured Linux device using
SO_BINDTODEVICE. A received HTTP response reports reachability and the
status code. The timeout, URL and device are operator settings. Up to four
requests may run concurrently.

Both engines used a fresh outbound connection bound to loopback and the
same isolated HTTP target. Management requests used warm HTTPS
connections. Each row has 120 requests per engine. Times are milliseconds.

| Build | Node p50 / p95 | PasarGuard p50 / p95 |
| --- | ---: | ---: |
| Native link inspection before the request | 1.352 / 1.865 | 0.919 / 1.402 |
| Recorded applied state before the socket probe | 0.648 / 0.893 | 0.637 / 0.990 |

The final build checks the recorded applied state and observes connectivity
through the bound socket. Use `--probe` with the [matched fixture](../interfaces/lifecycle.md#reproduce).
[First run](results/wg-probe-https.csv) · [Final run](results/wg-probe-repeat-https.csv)
