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


def launch(stack, argv, env, label, directory, context, listen, readiness, timeout=3):
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
    conn = http.client.HTTPSConnection("127.0.0.1", listen, timeout=timeout, context=context)
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


def measure_mtu(cases, config_sources):
    for repeat in range(5):
        for label, args in cases if repeat % 2 == 0 else reversed(cases):
            conn, start_method, start_path, source, stop_method, stop_path, valid = args
            _, payload = request(conn, start_method, start_path, source)
            assert valid(payload)
            previous_index = socket.if_nametoindex(peer_interface)
            for index in range(20):
                mtu = 1360 if index % 2 == 0 else 1380
                proposed = config_sources[label](mtu)
                elapsed, payload = request(conn, start_method, start_path, proposed)
                link = json.loads(subprocess.check_output(
                    ["ip", "-j", "link", "show", "dev", peer_interface]))[0]
                assert link["mtu"] == mtu and "UP" in link["flags"]
                if label == "node":
                    assert link["ifindex"] == previous_index
                    assert json.loads(payload)["applied_config_sha256"] == hashlib.sha256(proposed).hexdigest()
                else:
                    assert valid(payload) and link["ifindex"] != previous_index
                previous_index = link["ifindex"]
                print(f"{repeat + 1},{label},mtu_change,{index + 1},{elapsed}")
            request(conn, stop_method, stop_path)


def proto_bytes(field, content):
    return varint((field << 3) | 2) + varint(len(content)) + content


def peer_records(count, changed):
    addresses = [f"198.18.{(i + 2) // 256}.{(i + 2) % 256}/32" for i in range(count)]
    if changed:
        addresses[0] = "198.18.255.254/32"
    for i, address in enumerate(addresses):
        key = base64.b64encode(hashlib.sha256(f"isolated peer {i}".encode()).digest())
        wireguard = proto_bytes(1, key) + proto_bytes(2, address.encode())
        user = (proto_bytes(1, f"bench-{i}@example.invalid".encode())
                + proto_bytes(2, proto_bytes(5, wireguard))
                + proto_bytes(3, peer_interface.encode()))
        yield {"public_key": key.decode(), "allowed_ips": [address]}, user


def peer_payloads(count, changed):
    peers, users = zip(*peer_records(count, changed))
    expected = {peer["public_key"]: peer["allowed_ips"][0] for peer in peers}
    return {"node": json.dumps({"peers": peers}).encode(),
            "pasarguard": b"".join(proto_bytes(1, user) for user in users)}, expected


def check_peers(expected):
    lines = subprocess.check_output(["wg", "show", peer_interface, "allowed-ips"], text=True).splitlines()
    assert dict(line.split() for line in lines) == expected


def measure_peers(cases, phases=("noop", "changed")):
    payloads = {size: (peer_payloads(size, False), peer_payloads(size, True)) for size in (1, 100, 1000)}
    for repeat in range(5):
        for label, args in cases if repeat % 2 == 0 else reversed(cases):
            conn, start_method, start_path, source, stop_method, stop_path, valid = args
            _, reply = request(conn, start_method, start_path, source)
            assert valid(reply)
            path = "/users/sync" if label == "pasarguard" else f"/v1/wireguard/interfaces/{peer_interface}/peers"
            encoding = "pasarguard" if label == "pasarguard" else "node"
            for size, variants in payloads.items():
                initial, expected = variants[0]
                request(conn, "PUT", path, initial[encoding])
                check_peers(expected)
                for phase in phases:
                    for index in range(10):
                        variant = 1 - index % 2 if phase == "changed" else 0
                        bodies, expected = variants[variant]
                        elapsed, reply = request(conn, "PUT", path, bodies[encoding])
                        if encoding == "node":
                            assert json.loads(reply) == {"added": 0, "updated": int(phase == "changed"), "removed": 0}
                        print(f"{repeat + 1},{label},peers_{size}_{phase},{index + 1},{elapsed}")
                    check_peers(expected)
            request(conn, stop_method, stop_path)


