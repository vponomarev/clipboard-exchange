'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { DEFAULT_HOTKEYS, normalizeHotkeys, displayHotkey } = require('../src/hotkeys.cjs');

test('default hotkeys are complete and unique', () => {
  assert.deepEqual(normalizeHotkeys({}), DEFAULT_HOTKEYS);
  assert.equal(new Set(Object.values(DEFAULT_HOTKEYS)).size, 4);
});

test('custom hotkeys are normalized and duplicate bindings are rejected', () => {
  const custom = normalizeHotkeys({ ...DEFAULT_HOTKEYS, showLatest: '  F8 ' });
  assert.equal(custom.showLatest, 'F8');
  assert.throws(() => normalizeHotkeys({ ...DEFAULT_HOTKEYS, showLatest: DEFAULT_HOTKEYS.showHistory }), /совпадать/);
});

test('hotkeys have platform-friendly labels', () => {
  assert.equal(displayHotkey(DEFAULT_HOTKEYS.sendClipboard, 'win32'), 'Ctrl+Shift+Alt+V');
  assert.equal(displayHotkey(DEFAULT_HOTKEYS.sendClipboard, 'darwin'), 'Cmd+Shift+Option+V');
});
