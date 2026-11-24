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

روی Linux amd64 با Docker Compose، بستهٔ نصب نسخهٔ موردنظر را بگیر و Node را بالا بیاور.

```bash
(
  set -eu
  version=1.2.0
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

رابط مدیریتی به‌صورت پیش‌فرض روی loopback است. با `curl -fsS http://127.0.0.1:8080/healthz` آن را بررسی کن. نصب باینری، نصب آفلاین و تنظیم Xray در [راهنمای نصب](docs/install.md) آمده است.

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## قابلیت‌ها

- **میزبان** — زمان کارکرد، CPU، حافظه و فضای ذخیره‌سازی.
- **Xray** — چرخهٔ عمر، بررسی کانفیگ، همگام‌سازی و بازیابی حساب‌ها، آمار ترافیک و آنلاین، مسیریابی، لاگ و سلامت خروجی.
- **WireGuard** — چرخهٔ عمر و بازیابی رابط لینوکس، اعتبارسنجی کانفیگ، تغییر زندهٔ MTU و همگام‌سازی کامل peerها.

<a name="management"></a>

[مرجع HTTP و gRPC](docs/management.fa.md)

<a name="benchmarks"></a>

## بنچمارک‌ها

<!-- benchmark-summary -->
**در ۸۲ از ۱۰۲ سناریوی هم‌ارز Xray جلو بودیم**. نتیجهٔ ۱۳ سناریو ترکیبی بود و PasarGuard 0.5.7 در ۷ سناریو جلو بود. گزارش کامل ۱۶ سناریوی دیگر با ورودی، خروجی یا تضمین‌های متفاوت را هم پوشش می‌دهد.
<!-- /benchmark-summary -->

[خلاصهٔ بنچمارک، گزارش گروه‌ها و داده‌های خام](benchmarks/xray/README.md) · [بنچمارک رابط WireGuard](benchmarks/wireguard/interfaces/README.md)

<a name="development"></a>

## توسعه

برای بررسی محلی و ارسال Pull Request، [راهنمای مشارکت](CONTRIBUTING.md) را بخوان.

## مجوز

PapilioVPN Node تحت [مجوز عمومی همگانی گنو نسخهٔ ۳](LICENSE) منتشر می‌شود.
