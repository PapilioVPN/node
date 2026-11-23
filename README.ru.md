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
archive=node-linux-amd64-YOUR_COMMIT_SHA.tar.gz
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

<a name="management"></a>

## API управления

В примере оба интерфейса привязаны к loopback. HTTP-запрос `GET /healthz` возвращает `204 No Content`, когда Node готов. Служба проверки gRPC на порту 8081 возвращает `SERVING` для пустого имени службы. Для TLS укажите `management.tls.cert_file` и `management.tls.key_file`. Необязательный параметр `management.tls.client_ca_file` включает mTLS, а `management.api_key` задаёт ключ для заголовка или метаданных `x-api-key`.

<a name="development"></a>

## Разработка

Для проверок используйте Go 1.26.11 или новее. Для сборки архива также требуются Docker и curl.

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```
