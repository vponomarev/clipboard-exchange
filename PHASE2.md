# Фаза 2: передача файлов

## Цель

Добавить файлы в существующие комнаты, сохранив single-binary deployment,
real-time, отсутствие аккаунтов и client-side encryption. Файл до 500 МБ не
должен целиком загружаться в память сервера или браузера.

## Принятые исходные решения

- Лимит одной комнаты: 500 MiB реально занятых на сервере байт. Для encrypted
  room в квоту входит ciphertext и небольшой служебный overhead.
- Максимальный размер одного файла по умолчанию также 500 MiB.
- Любой участник комнаты может загрузить, скачать или удалить файл.
- Основное хранилище — локальная файловая система; SQLite хранит метаданные,
  upload sessions, reservations и связь с комнатой.
- Стандартный каталог systemd installation:
  `/var/lib/clipboard-exchange/files`.
- Upload разбивается на чанки по 1 MiB. Размер настраивается сервером и
  публикуется через capabilities endpoint.
- Незавершённая загрузка резервирует квоту и удаляется через 24 часа.
- Имя и байты файла сохраняются без модификации. Имя никогда не используется как
  путь на сервере.
- В открытой комнате сервер видит имя, MIME type и размер. В encrypted room имя,
  MIME type, точный исходный размер и содержимое шифруются в браузере; последний
  чанк дополняется до полного размера. Сервер видит room ID, file ID, округлённый
  ciphertext size, число чанков и время операций.
- Preview/thumbnail generation на сервере в первый релиз не входит.

Эти значения будут настраиваться флагами и environment variables.

## Архитектура хранения

### SQLite

Добавляются версионированные migrations и таблицы:

- `uploads`: upload ID, room ID, ожидаемый stored size, reservation, chunk count,
  полученные чанки, срок действия и encryption metadata;
- `files`: file ID, room ID, storage object, размеры, chunk layout, manifest,
  key ID, protocol version и timestamps;
- связь завершённого файла с общей timeline комнаты.

Reservation создаётся транзакционно. Одновременные загрузки не смогут суммарно
превысить room quota. Запись файла появляется в комнате только после успешного
commit всей загрузки.

### Файловая система

- чанки сначала записываются во временный upload directory;
- каждый чанк пишется во временный файл, `fsync`-ится и атомарно переименовывается;
- повторный PUT того же чанка идемпотентен при совпадающем digest;
- complete проверяет число, размеры и digest чанков, собирает итоговый blob,
  `fsync`-ит его и делает atomic rename;
- пользовательские имена не участвуют в filesystem paths;
- startup recovery и периодический GC удаляют stale uploads и orphan objects;
- удаление файла сначала фиксируется в SQLite, затем безопасно очищает object;
  повторный GC завершает очистку после crash.

## API v1 для файлов

Предлагаемые endpoints:

```text
GET    /api/capabilities
POST   /api/rooms/{room}/uploads
GET    /api/rooms/{room}/uploads/{upload}
PUT    /api/rooms/{room}/uploads/{upload}/chunks/{index}
POST   /api/rooms/{room}/uploads/{upload}/complete
DELETE /api/rooms/{room}/uploads/{upload}
GET    /api/rooms/{room}/files/{file}
DELETE /api/rooms/{room}/files/{file}
```

`POST uploads` возвращает upload ID, negotiated chunk size, expiry и уже принятые
чанки. Это позволяет продолжать загрузку после обрыва. После перезагрузки страницы
пользователь повторно выбирает тот же файл; клиент сверяет метаданные и digest.

Download поддерживает стандартные `Content-Length`, `Content-Disposition`, ETag и
Range для открытых файлов. Encrypted download выдаёт ciphertext stream и chunk
layout, необходимый клиенту для последовательной расшифровки.

Ошибки API получают стабильные machine-readable codes: quota exceeded, upload
expired, chunk conflict, invalid chunk, incomplete upload, file missing и storage
unavailable.

## Client-side encryption

- Используется ключ уже зашифрованной комнаты.
- Каждый plaintext chunk независимо шифруется AES-256-GCM.
- Для каждого чанка генерируется независимый случайный 96-bit nonce. Nonce хранится
  рядом с ciphertext; клиент и сервер отклоняют повтор nonce внутри upload session.
- AAD включает protocol version, room ID, file ID, chunk index и plaintext size.
- Manifest с исходным именем, MIME type, точным размером и chunk layout шифруется
  отдельно. Последний plaintext chunk перед шифрованием дополняется случайными
  байтами до negotiated chunk size; реальная длина берётся только из manifest.
