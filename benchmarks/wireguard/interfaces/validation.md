# WireGuard configuration validation

The fixture sends valid interface configurations to the read-only validation
API over warm loopback connections in a separate Linux network namespace.
Each request returns one validation result. The source uses one key and
either one or the maximum of sixteen configured addresses. Validation and
Start share the input parser. Validation performs no kernel operation.
PasarGuard at the pinned revision checks configuration as part of Start.
[PasarGuard API contract](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto)

| Addresses | HTTP, µs/op | gRPC, µs/op |
| ---: | ---: | ---: |
| 1 | 119.2 (115.8–196.6) | 145.4 (132.1–154.8) |
| 16 | 114.7 (112.4–157.4) | 124.8 (121.3–139.3) |

Each cell reports the median and range of five run means. The fixture uses
one logical CPU, sequential requests and Go 1.27.4 on Linux
`6.17.0-41-generic`, AMD Ryzen 7 3750H.
[Raw Go benchmark output](results/validation-linux-amd64.txt)

Use the namespace setup in [the interface benchmark](README.md#reproduce)
with `-test.bench '^BenchmarkConfigValidation$'`.
