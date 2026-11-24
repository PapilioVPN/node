<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="ru">Самостоятельный сервис на Go для наблюдения за Linux-хостом и управления сетевыми движками. Первый поддерживаемый движок — Xray.</p>

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

<a name="quick-start"></a>

## Быстрый старт

На Linux amd64 с Docker Compose скачайте пакет нужной версии и запустите Node.

```bash
(
  set -eu
  version=1.0.1
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

API управления по умолчанию слушает loopback. Проверьте его командой `curl -fsS http://127.0.0.1:8080/healthz`. Установка бинарного файла, автономная установка и настройка Xray описаны в [руководстве по установке](docs/install.md).

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## Возможности

- **Хост** — время работы, CPU, память и дисковое пространство.
- **Xray** — запуск и остановка, проверка конфигурации, синхронизация и восстановление учётных записей, трафик, онлайн-статистика, маршрутизация, журналы и состояние исходящих подключений.

<a name="management"></a>

[Справочник HTTP и gRPC](docs/management.ru.md)

<a name="benchmarks"></a>

## Бенчмарки

<!-- benchmark-summary -->
**PapilioVPN лидирует в 82 из 102 сопоставимых сценариев Xray**. В 13 сценариях результаты смешанные, в 7 лидирует PasarGuard 0.5.7. Сводка также содержит 16 сценариев на общей тестовой установке, в которых различаются входные данные, поля ответа или гарантии.
<!-- /benchmark-summary -->

[Сводка, отчёты по группам и исходные данные](benchmarks/xray/README.md)

<a name="development"></a>

## Разработка

Локальные проверки и порядок открытия pull request описаны в [руководстве для участников](CONTRIBUTING.md).

## Лицензия

PapilioVPN Node распространяется по [GNU General Public License версии 3](LICENSE).
