import base64
import hashlib
import http.client
import http.server
import itertools
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time
from contextlib import ExitStack, closing
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
    check_link_removed()
    return start_ns, same_ns, stop_ns


def check_link_removed(name=peer_interface):
    try:
        socket.if_nametoindex(name)
    except OSError:
        return
    raise RuntimeError("Stop left an interface")


def measure_kernel_delete(args, block):
    conn, start_method, start_path, source, stop_method, stop_path, valid = args
    for index in range(4):
        _, payload = request(conn, start_method, start_path, source)
        assert valid(payload)
        start = time.perf_counter_ns()
        subprocess.run(["ip", "link", "del", "dev", peer_interface], check=True)
        elapsed = time.perf_counter_ns() - start
        check_link_removed()
        request(conn, stop_method, stop_path)
        print(f"{block},ip,kernel_delete,{index + 1},{elapsed}")


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


def peer_record(key, address, email, name):
    wireguard = proto_bytes(1, key) + proto_bytes(2, address.encode())
    user = (proto_bytes(1, email.encode()) + proto_bytes(2, proto_bytes(5, wireguard))
            + proto_bytes(3, name.encode()))
    return {"public_key": key.decode(), "allowed_ips": [address]}, user


def peer_records(count, changed, name=peer_interface, pool="198.18", first_host=2):
    addresses = [f"{pool}.{host // 256}.{host % 256}/32" for host in range(first_host, first_host + count)]
    if changed:
        addresses[0] = f"{pool}.255.254/32"
    for i, address in enumerate(addresses):
        key = base64.b64encode(hashlib.sha256(f"isolated peer {i}".encode()).digest())
        yield peer_record(key, address, f"bench-{i}@example.invalid", name)


def peer_payloads(count, changed):
    peers, users = zip(*peer_records(count, changed))
    expected = {peer["public_key"]: peer["allowed_ips"][0] for peer in peers}
    return {"node": json.dumps({"peers": peers}).encode(),
            "pasarguard": b"".join(proto_bytes(1, user) for user in users)}, expected


def check_peers(expected, name=peer_interface):
    lines = subprocess.check_output(["wg", "show", name, "allowed-ips"], text=True).splitlines()
    assert dict(line.split() for line in lines) == expected


def measure_interface_traffic(cases, reference_name, usage=False):
    link_path = f"/v1/wireguard/interfaces/{peer_interface}"
    outbound_request = proto_bytes(1, reference_name.encode()) + b"\x18\x01"
    operations = (
        ("usage_read", "GET", link_path + "/usage", outbound_request),
        ("usage_reset", "POST", link_path + "/usage/resets", outbound_request + b"\x10\x01"),
    ) if usage else (("traffic", "GET", link_path + "/traffic", outbound_request),)
    names = {"node": peer_interface, "pasarguard": reference_name}
    for _, (conn, method, path, source, _, _, valid) in cases:
        _, payload = request(conn, method, path, source)
        assert valid(payload)
    for count in (0, 1000):
        peers = {peer["public_key"]: peer["allowed_ips"][0]
                 for peer, _ in peer_records(count, False)}
        peer_config = "[Interface]\n" + "".join(
            f"[Peer]\nPublicKey = {key}\nAllowedIPs = {address}\n"
            for key, address in peers.items())
        for name in names.values():
            if peers:
                subprocess.run(["wg", "addconf", name, "/dev/stdin"],
                               input=peer_config, text=True, check=True)
            check_peers(peers, name)
        for block in range(1, 5):
            for operation, node_method, node_path, ref_body in operations:
                for label, (conn, *_) in cases if block % 2 else reversed(cases):
                    method, path, body = (node_method, node_path, None) if label == "node" else ("GET", "/stats/", ref_body)
                    for iteration in range(-5, 40):
                        elapsed, payload = request(conn, method, path, body)
                        if label == "node":
                            assert json.loads(payload) == {"rx_bytes": "0", "tx_bytes": "0"}
                        else:
                            assert payload == b"", "expected an empty zero-delta StatResponse"
                        if iteration >= 0:
                            print(f"{block},{label},{operation}_{count}_peers,{iteration + 1},{elapsed}")
    for label, (conn, _, _, _, method, path, _) in cases:
        request(conn, method, path)
        check_link_removed(names[label])


