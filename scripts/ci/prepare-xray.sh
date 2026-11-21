#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
[ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || {
    printf '%s\n' 'Native Xray verification currently supports Linux amd64.' >&2; exit 1;
}
target="$root/.local/native-tests/xray"
expected=e87bce586acdb9136aa5b1f1d9c8db9f1ea1d145b6d86368c7f6f662f9f6a66d
if [ -e "$target" ]; then
    printf '%s  %s\n' "$expected" "$target" | sha256sum --check --status
else
    temporary=$(mktemp -d)
    trap 'rm -rf -- "$temporary"' EXIT HUP INT TERM
    curl --fail --location --silent --show-error --connect-timeout 10 --max-time 90 \
        https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-linux-64.zip \
        --output "$temporary/xray.zip"
    printf '%s  %s\n' b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82 "$temporary/xray.zip" |
        sha256sum --check --status
    unzip -p "$temporary/xray.zip" xray > "$temporary/xray"
    printf '%s  %s\n' "$expected" "$temporary/xray" | sha256sum --check --status
    chmod 755 "$temporary/xray"
    mkdir -p "$(dirname -- "$target")"
    mv "$temporary/xray" "$target"
fi
printf '%s\n' "$target"
