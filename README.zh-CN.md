<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="zh-CN">独立的 Go 服务，用于观测 Linux 主机并管理网络引擎，首先支持 Xray。</p>

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

<a name="quick-start"></a>

## 快速开始

在已安装 Docker Compose 的 Linux amd64 上，下载指定版本的安装包并启动 Node。

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

管理 API 默认仅监听回环地址。可使用 `curl -fsS http://127.0.0.1:8080/healthz` 检查。二进制安装、离线安装和 Xray 配置见[安装指南](docs/install.md)。

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## 功能

- **主机** — 运行时间、CPU、内存和存储。
- **Xray** — 生命周期管理、配置验证、账户同步和恢复、流量和在线统计、路由、日志和出站健康状态。
- **WireGuard** — 查询 Linux 接口状态，验证拟提交配置，以及启动和停止已配置的内核接口。

<a name="management"></a>

[HTTP 和 gRPC 参考](docs/management.zh-CN.md)

<a name="benchmarks"></a>

## 基准测试

<!-- benchmark-summary -->
**PapilioVPN 在 102 个可比的 Xray 场景中的 82 个领先**。13 个场景的结果各有优劣，PasarGuard 0.5.7 在 7 个场景领先。汇总还列出 16 个使用同一测试环境、但输入、输出字段或保证不同的场景。
<!-- /benchmark-summary -->

[基准测试汇总、分组报告和原始数据](benchmarks/xray/README.md) · [WireGuard 接口基准测试](benchmarks/wireguard/interfaces/README.md)

<a name="development"></a>

## 开发

本地检查和提交 Pull Request 的流程见[贡献指南](CONTRIBUTING.md)。

## 许可证

PapilioVPN Node 采用 [GNU 通用公共许可证第 3 版](LICENSE)。
