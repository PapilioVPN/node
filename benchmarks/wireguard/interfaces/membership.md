# WireGuard peer membership

Node lists live public keys and allowed IPs through one kernel dump. HTTP and
gRPC measurements use warm loopback connections and decode the response.
The command-line reference runs `wg show wg-node-test allowed-ips` and counts
the returned rows. The fixture uses idle peers with empty prefix lists. The
isolated integration test verifies a configured prefix and confirms that a
configured preshared key remains private.

| Peers | HTTP, ms/op | gRPC, ms/op | wg, ms/op |
| ---: | ---: | ---: | ---: |
| 1 | 0.132 | 0.117 | 1.153 |
| 1,000 | 2.758 | 1.821 | 2.880 |

Each value is the median of five run means from `BenchmarkPeerMembership`,
with `GOMAXPROCS=1` and CPU affinity in a separate network namespace. The pinned
[PasarGuard API](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto)
has no equivalent peer membership dump. [Raw results](results/membership-linux-amd64.txt)