def measure_diagnostics(cases, context):
    root = f"/v1/wireguard/interfaces/{peer_interface}"
    for block in range(1, 5):
        for label, args in cases if block % 2 else reversed(cases):
            conn, method, path, source, stop_method, stop_path, valid = args
            _, payload = request(conn, method, path, source)
            assert valid(payload)
            runtime_path = root + "/runtime" if label == "node" else "/stats/backend"
            logs_path = root + "/logs" if label == "node" else "/logs"
            for iteration in range(-3, 30):
                elapsed, payload = request(conn, "GET", runtime_path)
                assert int(json.loads(payload)["heap_bytes"]) > 0 if label == "node" else bool(payload)
                if iteration >= 0:
                    print(f"{block},{label},runtime_stats,{iteration + 1},{elapsed}")
                with closing(http.client.HTTPSConnection(conn.host, conn.port, timeout=3, context=context)) as stream:
                    then = time.perf_counter_ns()
                    stream.request("GET", logs_path, headers=headers)
                    reply = stream.getresponse()
                    line = reply.readline(4096)
                    elapsed = time.perf_counter_ns() - then
                    assert reply.status == 200 and bool(line)
                    assert json.loads(line)["kind"] == "started" if label == "node" and block == 1 else True
                    if iteration >= 0:
                        print(f"{block},{label},log_first_event,{iteration + 1},{elapsed}")
            request(conn, stop_method, stop_path)
            check_link_removed()


def probe_target(stack):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_HEAD(self):
            self.send_response(204)
            self.end_headers()

        def log_message(self, *_):
            pass

    listener = stack.enter_context(http.server.HTTPServer(("127.0.0.1", 0), Handler))
    thread = threading.Thread(target=listener.serve_forever, kwargs={"poll_interval": 0.01})
    thread.start()
    stack.callback(thread.join)
    stack.callback(listener.shutdown)
    return f"http://127.0.0.1:{listener.server_port}/health"


def measure_probe(cases):
    orders = itertools.permutations(cases) if len(cases) == 3 else (
        cases if block % 2 else reversed(cases) for block in range(1, 5))
    for block, order in enumerate(orders, 1):
        for label, args in order:
            conn, method, path, source, stop_method, stop_path, valid = args
            _, reply = request(conn, method, path, source)
            assert valid(reply)
            path = "/stats/latency" if label == "pasarguard" else f"/v1/wireguard/interfaces/{peer_interface}/egress-probe"
            for iteration in range(-3, 30):
                elapsed, payload = request(conn, "GET", path)
                if label != "pasarguard":
                    data = json.loads(payload)
                    assert data["reachable"] and data["http_status"] == 204
                else:
                    assert b"\x10\x01" in payload
                if iteration >= 0:
                    print(f"{block},{label},egress_probe,{iteration + 1},{elapsed}")
            request(conn, stop_method, stop_path)
            check_link_removed()


def measure_routing(cases):
    for block in range(1, 5):
        for label, args in cases if block % 2 else reversed(cases):
            conn, method, path, source, stop_method, stop_path, valid = args
            elapsed, payload = request(conn, method, path, source)
            assert valid(payload)
            rules = subprocess.check_output(["ip", "rule", "show"], text=True)
            assert f"iif {peer_interface} lookup 20101" in rules
            print(f"{block},{label},routing_start,1,{elapsed}")
            elapsed, _ = request(conn, stop_method, stop_path)
            check_link_removed()
            rules = subprocess.check_output(["ip", "rule", "show"], text=True)
            assert f"iif {peer_interface} lookup 20101" not in rules
            print(f"{block},{label},routing_stop,1,{elapsed}")


def measure_initial_peers(cases):
    for count in (1, 100):
        rows, users = zip(*peer_records(count, False))
        expected = {peer["public_key"]: peer["allowed_ips"][0] for peer in rows}
        for block in range(1, 4):
            for label, args in cases if block % 2 else reversed(cases):
                conn, method, path, source, stop_method, stop_path, _ = args
                if label == "node":
                    config = json.loads(source)
                    config["initial_peers"] = rows
                    source = json.dumps(config).encode()
                else:
                    source += b"".join(proto_bytes(3, user) for user in users)
                elapsed, payload = request(conn, method, path, source)
                if label == "node":
                    assert json.loads(payload)["applied_config_sha256"] == hashlib.sha256(source).hexdigest()
                else:
                    assert payload.startswith(b"\x08\x01")
                check_peers(expected)
                print(f"{block},{label},initial_{count}_peers,1,{elapsed}")
                request(conn, stop_method, stop_path)
                check_link_removed()


