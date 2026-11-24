# Xray instances and configuration

[Benchmark dashboard](../README.md)

## Matched gRPC Start paths with initial accounts

Both servers receive byte-identical Xray JSON in `Start` and the same
fixture VLESS account in their respective request schemas. PapilioVPN sends
it as `XrayInitialAccount.credential`. The legacy `email` and `uuid` fields
continue to encode a VLESS account. Initial snapshots take effect when
launching an execution. Heartbeat and live configuration updates keep the
running membership.

| Start path | PapilioVPN p50 / p95 (ms) | PasarGuard p50 / p95 (ms) | Raw samples |
| --- | ---: | ---: | --- |
| Cold start with one account | 48.576 / 58.988 | 1030.175 / 1055.202 | [CSV](results/2026-11-24-grpc-startup.csv) |
| Heartbeat | 1.265 / 3.435 | 1.739 / 5.605 | [CSV](results/2026-11-24-grpc-heartbeat.csv) |
| Live routing update | 62.049 / 74.586 | 35.267 / 50.682 | [CSV](results/2026-11-24-grpc-live-update.csv) |
| Restart with one account resent | 85.226 / 113.046 | 1034.784 / 1054.500 | [CSV](results/2026-11-24-grpc-restart.csv) |
| Restart with account replay | 90.041 / 128.133 | — | [CSV](results/2026-11-24-grpc-restart-replay.csv) |

- The 2026-11-24 run used five alternating rounds with 12 measured cycles and
  one warm-up cycle per server per round. The table gives nearest-rank p50 and
  p95 over 60 samples per server and path, in milliseconds. Both servers and
  the driver were built with Go 1.27.4, `CGO_ENABLED=0`, and `-trimpath`.
- Both servers used the same stock Xray v26.11.9 binary. The pinned reference
  was PasarGuard Node `739eab94a9343d9f0035baf7162796844109f4c4`. Servers and
  Xray children were pinned to CPU 2 with `GOMAXPROCS=1`, and the driver used CPU 6
  on a different physical core of the same AMD Ryzen 7 3750H workstation.
  The fixture ran in PID and network namespaces on Linux 6.17.0-41. The driver
  reused one TLS 1.3 gRPC connection per server.
- Each cycle starts a fresh Xray child, repeats the same request for heartbeat,
  changes a routing rule live, and changes the inbound port to force replacement.
  The driver checks the child PID after each operation and sends authenticated
  VLESS traffic to verify the expected loopback origin. It checks that Stop
  reaps the child after each cycle. Times cover the public RPC through response
  decoding. External PID and traffic checks run outside the timed interval.
