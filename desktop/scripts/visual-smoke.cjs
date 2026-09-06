'use strict';

const path = require('node:path');
const { chromium } = require('../../node_modules/@playwright/test');

const fileURL = (name) => `file:///${path.resolve(__dirname, '..', 'src', name).replaceAll('\\', '/')}`;
const output = (name) => path.resolve(__dirname, '..', '..', 'test-results', name);

void (async () => {
  const browser = await chromium.launch({ headless: true });
  const settings = await browser.newPage({ viewport: { width: 700, height: 550 } });
  await settings.addInitScript(() => { window.desktopSettings={load:async()=>({sendClipboard:'CommandOrControl+Shift+Alt+V',sendSelection:'CommandOrControl+Shift+Alt+S',showLatest:'CommandOrControl+Shift+Alt+L',showHistory:'CommandOrControl+Shift+Alt+H'}),save:async()=>true,close:()=>{}}; });
  await settings.goto(fileURL('settings.html')); await settings.screenshot({ path: output('desktop-settings.png') });

  const picker = await browser.newPage({ viewport: { width: 540, height: 430 } });
  await picker.addInitScript(() => { window.desktopPicker={onMessages:(callback)=>{window.__messages=callback;},paste:async()=>true,close:()=>{}}; });
  await picker.goto(fileURL('picker.html'));
  await picker.evaluate(() => window.__messages([{text:'Первое и самое свежее сообщение\nс новой строки',alias:'Ноутбук',createdAt:new Date().toISOString()},{text:'Адрес для подключения: https://example.test/r/home',alias:'Телефон',createdAt:new Date(Date.now()-3600000).toISOString()},{text:'Короткая заметка',alias:'',createdAt:new Date(Date.now()-86400000).toISOString()}]));
  await picker.screenshot({ path: output('desktop-picker.png'), omitBackground: true });

  const latest = await browser.newPage({ viewport: { width: 520, height: 170 } });
  await latest.addInitScript(() => { window.desktopLatest={onMessage:(callback)=>{window.__latest=callback;},close:()=>{}}; });
  await latest.goto(fileURL('latest.html'));
  await latest.evaluate(() => window.__latest({text:'Последнее сообщение из Exchange — без изменения clipboard.',alias:'MacBook',createdAt:new Date().toISOString()}));
  await latest.screenshot({ path: output('desktop-latest.png'), omitBackground: true });
  await browser.close();
})();
