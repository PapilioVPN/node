#!/usr/bin/env bash
set -euo pipefail

installer=$1
archive=$2
fixture=$(mktemp -d)
destination=$fixture/node
project_hash=$(printf '%s' "$destination" | sha256sum)
project=papiliovpn-node-${project_hash:0:12}

# shellcheck disable=SC2317
cleanup() {
  local -a ids=()
  mapfile -t ids < <(docker ps --all --quiet --filter "label=com.docker.compose.project=$project")
  if (( ${#ids[@]} )); then docker rm -f "${ids[@]}" >/dev/null; fi
  rm -rf -- "$fixture"
}
trap cleanup EXIT

tar -xOzf "$archive" node/install.sh | cmp "$installer" -
bash -n "$installer"

# Two local ports let the installer run alongside other Docker projects.
read -r http_port grpc_port < <(python3 - <<'PY'
import socket
with socket.socket() as http, socket.socket() as grpc:
    for listener in http, grpc:
        listener.bind(("127.0.0.1", 0))
    print(http.getsockname()[1], grpc.getsockname()[1])
PY
)
health=http://127.0.0.1:$http_port/healthz
mkdir -p "$destination/config"
cat > "$destination/config/config.yml" <<YAML
# Existing operator configuration
management:
  http_address: "127.0.0.1:$http_port"
  grpc_address: "127.0.0.1:$grpc_port"
YAML
chmod 0444 "$destination/config/config.yml"
cp -p "$destination/config/config.yml" "$fixture/original.yml"

args=(--yes --directory "$destination" --health-url "$health")
run_install() {
  bash "$installer" "${args[@]}" --archive "$1"
}
container_id() {
  docker ps --all --quiet --filter "label=com.docker.compose.project=$project"
}
wait_for_health() {
  local attempt
  for ((attempt=0; attempt<20; attempt++)); do
    if curl -fsS --max-time 1 "$health" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  docker logs --tail 15 "$(container_id)" >&2
  return 1
}

run_install "$archive"
first=$(container_id)
test -n "$first"
test "$(docker inspect --format '{{.HostConfig.RestartPolicy.Name}}' "$first")" = unless-stopped
before=$(< "$destination/current")
bash "$installer" "${args[@]}" > "$fixture/fast.log"
grep -F 'No download needed' "$fixture/fast.log"
test "$(container_id)" = "$first"
run_install "$archive"
test "$(container_id)" = "$first"
test "$(< "$destination/current")" = "$before"
cmp "$fixture/original.yml" "$destination/config/config.yml"
test "$(stat -c '%a' "$destination/config/config.yml")" = 444
test "$(stat -c '%a' "$destination/install.log")" = 600
grep -F 'Management API is healthy' "$destination/install.log"
grep -Fx 'Exit status: 0' "$destination/install.log"

mkdir "$fixture/rejected"
rejected=$fixture/rejected/${archive##*/}
ln -s "$(realpath "$archive")" "$rejected"
printf '%064d  %s\n' 0 "${archive##*/}" > "$rejected.sha256"
if run_install "$rejected" > "$fixture/rejected.log" 2>&1; then
  printf 'Installer accepted an invalid checksum.\n' >&2
  exit 1
fi
grep -F 'SHA-256 mismatch' "$fixture/rejected.log"
grep -Fx 'Exit status: 1' "$destination/install.log"
test "$(container_id)" = "$first"

docker restart "$first" >/dev/null
wait_for_health

mkdir "$fixture/broken"
tar -xzf "$archive" -C "$fixture/broken" node/compose.yml node/config.example.yml node/image-name node/image.tar
broken="$fixture/broken/${archive##*/}"
sed -i 's@^    entrypoint:.*@    entrypoint: ["/bin/false"]@' "$fixture/broken/node/compose.yml"
tar -czf "$broken" -C "$fixture/broken" node
(cd "$fixture/broken" && sha256sum "${broken##*/}" > "${broken##*/}.sha256")
if run_install "$broken" > "$fixture/broken.log" 2>&1; then
  printf 'Installer accepted a deployment that failed to start.\n' >&2
  exit 1
fi
grep -F 'Restoring the previous container configuration' "$fixture/broken.log"
test "$(< "$destination/current")" = "$before"
cmp "$fixture/original.yml" "$destination/config/config.yml"
wait_for_health
printf 'Installer verified a healthy deployment, fast rerun, preserved config, invalid checksum, restart, and rollback.\n'