- The paired restart resends the account to both servers. The replay row times a
  second inbound port change with `initial_users` omitted on PapilioVPN. It
  exercises replay from confirmed membership within the same Node process.
  The reference controller constructs a new backend using the incoming users
  and offers no equivalent replay guarantee for this config-triggered restart.
  [PasarGuard controller](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/controller.go#L177-L312)
- PasarGuard's startup readiness loop first probes after one second. A separate
  process trace confirmed that PapilioVPN runs `xray run -test` to validate the
  entire candidate before `xray convert pb` prepares the live routing update.
  PasarGuard uses `xray convert pb` for its live plan. The table measures those
  end-to-end server policies on a one-account workload.
  [PasarGuard readiness loop](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/jobs.go#L43-L67)

Use the binaries, descriptor, and verified stock Xray from the setup below.
Build the reference with `GOTOOLCHAIN=go1.27.4` for this run.

```bash
GOMAXPROCS=1 XRAY_BENCH_START_PATHS=1 XRAY_BENCH_ROUNDS=5 \
  XRAY_BENCH_CYCLES=12 XRAY_BENCH_SERVER_CPU=2 XRAY_BENCH_CLIENT_CPU=6 \
  bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
  "$work/bench" "$work/reference.pb" "$work/start-paths"
```

## Xray instance lifecycle

The [integrated lifecycle and account report](../integration/README.md)
includes measured launch, stop, status, single-account sync, and log delivery
after the account and lifecycle branches were combined.

On 2026-11-23, PapilioVPN Node had lower p50 and p95 latencies in all six tested transport and operation pairs. The table shows the median of five per-round percentiles, in milliseconds. Each round includes 30 cold starts, 360 status reads while ready, and 30 joined stops per server.

| Transport | Operation | PapilioVPN p50 | PapilioVPN p95 | PasarGuard p50 | PasarGuard p95 |
| --- | --- | ---: | ---: | ---: | ---: |
| HTTP | Start | 29.768 | 42.699 | 1028.732 | 1044.833 |
| HTTP | Status | 0.217 | 1.244 | 0.324 | 2.879 |
| HTTP | Stop | 3.270 | 5.641 | 4.520 | 7.577 |
| gRPC | Start | 31.724 | 39.090 | 1029.851 | 1042.816 |
| gRPC | Status | 0.290 | 1.547 | 0.531 | 2.236 |
| gRPC | Stop | 3.694 | 5.474 | 5.244 | 9.254 |

[HTTP per-round results](results/2026-11-23-http.csv) · [gRPC per-round results](results/2026-11-23-grpc.csv)

### Workload and limits

- Both Node binaries and the benchmark client were built with Go 1.27.3, `CGO_ENABLED=0`, and `-trimpath`. The host ran Linux 6.17 on an AMD Ryzen 7 3750H. Both servers and their children used `GOMAXPROCS=1` and were pinned to CPU 2. The client used CPU 4 on a different physical core. The fixture used isolated Linux PID and network namespaces with loopback.
- Both servers ran the same Xray v26.11.9 executable and started from the same JSON with one idle loopback SOCKS inbound, one direct outbound, and zero users. Each product supplied its own native management configuration. The public client used a single persistent TLS 1.3 connection per server and transport, provided an API key, and decoded each response. HTTP used HTTP/1.1.
- `Start` measures a cold start from no running process to successful native `GetSysStats`. In the historical run, the client sent the configuration to PasarGuard and the instance ID to PapilioVPN, where the configuration was already on disk. Reruns now supply the same configuration in both Start requests. PasarGuard's native readiness loop probes once per second. PapilioVPN uses a private Unix socket with short reconnect delays. [PasarGuard readiness loop](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/jobs.go#L43-L67)
- `Status` compares reads of the running lifecycle state. PasarGuard also returns version fields. `Stop` measures from the request until a successful response, then the client independently checks that Linux lists no remaining child process. PapilioVPN sends SIGTERM with a bounded SIGKILL fallback. PasarGuard sends SIGKILL first. The workload contains no active client traffic. The reference version is PasarGuard 0.5.7 at `739eab94a9343d9f0035baf7162796844109f4c4`. [PasarGuard stop implementation](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/backend/xray/core.go#L308-L352)

### Reproduce

Use Linux with unprivileged user namespaces, the repository Go version, protoc, OpenSSL, curl, unzip, and iproute2. Run from the repository root. Set `XRAY_BENCH_SERVER_CPU` and `XRAY_BENCH_CLIENT_CPU` to distinct physical cores if CPUs 2 and 4 are unavailable.

```bash
work=$(mktemp -d)
ref=739eab94a9343d9f0035baf7162796844109f4c4
mkdir "$work/reference"
curl -fsSL "https://codeload.github.com/PasarGuard/node/tar.gz/$ref" |
  tar -xz --strip-components=1 -C "$work/reference"
curl -fsSL https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-linux-64.zip \
  -o "$work/xray.zip"
printf '%s  %s\n' b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82 "$work/xray.zip" | sha256sum -c
unzip -q "$work/xray.zip" -d "$work/xray"
chmod +x "$work/xray/xray"
(cd "$work/reference" && GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 go build -trimpath -o "$work/pasarguard" ./cmd/node)
CGO_ENABLED=0 go build -trimpath -o "$work/node" ./cmd/node
CGO_ENABLED=0 go build -trimpath -o "$work/bench" ./benchmarks/xray
protoc -I "$work/reference" --descriptor_set_out="$work/reference.pb" "$work/reference/common/service.proto"
bash benchmarks/xray/run.sh "$work/node" "$work/pasarguard" "$work/xray/xray" \
  "$work/bench" "$work/reference.pb" "$work/results"
```

## Xray config validation

The table reports the median of five per-run mean timings on 2026-11-23, in milliseconds. The reference is direct `xray run -test` on the same Xray v26.11.9 binary, JSON, and management overlay. The pinned PasarGuard revision has no equivalent standalone management operation. [PasarGuard gRPC contract](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/common/service.proto#L218-L243) · [PasarGuard HTTP routes](https://github.com/PasarGuard/node/blob/739eab94a9343d9f0035baf7162796844109f4c4/controller/rest/service.go#L25-L65)

| Configuration | Direct Xray | Node HTTP | Node gRPC |
| --- | ---: | ---: | ---: |
| Valid | 22.72 | 23.56 | 23.59 |
| Rejected protocol | 20.09 | 21.88 | 23.97 |

[Per-run measurements](results/2026-11-23-config-validation.csv) include iteration counts and Go allocations in the benchmark process. The fixture used Linux amd64, Go 1.26.11, an AMD Ryzen 7 3750H, and CPU 2 with `GOMAXPROCS=1`. HTTP and gRPC used persistent loopback connections without TLS. The direct CLI path reused the prepared overlay. The API paths include request decoding, configuration preparation, native execution, and response decoding. Per-run timing ranges overlap, so the figures describe end-to-end cost for these two small files.

Run from the repository root with the Xray v26.11.9 executable and adjust the CPU affinity to match your machine.

```bash
NODE_TEST_XRAY=/absolute/path/to/xray GOMAXPROCS=1 taskset -c 2 \
  go test ./internal/engines/xray/configs/integration -run '^$' \
  -bench '^BenchmarkValidateConfig$' -benchmem -benchtime=300ms -count=5
```

## Xray lifecycle after startup error reporting

The rerun uses the Xray lifecycle fixture above with five rounds, six cold starts and joined stops per round, and 12 status reads per start. The table shows the median of the per-round p50s in milliseconds for successful calls. The occupied-port and readiness-timeout cases were verified separately in integration tests.

| Transport | Operation | PapilioVPN p50 | PasarGuard p50 |
| --- | --- | ---: | ---: |
| HTTP | Start | 37.776 | 1034.452 |
| HTTP | Status | 0.341 | 0.459 |
| HTTP | Stop | 4.480 | 5.510 |
| gRPC | Start | 32.929 | 1028.577 |
| gRPC | Status | 0.390 | 0.450 |
| gRPC | Stop | 4.008 | 4.961 |

Use `XRAY_BENCH_CYCLES=6` with the reproduction command in the lifecycle section. [HTTP per-round results](results/2026-11-23-startup-details-http.csv) · [gRPC per-round results](results/2026-11-23-startup-details-grpc.csv)

## Xray integration performance recheck

The baseline `a51a787` combines the routing controls, VLESS account operations,
and traffic resets. Both targets and the driver used Go 1.27.4, stock Xray
v26.11.9 and the pinned PasarGuard revision `739eab9`. The driver used CPU 6.
Both servers and Xray children used CPU 2. Each row represents five
alternating rounds using 450 ms windows for runtime and reset or 550 ms
windows for TestRoute. No measured requests were rejected.

Each cell shows Node / PasarGuard. The first two values are latency in
microseconds. The third is successful requests per second.

| Operation | API | Clients | Baseline p50 · p95 · reads/s | Trial p50 · p95 · reads/s |
| --- | --- | ---: | --- | --- |
| TestRoute | HTTP | 1 | 479.4 / 526.6 · 964.7 / 960.3 · 1765.4 / 1728.8 | 314.1 / 339.8 · 359.0 / 460.9 · 2989.2 / 2726.8 |
| TestRoute | HTTP | 8 | 1309.3 / 1352.5 · 3182.4 / 3179.7 · 5336.1 / 5146.9 | 929.4 / 1062.6 · 2591.7 / 2768.1 · 7279.8 / 6476.6 |
| Runtime stats | HTTP | 1 | 462.4 / 505.1 · 983.9 / 1028.4 · 1777.0 / 1662.3 | 375.6 / 395.7 · 687.2 / 505.0 · 2360.8 / 2332.3 |
| Runtime stats | HTTP | 8 | 993.3 / 1236.2 · 2220.9 / 3155.5 · 7019.0 / 5610.0 | 854.2 / 1161.4 · 1764.9 / 2983.1 · 8102.8 / 6077.3 |
| Runtime stats | gRPC | 1 | 452.0 / 533.9 · 771.5 / 1043.5 · 1928.6 / 1629.2 | 462.7 / 515.9 · 732.9 / 829.5 · 1999.0 / 1723.2 |
| Runtime stats | gRPC | 8 | 1050.4 / 1312.1 · 2499.8 / 3014.9 · 6847.9 / 5470.4 | 1001.5 / 1428.0 · 2106.2 / 3266.3 · 7297.9 / 4933.7 |
| Reset pair, 1 account | HTTP | 1 | 1162.3 / 799.9 · 3594.6 / 3315.1 · 692.5 / 889.0 | 480.5 / 339.5 · 628.5 / 471.6 · 1933.2 / 2689.1 |
| Reset pair, 1 account | HTTP | 8 | 3053.6 / 1867.9 · 6069.7 / 4590.0 · 2316.6 / 3745.9 | 1286.8 / 1010.1 · 3228.1 / 2567.4 · 5407.6 / 6912.9 |
| Reset pair, 1 account | gRPC | 1 | 1081.3 / 852.6 · 2735.8 / 2686.9 · 792.2 / 949.8 | 508.9 / 364.6 · 720.6 / 504.1 · 1745.3 / 2561.4 |
| Reset pair, 1 account | gRPC | 8 | 2780.3 / 2064.8 · 5804.9 / 4382.1 · 2588.3 / 3498.9 | 1418.6 / 1067.5 · 3245.2 / 2726.1 · 4980.7 / 6391.8 |
| Reset pair, 512 accounts | HTTP | 1 | 731.0 / 592.5 · 1244.9 / 1061.6 · 1216.6 / 1516.3 | 449.0 / 392.4 · 541.1 / 546.4 · 2075.3 / 2362.9 |
| Reset pair, 512 accounts | HTTP | 8 | 1873.7 / 1666.1 · 4025.5 / 3451.7 · 3838.1 / 4358.6 | 1298.1 / 1390.4 · 3229.8 / 3114.3 · 5346.7 / 5203.1 |
| Reset pair, 512 accounts | gRPC | 1 | 856.7 / 702.4 · 1659.8 / 1176.5 · 1055.0 / 1300.4 | 523.7 / 478.8 · 999.6 / 782.2 · 1623.2 / 1849.3 |
| Reset pair, 512 accounts | gRPC | 8 | 2055.7 / 2024.3 · 4094.6 / 4092.8 · 3517.6 / 3557.2 | 1508.9 / 1467.0 · 3549.2 / 2933.8 · 4505.1 / 4961.9 |

The retained TestRoute change keeps the native request and its routing context
in one allocation and detects extra JSON input without decoding an extra
value. The trial led on all three metrics at both tested HTTP client counts.
The remaining trial changes reused a runtime reply and reused the reset
request and reply while adjusting native call options. The baseline already
led in the four runtime cases. The reset trial still trailed on at least one
metric in each case. PasarGuard's own timing shifted between runs on the
shared workstation. Both statistics paths retain the baseline implementation.

[Per-round measurements](../integration/results/2026-11-23-integrated-control-retest.csv)

Use the [routing fixture](../routing/control.md#benchmark) with
`XRAY_BENCH_ROUTING_RULES=4`, `XRAY_BENCH_ROUTING_OPERATION=test`,
`XRAY_BENCH_ROUTING_CLIENTS=1,8`, `XRAY_BENCH_ROUTING_WINDOW=550ms` and
`XRAY_BENCH_TRANSPORTS=http` for the retained change. Build both nodes and
the driver with Go 1.27.4. Set `XRAY_BENCH_ROUNDS=5` and
`XRAY_BENCH_CLIENT_CPU=6`. Use the corresponding runtime statistics and named
pair reset fixtures above for those workloads.
