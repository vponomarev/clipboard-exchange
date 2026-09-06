'use strict';

const DEFAULT_HOTKEYS = Object.freeze({
  sendClipboard: 'CommandOrControl+Shift+Alt+V',
  sendSelection: 'CommandOrControl+Shift+Alt+S',
  showLatest: 'CommandOrControl+Shift+Alt+L',
  showHistory: 'CommandOrControl+Shift+Alt+H'
});

const HOTKEY_KEYS = Object.freeze(Object.keys(DEFAULT_HOTKEYS));

function normalizeHotkeys(value) {
  const result = {};
  for (const key of HOTKEY_KEYS) {
    const accelerator = typeof value?.[key] === 'string' ? value[key].trim() : DEFAULT_HOTKEYS[key];
    if (!accelerator || accelerator.length > 80) throw new Error('Каждое действие должно иметь хоткей');
    result[key] = accelerator;
  }
  const folded = Object.values(result).map((accelerator) => accelerator.toLocaleLowerCase('en-US'));
  if (new Set(folded).size !== folded.length) throw new Error('Хоткеи разных действий не должны совпадать');
  return result;
}

function displayHotkey(accelerator, platform = process.platform) {
  return accelerator
    .replace('CommandOrControl', platform === 'darwin' ? 'Cmd' : 'Ctrl')
    .replaceAll('Alt', platform === 'darwin' ? 'Option' : 'Alt');
}

module.exports = { DEFAULT_HOTKEYS, HOTKEY_KEYS, normalizeHotkeys, displayHotkey };
