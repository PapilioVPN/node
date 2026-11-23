<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="fa" dir="rtl">سرویس مستقل Go برای مشاهدهٔ وضعیت میزبان Linux و مدیریت موتورهای شبکه، با پشتیبانی از Xray.</p>

<p align="center">
  <a href="README.md" lang="en"><img src="assets/flags/gb.svg" width="24" alt=""> English</a> ·
  <strong lang="fa"><img src="assets/flags/ir-lion-and-sun.svg" width="24" alt=""> فارسی</strong> ·
  <a href="README.ru.md" lang="ru"><img src="assets/flags/ru.svg" width="24" alt=""> Русский</a> ·
  <a href="README.zh-CN.md" lang="zh-CN"><img src="assets/flags/cn.svg" width="24" alt=""> 简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/PapilioVPN/node/actions/workflows/check.yml"><img alt="Checks" src="https://github.com/PapilioVPN/node/actions/workflows/check.yml/badge.svg?branch=main"></a>
</p>

<p align="center" dir="rtl">
  <a href="#quick-start">شروع سریع</a> ·
  <a href="#capabilities">قابلیت‌ها</a> ·
  <a href="#management">رابط‌های مدیریتی</a> ·
  <a href="#benchmarks">بنچمارک‌ها</a> ·
  <a href="#development">توسعه</a>
</p>

<div dir="rtl" lang="fa">

<a name="quick-start"></a>

## شروع سریع