def usage_client(stack, index, server_ip, client_ip, server_port, secret):
    child = subprocess.Popen(["unshare", "--net", "sh", "-c", "echo ready; read -r hold"],
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)

    def close():
        child.stdin.close()
        try:
            child.wait(timeout=3)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait()
        child.stdout.close()

    stack.callback(close)
    if child.stdout.readline() != "ready\n":
        raise RuntimeError("client network namespace failed")
    run = ["nsenter", "--target", str(child.pid), "--net", "--"]
    wire = f"wguplink{index}"
    server_underlay = f"198.51.100.{4 * index + 1}"
    client_underlay = f"198.51.100.{4 * index + 2}"
    subprocess.run(["ip", "link", "add", wire, "type", "veth", "peer", "name", "underlay", "netns", str(child.pid)], check=True)
    subprocess.run(["ip", "addr", "add", server_underlay + "/30", "dev", wire], check=True)
    subprocess.run(["ip", "link", "set", wire, "up"], check=True)
    subprocess.run(run + ["ip", "addr", "add", client_underlay + "/30", "dev", "underlay"], check=True)
    subprocess.run(run + ["ip", "link", "set", "underlay", "up"], check=True)
    subprocess.run(run + ["ip", "link", "add", "wg-client", "type", "wireguard"], check=True)
    subprocess.run(run + ["ip", "addr", "add", client_ip + "/24", "dev", "wg-client"], check=True)
    server_key = subprocess.check_output(["wg", "pubkey"], input=private_key + "\n", text=True).strip()
    settings = (f"[Interface]\nPrivateKey = {secret}\n[Peer]\nPublicKey = {server_key}\n"
                f"AllowedIPs = {server_ip}/32\nEndpoint = {server_underlay}:{server_port}\n")
    subprocess.run(run + ["wg", "setconf", "wg-client", "/dev/stdin"], input=settings, text=True, check=True)
    subprocess.run(run + ["ip", "link", "set", "wg-client", "up"], check=True)
    subprocess.run(run + ["ping", "-n", "-c", "1", "-W", "3", server_ip], check=True, stdout=subprocess.DEVNULL)


