<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="en">The standalone Go service for Linux-host observations and engine management, starting with Xray.</p>

<p align="center">
  <strong lang="en"><img src="assets/flags/gb.svg" width="24" alt=""> English</strong> ·
  <a href="README.fa.md" lang="fa"><img src="assets/flags/ir-lion-and-sun.svg" width="24" alt=""> فارسی</a> ·
  <a href="README.ru.md" lang="ru"><img src="assets/flags/ru.svg" width="24" alt=""> Русский</a> ·
  <a href="README.zh-CN.md" lang="zh-CN"><img src="assets/flags/cn.svg" width="24" alt=""> 简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/PapilioVPN/node/actions/workflows/check.yml"><img alt="Checks" src="https://github.com/PapilioVPN/node/actions/workflows/check.yml/badge.svg?branch=main"></a>
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#capabilities">Capabilities</a> ·
  <a href="#management">Management APIs</a> ·
  <a href="#development">Development</a>
</p>

## Quick start

The runtime bundle targets **Linux amd64** and requires Docker and GNU coreutils.
Download the `node-linux-amd64-<commit>` artifact for a successful
[Checks run](https://github.com/PapilioVPN/node/actions/workflows/check.yml)
and extract the GitHub artifact. In the directory containing the archive and
its checksum, replace `YOUR_FULL_COMMIT_SHA` with the artifact's commit SHA.

```bash
revision=YOUR_FULL_COMMIT_SHA
archive="node-linux-amd64-$revision.tar.gz"
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
```

After verification and extraction succeed, install the bundled image and copy
the example settings for first-time setup.

```bash
install_dir="$HOME/.local/share/papilio-node"
config_dir="$HOME/.config/papilio-node"
./node/install.sh "$install_dir"
install -d -m 700 "$config_dir"
test -f "$config_dir/config.yml" ||
  cp "$install_dir/deploy/config.example.yml" "$config_dir/config.yml"
```

Edit `config.yml` and supply the configured certificates before starting.
The sample enables all three transports. For HTTP-only use, remove
`communications.grpc`, `communications.nats`, and
`communications.requests.nats`. The sample HTTP listener binds to
`127.0.0.1:8443`.

```bash
"$install_dir/run.sh" "$config_dir" papilio-node --network=host --detach
docker logs papilio-node
```

Stop this example with `docker stop --time 150 papilio-node`.

The runner mounts the configuration directory read-only. For Xray, add
`instances` in [the example settings](deploy/config.example.yml) and mount
the selected binary and configuration read-only. Give each instance its own
private writable work directory and, if configured, a durable state directory
owned by the Node user with mode `0700`.
For host observations in Docker, use the explicit namespaces and mounts
described in the example settings.

<a name="current-capabilities"></a>

## Capabilities

| Area | Available operations |
| --- | --- |
| [System and Node](https://github.com/PapilioVPN/node/tree/main/internal/apps/system) | Host identity, uptime, load, CPU, memory and swap, processes, sessions and login records, sensors, filesystem and device I/O, interface counters, and Node process and resource diagnostics. |
| [Xray instances](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/instances) | Start, stop and restart configured instances, read status and check native readiness. |
| [Xray configuration](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/configs) | Validate and compile sources, inspect desired and applied revisions, and replace or apply configuration. |
| [Xray inbounds](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/inbounds) | List handlers and tags, inspect summaries and compatibility, add from compiled data or sources, remove by tag, and review and execute replacement plans. |
| [Xray outbounds](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/outbounds) | Read paginated handler snapshots, add from compiled data or sources, and remove by tag. Review replacement plans, apply supported live changes and retrieve their outcomes. Changes that need a restart use the configuration API. |
| [Xray accounts](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/accounts) | Count and look up live accounts, page through credential-free metadata, explicitly export native account records, and check inbound compatibility. Add or remove accounts individually or in bounded batches, and prepare membership in compiled configuration. |
| [Xray presence](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/online) | Read online counts, users, records and individual users’ IP addresses. |
| [Xray routing](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/network/routing) | List, add and remove live rules, test route selection, inspect balancers and override their target. |
| [Xray statistics](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/statistics) | Read native runtime statistics. |
| [Xray traffic](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/traffic) | Read and reset individual counters, upload/download pairs and selected groups of counters. |
| [Xray outbound health](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/outboundhealth) | List observable outbound health results and inspect individual outbounds by tag. |
| [Xray loggers](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/loggers) | Reopen Xray loggers and inspect the outcome. |
| [Xray credential tools](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/tools/credentials) | Generate UUIDs, X25519/WireGuard and ML-DSA-65/ML-KEM-768 key material, VLESS encryption pairs, TLS certificates and ECH key sets. Hash TLS certificates. Private artifacts are returned only in the current response. |

Live operations act on the running instance. Configuration operations manage
what Xray starts with. Keep application user records and policies in the
controller and use Node for native engine operations and configuration.

<a name="management"></a>

## Management APIs

Enable the transports your controller uses. They expose versioned operations
over the same capability services.

| Transport | Runtime information example | Trust |
| --- | --- | --- |
| HTTP | `GET /v1/system/node/runtime` | Node verifies the controller certificate using mTLS. |
| gRPC | `papilio.system.node.runtime.v1.NodeRuntimeService/GetNodeInfo` | Node verifies the controller certificate using mTLS. |
| NATS | `<prefix>.system.node.runtime.info.v1.get` with an empty JSON object | The broker authenticates both parties with mTLS and limits subject access through ACLs. |

Use the configured NATS prefix for requests. Keep the Xray native management
endpoint local to Node, using a numeric loopback address or a Unix socket.

From the host, verify the HTTP endpoint with a trusted controller certificate.
Use the CA that signed the server certificate and an address in that certificate.

```bash
curl --fail --show-error --silent \
  --cacert node-ca.crt --cert controller.crt --key controller.key \
  https://127.0.0.1:8443/v1/system/node/runtime
```

## Development

Build with the Go version declared in [go.mod](https://github.com/PapilioVPN/node/blob/main/go.mod). Pass
`-config /path/to/config.yml` when running the resulting binary.

```bash
go build -trimpath -o bin/node ./cmd/node
./bin/node -help
go test -race ./...
```

On Linux amd64, run the integration suite with the pinned Xray binary.

```bash
xray_test_binary="$(sh scripts/ci/prepare-xray.sh)" &&
  XRAY_TEST_BINARY="$xray_test_binary" go test -race -tags=integration ./...
```

[Checks](https://github.com/PapilioVPN/node/actions/workflows/check.yml) runs source and packaging verification.
When merging a capability, update the relevant row in all four READMEs after
its public operations have been wired and verified.
