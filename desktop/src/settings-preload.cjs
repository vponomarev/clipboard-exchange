'use strict';

const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('desktopSettings', Object.freeze({
  load: () => ipcRenderer.invoke('desktop:get-hotkeys'),
  save: (hotkeys) => ipcRenderer.invoke('desktop:save-hotkeys', hotkeys),
  close: () => ipcRenderer.send('desktop:close-settings'),
  platform: process.platform
}));
