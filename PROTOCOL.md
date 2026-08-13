# Clipboard Exchange protocol v6

`GET /api/capabilities` — источник negotiated limits и поддержанных версий.
Клиент не должен считать настроечные значения сервера равными defaults.

## Protected short links v1

Короткая ссылка `/s/{code}` содержит 4–6 символов из алфавита без неоднозначных
`0/O/1/I/L`. Полный same-origin URL `/r/{room}#...` никогда не отправляется
серверу открытым текстом. Клиент дополняет JSON
`{"target":"...","redemptionToken":"..."}` случайными байтами до 512 байт и
шифрует AES-256-GCM со случайными 96-bit IV и 128-bit salt. Ключ получается из
четырёхзначного PIN через PBKDF2-SHA-256, 600 000 итераций. AAD:

```text
clipboard-exchange-short-link:v1
```

API:

```text
POST /api/short-links
GET  /api/short-links/{code}
POST /api/short-links/{code}/redeem
```

Первый запрос передаёт только encrypted envelope, hex SHA-256 случайного
redemption-token, TTL, `maxUses` и желаемую длину кода. `GET` ничего не расходует
и возвращает envelope фиксированного размера; для отсутствующего, истёкшего или
использованного кода возвращается синтетический envelope той же формы. После
успешной локальной расшифровки клиент предъявляет 32-byte redemption token.
Сервер сравнивает его SHA-256 и атомарно увеличивает `useCount`. Поэтому неверный
PIN и HTTP-prefetch не могут погасить одноразовую ссылку.

`maxUses=1` означает одноразовый режим, `maxUses=0` — использование до TTL.
Четырёхсимвольный код разрешён только с `maxUses=1` и TTL не более 600 секунд.
Максимальный TTL — семь дней. Ответы и HTML имеют `Cache-Control: no-store` и
глобальную `Referrer-Policy: no-referrer`.

## Atomic entries v5

Protocol v5 публикует группу «необязательный текст + N файлов» только целиком:

```text
POST   /api/rooms/{room}/entries
POST   /api/rooms/{room}/entries/{entry}/commit
GET    /api/rooms/{room}/entries/{entry}/archive
PUT    /api/rooms/{room}/entries/{entry}/pin
DELETE /api/rooms/{room}/entries/{entry}
POST   /api/rooms/{room}/clear
```

Первый запрос создаёт невидимый draft и принимает `id`, `expectedFiles`,
необязательный `item`, `expiresInSeconds` и `deleteAfterDownload`. Каждый upload
ссылается на этот `entryId` и уникальный `entryIndex`. `commit` успешен, только
если завершено ровно `expectedFiles` и активных uploads группы не осталось.
До commit draft, его текст и завершённые файлы не возвращаются читателям и не
создают room event. Legacy v4-записи без строки в `entries` остаются видимыми.

TTL записи хранится как абсолютное серверное время. Download-once разрешён только
для записи без текста с одним файлом и является best-effort, а не DRM. Полный
download без Range удаляет запись; encrypted client подтверждает завершение через
`POST /api/rooms/{room}/files/{file}/consume`.

При создании комнаты `ttlSeconds` переопределяет глобальный inactivity TTL.
Нулевое значение наследует глобальную настройку. `GET /metrics` содержит только
агрегаты и не является частью пользовательских данных комнаты.

## Capability tokens

При создании комнаты поле `writeProtected` выбирает модель доступа:

- `false`: обычная R/W-комната; `writeToken` должен быть пустым, mutations не
  требуют `Authorization`;
- `true`: комната с разделением прав; требуется корректный `writeToken`, а без
  него ссылка является R/O.

Для совместимости с клиентами protocol v2 отсутствие `writeProtected` означает
защищённую комнату, если передан `writeToken`. `GET /api/rooms/{room}` возвращает
фактическое значение `room.writeProtected`. Capabilities protocol v3 публикует
`openWriteRooms: true`.

- write token: `cw1_` + 32 random bytes в unpadded base64url;
- upload token: `cu1_` + 32 random bytes в unpadded base64url;
- mutations используют `Authorization: ClipboardWrite <token>`;
- chunks, status, complete и abort используют
  `Authorization: ClipboardUpload <token>`.

Сервер хранит только unpadded-base64url SHA-256 полного token. Upload token
ограничен одной upload session. Секреты комнаты находятся в URL fragment и не
попадают в обычный request URL.

## Encrypted text v2

AES-256-GCM, random 96-bit IV, tag 128 bit. AAD:

```text
clipboard-exchange:v2:{roomID}
```

