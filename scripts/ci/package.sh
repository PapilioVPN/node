#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
out=${1:-dist}
mkdir -p "$out"
out=$(cd "$out" && pwd)
work=$(mktemp -d)
revision=$(git rev-parse HEAD)
image="papiliovpn/node:check-${work##*.}"
container=

cleanup() {
  if [[ -n "$container" ]]; then
    docker rm -f "$container" >/dev/null 2>&1 || true
  fi
  docker image rm "$image" >/dev/null 2>&1 || true
  rm -rf -- "$work"
}
trap cleanup EXIT

mkdir "$work/node"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$work/node/node" ./cmd/node
cp deploy/config.example.yml "$work/node/config.example.yml"
docker build -q -f deploy/Dockerfile -t "$image" "$work/node"

cat > "$work/smoke.yml" <<'YAML'
management:
  http_address: "0.0.0.0:8080"
  grpc_address: "0.0.0.0:8081"
YAML
chmod 0644 "$work/smoke.yml"
container=$(docker run -d --read-only --cap-drop ALL \
  --publish 127.0.0.1::8080 \
  --mount "type=bind,source=$work/smoke.yml,target=/config.yml,readonly" "$image")
address=$(docker port "$container" 8080/tcp)
ready=false
for ((attempt = 0; attempt < 50; attempt++)); do
  if curl -fs --max-time 1 "http://$address/healthz" >/dev/null; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  docker logs "$container" >&2
  exit 1
fi
docker stop --time 8 "$container" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0

docker save -o "$work/node/image.tar" "$image"
printf '%s\n' "$image" > "$work/node/image-name"
archive="node-linux-amd64-$revision.tar.gz"
tar -czf "$out/$archive" -C "$work" node
(cd "$out" && sha256sum "$archive" > "$archive.sha256" && sha256sum -c "$archive.sha256")
