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
reference_xray=$(realpath "${XRAY_BENCH_REFERENCE_XRAY:-$xray}")
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
if [[ ${XRAY_BENCH_VLESS_ACCOUNTS:-0} == 1 ]]; then
  cat > "$work/xray.json" <<'JSON'
{
  "log":{"loglevel":"none"},
  "inbounds":[{"tag":"vless","listen":"127.0.0.1","port":24440,"protocol":"vless",
    "settings":{"clients":[],"decryption":"none"}}],
  "outbounds":[{"tag":"direct","protocol":"freedom"}]
}
JSON
elif [[ ${XRAY_BENCH_ROUTING_RULES:-0} != 0 ]]; then
  python3 - "$XRAY_BENCH_ROUTING_RULES" "$work" <<'PYTHON'
import json
from pathlib import Path
import sys

total = int(sys.argv[1])
if not 2 <= total <= 10000:
    raise SystemExit("expected 2 to 10000 active routing rules")
rules = []
for i in range(1, total):
    rule = {"type": "field", "port": str(2000 + i)}
    if i % 4 == 1:
        rule["balancerTag"] = "pool"
    else:
        rule.update(ruleTag=f"rule-{total-i:04}", outboundTag="direct")
    rules.append(rule)
config = {
    "log": {"loglevel": "none"},
    "outbounds": [{"protocol": "freedom", "tag": tag} for tag in ("direct", "pool-a", "pool-b")],
    "routing": {
        "balancers": [{"tag": "pool", "selector": ["pool-"], "strategy": {"type": "roundRobin"}}],
        "rules": rules,
    },
}
directory = Path(sys.argv[2])
(directory / "reference-xray.json").write_text(json.dumps(config))
# The reference inserts this list row for its management inbound. Give Node's
# otherwise identical fixture the same row so all returned pairs are equal.
rules.insert(0, {"type": "field", "inboundTag": ["API_INBOUND"], "outboundTag": "API"})
(directory / "xray.json").write_text(json.dumps(config))
PYTHON
elif [[ ${XRAY_BENCH_OUTBOUND_HEALTH:-0} == 1 ]]; then
  python3 - "$work/xray.json" <<'PYTHON'
import json
import sys

outbounds = [{"tag": f"sample-{i:02}", "protocol": "freedom"} for i in range(16)]
outbounds[-1]["settings"] = {"redirect": "127.0.0.1:24443"}
outbounds.append({"tag": "unobserved", "protocol": "freedom"})
with open(sys.argv[1], "w") as output:
    json.dump({
        "log": {"loglevel": "none"},
        "observatory": {
            "subjectSelector": ["sample-"],
            "probeURL": "http://127.0.0.1:24442/health",
            "probeInterval": "1h",
            "enableConcurrency": True,
        },
        "outbounds": outbounds,
    }, output)
PYTHON
elif [[ ${XRAY_BENCH_TRAFFIC_NAMES:-0} != 0 ]]; then
  python3 - "${XRAY_BENCH_TRAFFIC_NAMES}" "$work" "${XRAY_BENCH_TRAFFIC_CASE:-}" <<'PYTHON'
import json
from pathlib import Path
import sys

names = int(sys.argv[1])
if not 1 <= names <= 10000:
    raise SystemExit("expected 1 to 10000 traffic accounts")
config = {
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
}
if sys.argv[3] == "online" or sys.argv[3].startswith("online-"):
    config["policy"]["levels"]["0"]["statsUserOnline"] = True
directory = Path(sys.argv[2])
(directory / "xray.json").write_text(json.dumps(config))
config["inbounds"][0]["port"] = 24441
(directory / "reference-xray.json").write_text(json.dumps(config))
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
if [[ ${XRAY_BENCH_TRAFFIC_CASE:-} == reset-pair/one || ${XRAY_BENCH_TRAFFIC_CASE:-} == reset-scope/list ]]; then
  printf '  reset_engine_stats: true\n' >> "$work/node.yml"
  if [[ ${XRAY_BENCH_NATIVE_SCOPE_RESET:-0} == 1 ]]; then
    printf '  native_scope_reset: true\n' >> "$work/node.yml"
  fi
fi
if [[ ${XRAY_BENCH_TRAFFIC_CASE:-} == reset-scope/list ]]; then
  printf '  reset_scope_concurrency: %d\n' "${XRAY_BENCH_SCOPE_RESET_CONCURRENCY:-1}" >> "$work/node.yml"
fi

