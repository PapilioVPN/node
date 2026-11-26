# OpenVPN

The fixture uses Node's public HTTP and gRPC API on a single active OpenVPN server. OpenVPN 2.6.19 was built from upstream source, and the server and client network namespaces are isolated. Node and the benchmark client run on Linux amd64 with two pinned logical CPUs. Validation checks a 169-byte profile. HTTP uses warm plaintext loopback connections, and gRPC uses a reused connection. Each value is the median of three Go per-run means.

| Operation | Direct OpenVPN | Node HTTP | Node gRPC |
| --- | ---: | ---: | ---: |
| Native option validation | 5.638 ms | 5.885 ms | 6.126 ms |
| Read execution status | — | 0.184 ms | 0.194 ms |

Validation runs the same native option parser in all three paths. Node adds managed-source checks and serialization. Status reads only the state recorded by Node, which includes readiness and the applied source fingerprint. The native CLI has a different status contract. The pinned PasarGuard Node has no OpenVPN backend.

[Raw measurements](results/2026-11-26-native-lifecycle.txt)

```sh
export NODE_TEST_OPENVPN=/absolute/path/to/openvpn
export NODE_TEST_OPENVPN_PARENT_NETNS="$(readlink /proc/self/ns/net)"
GOMAXPROCS=2 taskset -c 2,3 unshare -Urn go test \
  ./internal/engines/openvpn/instances/integration -run '^$' \
  -bench BenchmarkNativeManagement -benchtime=250ms -count=3
```
