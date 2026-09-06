'use strict';

const ROOM_PATH = /^\/r\/[A-Za-z0-9][A-Za-z0-9_-]{0,63}\/?$/;
const SHORT_PATH = /^\/s\/[23456789ABCDEFGHJKMNPQRSTVWXYZ]{4,6}\/?$/i;

function normalizeTarget(input) {
  const value = String(input || '').trim();
  if (!value) throw new Error('Введите адрес сервера или ссылку комнаты');
  if (/^[a-z][a-z0-9+.-]*:/i.test(value) && !/^https?:\/\//i.test(value)) {
    throw new Error('Поддерживаются только HTTP и HTTPS');
  }

  let target;
  try {
    target = new URL(/^https?:\/\//i.test(value) ? value : `https://${value}`);
  } catch (_) {
    throw new Error('Некорректный адрес');
  }
  if (!['http:', 'https:'].includes(target.protocol)) throw new Error('Поддерживаются только HTTP и HTTPS');
  if (target.username || target.password) throw new Error('Адрес не должен содержать логин или пароль');
  if (target.pathname !== '/' && !ROOM_PATH.test(target.pathname) && !SHORT_PATH.test(target.pathname)) {
    throw new Error('Укажите адрес сервера, комнаты /r/... или короткую ссылку /s/...');
  }
  target.search = '';
  const server = new URL(target.origin);
  return { target: target.toString(), server: server.toString().replace(/\/$/, '') };
}

function targetFromDeepLink(value) {
  let deepLink;
  try { deepLink = new URL(value); } catch (_) { return null; }
  if (deepLink.protocol !== 'clipboard-exchange:') return null;
  const target = deepLink.searchParams.get('url');
  if (!target) return null;
  try { return normalizeTarget(target); } catch (_) { return null; }
}

function isAllowedNavigation(value, server) {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) && url.origin === new URL(server).origin;
  } catch (_) {
    return false;
  }
}

module.exports = { normalizeTarget, targetFromDeepLink, isAllowedNavigation };
