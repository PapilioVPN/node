# Integrated Xray lifecycle and account synchronization

These checks compared the integrated code at `59ddc16` with the earlier
PapilioVPN Node `6d5691c` and PasarGuard `739eab9`. The fixture used the verified
Xray v26.11.9 Linux amd64 release, Go 1.27.4 for PapilioVPN and the driver, and
Go 1.27.3 for PasarGuard. The runner alternates target order, uses TLS over
loopback, and pins the server and client to different physical cores with
`GOMAXPROCS=1`.

## Xray start

The lifecycle runner starts a new Xray child on each cycle and verifies that
the running and stopped states are observable through the tested transport.
The final Node version probe executes only on the first launch of an unchanged
binary for each Node process. The median below is for repeated child launches.
Cold launch of a fresh Node process still includes the version probe.

Each number is the median of five per-round measurements in milliseconds.
The server used CPU 2 and the driver CPU 6. Each round used 10 start/stop
cycles before the optimization and six after it.

| Transport | Operation | Previous main p50 / p95 | Integrated before version reuse p50 / p95 | Integrated with version reuse p50 / p95 | PasarGuard in final run p50 / p95 |
| --- | --- | ---: | ---: | ---: | ---: |
| HTTP | Start | 23.64 / 26.95 | 42.92 / 55.95 | 23.18 / 29.82 | 1022.36 / 1033.19 |
| gRPC | Start | 24.26 / 27.80 | 43.74 / 56.06 | 26.02 / 26.29 | 1028.12 / 1038.75 |
| HTTP | Status | 0.207 / 0.342 | 0.212 / 0.533 | 0.195 / 0.614 | 0.256 / 1.395 |
| gRPC | Status | 0.227 / 0.382 | 0.211 / 0.515 | 0.240 / 0.956 | 0.380 / 3.068 |
| HTTP | Stop | 1.07 / 3.77 | 1.29 / 4.97 | 1.02 / 4.20 | 3.86 / 7.05 |
| gRPC | Stop | 1.07 / 3.74 | 1.30 / 4.44 | 1.40 / 4.51 | 4.13 / 10.35 |

Reusing the successful version observation returned the start median close to
the pre-integration result. The benchmark does not time keep-alive, live config
application or account recovery during restart.

[Lifecycle rounds](results/2026-11-24-integrated-lifecycle.csv)

## Single VLESS account synchronization

The driver checked exact membership in Xray after each request, outside the
timed interval. Each of the six cases uses the median of five rounds of 20
samples per target. All 1,200 operation samples succeeded in each build.
Server CPU was 0 and driver CPU was 4. Latencies are in microseconds.

| Transport | Case | Previous main p50 / p95 | Integrated p50 / p95 | PasarGuard in integrated run p50 / p95 |
| --- | --- | ---: | ---: | ---: |
| HTTP | Converged | 372 / 587 | 332 / 491 | 358 / 629 |
| HTTP | Add | 682 / 962 | 470 / 917 | 759 / 1014 |
| HTTP | Remove | 530 / 740 | 469 / 691 | 499 / 699 |
| gRPC | Converged | 304 / 380 | 544 / 1017 | 385 / 654 |
| gRPC | Add | 652 / 981 | 851 / 1651 | 899 / 1463 |
| gRPC | Remove | 510 / 724 | 835 / 1068 | 687 / 1290 |

A second gRPC pass ran seven rounds of 40 requests per target and case in
the opposite build order. The integrated build led both percentiles for
add (829 / 1446 versus 1251 / 1939 µs). The converged result led on p95
only (466 / 770 versus 446 / 1082 µs), and removal led on p50 only
(769 / 1783 versus 820 / 1198 µs). All 1,680 operation samples succeeded
in each build. The integrated gRPC results were slower than the earlier
Node build in both passes for these single-account cases.

Run the [lifecycle fixture](../reports/instances.md#xray-instance-lifecycle) with
`XRAY_BENCH_ROUNDS=5`, `XRAY_BENCH_CYCLES=6`, and `XRAY_BENCH_READS=12` for
the final lifecycle sample. For the account rows, use the
[VLESS sync fixture](vless-sync.md#benchmark-method) with `XRAY_BENCH_READS=20`.

[Account sync rounds](results/2026-11-24-integrated-sync-user.csv)

## Log delivery

Five [log delivery rounds](get-logs.md) used the same CPU pair as the account
benchmark. Each sample was timed from the producer traffic to the complete
log record at the subscriber. All expected records arrived. Latencies are in
microseconds, PapilioVPN / PasarGuard.

| Transport | Subscribers | p50 | p95 |
| --- | ---: | ---: | ---: |
| HTTP | 1 | 127 / 97 | 277 / 294 |
| HTTP | 8 | 200 / 153 | 510 / 693 |
| gRPC | 1 | 283 / 221 | 1443 / 446 |
| gRPC | 8 | 330 / 360 | 1060 / 637 |

[Log rounds](results/2026-11-24-integrated-logs.csv)
