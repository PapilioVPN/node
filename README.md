<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="en">Manage Xray, WireGuard, OpenVPN, and Hysteria 2 on Linux. Control engine executions and check traffic, live logs, and host metrics over HTTP and gRPC.</p>

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

<p align="center">
  <a href="https://xtls.github.io/en/"><img src="assets/engines/xray.png" height="34" alt="Xray"></a>
  &nbsp;&nbsp;
  <a href="https://www.wireguard.com/"><img src="assets/engines/wireguard.svg" width="128" alt="WireGuard"></a>
  &nbsp;&nbsp;
  <a href="https://openvpn.net/community-docs/openvpn-project/"><img src="assets/engines/openvpn.png" height="34" alt="OpenVPN"></a>
  &nbsp;&nbsp;
  <a href="https://hysteria.network/"><img src="assets/engines/hysteria.svg" height="34" alt="Hysteria 2"></a>
</p>

## Quick start

On a Linux amd64 server, copy this command into an SSH terminal. The installer sets up Docker and Compose when needed, checks the release archive, and starts Node.

```bash
(f=$(mktemp) && trap 'rm -f "$f"' EXIT && curl -fsSL https://github.com/PapilioVPN/node/releases/download/v1.7.1/node-1.7.1-linux-amd64-install.sh -o "$f" && if [ "$(id -u)" -eq 0 ]; then bash "$f"; else sudo bash "$f"; fi)
```

<details>
<summary>Quick install without prompts</summary>

```bash
(f=$(mktemp) && trap 'rm -f "$f"' EXIT && curl -fsSL https://github.com/PapilioVPN/node/releases/download/v1.7.1/node-1.7.1-linux-amd64-install.sh -o "$f" && if [ "$(id -u)" -eq 0 ]; then bash "$f" --yes; else sudo bash "$f" --yes; fi)
```

</details>

The API listens on loopback by default. The installer prints the health URL and records its latest run in `/opt/papiliovpn-node/install.log`. For offline installation and engine setup, see the [installation guide](docs/install.md).

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## Capabilities

| Engine or host | What Node manages |
| --- | --- |
| **Host** | uptime, CPU, memory, storage, and network throughput. |
| **Xray** | lifecycle, config validation, live account sync and recovery, traffic and online stats, routing, logs, and outbound health. |
| **WireGuard** | Linux interface lifecycle with management leases and repair, config validation and multi-setting live updates, peer management and chunked changes over HTTP and gRPC, activity, runtime stats and logs, egress checks, and optional IPv4 forwarding, NAT and policy routing. |
| [**OpenVPN**](docs/openvpn.md) | Manage a Linux server, validate its configuration, inspect sessions and counters, follow logs, disconnect sessions, and publish signed revocation lists over HTTP and gRPC. |
| [**Hysteria 2**](benchmarks/hysteria/README.md) | Manage Linux servers over HTTP and gRPC with a confirmed config digest, preflight checks, redacted live logs, runtime and per-ID traffic stats, instance-wide resets, online device counts, and native disconnect requests. |

<a name="management"></a>

[HTTP and gRPC reference](docs/management.md)

<a name="benchmarks"></a>

## Benchmarks

<!-- benchmark-summary -->
**PapilioVPN led in 82 of 102 matched Xray workloads** against PasarGuard 0.5.7. Results were mixed in 13 workloads, and PasarGuard led in 7. The dashboard also covers 16 shared-fixture workloads with different inputs, output fields, or guarantees.
<!-- /benchmark-summary -->

[Benchmark dashboard, group reports, and raw data](benchmarks/xray/README.md) · [WireGuard interface benchmark](benchmarks/wireguard/interfaces/README.md) · [Hysteria 2 contract and benchmark](benchmarks/hysteria/README.md)

<a name="development"></a>

## Development

See the [contribution guide](CONTRIBUTING.md) for local checks and pull requests.

## License

PapilioVPN Node is licensed under the [GNU General Public License version 3](LICENSE).

<sub>“WireGuard” and the “WireGuard” logo are registered trademarks of Jason A. Donenfeld. OpenVPN® is a registered trademark of OpenVPN Inc. <a href="assets/engines/README.md">Upstream artwork</a></sub>
