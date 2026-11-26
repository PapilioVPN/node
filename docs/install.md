# Install PapilioVPN Node

The [release page](https://github.com/PapilioVPN/node/releases/latest) has
a versioned installer and two Linux amd64 archives. The full archive
includes the installer, Docker image, and standalone binaries. The small
Compose bundle pulls the image from GHCR. A matching `.sha256` file is
available alongside each download.

## Install on a Linux server

Run the [one-line installer](../README.md#quick-start) over SSH with Bash,
`curl`, and root or sudo access. It uses a working local Docker installation
if available. On a fresh server it installs Docker, verifies the release
checksum, loads the image locally, and checks that Node is healthy.

Automatic Docker setup targets Ubuntu, Debian, Fedora, RHEL, CentOS Stream,
Rocky Linux, AlmaLinux, Oracle Linux, Amazon Linux, Arch Linux, and
openSUSE. Use a maintained release with systemd. On other distributions
you can use the installer with an existing local Docker Engine and
`docker compose` plugin. The Node artifact targets `amd64`.

The default destination is `/opt/papiliovpn-node`. The terminal summary
shows the config file, health URL, and command for live logs. The current
installation attempt is recorded in `install.log` with mode `0600`.
Set `NO_COLOR=1` for plain output. Use `--yes` for an unattended install or
update. A healthy installation of the same version is reused when the
installer is run without `--archive`.

Node starts on loopback ports 8080 for HTTP and 8081 for gRPC. Each engine
is enabled through `config/config.yml` and requires its own configuration.
Use `compose.override.yml` in the installation directory for local
Compose settings, and `--health-url` if the HTTP health address differs.

For an installation managed directly with Compose, extract the small
bundle and run `docker compose up -d` if you can pull from GHCR, or load
the full bundle's image using the manual procedure below.

## Offline Docker installation

On a server with Docker and Compose, place the full archive, checksum,
and versioned installer in the same directory. Install the local package
without downloading an image or connecting to GHCR.

```bash
sudo bash node-1.7.1-linux-amd64-install.sh --archive node-1.7.1-linux-amd64.tar.gz --yes
```

The same installer is in the archive as `node/install.sh`. For a manual
Compose installation from the full archive, use the following steps.

```bash
bundle=node-1.7.1-linux-amd64.tar.gz
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

## Enable OpenVPN

The image includes `/usr/sbin/openvpn`. For a native installation, install OpenVPN 2.6 before enabling the engine. Provide the server profile and its TLS files in the configuration mount.

To run OpenVPN with Docker Compose, add an override for the `node` service.

```yaml
services:
  node:
    user: "0:0"
    cap_add: [NET_ADMIN]
    devices: ["/dev/net/tun:/dev/net/tun"]
```

OpenVPN manages the tunnel in the container's network namespace. Select that namespace and the listening address deliberately. To enable managed revocations, mount a writable directory for the CRL. [OpenVPN management](openvpn.md)

## Enable Hysteria 2

Supply the pinned Linux amd64 Hysteria 2 executable, its server YAML, and
its TLS files. The [Hysteria 2 guide](../benchmarks/hysteria/README.md#enable-a-server)
shows the Node settings and management operations.

## Upgrade

Run the new version's installer against the same directory. It preserves
the existing `config/` directory and records the new release after its
health check passes. If startup fails, it attempts to restore the
previous Compose deployment. For a manual Compose installation, extract
the new bundle and keep the existing `config/` directory and state mounts.
