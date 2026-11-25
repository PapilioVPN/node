# WireGuard status configuration

`GetStatus` includes the kernel's effective public key, listen port and local
addresses when requested. HTTP clients set `?include_configuration=true`
and gRPC clients set `include_configuration` on `WireGuardInterfaceRequest`.
The default path reads only the link status. The detailed path also reads
addresses and drains a WireGuard device dump while skipping peer decoding.

| Idle peers | Basic HTTP, ms/op | Detailed HTTP, ms/op | Basic gRPC, ms/op | Detailed gRPC, ms/op |
| ---: | ---: | ---: | ---: | ---: |
| 0 | 0.150 | 0.182 | 0.146 | 0.187 |
| 1,000 | 0.121 | 1.006 | 0.152 | 0.945 |

Values are medians of five run means from `BenchmarkStatusConfiguration`.
The fixture uses warm loopback connections, one isolated network namespace,
one CPU, and one configured IPv4 address. The integration test separately
compares HTTP and gRPC results with the port, address and key verified after
live changes. PasarGuard has no equivalent detailed read in its
[pinned API](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto).
[Raw results](results/status-configuration-linux-amd64.txt)
