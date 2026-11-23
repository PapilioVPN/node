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
| Экземпляр Xray | Запуск, чтение состояния и остановка одного настроенного экземпляра по HTTP и gRPC с типизированными ошибками запуска. |
| Проверка конфигурации Xray | Проверка настроенного JSON по HTTP и gRPC перед перезапуском экземпляра. |
| Статистика Xray | Время работы, выделение памяти, количество горутин и счётчики сборки мусора активного экземпляра по HTTP и gRPC. |
| Счётчики трафика Xray | Чтение входящих и исходящих байтов для одной или всех учётных записей, входящих или исходящих тегов через HTTP и gRPC. |
| Онлайн-IP в Xray | Подсчитать уникальные IP-адреса источников и получить время их последнего обнаружения для одной учётной записи по HTTP и gRPC. |

<a name="management"></a>

## API управления

HTTP-запрос `GET /v1/host/uptime` возвращает поле `uptime_seconds`. В gRPC метод `HostService.GetUptime` возвращает ту же величину согласно [контракту API хоста](internal/host/bridge/grpc/proto/v1/host.proto). Оба метода используют часы загрузки из пространства имён времени Linux, доступного Node.

HTTP-запрос `GET /v1/host/memory` и метод gRPC `HostService.GetMemory` читают доступный Node файл Linux `/proc/meminfo`. Значения указаны в байтах. Поле `available_bytes` содержит оценку доступной без подкачки памяти и присутствует, когда ядро сообщает это значение.

HTTP-запрос `GET /v1/host/cpu` и метод gRPC `HostService.GetCPU` возвращают общий замер загрузки процессора Linux, обновляемый примерно раз в секунду. Ответ содержит `busy_percent`, `window_milliseconds`, `observed_at` и `steal_percent`, если его сообщает Linux. Время ожидания ввода-вывода и время, отнятое гипервизором, не входят в загрузку. Пока корректного замера нет, HTTP возвращает `503`, а gRPC возвращает `UNAVAILABLE`.

HTTP-запрос `GET /v1/host/storage` и метод gRPC `HostService.GetStorage` возвращают `path`, `total_bytes`, `used_bytes`, `available_bytes` и `read_only`. В `available_bytes` не входит пространство, зарезервированное для привилегированных пользователей. Укажите в `host.storage_path` абсолютный путь на нужном томе. По умолчанию используется `/` в пространстве имён монтирования Node. В Docker укажите путь, видимый внутри контейнера. Если проверить путь невозможно, HTTP возвращает `503`, а gRPC возвращает `UNAVAILABLE`.

HTTP-запрос `GET /v1/host/stats` и метод gRPC `HostService.GetSystemStats` возвращают `uptime_seconds`, `cpu`, `memory` и `storage` с теми же данными и единицами, что и отдельные операции. Недоступные поля пропускаются. Поле `cpu` появляется после первого корректного замера.

Для включения одного экземпляра задайте `xray.id`, `xray.executable` и `xray.config_file` по [примеру настройки](deploy/config.example.yml). Используйте Xray v26.11.9 и JSON-файл, которым управляет оператор. При запуске Node заменяет его блок `api` приватным Unix-сокетом для проверки готовности. В Docker смонтируйте исполняемый файл и JSON и предоставьте каталог с правом записи, например с помощью `--tmpfs /tmp`. Для удалённого управления требуются TLS и ключ API или клиентский CA.

Запрос `GET /v1/xray/instances/{id}` возвращает состояние. Запросы `PUT` и `DELETE /v1/xray/instances/{id}/execution` с пустым телом запускают и останавливают процесс. [Контракт gRPC](internal/engines/xray/instances/bridge/grpc/proto/v1/instances.proto) предоставляет `XrayInstancesService.Start`, `GetStatus` и `Stop`. Запуск ждёт ответа API Xray, остановка ждёт завершения процесса.