- Сервер проверяет `keyId`, envelope structure, размеры и число чанков, но не видит
  plaintext.
- В репозитории публикуются test vectors для Web UI и будущих Android/macOS
  клиентов фазы 3.
- Повреждение или перестановка чанков должна обнаруживаться до выдачи результата.

Для streaming download в encrypted room используется встроенный same-origin
service worker: он формирует download response и принимает расшифрованные чанки
через Web Streams/MessageChannel. Это исключает сборку 500-МБ Blob в памяти и
работает только в secure context, что уже является требованием encrypted room.

## Web UI

- drag-and-drop и обычный file picker;
- выбор нескольких файлов;
- очередь с прогрессом каждого файла и общим использованием quota;
- cancel и retry/resume;
- понятные состояния encrypting, uploading, finalizing и failed;
- download progress и проверка целостности;
- кнопки скачать, копировать имя и удалить;
- точные имя и размер без серверных preview/formatting;
- адаптивная компоновка Android portrait/landscape;
- уведомление о необходимости HTTPS для encrypted files;
- доступные labels, keyboard navigation и светлая/тёмная темы.

WebSocket сообщает только о завершении или удалении файла. Прогресс приватной
upload session не транслируется другим участникам.

## Конфигурация

Планируемые параметры:

```text
--files-dir=/var/lib/clipboard-exchange/files
--max-file-bytes=524288000
--max-room-file-bytes=524288000
--file-chunk-bytes=1048576
--upload-ttl=24h
--max-active-uploads=32
```

Systemd `install/upgrade/deinstall` создаёт каталог, назначает service user и
сохраняет файлы при обычном `deinstall`. `deinstall --purge` удаляет и SQLite, и
file storage.

## Этапы реализации

### 2.1 — protocol и storage foundation

- capabilities endpoint и protocol document;
- SQLite migration framework;
- metadata/upload schema;
- filesystem store abstraction;
- quota reservation и stale upload GC;
- unit, migration, crash-recovery и path-safety tests.

Результат: сервер умеет безопасно хранить upload chunks, но UI ещё не показывает
файлы.

### 2.2 — открытые файлы

- resumable upload/complete/abort API;
- streaming/Range download и delete;
- timeline/WebSocket integration;
- drag-and-drop, progress, retry и download в Web UI;
- browser tests Chrome, Firefox и Android viewport.

Результат: полноценные файлы в открытых комнатах. Планируемый prerelease: v0.3.0.

### 2.3 — encrypted files

- encrypted manifest и chunk protocol;
- browser streaming encrypt/decrypt;
- same-origin download service worker;
- test vectors и negative integrity tests;
- interoperability specification для фазы 3.

Результат: сервер не видит содержимое и метаданные файлов encrypted room.
Планируемый prerelease: v0.4.0.

### 2.4 — hardening и стабильный релиз

- concurrent quota/race tests;
- interrupted upload, restart и disk-full scenarios;
- storage reconciliation и backup/restore documentation;
- нагрузочный тест нескольких одновременных файлов на Linux lab;
- реальный Chrome/Firefox/macOS/Android acceptance;
- systemd migration и upgrade/rollback проверка.

Результат: стабильный релиз фазы 2, ориентировочно v0.5.0.

## Definition of Done

- Файл размером до 500 МБ загружается и скачивается без удержания целого файла в
  RAM клиента или сервера.
- Несколько клиентов видят завершённые upload/delete события в real-time.
- Обрыв сети допускает продолжение загрузки без повторной передачи готовых чанков.
- Параллельные uploads не обходят room quota.
- Crash/restart не публикует неполный файл и не оставляет постоянную reservation.
- TTL комнаты удаляет SQLite metadata и file objects.
- Encrypted room скрывает имя, MIME type, исходный размер и содержимое от сервера.
- Ошибочный ключ, повреждённый или переставленный chunk не создаёт повреждённый
  plaintext-файл.
- Старые текстовые комнаты и клиенты продолжают работать после миграции.
- Chrome, Firefox и Android browser tests проходят в CI; выполнена ручная проверка
  на реальных устройствах.
- Systemd install, upgrade, rollback, deinstall и purge учитывают file storage.
- Выпущен Linux amd64 single-binary release с checksum и инструкциями backup.

## Риски, которые проверяем первыми

1. Streaming encrypted download в Firefox/Android без большого Blob в памяти.
2. Crash consistency между SQLite reservation и filesystem rename.
3. Disk-full во время chunk write, final assembly и cleanup.
4. Одновременное завершение uploads около лимита 500 MiB.
5. Производительность SQLite/WAL и filesystem GC при большом числе чанков.
