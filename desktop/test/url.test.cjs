'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { normalizeTarget, targetFromDeepLink, isAllowedNavigation } = require('../src/url.cjs');

test('normalizes server and room links', () => {
  assert.deepEqual(normalizeTarget('example.test'), { target: 'https://example.test/', server: 'https://example.test' });
  assert.deepEqual(normalizeTarget('http://box.local:8080/r/home#write=cw1_secret'), {
    target: 'http://box.local:8080/r/home#write=cw1_secret', server: 'http://box.local:8080'
  });
});

test('rejects credentials, unsupported paths and protocols', () => {
  assert.throws(() => normalizeTarget('https://me:secret@example.test'), /логин/);
  assert.throws(() => normalizeTarget('https://example.test/admin'), /адрес сервера/);
  assert.throws(() => normalizeTarget('file:///tmp/index.html'), /HTTP/);
});

test('parses a desktop deep link without leaking its fragment', () => {
  const room = 'https://example.test/r/home%23key%3Dce1_secret';
  assert.equal(targetFromDeepLink(`clipboard-exchange://open?url=${room}`).target, 'https://example.test/r/home#key=ce1_secret');
  assert.equal(targetFromDeepLink('clipboard-exchange://open'), null);
});

test('allows navigation only inside the configured origin', () => {
  assert.equal(isAllowedNavigation('https://example.test/r/a', 'https://example.test'), true);
  assert.equal(isAllowedNavigation('https://evil.test/r/a', 'https://example.test'), false);
  assert.equal(isAllowedNavigation('file:///etc/passwd', 'https://example.test'), false);
});
