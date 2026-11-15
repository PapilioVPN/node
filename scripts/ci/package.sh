#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd -- "$repo_dir"
build_version=${1:-$(git describe --always --dirty)}
artifact_dir="$repo_dir/.local/artifacts"
mkdir -p -- "$artifact_dir/context"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=$build_version" -o "$artifact_dir/context/node" ./cmd/node
cp -- deploy/config.example.yml "$artifact_dir/context/config.example.yml"
tar -czf "$artifact_dir/node-linux-amd64.tar.gz" -C "$artifact_dir/context" node config.example.yml
(cd -- "$artifact_dir" && sha256sum node-linux-amd64.tar.gz > checksums.sha256)
