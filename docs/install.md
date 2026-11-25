# Install PapilioVPN Node

The [release page](https://github.com/PapilioVPN/node/releases/latest) has
two Linux amd64 archives. The small Compose bundle pulls the image from
GHCR. The full archive includes the Docker image and standalone binaries.
Download the matching `.sha256` file alongside either archive.

## Docker Compose

Follow the [quick start](../README.md#quick-start) with the small bundle.
The bundle pins the image version in `.env`. Node runs without Linux
capabilities, with a read-only root filesystem, and listens on loopback
ports 8080 for HTTP and 8081 for gRPC by default.

Use `docker compose logs node` to see logs and `docker compose down` to
stop Node.

## Offline Docker installation

Use the full archive when you cannot pull from GHCR. This example uses
version 1.3.0.

```bash
bundle=node-1.3.0-linux-amd64.tar.gz
sha256sum -c "$bundle.sha256" && tar -xzf "$bundle"
cd node
mkdir -p config
test -e config/config.yml || cp config.example.yml config/config.yml
docker load -i image.tar
NODE_IMAGE="$(cat image-name)" docker compose up -d --pull never
curl -fsS http://127.0.0.1:8080/healthz
```

## Native installation

After verifying and extracting the full archive, start the included binary
from the `node` directory.

```bash
test -e config.yml || cp config.example.yml config.yml
./node -config config.yml
```

## Enable Xray

For Compose, put your Xray JSON in `config/xray.json` and uncomment the
`xray` block in `config/config.yml`. The example uses `/node-xray` and
Compose supplies a writable `/tmp` for the private Xray socket. For a
native installation, set `executable: "./node-xray"` and put
`xray.json` beside `config.yml`.

Restart Node after changing its settings. For the default instance and
local HTTP address, start Xray with this command.

```bash
curl -fsS -X PUT http://127.0.0.1:8080/v1/xray/instances/primary/execution
```

Keep the sample loopback listeners for local management. Configure TLS
with an API key or a client CA before exposing Xray management remotely.
To enable `xray.state_directory` in Docker, mount a writable persistent
directory there owned by UID 65532 with mode `0700`. See the
[management reference](management.md#management) for the API contracts.

## Enable managed WireGuard routing

The Node image includes `nft` and `wg`. A native installation needs these
tools available when using routing or inspecting the interface with `wg`.
Managed WireGuard requires `CAP_NET_ADMIN` in the network namespace that
owns the link. Use an explicitly configured namespace and a runtime
identity with that capability. Configure forwarding in that namespace
through the container's `net.ipv4.ip_forward=1` sysctl or through the
explicit managed routing option where the sysctl is writable. The quick
start Compose profile keeps all capabilities dropped. See the
[management reference](management.md) for the routing settings.

## Upgrade

Extract the new bundle and keep the existing `config/` directory. Check
any release notes first, then run `docker compose up -d` with the new
bundle. For an offline upgrade, load the new image and use its
`image-name` as shown above. Keep persisted state in its existing mount.
