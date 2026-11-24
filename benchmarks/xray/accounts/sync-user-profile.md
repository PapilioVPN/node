# gRPC SyncUser by existing membership size

The comparison uses the same benchmark fixtures on Node commits `70d5e4f`
and `27bde71`. The fixture loads 1, 1,000, or 10,000 named VLESS accounts from an
isolated Xray configuration. A chunked Sync marks them as accepted for
replay before timing. Every timed request checks the result and acknowledgement
counts. A warm-up checks the entire native membership and credentials after
the operation. Each timed addition or removal is reversed with the timer
stopped so the next sample starts at the same membership size. A final native
read verifies the whole original membership.

Local public gRPC uses a persistent loopback TCP connection. The benchmark
process hosts both ends of gRPC and Xray runs as a separate child. Go 1.27.4,
Linux amd64, AMD Ryzen 7 3750H, CPUs 2 and 4, and GOMAXPROCS=2 were used on
both revisions. The standalone Xray v26.11.9 executable (`676eaf6`) had
SHA-256 `e87bce586acdb9136aa5b1f1d9c8db9f1ea1d145b6d86368c7f6f662f9f6a66d`.
Five rounds alternate revision order, each with 200 timed requests per size
and operation. These VLESS measurements compare two Node revisions.

[Before CSV](results/2026-11-24-sync-user-profile-before.csv) ·
[After CSV](results/2026-11-24-sync-user-profile-after.csv)

## gRPC round medians

Latency is in microseconds. Allocation is the Go benchmark process's bytes
per timed operation and excludes allocations in the Xray child.

| Existing accounts | Operation | p50 before → after | p95 before → after | Bytes/op before → after |
| ---: | --- | ---: | ---: | ---: |
| 1 | Add | 780 → 803 | 1,304 → 1,320 | 18,789 → 17,851 |
| 1 | Remove | 775 → 787 | 1,437 → 1,331 | 17,657 → 16,705 |
| 1 | No change | 206 → 227 | 358 → 409 | 12,197 → 11,115 |
| 1,000 | Add | 792 → 773 | 1,694 → 1,279 | 73,221 → 17,768 |
| 1,000 | Remove | 744 → 873 | 1,534 → 1,658 | 72,240 → 16,802 |
| 1,000 | No change | 269 → 247 | 761 → 417 | 66,948 → 11,279 |
| 10,000 | Add | 1,054 → 903 | 3,640 → 1,539 | 455,915 → 17,806 |
| 10,000 | Remove | 993 → 761 | 2,882 → 1,468 | 454,830 → 16,680 |
| 10,000 | No change | 439 → 195 | 2,398 → 354 | 449,407 → 11,277 |

## Isolated preparation and staging

The CSVs include three measurements outside gRPC and native I/O. The
`credential` rows time VLESS credential preparation and protobuf encoding.
The `plan` rows run the real single-account planner against in-memory
indexes with the credential already encoded. The `stage` rows open a warm
editor and check replay capacity using a prepared change, then discard the
staged result before dispatch or commit. The baseline stage includes the
accepted membership copy. Each isolated run lasts at least 200 ms.

Median round means for credential preparation were 1.79 → 1.59 µs with
304 bytes and five allocations per operation. Planner results ranged from
0.76–1.33 µs before to 0.84–1.36 µs after. In the isolated 10,000-account
no-change allocation profile, `maps.clone` accounted for 99.38% of sampled
bytes before. The new implementation stages changes to the selected records
and publishes them only after all native effects are confirmed.

| Existing accounts | Stage | Mean µs before → after | Bytes/op before → after |
| ---: | --- | ---: | ---: |
| 1 | Add | 2.67 → 1.34 | 1,416 → 472 |
| 1 | Remove | 2.92 → 1.02 | 1,256 → 312 |
| 1 | No change | 2.60 → 0.48 | 1,256 → 176 |
| 1,000 | Add | 70.06 → 1.33 | 55,816 → 472 |
| 1,000 | Remove | 83.10 → 1.08 | 55,656 → 312 |
| 1,000 | No change | 66.80 → 0.51 | 55,656 → 176 |
| 10,000 | Add | 522.49 → 1.33 | 438,072 → 472 |
| 10,000 | Remove | 701.50 → 1.10 | 437,912 → 312 |
| 10,000 | No change | 647.40 → 0.54 | 437,912 → 176 |

## Reproduce

Run from the profiled revision with Go 1.27.4 and set `NODE_TEST_XRAY` to the
absolute path of the standalone executable. Prepare a detached baseline and
copy only the shared measurement fixtures into it.

```bash
profile_dir=$(mktemp -d)
git worktree add --detach "$profile_dir/baseline" 70d5e4f
account_dir=internal/engines/xray/accounts
for path in integration/fixtures_test.go integration/protocol_sync_test.go \
  integration/sync_user_benchmark_test.go membership/sync_user_benchmark_test.go \
  native/sync_user_benchmark_test.go; do
  cp "$account_dir/$path" "$profile_dir/baseline/$account_dir/$path"
done

for variant in before after; do
  source_tree=$PWD
  if [[ $variant == before ]]; then source_tree=$profile_dir/baseline; fi
  (
    cd "$source_tree"
    go test -c -o "$profile_dir/$variant-grpc.test" "./$account_dir/integration"
    go test -c -o "$profile_dir/$variant-plan.test" "./$account_dir/membership"
    go test -c -o "$profile_dir/$variant-stages.test" "./$account_dir/native"
  )
done

export GOMAXPROCS=2
for round in 1 2 3 4 5; do
  if (( round % 2 )); then order=(before after); else order=(after before); fi
  for variant in "${order[@]}"; do
    taskset -c 2,4 "$profile_dir/$variant-grpc.test" -test.run '^$' \
      -test.bench '^BenchmarkSyncUserGRPC$' -test.benchtime=200x \
      > "$profile_dir/$variant-grpc-$round.txt"
    taskset -c 2,4 "$profile_dir/$variant-stages.test" -test.run '^$' \
      -test.bench '^BenchmarkSyncUserPhases$' -test.benchtime=200ms \
      > "$profile_dir/$variant-stages-$round.txt"
  done
done

for round in 1 2 3 4 5; do
  if (( round % 2 )); then order=(before after); else order=(after before); fi
  for variant in "${order[@]}"; do
    taskset -c 2,4 "$profile_dir/$variant-plan.test" -test.run '^$' \
      -test.bench '^BenchmarkSyncUserPlanning$' -test.benchtime=200ms \
      > "$profile_dir/$variant-plan-$round.txt"
  done
done

python3 benchmarks/xray/accounts/sync_user_profile.py "$profile_dir" "$profile_dir"

for variant in before after; do
  taskset -c 2,4 "$profile_dir/$variant-stages.test" -test.run '^$' \
    -test.bench '^BenchmarkSyncUserPhases$/^existing=10000$/^stage-no-change$' \
    -test.benchtime=2s -test.cpuprofile="$profile_dir/$variant-cpu.pprof" \
    -test.memprofile="$profile_dir/$variant-heap.pprof"
  go tool pprof -top -alloc_space "$profile_dir/$variant-heap.pprof"
done
```