def measure_peer_delta(cases):
    path = f"/v1/wireguard/interfaces/{peer_interface}/peers"
    population = 1000
    payload, baseline = peer_payloads(population, False)
    _, altered = peer_payloads(population, True)
    first_key = next(iter(baseline))
    url_key = base64.urlsafe_b64encode(base64.b64decode(first_key)).decode().rstrip("=")
    single_path = f"{path}/{url_key}"
    operations = {}
    for size in (1, 100, 1000):
        variants = []
        for changed in (False, True):
            peers, users = zip(*peer_records(size, changed))
            if size == 1:
                source = json.dumps({"allowed_ips": peers[0]["allowed_ips"]}).encode()
                variants.append({"node": ("PUT", single_path, source),
                                 "pasarguard": ("PUT", "/user/sync", users[0])})
            else:
                chunk = b"".join(proto_bytes(1, user) for user in users) + b"\x18\x01"
                variants.append({"node": ("PATCH", path, json.dumps({"upsert": peers}).encode()),
                                 "pasarguard": ("PUT", "/users/sync/chunked", varint(len(chunk)) + chunk)})
        operations[size] = variants
    remove = {"node": ("DELETE", single_path, None),
              "pasarguard": ("PUT", "/user/sync", proto_bytes(1, b"bench-0@example.invalid"))}
    without_first = {key: address for key, address in baseline.items() if key != first_key}
    for block in range(4):
        for label, args in cases if block % 2 == 0 else reversed(cases):
            conn, start_method, start_path, source, stop_method, stop_path, valid = args
            _, reply = request(conn, start_method, start_path, source)
            assert valid(reply)
            encoding = "pasarguard" if label == "pasarguard" else "node"
            request(conn, "PUT", path if encoding == "node" else "/users/sync", payload[encoding])
            check_peers(baseline)
            for size, variants in operations.items():
                for phase in ("change", "replay"):
                    for index in range(10):
                        changed = phase == "change" and index % 2 == 0
                        elapsed, reply = request(conn, *variants[changed][encoding])
                        if encoding == "node":
                            assert json.loads(reply) == {"added": 0, "updated": int(phase == "change"), "removed": 0}
                        print(f"{block + 1},{label},delta_{size}_{phase},{index + 1},{elapsed}")
                        if index in (0, 9):
                            check_peers(altered if changed else baseline)
            for index in range(10):
                elapsed, _ = request(conn, *remove[encoding])
                print(f"{block + 1},{label},delta_1_remove,{index + 1},{elapsed}")
                check_peers(without_first)
                request(conn, *operations[1][0][encoding])
            check_peers(baseline)
            request(conn, stop_method, stop_path)


def main():
    mode = sys.argv[3] if len(sys.argv) >= 4 else ""
    valid_args = (len(sys.argv) == 3 or len(sys.argv) == 4 and mode in ("--live-mtu", "--peer-sync", "--peer-delta")
                  or len(sys.argv) == 5 and mode in ("--peer-replay", "--peer-delta"))
    if not valid_args:
        raise SystemExit("usage: measure-lifecycle.py NODE PASARGUARD [--live-mtu|--peer-sync|--peer-replay PREVIOUS_NODE|--peer-delta [PREVIOUS_NODE]]")
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
        peer_test = mode in ("--peer-sync", "--peer-replay", "--peer-delta")
        env = {**os.environ, "GOMAXPROCS": "2" if peer_test else "1"}
        timeout = 15 if peer_test else 3

        def launch_node(binary, label):
            node_port, grpc_port = port(), port()
            cfg = directory / f"{label}.yml"
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
            return launch(stack, [binary, "-config", str(cfg)], env, label,
                          directory, context, node_port, "/healthz", timeout)

        node = launch_node(sys.argv[1], "node")
        ref_port = port()
        ref = launch(stack, [sys.argv[2]], {**env, "NODE_HOST": "127.0.0.1",
                     "SERVICE_PORT": str(ref_port), "SERVICE_PROTOCOL": "rest",
                     "API_KEY": api_key, "SSL_CERT_FILE": str(cert), "SSL_KEY_FILE": str(key),
                     "PG_NODE_WG_HOST_ROUTING": "false", "PG_NODE_WG_NAT_DISABLE": "true"},
                     "reference", directory, context, ref_port, "/info", timeout)
        local = {"listen_port": 51820, "private_key": private_key}
        address = "198.18.0.1/16" if peer_test else "192.0.2.1/24"

        def node_config(mtu):
            return json.dumps({**local, "mtu": mtu, "addresses": [address]}).encode()

        def ref_config(mtu):
            config = json.dumps({**local, "mtu": mtu, "interface_name": peer_interface, "address": [address]}).encode()
            return b"\x08\x01\x12" + varint(len(config)) + config

        source = node_config(1380)
        pg_source = ref_config(1380)
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
        if mode == "--live-mtu":
            measure_mtu(cases, {"node": node_config, "pasarguard": ref_config})
            return
        if mode == "--peer-sync":
            measure_peers(cases)
            return
        if mode == "--peer-delta":
            if len(sys.argv) == 5:
                previous = launch_node(sys.argv[4], "node-before")
                cases.insert(0, ("node-before", (previous, "PUT", execution, source, "DELETE", execution, node_valid)))
            measure_peer_delta(cases)
            return
        if mode == "--peer-replay":
            previous = launch_node(sys.argv[4], "node-before")
            cases.insert(0, ("node-before", (previous, "PUT", execution, source, "DELETE", execution, node_valid)))
            measure_peers(cases, phases=("noop",))
            return
        for repeat in range(5):
            for label, args in cases if repeat % 2 == 0 else reversed(cases):
                sample(*args)
                for index in range(100):
                    for op, elapsed in zip(("cold_start", "same_start", "stop"), sample(*args)):
                        print(f"{repeat + 1},{label},{op},{index + 1},{elapsed}")


if __name__ == "__main__":
    main()
