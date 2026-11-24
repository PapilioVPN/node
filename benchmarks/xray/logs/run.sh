#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 6 ]]; then
  echo "usage: $0 NODE PASARGUARD XRAY BENCHMARK REFERENCE_DESCRIPTOR OUTPUT_DIR" >&2
  exit 2
fi
if [[ ${XRAY_LOG_BENCH_ISOLATED:-} != 1 ]]; then
  exec unshare --user --map-root-user --net --pid --fork --mount-proc env XRAY_LOG_BENCH_ISOLATED=1 bash "$0" "$@"
fi

node=$(realpath -e "$1")
reference=$(realpath -e "$2")
xray=$(realpath -e "$3")
client=$(realpath -e "$4")
descriptor=$(realpath -e "$5")
mkdir -p "$6"
output=$(realpath "$6")
work=$(mktemp -d /tmp/node-logs-bench.XXXXXXXX)
node_pid=
reference_pid=
origin_pid=
stop() {
  if [[ -n "$1" ]]; then
    kill "$1" 2>/dev/null || true
    wait "$1" 2>/dev/null || true
  fi
}
cleanup() {
  stop "$reference_pid"
  stop "$node_pid"
  stop "$origin_pid"
  python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1])' "$work"
}
trap cleanup EXIT

wait_listen() {
  local port=$1
  for ((attempt = 0; attempt < 100; attempt++)); do
    if (: >"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
      return
    fi
    sleep 0.02
  done
  echo "Listener :$port did not start" >&2
  tail -n 15 "$work"/*.log >&2
  exit 1
}

ip link set lo up
export GOMAXPROCS=1 TZ=UTC
key=8bc4e708-8b28-4705-8f3e-0bf78145e229
mkdir "$work/run" "$work/generated"
openssl req -x509 -newkey ed25519 -days 1 -nodes -subj '/CN=benchmark' \
  -addext 'subjectAltName=IP:127.0.0.1' -keyout "$work/key.pem" -out "$work/cert.pem" 2>/dev/null
cat > "$work/xray.json" <<'JSON'
{
  "log": {"loglevel":"warning"},
  "inbounds": [{"tag":"proxy","listen":"127.0.0.1","port":24440,"protocol":"http"}],
  "outbounds": [{"tag":"direct","protocol":"freedom","settings":{"redirect":"127.0.0.1:24442"}}]
}
JSON
ln -s "$xray" "$work/xray"
cat > "$work/node.yml" <<YAML
management:
  http_address: '127.0.0.1:20881'
  grpc_address: '127.0.0.1:20882'
  api_key: '$key'
  tls:
    cert_file: 'cert.pem'
    key_file: 'key.pem'
xray:
  id: 'primary'
  executable: 'xray'
  config_file: 'xray.json'
  runtime_directory: 'run'
YAML

server_cpu=${XRAY_LOG_BENCH_SERVER_CPU:-6}
client_cpu=${XRAY_LOG_BENCH_CLIENT_CPU:-7}
taskset -c "$client_cpu" python3 -u -c '
import socket
with socket.create_server(("127.0.0.1", 24442)) as listener:
    while True:
        connection, _ = listener.accept()
        connection.close()
' >"$work/origin.log" 2>&1 &
origin_pid=$!
wait_listen 24442

taskset -c "$server_cpu" "$node" -config "$work/node.yml" >"$work/node.log" 2>&1 &
node_pid=$!
IFS=, read -r -a protocols <<< "${XRAY_LOG_BENCH_TRANSPORTS:-http,grpc}"
for protocol in "${protocols[@]}"; do
  if [[ $protocol != http && $protocol != grpc ]]; then
    echo "Unknown benchmark transport: $protocol" >&2
    exit 2
  fi
  service_protocol=$protocol
  node_port=20882
  if [[ $protocol == http ]]; then
    service_protocol=rest
    node_port=20881
  fi
  env DEBUG=false LOG_BUFFER_SIZE=10000 NODE_HOST=127.0.0.1 SERVICE_PORT=20883 SERVICE_PROTOCOL="$service_protocol" API_KEY="$key" \
    SSL_CERT_FILE="$work/cert.pem" SSL_KEY_FILE="$work/key.pem" \
    XRAY_EXECUTABLE_PATH="$work/xray" XRAY_ASSETS_PATH="$(dirname "$xray")" GENERATED_CONFIG_PATH="$work/generated" \
    taskset -c "$server_cpu" "$reference" >"$work/reference.log" 2>&1 &
  reference_pid=$!
  wait_listen "$node_port"
  wait_listen 20883
  if ! taskset -c "$client_cpu" "$client" -transport "$protocol" \
    -node "127.0.0.1:$node_port" -reference 127.0.0.1:20883 -cert "$work/cert.pem" \
    -reference-proto "$descriptor" -config "$work/xray.json" -rounds "${XRAY_LOG_BENCH_ROUNDS:-5}" \
    -logs-proxy 127.0.0.1:24440 -logs-samples "$output/$protocol-samples.csv.gz" > "$output/$protocol.csv"; then
    tail -n 15 "$work/node.log" "$work/reference.log" >&2
    exit 1
  fi
  stop "$reference_pid"
  reference_pid=
done
