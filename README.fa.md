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

بستهٔ اجرایی برای **Linux amd64** است و به Docker و GNU coreutils نیاز دارد.
آرتیفکت `node-linux-amd64-<commit>` را از یک اجرای موفق
[Checks](https://github.com/PapilioVPN/node/actions/workflows/check.yml)
دانلود و فایل دریافتی از GitHub را باز کن. در پوشه‌ای که آرشیو و فایل checksum
آن قرار دارند، به‌جای `YOUR_FULL_COMMIT_SHA` شناسهٔ کامل کامیت آرتیفکت را بگذار.

```bash
revision=YOUR_FULL_COMMIT_SHA
archive="node-linux-amd64-$revision.tar.gz"
sha256sum -c "$archive.sha256" && tar -xzf "$archive"
```

بعد از تأیید checksum و باز شدن آرشیو، ایمیج داخل بسته را نصب کن و برای
راه‌اندازی اول از تنظیمات نمونه کپی بگیر.

```bash
install_dir="$HOME/.local/share/papilio-node"
config_dir="$HOME/.config/papilio-node"
./node/install.sh "$install_dir"
install -d -m 700 "$config_dir"
test -f "$config_dir/config.yml" ||
  cp "$install_dir/deploy/config.example.yml" "$config_dir/config.yml"
```

پیش از اجرا `config.yml` را ویرایش کن و گواهی‌های معرفی‌شده در تنظیمات را
قرار بده. نمونه هر سه روش ارتباطی را فعال می‌کند. برای اجرا فقط با HTTP،
بخش‌های `communications.grpc`، `communications.nats` و
`communications.requests.nats` را حذف کن. آدرس شنود HTTP در نمونه
`127.0.0.1:8443` است.

```bash
"$install_dir/run.sh" "$config_dir" papilio-node --network=host --detach
docker logs papilio-node
```

برای توقف اجرای نمونه از `docker stop --time 150 papilio-node` استفاده کن.

اسکریپت اجرا پوشهٔ تنظیمات را فقط‌خواندنی mount می‌کند. برای Xray،
`instances` را طبق [تنظیمات نمونه](deploy/config.example.yml) تعریف کن و فایل
اجرایی و کانفیگ انتخاب‌شده را فقط‌خواندنی mount کن. برای هر نمونه یک پوشهٔ
کاری خصوصی و قابل‌نوشتن و در صورت پیکربندی، یک مسیر ماندگار خصوصی برای
وضعیت آن تعیین کن. مالک پوشهٔ وضعیت باید کاربر اجرای Node و سطح دسترسی آن
باید `0700` باشد. برای دیدن اطلاعات میزبان از داخل Docker، namespaceها و
mountهای صریح توضیح‌داده‌شده در نمونه را به‌کار ببر.

<a name="capabilities"></a>

<a name="current-capabilities"></a>

## قابلیت‌ها

| بخش | عملیات قابل استفاده |
| --- | --- |
| [سیستم و Node](https://github.com/PapilioVPN/node/tree/main/internal/apps/system) | هویت میزبان، زمان کارکرد، بار، CPU، حافظه و swap، پردازش‌ها، نشست‌ها و سوابق ورود، حسگرها، فایل‌سیستم و ورودی‌وخروجی دیسک، شمارنده‌های رابط شبکه و عیب‌یابی پردازش و منابع Node. |
| [نمونه‌های Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/instances) | فهرست و وضعیت نمونه‌ها، شروع، توقف، راه‌اندازی مجدد و بررسی آمادگی بومی. با تنظیم مسیر ماندگار، تأیید و اتصال مجدد به پردازش سالم، بازیابی با تعداد تلاش محدود، تعیین حفظ پردازش هنگام توقف و مدیریت lease کنترلر. |
| [تنظیمات Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/configs) | اعتبارسنجی و کامپایل سورس‌ها، مشاهدهٔ نسخهٔ مطلوب و اعمال‌شده، جایگزینی و اعمال کانفیگ. |
| [ورودی‌های Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/inbounds) | فهرست handlerها و tagها، خلاصه و سازگاری ورودی‌ها، افزودن از دادهٔ کامپایل‌شده یا سورس، حذف با tag و بازبینی و اجرای طرح جایگزینی. |
| [خروجی‌های Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/proxies/outbounds) | فهرست صفحه‌بندی‌شدهٔ handlerها، افزودن از دادهٔ کامپایل‌شده یا سورس و حذف با tag. بازبینی طرح جایگزینی، اعمال تغییر زندهٔ پشتیبانی‌شده و خواندن نتیجه. تغییرهای نیازمند راه‌اندازی مجدد از API تنظیمات اعمال می‌شوند. |
| [حساب‌های Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/accounts) | شمارش و جست‌وجوی حساب‌های موتور، فهرست صفحه‌بندی‌شدهٔ مشخصات بدون دادهٔ محرمانه، خروجی صریح رکوردهای بومی و بررسی سازگاری ورودی. افزودن و حذف تکی یا گروهی با سقف مشخص و آماده‌سازی اعضا در کانفیگ کامپایل‌شده. |
| [حضور آنلاین در Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/users/online) | مشاهدهٔ شمارش، کاربران و رکوردهای آنلاین و IPهای هر کاربر. |
| [مسیریابی Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/network/routing) | فهرست‌کردن، افزودن و حذف قوانین زنده، آزمایش انتخاب مسیر، خواندن وضعیت balancer و تعیین مقصد آن. |
| [آمار Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/runtime/statistics) | خواندن آمار اجرایی بومی موتور. |
| [ترافیک Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/traffic) | خواندن و بازنشانی شمارنده‌های تکی، جفت ارسال و دریافت و مجموعهٔ شمارنده‌های انتخاب‌شده. |
| [سلامت خروجی‌های Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/outboundhealth) | فهرست نتایج قابل مشاهدهٔ سلامت و بررسی یک خروجی با tag. |
| [Loggerهای Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/observability/loggers) | بازکردن مجدد loggerهای Xray و مشاهدهٔ نتیجهٔ عملیات. |
| [ابزارهای کلید و گواهی Xray](https://github.com/PapilioVPN/node/tree/main/internal/apps/xray/tools/credentials) | تولید UUID، کلیدهای X25519 و WireGuard، مواد کلید ML-DSA-65 و ML-KEM-768، جفت رمزنگاری VLESS، گواهی TLS و مجموعه‌کلید ECH. محاسبهٔ هش گواهی TLS. مواد خصوصی فقط در همان پاسخ برمی‌گردند. |

عملیات زنده روی نمونهٔ در حال اجرا اثر می‌گذارد. عملیات کانفیگ تعیین
می‌کند Xray با چه تنظیماتی شروع شود. سوابق کاربران محصول و سیاست‌هایشان
در کنترلر می‌ماند و Node عملیات بومی موتور و مدیریت کانفیگ را ارائه می‌دهد.

<a name="management"></a>

## رابط‌های مدیریتی

روش‌هایی را فعال کن که کنترلر استفاده می‌کند. هر سه رابط عملیات نسخه‌بندی‌شدهٔ
همان سرویس‌های Node را ارائه می‌دهند.

| روش | نمونهٔ خواندن اطلاعات اجرایی | کنترل دسترسی |
| --- | --- | --- |
| HTTP | `GET /v1/system/node/runtime` | Node گواهی کنترلر را با mTLS اعتبارسنجی می‌کند. |
| gRPC | `papilio.system.node.runtime.v1.NodeRuntimeService/GetNodeInfo` | Node گواهی کنترلر را با mTLS اعتبارسنجی می‌کند. |
| NATS | `<prefix>.system.node.runtime.info.v1.get` با یک شیء خالی JSON | کارگزار هر دو طرف را با mTLS احراز هویت می‌کند و دسترسی به subjectها را با ACL محدود می‌کند. |

برای درخواست‌های NATS از پیشوند تعریف‌شده در تنظیمات استفاده کن.
رابط مدیریتی بومی Xray را در دسترس محلی Node نگه دار و برای آن از
آدرس عددی loopback یا سوکت Unix استفاده کن.

از میزبان، مسیر HTTP را با گواهی معتبر کنترلر بررسی کن. از CA امضاکنندهٔ
گواهی سرور و آدرسی استفاده کن که در همان گواهی آمده است.

```bash
curl --fail --show-error --silent \
  --cacert node-ca.crt --cert controller.crt --key controller.key \
  https://127.0.0.1:8443/v1/system/node/runtime
```

<a name="development"></a>

## توسعه

برای ساخت از نسخهٔ Go ثبت‌شده در [go.mod](https://github.com/PapilioVPN/node/blob/main/go.mod) استفاده کن. برای اجرای
برنامهٔ ساخته‌شده، مسیر تنظیمات را با `-config /path/to/config.yml` بده.

```bash
go build -trimpath -o bin/node ./cmd/node
./bin/node -help
go test -race ./...
```

در Linux amd64 تست‌های یکپارچه را با نسخهٔ پین‌شدهٔ Xray اجرا کن.

```bash
xray_test_binary="$(sh scripts/ci/prepare-xray.sh)" &&
  XRAY_TEST_BINARY="$xray_test_binary" go test -race -tags=integration ./...
```

[Checks](https://github.com/PapilioVPN/node/actions/workflows/check.yml) کد و بستهٔ اجرایی را بررسی می‌کند.
هنگام ادغام هر قابلیت، پس از اتصال و بررسی عملیات عمومی آن، ردیف مربوط
را در هر چهار README به‌روز کن.

</div>
