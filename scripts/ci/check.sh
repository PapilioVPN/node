#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
bash scripts/install/host_test.sh
unformatted=$(gofmt -l cmd internal benchmarks)
if [[ -n "$unformatted" ]]; then
  printf 'Go files require formatting:\n%s\n' "$unformatted" >&2
  exit 1
fi

go mod tidy -diff
go mod verify
go vet ./...
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
