import base64
import hashlib
import http.client
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import time
from contextlib import ExitStack
from pathlib import Path

peer_interface = "wg-bench0"
api_key = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
headers = {"X-API-Key": api_key}
private_key = base64.b64encode(hashlib.sha256(b"isolated fixture").digest()).decode()


def request(conn, method, path, data=None):
    start = time.perf_counter_ns()
    conn.request(method, path, data, headers)
    reply = conn.getresponse()
    payload = reply.read()
    took = time.perf_counter_ns() - start
    if reply.status not in (200, 204):
        raise RuntimeError(f"{method} {path} returned {reply.status}: {payload[:100]!r}")
    return took, payload


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def varint(value):
    result = bytearray()
    while value >= 128:
        result.append((value & 127) | 128)
        value >>= 7
    result.append(value)
    return result


def launch(stack, argv, env, label, directory, context, listen, readiness):
    log = stack.enter_context((directory / f"{label}.log").open("w"))
    p = subprocess.Popen(argv, env=env, cwd=directory, stdout=log, stderr=log)
    def close():
        if p.poll() is None:
            p.terminate()
            try:
                p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
    stack.callback(close)
    conn = http.client.HTTPSConnection("127.0.0.1", listen, timeout=3, context=context)
    stack.callback(conn.close)
    last = None
    for _ in range(100):
        try:
            request(conn, "GET", readiness)
            return conn
        except (OSError, http.client.HTTPException) as exc:
            last = exc
            conn.close()
            if p.poll() is not None:
                break
            time.sleep(.02)
    raise RuntimeError(f"{label} failed to start ({last}): {(directory / f'{label}.log').read_text()[-1500:]}")


def sample(conn, start_method, start_path, source, stop_method, stop_path, valid):
    start_ns, payload = request(conn, start_method, start_path, source)
    assert valid(payload)
    index = socket.if_nametoindex(peer_interface)
    same_ns, payload = request(conn, start_method, start_path, source)
    assert valid(payload) and socket.if_nametoindex(peer_interface) == index
    stop_ns, _ = request(conn, stop_method, stop_path)
    try:
        socket.if_nametoindex(peer_interface)
    except OSError:
        return start_ns, same_ns, stop_ns
    raise RuntimeError("Stop left an interface")


def main():
    parent = os.environ.get("NODE_TEST_WIREGUARD_PARENT_NETNS")
    if not parent or parent == os.readlink("/proc/self/ns/net"):
        raise RuntimeError("run in a separate network namespace")
    subprocess.run(["ip", "link", "set", "lo", "up"], check=True)
    with ExitStack() as stack:
        directory = Path(stack.enter_context(tempfile.TemporaryDirectory()))
        cert, key = directory / "cert.pem", directory / "key.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt",
                        "ec_paramgen_curve:P-256", "-nodes", "-subj", "/CN=127.0.0.1",
                        "-addext", "subjectAltName=IP:127.0.0.1", "-days", "1",
                        "-keyout", str(key), "-out", str(cert)], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        context = ssl.create_default_context(cafile=str(cert))
        context.minimum_version = ssl.TLSVersion.TLSv1_3
        node_port, ref_port, grpc_port = port(), port(), port()
        cfg = directory / "config.yml"
        cfg.write_text(f"""management:
  http_address: "127.0.0.1:{node_port}"
  grpc_address: "127.0.0.1:{grpc_port}"
  api_key: "{api_key}"
  tls:
    cert_file: "{cert}"
    key_file: "{key}"
wireguard:
  interface: "{peer_interface}"
  managed: true
""")
        env = {**os.environ, "GOMAXPROCS": "1"}
        node = launch(stack, [sys.argv[1], "-config", str(cfg)], env, "node",
                      directory, context, node_port, "/healthz")
        ref = launch(stack, [sys.argv[2]], {**env, "NODE_HOST": "127.0.0.1",
                     "SERVICE_PORT": str(ref_port), "SERVICE_PROTOCOL": "rest",
                     "API_KEY": api_key, "SSL_CERT_FILE": str(cert), "SSL_KEY_FILE": str(key),
                     "PG_NODE_WG_HOST_ROUTING": "false", "PG_NODE_WG_NAT_DISABLE": "true"},
                     "reference", directory, context, ref_port, "/info")
        local = {"mtu": 1380, "listen_port": 51820, "private_key": private_key}
        source = json.dumps({**local, "addresses": ["192.0.2.1/24"]}).encode()
        pg_config = json.dumps({**local, "interface_name": peer_interface, "address": ["192.0.2.1/24"]}).encode()
        pg_source = b"\x08\x01\x12" + varint(len(pg_config)) + pg_config
        execution = f"/v1/wireguard/interfaces/{peer_interface}/execution"
        expected_hash = hashlib.sha256(source).hexdigest()
        def node_valid(payload):
            link = json.loads(payload)
            return link == {"name": peer_interface, "up": True, "mtu": 1380, "applied_config_sha256": expected_hash}
        def ref_valid(payload):
            return payload.startswith(b"\x08\x01")
        cases = [
            ("node", (node, "PUT", execution, source, "DELETE", execution, node_valid)),
            ("pasarguard", (ref, "POST", "/start", pg_source, "PUT", "/stop", ref_valid)),
        ]
        print("run,implementation,operation,iteration,nanoseconds")
        for repeat in range(5):
            for label, args in cases if repeat % 2 == 0 else reversed(cases):
                sample(*args)
                for index in range(100):
                    for op, elapsed in zip(("cold_start", "same_start", "stop"), sample(*args)):
                        print(f"{repeat + 1},{label},{op},{index + 1},{elapsed}")


if __name__ == "__main__":
    main()
