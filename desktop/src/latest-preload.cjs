'use strict';

const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('desktopLatest', Object.freeze({
  onMessage: (callback) => ipcRenderer.on('desktop:latest-message', (_event, message) => callback(message)),
  close: () => ipcRenderer.send('desktop:close-latest')
}));
