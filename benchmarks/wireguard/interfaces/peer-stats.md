# WireGuard peer statistics

Both Node transports read one live WireGuard device dump per request and
decode only the public key, RX and TX counters, and last handshake. The
comparison launches `wg show <interface> dump` per call and parses the same
fields from its output. The CLI timings include process startup and its
additional output columns. The fixture has idle peers. A separate tunnel test
compares both transports with the kernel after real traffic.

| Peers | Node HTTP, ms/op | Node gRPC, ms/op | `wg`, ms/op |
| ---: | ---: | ---: | ---: |
| 1 | 0.136 | 0.125 | 1.270 |
| 1,000 | 3.353 | 2.033 | 4.370 |

Each cell is the median of three Go benchmark run means. Linux
`6.17.0-41-generic`, Go `1.27.4`, wireguard-tools `1.0.20210914`, and one
logical CPU of the AMD Ryzen 7 3750H. Node uses warm loopback HTTP and gRPC
connections without TLS in an isolated network namespace. [Raw output](results/peer-stats-linux-amd64.txt)

The pinned PasarGuard user-stats API reads periodically sampled tracker
counters by account. This benchmark uses the native CLI for the live peer
projection, which also includes the last handshake. [Reference stats reader](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/stats.go)
· [Reference sampling loop](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/jobs.go)

Use the isolated setup in [the interface benchmark](README.md#reproduce) and
select `-test.bench '^BenchmarkPeerStats$' -test.benchtime=150ms -test.count=3`.
