#!/usr/bin/env bash
set -euo pipefail

# Compiler distribution is deliberately pinned to the first supported tool host.
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  printf 'Code generation currently requires Linux amd64.\n' >&2
  exit 1
fi

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd -- "$repo_dir"
tool_dir="$repo_dir/.local/foundation-tools/bin"
mkdir -p -- "$tool_dir"
download_dir=$(mktemp -d)
trap 'rm -rf -- "$download_dir"' EXIT

curl --fail --location --silent --show-error --max-time 60 \
  https://github.com/protocolbuffers/protobuf/releases/download/v36.2/protoc-36.2-linux-x86_64.zip \
  -o "$download_dir/protoc.zip"
printf '%s  %s\n' \
  121f6c7afe1d4d0e3ea6aab9432038599250134cbf4474cb1167d2c7decd4278 \
  "$download_dir/protoc.zip" | sha256sum --check --status
unzip -p "$download_dir/protoc.zip" bin/protoc > "$tool_dir/protoc"
chmod 700 "$tool_dir/protoc"
GOBIN="$tool_dir" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOBIN="$tool_dir" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2

PATH="$tool_dir:$PATH" "$tool_dir/protoc" \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  internal/modules/system/grpcapi/proto/v1/node.proto \
  internal/modules/instances/grpcapi/proto/v1/instances.proto \
  internal/modules/engines/xray/grpcapi/proto/v1/xray.proto
