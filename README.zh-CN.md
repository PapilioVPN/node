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
  <a href="#development">开发</a>
</p>

<a name="quick-start"></a>

## 快速开始

运行包适用于 **Linux amd64**，需要 Docker 和 GNU coreutils。打开一次成功的
[Checks 运行](https://github.com/PapilioVPN/node/actions/workflows/check.yml)，
下载 `node-linux-amd64-<commit>` 构建产物并解压 GitHub 下载文件。在归档文件和
校验和文件所在目录中，将 `YOUR_FULL_COMMIT_SHA` 替换为该构建产物对应的完整提交 SHA。

```bash
revision=YOUR_FULL_COMMIT_SHA
archive="node-linux-amd64-$revision.tar.gz"
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
```

校验并解压成功后，安装包内提供的镜像，并为首次启动复制示例配置。

```bash
install_dir="$HOME/.local/share/papilio-node"
config_dir="$HOME/.config/papilio-node"
./node/install.sh "$install_dir"
install -d -m 700 "$config_dir"
test -f "$config_dir/config.yml" ||
  cp "$install_dir/deploy/config.example.yml" "$config_dir/config.yml"
```

启动之前，编辑 `config.yml` 并放置配置所需的证书。示例启用了三种通信方式。
如只使用 HTTP，请移除 `communications.grpc`、`communications.nats` 和
`communications.requests.nats`。示例中的 HTTP 监听地址为 `127.0.0.1:8443`。

```bash
"$install_dir/run.sh" "$config_dir" papilio-node --network=host --detach
docker logs papilio-node
```

使用 `docker stop --time 130 papilio-node` 停止示例容器。

启动脚本以只读方式挂载配置目录。要运行 Xray，请参照[示例配置](deploy/config.example.yml)
添加 `instances`，并以只读方式挂载所选的可执行文件和配置。每个实例应拥有独立的
私有可写工作目录，并可按需配置独立的私有持久化状态目录。要在 Docker 中采集宿主机
信息，请使用示例中明确列出的命名空间和挂载参数。

<a name="capabilities"></a>

## 功能

| 模块 | 可用操作 |
| --- | --- |
| [系统与 Node](https://github.com/PapilioVPN/node/tree/main/internal/apps/system) | 主机标识、运行时长、负载、CPU、内存和交换空间、进程、会话与登录记录、传感器、文件系统和设备 I/O、网络接口计数器，以及 Node 进程与资源诊断。 |
| [Xray 实例](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/instances) | 启动、停止和重启已配置的实例，查看状态并通过 Xray 原生 API 检查就绪情况。 |
| [Xray 配置](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/configs) | 验证和编译配置源，查看期望和已应用的修订版本，以及替换和应用配置。 |
| [Xray 入站](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/inbounds) | 列出处理器和标签，查看摘要及兼容性，从编译数据或配置源添加，按标签删除，以及审查并执行替换计划。 |
| [Xray 出站](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/outbounds) | 列出处理器，从编译数据或配置源添加，以及按标签删除。 |
| [Xray 账户](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/accounts) | 读取和统计运行中实例的账户，支持单个或限量批量增删，并为编译后的配置准备账户成员列表。 |
| [Xray 在线状态](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/online) | 查看在线人数、用户、记录以及单个用户的 IP 地址。 |
| [Xray 路由](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/network/routing) | 列出、添加和删除运行中的路由规则，测试路由选择，查看负载均衡器状态并覆盖其目标。 |
| [Xray 统计](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/statistics) | 读取引擎的原生运行统计。 |
| [Xray 流量](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/traffic) | 读取和重置单项计数器、收发计数对及选定的计数器组。 |
| [Xray 出站健康](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/outboundhealth) | 列出可观测的出站健康结果，也可按出站标签查询。 |
| [Xray 日志记录器](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/loggers) | 重新打开 Xray 日志记录器并查看操作结果。 |
| [Xray 凭据工具](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/tools/credentials) | 生成 UUID、X25519/WireGuard 和 ML-DSA-65/ML-KEM-768 密钥材料、VLESS 加密对、TLS 证书和 ECH 密钥集，并计算 TLS 证书哈希。私密材料仅在当前响应中返回。 |

在线操作作用于运行中的实例，配置操作决定 Xray 启动时使用的设置。
控制器负责维护应用用户数据和策略，Node 提供引擎的原生操作和配置管理。

<a name="management"></a>

## 管理 API

按控制器的需要启用通信方式。三种方式均提供同一套 Node 服务的版本化操作。

| 方式 | 查询 Node 运行时信息 | 信任机制 |
| --- | --- | --- |
| HTTP | `GET /v1/system/node/runtime` | Node 通过 mTLS 验证控制器证书。 |
| gRPC | `papilio.system.node.runtime.v1.NodeRuntimeService/GetNodeInfo` | Node 通过 mTLS 验证控制器证书。 |
| NATS | 向 `<prefix>.system.node.runtime.info.v1.get` 发送空 JSON 对象 | Broker 通过 mTLS 验证双方，并使用 ACL 限制主题访问。 |

NATS 请求使用配置中指定的主题前缀。请将 Xray 原生管理端点限制在 Node 本地，
使用数字形式的回环地址或 Unix 套接字。

在宿主机上使用受信任的控制器证书检查 HTTP 接口。请使用签发服务端证书的 CA，
以及该证书中列出的地址。

```bash
curl --fail --show-error --silent \
  --cacert node-ca.crt --cert controller.crt --key controller.key \
  https://127.0.0.1:8443/v1/system/node/runtime
```

<a name="development"></a>

## 开发

使用 [go.mod](https://github.com/PapilioVPN/node/blob/main/go.mod) 中声明的 Go 版本构建。运行生成的程序时，通过
`-config /path/to/config.yml` 指定配置文件。

```bash
go build -trimpath -o bin/node ./cmd/node
./bin/node -help
go test -race ./...
```

在 Linux amd64 上，使用锁定版本的 Xray 运行集成测试。

```bash
xray_test_binary="$(sh scripts/ci/prepare-xray.sh)" &&
  XRAY_TEST_BINARY="$xray_test_binary" go test -race -tags=integration ./...
```

[Checks](https://github.com/PapilioVPN/node/actions/workflows/check.yml) 验证源代码和运行包。合并新功能时，
接入并验证公开操作后，请同步更新四个 README 中相应的功能说明。
