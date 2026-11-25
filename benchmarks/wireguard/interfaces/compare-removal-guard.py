import base64
import hashlib
import importlib.util
import itertools
import json
from pathlib import Path
import sys
import subprocess

if len(sys.argv) != 5 or sys.argv[3] != "--peer-removal":
    raise SystemExit(
        "usage: compare-removal-guard.py NODE PASARGUARD --peer-removal BASELINE_NODE"
    )

fpath = Path(__file__).with_name("measure-lifecycle.py")
spec = importlib.util.spec_from_file_location("wg_fixture", fpath)
f = importlib.util.module_from_spec(spec)
spec.loader.exec_module(f)

launch = f.launch
links = {
    "node-sampled": ("wg-bench1", "198.19", 51821),
    "node-before": ("wg-bench2", "198.20", 51822),
    "pasarguard": (f.peer_interface, "198.18", 51820),
}


def launch_compared(stack, argv, env, label, directory, *args):
    if label in ("node-sampled", "node-before"):
        config = Path(argv[-1])
        config.write_text(
            config.read_text().replace(
                f'interface: "{f.peer_interface}"', f'interface: "{links[label][0]}"'
            )
        )
    return launch(stack, argv, env, label, directory, *args)


f.launch = launch_compared


def compare(cases):
    cases = [case for case in cases if case[0] in links]
    for population in (1, 100):
        operations = {}
        stop_calls = {}
        for label, (conn, start, path, source, stop, stop_path, valid) in cases:
            name, pool, port = links[label]
            original = label == "pasarguard"
            if not original:
                config = json.loads(source)
                config.update(listen_port=port, addresses=[f"{pool}.0.1/16"])
                source = json.dumps(config).encode()
                path = path.replace(f.peer_interface, name)
                stop_path = stop_path.replace(f.peer_interface, name)
            _, state = f.request(conn, start, path, source)
            if original:
                assert valid(state)
            else:
                status = json.loads(state)
                assert (
                    status["name"] == name
                    and status["up"]
                    and status["applied_config_sha256"]
                    == hashlib.sha256(source).hexdigest()
                )
            peers, users = zip(*f.peer_records(population, False, name, pool=pool))
            root = f"/v1/wireguard/interfaces/{name}/peers"
            single = (
                root
                + "/"
                + base64.urlsafe_b64encode(base64.b64decode(peers[0]["public_key"]))
                .decode()
                .rstrip("=")
            )
            if original:
                f.request(
                    conn,
                    "PUT",
                    "/users/sync",
                    b"".join(f.proto_bytes(1, user) for user in users),
                )
                remove = (
                    "PUT",
                    "/user/sync",
                    f.proto_bytes(1, b"bench-0@example.invalid"),
                )
                restore = ("PUT", "/user/sync", users[0])
            else:
                f.request(conn, "PUT", root, json.dumps({"peers": peers}).encode())
                remove = ("DELETE", single, None)
                restore = (
                    "PUT",
                    single,
                    json.dumps({"allowed_ips": peers[0]["allowed_ips"]}).encode(),
                )
            full = {peer["public_key"]: peer["allowed_ips"][0] for peer in peers}
            remaining = {
                peer["public_key"]: peer["allowed_ips"][0] for peer in peers[1:]
            }
            f.check_peers(full, name)
            if label == "node-sampled":

                def rejected(code):
                    conn.request(*remove, headers=f.headers)
                    reply = conn.getresponse()
                    reply.read()
                    assert reply.status == code, (label, reply.status, code)
                    f.check_peers(full, name)

                original_alias = json.loads(
                    subprocess.check_output(["ip", "-j", "link", "show", name])
                )[0]["ifalias"]
                subprocess.run(["ip", "link", "set", name, "down"], check=True)
                rejected(503)
                subprocess.run(["ip", "link", "set", name, "up"], check=True)
                subprocess.run(
                    ["ip", "link", "set", name, "alias", "foreign"], check=True
                )
                rejected(409)
                subprocess.run(
                    ["ip", "link", "set", name, "alias", original_alias], check=True
                )
            operations[label] = conn, name, remove, restore, full, remaining
            stop_calls[label] = conn, stop, stop_path
        orders = tuple(itertools.permutations(operations))
        for block in range(1, 5):
            for i in range(-6, 30):
                order = orders[(i + block) % len(orders)]
                elapsed = {
                    label: f.request(operations[label][0], *operations[label][2])[0]
                    for label in order
                }
                for label in order:
                    conn, name, _, restore, full, remaining = operations[label]
                    f.check_peers(remaining, name)
                    f.request(conn, *restore)
                    f.check_peers(full, name)
                    if i >= 0:
                        print(
                            f"{block},{label},remove_{population}_peers,{i + 1},{elapsed[label]}"
                        )
        for label, calls in stop_calls.items():
            f.request(*calls)
            f.check_link_removed(operations[label][1])


f.measure_peer_removal = compare
f.main()
