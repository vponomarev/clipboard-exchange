# Clipboard Exchange for Windows

Нативный 64-bit Win32-клиент для Windows 10 и 11. Приложение не использует
Electron, Node.js, PowerShell или .NET во время работы и устанавливается без прав
администратора.

## Возможности

- четыре настраиваемых глобальных хоткея;
- отправка clipboard и выделенного через UI Automation без промежуточной записи
  выделения в clipboard;
- вставка последнего сообщения и быстрый picker последних 30 сообщений через
  Unicode `SendInput`, также без clipboard;
- tray, автозапуск, уведомления, single-instance и deep links
  `clipboard-exchange://connect?url=...`;
- open, R/O/R/W и AES-256-GCM encrypted text rooms;
- WinHTTP WebSocket realtime с автоматическим reconnect и safety polling;
- офлайн-кэш и URL комнаты, защищённые Windows DPAPI для текущего пользователя;
- системный DPI scaling и заранее созданный popup: показ никогда не ждёт сеть,
  диск, subprocess или UI Automation.

Файловые операции остаются в полном Web UI; его можно открыть нативной кнопкой
или через tray без потери room key/write capability из fragment URL.

## Сборка и тесты

Нужны Visual Studio Build Tools 2022+ с компонентом Desktop development with C++,
CMake и NSIS 3. Runtime-зависимостей у приложения нет:

```powershell
cd desktop/windows
cmake -S . -B build-release -A x64 -DAPP_VERSION=1.0.0
cmake --build build-release --config Release
ctest --test-dir build-release -C Release --output-on-failure
makensis /DAPP_VERSION=1.0.0 /DAPP_VERSION_NUMERIC=1.0.0.0 installer.nsi
```

GUI smoke-тесты запускаются отдельно:

```powershell
build/lifecycle-smoke.exe
build/native-input-smoke.exe
build/clipboard-exchange-win32.exe --background
build/picker-latency.exe
```

Сетевые contract smoke-тесты принимают URL заранее созданной комнаты:

```powershell
build/room-client-smoke.exe "http://localhost:8080/r/native-smoke"
build/room-events-smoke.exe "http://localhost:8080/r/native-smoke"
```

Команда `makensis` создаёт
`build/clipboard-exchange-windows-x64-1.0.0-setup.exe`. Установщик корректно
закрывает работающий экземпляр при update/uninstall, сохраняет пользовательские
настройки и регистрирует protocol handler. Публичный artifact без code-signing
сертификата будет показывать стандартное предупреждение Windows SmartScreen.

Acceptance budget для history popup — не более 250 ms. Локальный автоматический
прогон измеряет 20 последовательных открытий и завершается ошибкой при превышении.