При сбое запуска HTTP возвращает `503` с полем JSON `reason`, а gRPC возвращает `UNAVAILABLE` с деталями `XrayStartFailure`. Возможные причины: `launch_failed`, `exited_before_ready`, `readiness_timeout` и `readiness_failed`. Если Xray завершился до готовности и вернул код, ответ содержит `exit_code`.

Отправьте пустой запрос `POST /v1/xray/instances/{id}/config-validations` или вызовите `XrayConfigsService.ValidateConfig` с идентификатором экземпляра. Node проверяет настроенный файл с теми же параметрами, что при запуске. Недопустимая конфигурация возвращает `valid: false` и код проблемы, а также строку и столбец, если их сообщил Xray. Сбой выполнения проверки возвращает ошибку API. [Контракт gRPC](internal/engines/xray/configs/bridge/grpc/proto/v1/configs.proto)

Используйте `GET /v1/xray/instances/{id}/stats/runtime` или `XrayStatsService.GetRuntimeStats`. Каждый ответ использует свежий снимок активного процесса Xray. Одновременные запросы могут получить один и тот же следующий снимок. Память измеряется в байтах, время работы — в целых секундах с запуска службы статистики Xray, суммарная пауза GC — в наносекундах. Для недоступного экземпляра HTTP возвращает `503`, а gRPC — `UNAVAILABLE`. Буфер ожидающих чтений вмещает 128 запросов. При заполнении буфера HTTP возвращает `429`, а gRPC — `RESOURCE_EXHAUSTED`. [Контракт gRPC](internal/engines/xray/stats/bridge/grpc/proto/v1/runtime_stats.proto) · [Бенчмарк и исходные результаты](benchmarks/README.md#xray-runtime-statistics-after-the-rewrite)

Используйте `GET /v1/xray/instances/{id}/stats/traffic/{scope}` с `user`, `inbound` или `outbound`. Добавьте `?name=...` для одной учётной записи или тега. Методы gRPC — `XrayStatsService.GetTraffic` и `ListTraffic`. Чтение сохраняет значения счётчиков. Отсутствующее направление пропускается, а значение ноль возвращается как ноль. Для отсутствующего имени HTTP возвращает `404`, а gRPC — `NOT_FOUND`. Порядок списка не задан. Включите статистику трафика для нужной области в политике Xray. [Контракт gRPC](internal/engines/xray/stats/bridge/grpc/proto/v1/traffic.proto)

Используйте `GET /v1/xray/instances/{id}/stats/online/count?name=...` для подсчёта и `/stats/online/ips?name=...` для списка в том же экземпляре. Методы gRPC — `XrayStatsService.GetOnlineIPCount` и `ListOnlineIPs`. Включите `statsUserOnline` на уровне политики учётной записи. Время указано в секундах Unix. Пустая карта отслеживаемых IP даёт ноль или пустую карту. При отсутствии наблюдения HTTP возвращает `404`, а gRPC — `NOT_FOUND`. [Контракт gRPC](internal/engines/xray/stats/bridge/grpc/proto/v1/online.proto)

В примере оба интерфейса привязаны к loopback. HTTP-запрос `GET /healthz` возвращает `204 No Content`, когда Node готов. Служба проверки gRPC на порту 8081 возвращает `SERVING` для пустого имени службы. Для TLS укажите `management.tls.cert_file` и `management.tls.key_file`. Необязательный параметр `management.tls.client_ca_file` включает mTLS, а `management.api_key` задаёт ключ для заголовка или метаданных `x-api-key`.

<a name="benchmarks"></a>

## Бенчмарки

В [отчёте о бенчмарках](benchmarks/README.md) приведены сравнения Host и Xray с PasarGuard 0.5.7, результаты каждого раунда и инструкции для повторного запуска.

<a name="development"></a>

## Разработка

Для проверок используйте Go 1.26.11 или новее. Для сборки архива также требуются Docker и curl.

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```

После изменения `host.proto` заново сгенерируйте `host.pb.go` в том же каталоге. Добавьте `protoc` 36.2 и `protoc-gen-go` v1.36.12 в `PATH` и выполните команду из корня репозитория.

```bash
go generate ./internal/host/bridge/grpc
```
