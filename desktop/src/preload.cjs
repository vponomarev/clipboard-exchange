'use strict';

const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('clipboardExchangeDesktop', Object.freeze({
  connect: (target) => ipcRenderer.invoke('desktop:connect', target),
  platform: process.platform
}));
