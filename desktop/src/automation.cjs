'use strict';

const { execFile: execFileCallback } = require('node:child_process');
const { promisify } = require('node:util');
const path = require('node:path');

const execFile = promisify(execFileCallback);

function chunks(value, size = 2000) {
  const result = [];
  for (let offset = 0; offset < value.length;) {
    let end = Math.min(value.length, offset + size);
    if (end < value.length && value.charCodeAt(end - 1) >= 0xD800 && value.charCodeAt(end - 1) <= 0xDBFF) end--;
    result.push(value.slice(offset, end));
    offset = end;
  }
  return result;
}

function createAutomation({ platform = process.platform, helperPath = path.join(__dirname, '..', 'native', 'bin', 'clipboard-exchange-windows-helper.exe'), run = execFile }) {
  async function captureSelection() {
    let selected = '';
    if (platform === 'win32') {
      const { stdout } = await run(helperPath, ['selection'], { windowsHide: true });
      selected = Buffer.from(String(stdout).trim(), 'base64').toString('utf8');
    } else if (platform === 'darwin') {
      const script = 'tell application "System Events" to tell first application process whose frontmost is true to get value of attribute "AXSelectedText" of focused UI element';
      const { stdout } = await run('/usr/bin/osascript', ['-e', script]);
      selected = String(stdout).replace(/\r?\n$/, '');
    } else {
      const { stdout } = await run('xclip', ['-o', '-selection', 'primary']);
      selected = String(stdout);
    }
    if (!selected) throw new Error('Не удалось получить выделенный текст: приложение не предоставляет его через Accessibility/UI Automation');
    return selected;
  }

  async function pasteText(text) {
    if (!text) throw new Error('Сообщение не содержит текста');
    for (const part of chunks(text)) {
      if (platform === 'win32') {
        const encoded = Buffer.from(part, 'utf8').toString('base64');
        await run(helperPath, ['type', encoded], { windowsHide: true });
      } else if (platform === 'darwin') {
        const script = 'on run argv\ndelay 0.1\ntell application "System Events" to keystroke (item 1 of argv)\nend run';
        await run('/usr/bin/osascript', ['-e', script, part]);
      } else {
        await run('xdotool', ['type', '--clearmodifiers', '--delay', '0', '--', part]);
      }
    }
  }

  return { captureSelection, pasteText };
}

module.exports = { chunks, createAutomation };
