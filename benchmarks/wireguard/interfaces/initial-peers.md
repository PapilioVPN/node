# Initial peers and live configuration

Start accepts an optional `initial_peers` array and applies it before
marking the interface ready. The validated array belongs to the Start
source fingerprint. A live Start accepts one change to MTU, addresses,
listen port, or private key. A key rotation changes the server public key
and requires clients to use it for future handshakes.

HTTPS cold start latencies in milliseconds, median of three paired
observations. Each response is checked against the live kernel peer set.

| Initial peers | Node | PasarGuard |
| ---: | ---: | ---: |
| 1 | 4.399 | 5.410 |
| 100 | 19.545 | 274.202 |

An exploratory 1,000-peer start returned in 127.904 ms on Node. The
reference did not return within the 15-second fixture deadline, which
left no paired timing at that size. The live port, address and key paths
are verified by kernel readback in the isolated integration test.
Use `--initial-peers` with the [matched fixture](lifecycle.md#reproduce).
[Raw paired timings](results/initial-peers-https.csv)
