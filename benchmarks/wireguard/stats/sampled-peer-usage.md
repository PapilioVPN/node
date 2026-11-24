# WireGuard sampled peer usage

`GetPeerUsage`, `ResetPeerUsage`, `ListPeerUsage` and `ResetAllPeerUsage`
operate on unreported bytes collected for the configured managed link.
Single-peer operations select a 32-byte public key. The group is an
unordered collection of peers with nonzero pending usage. HTTP exposes
`/v1/wireguard/interfaces/{name}/peers/{key}/usage` and
`/v1/wireguard/interfaces/{name}/peers/usage`. `GET` reads the current
period and `POST` to the respective `/resets` subresource consumes it.

The owner samples recent-handshake peers every ten seconds, with the same
45-second handshake window as the pinned reference. Reads and resets use
the in-memory sample. One extra device read captures available counters
before a managed peer or link removal. Acknowledged removals retain pending
bytes and release the old kernel baseline. A replacement can therefore add
its new counter values even when they have already exceeded the old ones.
An unconfirmed removal awaits a later observation of absence or rollback.
Failed native samples leave the previous observation timestamp available.

The ledger is process-local and holds up to 2,000 active or unreported
retired entries. A sample that exceeds this bound sets `incomplete` in the
response. Individual byte values saturate at the unsigned 64-bit limit.
The real tunnel test checks periodic collection and undelivered traffic
after a bulk peer replacement. The accounting unit test covers reusing a
key with higher kernel counters and consuming separate peer periods.

The paired HTTPS benchmark starts both implementations with 1 or 1,000
configured peers. One peer on each link moves packets through a real
tunnel. The fixture waits for each ten-second sampler to publish nonzero
traffic. It measures single and group reads with that sample present, then
single and group read-and-reset on the consumed period. Each case contains
four alternating blocks of 40 timed requests per implementation.
PasarGuard uses `GetStats` with `UserStat` or `UsersStat`, the matching reset
flag, and an email selector for the single-user read. Node selects by key.
[Reference sampled peer stats](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/stats.go#L27)

Times below are p50 / p95 in milliseconds. Each run has 160 requests per
operation and implementation.

| Operation | Configured peers | Node, first | PasarGuard, first | Node, repeat | PasarGuard, repeat |
| --- | ---: | ---: | ---: | ---: | ---: |
| Single read | 1 | 0.290 / 0.802 | 0.340 / 0.573 | 0.457 / 1.002 | 0.417 / 0.825 |
| Group read | 1 | 0.248 / 0.548 | 0.306 / 0.545 | 0.271 / 0.679 | 0.295 / 0.719 |
| Single reset | 1 | 0.233 / 0.432 | 0.300 / 0.508 | 0.215 / 0.363 | 0.251 / 0.395 |
| Group reset | 1 | 0.338 / 0.730 | 0.425 / 0.835 | 0.242 / 0.441 | 0.279 / 0.474 |
| Single read | 1,000 | 0.305 / 0.527 | 0.369 / 0.628 | 0.324 / 0.666 | 0.378 / 0.676 |
| Group read | 1,000 | 0.275 / 0.462 | 0.329 / 0.541 | 0.320 / 0.586 | 0.322 / 0.586 |
| Single reset | 1,000 | 0.231 / 0.442 | 0.286 / 0.472 | 0.221 / 0.408 | 0.269 / 0.461 |
| Group reset | 1,000 | 0.231 / 0.380 | 0.265 / 0.499 | 0.217 / 0.347 | 0.249 / 0.441 |

Node's block median was lower in 53 of 64 paired blocks. With one configured
peer, the single-read median changed order and Node had a higher single-read
p95 in both runs. Reset medians were lower for Node at both sizes in both
runs. Both runs used Go `1.27.4` on Linux `6.17.0-41-generic` and one logical
CPU of an AMD Ryzen 7 3750H. [First run](results/sampled-peer-usage-https.csv)
· [Repeat](results/sampled-peer-usage-repeat-https.csv)

Build both binaries using the [pinned lifecycle setup](../interfaces/lifecycle.md#reproduce).

```bash
NODE_TEST_WIREGUARD_PARENT_NETNS=$(readlink /proc/self/ns/net) \
  taskset -c 2 unshare -Urn \
  python3 benchmarks/wireguard/interfaces/measure-lifecycle.py \
  "$work/node" "$work/pasarguard" --sampled-peer-usage
```
