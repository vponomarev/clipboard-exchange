'use strict';

const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('desktopPicker', Object.freeze({
  onMessages: (callback) => ipcRenderer.on('desktop:picker-messages', (_event, messages) => callback(messages)),
  paste: (index) => ipcRenderer.invoke('desktop:paste-message', index),
  close: () => ipcRenderer.send('desktop:close-picker')
}));
