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

从 [GitHub Releases](https://github.com/PapilioVPN/node/releases/latest) 下载 Linux amd64 归档及其校验文件。校验并解压归档后，使用示例配置启动 Node。

```bash
archive=node-1.0.0-linux-amd64-YOUR_COMMIT_SHA.tar.gz
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
| Xray 实例 | 通过 HTTP 和 gRPC 启动、停止以及查询状态和版本。两种接口均可提交带类型的初始账户。启动支持保活、在线应用兼容配置变更，以及重启恢复。 |
| Xray 配置验证 | 通过 HTTP 和 gRPC 在重启实例之前验证已配置的 JSON。 |
| Xray 运行统计 | 通过 HTTP 和 gRPC 查询活动实例的运行时间、内存分配、goroutine 和垃圾回收计数。 |
| Xray 流量计数器 | 通过 HTTP 和 gRPC 读取单个或全部账户、入站标签或出站标签的上行与下行字节数。 |
| Xray 流量清零 | 显式启用后，通过 HTTP 和 gRPC 清零单个计数器、指定名称的两个方向或有数量上限的范围。 |
| Xray 在线账户和 IP | 通过 HTTP 和 gRPC 查询在线账户、不同来源 IP 数量及其最近出现时间。 |
| Xray 路由 | 通过 HTTP 和 gRPC 读取与修改规则、测试路由，并查询或覆盖负载均衡目标。 |
| Xray 账户 | 通过 HTTP 和 gRPC 在选定入站上按账户、完整列表或分块增量同步，并在受管重启时恢复已确认的账户。启用持久化后，重启 Node 也可以恢复账户。同步会跳过启动时在 `exclude_inbounds` 中列出的入站。支持 VLESS、VMess、Trojan、Shadowsocks、兼容的 Shadowsocks 2022 服务器、Hysteria、MASQUE 和 Xray 内的 WireGuard。VLESS 还支持直接添加、删除和计数。 |
| Xray 日志流 | 通过 HTTP 和 gRPC 持续接收运行实例的 stdout 和 stderr。 |
| Xray 出站健康状态 | 通过 HTTP 和 gRPC 读取全部 Observatory 观测结果，或按出站标签查询。 |

<a name="management"></a>

## 管理 API

HTTP `GET /v1/host/uptime` 返回 `uptime_seconds`。gRPC `HostService.GetUptime` 按照[主机 API 契约](internal/host/bridge/grpc/proto/v1/host.proto)返回相同的值。两者均读取 Node 所在 Linux 时间命名空间可见的启动时钟。

HTTP `GET /v1/host/memory` 和 gRPC `HostService.GetMemory` 读取 Node 可见的 Linux `/proc/meminfo`。所有数值均以字节为单位。`available_bytes` 是内核估算的无需使用交换空间即可使用的内存，只在内核提供该值时返回。

HTTP `GET /v1/host/cpu` 和 gRPC `HostService.GetCPU` 返回同一份 Linux CPU 总体采样，约每秒更新一次。响应包含 `busy_percent`、`window_milliseconds`、`observed_at`，以及 Linux 提供的可选字段 `steal_percent`。I/O 等待时间和被虚拟机监控程序占用的时间均不计入忙碌时间。尚无有效采样时，HTTP 返回 `503`，gRPC 返回 `UNAVAILABLE`。

HTTP `GET /v1/host/storage` 和 gRPC `HostService.GetStorage` 返回 `path`、`total_bytes`、`used_bytes`、`available_bytes` 和 `read_only`。`available_bytes` 不包含为特权用户保留的空间。通过 `host.storage_path` 指定要监控的存储卷上的绝对路径，默认使用 Node 挂载命名空间中的 `/`。在 Docker 中，请填写容器内可见的路径。无法读取该路径时，HTTP 返回 `503`，gRPC 返回 `UNAVAILABLE`。

HTTP `GET /v1/host/stats` 和 gRPC `HostService.GetSystemStats` 返回 `uptime_seconds`、`cpu`、`memory` 和 `storage`，数据和单位与各独立操作一致。不可用的字段会省略，`cpu` 会在首次有效采样后返回。

参照[配置示例](deploy/config.example.yml)设置 `xray.id`、`xray.executable` 和 `xray.config_file`，即可启用一个实例。请使用 Xray v26.11.9 和由运维人员控制的 JSON 文件。启动时，Node 将其中的 `api` 配置替换为用于就绪检查的私有 Unix 套接字。在 Docker 中挂载可执行文件和 JSON 配置，并提供可写目录，如使用 `--tmpfs /tmp`。远程管理 Xray 需要 TLS 与 API 密钥或客户端 CA。

使用 `GET /v1/xray/instances/{id}` 读取状态。使用空请求体的 `PUT` 和 `DELETE /v1/xray/instances/{id}/execution` 启动和停止实例。[gRPC 接口](internal/engines/xray/instances/bridge/grpc/proto/v1/instances.proto)提供 `XrayInstancesService.Start`、`GetStatus` 和 `Stop`。启动会等待 Xray 原生 API 响应，停止会等待进程退出。

启动失败时，HTTP 返回 `503` 和 JSON `reason` 字段，gRPC 返回 `UNAVAILABLE` 和 `XrayStartFailure` 详情。原因包括 `launch_failed`、`exited_before_ready`、`readiness_timeout` 和 `readiness_failed`。如果 Xray 在就绪前退出并返回退出码，响应会包含 `exit_code`。

发送空请求体的 `POST /v1/xray/instances/{id}/config-validations`，或使用实例 ID 调用 `XrayConfigsService.ValidateConfig`。Node 使用与启动时相同的选项验证配置文件。无效配置返回 `valid: false` 和问题代码，并在 Xray 提供时返回行号和列号。验证执行失败时返回 API 错误。[gRPC 接口](internal/engines/xray/configs/bridge/grpc/proto/v1/configs.proto)

使用 `GET /v1/xray/instances/{id}/stats/runtime` 或 `XrayStatsService.GetRuntimeStats`。每个响应都使用正在运行的 Xray 实例的新样本。并发请求可以共享下一次采样结果。内存以字节计，运行时间以 Xray 统计服务启动后的整秒计，GC 累计暂停以纳秒计。实例不可用时，HTTP 返回 `503`，gRPC 返回 `UNAVAILABLE`。等待读取的缓冲区可容纳 128 个请求。缓冲区满时，HTTP 返回 `429`，gRPC 返回 `RESOURCE_EXHAUSTED`。[gRPC 接口](internal/engines/xray/stats/bridge/grpc/proto/v1/runtime_stats.proto) · [基准测试和原始结果](benchmarks/xray/runtime/README.md#xray-runtime-statistics-after-the-rewrite)

使用 `GET /v1/xray/instances/{id}/stats/traffic/{scope}`，范围为 `user`、`inbound` 或 `outbound`。添加 `?name=...` 可读取单个账户或标签。gRPC 方法为 `XrayStatsService.GetTraffic` 和 `ListTraffic`。读取时保留原计数值。缺少某个方向时省略对应字段，零值仍返回零。找不到指定名称时，HTTP 返回 `404`，gRPC 返回 `NOT_FOUND`。列表顺序不固定。请在 Xray 策略中启用相应范围的流量统计。[gRPC 接口](internal/engines/xray/stats/bridge/grpc/proto/v1/traffic.proto)

设置 `xray.reset_engine_stats: true` 可通过管理 API 清零计数器。使用 `POST /v1/xray/instances/{id}/stats/traffic/{scope}/resets?name=...&direction=uplink` 或 `XrayStatsService.ResetTrafficCounter`。另一个方向使用 `downlink`。Xray 以原子方式返回原字节数并清零所选计数器。结果未知或响应丢失时，应先核对状态再重试。

使用 `POST /v1/xray/instances/{id}/stats/traffic/{scope}/pair-resets?name=...` 或 `XrayStatsService.ResetTrafficPair` 清零两个方向。Xray 分别清零每个方向。即使操作中断，HTTP 也在响应体中返回两个方向的结果。gRPC 通过状态详情返回部分结果。只有已确认的方向会返回原字节数。

使用 `POST /v1/xray/instances/{id}/stats/traffic/{scope}/scope-resets?max_counters=...` 或 `XrayStatsService.ResetTrafficScope` 清零一个范围。将上限设为 1 到 1000 个方向计数器。选中的计数器超过上限时，请求会在首次清零之前被拒绝。请检查整体结果和每个计数器的结果。gRPC 在响应体中返回中断或结果未知的报告。 范围清零默认顺序执行。将 `xray.reset_scope_concurrency` 设为 2 到 8 可启用并行清零。[接口约定与基准测试](benchmarks/xray/traffic/scope-reset.md)

如需单次调用清零，请使用包内的 `/node-xray` 并设置 `xray.native_scope_reset: true`。标准 Xray 仍支持顺序或按配置并行执行。

使用 `GET /v1/xray/instances/{id}/stats/online/count?name=...` 查询数量，或在同一实例路径下使用 `/stats/online/ips?name=...` 查询列表。gRPC 方法为 `XrayStatsService.GetOnlineIPCount` 和 `ListOnlineIPs`。请在账户的策略级别启用 `statsUserOnline`。时间戳为 Unix 秒。跟踪记录为空时返回零或空映射。缺少观察记录时，HTTP 返回 `404`，gRPC 返回 `NOT_FOUND`。 [gRPC 接口](internal/engines/xray/stats/bridge/grpc/proto/v1/online.proto)

使用 `GET /v1/xray/instances/{id}/stats/online/users` 或 `XrayStatsService.ListOnlineUsers` 查询 Xray 当前至少跟踪一个来源 IP 的账户名称。列表顺序不固定。没有在线账户时，HTTP 返回 `{"names":[]}`。

使用 `GET /v1/xray/instances/{id}/routing/rules` 和 `GET /v1/xray/instances/{id}/routing/balancer?tag=...` 读取路由状态。gRPC 方法为 `XrayRoutingService.ListRoutingRules` 和 `GetBalancerInfo`。响应分别返回覆盖目标和 Xray 报告的目标。 [gRPC 接口](internal/engines/xray/routing/bridge/grpc/proto/v1/routing.proto)

使用 `XrayRoutingService.TestRoute`、`AddRoutingRule`、`RemoveRoutingRule` 和 `OverrideBalancerTarget` 评估或修改当前路由。报告记录了 HTTP 路径和替换规则所需的确认。 [路由控制](benchmarks/xray/routing/rule-writes.md)

使用 `XrayAccountsService.AddVLESSAccount`、`RemoveVLESSAccount` 和 `CountVLESSAccounts`，或使用 `/v1/xray/instances/{id}/inbounds/{tag}/accounts` 下的 HTTP 路径。操作作用于当前运行的实例。 [VLESS 接口](benchmarks/xray/accounts/vless.md)

[VLESS 同步](benchmarks/xray/accounts/sync.md) · [Xray 日志流](benchmarks/xray/logs/contract.md)

要在重启 Node 后保留已确认的同步结果，请将 `xray.state_directory` 指向 Node 用户拥有且权限为 `0700` 的现有目录。使用 Docker 时，请将其挂载为可写的持久存储。Node 会在确认同步前保存账户状态，并在下一次 `Start` 报告就绪之前恢复账户。重启时请保留实例 ID 和兼容的入站配置。

使用 `GET /v1/xray/instances/{id}/outbounds/health` 或 `XrayOutboundsService.GetOutboundHealth` 读取普通 Observatory 观测结果。提供标签可查询单个出站。响应区分健康、探测失败和缺少样本。 [gRPC 接口](internal/engines/xray/outbounds/bridge/grpc/proto/v1/outbounds.proto)

示例中的两个监听端口都绑定到环回地址。Node 就绪时，HTTP `GET /healthz` 返回 `204 No Content`。端口 8081 上的 gRPC 健康检查对空服务名返回 `SERVING`。设置 `management.tls.cert_file` 和 `management.tls.key_file` 可启用 TLS。可选项 `management.tls.client_ca_file` 启用 mTLS，`management.api_key` 用于 `x-api-key` 请求头或元数据。

<a name="benchmarks"></a>

## 基准测试

<!-- benchmark-summary -->
**PapilioVPN 在 102 个可比的 Xray 场景中的 82 个领先**。13 个场景的结果各有优劣，PasarGuard 0.5.7 在 7 个场景领先。汇总还列出 16 个使用同一测试环境、但输入、输出字段或保证不同的场景。
<!-- /benchmark-summary -->

![按组划分的基准测试结果](benchmarks/xray/charts/outcomes.png)

[基准测试汇总、分组报告和原始数据](benchmarks/xray/README.md)

<a name="development"></a>

## 开发

有关提交 Pull Request 和验证改动的说明，请参阅[贡献指南](CONTRIBUTING.md)。

使用 Go 1.27.4 或更高版本运行检查。打包还需要 Docker 和 curl。

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```

修改 `host.proto` 后，在同一目录中重新生成 `host.pb.go`。将 `protoc` 36.2 和 `protoc-gen-go` v1.36.12 加入 `PATH`，然后在仓库根目录执行以下命令。

```bash
go generate ./internal/host/bridge/grpc
```

## 许可证

PapilioVPN Node 采用 [GNU 通用公共许可证第 3 版](LICENSE)。
