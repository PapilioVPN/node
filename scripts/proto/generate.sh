#!/bin/sh
set -eu

case "${1-}" in ''|--check) ;; *) printf '%s\n' 'Usage: generate.sh [--check]' >&2; exit 2 ;; esac
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
tools="$root/.local/proto-tools"
mkdir -p "$tools/bin"
if [ ! -x "$tools/bin/protoc" ]; then
    [ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || {
        printf '%s\n' 'Protobuf generation currently supports Linux amd64.' >&2; exit 1;
    }
    curl --fail --location --silent --show-error \
        https://github.com/protocolbuffers/protobuf/releases/download/v36.2/protoc-36.2-linux-x86_64.zip \
        --output "$tools/protoc.zip"
    printf '%s\n' '121f6c7afe1d4d0e3ea6aab9432038599250134cbf4474cb1167d2c7decd4278  protoc.zip' |
        (cd "$tools" && sha256sum --check -)
    unzip -oq "$tools/protoc.zip" -d "$tools"
fi
if [ ! -x "$tools/bin/protoc-gen-go" ]; then
    GOBIN="$tools/bin" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
fi
if [ ! -x "$tools/bin/protoc-gen-go-grpc" ]; then
    GOBIN="$tools/bin" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
fi
[ "$("$tools/bin/protoc" --version)" = 'libprotoc 36.2' ]
[ "$("$tools/bin/protoc-gen-go" --version)" = 'protoc-gen-go v1.36.12' ]
[ "$("$tools/bin/protoc-gen-go-grpc" --version)" = 'protoc-gen-go-grpc 1.6.2' ]
export PATH="$tools/bin:$PATH"
temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT HUP INT TERM
cd "$root"
for schema in \
    internal/apps/system/host/identity/grpcapi/proto/v1/identity \
    internal/apps/system/host/uptime/grpcapi/proto/v1/uptime \
    internal/apps/system/host/cpu/grpcapi/proto/v1/load \
    internal/apps/system/host/cpu/grpcapi/proto/v1/cpu \
    internal/apps/system/host/memory/grpcapi/proto/v1/memory; do
    protoc -I . -I "$tools/include" --go_out="$temporary" --go_opt=paths=source_relative \
        --go-grpc_out="$temporary" --go-grpc_opt=paths=source_relative "$schema.proto"
    for generated in "$schema.pb.go" "${schema}_grpc.pb.go"; do
        if [ "${1-}" = --check ]; then
            diff -u "$generated" "$temporary/$generated"
        else
            cp "$temporary/$generated" "$generated"
        fi
    done
done