export GOMAXPROCS=1
benchmark_args=()
if [[ ${XRAY_BENCH_VLESS_ACCOUNTS:-0} == 1 ]]; then
  benchmark_args+=(-account-runtime-dir "$work/run" -account-reference-generated "$work/generated/xray.json"
    -account-mutations "${XRAY_BENCH_ACCOUNT_MUTATIONS:-256}"
    -runtime-stats-clients "${XRAY_BENCH_ACCOUNT_COUNT_CLIENTS:-1,8}"
    -runtime-stats-window "${XRAY_BENCH_ACCOUNT_COUNT_WINDOW:-550ms}")
  if [[ -n ${XRAY_BENCH_ACCOUNT_SYNC:-} ]]; then
    benchmark_args+=(-account-sync "$XRAY_BENCH_ACCOUNT_SYNC")
  fi
elif [[ ${XRAY_BENCH_ROUTING_RULES:-0} != 0 ]]; then
  benchmark_args+=(-routing-rules "$XRAY_BENCH_ROUTING_RULES" -routing-reference-config "$work/reference-xray.json"
    -routing-runtime-dir "$work/run" -routing-operation "${XRAY_BENCH_ROUTING_OPERATION:-}"
    -runtime-stats-clients "${XRAY_BENCH_ROUTING_CLIENTS:-1,8}"
    -runtime-stats-window "${XRAY_BENCH_ROUTING_WINDOW:-450ms}")
elif [[ ${XRAY_BENCH_OUTBOUND_HEALTH:-0} == 1 ]]; then
  benchmark_args+=(-outbound-health -outbound-health-case "${XRAY_BENCH_OUTBOUND_CASE:-}"
    -runtime-stats-clients "${XRAY_BENCH_OUTBOUND_CLIENTS:-1,8}"
    -runtime-stats-window "${XRAY_BENCH_OUTBOUND_WINDOW:-550ms}")
elif [[ ${XRAY_BENCH_TRAFFIC_NAMES:-0} != 0 ]]; then
  benchmark_args+=(-traffic-names "$XRAY_BENCH_TRAFFIC_NAMES" -traffic-reference-config "$work/reference-xray.json"
    -traffic-case "${XRAY_BENCH_TRAFFIC_CASE:-}"
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
  reference_env=()
  if [[ ${XRAY_BENCH_VLESS_ACCOUNTS:-0} == 1 ]]; then
    reference_env+=(DEBUG=true)
  fi
  env "${reference_env[@]}" NODE_HOST=127.0.0.1 SERVICE_PORT=20883 SERVICE_PROTOCOL=$service_protocol API_KEY=$key \
    SSL_CERT_FILE="$work/cert.pem" SSL_KEY_FILE="$work/key.pem" \
    XRAY_EXECUTABLE_PATH="$reference_xray" XRAY_ASSETS_PATH="$(dirname "$reference_xray")" GENERATED_CONFIG_PATH="$work/generated" \
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
  samples=()
  if [[ ${XRAY_BENCH_VLESS_ACCOUNTS:-0} == 1 ]]; then
    if [[ -n ${XRAY_BENCH_ACCOUNT_SYNC:-} ]]; then
      samples+=(-account-samples "$output/$protocol-samples.csv")
    else
      samples+=(-account-samples "$output/$protocol-samples.csv.gz")
    fi
  elif [[ ${XRAY_BENCH_ROUTING_RULES:-0} != 0 ]]; then
    samples+=(-routing-samples "$output/$protocol-samples.csv")
  elif [[ ${XRAY_BENCH_OUTBOUND_HEALTH:-0} == 1 ]]; then
    samples+=(-outbound-health-samples "$output/$protocol-samples.csv.gz")
  fi
  if ! taskset -c "${XRAY_BENCH_CLIENT_CPU:-4}" "$client" "${benchmark_args[@]}" "${samples[@]}" -transport "$protocol" \
    -node "127.0.0.1:$node_port" -node-pid "$node_pid" -reference 127.0.0.1:20883 \
    -reference-pid "$reference_pid" -cert "$work/cert.pem" -reference-proto "$descriptor" \
    -config "$work/xray.json" -rounds "${XRAY_BENCH_ROUNDS:-5}" -cycles "${XRAY_BENCH_CYCLES:-30}" \
    -reads "${XRAY_BENCH_READS:-12}" > "$output/$protocol.csv"; then
    if [[ -z ${XRAY_BENCH_ACCOUNT_SYNC:-} ]]; then
      tail -n 30 "$work/node.log" "$work/reference.log" >&2
    fi
    exit 1
  fi
  kill "$reference_pid"
  wait "$reference_pid"
  reference_pid=
done
