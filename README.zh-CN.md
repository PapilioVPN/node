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

从成功的[检查工作流](https://github.com/PapilioVPN/node/actions/workflows/check.yml)下载 Linux amd64 构件。在解压后的构件目录中校验归档文件，然后使用示例配置启动 Node。

```bash
archive=node-0.0.0-linux-amd64-YOUR_COMMIT_SHA.tar.gz
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
cp -n node/config.example.yml config.yml
./node/node -config config.yml
```

如需通过 Docker 运行同一构建，请加载随包提供的镜像并挂载配置文件。

```bash
docker load -i node/image.tar
docker run --rm --network=host --read-only --cap-drop ALL \
  --mount "type=bind,source=$PWD/config.yml,target=/config.yml,readonly" \
  "$(cat node/image-name)"
```

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## 功能

| 模块 | 可用操作 |
| --- | --- |
| Node 就绪状态 | 通过 HTTP 和 gRPC 检查 Node 管理接口是否就绪。 |
| 主机运行时间 | 通过 HTTP 和 gRPC 以整秒读取自 Linux 启动以来的时间。 |
| 主机内存 | 通过 HTTP 和 gRPC 读取总内存、可用内存、交换空间总量和空闲量，单位为字节。 |
| 主机 CPU | 通过 HTTP 和 gRPC 读取 CPU 总体使用率。 |
| 主机存储 | 查看配置路径所在文件系统的容量和只读状态。 |
| 主机概览 | 在一次响应中获取运行时间、CPU、内存和存储信息。 |
| Xray 实例 | 通过 HTTP 和 gRPC 启动、查询状态和停止一个已配置的实例。 |
| Xray 配置验证 | 通过 HTTP 和 gRPC 在重启实例之前验证已配置的 JSON。 |

<a name="management"></a>

## 管理 API

HTTP `GET /v1/host/uptime` 返回 `uptime_seconds`。gRPC `HostService.GetUptime` 按照[主机 API 契约](internal/host/bridge/grpc/proto/v1/host.proto)返回相同的值。两者均读取 Node 所在 Linux 时间命名空间可见的启动时钟。

HTTP `GET /v1/host/memory` 和 gRPC `HostService.GetMemory` 读取 Node 可见的 Linux `/proc/meminfo`。所有数值均以字节为单位。`available_bytes` 是内核估算的无需使用交换空间即可使用的内存，只在内核提供该值时返回。

HTTP `GET /v1/host/cpu` 和 gRPC `HostService.GetCPU` 返回同一份 Linux CPU 总体采样，约每秒更新一次。响应包含 `busy_percent`、`window_milliseconds`、`observed_at`，以及 Linux 提供的可选字段 `steal_percent`。I/O 等待时间和被虚拟机监控程序占用的时间均不计入忙碌时间。尚无有效采样时，HTTP 返回 `503`，gRPC 返回 `UNAVAILABLE`。

HTTP `GET /v1/host/storage` 和 gRPC `HostService.GetStorage` 返回 `path`、`total_bytes`、`used_bytes`、`available_bytes` 和 `read_only`。`available_bytes` 不包含为特权用户保留的空间。通过 `host.storage_path` 指定要监控的存储卷上的绝对路径，默认使用 Node 挂载命名空间中的 `/`。在 Docker 中，请填写容器内可见的路径。无法读取该路径时，HTTP 返回 `503`，gRPC 返回 `UNAVAILABLE`。

HTTP `GET /v1/host/stats` 和 gRPC `HostService.GetSystemStats` 返回 `uptime_seconds`、`cpu`、`memory` 和 `storage`，数据和单位与各独立操作一致。不可用的字段会省略，`cpu` 会在首次有效采样后返回。

参照[配置示例](deploy/config.example.yml)设置 `xray.id`、`xray.executable` 和 `xray.config_file`，即可启用一个实例。请使用 Xray v26.11.9 和由运维人员控制的 JSON 文件。启动时，Node 将其中的 `api` 配置替换为用于就绪检查的私有 Unix 套接字。在 Docker 中挂载可执行文件和 JSON 配置，并提供可写目录，如使用 `--tmpfs /tmp`。远程管理 Xray 需要 TLS 与 API 密钥或客户端 CA。

使用 `GET /v1/xray/instances/{id}` 读取状态。使用空请求体的 `PUT` 和 `DELETE /v1/xray/instances/{id}/execution` 启动和停止实例。[gRPC 接口](internal/engines/xray/instances/bridge/grpc/proto/v1/instances.proto)提供 `XrayInstancesService.Start`、`GetStatus` 和 `Stop`。启动会等待 Xray 原生 API 响应，停止会等待进程退出。

发送空请求体的 `POST /v1/xray/instances/{id}/config-validations`，或使用实例 ID 调用 `XrayConfigsService.ValidateConfig`。Node 使用与启动时相同的选项验证配置文件。无效配置返回 `valid: false` 和问题代码，并在 Xray 提供时返回行号和列号。验证执行失败时返回 API 错误。[gRPC 接口](internal/engines/xray/configs/bridge/grpc/proto/v1/configs.proto)

示例中的两个监听端口都绑定到环回地址。Node 就绪时，HTTP `GET /healthz` 返回 `204 No Content`。端口 8081 上的 gRPC 健康检查对空服务名返回 `SERVING`。设置 `management.tls.cert_file` 和 `management.tls.key_file` 可启用 TLS。可选项 `management.tls.client_ca_file` 启用 mTLS，`management.api_key` 用于 `x-api-key` 请求头或元数据。

<a name="benchmarks"></a>

## 基准测试

[基准测试报告](benchmarks/README.md)包含 Host 和 Xray 与 PasarGuard 0.5.7 的对比、每轮测试结果和复现步骤。

<a name="development"></a>

## 开发

使用 Go 1.26.11 或更高版本运行检查。打包还需要 Docker 和 curl。

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```

修改 `host.proto` 后，在同一目录中重新生成 `host.pb.go`。将 `protoc` 36.2 和 `protoc-gen-go` v1.36.12 加入 `PATH`，然后在仓库根目录执行以下命令。

```bash
go generate ./internal/host/bridge/grpc
```
