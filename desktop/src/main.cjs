'use strict';

const { app, BrowserWindow, Menu, Tray, clipboard, dialog, globalShortcut, ipcMain, nativeImage, net, screen, shell } = require('electron');
const fs = require('node:fs');
const path = require('node:path');
const { createAutomation } = require('./automation.cjs');
const { DEFAULT_HOTKEYS, normalizeHotkeys, displayHotkey } = require('./hotkeys.cjs');
const { normalizeTarget, targetFromDeepLink, isAllowedNavigation } = require('./url.cjs');

const APP_PROTOCOL = 'clipboard-exchange';
let mainWindow;
let settingsWindow;
let pickerWindow;
let latestWindow;
let tray;
let automation;
let server = '';
let currentHotkeys = { ...DEFAULT_HOTKEYS };
let hotkeysRegistered = false;
let pickerMessages = [];
let latestTimer;
let pendingDeepLink = process.argv.find((argument) => argument.startsWith(`${APP_PROTOCOL}://`)) || '';

const hasSingleInstanceLock = app.requestSingleInstanceLock();
if (!hasSingleInstanceLock) app.quit();

function settingsPath() { return path.join(app.getPath('userData'), 'settings.json'); }

function readSettings() {
  try {
    const parsed = JSON.parse(fs.readFileSync(settingsPath(), 'utf8'));
    return { server: typeof parsed.server === 'string' ? parsed.server : '', hotkeys: normalizeHotkeys(parsed.hotkeys) };
  } catch (_) {
    return { server: '', hotkeys: { ...DEFAULT_HOTKEYS } };
  }
}

