# WireGuard lifecycle over HTTP

The fixture runs PapilioVPN Node and [PasarGuard Node at 739eab9](https://github.com/PasarGuard/node/tree/739eab94a9343d9f0035baf7162796844109f4c4)
with the same synthetic key, interface name, IPv4 address, MTU 1380, UDP port
51820 and zero peers. PasarGuard host routing and NAT are disabled. Both use
TLS 1.3, authenticated requests, warm connections and one request at a time
on one logical CPU in an isolated Linux network namespace. The fixture
verifies interface creation, stable interface identity on reattach, and
absence after Stop. The timer includes receiving the HTTP response body.
Payload validation runs outside it. Each implementation keeps its own API
payload and health checks.
PasarGuard refreshes backend health and returns version metadata. Node
checks the link and returns its applied source fingerprint.

| Operation | Node p50, ms | PasarGuard p50, ms | Node p95, ms | PasarGuard p95, ms |
| --- | ---: | ---: | ---: | ---: |
| Cold Start | 3.18 | 5.60 | 4.38 | 8.33 |
| Same-source Start | 0.97 | 1.07 | 1.53 | 2.02 |
| Stop | 76.17 | 79.86 | 92.90 | 98.25 |

These percentiles use 500 requests per operation per implementation. Five
blocks alternate implementation order and warm the connection before
measurement. Cold Start had a lower median in all five blocks.
Same-source Start and Stop switched order between blocks. A separate
twelve-iteration probe timed `ip link del` for a live link at a median of
76.50 ms, close to the HTTP Stop measurements. Stop waits for kernel
deletion before responding.

Linux `6.17.0-41-generic`, AMD Ryzen 7 3750H, Go `1.27.4` for both
servers. [Raw request durations](results/lifecycle-http.csv)

## Reproduce

Use Linux with kernel WireGuard, `ip`, Python 3, OpenSSL, and permission to
create separate user and network namespaces. Select a logical CPU allowed
by your CPU affinity in place of CPU 2.

```bash
work=$(mktemp -d)
mkdir "$work/reference"
curl -fsSL https://codeload.github.com/PasarGuard/node/tar.gz/739eab94a9343d9f0035baf7162796844109f4c4 \
  | tar -xz --strip-components=1 -C "$work/reference"
GOTOOLCHAIN=go1.27.4 go build -o "$work/node" ./cmd/node
(
  cd "$work/reference"
  GOTOOLCHAIN=go1.27.4 go build -o "$work/pasarguard" ./cmd/node
)
NODE_TEST_WIREGUARD_PARENT_NETNS=$(readlink /proc/self/ns/net) \
  taskset -c 2 unshare --user --map-root-user --net python3 \
  benchmarks/wireguard/interfaces/measure-lifecycle.py "$work/node" "$work/pasarguard" \
  > "$work/lifecycle-http.csv"
```
