<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="ru">Управляйте Xray, WireGuard, OpenVPN и Hysteria 2 на Linux. Запуск, трафик, журналы и метрики хоста доступны через HTTP и gRPC.</p>

<p align="center">
  <a href="README.md" lang="en"><img src="assets/flags/gb.svg" width="24" alt=""> English</a> ·
  <a href="README.fa.md" lang="fa"><img src="assets/flags/ir-lion-and-sun.svg" width="24" alt=""> فارسی</a> ·
  <strong lang="ru"><img src="assets/flags/ru.svg" width="24" alt=""> Русский</strong> ·
  <a href="README.zh-CN.md" lang="zh-CN"><img src="assets/flags/cn.svg" width="24" alt=""> 简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/PapilioVPN/node/actions/workflows/check.yml"><img alt="Checks" src="https://github.com/PapilioVPN/node/actions/workflows/check.yml/badge.svg?branch=main"></a>
</p>

<p align="center">
  <a href="#quick-start">Быстрый старт</a> ·
  <a href="#capabilities">Возможности</a> ·
  <a href="#management">API управления</a> ·
  <a href="#benchmarks">Бенчмарки</a> ·
  <a href="#development">Разработка</a>
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

## Быстрый старт

На Linux amd64 с Docker Compose скачайте полный архив, проверьте контрольную сумму и запустите Node. Образ Docker включён в архив.

```bash
(
  set -eu
  version=1.7.0
  bundle="node-$version-linux-amd64.tar.gz"
  release="https://github.com/PapilioVPN/node/releases/download/v$version"
  curl -fLO "$release/$bundle" -fLO "$release/$bundle.sha256"
  sha256sum -c "$bundle.sha256"
  tar -xzf "$bundle"
  cd node
  mkdir -p config
  test -e config/config.yml || cp config.example.yml config/config.yml
  docker load -i image.tar
  NODE_IMAGE="$(cat image-name)" docker compose up -d --pull never
)
```

API управления по умолчанию слушает loopback. Проверьте его командой `curl -fsS http://127.0.0.1:8080/healthz`. Нативная установка и настройка движков описаны в [руководстве по установке](docs/install.md).

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## Возможности

| Движок или хост | Возможности Node |
| --- | --- |
| **Хост** | время работы, CPU, память, хранилище и скорость сетевого трафика. |
| **Xray** | запуск и остановка, проверка конфигурации, синхронизация и восстановление учётных записей, трафик, онлайн-статистика, маршрутизация, журналы и состояние исходящих подключений. |
| **WireGuard** | управление интерфейсом Linux с таймаутом управления и восстановлением, проверка конфигурации и изменение нескольких параметров на ходу, управление пирами и передача изменений по частям через HTTP и gRPC, мониторинг активности, статистика процесса, журналы, проверка исходящего соединения и опциональные пересылка IPv4, NAT и маршрутизация по правилам. |
| [**OpenVPN**](docs/openvpn.md) | Управление сервером Linux, проверка конфигурации, сессии и счётчики, журналы, отключение клиентов и публикация подписанных списков отзыва через HTTP и gRPC. |
| [**Hysteria 2**](benchmarks/hysteria/README.md) | Управление серверами Linux через HTTP и gRPC. Подтверждённый хеш конфигурации, предварительная проверка, журналы без секретов, статистика работы и трафика по ID, сброс счётчиков всего экземпляра, число подключённых устройств и нативные запросы на отключение. |

<a name="management"></a>

[Справочник HTTP и gRPC](docs/management.ru.md)

<a name="benchmarks"></a>

## Бенчмарки

<!-- benchmark-summary -->
**PapilioVPN лидирует в 82 из 102 сопоставимых сценариев Xray**. В 13 сценариях результаты смешанные, в 7 лидирует PasarGuard 0.5.7. Сводка также содержит 16 сценариев на общей тестовой установке, в которых различаются входные данные, поля ответа или гарантии.
<!-- /benchmark-summary -->

[Сводка, отчёты по группам и исходные данные](benchmarks/xray/README.md) · [Бенчмарк интерфейса WireGuard](benchmarks/wireguard/interfaces/README.md) · [Контракт и бенчмарк Hysteria 2](benchmarks/hysteria/README.md)

<a name="development"></a>

## Разработка

Локальные проверки и порядок открытия pull request описаны в [руководстве для участников](CONTRIBUTING.md).

## Лицензия

PapilioVPN Node распространяется по [GNU General Public License версии 3](LICENSE).

<sub>“WireGuard” and the “WireGuard” logo are registered trademarks of Jason A. Donenfeld. OpenVPN® is a registered trademark of OpenVPN Inc. <a href="assets/engines/README.md">Источники логотипов</a></sub>
