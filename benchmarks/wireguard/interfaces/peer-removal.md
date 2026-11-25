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

## Focused recheck at 1 and 100 peers

Use `--peer-removal-paired` with the [HTTPS fixture](lifecycle.md#reproduce).
Both servers run real WireGuard interfaces in the same isolated Linux network
namespace. The fixture alternates which removal goes first in each pair and
checks the exact kernel membership after every removal and restore. Node
uses sampled accounting in both runs. Each cell contains 120 observations.

| Configured peers | Run | Node p50 / p95, ms | PasarGuard p50 / p95, ms |
| ---: | --- | ---: | ---: |
| 1 | A | 0.887 / 1.761 | 0.834 / 1.502 |
| 1 | B | 0.766 / 1.244 | 0.747 / 1.252 |
| 100 | A | 0.896 / 2.254 | 0.870 / 1.717 |
| 100 | B | 0.789 / 1.516 | 0.783 / 1.734 |

Node's pooled p50 was higher by 20–53 µs at one peer and 6–26 µs at
100 peers. The p95 ordering changed across runs. The median of individual
paired differences also switched sign at both counts.

A syscall trace of separate HTTPS requests shows Node's live route Netlink
ownership check before the WireGuard write. The pinned reference performs
the WireGuard write without that separate route lookup. Both wait for a
kernel acknowledgment. The trace perturbs latency and is used only to
identify the stages. A separate Go probe calls the same native guard and
write on real isolated links. The median of three run p50s was 55.72 and
59.15 µs for the guard, and 100.5 and 104.8 µs for the write including its
acknowledgment, at 1 and 100 peers respectively.

A byte-identical fixed-layout removal payload was also measured in isolation.
The median of three Go benchmark run means was 850.1 ns per generic encode
and 23.8 ns per fixed-layout encode. Even this ideal gain of 0.826 µs is below
the observed HTTPS differences. The fixed encoder was not integrated or
claimed as an end-to-end win. The runtime, live ownership check, accounting
policy and acknowledgment semantics remain unchanged.

[HTTPS A](results/peer-removal-paired-https-a.csv)
· [HTTPS B](results/peer-removal-paired-https-b.csv)
· [Traced stages](results/peer-removal-traced-stages.csv)
· [Native probe and encoder timings](results/peer-removal-focused-linux-amd64.txt)

## Single-reply ownership check

Peer writes now read the current link with one targeted route Netlink reply.
The check still matches the current index, name, type, alias and `IFF_UP`.
It requests no link statistics and reads the reply into a bounded reusable
buffer. WireGuard mutation and acknowledgment, accounting policy, and Stop
are unchanged. In the isolated HTTPS fixture, a down link and a changed alias
both rejected removal and left the exact peer membership intact.

The three-build comparison paired removals on distinct interfaces in one
isolated namespace. Each cell contains 120 observations. Both Node builds
use sampled accounting. Times are p50 / p95 in milliseconds.

| Configured peers | Run | Previous Node | New Node | PasarGuard |
| ---: | --- | ---: | ---: | ---: |
| 1 | A | 0.766 / 1.220 | 0.667 / 1.282 | 0.699 / 1.291 |
| 1 | B | 0.721 / 1.257 | 0.689 / 1.047 | 0.661 / 1.263 |
| 100 | A | 0.722 / 1.275 | 0.689 / 1.314 | 0.698 / 1.127 |
| 100 | B | 0.723 / 1.203 | 0.721 / 1.251 | 0.712 / 1.228 |

The median of paired differences favored the new build over previous Node
by 46–50 µs at one peer and 22–41 µs at 100 peers. The 100-peer p95 was
slightly higher than previous Node in both runs. PasarGuard led some rows.

```bash
export NODE_TEST_WIREGUARD_PARENT_NETNS=$(readlink /proc/self/ns/net)
unshare -Urn python3 -B benchmarks/wireguard/interfaces/compare-removal-guard.py \
  "$PWD/node" "$PWD/pasarguard" --peer-removal "$PWD/node-before"
```

[HTTPS A](results/peer-removal-single-reply-a.csv)
· [HTTPS B](results/peer-removal-single-reply-b.csv)
