<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="zh-CN">在 Linux 上管理 Xray、WireGuard、OpenVPN 和 Hysteria 2。通过 HTTP 和 gRPC 控制运行实例，查看流量、实时日志和主机指标。</p>

<p align="center">
  <a href="README.md" lang="en"><img src="assets/flags/gb.svg" width="24" alt=""> English</a> ·
  <a href="README.fa.md" lang="fa"><img src="assets/flags/ir-lion-and-sun.svg" width="24" alt=""> فارسی</a> ·
  <a href="README.ru.md" lang="ru"><img src="assets/flags/ru.svg" width="24" alt=""> Русский</a> ·
  <strong lang="zh-CN"><img src="assets/flags/cn.svg" width="24" alt=""> 简体中文</strong>
</p>

<p align="center">
  <a href="https://github.com/PapilioVPN/node/actions/workflows/check.yml"><img alt="Checks" src="https://github.com/PapilioVPN/node/actions/workflows/check.yml/badge.svg?branch=main"></a>
</p>

<p align="center">
  <a href="#quick-start">快速开始</a> ·
  <a href="#capabilities">功能</a> ·
  <a href="#management">管理 API</a> ·
  <a href="#benchmarks">基准测试</a> ·
  <a href="#development">开发</a>
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

<a name="quick-start"></a>

## 快速开始

通过 SSH 在 Linux amd64 服务器上运行此命令。安装程序按需安装 Docker 和 Compose，校验发布包并启动 Node。

```bash
(f=$(mktemp) && trap 'rm -f "$f"' EXIT && curl -fsSL https://github.com/PapilioVPN/node/releases/download/v1.7.1/node-1.7.1-linux-amd64-install.sh -o "$f" && if [ "$(id -u)" -eq 0 ]; then bash "$f"; else sudo bash "$f"; fi)
```

<details>
<summary>快速无人值守安装</summary>

```bash
(f=$(mktemp) && trap 'rm -f "$f"' EXIT && curl -fsSL https://github.com/PapilioVPN/node/releases/download/v1.7.1/node-1.7.1-linux-amd64-install.sh -o "$f" && if [ "$(id -u)" -eq 0 ]; then bash "$f" --yes; else sudo bash "$f" --yes; fi)
```

</details>

管理 API 默认仅监听回环地址。安装程序会显示健康检查地址，并将最近一次安装过程写入 `/opt/papiliovpn-node/install.log`。离线安装和引擎配置见[安装指南](docs/install.md)。

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## 功能

| 引擎或主机 | Node 功能 |
| --- | --- |
| **主机** | 运行时间、CPU、内存、存储和网络吞吐量。 |
| **Xray** | 生命周期管理、配置验证、账户同步和恢复、流量和在线统计、路由、日志和出站健康状态。 |
| **WireGuard** | Linux 接口生命周期、管理租约与修复、配置验证和多项设置在线更新、对等端管理及通过 HTTP 和 gRPC 分块提交变更、活动监测、运行统计与日志、出站探测，以及可选的 IPv4 转发、NAT 和策略路由。 |
| [**OpenVPN**](docs/openvpn.md) | 通过 HTTP 和 gRPC 管理 Linux 服务器，验证配置，查看会话与计数器、跟踪日志、断开会话并发布签名吊销列表。 |
| [**Hysteria 2**](benchmarks/hysteria/README.md) | 通过 HTTP 和 gRPC 管理 Linux 服务器，支持已确认的配置摘要、启动前检查、脱敏实时日志、运行和按 ID 统计流量、实例级计数器重置、在线设备计数与原生断开请求。 |

<a name="management"></a>

[HTTP 和 gRPC 参考](docs/management.zh-CN.md)

<a name="benchmarks"></a>

## 基准测试

<!-- benchmark-summary -->
**PapilioVPN 在 102 个可比的 Xray 场景中的 82 个领先**。13 个场景的结果各有优劣，PasarGuard 0.5.7 在 7 个场景领先。汇总还列出 16 个使用同一测试环境、但输入、输出字段或保证不同的场景。
<!-- /benchmark-summary -->

[基准测试汇总、分组报告和原始数据](benchmarks/xray/README.md) · [WireGuard 接口基准测试](benchmarks/wireguard/interfaces/README.md) · [Hysteria 2 接口约定与基准测试](benchmarks/hysteria/README.md)

<a name="development"></a>

## 开发

本地检查和提交 Pull Request 的流程见[贡献指南](CONTRIBUTING.md)。

## 许可证

PapilioVPN Node 采用 [GNU 通用公共许可证第 3 版](LICENSE)。

<sub>“WireGuard” and the “WireGuard” logo are registered trademarks of Jason A. Donenfeld. OpenVPN® is a registered trademark of OpenVPN Inc. <a href="assets/engines/README.md">标志来源</a></sub>
