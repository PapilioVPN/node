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
  <a href="#development">Разработка</a>
</p>

<a name="quick-start"></a>

## Быстрый старт

Скачайте артефакт для Linux amd64 из успешного [запуска проверок](https://github.com/PapilioVPN/node/actions/workflows/check.yml). В каталоге распакованного артефакта проверьте архив и запустите Node с примером настроек.

```bash
archive=node-0.0.0-linux-amd64-YOUR_COMMIT_SHA.tar.gz
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
cp -n node/config.example.yml config.yml
./node/node -config config.yml
```

Чтобы запустить ту же сборку в Docker, загрузите образ из архива и подключите файл настроек.

```bash
docker load -i node/image.tar
docker run --rm --network=host --read-only --cap-drop ALL \
  --mount "type=bind,source=$PWD/config.yml,target=/config.yml,readonly" \
  "$(cat node/image-name)"
```

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## Возможности

| Раздел | Доступные операции |
| --- | --- |
| Готовность Node | Проверка готовности интерфейсов управления Node по HTTP и gRPC. |
| Время работы хоста | Время с момента загрузки Linux в целых секундах по HTTP и gRPC. |
| Память хоста | Общий и доступный объём ОЗУ, а также общий и свободный объём swap, в байтах по HTTP и gRPC. |
| Процессор хоста | Суммарная загрузка CPU по HTTP и gRPC. |
| Хранилище хоста | Объём и режим чтения файловой системы для настроенного пути. |
| Сводка хоста | Время работы, загрузка CPU, память и хранилище в одном ответе. |

<a name="management"></a>

## API управления

HTTP-запрос `GET /v1/host/uptime` возвращает поле `uptime_seconds`. В gRPC метод `HostService.GetUptime` возвращает ту же величину согласно [контракту API хоста](api/v1/host.proto). Оба метода используют часы загрузки из пространства имён времени Linux, доступного Node.

HTTP-запрос `GET /v1/host/memory` и метод gRPC `HostService.GetMemory` читают доступный Node файл Linux `/proc/meminfo`. Значения указаны в байтах. Поле `available_bytes` содержит оценку доступной без подкачки памяти и присутствует, когда ядро сообщает это значение.

HTTP-запрос `GET /v1/host/cpu` и метод gRPC `HostService.GetCPU` возвращают общий замер загрузки процессора Linux, обновляемый примерно раз в секунду. Ответ содержит `busy_percent`, `window_milliseconds`, `observed_at` и `steal_percent`, если его сообщает Linux. Время ожидания ввода-вывода и время, отнятое гипервизором, не входят в загрузку. Пока корректного замера нет, HTTP возвращает `503`, а gRPC возвращает `UNAVAILABLE`.

HTTP-запрос `GET /v1/host/storage` и метод gRPC `HostService.GetStorage` возвращают `path`, `total_bytes`, `used_bytes`, `available_bytes` и `read_only`. В `available_bytes` не входит пространство, зарезервированное для привилегированных пользователей. Укажите в `host.storage_path` абсолютный путь на нужном томе. По умолчанию используется `/` в пространстве имён монтирования Node. В Docker укажите путь, видимый внутри контейнера. Если проверить путь невозможно, HTTP возвращает `503`, а gRPC возвращает `UNAVAILABLE`.

HTTP-запрос `GET /v1/host/stats` и метод gRPC `HostService.GetSystemStats` возвращают `uptime_seconds`, `cpu`, `memory` и `storage` с теми же данными и единицами, что и отдельные операции. Недоступные поля пропускаются. Поле `cpu` появляется после первого корректного замера.

В примере оба интерфейса привязаны к loopback. HTTP-запрос `GET /healthz` возвращает `204 No Content`, когда Node готов. Служба проверки gRPC на порту 8081 возвращает `SERVING` для пустого имени службы. Для TLS укажите `management.tls.cert_file` и `management.tls.key_file`. Необязательный параметр `management.tls.client_ca_file` включает mTLS, а `management.api_key` задаёт ключ для заголовка или метаданных `x-api-key`.

<a name="development"></a>

## Разработка

Для проверок используйте Go 1.26.11 или новее. Для сборки архива также требуются Docker и curl.

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```
