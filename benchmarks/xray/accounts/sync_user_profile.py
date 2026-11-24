"""Convert paired SyncUser Go benchmark logs to per-round CSVs."""

import argparse
import csv
from pathlib import Path


HEADER = (
    "phase", "existing_accounts", "operation", "round", "iterations",
    "mean_ns", "p50_ns", "p95_ns", "bytes_per_op", "allocs_per_op",
)


def measurements(path, phase, round_number):
    expected = 10 if phase == "stages" else 9
    lines = [
        line.split() for line in path.read_text().splitlines()
        if line.startswith("BenchmarkSyncUser")
    ]
    if len(lines) != expected:
        raise ValueError(f"{path} has {len(lines)} benchmark results, expected {expected}")

    for name, iterations, *fields in lines:
        _, *case = name.rsplit("-", 1)[0].split("/")
        if phase == "stages" and case == ["prepare-vless"]:
            kind, existing, operation = "credential", "", case[0]
        else:
            size, operation = case
            existing = size.removeprefix("existing=")
            kind = "stage" if phase == "stages" else phase
            operation = operation.removeprefix("stage-")
        metrics = dict(zip(fields[1::2], fields[::2], strict=True))
        yield (
            kind, existing, operation, round_number, iterations, metrics["ns/op"],
            metrics.get("p50-ns", ""), metrics.get("p95-ns", ""),
            metrics["B/op"], metrics["allocs/op"],
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("log_directory", type=Path)
    parser.add_argument("output_directory", type=Path)
    parser.add_argument("--prefix", default="sync-user-profile")
    parser.add_argument("--rounds", type=int, default=5)
    args = parser.parse_args()

    for revision in ("before", "after"):
        rows = [
            row
            for round_number in range(1, args.rounds + 1)
            for kind in ("grpc", "plan", "stages")
            for row in measurements(
                args.log_directory / f"{revision}-{kind}-{round_number}.txt",
                kind, round_number,
            )
        ]
        with (args.output_directory / f"{args.prefix}-{revision}.csv").open("w", newline="") as output:
            writer = csv.writer(output, lineterminator="\n")
            writer.writerow(HEADER)
            writer.writerows(rows)


if __name__ == "__main__":
    main()
