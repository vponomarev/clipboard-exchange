# Clipboard Exchange

Маленький self-hosted обменник текста между браузерами. Сервер написан на Go,
SQLite и весь интерфейс встроены в один бинарник. Комнаты обновляются в реальном
времени через WebSocket; регистрация и аккаунты не требуются.

## Возможности

- произвольный многострочный UTF-8 текст без форматирования и изменения;
- комнаты с UUID или коротким именем в URL `/r/{room}`;
- добавление, копирование и удаление записей любым участником комнаты;
- адаптивный интерфейс и QR-код ссылки;
- опциональное client-side шифрование AES-256-GCM;
- HTTP, встроенный HTTPS или работа за nginx/HAProxy;
- SQLite/WAL, TTL комнат и эксплуатационные лимиты;
- single binary для Linux amd64 без CGO.

Передача файлов запланирована для второй фазы и в текущую версию не входит.

## Быстрый старт

```bash
./clipboard-exchange --listen=:8080 --database=/var/lib/clipboard-exchange/data.db
```

Откройте `http://server:8080`. По умолчанию неактивные комнаты удаляются через
30 дней.

Встроенный TLS:

```bash
./clipboard-exchange \
  --listen=:8443 \
  --tls-cert=/etc/ssl/certs/clipboard.pem \
  --tls-key=/etc/ssl/private/clipboard.key
```

Все параметры:

```text
--listen=:8080                 адрес HTTP(S)
--database=clipboard-exchange.db
--tls-cert=FILE                PEM-сертификат
--tls-key=FILE                 PEM-ключ
--room-ttl=720h                TTL неактивной комнаты; 0 отключает
--max-item-bytes=65536         максимум байт одной записи
--max-items-per-room=500       максимум записей комнаты
--max-rooms=10000              максимум комнат
--rate-limit=120               изменений с одного IP в минуту; 0 отключает
--trust-proxy=false            доверять Forwarded/X-Forwarded-For
```

## Reverse proxy

Минимальная конфигурация nginx с TLS termination и WebSocket:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}
```

При rate limiting по реальному адресу клиента запускайте сервер с
`--trust-proxy` только если к нему нельзя подключиться в обход доверенного proxy.

## Шифрование

При создании защищённой комнаты браузер генерирует случайный 256-битный ключ либо
преобразует введённый пароль через PBKDF2-SHA-256 (310 000 итераций). Каждая запись
шифруется независимо AES-256-GCM; room ID включён в authenticated additional data.

Полная ссылка имеет вид:

```text
https://example/r/room-id#key=ce1_...
```

URL fragment после `#` не отправляется HTTP-серверу. Сервер хранит ciphertext,
nonce, идентификатор ключа, размер и время записи. Идентификатор SHA-256 фиксирует
единственный ключ комнаты, но не раскрывает случайный ключ. Зашифрованную ссылку
можно показать QR-кодом целиком или без ключа и передать ключ отдельно.

Web Crypto работает только в secure context: используйте HTTPS либо `localhost`.
Обычный `http://192.168.x.x` подходит для открытых комнат, но современные браузеры
не предоставляют там криптографический API.

### Модель угроз

Room ID является bearer-секретом. Любой, кто знает ссылку, может читать открытые
данные, добавлять и удалять записи. Короткие имена вроде `my-ex` считаются
угадываемыми и предназначены только для доверенной локальной сети.

Client-side encryption скрывает содержимое от сервера, базы и сетевого proxy, но
не скрывает room ID, размеры, время, порядок и операции удаления. Получатель полной
зашифрованной ссылки обладает ключом и полным доступом. Человеческий пароль может
быть подобран по сохранённому ciphertext; случайный ключ предпочтительнее.

## Разработка и тесты

Требуется Go 1.24 или новее. Для browser e2e также нужны Node.js и Playwright:

```bash
go test ./...
go vet ./...
npm ci
npx playwright install chrome firefox chromium
npm run test:e2e
```

Тесты Playwright покрывают Chrome, Firefox и Android Chrome viewport: создание
комнат, точность многострочного текста, real-time, удаление, QR и шифрование.
GitHub Actions дополнительно выполняет Go-тесты на Linux, Windows и macOS, race
detector на Linux и собирает Linux amd64 single binary.

## Релизы

Workflow `.github/workflows/release.yml` запускается по тегу `v*`, тестирует код,
создаёт stripped Linux amd64 архив, SHA-256 checksums и GitHub Release.
