# WireGuard single-peer traffic

`WireGuardStatsService.GetPeerTraffic` reads RX and TX bytes for an exact
32-byte public key. `ResetPeerTraffic` returns the preceding period and starts
the next period at the same live sample. HTTP uses
`GET /v1/wireguard/interfaces/{name}/peers/{key}/traffic` and
`POST /v1/wireguard/interfaces/{name}/peers/{key}/traffic/resets`, with an
unpadded base64url key and decimal strings for the 64-bit byte counters.

Logical resets require managed mode. Node holds up to 1,000 reset baselines
in memory for its configured link. A new Node process starts with live kernel
counters. Managed peer removal, link removal, a missing peer, a changed link
index, or observed counter rollback drops the old baseline. An unconfirmed
managed removal also invalidates the affected baseline. Changes made
through Node preserve these scopes even when a replacement peer has already
exceeded the old counters. Detection of external replacement depends on
observing one of the kernel changes above. An in-flight read that crosses
managed removal returns busy. `ListPeerStats` continues to report kernel
bytes and the latest handshake.

Linux WireGuard selects a device by name or index for `WG_CMD_GET_DEVICE`.
The single-peer read uses one dump, retains the requested counters, and
consumes the multipart completion before publishing. The kernel work can
grow with the peer count. Node avoids building and serializing the full
peer list for this operation.

The loopback HTTP comparison uses an idle managed interface with 1 or 1,000
peers. `SelectedHTTP` reads the last configured peer, `FullHTTP` reads and
filters the full live list for the same peer, and `ResetHTTP` samples and
resets the selected peer. The isolated tunnel test separately compares both
transports with real traffic before and after reset. The pinned PasarGuard
single-user operation reads a periodically sampled tracker keyed by email.
Its latency corresponds to a different freshness guarantee. [PasarGuard
single-user read](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/wireguard/stats.go#L27)

| Peers | Selected HTTP, ms/op | Full HTTP, ms/op | Reset HTTP, ms/op |
| ---: | ---: | ---: | ---: |
| 1 | 0.131 | 0.140 | 0.157 |
| 1,000 | 1.360 | 3.569 | 1.092 |

Each value is the median of two Go benchmark run means. The selected read
was faster at 1,000 peers in both runs. At one peer the order changed across
runs. The 1,000-peer selected read allocated about 12 kB per operation,
compared with about 598 kB for the full response and client-side selection.
The run used Go `1.27.4` and one logical CPU of an AMD Ryzen 7 3750H.
[Raw Go benchmark output](results/peer-traffic-linux-amd64.txt)

Build and run the fixture using the [isolated benchmark setup](../interfaces/README.md#reproduce).
Use `-test.bench '^BenchmarkPeerTraffic$' -test.benchtime=120ms -test.count=2`
with the selected logical CPU.
