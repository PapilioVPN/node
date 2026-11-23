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

Download the Linux amd64 artifact from a successful [Checks run](https://github.com/PapilioVPN/node/actions/workflows/check.yml). From the extracted artifact directory, check the archive and run Node with the example settings.

```bash
archive=node-0.0.0-linux-amd64-YOUR_COMMIT_SHA.tar.gz
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
cp -n node/config.example.yml config.yml
./node/node -config config.yml
```

To run the same build in Docker, load the bundled image and mount the settings file.

```bash
docker load -i node/image.tar
docker run --rm --network=host --read-only --cap-drop ALL \
  --mount "type=bind,source=$PWD/config.yml,target=/config.yml,readonly" \
  "$(cat node/image-name)"
```

<a name="current-capabilities"></a>

## Capabilities

| Area | Available operations |
| --- | --- |
| Node readiness | Check whether the Node management listeners are serving over HTTP and gRPC. |
| Host uptime | Read the elapsed time since Linux boot in whole seconds over HTTP and gRPC. |
| Host memory | Read total and available RAM, plus total and free swap, in bytes over HTTP and gRPC. |
| Host CPU | Read aggregate CPU utilization over HTTP and gRPC. |
| Host storage | Check space and read-only status for the filesystem backing a configured path. |
| Host system stats | Fetch uptime, CPU, memory and storage in one response. |

<a name="management"></a>

## Management APIs

HTTP `GET /v1/host/uptime` returns `uptime_seconds`. gRPC `HostService.GetUptime` returns the same quantity using the [Host API contract](api/v1/host.proto). Both read the boot clock visible to Node's Linux time namespace.

HTTP `GET /v1/host/memory` and gRPC `HostService.GetMemory` read the Linux `/proc/meminfo` visible to Node. Counts are in bytes. `available_bytes` is the kernel's estimate of RAM available without swapping and is present when the kernel reports it.

HTTP `GET /v1/host/cpu` and gRPC `HostService.GetCPU` serve one shared aggregate Linux CPU sample, updated about once per second. It includes `busy_percent`, `window_milliseconds`, `observed_at`, and `steal_percent` when Linux reports it. I/O wait and stolen time are excluded from busy time. While a valid interval is unavailable, HTTP returns `503` and gRPC returns `UNAVAILABLE`.

HTTP `GET /v1/host/storage` and gRPC `HostService.GetStorage` report `path`, `total_bytes`, `used_bytes`, `available_bytes`, and `read_only`. `available_bytes` excludes space reserved for privileged use. Set `host.storage_path` to an absolute path on the volume you want to monitor. The default is `/` in Node's mount namespace. In Docker, use the path visible inside the container. When the configured path cannot be inspected, HTTP returns `503` and gRPC returns `UNAVAILABLE`.

HTTP `GET /v1/host/stats` and gRPC `HostService.GetSystemStats` return `uptime_seconds`, `cpu`, `memory`, and `storage` using the individual operations' data and units. Unavailable fields are omitted, including `cpu` during its initial sample window.

The example binds both listeners to loopback. HTTP `GET /healthz` returns `204 No Content` when ready. The gRPC health service on port 8081 reports `SERVING` for the empty service name. Set `management.tls.cert_file` and `management.tls.key_file` to enable TLS. Optionally configure `management.tls.client_ca_file` for mutual TLS and `management.api_key` for the `x-api-key` header or metadata.

## Development

Run the checks with Go 1.26.11 or newer. The packaging command also requires Docker and curl.

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```