def measure_sampled_usage(cases, reference_name, activity=False):
    name_by_label = {"node": peer_interface, "pasarguard": reference_name}
    email = "sampled-peer@example.invalid"
    node_root = f"/v1/wireguard/interfaces/{peer_interface}/peers"
    for count in (1, 1000):
        operations = {}
        activity_paths = {}
        with ExitStack() as clients:
            for index, (label, args) in enumerate(cases):
                conn, method, path, source, _, _, valid = args
                _, reply = request(conn, method, path, source)
                assert valid(reply)
                name = name_by_label[label]
                pool = "198.18" if label == "node" else "198.19"
                client_ip, server_ip = f"{pool}.0.2", f"{pool}.0.1"
                secret = subprocess.check_output(["wg", "genkey"], text=True).strip()
                key = subprocess.check_output(["wg", "pubkey"], input=secret + "\n", text=True).strip()
                active = peer_record(key.encode(), client_ip + "/32", email, name)
                peers, users = zip(active, *peer_records(count - 1, False, name, pool=pool, first_host=3))
                if label == "node":
                    request(conn, "PUT", node_root, json.dumps({"peers": peers}).encode())
                    key_path = base64.urlsafe_b64encode(base64.b64decode(key)).decode().rstrip("=")
                    single, group = f"{node_root}/{key_path}/usage", node_root + "/usage"
                    operations[label] = [("GET", single, None), ("GET", group, None),
                                         ("POST", single + "/resets", None), ("POST", group + "/resets", None)]
                    activity_paths[label] = [("GET", f"{node_root}/{key_path}/" + suffix, None) for suffix in ("activity", "endpoint-ips")]
                else:
                    request(conn, "PUT", "/users/sync", b"".join(proto_bytes(1, user) for user in users))
                    single, group = proto_bytes(1, email.encode()) + b"\x18\x05", b"\x18\x04"
                    operations[label] = [("GET", "/stats/", body) for body in (single, group, single + b"\x10\x01", group + b"\x10\x01")]
                    activity_paths[label] = [("GET", "/stats/user/" + suffix, proto_bytes(1, email.encode())) for suffix in ("online", "online_ip")]
                check_peers({peer["public_key"]: peer["allowed_ips"][0] for peer in peers}, name)
                usage_client(clients, index, server_ip, client_ip, 51820 + index, secret)

            def read(label, conn, operation, nonzero):
                elapsed, payload = request(conn, *operations[label][operation])
                if label == "node":
                    usage = json.loads(payload).get("peer" if operation % 2 == 0 else "peers")
                    seen = bool(usage)
                else:
                    seen = bool(payload)
                assert seen == nonzero, (label, operation, payload)
                return elapsed

            for label, (conn, *_) in cases:
                deadline = time.monotonic() + 15
                while True:
                    _, payload = request(conn, *operations[label][0])
                    if (bool(json.loads(payload).get("peer")) if label == "node" else bool(payload)):
                        break
                    if time.monotonic() >= deadline:
                        raise RuntimeError(f"{label} sampled usage was empty after exchanging traffic")
                    time.sleep(.05)

            names = ("activity", "endpoint_ips") if activity else ("peer_read", "group_read", "peer_reset", "group_reset")
            for operation, operation_name in enumerate(names):
                if not activity and operation == 2:
                    for label, (conn, *_) in cases:
                        read(label, conn, 3, nonzero=True)
                for block in range(1, 5):
                    if activity:
                        # Pair the two reads in time. Keep a slow scheduler window
                        # or one sampler tick from affecting an entire build's block.
                        for iteration in range(-5, 40):
                            order = cases if (block + iteration) % 2 else reversed(cases)
                            for label, (conn, *_) in order:
                                elapsed, payload = request(conn, *activity_paths[label][operation])
                                if label == "node":
                                    result = json.loads(payload)
                                    assert result["active"] if operation == 0 else result["endpoints"][0]["ip"] == "198.51.100.2"
                                else:
                                    assert (b"\x10\x01" if operation == 0 else b"198.51.100.6") in payload
                                if iteration >= 0:
                                    print(f"{block},{label},{operation_name}_{count}_peers,{iteration + 1},{elapsed}")
                        continue
                    for label, (conn, *_) in cases if block % 2 else reversed(cases):
                        for iteration in range(-5, 40):
                            elapsed = read(label, conn, operation, nonzero=operation < 2)
                            if iteration >= 0:
                                print(f"{block},{label},{operation_name}_{count}_peers,{iteration + 1},{elapsed}")
        for label, (conn, _, _, _, method, path, _) in cases:
            request(conn, method, path)
            check_link_removed(name_by_label[label])


def measure_peers(cases, phases=("noop", "changed")):
    payloads = {size: (peer_payloads(size, False), peer_payloads(size, True)) for size in (1, 100, 1000)}
    if len(cases) == 3:
        orders = itertools.permutations(cases)
    else:
        orders = (cases if repeat % 2 == 0 else reversed(cases) for repeat in range(5))
    for repeat, order in enumerate(orders, start=1):
        for label, args in order:
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
                        print(f"{repeat},{label},peers_{size}_{phase},{index + 1},{elapsed}")
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
            request(conn, *operations[1][0][encoding])
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


def measure_peer_removal(cases):
    path = f"/v1/wireguard/interfaces/{peer_interface}/peers"
    for population in (1, 100, 1000):
        sources, baseline = peer_payloads(population, False)
        target, encoded = next(peer_records(1, False))
        key = target["public_key"]
        key_path = base64.urlsafe_b64encode(base64.b64decode(key)).decode().rstrip("=")
        single = f"{path}/{key_path}"
        restore = json.dumps({"allowed_ips": target["allowed_ips"]}).encode()
        remaining = {peer: prefix for peer, prefix in baseline.items() if peer != key}
        for block, order in enumerate(itertools.permutations(cases), 1):
            for label, args in order:
                conn, start_method, start_path, source, stop_method, stop_path, valid = args
                _, state = request(conn, start_method, start_path, source)
                assert valid(state)
                reference = label == "pasarguard"
                sync_path = "/users/sync" if reference else path
                request(conn, "PUT", sync_path, sources["pasarguard" if reference else "node"])
                check_peers(baseline)
                for index in range(-2, 12):
                    if reference:
                        elapsed, _ = request(conn, "PUT", "/user/sync", proto_bytes(1, b"bench-0@example.invalid"))
                    else:
                        elapsed, _ = request(conn, "DELETE", single)
                    check_peers(remaining)
                    if reference:
                        request(conn, "PUT", "/user/sync", encoded)
                    else:
                        request(conn, "PUT", single, restore)
                    if index >= 0:
                        print(f"{block},{label},remove_{population}_peers,{index + 1},{elapsed}")
                check_peers(baseline)
                request(conn, stop_method, stop_path)


