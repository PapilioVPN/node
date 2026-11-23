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

<a name="management"></a>

## رابط‌های مدیریتی

درخواست HTTP به `GET /v1/host/uptime` فیلد `uptime_seconds` را برمی‌گرداند. عملیات gRPC یعنی `HostService.GetUptime` همین مقدار را مطابق [قرارداد API میزبان](api/v1/host.proto) می‌دهد. هر دو ساعت بوت قابل مشاهده در فضای زمانی لینوکسِ Node را می‌خوانند.

درخواست `GET /v1/host/memory` و عملیات `HostService.GetMemory` از `/proc/meminfo` قابل مشاهده برای Node می‌خوانند. همهٔ مقادیر به بایت هستند. فیلد `available_bytes` برآورد کرنل از RAM قابل استفاده بدون swap است و هر وقت کرنل آن را گزارش کند در پاسخ می‌آید.

درخواست `GET /v1/host/cpu` و عملیات `HostService.GetCPU` یک نمونهٔ مشترک از CPU لینوکس را می‌دهند که تقریباً هر ثانیه به‌روز می‌شود. پاسخ شامل `busy_percent`، `window_milliseconds`، `observed_at` و در صورت گزارش کرنل `steal_percent` است. زمان انتظار I/O و زمان گرفته‌شده توسط هایپروایزر در درصد مشغولی حساب نمی‌شوند. تا وقتی بازهٔ معتبری در دسترس نباشد، HTTP وضعیت `503` و gRPC وضعیت `UNAVAILABLE` می‌دهد.

درخواست `GET /v1/host/storage` و عملیات `HostService.GetStorage` فیلدهای `path`، `total_bytes`، `used_bytes`، `available_bytes` و `read_only` را می‌دهند. فضای رزروشده برای کاربران دارای دسترسی ویژه در `available_bytes` حساب نمی‌شود. برای پایش یک دیسک، `host.storage_path` را روی یک مسیر مطلق از همان دیسک بگذار. مقدار پیش‌فرض `/` در فضای mount خود Node است. در Docker، مسیر قابل‌مشاهده از داخل کانتینر را وارد کن. اگر مسیر قابل‌بررسی نباشد، HTTP وضعیت `503` و gRPC وضعیت `UNAVAILABLE` می‌دهد.

درخواست `GET /v1/host/stats` و عملیات `HostService.GetSystemStats` فیلدهای `uptime_seconds`، `cpu`، `memory` و `storage` را با داده‌ها و واحدهای APIهای تکی می‌دهند. فیلدهای در دسترس‌نبودنی در پاسخ نمی‌آیند. برای نمونه، `cpu` بعد از آماده‌شدن اولین نمونه می‌آید.

در نمونه، هر دو listener روی loopback باز می‌شوند. درخواست HTTP به `GET /healthz` هنگام آمادگی پاسخ `204 No Content` دارد. سرویس health در gRPC روی پورت 8081 برای نام سرویس خالی وضعیت `SERVING` را می‌دهد. برای فعال‌کردن TLS، فیلدهای `management.tls.cert_file` و `management.tls.key_file` را تنظیم کن. فیلد `management.tls.client_ca_file` برای mTLS و `management.api_key` برای هدر یا متادیتای `x-api-key` اختیاری‌اند.

<a name="development"></a>

## توسعه

بررسی‌ها را با Go نسخهٔ 1.26.11 یا جدیدتر اجرا کن. بسته‌بندی به Docker و curl هم نیاز دارد.

```bash
bash scripts/ci/check.sh
bash scripts/ci/package.sh
```
