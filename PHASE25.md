# Фаза 2.5: productivity, atomic entries и эксплуатация

## Цель

Сделать Web UI пригодным для ежедневного использования как установленное PWA,
не добавляя аккаунты и не ослабляя client-side encryption. Protocol v5 вводит
атомарно публикуемые записи, срок жизни и операции управления комнатой.

## Принятые решения

- PWA и Web Share Target используют тот же embedded Service Worker, что и
  streaming decrypt. Полученные через Android Sharesheet данные временно хранятся
  только в IndexedDB браузера и не отправляются до нажатия «Добавить».
- Недавние комнаты, пользовательские названия, избранное, pins, настройки
  уведомлений и поисковый индекс локальны конкретному browser profile.
- Ключ комнаты запоминается только по явному согласию. Он шифруется локальным
  non-extractable Web Crypto key, сохранённым в IndexedDB. Это защищает от
  случайного чтения browser storage, но не от кода, уже выполняющегося в origin.
- Новая запись сначала создаётся как draft с ожидаемым числом файлов. Текст и
  завершённые uploads не выдаются читателям до `commit`. Старые protocol v4
  записи без строки в таблице entries считаются опубликованными.
- TTL комнаты задаётся при создании и переопределяет global inactivity TTL.
  TTL записи задаётся при отправке. Нулевое значение означает отсутствие
  отдельного срока.
- «Удалить после скачивания» является best-effort: сервер удаляет запись после
  первого полного ответа на скачивание. Получатель может сохранить или
  скопировать данные; это не DRM. Для группы с несколькими файлами режим
  запрещён, чтобы первое скачивание не уничтожило остальные вложения.
- Inline preview разрешён только для passive MIME types. HTML, SVG и JavaScript
  никогда не исполняются в origin приложения.
- Prometheus endpoint не содержит содержимого, room IDs, aliases или имён файлов.

## Protocol v5

```text
POST   /api/rooms/{room}/entries
POST   /api/rooms/{room}/entries/{entry}/commit
DELETE /api/rooms/{room}/entries/{entry}
POST   /api/rooms/{room}/clear
PUT    /api/rooms/{room}/entries/{entry}/pin
GET    /metrics
```

`POST entries` принимает UUID, необязательный text/encrypted payload,
`expectedFiles`, `expiresInSeconds` и `deleteAfterDownload`. Uploads используют
тот же `entryId`. `commit` успешно выполняется только когда число завершённых
файлов равно `expectedFiles`; после commit единственное WebSocket-событие делает
запись видимой всем клиентам.

## Web UI

- installable PWA, offline app shell и Web Share Target;
- recent/favorite rooms и локальные названия;
- явное запоминание/удаление room secrets;
- clipboard read/paste для текста, изображений и файлов;
- client-side поиск, type filters, локальные pins, unread marker;
- галерея passive previews для image/audio/video/PDF/text/JSON;
- browser notifications и опциональный короткий sound только для новых записей,
  появившихся когда документ скрыт;
- настройка TTL записи и best-effort download-once для допустимого сообщения.

## CLI

```text
clipboard-exchange status
clipboard-exchange rooms list
clipboard-exchange rooms purge ROOM
clipboard-exchange backup --output FILE
clipboard-exchange restore --input FILE --force
clipboard-exchange storage reconcile
```

`backup` создаёт tar.gz со SQLite snapshot, file objects и manifest. Для строго
согласованной копии рекомендуется остановить systemd service. `restore` требует
явный `--force`, отказывается перезаписывать непустое назначение без него и также
должен выполняться при остановленном сервисе.

## Definition of Done

- protocol v4 clients продолжают читать и создавать legacy entries;
- draft никогда не виден до commit и очищается вместе с stale uploads;
- поиск и room history не отправляют дополнительные plaintext данные серверу;
- shared/clipboard files не отправляются без «Добавить»;
- preview active content остаётся download-only;
- metrics и CLI не раскрывают пользовательское содержимое;
- Chrome, Firefox и Android viewport проходят E2E; Linux lab проходит race,
  backup/restore/reconcile, migration и live smoke.
