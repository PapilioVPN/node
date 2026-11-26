#!/usr/bin/env bash
set -euo pipefail

image=${1:?Provide the built Node image}
work=$(mktemp -d)
container=
cleanup() {
  local status=$?
  if [[ -n "$container" ]]; then
    if (( status != 0 )); then
      docker logs --tail 20 "$container" >&2 || true
    fi
    docker rm -f "$container" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$work"
}
trap cleanup EXIT

cat > "$work/config.yml" <<'YAML'
management:
  http_address: "127.0.0.1:8080"
  grpc_address: "127.0.0.1:8081"
openvpn:
  id: package
  executable: /usr/sbin/openvpn
  config_file: server.conf
  runtime_directory: /tmp
YAML
cat > "$work/server.conf" <<'OVPN'
dev tun-package
disable-dco
local 127.0.0.1
proto udp4
port 1194
topology subnet
server 192.0.2.0 255.255.255.0
ca ca.crt
cert server.crt
key server.key
dh none
OVPN
(
  cd "$work"
  openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc -x509 \
    -days 1 -subj /CN=package-fixture-ca -keyout ca.key -out ca.crt
  openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc \
    -subj /CN=package-fixture-server -keyout server.key -out server.csr
  openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -days 1 -out server.crt
  rm ca.key server.csr
)
# Container root has only NET_ADMIN and cannot bypass host file modes.
# The disposable test key is used in an isolated network and removed on exit.
chmod 0711 "$work"
chmod 0644 "$work/server.key"

container=$(docker run -d --network none --read-only --cap-drop ALL --cap-add NET_ADMIN \
  --user 0:0 --device /dev/net/tun --tmpfs /tmp:mode=1777 \
  --mount "type=bind,source=$work,target=/fixture,readonly" \
  --entrypoint /node "$image" -config /fixture/config.yml)

for ((attempt = 0; attempt < 40; attempt++)); do
  if docker exec "$container" wget -qO /dev/null http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
docker exec "$container" wget -qO /dev/null http://127.0.0.1:8080/healthz
docker exec "$container" /usr/sbin/openvpn --version

request() {
  local method=$1 path=$2
  {
    printf '%s %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: 0\r\n\r\n' "$method" "$path"
    sleep 2
  } | docker exec -i "$container" timeout 10 nc 127.0.0.1 8080
}

base=/v1/openvpn/instances/package
reply=$(request PUT "$base/execution")
[[ "$reply" == 'HTTP/1.1 200 '* ]]
printf '%s\n' "$reply" | sed '1,/^\r$/d' | jq -e '.state == "ready" and (.openvpn_version | length > 0)'
docker exec "$container" ip link show dev tun-package
docker exec "$container" wget -qO- "http://127.0.0.1:8080$base/sessions" | jq -e '.sessions == [] and (.execution_id | length > 0)'
reply=$(request DELETE "$base/execution")
[[ "$reply" == 'HTTP/1.1 200 '* ]]
printf '%s\n' "$reply" | sed '1,/^\r$/d' | jq -e '.state == "stopped" and .execution_id == null'
if docker exec "$container" ip link show dev tun-package >/dev/null 2>&1; then
  printf '%s\n' "OpenVPN Stop left the package tunnel" >&2
  exit 1
fi
docker stop --time 8 "$container" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
