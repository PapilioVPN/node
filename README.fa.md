<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/banner.png">
    <img alt="PapilioVPN Node" src="assets/banner-light.png" width="100%">
  </picture>
</p>

<h1 align="center">PapilioVPN Node</h1>

<p align="center" lang="fa" dir="rtl">مدیریت Xray، WireGuard، OpenVPN و Hysteria 2 روی لینوکس. چرخهٔ اجرا، ترافیک، لاگ زنده و وضعیت میزبان از HTTP و gRPC در دسترس‌اند.</p>

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

## شروع سریع

روی Linux amd64 با Docker Compose، بستهٔ کامل را بگیر، checksum را بررسی کن و Node را بالا بیاور. ایمیج Docker داخل بسته است.

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

رابط مدیریتی به‌صورت پیش‌فرض روی loopback است. با `curl -fsS http://127.0.0.1:8080/healthz` آن را بررسی کن. نصب باینری و راه‌اندازی موتورها در [راهنمای نصب](docs/install.md) آمده است.

<a name="capabilities"></a>
<a name="current-capabilities"></a>

## قابلیت‌ها

| موتور یا میزبان | قابلیت‌های Node |
| --- | --- |
| **میزبان** | زمان کارکرد، CPU، حافظه، فضای ذخیره‌سازی و نرخ دریافت و ارسال شبکه. |
| **Xray** | چرخهٔ عمر، بررسی کانفیگ، همگام‌سازی و بازیابی حساب‌ها، آمار ترافیک و آنلاین، مسیریابی، لاگ و سلامت خروجی. |
| **WireGuard** | چرخهٔ عمر رابط لینوکس با lease مدیریتی و بازیابی، اعتبارسنجی و تغییر زندهٔ چند تنظیم با یک درخواست، مدیریت peerها و تغییرات تکه‌ای با HTTP و gRPC، پایش فعالیت، آمار اجرا و لاگ، بررسی دسترسی خروجی و تنظیم اختیاری forwarding، NAT و policy routing برای IPv4. |
| [**OpenVPN**](docs/openvpn.md) | مدیریت سرور لینوکس، اعتبارسنجی کانفیگ، سشن‌ها و شمارنده‌ها، لاگ زنده، قطع اتصال و انتشار فهرست ابطال امضاشده از HTTP و gRPC. |
| [**Hysteria 2**](benchmarks/hysteria/README.md) | مدیریت سرورهای لینوکس از HTTP و gRPC، با اثرانگشت کانفیگ تأییدشده، بررسی پیش‌اجرای کانفیگ، لاگ زنده بدون دادهٔ حساس، آمار اجرا و ترافیک هر شناسه، بازنشانی ترافیک کل نمونه، شمارش دستگاه‌های آنلاین و درخواست بومی قطع اتصال. |

<a name="management"></a>

[مرجع HTTP و gRPC](docs/management.fa.md)

<a name="benchmarks"></a>

## بنچمارک‌ها

<!-- benchmark-summary -->
**در ۸۲ از ۱۰۲ سناریوی هم‌ارز Xray جلو بودیم**. نتیجهٔ ۱۳ سناریو ترکیبی بود و PasarGuard 0.5.7 در ۷ سناریو جلو بود. گزارش کامل ۱۶ سناریوی دیگر با ورودی، خروجی یا تضمین‌های متفاوت را هم پوشش می‌دهد.
<!-- /benchmark-summary -->

[خلاصهٔ بنچمارک، گزارش گروه‌ها و داده‌های خام](benchmarks/xray/README.md) · [بنچمارک رابط WireGuard](benchmarks/wireguard/interfaces/README.md) · [قرارداد و بنچمارک Hysteria 2](benchmarks/hysteria/README.md)

<a name="development"></a>

## توسعه

برای بررسی محلی و ارسال Pull Request، [راهنمای مشارکت](CONTRIBUTING.md) را بخوان.

## مجوز

PapilioVPN Node تحت [مجوز عمومی همگانی گنو نسخهٔ ۳](LICENSE) منتشر می‌شود.

<sub>“WireGuard” and the “WireGuard” logo are registered trademarks of Jason A. Donenfeld. OpenVPN® is a registered trademark of OpenVPN Inc. <a href="assets/engines/README.md">منبع لوگوها</a></sub>
