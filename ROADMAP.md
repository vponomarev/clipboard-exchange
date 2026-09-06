# Roadmap

## Фаза 1 — текстовый обменник

Статус: завершена.

- Go single binary со встроенным Web UI;
- комнаты, real-time WebSocket, QR-код и удаление записей;
- client-side AES-256-GCM;
- HTTP, HTTPS и reverse proxy;
- SQLite, лимиты, TTL и systemd lifecycle;
- CI, браузерные тесты и Linux amd64 releases.

Ручная приёмка выполнена в Firefox на Windows, Chrome на macOS и Firefox на
Android. На Android также проверены QR-код и смена ориентации экрана.

## Фаза 2 — файлы

Статус: реализована, идёт hardening и приёмка. Работают capability-права R/O/R/W,
ротация, alias, resumable open/encrypted uploads, квоты, Range-download, потоковое
client-side расшифрование и очистка storage.

Подробный технический план: [PHASE2.md](PHASE2.md).

- загрузка и скачивание файлов потоками;
- общий лимит файлов комнаты 500 МБ;
- отдельные R/O и R/W capability-ссылки для новых комнат;
- добровольный непроверяемый alias, сохранённый рядом с каждой записью и файлом;
- прогресс, отмена и обработка обрыва соединения;
- client-side шифрование файлов;
- очистка файлов вместе с TTL комнаты;
- дисковые квоты и защита от исчерпания места;
- инкрементальные события и пагинация истории комнаты.

## Фаза 3 — desktop-приложения

Статус: Electron-клиент использован как функциональный прототип. После проверки
UX принято решение выпускать отдельные нативные клиенты, начиная с Win32 C++ для
Windows; macOS и Linux получат собственные реализации позднее. Для Android принят
существующий installable PWA с Web Share Target. Все клиенты используют один
HTTP/WebSocket protocol, но системная интеграция, popup и hotkey path остаются
полностью нативными.

### Общая подготовка

- формализовать и версионировать REST/WebSocket protocol;
- опубликовать JSON-схемы запросов, ответов и ошибок;
- зафиксировать совместимый формат client-side encryption и тестовые векторы;
- добавить endpoint с версией и capabilities сервера;
- определить правила совместимости клиента со старыми серверами;
- подготовить mock server и общий набор contract tests;
- определить UX подключения: ввод URL, QR scan, deep link и список избранных комнат;
- хранить ключи комнат только по явному согласию пользователя в Android Keystore
  или macOS Keychain;
- корректно работать с HTTP в локальной сети, HTTPS и пользовательскими CA;
- не отключать TLS-проверку для self-signed сертификатов: вместо этого поддержать
  установку/выбор доверенного сертификата;
- предусмотреть локализацию RU/EN и accessibility.

### Desktop MVP

- [x] создать общий Electron-проект и безопасный экран подключения к серверу;
- [x] сохранить совместимость add/copy/delete, files, encryption и real-time через Web UI;
- [x] добавить обычное окно, tray/menu bar и global shortcut явной отправки clipboard;
- [x] добавить настройку четырёх глобальных хоткеев с проверкой конфликтов;
- [x] отправлять выделенный текст через Accessibility/UI Automation без clipboard;
- [x] показывать последнее сообщение в non-activating overlay;
- [x] добавить keyboard-friendly mini picker последних сообщений для вставки без clipboard;
- [x] зарегистрировать `clipboard-exchange://` deep links;
- [x] добавить unit tests URL/deep-link boundary и CI;
- [x] создать автономный Win32 shell без Electron, PowerShell и .NET;
- [x] перенести tray, настройки и четыре hotkey на WinAPI `RegisterHotKey`;
- [x] реализовать быстрый заранее созданный history popup и вставку через `SendInput`;
- [x] получать выделенный текст напрямую через COM UI Automation без clipboard;
- [x] подключить к Win32-клиенту WinHTTP REST transport, capability auth и кэш сообщений;
- [x] заменить двухсекундный polling нативным WinHTTP WebSocket reconnect;
- [x] заменить Electron Windows artifact нативным установщиком после parity;
- [x] подготовить финальный Windows `.ico` asset;
- [ ] добавить нативный Share Extension на macOS и Share target на Windows;
- [ ] подписать и notarize universal macOS artifact;
- [ ] подписать Windows installer;
- [ ] добавить Linux AppImage/deb в release matrix после ручной проверки;

### Definition of Done фазы 3

- Android PWA и desktop-клиенты совместимы с Web UI в одной комнате;
- текст, файлы и шифрованные записи одинаково читаются всеми клиентами;
- real-time, reconnect, QR/deep links и системный clipboard flow протестированы;
- после фазы 2 обеспечена совместимость файлов и их шифрования;
- ключи не попадают в логи, аналитику, crash reports или серверные запросы;
- Windows installer и подписанный/notarized macOS artifact собираются в CI;
- опубликованы первые стабильные GitHub Releases и инструкции установки;
- выполнена ручная проверка на реальном Android-устройстве и Mac.

### Что потребуется от владельца проекта

- утвердить текущий application ID `com.clipboardexchange.desktop`;
- предоставить название приложения, иконку и желаемые цвета либо утвердить их
  разработку;
- определить минимальные версии macOS и Windows;
- решить, достаточно ли GitHub Releases или нужна публикация в Mac App Store и
  Microsoft Store;
- для store-релизов предоставить Apple Developer и Microsoft Partner accounts;
- безопасно передать Apple signing/notarization и Windows signing credentials
  через GitHub Actions secrets — не добавлять их в репозиторий;
- предоставить Mac и отдельную Windows-машину/VM для финальной ручной приёмки;
- решить, должны ли приложения запоминать комнаты и ключи по умолчанию;
- определить отношение к self-signed TLS и корпоративным/домашним CA;
- утвердить четыре системных shortcut и проверить Accessibility-разрешение на macOS.

### Предлагаемый порядок реализации

1. Desktop wrapper MVP для macOS/Windows с tray, clipboard и deep links.
2. CI artifacts, финальные иконки и ручная проверка обеих ОС.
3. Signing/notarization и GitHub Release.
4. Нативные системные share extensions.
5. Linux AppImage/deb и ручная проверка desktop environments.
6. Store publication, если она требуется.

## Фаза 2.5 — productivity и эксплуатация

Статус: реализована и прошла Go, browser matrix и Linux lab smoke. Подробная
спецификация: [PHASE25.md](PHASE25.md).

- installable PWA и Android Web Share Target;
- clipboard paste/read для текста, изображений и файлов;
- client-side поиск, фильтры, pins и unread navigation;
- recent/favorite rooms и явное безопасное запоминание ключей;
- короткие защищённые ссылки с PIN, TTL и одноразовым redemption;
- атомарная публикация текста с группой файлов;
- TTL комнат/записей, clear room и best-effort download-once;
- расширенный passive preview без выполнения active content;
- browser notifications и опциональный sound;
- CLI status/rooms/backup/restore/reconcile и Prometheus metrics.
