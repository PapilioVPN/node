#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 6 ]]; then
  echo "usage: $0 NODE PASARGUARD XRAY BENCHMARK REFERENCE_DESCRIPTOR OUTPUT_DIR" >&2
  exit 2
fi
if [[ ${XRAY_BENCH_ISOLATED:-} != 1 ]]; then
  exec unshare --user --map-root-user --net --pid --fork --mount-proc env XRAY_BENCH_ISOLATED=1 bash "$0" "$@"
fi

node=$(realpath "$1")
reference=$(realpath "$2")
xray=$(realpath "$3")
client=$(realpath "$4")
descriptor=$(realpath "$5")
mkdir -p "$6"
output=$(realpath "$6")
work=$(mktemp -d /tmp/node-bench.XXXXXXXX)
node_pid=
reference_pid=
cleanup() {
  local pid
  for pid in "$reference_pid" "$node_pid"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1])' "$work"
}
trap cleanup EXIT

ip link set lo up
key=8bc4e708-8b28-4705-8f3e-0bf78145e229
cp "$xray" "$work/xray"
mkdir "$work/run" "$work/generated"
openssl req -x509 -newkey ed25519 -days 1 -nodes -subj '/CN=benchmark' \
  -addext 'subjectAltName=IP:127.0.0.1' -keyout "$work/key.pem" -out "$work/cert.pem" 2>/dev/null
cat > "$work/xray.json" <<'JSON'
{
  "log": {"loglevel":"none"},
  "inbounds": [{"tag":"fixture","listen":"127.0.0.1","port":24440,"protocol":"socks","settings":{"auth":"noauth"}}],
  "outbounds": [{"tag":"direct","protocol":"freedom"}]
}
JSON
if [[ ${XRAY_BENCH_TRAFFIC_NAMES:-0} != 0 ]]; then
  python3 - "${XRAY_BENCH_TRAFFIC_NAMES}" > "$work/xray.json" <<'PYTHON'
import json
import sys

names = int(sys.argv[1])
if not 1 <= names <= 10000:
    raise SystemExit("expected 1 to 10000 traffic accounts")
json.dump({
    "log": {"loglevel": "none"},
    "stats": {},
    "policy": {
        "levels": {"0": {"statsUserUplink": True, "statsUserDownlink": True}},
        "system": {
            "statsInboundUplink": True, "statsInboundDownlink": True,
            "statsOutboundUplink": True, "statsOutboundDownlink": True,
        },
    },
    "inbounds": [{
        "tag": "fixture", "listen": "127.0.0.1", "port": 24440, "protocol": "http",
        "settings": {"accounts": [{"user": f"account-{i:04}", "pass": "fixture"} for i in range(names)]},
    }],
    "outbounds": [{"tag": "direct", "protocol": "freedom"}],
}, sys.stdout)
PYTHON
fi
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

export GOMAXPROCS=1
benchmark_args=()
if [[ ${XRAY_BENCH_TRAFFIC_NAMES:-0} != 0 ]]; then
  benchmark_args+=(-traffic-names "$XRAY_BENCH_TRAFFIC_NAMES" -traffic-case "${XRAY_BENCH_TRAFFIC_CASE:-}"
    -runtime-stats-clients "${XRAY_BENCH_TRAFFIC_CLIENTS:-1,8}"
    -runtime-stats-window "${XRAY_BENCH_TRAFFIC_WINDOW:-300ms}")
elif [[ ${XRAY_BENCH_RUNTIME_STATS:-0} == 1 ]]; then
  benchmark_args+=(-runtime-stats
    -runtime-stats-clients "${XRAY_BENCH_RUNTIME_STATS_CLIENTS:-1,8}"
    -runtime-stats-window "${XRAY_BENCH_RUNTIME_STATS_WINDOW:-450ms}")
fi
taskset -c "${XRAY_BENCH_SERVER_CPU:-2}" "$node" -config "$work/node.yml" >"$work/node.log" 2>&1 &
node_pid=$!
IFS=, read -r -a protocols <<< "${XRAY_BENCH_TRANSPORTS:-http,grpc}"
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
  NODE_HOST=127.0.0.1 SERVICE_PORT=20883 SERVICE_PROTOCOL=$service_protocol API_KEY=$key \
    SSL_CERT_FILE="$work/cert.pem" SSL_KEY_FILE="$work/key.pem" \
    XRAY_EXECUTABLE_PATH="$work/xray" XRAY_ASSETS_PATH="$(dirname "$xray")" GENERATED_CONFIG_PATH="$work/generated" \
    taskset -c "${XRAY_BENCH_SERVER_CPU:-2}" "$reference" >"$work/reference.log" 2>&1 &
  reference_pid=$!
  for port in "$node_port" 20883; do
    ready=false
    for ((attempt = 0; attempt < 100; attempt++)); do
      if (: >"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then ready=true; break; fi
      sleep 0.02
    done
    if [[ $ready != true ]]; then
      cat "$work/node.log" "$work/reference.log" >&2
      exit 1
    fi
  done
  if ! taskset -c "${XRAY_BENCH_CLIENT_CPU:-4}" "$client" "${benchmark_args[@]}" -transport "$protocol" \
    -node "127.0.0.1:$node_port" -node-pid "$node_pid" -reference 127.0.0.1:20883 \
    -reference-pid "$reference_pid" -cert "$work/cert.pem" -reference-proto "$descriptor" \
    -config "$work/xray.json" -rounds "${XRAY_BENCH_ROUNDS:-5}" -cycles "${XRAY_BENCH_CYCLES:-30}" \
    -reads "${XRAY_BENCH_READS:-12}" > "$output/$protocol.csv"; then
    tail -n 30 "$work/node.log" "$work/reference.log" >&2
    exit 1
  fi
  kill "$reference_pid"
  wait "$reference_pid"
  reference_pid=
done
