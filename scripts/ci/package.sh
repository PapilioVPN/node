#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
out=${1:-dist}
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
revision=$(git rev-parse HEAD)
image=
compose=()

cleanup() {
  if (( ${#compose[@]} )); then
    NODE_IMAGE="$image" "${compose[@]}" down --timeout 8 >/dev/null 2>&1 || true
  fi
  if [[ -n "$image" ]]; then
    docker image rm "$image" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$work"
}
trap cleanup EXIT

mkdir "$work/node"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$work/node/node" ./cmd/node
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$work/node/node-xray" ./cmd/node-xray
NODE_TEST_BUNDLED_XRAY="$work/node/node-xray" go test ./internal/engines/xray/stats/integration -run '^TestTrafficCountersWithBundledXray$' -count=1
version=$("$work/node/node" -version)
name="node-$version-linux-amd64"
image="papiliovpn/node:$version-${work##*.}"
cp deploy/config.example.yml "$work/node/config.example.yml"
cp deploy/compose.yml "$work/node/compose.yml"
printf 'NODE_IMAGE=ghcr.io/papiliovpn/node:%s\n' "$version" > "$work/node/.env"
cp LICENSE "$work/node/LICENSE"
docker build -q -f deploy/Dockerfile -t "$image" \
  --label org.opencontainers.image.source=https://github.com/PapilioVPN/node \
  --label org.opencontainers.image.revision="$revision" \
  --label org.opencontainers.image.version="$version" \
  --label org.opencontainers.image.licenses=GPL-3.0-only "$work/node"
docker run --rm --read-only --cap-drop ALL --entrypoint /node-xray "$image" version

mkdir -p "$work/smoke/config"
cp "$work/node/compose.yml" "$work/smoke/compose.yml"
cat > "$work/smoke/config/config.yml" <<'YAML'
management:
  http_address: "127.0.0.1:0"
  grpc_address: "127.0.0.1:0"
xray:
  id: primary
  executable: /node-xray
  config_file: xray.json
  runtime_directory: /tmp
YAML
cat > "$work/smoke/config/xray.json" <<'JSON'
{"log":{"loglevel":"none"},"outbounds":[{"tag":"direct","protocol":"freedom"}]}
JSON
project="node-ci-${work##*.}"
compose=(docker compose --project-name "${project,,}" --file "$work/smoke/compose.yml")
NODE_IMAGE="$image" "${compose[@]}" up -d --pull never
container=$(NODE_IMAGE="$image" "${compose[@]}" ps --all --quiet node)
ready=false
for ((attempt = 0; attempt < 50; attempt++)); do
  address=$(docker logs "$container" 2>&1 | jq -Rr 'fromjson? | select(.msg == "management listeners ready") | .http | "\(.IP):\(.Port)"' | tail -n 1)
  if [[ -n "$address" ]] && curl -fs --max-time 1 "http://$address/healthz" >/dev/null &&
    curl -fs --max-time 1 "http://$address/v1/host/cpu" >/dev/null; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  docker logs --tail 20 "$container" >&2
  exit 1
fi
curl -fs --max-time 1 "http://$address/v1/host/uptime" >/dev/null
curl -fs --max-time 1 "http://$address/v1/host/memory" >/dev/null
storage=$(curl -fs --max-time 1 "http://$address/v1/host/storage")
[[ "$storage" == *'"read_only":true'* ]]
summary=$(curl -fs --max-time 1 "http://$address/v1/host/stats")
[[ "$summary" == *'"cpu":{'* && "$summary" == *'"read_only":true'* ]]
execution="http://$address/v1/xray/instances/primary/execution"
curl -fsS --max-time 6 -X PUT "$execution" | jq -e --arg version "$version" '.state == "ready" and .node_version == $version'
curl -fsS --max-time 6 -X DELETE "$execution" | jq -e '.state == "stopped"'
docker stop --time 8 "$container" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0

bash scripts/ci/wireguard-smoke.sh "$image"
docker save -o "$work/node/image.tar" "$image"
printf '%s\n' "$image" > "$work/node/image-name"
archive="$name.tar.gz"
tar -czf "$out/$archive" -C "$work" node
compose_archive="$name-compose.tar.gz"
tar -czf "$out/$compose_archive" -C "$work" node/compose.yml node/.env node/config.example.yml node/LICENSE
(
  cd "$out"
  sha256sum "$archive" > "$archive.sha256"
  sha256sum "$compose_archive" > "$compose_archive.sha256"
  sha256sum -c "$archive.sha256" -c "$compose_archive.sha256"
)
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  printf 'name=%s\n' "$name" >> "$GITHUB_OUTPUT"
fi
