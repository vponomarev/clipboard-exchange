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

## Фаза 3 — нативные Android и macOS приложения

Статус: запланирована. Рекомендуемый подход — два небольших нативных клиента к
тому же HTTP/WebSocket API: Kotlin + Jetpack Compose для Android и Swift + SwiftUI
для macOS. WebView не используется как основная архитектура.

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

### Android

- создать Gradle-проект на Kotlin с Jetpack Compose;
- реализовать подключение к комнате, список записей, add/copy/delete и real-time;
- реализовать QR scanner и обработку ссылок комнаты;
- добавить Android Sharesheet: отправка выделенного текста и файлов в комнату;
- добавить системные действия «копировать» и «поделиться» для записи;
- учитывать ограничения Android clipboard/background execution;
- реализовать шифрование, совместимое с Web UI;
- после фазы 2 добавить загрузку, скачивание и шаринг файлов;
- добавить unit, contract и Compose UI tests;
- проверить телефоны/планшеты, portrait/landscape, светлую/тёмную темы;
- настроить signed APK/AAB, GitHub Actions и публикацию в GitHub Releases;
- при необходимости подготовить Google Play listing и privacy policy.

### macOS

- создать Swift/SwiftUI-проект;
- реализовать подключение к комнате, add/copy/delete и real-time;
- сделать обычное окно и опциональный menu bar режим;
- добавить global shortcut для отправки текущего clipboard по явному действию;
- поддержать открытие room/deep links и QR-код через камеру или изображение;
- реализовать шифрование, совместимое с Web UI, с хранением ключей в Keychain;
- добавить drag-and-drop и Share Extension после завершения файловой фазы;
- добавить unit, contract и XCUITest smoke tests;
- проверить Intel и Apple Silicon либо выпускать universal binary;
- настроить code signing, hardened runtime, notarization и GitHub Actions release;
- при необходимости подготовить Mac App Store listing и sandbox entitlements.

### Definition of Done фазы 3

- Android и macOS клиенты совместимы с Web UI в одной комнате;
- текст и шифрованные записи одинаково читаются всеми тремя клиентами;
- real-time, reconnect, QR/deep links и системный share flow протестированы;
- после фазы 2 обеспечена совместимость файлов и их шифрования;
- ключи не попадают в логи, аналитику, crash reports или серверные запросы;
- Android APK/AAB и подписанный/notarized macOS artifact собираются в CI;
- опубликованы первые стабильные GitHub Releases и инструкции установки;
- выполнена ручная проверка на реальном Android-устройстве и Mac.

### Что потребуется от владельца проекта

- выбрать bundle/application IDs, например `com.example.clipboardexchange`;
- предоставить название приложения, иконку и желаемые цвета либо утвердить их
  разработку;
- определить минимальные версии Android и macOS;
- решить, достаточно ли GitHub Releases или нужна публикация в Google Play и
  Mac App Store;
- для store-релизов предоставить Google Play Console и Apple Developer accounts;
- безопасно передать Android signing key и Apple signing/notarization credentials
  через GitHub Actions secrets — не добавлять их в репозиторий;
- предоставить Android-устройство и Mac для финальной ручной приёмки;
- решить, должны ли приложения запоминать комнаты и ключи по умолчанию;
- определить отношение к self-signed TLS и корпоративным/домашним CA;
- утвердить, нужен ли macOS menu bar режим и global shortcut в первом MVP.

### Предлагаемый порядок реализации

1. Protocol specification, capabilities endpoint и encryption test vectors.
2. Android text-only MVP и GitHub APK release.
3. macOS text-only MVP и signed/notarized GitHub release.
4. Общие contract tests и cross-client interoperability matrix.
5. Подключение файлов после стабилизации фазы 2.
6. Store publication, если она требуется.

## Фаза 2.5 — productivity и эксплуатация

Статус: реализована и прошла Go, browser matrix и Linux lab smoke. Подробная
спецификация: [PHASE25.md](PHASE25.md).

- installable PWA и Android Web Share Target;
- clipboard paste/read для текста, изображений и файлов;
- client-side поиск, фильтры, pins и unread navigation;
- recent/favorite rooms и явное безопасное запоминание ключей;
- атомарная публикация текста с группой файлов;
- TTL комнат/записей, clear room и best-effort download-once;
- расширенный passive preview без выполнения active content;
- browser notifications и опциональный sound;
- CLI status/rooms/backup/restore/reconcile и Prometheus metrics.
