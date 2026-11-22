#!/bin/sh
set -eu
export LC_ALL=C

if [ "$#" -ne 3 ]; then
    printf '%s\n' 'Usage: package.sh PREBUILT_BINARY FULL_REVISION OUTPUT_DIRECTORY' >&2
    exit 2
fi
binary=$(realpath -- "$1")
revision=$2
case "$revision" in *[!0-9a-f]*) exit 2 ;; esac
[ "${#revision}" -eq 40 ]
mkdir -p -- "$3"
output=$(realpath -- "$3")
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT HUP INT TERM
mkdir -p "$stage/context" "$stage/node/scripts/install" "$stage/node/scripts/run" "$stage/node/deploy"
mkdir -p "$stage/node/assets/flags"
install -m 0555 "$binary" "$stage/context/node"
image="papilio-node-build:$revision"
docker build --network=none --platform=linux/amd64 --build-arg "REVISION=$revision" \
    --file "$root/deploy/Dockerfile" --tag "$image" "$stage/context"
printf '%s\n' "$image" > "$stage/node/image.ref"
docker image save --output "$stage/node/image.tar" "$image"
printf '%s\n' "$revision" > "$stage/node/revision"
install -m 0755 "$root/install.sh" "$root/run.sh" "$stage/node/"
install -m 0755 "$root/scripts/install/install.sh" "$stage/node/scripts/install/install.sh"
install -m 0755 "$root/scripts/run/run.sh" "$stage/node/scripts/run/run.sh"
install -m 0644 "$root/deploy/config.example.yml" "$stage/node/deploy/config.example.yml"
install -m 0644 "$root/README.md" "$root/README.fa.md" "$root/README.ru.md" "$root/README.zh-CN.md" "$stage/node/"
install -m 0644 "$root"/assets/banner*.png "$stage/node/assets/"
install -m 0644 "$root"/assets/flags/*.svg "$root/assets/flags/NOTICE.md" "$stage/node/assets/flags/"
(
    cd "$stage/node"
    sha256sum image.tar image.ref revision install.sh run.sh scripts/install/install.sh \
        scripts/run/run.sh deploy/config.example.yml README*.md assets/banner*.png \
        assets/flags/*.svg assets/flags/NOTICE.md > checksums.sha256
)
archive="node-linux-amd64-$revision.tar.gz"
tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf "$stage/bundle.tar" -C "$stage" node
gzip -n -c "$stage/bundle.tar" > "$stage/$archive"
install -m 0644 "$stage/$archive" "$output/$archive"
(cd "$output" && sha256sum "$archive" > "$archive.sha256")
printf '%s\n' "Runtime bundle: $output/$archive"
