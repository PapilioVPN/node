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

Пакет предназначен для **Linux amd64**. Для установки нужны Docker и GNU
coreutils. Скачайте артефакт `node-linux-amd64-<commit>` из успешного запуска
[Checks](https://github.com/PapilioVPN/node/actions/workflows/check.yml)
и распакуйте полученный файл GitHub. В каталоге с архивом и файлом контрольной
суммы замените `YOUR_FULL_COMMIT_SHA` полным SHA коммита артефакта.

```bash
revision=YOUR_FULL_COMMIT_SHA
archive="node-linux-amd64-$revision.tar.gz"
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
```

Проверив контрольную сумму и распаковав архив, установите образ из пакета
и скопируйте пример настроек для первого запуска.

```bash
install_dir="$HOME/.local/share/papilio-node"
config_dir="$HOME/.config/papilio-node"
./node/install.sh "$install_dir"
install -d -m 700 "$config_dir"
test -f "$config_dir/config.yml" ||
  cp "$install_dir/deploy/config.example.yml" "$config_dir/config.yml"
```

Перед запуском отредактируйте `config.yml` и разместите указанные в нём
сертификаты. В примере включены все три транспорта. Для работы только по HTTP
удалите `communications.grpc`, `communications.nats` и
`communications.requests.nats`. В примере HTTP слушает на `127.0.0.1:8443`.

```bash
"$install_dir/run.sh" "$config_dir" papilio-node --network=host --detach
docker logs papilio-node
```

Для остановки примера используйте `docker stop --time 150 papilio-node`.

Скрипт монтирует каталог настроек только для чтения. Для Xray добавьте
`instances` по [примеру настроек](deploy/config.example.yml) и подключите
выбранный исполняемый файл и конфигурацию только для чтения. Выделите каждому
экземпляру собственный закрытый рабочий каталог с правом записи и, если нужно,
отдельный постоянный каталог состояния с режимом `0700`, принадлежащий
пользователю Node. Для сбора данных хоста из Docker
явно задайте пространства имён и монтирования, описанные в примере.

<a name="capabilities"></a>

## Возможности

| Раздел | Доступные операции |
| --- | --- |
| [Система и Node](https://github.com/PapilioVPN/node/tree/main/internal/apps/system) | Идентификация хоста, время работы, нагрузка, CPU, память и swap, процессы, сеансы и история входов, датчики, файловые системы и ввод-вывод устройств, счётчики сетевых интерфейсов и диагностика ресурсов Node. |
| [Экземпляры Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/instances) | Список настроенных экземпляров, просмотр состояния, запуск, остановка, перезапуск и проверка готовности через API Xray. При настроенном постоянном хранилище доступны повторное подключение к проверенному процессу, восстановление с ограничением попыток, явное сохранение процесса и аренды контроллера. |
| [Конфигурация Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/configs) | Проверка и компиляция исходных файлов, чтение целевой и применённой ревизий, замена и применение конфигурации. |
| [Входящие обработчики Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/inbounds) | Списки обработчиков и тегов, сводки и проверка совместимости, добавление из скомпилированных данных или исходников, удаление по тегу, просмотр и выполнение планов замены. |
| [Исходящие обработчики Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/outbounds) | Снимки списка обработчиков с пагинацией, добавление из скомпилированных данных или исходников и удаление по тегу. Просмотр планов замены, применение поддерживаемых изменений без перезапуска и чтение результатов. Изменения с перезапуском выполняются через API конфигурации. |
| [Учётные записи Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/accounts) | Подсчёт и поиск действующих учётных записей, постраничные метаданные без секретов, явный экспорт нативных записей и проверка совместимости входящих обработчиков. Добавление и удаление по одной или ограниченными пакетами и подготовка состава учётных записей в скомпилированной конфигурации. |
| [Присутствие в Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/online) | Просмотр числа пользователей онлайн, списков пользователей, записей и IP-адресов отдельных пользователей. |
| [Маршрутизация Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/network/routing) | Просмотр, добавление и удаление действующих правил, проверка выбора маршрута, просмотр состояния балансировщиков и переопределение их цели. |
| [Статистика Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/statistics) | Чтение нативной статистики движка. |
| [Трафик Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/traffic) | Чтение и сброс отдельных счётчиков, пар приёма и передачи и выбранных групп счётчиков. |
| [Состояние исходящих обработчиков Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/outboundhealth) | Списки доступных результатов проверок и поиск результатов по тегу обработчика. |
| [Журналы Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/loggers) | Повторное открытие журналов Xray с просмотром результата операции. |
| [Консольные журналы Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/logs) | Чтение последних консольных записей с удалением секретов и продолжение по курсору. Ответ сообщает о пропусках, смене процесса и пределах захвата. |
| [Ключи и сертификаты Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/tools/credentials) | Генерация UUID, ключевого материала X25519/WireGuard и ML-DSA-65/ML-KEM-768, пар шифрования VLESS, сертификатов TLS и наборов ключей ECH. Вычисление хешей сертификатов TLS. Закрытые данные доступны только в текущем ответе. |

Операции в реальном времени действуют на запущенный экземпляр. Операции
с конфигурацией определяют настройки запуска Xray. Данные пользователей
приложения и политики хранятся в контроллере, а Node предоставляет нативные
операции движка и управление конфигурацией.

<a name="management"></a>

## API управления

Включите транспорты, которые использует ваш контроллер. Каждый предоставляет
версионированные операции одних и тех же служб Node.

| Транспорт | Пример получения сведений о Node | Доверие |
| --- | --- | --- |
| HTTP | `GET /v1/system/node/runtime` | Node проверяет сертификат контроллера через mTLS. |
| gRPC | `papilio.system.node.runtime.v1.NodeRuntimeService/GetNodeInfo` | Node проверяет сертификат контроллера через mTLS. |
| NATS | `<prefix>.system.node.runtime.info.v1.get` с пустым объектом JSON | Брокер аутентифицирует обе стороны через mTLS и ограничивает доступ к топикам с помощью ACL. |

Для запросов NATS используйте настроенный префикс. Нативный интерфейс
управления Xray должен быть доступен локально для Node через числовой
loopback-адрес или сокет Unix.

Проверьте HTTP с хоста, используя доверенный сертификат контроллера.
Укажите CA, подписавший сертификат сервера, и адрес из этого сертификата.

```bash
curl --fail --show-error --silent \
  --cacert node-ca.crt --cert controller.crt --key controller.key \
  https://127.0.0.1:8443/v1/system/node/runtime
```

<a name="development"></a>

## Разработка

Используйте версию Go из [go.mod](https://github.com/PapilioVPN/node/blob/main/go.mod). При запуске собранного бинарного
файла передайте путь к настройкам через `-config /path/to/config.yml`.

```bash
go build -trimpath -o bin/node ./cmd/node
./bin/node -help
go test -race ./...
```

На Linux amd64 запустите интеграционные тесты с закреплённой версией Xray.

```bash
xray_test_binary="$(sh scripts/ci/prepare-xray.sh)" &&
  XRAY_TEST_BINARY="$xray_test_binary" go test -race -tags=integration ./...
```

[Checks](https://github.com/PapilioVPN/node/actions/workflows/check.yml) проверяет исходники и пакет.
При слиянии новой возможности обновляйте соответствующую строку во всех
четырёх README после подключения и проверки её общедоступных операций.
