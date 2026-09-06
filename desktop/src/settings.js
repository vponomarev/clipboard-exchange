'use strict';

const form = document.getElementById('hotkeys-form');
const error = document.getElementById('error');
const inputs = Array.from(document.querySelectorAll('[data-hotkey]'));

function displayAccelerator(value) {
  return value
    .replace('CommandOrControl', window.desktopSettings.platform === 'darwin' ? 'Cmd' : 'Ctrl')
    .replaceAll('Alt', window.desktopSettings.platform === 'darwin' ? 'Option' : 'Alt');
}

function setAccelerator(input, value) {
  input.dataset.accelerator = value;
  input.value = displayAccelerator(value);
}

function acceleratorFromEvent(event) {
  if (event.key === 'Escape') return null;
  if (event.key === 'Backspace' || event.key === 'Delete') return '';
  if (['Control', 'Meta', 'Shift', 'Alt', 'AltGraph'].includes(event.key)) return undefined;
  let key = event.key;
  if (key === ' ') key = 'Space';
  else if (/^F(?:[1-9]|1\d|2[0-4])$/i.test(key)) key = key.toUpperCase();
  else if (key.length === 1 && /[A-Za-z0-9]/.test(key)) key = key.toUpperCase();
  else return undefined;
  const modifiers = [];
  if (event.ctrlKey || event.metaKey) modifiers.push('CommandOrControl');
  if (event.shiftKey) modifiers.push('Shift');
  if (event.altKey) modifiers.push('Alt');
  if (!modifiers.length && !key.startsWith('F')) return undefined;
  return [...modifiers, key].join('+');
}

for (const input of inputs) {
  input.addEventListener('keydown', (event) => {
    event.preventDefault();
    event.stopPropagation();
    const accelerator = acceleratorFromEvent(event);
    if (accelerator === null) { window.desktopSettings.close(); return; }
    if (accelerator === undefined) { error.textContent = 'Используйте Ctrl/Cmd, Alt или функциональную клавишу F1–F24'; return; }
    setAccelerator(input, accelerator);
    error.textContent = '';
  });
}

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  error.textContent = '';
  const hotkeys = Object.fromEntries(inputs.map((input) => [input.dataset.hotkey, input.dataset.accelerator || '']));
  try { await window.desktopSettings.save(hotkeys); }
  catch (problem) { error.textContent = problem.message || 'Не удалось зарегистрировать хоткеи'; }
});
document.getElementById('cancel').addEventListener('click', () => window.desktopSettings.close());

void window.desktopSettings.load().then((hotkeys) => {
  for (const input of inputs) setAccelerator(input, hotkeys[input.dataset.hotkey] || '');
});
