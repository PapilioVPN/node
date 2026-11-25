import importlib.util
import json
import socket
import subprocess
import sys
import time
from pathlib import Path

fixture_path = Path(__file__).resolve().parents[1] / "wireguard/interfaces/measure-lifecycle.py"
spec = importlib.util.spec_from_file_location("fixture", fixture_path)
f = importlib.util.module_from_spec(spec)
spec.loader.exec_module(f)


def measure_system_stats(cases):
    endpoints = {"node": "/v1/host/stats", "node-before": "/v1/host/stats", "pasarguard": "/stats/system"}
    subprocess.run(["ip", "link", "add", "cov-left", "type", "veth", "peer", "name", "cov-right"], check=True)
    for name in ("cov-left", "cov-right"):
        subprocess.run(["ip", "link", "set", name, "up"], check=True)
    with socket.socket(socket.AF_PACKET, socket.SOCK_RAW) as b:
        b.bind(("cov-left", 0))
        resources = {}
        for label, (conn, method, path, source, end_method, end_path, valid) in cases:
            if label == "pasarguard":
                _, data = f.request(conn, method, path, source)
                assert valid(data)
            resources[label] = (conn, end_method, end_path)
        time.sleep(1.10)
        for _ in range(30):
            b.send(b"\xff"*6 + b"\x02" + b"\x00"*5 + b"\x88\xb5" + b"\x00"*1200)
        for label, (conn, _, _) in resources.items():
            _, body = f.request(conn, "GET", endpoints[label])
            if label == "node":
                # A saved interval with the veth frames can arrive at any point in
                # the next second. Verify both directions before measuring.
                for _ in range(20):
                    _, body = f.request(conn, "GET", endpoints[label])
                    values = json.loads(body)
                    net = values.get("network", {})
                    if net.get("rx_bytes_per_second", 0) > 0 and net.get("tx_bytes_per_second", 0) > 0:
                        break
                    time.sleep(.1)
                else:
                    raise RuntimeError(f"no non-loopback network rates: {values}")
                assert values["logical_cpu_count"] > 0 and values["memory"]["used_bytes"] > 0, values
            elif label == "pasarguard":
                assert body.startswith(b"\x08") and len(body) > 10, body
        for trial in range(8):
            for label, (conn, _, _) in resources.items() if trial % 2 == 0 else reversed(resources.items()):
                for i in range(200):
                    ns, _ = f.request(conn, "GET", endpoints[label])
                    print(f"{trial + 1},{label},system_stats,{i + 1},{ns}")
        if "pasarguard" in resources:
            f.request(*resources["pasarguard"])


if __name__ == "__main__":
    if len(sys.argv) not in (3, 4):
        raise SystemExit("usage: measure-system-stats.py NODE PASARGUARD [PREVIOUS_NODE]")
    sys.argv.insert(3, "--lifecycle-check")
    f.main(workload=measure_system_stats)
