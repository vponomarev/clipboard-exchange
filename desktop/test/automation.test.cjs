'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { chunks, createAutomation } = require('../src/automation.cjs');

test('long text is split without changing its contents', () => {
  const text = `${'a'.repeat(1999)}😀${'abc'.repeat(1400)}`;
  assert.equal(chunks(text).join(''), text);
  assert.equal(chunks(text).length, 4);
  assert.ok(chunks(text).every((part) => !/^[\uDC00-\uDFFF]|[\uD800-\uDBFF]$/.test(part)));
});

test('Windows selection is decoded from the UI Automation helper', async () => {
  const selected = 'выделено\nточно';
  const automation = createAutomation({ platform: 'win32', run: async (_file, args) => {
    assert.ok(args.includes('selection'));
    return { stdout: `${Buffer.from(selected).toString('base64')}\r\n` };
  } });
  assert.equal(await automation.captureSelection(), selected);
});

test('Windows insertion sends Unicode chunks without using clipboard', async () => {
  const received = [];
  const automation = createAutomation({ platform: 'win32', run: async (_file, args) => {
    assert.equal(args[0], 'type');
    const encoded = args[1];
    received.push(Buffer.from(encoded, 'base64').toString('utf8'));
    return { stdout: '' };
  } });
  const text = `${'я'.repeat(2100)}\nfinished`;
  await automation.pasteText(text);
  assert.equal(received.join(''), text);
  assert.equal(received.length, 2);
});
