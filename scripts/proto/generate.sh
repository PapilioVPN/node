#!/bin/sh
set -eu

mode=${1-}
case "$mode" in ''|--check) ;; *) printf '%s\n' 'Usage: generate.sh [--check]' >&2; exit 2 ;; esac
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
# Discover versioned, capability-owned schemas and generate them in one pass.
find internal/apps -type f -path '*/grpcapi/proto/v*/*.proto' > "$temporary/schemas"
LC_ALL=C sort -o "$temporary/schemas" "$temporary/schemas"
set --
while IFS= read -r schema; do
    set -- "$@" "$schema"
done < "$temporary/schemas"
if [ "$#" -eq 0 ]; then
    printf '%s\n' 'No capability gRPC schemas found.' >&2
    exit 1
fi
protoc -I . -I "$tools/include" --go_out="$temporary" --go_opt=paths=source_relative \
    --go-grpc_out="$temporary" --go-grpc_opt=paths=source_relative "$@"

find internal/apps -type f -path '*/grpcapi/proto/v*/*.pb.go' > "$temporary/existing"
while IFS= read -r generated; do
    if [ ! -f "$temporary/$generated" ]; then
        printf '%s\n' "Unexpected generated file: $generated" >&2
        exit 1
    fi
done < "$temporary/existing"

find "$temporary/internal/apps" -type f -name '*.pb.go' > "$temporary/generated"
LC_ALL=C sort -o "$temporary/generated" "$temporary/generated"
while IFS= read -r generated; do
    destination=${generated#"$temporary/"}
    if [ "$mode" = --check ]; then
        diff -u "$destination" "$generated"
    else
        cp "$generated" "$destination"
    fi
done < "$temporary/generated"