UTF-8 plaintext — compact JSON `{"text":"...","alias":"..."}`. Чтение v1
с AAD `clipboard-exchange:v1:{roomID}` остаётся поддержано.

## File upload v1

Server capability `limits.fileChunkBytes` задаёт plaintext chunk size. Open file
посылается без изменения. Encrypted file всегда состоит из `chunkCount` объектов
фиксированного размера `fileChunkBytes + 28`: 12-byte IV, AES-GCM ciphertext
полного plaintext chunk и 16-byte tag. Последний plaintext chunk дополняется
нулями; точная длина находится только в encrypted manifest.

AAD чанка:

```text
clipboard-exchange:file:v1:{roomID}:{fileID}:{index}:{plaintextChunkSize}
```

Manifest — AES-256-GCM JSON со случайным 96-bit IV и AAD:

```text
clipboard-exchange:file-manifest:v1:{roomID}:{fileID}
```

Payload:

```json
{"name":"exact name","mimeType":"application/octet-stream","size":123,"alias":"optional","chunkSize":1048576,"chunkCount":1}
```

`X-Clipboard-Chunk-IV` содержит unpadded-base64url IV и обязан совпадать с
первыми 12 байтами stored chunk. Nonce не может повторяться внутри upload.
Manifest отправляется в `POST .../complete`; до успешного complete файл не
появляется в комнате.

Protocol v4 объединяет текст и несколько файлов в одну запись комнаты. Клиент
создаёт общий UUID `entryId`, использует его как `item.id` для необязательного
текста и передаёт в `POST uploads` для каждого файла. Поле `entryIndex`
(`0..999`) задаёт порядок файлов внутри записи. Старый клиент может не передавать
`entryId`: тогда сервер использует `fileId`, и файл становится отдельной записью.

Удаление `DELETE /api/rooms/{room}/entries/{entryId}` атомарно удаляет текстовую
часть и метаданные всех файлов этой записи, после чего сервер очищает их объекты.
Старые endpoints удаления отдельного item/file сохраняются для совместимости.

`GET /api/rooms/{room}/entries/{entry}/archive` потоково формирует для открытой
комнаты ZIP с методом Store, то есть без сжатия и без временной копии архива на
диске. Имена очищаются от компонентов пути, совпадающие имена получают числовой
суффикс. В encrypted room серверный endpoint архив не создаёт: клиент
расшифровывает чанки и формирует такой же потоковый ZIP в Service Worker, поэтому
сервер не получает открытые имена и содержимое.

Открытый незашифрованный файл можно запросить с `?inline=1`. Сервер отвечает
`Content-Disposition: inline` только для безопасных passive MIME types: plain
text/CSV/JSON/PDF, raster images, audio и video. HTML, SVG, JavaScript и
неизвестные типы всегда отдаются как attachment, чтобы файл комнаты не мог
выполнять active content в origin приложения. Для encrypted file такое же
решение принимает встроенный Service Worker после клиентской расшифровки.

## Deterministic AES-256-GCM test vector

Вектор использует искусственно фиксированные IV только для interoperability test.
В production IV всегда генерируется CSPRNG.

```text
roomID=test-room
fileID=123e4567-e89b-12d3-a456-426614174000
key(base64url)=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8
chunkSize=16
chunkAAD=clipboard-exchange:file:v1:test-room:123e4567-e89b-12d3-a456-426614174000:0:16
chunkPlaintext(hex)=68656c6c6f0000000000000000000000
chunkIV(base64url)=AAECAwQFBgcICQoL
chunkCiphertextAndTag(base64url)=L2e6d6rlwhuNQZeLsel4bdiaIdDEtuOJ3wUwgkxN3fU
storedEnvelope(base64url)=AAECAwQFBgcICQoLL2e6d6rlwhuNQZeLsel4bdiaIdDEtuOJ3wUwgkxN3fU
manifestJSON={"name":"hello.txt","mimeType":"text/plain","size":5,"alias":"tester","chunkSize":16,"chunkCount":1}
manifestAAD=clipboard-exchange:file-manifest:v1:test-room:123e4567-e89b-12d3-a456-426614174000
manifestIV(base64url)=DA0ODxAREhMUFRYX
manifestCiphertextAndTag(base64url)=49wHuRIT3mkVQi25_eQIky13z8LUEFLH_vaDkIGC-thYkhJRzrVKAHadLH7M33Og6XSB5IXWdd8nngl-gM8-3WheJbnTnv4ynQ3ZEBNlcO0YEv1xffRjXuNDpIhx0Uvvl5nvlUDFaUm9X4D3Xu_-j18BhkQ
```