def measure_paired_peer_removal(cases, reference_name):
    for population in (1, 100):
        operations = {}
        for label, (conn, start_method, start_path, source, _, _, valid) in cases:
            _, state = request(conn, start_method, start_path, source)
            assert valid(state)
            reference = label == "pasarguard"
            name = reference_name if reference else peer_interface
            pool = "198.19" if reference else "198.18"
            peers, users = zip(*peer_records(population, False, name, pool=pool))
            if reference:
                request(conn, "PUT", "/users/sync", b"".join(proto_bytes(1, user) for user in users))
                remove = ("PUT", "/user/sync", proto_bytes(1, b"bench-0@example.invalid"))
                restore = ("PUT", "/user/sync", users[0])
            else:
                path = f"/v1/wireguard/interfaces/{name}/peers"
                request(conn, "PUT", path, json.dumps({"peers": peers}).encode())
                key = base64.urlsafe_b64encode(base64.b64decode(peers[0]["public_key"])).decode().rstrip("=")
                remove = ("DELETE", f"{path}/{key}")
                restore = ("PUT", f"{path}/{key}", json.dumps({"allowed_ips": peers[0]["allowed_ips"]}).encode())
            expected = {peer["public_key"]: peer["allowed_ips"][0] for peer in peers}
            remaining = {peer["public_key"]: peer["allowed_ips"][0] for peer in peers[1:]}
            check_peers(expected, name)
            operations[label] = (conn, name, remove, restore, expected, remaining)

        for block in range(1, 5):
            for index in range(-4, 30):
                order = operations if (block + index) % 2 else reversed(operations)
                # Time both acknowledged removals back to back. Verify and
                # restore the actual kernel membership outside the timed pair.
                elapsed = {label: request(operations[label][0], *operations[label][2])[0]
                           for label in order}
                for label in elapsed:
                    conn, name, _, restore, expected, remaining = operations[label]
                    check_peers(remaining, name)
                    request(conn, *restore)
                    check_peers(expected, name)
                    if index >= 0:
                        print(f"{block},{label},remove_{population}_peers,{index + 1},{elapsed[label]}")
        for label, (conn, _, _, _, stop_method, stop_path, _) in cases:
            request(conn, stop_method, stop_path)
            check_link_removed(operations[label][1])


def chunked_delta_payload(label, peers, users):
    objects = peers if label == "node" else users
    parts = [objects[start:start + 100] for start in range(0, len(objects), 100)]
    frames = []
    for index, part in enumerate(parts):
        last = index == len(parts) - 1
        if label == "node":
            frames.append(json.dumps({"index": index, "last": last, "upsert": part}, separators=(",", ":")).encode() + b"\n")
        else:
            payload = (b"\x10" + varint(index) if index else b"") + (b"\x18\x01" if last else b"")
            payload += b"".join(proto_bytes(1, user) for user in part)
            frames.append(varint(len(payload)) + payload)
    return b"".join(frames)


def measure_chunked_peer_delta(cases):
    path = f"/v1/wireguard/interfaces/{peer_interface}/peers"
    for count in (1, 100, 1000):
        initial, before = peer_payloads(count, False)
        changed_peers, changed_users = zip(*peer_records(count, False, first_host=count + 2))
        after = {peer["public_key"]: peer["allowed_ips"][0] for peer in changed_peers}
        peers, users = zip(*peer_records(count, False))
        for block, order in enumerate((cases, reversed(cases)), 1):
            for label, (conn, start, start_path, source, stop, stop_path, valid) in order:
                node = label == "node"
                _, response = request(conn, start, start_path, source)
                assert valid(response)
                request(conn, "PUT", path if node else "/users/sync", initial[label])
                check_peers(before)
                method, endpoint = ("PATCH", path + "/chunks") if node else ("PUT", "/users/sync/chunked")
                update = chunked_delta_payload(label, changed_peers, changed_users)
                restore = chunked_delta_payload(label, peers, users)
                if node:
                    headers["Content-Type"] = "application/x-ndjson"
                try:
                    for index in range(-2, 10):
                        elapsed, reply = request(conn, method, endpoint, update)
                        if node:
                            assert json.loads(reply)["updated"] == count
                        check_peers(after)
                        request(conn, method, endpoint, restore)
                        check_peers(before)
                        if index >= 0:
                            print(f"{block},{label},chunks_{count},{index + 1},{elapsed}")
                finally:
                    if node:
                        del headers["Content-Type"]
                request(conn, stop, stop_path)
                check_link_removed()


