# WireGuard peer activity and endpoint IPs

The ten-second accounting sampler records the time of observed traffic growth
and the current WireGuard endpoint IP. Activity expires after 45 seconds.
Acknowledged removal clears activity and the endpoint while pending usage
remains reportable. A peer with no observation returns offline and an empty
endpoint list. The kernel supplies one current endpoint per peer.

HTTPS comparison with PasarGuard at `739eab94a9343d9f0035baf7162796844109f4c4`.
Both engines sampled real tunnel traffic from one active peer. Each row uses
160 requests per engine over four alternating blocks. Times are milliseconds.

| Operation | Configured peers | Node p50 / p95 | PasarGuard p50 / p95 |
| --- | ---: | ---: | ---: |
| Activity | 1 | 0.266 / 0.580 | 0.320 / 0.537 |
| Activity | 1,000 | 0.245 / 0.575 | 0.293 / 0.374 |
| Endpoint IPs | 1 | 0.258 / 0.444 | 0.367 / 0.663 |
| Endpoint IPs | 1,000 | 0.242 / 0.301 | 0.299 / 0.396 |

Use `--peer-activity` with the [matched HTTPS fixture](../interfaces/lifecycle.md#reproduce).
[Raw timings](results/peer-activity-https.csv)