function writeSettings(value) {
  fs.mkdirSync(app.getPath('userData'), { recursive: true });
  fs.writeFileSync(settingsPath(), `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}

function persistSettings(changes = {}) {
  const saved = readSettings();
  writeSettings({ ...saved, server, hotkeys: currentHotkeys, ...changes });
}

async function probeServer(base) {
  const response = await net.fetch(`${base}/api/capabilities`, { cache: 'no-store', signal: AbortSignal.timeout(8000) });
  if (!response.ok) throw new Error(`Сервер ответил HTTP ${response.status}`);
  const capabilities = await response.json();
  if (!Number.isInteger(capabilities.protocolVersion) || capabilities.protocolVersion < 5) throw new Error('Нужен Clipboard Exchange с protocol v5 или новее');
}

async function navigate(input) {
  if (!input) throw new Error('Некорректная ссылка приложения');
  const parsed = typeof input === 'string' ? normalizeTarget(input) : input;
  await probeServer(parsed.server);
  server = parsed.server;
  persistSettings({ server });
  await mainWindow.loadURL(parsed.target);
  return true;
}

async function showLauncher(message = '') {
  mainWindow.show();
  await mainWindow.loadFile(path.join(__dirname, 'launcher.html'), { query: message ? { error: message } : {} });
}

function secureWebPreferences(preload) {
  return { preload: path.join(__dirname, preload), contextIsolation: true, nodeIntegration: false, sandbox: true };
}

function createMainWindow() {
  mainWindow = new BrowserWindow({
    width: 1040, height: 760, minWidth: 420, minHeight: 560,
    title: 'Clipboard Exchange', backgroundColor: '#f4f7f6',
    icon: path.join(__dirname, '..', 'assets', 'icon.png'),
    webPreferences: { ...secureWebPreferences('preload.cjs'), backgroundThrottling: false }
  });
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (server && isAllowedNavigation(url, server)) void mainWindow.loadURL(url);
    else if (/^https?:\/\//i.test(url)) void shell.openExternal(url);
    return { action: 'deny' };
  });
  mainWindow.webContents.on('will-navigate', (event, url) => {
    if (url.startsWith('file:')) return;
    if (!server || !isAllowedNavigation(url, server)) {
      event.preventDefault();
      if (/^https?:\/\//i.test(url)) void shell.openExternal(url);
    }
  });
  mainWindow.webContents.session.setPermissionCheckHandler((_webContents, permission, requestingOrigin) => {
    return Boolean(server && requestingOrigin === new URL(server).origin && ['clipboard-read', 'clipboard-sanitized-write', 'notifications', 'media'].includes(permission));
  });
  mainWindow.webContents.session.setPermissionRequestHandler((webContents, permission, callback) => {
    const origin = new URL(webContents.getURL()).origin;
    callback(Boolean(server && origin === new URL(server).origin && ['clipboard-read', 'clipboard-sanitized-write', 'notifications', 'media'].includes(permission)));
  });
  mainWindow.on('close', (event) => {
    if (!app.isQuitting) { event.preventDefault(); mainWindow.hide(); }
  });
}

async function createAuxiliaryWindows() {
  pickerWindow = new BrowserWindow({
    width: 540, height: 430, show: false, frame: false, transparent: true,
    alwaysOnTop: true, skipTaskbar: true, resizable: false, webPreferences: secureWebPreferences('picker-preload.cjs')
  });
  pickerWindow.on('blur', () => pickerWindow.hide());
  await pickerWindow.loadFile(path.join(__dirname, 'picker.html'));

  latestWindow = new BrowserWindow({
    width: 520, height: 170, show: false, frame: false, transparent: true,
    alwaysOnTop: true, skipTaskbar: true, resizable: false, focusable: false,
    webPreferences: secureWebPreferences('latest-preload.cjs')
  });
  await latestWindow.loadFile(path.join(__dirname, 'latest.html'));
}

async function sendText(text, reveal = false) {
  if (!text) throw new Error('Нет текста для отправки');
  if (!mainWindow || !/^https?:/.test(mainWindow.webContents.getURL())) throw new Error('Сначала откройте комнату');
  const encoded = JSON.stringify(text);
  const sent = await mainWindow.webContents.executeJavaScript(`(() => {
    const field = document.getElementById('item-text');
    if (!field || field.disabled || !field.form) return false;
    field.value = ${encoded};
    field.dispatchEvent(new Event('input', { bubbles: true }));
    field.form.requestSubmit();
    return true;
  })()`, true);
  if (!sent) throw new Error('Текущая страница не является доступной для записи комнатой');
  if (reveal) { mainWindow.show(); mainWindow.focus(); }
}

async function sendClipboard() {
  const text = clipboard.readText();
  if (!text) throw new Error('В буфере обмена нет текста');
  await sendText(text);
}

async function sendSelection() {
  const text = await automation.captureSelection();
  await sendText(text);
}

async function recentMessages() {
  if (!mainWindow || !/^https?:/.test(mainWindow.webContents.getURL())) throw new Error('Сначала откройте комнату');
  const raw = await mainWindow.webContents.executeJavaScript(`(() => {
    if (Array.isArray(globalThis.clipboardExchangeDesktopMessages)) return globalThis.clipboardExchangeDesktopMessages;
    return Array.from(document.querySelectorAll('#items article.item')).map((article) => ({
      text: article.querySelector('.item-content')?.textContent || '',
      alias: article.querySelector('.item-alias')?.textContent || '',
      createdAt: article.querySelector('time')?.dateTime || ''
    }));
  })()`, true);
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((message) => message && typeof message.text === 'string' && message.text)
    .map((message) => ({
      text: message.text.slice(0, 65536),
      alias: typeof message.alias === 'string' ? message.alias.slice(0, 128) : '',
      createdAt: typeof message.createdAt === 'string' ? message.createdAt : ''
    }))
    .sort((left, right) => new Date(right.createdAt) - new Date(left.createdAt))
    .slice(0, 30);
}

function placeNearCursor(window) {
  const cursor = screen.getCursorScreenPoint();
  const workArea = screen.getDisplayNearestPoint(cursor).workArea;
  const bounds = window.getBounds();
  const x = Math.max(workArea.x, Math.min(cursor.x - Math.round(bounds.width / 2), workArea.x + workArea.width - bounds.width));
  const y = Math.max(workArea.y, Math.min(cursor.y + 18, workArea.y + workArea.height - bounds.height));
  window.setPosition(x, y, false);
}

async function showHistory() {
  placeNearCursor(pickerWindow);
  pickerWindow.webContents.send('desktop:picker-messages', pickerMessages.length ? pickerMessages : null);
  pickerWindow.show();
  pickerWindow.focus();
  try {
    const current = await recentMessages();
    pickerMessages = current;
    if (pickerWindow.isVisible()) pickerWindow.webContents.send('desktop:picker-messages', current);
  } catch (error) {
    pickerWindow.hide();
    throw error;
  }
}

async function showLatest() {
  const [message] = await recentMessages();
  if (!message) throw new Error('В комнате пока нет текстовых сообщений');
  const workArea = screen.getDisplayNearestPoint(screen.getCursorScreenPoint()).workArea;
  const bounds = latestWindow.getBounds();
  latestWindow.setPosition(workArea.x + workArea.width - bounds.width - 20, workArea.y + 20, false);
  latestWindow.webContents.send('desktop:latest-message', message);
  latestWindow.showInactive();
  clearTimeout(latestTimer);
  latestTimer = setTimeout(() => latestWindow?.hide(), 6500);
}

function reportActionError(error) {
  if (mainWindow) void dialog.showMessageBox(mainWindow, { type: 'warning', title: 'Clipboard Exchange', message: error.message });
}

const hotkeyActions = {
  sendClipboard: () => void sendClipboard().catch(reportActionError),
  sendSelection: () => void sendSelection().catch(reportActionError),
  showLatest: () => void showLatest().catch(reportActionError),
  showHistory: () => void showHistory().catch(reportActionError)
};

function registerHotkeys(next) {
  const normalized = normalizeHotkeys(next);
  globalShortcut.unregisterAll();
  hotkeysRegistered = false;
  try {
    for (const [action, accelerator] of Object.entries(normalized)) {
      if (!globalShortcut.register(accelerator, hotkeyActions[action])) throw new Error(`Хоткей ${displayHotkey(accelerator)} уже занят системой или другим приложением`);
    }
  } catch (error) {
    globalShortcut.unregisterAll();
    for (const [action, accelerator] of Object.entries(currentHotkeys)) globalShortcut.register(accelerator, hotkeyActions[action]);
    hotkeysRegistered = true;
    throw error;
  }
  currentHotkeys = normalized;
  hotkeysRegistered = true;
  refreshMenus();
}

function showHotkeySettings() {
  if (settingsWindow) { settingsWindow.show(); settingsWindow.focus(); return; }
  globalShortcut.unregisterAll();
  hotkeysRegistered = false;
  settingsWindow = new BrowserWindow({
    width: 700, height: 550, minWidth: 580, minHeight: 500,
    title: 'Хоткеи — Clipboard Exchange', parent: mainWindow, modal: false,
    backgroundColor: '#f4f7f6', webPreferences: secureWebPreferences('settings-preload.cjs')
  });
  settingsWindow.setMenu(null);
  void settingsWindow.loadFile(path.join(__dirname, 'settings.html'));
  settingsWindow.on('closed', () => {
    settingsWindow = undefined;
    if (!hotkeysRegistered) {
      try { registerHotkeys(currentHotkeys); } catch (error) { reportActionError(error); }
    }
  });
}

function refreshMenus() {
  const label = (action) => displayHotkey(currentHotkeys[action]);
  if (tray) tray.setContextMenu(Menu.buildFromTemplate([
    { label: 'Показать приложение', click: () => { mainWindow.show(); mainWindow.focus(); } },
    { type: 'separator' },
    { label: `Отправить буфер (${label('sendClipboard')})`, click: hotkeyActions.sendClipboard },
    { label: `Отправить выделение (${label('sendSelection')})`, click: hotkeyActions.sendSelection },
    { label: `Показать последнее (${label('showLatest')})`, click: hotkeyActions.showLatest },
    { label: `Последние сообщения… (${label('showHistory')})`, click: hotkeyActions.showHistory },
    { type: 'separator' },
    { label: 'Настроить хоткеи…', click: showHotkeySettings },
    { label: 'Сменить сервер…', click: () => void showLauncher() },
    { label: 'Выход', click: () => { app.isQuitting = true; app.quit(); } }
  ]));
  const template = [
    ...(process.platform === 'darwin' ? [{ role: 'appMenu' }] : []),
    { label: 'Приложение', submenu: [
      { label: 'Отправить буфер обмена', click: hotkeyActions.sendClipboard },
      { label: 'Отправить выделенный текст', click: hotkeyActions.sendSelection },
      { label: 'Показать последнее сообщение', click: hotkeyActions.showLatest },
      { label: 'Последние сообщения…', click: hotkeyActions.showHistory },
      { type: 'separator' },
      { label: 'Настроить хоткеи…', click: showHotkeySettings },
      { label: 'Сменить сервер…', click: () => void showLauncher() },
      { type: 'separator' },
      process.platform === 'darwin' ? { role: 'close' } : { role: 'quit' }
    ] },
    { role: 'editMenu' }, { role: 'viewMenu' }, { role: 'windowMenu' }
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

function createTray() {
  const image = nativeImage.createFromPath(path.join(__dirname, '..', 'assets', 'icon.png')).resize({ width: 20, height: 20 });
  if (image.isEmpty()) return;
  tray = new Tray(image);
  tray.setToolTip('Clipboard Exchange');
  tray.on('double-click', () => { mainWindow.show(); mainWindow.focus(); });
  refreshMenus();
}

if (hasSingleInstanceLock) {
  ipcMain.handle('desktop:connect', async (event, target) => {
    if (!event.senderFrame.url.startsWith('file:')) throw new Error('Недоступно для удалённой страницы');
    return navigate(target);
  });
  ipcMain.handle('desktop:get-hotkeys', (event) => {
    if (!settingsWindow || event.sender !== settingsWindow.webContents) throw new Error('Недоступно');
    return currentHotkeys;
  });
  ipcMain.handle('desktop:save-hotkeys', (event, hotkeys) => {
    if (!settingsWindow || event.sender !== settingsWindow.webContents) throw new Error('Недоступно');
    registerHotkeys(hotkeys);
    persistSettings({ hotkeys: currentHotkeys });
    settingsWindow.close();
    return true;
  });
  ipcMain.on('desktop:close-settings', (event) => { if (settingsWindow && event.sender === settingsWindow.webContents) settingsWindow.close(); });
  ipcMain.on('desktop:close-picker', (event) => { if (pickerWindow && event.sender === pickerWindow.webContents) pickerWindow.hide(); });
  ipcMain.on('desktop:close-latest', (event) => { if (latestWindow && event.sender === latestWindow.webContents) latestWindow.hide(); });
  ipcMain.handle('desktop:paste-message', async (event, index) => {
    if (!pickerWindow || event.sender !== pickerWindow.webContents || !Number.isInteger(index) || !pickerMessages[index]) throw new Error('Сообщение недоступно');
    const text = pickerMessages[index].text;
    pickerWindow.hide();
    await new Promise((resolve) => setTimeout(resolve, 140));
    await automation.pasteText(text);
    return true;
  });

  app.on('second-instance', (_event, argv) => {
    const value = argv.find((argument) => argument.startsWith(`${APP_PROTOCOL}://`));
    if (value) void navigate(targetFromDeepLink(value)).catch(reportActionError);
    if (mainWindow) { mainWindow.show(); mainWindow.focus(); }
  });
  app.on('open-url', (event, url) => {
    event.preventDefault();
    if (!mainWindow) pendingDeepLink = url;
    else void navigate(targetFromDeepLink(url)).catch(reportActionError);
  });
  app.on('before-quit', () => { app.isQuitting = true; clearTimeout(latestTimer); });
  app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });
  app.on('activate', () => { if (mainWindow) { mainWindow.show(); mainWindow.focus(); } });

  void app.whenReady().then(async () => {
    app.setAppUserModelId('com.clipboardexchange.desktop');
    if (process.defaultApp && process.argv[1]) app.setAsDefaultProtocolClient(APP_PROTOCOL, process.execPath, [path.resolve(process.argv[1])]);
    else app.setAsDefaultProtocolClient(APP_PROTOCOL);
    const saved = readSettings();
    server = saved.server;
    currentHotkeys = saved.hotkeys;
    const helperPath = app.isPackaged
      ? path.join(process.resourcesPath, 'native', 'clipboard-exchange-windows-helper.exe')
      : path.join(__dirname, '..', 'native', 'bin', 'clipboard-exchange-windows-helper.exe');
    automation = createAutomation({ helperPath });
    createMainWindow();
    await createAuxiliaryWindows();
    createTray();
    try { registerHotkeys(currentHotkeys); } catch (error) {
      currentHotkeys = { ...DEFAULT_HOTKEYS };
      try { registerHotkeys(currentHotkeys); } catch (_) { /* menu actions remain available */ }
      reportActionError(error);
    }

    const deepLink = pendingDeepLink && targetFromDeepLink(pendingDeepLink);
    if (deepLink) await navigate(deepLink).catch((error) => showLauncher(error.message));
    else if (saved.server) await navigate(saved.server).catch((error) => showLauncher(error.message));
    else await showLauncher();
  });
}
