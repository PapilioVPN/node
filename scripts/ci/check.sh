#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd -- "$repo_dir"

mapfile -d '' -t go_files < <(git ls-files -z --cached --others --exclude-standard -- '*.go')
unformatted=$(gofmt -l "${go_files[@]}")
if [[ -n "$unformatted" ]]; then
  printf 'Run gofmt on:\n%s\n' "$unformatted" >&2
  exit 1
fi

go mod tidy -diff
go mod verify
go vet -tags=integration ./...
go test -race -tags=integration ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -db "${1:-https://vuln.go.dev}" ./...
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml
