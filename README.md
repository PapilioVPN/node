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
  <a href="#benchmarks">Benchmarks</a> ·
  <a href="#development">Development</a>
</p>

## Quick start

On Linux amd64 with Docker Compose, download the versioned setup bundle and start Node.

```bash
(
  set -eu
  version=1.2.0
  bundle="node-$version-linux-amd64-compose.tar.gz"
  release="https://github.com/PapilioVPN/node/releases/download/v$version"
  curl -fLO "$release/$bundle" -fLO "$release/$bundle.sha256"
  sha256sum -c "$bundle.sha256"
  tar -xzf "$bundle"
  cd node
  mkdir -p config
  test -e config/config.yml || cp config.example.yml config/config.yml
  docker compose up -d
)
```

The management API binds to loopback by default. Check it with `curl -fsS http://127.0.0.1:8080/healthz`. For native, offline, and Xray setup, see the [installation guide](docs/install.md).

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## Capabilities

- **Host** — uptime, CPU, memory, and storage.
- **Xray** — lifecycle, config validation, live account sync and recovery, traffic and online stats, routing, logs, and outbound health.
- **WireGuard** — Linux interface lifecycle and recovery, configuration validation, live MTU updates, full and targeted peer changes, and live peer statistics.

<a name="management"></a>

[HTTP and gRPC reference](docs/management.md)

<a name="benchmarks"></a>

## Benchmarks

<!-- benchmark-summary -->
**PapilioVPN led in 82 of 102 matched Xray workloads** against PasarGuard 0.5.7. Results were mixed in 13 workloads, and PasarGuard led in 7. The dashboard also covers 16 shared-fixture workloads with different inputs, output fields, or guarantees.
<!-- /benchmark-summary -->

[Benchmark dashboard, group reports, and raw data](benchmarks/xray/README.md) · [WireGuard interface benchmark](benchmarks/wireguard/interfaces/README.md)

<a name="development"></a>

## Development

See the [contribution guide](CONTRIBUTING.md) for local checks and pull requests.

## License

PapilioVPN Node is licensed under the [GNU General Public License version 3](LICENSE).
