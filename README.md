# Clipboard Exchange

Планы следующих этапов находятся в [ROADMAP.md](ROADMAP.md).

Маленький self-hosted обменник текста между браузерами. Сервер написан на Go,
SQLite и весь интерфейс встроены в один бинарник. Комнаты обновляются в реальном
времени через WebSocket; регистрация и аккаунты не требуются.

## Возможности

- произвольный многострочный UTF-8 текст без форматирования и изменения;
- комнаты с UUID или коротким именем в URL `/r/{room}`;
- отдельные R/O- и R/W-ссылки: читать и копировать может любой участник, изменять — только обладатель write capability;
- добровольный непроверяемый alias рядом с датой записи;
- потоковая передача файлов до 500 МиБ, продолжение прерванных upload и Range-download;
- адаптивный интерфейс и QR-код ссылки;
- опциональное client-side шифрование AES-256-GCM;
- HTTP, встроенный HTTPS или работа за nginx/HAProxy;
- SQLite/WAL, TTL комнат и эксплуатационные лимиты;
- single binary для Linux amd64 без CGO.

Для файлов действует отдельная квота комнаты 500 МиБ. Открытые файлы сохраняют
точные имя и байты. В encrypted room имя, MIME type, исходный размер, alias и
содержимое шифруются в браузере; сервер хранит только фиксированные ciphertext-чанки.

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
--files-dir=clipboard-exchange-files
--max-file-bytes=524288000     максимум хранимых байт одного файла
--max-room-file-bytes=524288000 квота файлов и reservations комнаты
--file-chunk-bytes=1048576     размер plaintext-чанка
--upload-ttl=24h               TTL незавершённой загрузки
--max-active-uploads=32        активные uploads на сервере
```

## Установка как systemd service

Команды установки встроены в Linux-бинарник и требуют root. Установка копирует
текущий бинарник в `/usr/local/bin`, создаёт отдельного системного пользователя,
каталоги конфигурации и данных, включает сервис и сразу запускает его:

```bash
sudo ./clipboard-exchange install --listen=:8080
systemctl status clipboard-exchange
```

Параметры сервера сохраняются в
`/etc/clipboard-exchange/clipboard-exchange.env`. После ручного изменения файла:

```bash
sudo systemctl restart clipboard-exchange
```

Обновление выполняется новым бинарником, скачанным из проверенного релиза. Старый
бинарник сохраняется как `/usr/local/bin/clipboard-exchange.previous`; если новый
сервис не перезапустится, команда автоматически откатит бинарник:

```bash
curl -fLO https://github.com/vponomarev/clipboard-exchange/releases/download/VERSION/clipboard-exchange-linux-amd64.tar.gz
curl -fLO https://github.com/vponomarev/clipboard-exchange/releases/download/VERSION/checksums.txt
sha256sum -c checksums.txt
tar -xzf clipboard-exchange-linux-amd64.tar.gz
sudo ./clipboard-exchange-linux-amd64/clipboard-exchange upgrade
```

Обычное удаление сохраняет конфигурацию и SQLite, поэтому последующая установка
может использовать прежние данные:

```bash
sudo /usr/local/bin/clipboard-exchange deinstall
```

Полное удаление данных, конфигурации и service user необратимо и выполняется только
с явным параметром:

```bash
sudo /usr/local/bin/clipboard-exchange deinstall --purge
```

### Backup и restore

SQLite metadata и каталог `files` образуют одну согласованную копию. Для простого
offline backup остановите сервис и архивируйте весь data directory вместе с
конфигурацией:

```bash
sudo systemctl stop clipboard-exchange
sudo tar -C / -czf clipboard-exchange-backup.tar.gz \
  var/lib/clipboard-exchange etc/clipboard-exchange
sudo systemctl start clipboard-exchange
```

Restore выполняется при остановленном сервисе в пустые каталоги из того же архива;
не восстанавливайте SQLite и `files` из разных моментов времени. После restore
запустите сервис и проверьте `/readyz`; startup reconciliation удалит только
незарегистрированные временные chunks и orphan objects.

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

## Права доступа и alias

При создании комнаты браузер выдаёт R/W-ссылку вида
`/r/room-id#write=cw1_...`. Секрет после `#` не попадает в обычный HTTP-запрос,
а UI передаёт его серверу в заголовке авторизации только для добавления, удаления
и ротации права записи. R/O-ссылка не содержит параметр `write`. В диалоге
«Поделиться» можно выбрать оба варианта и построить для них QR-код.

Сервер хранит только SHA-256 write capability. После ротации все прежние R/W-ссылки
сразу перестают работать; восстановить потерянное право записи без старой ссылки
невозможно. Alias необязателен, не проверяется и не подтверждает личность. В открытой
комнате он виден серверу, а в зашифрованной находится внутри ciphertext.

Эта версия использует новую схему SQLite без автоматической миграции ранних комнат.
Перед обновлением с v0.2.0 архивируйте старую БД и запустите приложение с пустой БД.

## Шифрование

При создании защищённой комнаты браузер генерирует случайный 256-битный ключ либо
преобразует введённый пароль через PBKDF2-SHA-256 (310 000 итераций). Каждая запись
и каждый файловый чанк шифруются независимо AES-256-GCM; room ID, file ID и индекс
чанка включены в authenticated additional data. Encrypted manifest скрывает имя,
MIME type, точный размер и alias. Потоковое скачивание расшифровывается встроенным
same-origin Service Worker без сборки всего файла в памяти.

Полная ссылка имеет вид:

```text
https://example/r/room-id#write=cw1_...&key=ce1_...
```

URL fragment после `#` не отправляется HTTP-серверу. Сервер хранит ciphertext,
nonce, идентификатор ключа, размер и время записи. Идентификатор SHA-256 фиксирует
единственный ключ комнаты, но не раскрывает случайный ключ. Зашифрованную ссылку
можно показать QR-кодом целиком или без ключа и передать ключ отдельно.

Web Crypto работает только в secure context: используйте HTTPS либо `localhost`.
Обычный `http://192.168.x.x` подходит для открытых комнат, но современные браузеры
не предоставляют там криптографический API.

### Модель угроз

Room ID даёт право чтения. Любой, кто знает R/W-ссылку с write capability, может
также добавлять и удалять записи. Короткие имена вроде `my-ex` считаются
угадываемыми и предназначены только для доверенной локальной сети.

Client-side encryption скрывает содержимое от сервера, базы и сетевого proxy, но
не скрывает room ID, округлённые размеры, время, порядок и операции удаления. Ключ
даёт возможность расшифрования, а право записи определяется отдельным write
capability. Человеческий пароль может быть подобран по сохранённому ciphertext;
случайный ключ предпочтительнее.

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
