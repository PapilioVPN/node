#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'Native Xray integration currently requires Linux amd64.' >&2
  exit 1
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl --fail --silent --show-error --location --max-time 120 \
  https://github.com/XTLS/Xray-core/releases/download/v26.11.9/Xray-linux-64.zip \
  --output "$work/xray.zip"
printf '%s  %s\n' \
  b651f7e1d9966ee2c1b1ee1bf8b52c72771824d3065dce96bbc8bcc19ed95f82 \
  "$work/xray.zip" | sha256sum --check --status
unzip -p "$work/xray.zip" xray > "$work/xray"
chmod 700 "$work/xray"
# Retain this verified test executable for the later packaged-runtime smoke.
# It remains a local fixture and is never copied into the runtime image/archive.
mkdir -p .local/native-tests
install -m 700 "$work/xray" .local/native-tests/xray

# Broker is a test-only executable; it is not linked into or shipped with Node.
GOBIN="$work" go install github.com/nats-io/nats-server/v2@v2.15.3
NATS_TEST_BINARY="$work/nats-server" XRAY_TEST_BINARY="$work/xray" go test -race -tags=integration -count=1 -timeout=90s ./...