def main():
    mode = sys.argv[3] if len(sys.argv) >= 4 else ""
    routing_test = mode == "--routing"
    paired_removal = mode == "--peer-removal-paired"
    sampled_test = mode in ("--sampled-peer-usage", "--peer-activity")
    link_test = sampled_test or mode in ("--interface-traffic", "--interface-usage")
    valid_args = (len(sys.argv) == 3 or len(sys.argv) == 4 and (link_test or routing_test or paired_removal or mode in ("--lifecycle-check", "--live-mtu", "--peer-sync", "--peer-delta", "--peer-delta-chunks", "--peer-removal", "--diagnostics", "--probe", "--initial-peers"))
                  or len(sys.argv) == 5 and mode in ("--lifecycle-check", "--peer-sync", "--peer-replay", "--peer-delta", "--peer-removal", "--probe"))
    if not valid_args:
        raise SystemExit("usage: measure-lifecycle.py NODE PASARGUARD [--lifecycle-check [PREVIOUS_NODE]|--live-mtu|--interface-traffic|--interface-usage|--sampled-peer-usage|--peer-activity|--peer-sync [PREVIOUS_NODE]|--peer-replay PREVIOUS_NODE|--peer-delta [PREVIOUS_NODE]|--peer-delta-chunks|--peer-removal [PREVIOUS_NODE]|--peer-removal-paired|--probe [PREVIOUS_NODE]]")
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
        peer_test = paired_removal or mode in ("--peer-sync", "--peer-replay", "--peer-delta", "--peer-delta-chunks", "--peer-removal", "--initial-peers")
        env = {**os.environ, "GOMAXPROCS": "2" if peer_test else "1"}
        timeout = 15 if peer_test or link_test else 3
        target = probe_target(stack) if mode == "--probe" else ""
        if routing_test:
            subprocess.run(["ip", "link", "add", "wg-bench-egress", "type", "dummy"], check=True)
            subprocess.run(["ip", "link", "set", "wg-bench-egress", "up"], check=True)

        def launch_node(binary, label, sampled_removal=False):
            node_port, grpc_port = port(), port()
            cfg = directory / f"{label}.yml"
            settings = f"""management:
  http_address: "127.0.0.1:{node_port}"
  grpc_address: "127.0.0.1:{grpc_port}"
  api_key: "{api_key}"
  tls:
    cert_file: "{cert}"
    key_file: "{key}"
wireguard:
  interface: "{peer_interface}"
  managed: true
"""
            if sampled_removal:
                settings += "  peer_removal_usage: sampled\n"
            if target:
                settings += f"  probe:\n    url: {target}\n    interface: lo\n    timeout_ms: 2000\n"
            if routing_test:
                settings += "  routing:\n    egress_interface: wg-bench-egress\n    masquerade: true\n    policy_table: 20101\n"
            cfg.write_text(settings)
            return launch(stack, [binary, "-config", str(cfg)], env, label,
                          directory, context, node_port, "/healthz", timeout)

        node_label = "node-sampled" if paired_removal else "node"
        node = launch_node(sys.argv[1], node_label, sampled_removal=paired_removal)
        ref_port = port()
        ref = launch(stack, [sys.argv[2]], {**env, "NODE_HOST": "127.0.0.1",
                     "SERVICE_PORT": str(ref_port), "SERVICE_PROTOCOL": "rest",
                     "API_KEY": api_key, "SSL_CERT_FILE": str(cert), "SSL_KEY_FILE": str(key),
                     "PG_NODE_WG_HOST_ROUTING": "false", "PG_NODE_WG_NAT_DISABLE": "true",
                     **({"PG_NODE_WG_NAT_OUTPUT_INTERFACE": "lo"} if target else {}),
                     **({"PG_NODE_WG_HOST_ROUTING": "true", "PG_NODE_WG_NAT_DISABLE": "false", "PG_NODE_WG_NAT_EGRESS_ONLY": "false",
                         "PG_NODE_WG_NAT_OUTPUT_INTERFACE": "wg-bench-egress", "PG_NODE_WG_ROUTE_TABLE": "20101",
                         "PG_NODE_WG_ROUTE_OUT_INTERFACE": "wg-bench-egress"} if routing_test else {})},
                     "reference", directory, context, ref_port, "/info", timeout)
        local = {"listen_port": 51820, "private_key": private_key}
        address = "198.18.0.1/16" if peer_test or sampled_test else "192.0.2.1/24"
        reference_name = "wg-bench1" if link_test or paired_removal else peer_interface

        def node_config(mtu):
            return json.dumps({**local, "mtu": mtu, "addresses": [address]}).encode()

        def ref_config(mtu):
            reference = {**local, "mtu": mtu, "interface_name": reference_name, "address": [address]}
            if link_test or paired_removal:
                ref_address = "198.19.0.1/16" if sampled_test or paired_removal else "192.0.2.2/24"
                reference.update(listen_port=51821, address=[ref_address])
            if target:
                reference["latency"] = {"test_url": target, "timeout_seconds": 2}
            config = json.dumps(reference).encode()
            return b"\x08\x01\x12" + varint(len(config)) + config

        source = node_config(1380)
        pg_source = ref_config(1380)
        execution = f"/v1/wireguard/interfaces/{peer_interface}/execution"
        expected_hash = hashlib.sha256(source).hexdigest()
        def node_valid(payload):
            link = json.loads(payload)
            projection = {key: link.get(key) for key in ("name", "up", "mtu", "applied_config_sha256")}
            return projection == {"name": peer_interface, "up": True, "mtu": 1380, "applied_config_sha256": expected_hash}
        def ref_valid(payload):
            return payload.startswith(b"\x08\x01")
        cases = [
            (node_label, (node, "PUT", execution, source, "DELETE", execution, node_valid)),
            ("pasarguard", (ref, "POST", "/start", pg_source, "PUT", "/stop", ref_valid)),
        ]
        if len(sys.argv) == 5:
            previous = launch_node(sys.argv[4], "node-before", sampled_removal=mode == "--peer-removal")
            cases.insert(0, ("node-before", (previous, "PUT", execution, source, "DELETE", execution, node_valid)))
        print("run,implementation,operation,iteration,nanoseconds")
        if paired_removal:
            measure_paired_peer_removal(cases, reference_name)
            return
        if mode == "--peer-removal":
            if len(sys.argv) == 5:
                cases = [(label, args) for label, args in cases if label != "node"]
            sampled = launch_node(sys.argv[1], "node-sampled", sampled_removal=True)
            cases.append(("node-sampled", (sampled, "PUT", execution, source, "DELETE", execution, node_valid)))
            measure_peer_removal(cases)
            return
        if routing_test:
            measure_routing(cases)
            return
        if mode == "--initial-peers":
            measure_initial_peers(cases)
            return
        if mode == "--probe":
            measure_probe(cases)
            return
        if mode == "--diagnostics":
            measure_diagnostics(cases, context)
            return
        if sampled_test:
            measure_sampled_usage(cases, reference_name, activity=mode == "--peer-activity")
            return
        if link_test:
            measure_interface_traffic(cases, reference_name, usage=mode == "--interface-usage")
            return
        if mode == "--live-mtu":
            measure_mtu(cases, {"node": node_config, "pasarguard": ref_config})
            return
        if mode == "--peer-delta":
            measure_peer_delta(cases)
            return
        if mode == "--peer-delta-chunks":
            measure_chunked_peer_delta(cases)
            return
        if mode in ("--peer-sync", "--peer-replay"):
            measure_peers(cases, phases=("noop",) if mode == "--peer-replay" else ("noop", "changed"))
            return
        paired = mode == "--lifecycle-check" and len(cases) == 3
        orders = itertools.permutations(cases) if paired else (cases if i % 2 == 0 else reversed(cases) for i in range(5))
        for repeat, order in enumerate(orders, 1):
            for label, args in order:
                sample(*args)
                for index in range(12 if paired else 20 if mode == "--lifecycle-check" else 100):
                    for op, elapsed in zip(("cold_start", "same_start", "stop"), sample(*args)):
                        print(f"{repeat},{label},{op},{index + 1},{elapsed}")
            if mode == "--lifecycle-check":
                measure_kernel_delete(cases[0][1], repeat)


if __name__ == "__main__":
    main()
