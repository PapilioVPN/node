#!/usr/bin/env bash
set -euo pipefail

image=${1:?Provide the built Node image}
work=$(mktemp -d)
container=
cleanup() {
  if [[ -n "$container" ]]; then
    docker rm -f "$container" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$work"
}
trap cleanup EXIT

cat > "$work/config.yml" <<'YAML'
management:
  http_address: "127.0.0.1:8080"
  grpc_address: "127.0.0.1:8081"
wireguard:
  interface: "wg-package"
  managed: true
  routing:
    egress_interface: "wg-egress"
    masquerade: true
    policy_table: 20101
YAML

container=$(docker run -d --network none --read-only --cap-drop ALL --cap-add NET_ADMIN \
  --user 0:0 --sysctl net.ipv4.ip_forward=1 \
  --mount "type=bind,source=$work/config.yml,target=/config.yml,readonly" "$image")
docker exec "$container" nft --version
docker exec "$container" wg --version
docker exec "$container" ip link add wg-egress type dummy
docker exec "$container" ip link set wg-egress up

request() {
  local method=$1 path=$2 body=${3:-}
  local sender receiver pid
  # Keep stdin open until the server closes the HTTP connection. In BusyBox
  # nc, closing stdin early half-closes TCP and can cancel the Go request.
  coproc node_http { docker exec -i "$container" timeout 5 nc 127.0.0.1 8080; }
  pid=${node_http_PID:-}
  [[ -n "$pid" ]] || return 1
  exec {sender}>&"${node_http[1]}" {receiver}<&"${node_http[0]}"
  printf '%s %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: %s\r\n\r\n%s' \
    "$method" "$path" "${#body}" "$body" >&"$sender"
  cat <&"$receiver"
  exec {sender}>&- {receiver}<&-
  wait "$pid"
}

fail() {
  printf '%s\n' "$@" >&2
  docker logs --tail 20 "$container" >&2
  exit 1
}

ready=false
for ((attempt = 0; attempt < 30; attempt++)); do
  if reply=$(request GET /healthz 2>/dev/null) && [[ "$reply" == 'HTTP/1.1 204 '* ]]; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  fail "WireGuard package did not become ready"
fi

execution=/v1/wireguard/interfaces/wg-package/execution
config='{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"]}'
reply=$(request PUT "$execution" "$config") || fail "WireGuard Start request failed" "$reply"
[[ "$reply" == 'HTTP/1.1 200 '* && "$reply" == *'"wireguard_tools_version":'* ]] || fail "Unexpected WireGuard Start reply" "$reply"
rules=$(docker exec "$container" nft list tables)
[[ "$rules" == *'table ip node_wg_'* ]] || fail "The package did not create the scoped nftables table" "$rules"
policy=$(docker exec "$container" ip rule show)
[[ "$policy" == *'wg-package'*'lookup 20101'* ]] || fail "The package did not create the scoped policy rule" "$policy"

reply=$(request DELETE "$execution") || fail "WireGuard Stop request failed" "$reply"
[[ "$reply" == 'HTTP/1.1 204 '* ]] || fail "Unexpected WireGuard Stop reply" "$reply"
rules=$(docker exec "$container" nft list tables)
[[ "$rules" != *'table ip node_wg_'* ]] || fail "WireGuard Stop left its nftables table" "$rules"
policy=$(docker exec "$container" ip rule show)
[[ "$policy" != *'lookup 20101'* ]] || fail "WireGuard Stop left its policy rule" "$policy"
docker stop --time 8 "$container" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
