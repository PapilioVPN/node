# WireGuard checkpoint recovery

The benchmark opens new process-owned sockets and the locked checkpoint
directory, reads the checkpoint and verifies the existing interface with
one targeted kernel link read. Each iteration checks that the expected
fingerprint was recovered and closes its handles. The file is warm in
the filesystem cache. Stop, checkpoint writes and API listener startup
run outside the timed workload.

| Configured peers | Time per recovery, ms |
| ---: | ---: |
| 0 | 0.497 (0.421–0.696) |
| 1,000 | 0.481 (0.473–0.641) |

Each result is the median and range of five run means. The benchmark uses
Linux `6.17.0-41-generic`, Go `1.27.4` and one logical CPU on an AMD Ryzen
7 3750H. A separate native integration test stops and kills the Node process,
then verifies the interface, fingerprint and tunnel after each restart.
[Raw Go benchmark output](results/recovery-linux-amd64.txt)

PasarGuard's pinned entry point starts a fresh management service and
disconnects the backend on graceful exit. Its Start path is a different
workload. [PasarGuard process lifecycle](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/cmd/node/main.go#L36-L58)

Use the namespace setup in [the interface benchmark](README.md#reproduce)
with `-test.bench '^BenchmarkCheckpointRecovery$'`.
