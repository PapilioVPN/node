# WireGuard peer removal

Managed Node supports two accounting policies for the same acknowledged
kernel deletion. `peer_removal_usage: final` attempts a fresh counter read
and is the default. `peer_removal_usage: sampled` checks link ownership,
sends the delete, and retains pending usage from the latest published sample.
It discards a concurrent sample if the snapshot overlaps the deletion.
Traffic since the last published sample may go unrecorded in sampled mode.
PasarGuard deletes the peer without a fresh final native stats capture.

## Native counter read

The [Linux Netlink API](https://docs.kernel.org/netlink/specs/wireguard.html#get-device)
selects the device by name or index. Reading one peer still requires the
multipart device dump. The benchmark selects the last inserted peer on a
fresh isolated interface, with empty allowed-IP sets. Values are medians of
three Go benchmark run means.

| Configured peers | Native targeted read, µs/op |
| ---: | ---: |
| 1 | 16.568 |
| 100 | 112.433 |
| 1,000 | 845.951 |

[Raw counter read](results/peer-removal-counter-read-linux-amd64.txt)

## Paired HTTPS deletion

The fixture uses the same Node binary in final and sampled modes, plus
the [pinned PasarGuard reference](https://github.com/PasarGuard/node/tree/739eab94a9343d9f0035baf7162796844109f4c4).
It tries all six server orders. Each cell reports the p50 and nearest-rank
p95 in milliseconds across 72 deletions. The target is removed and re-added
between requests. The fixture checks exact kernel peer membership after
each deletion and excludes these checks from the request time.

| Configured peers | Node final p50 / p95 | Node sampled p50 / p95 | PasarGuard p50 / p95 |
| ---: | ---: | ---: | ---: |
| 1 | 0.591 / 1.362 | 0.605 / 1.301 | 0.510 / 1.025 |
| 100 | 0.731 / 1.047 | 0.475 / 0.831 | 0.387 / 0.624 |
| 1,000 | 2.371 / 3.646 | 0.632 / 4.130 | 0.731 / 1.459 |

The 1,000-peer sampled median fell by 73% against final accounting. The
sampled p95 was higher in this run. Its four highest samples fell in one
block. Other blocks peaked at 1.115 ms or below.

Use `--peer-removal` with the [HTTPS fixture](lifecycle.md#reproduce).
[Raw request timings](results/peer-removal-https.csv)

## Repeat

The second run used the same fixture and sample count per cell.

| Configured peers | Node final p50 / p95 | Node sampled p50 / p95 | PasarGuard p50 / p95 |
| ---: | ---: | ---: | ---: |
| 1 | 0.610 / 1.064 | 0.533 / 1.280 | 0.582 / 1.063 |
| 100 | 0.874 / 1.419 | 0.656 / 1.265 | 0.643 / 1.056 |
| 1,000 | 2.051 / 2.748 | 0.580 / 1.259 | 0.723 / 1.930 |

The sampled mode reduced the 1,000-peer median by more than 70% in both
runs. It had a lower p50 than PasarGuard at 1,000 peers in both runs. The
p95 comparison changed between runs. Smaller interfaces gain less from
avoiding the dump. [Raw repeat timings](results/peer-removal-repeat-https.csv)