بستهٔ Linux amd64 را از یک [اجرای موفق بررسی‌ها](https://github.com/PapilioVPN/node/actions/workflows/check.yml) بگیر. در پوشهٔ استخراج‌شدهٔ artifact، صحت آرشیو را بررسی کن و Node را با تنظیمات نمونه اجرا کن.

```bash
archive=node-0.0.0-linux-amd64-YOUR_COMMIT_SHA.tar.gz
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
cp -n node/config.example.yml config.yml
./node/node -config config.yml
```

برای اجرای همین نسخه با Docker، تصویر همراه بسته را بارگذاری کن و فایل تنظیمات را به کانتینر بده.

```bash
docker load -i node/image.tar
docker run --rm --network=host --read-only --cap-drop ALL \
  --mount "type=bind,source=$PWD/config.yml,target=/config.yml,readonly" \
  "$(cat node/image-name)"
```

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## قابلیت‌ها

| بخش | عملیات در دسترس |
| --- | --- |
| آمادگی Node | بررسی آمادگی رابط‌های مدیریتی Node از طریق HTTP و gRPC. |
| زمان کارکرد میزبان | خواندن زمان سپری‌شده از بوت لینوکس به ثانیهٔ کامل از طریق HTTP و gRPC. |
| حافظهٔ میزبان | خواندن RAM کل و در دسترس، و ظرفیت و فضای آزاد swap، به بایت از طریق HTTP و gRPC. |
| پردازندهٔ میزبان | خواندن درصد مصرف کل CPU از طریق HTTP و gRPC. |
| فضای ذخیره‌سازی میزبان | بررسی ظرفیت و وضعیت فقط‌خواندنی فایل‌سیستمِ مسیر تنظیم‌شده. |
| گزارش کلی میزبان | دریافت uptime، CPU، حافظه و فضای ذخیره‌سازی در یک پاسخ. |
| نمونهٔ Xray | راه‌اندازی، خواندن وضعیت و توقف یک نمونهٔ تنظیم‌شده از طریق HTTP و gRPC، همراه با جزئیات خطای راه‌اندازی. |
| اعتبارسنجی کانفیگ Xray | بررسی فایل JSON تنظیم‌شده از HTTP و gRPC پیش از راه‌اندازی دوبارهٔ نمونه. |
| آمار اجرای Xray | خواندن زمان کارکرد، تخصیص حافظه، goroutineها و شمارنده‌های GC نمونهٔ فعال از HTTP و gRPC. |

<a name="management"></a>

## رابط‌های مدیریتی

درخواست HTTP به `GET /v1/host/uptime` فیلد `uptime_seconds` را برمی‌گرداند. عملیات gRPC یعنی `HostService.GetUptime` همین مقدار را مطابق [قرارداد API میزبان](internal/host/bridge/grpc/proto/v1/host.proto) می‌دهد. هر دو ساعت بوت قابل مشاهده در فضای زمانی لینوکسِ Node را می‌خوانند.

درخواست `GET /v1/host/memory` و عملیات `HostService.GetMemory` از `/proc/meminfo` قابل مشاهده برای Node می‌خوانند. همهٔ مقادیر به بایت هستند. فیلد `available_bytes` برآورد کرنل از RAM قابل استفاده بدون swap است و هر وقت کرنل آن را گزارش کند در پاسخ می‌آید.

درخواست `GET /v1/host/cpu` و عملیات `HostService.GetCPU` یک نمونهٔ مشترک از CPU لینوکس را می‌دهند که تقریباً هر ثانیه به‌روز می‌شود. پاسخ شامل `busy_percent`، `window_milliseconds`، `observed_at` و در صورت گزارش کرنل `steal_percent` است. زمان انتظار I/O و زمان گرفته‌شده توسط هایپروایزر در درصد مشغولی حساب نمی‌شوند. تا وقتی بازهٔ معتبری در دسترس نباشد، HTTP وضعیت `503` و gRPC وضعیت `UNAVAILABLE` می‌دهد.

درخواست `GET /v1/host/storage` و عملیات `HostService.GetStorage` فیلدهای `path`، `total_bytes`، `used_bytes`، `available_bytes` و `read_only` را می‌دهند. فضای رزروشده برای کاربران دارای دسترسی ویژه در `available_bytes` حساب نمی‌شود. برای پایش یک دیسک، `host.storage_path` را روی یک مسیر مطلق از همان دیسک بگذار. مقدار پیش‌فرض `/` در فضای mount خود Node است. در Docker، مسیر قابل‌مشاهده از داخل کانتینر را وارد کن. اگر مسیر قابل‌بررسی نباشد، HTTP وضعیت `503` و gRPC وضعیت `UNAVAILABLE` می‌دهد.

درخواست `GET /v1/host/stats` و عملیات `HostService.GetSystemStats` فیلدهای `uptime_seconds`، `cpu`، `memory` و `storage` را با داده‌ها و واحدهای APIهای تکی می‌دهند. فیلدهای در دسترس‌نبودنی در پاسخ نمی‌آیند. برای نمونه، `cpu` بعد از آماده‌شدن اولین نمونه می‌آید.

برای فعال‌کردن یک نمونه، `xray.id`، `xray.executable` و `xray.config_file` را طبق [نمونهٔ تنظیمات](deploy/config.example.yml) تنظیم کن. از Xray نسخهٔ v26.11.9 و فایل JSON تحت کنترل اپراتور استفاده کن. Node موقع راه‌اندازی بخش `api` را با یک سوکت یونیکس خصوصی برای بررسی آمادگی جایگزین می‌کند. در Docker باینری و فایل JSON را mount کن و به کمک گزینه‌ای مثل `--tmpfs /tmp` یک مسیر قابل‌نوشتن بده. مدیریت راه‌دور Xray به TLS با کلید API یا CA کلاینت نیاز دارد.

برای وضعیت از `GET /v1/xray/instances/{id}` و برای راه‌اندازی و توقف از `PUT` و `DELETE /v1/xray/instances/{id}/execution` با بدنهٔ خالی استفاده کن. [قرارداد gRPC](internal/engines/xray/instances/bridge/grpc/proto/v1/instances.proto) عملیات `XrayInstancesService.Start`، `GetStatus` و `Stop` را دارد. راه‌اندازی منتظر پاسخ API بومی می‌ماند و توقف پس از خروج فرایند تمام می‌شود.

شکست راه‌اندازی با HTTP `503` و فیلد JSON به نام `reason`، یا با gRPC `UNAVAILABLE` و جزئیات `XrayStartFailure` گزارش می‌شود. مقادیر علت `launch_failed`، `exited_before_ready`، `readiness_timeout` و `readiness_failed` هستند. اگر Xray پیش از آمادگی با کد خروج تمام شود، پاسخ `exit_code` را هم دارد.

با بدنهٔ خالی به `POST /v1/xray/instances/{id}/config-validations` درخواست بفرست یا `XrayConfigsService.ValidateConfig` را با شناسهٔ نمونه صدا بزن. Node فایل تنظیم‌شده را با همان گزینه‌های راه‌اندازی بررسی می‌کند. فایل نامعتبر با `valid: false` و کد ایراد برمی‌گردد. اگر Xray خط و ستون را گزارش کرده باشد، در پاسخ می‌آیند. شکست اجرای آزمون خطای API دارد. [قرارداد gRPC](internal/engines/xray/configs/bridge/grpc/proto/v1/configs.proto)

از `GET /v1/xray/instances/{id}/stats/runtime` یا `XrayStatsService.GetRuntimeStats` استفاده کن. هر پاسخ مستقیم از اجرای فعال Xray خوانده می‌شود. واحد حافظه بایت، زمان کارکرد ثانیهٔ کامل از آغاز سرویس آمار Xray و مجموع مکث GC نانوثانیه است. اگر نمونهٔ تنظیم‌شده در دسترس نباشد، HTTP پاسخ `503` و gRPC وضعیت `UNAVAILABLE` می‌دهد. تا ۳۲ خواندن هم‌زمان پذیرفته می‌شود. درخواست اضافه در HTTP پاسخ `429` و در gRPC وضعیت `RESOURCE_EXHAUSTED` می‌گیرد. [قرارداد gRPC](internal/engines/xray/stats/bridge/grpc/proto/v1/runtime_stats.proto)

در نمونه، هر دو listener روی loopback باز می‌شوند. درخواست HTTP به `GET /healthz` هنگام آمادگی پاسخ `204 No Content` دارد. سرویس health در gRPC روی پورت 8081 برای نام سرویس خالی وضعیت `SERVING` را می‌دهد. برای فعال‌کردن TLS، فیلدهای `management.tls.cert_file` و `management.tls.key_file` را تنظیم کن. فیلد `management.tls.client_ca_file` برای mTLS و `management.api_key` برای هدر یا متادیتای `x-api-key` اختیاری‌اند.

<a name="benchmarks"></a>

## بنچمارک‌ها

[گزارش بنچمارک](benchmarks/README.md) مقایسهٔ Host و Xray با PasarGuard 0.5.7، نتایج هر دور و روش بازتولید اندازه‌گیری‌ها را دارد.

<a name="development"></a>

## توسعه

بررسی‌ها را با Go نسخهٔ 1.26.11 یا جدیدتر اجرا کن. بسته‌بندی به Docker و curl هم نیاز دارد.

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```

بعد از تغییر `host.proto`، فایل `host.pb.go` را در همان پوشه دوباره تولید کن. `protoc` نسخهٔ 36.2 و `protoc-gen-go` نسخهٔ v1.36.12 را در `PATH` بگذار و دستور را از ریشهٔ ریپو اجرا کن.

```bash
go generate ./internal/host/bridge/grpc
```
