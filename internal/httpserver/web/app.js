(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const shellVersion = document.querySelector('meta[name="clipboard-exchange-version"]')?.content || "unknown";
  const roomMatch = location.pathname.match(/^\/r\/([A-Za-z0-9][A-Za-z0-9_-]{0,63})$/);
  const shortMatch = location.pathname.match(/^\/s\/([23456789ABCDEFGHJKMNPQRSTVWXYZ]{4,6})$/i);
  const fragment = new URLSearchParams(location.hash.slice(1));
  const state = { roomID: roomMatch ? roomMatch[1] : "", room: null, key: null, keyText: "", writeToken: fragment.get("write") || "", canWrite: Boolean(fragment.get("write")), entries: [], items: [], files: [], renderedEntries: [], pendingFiles: [], capabilities: null, downloadRegistration: null, appRegistration: null, updateReady: false, updateChecking: false, socket: null, refreshTimer: 0, previousEntryIDs: new Set(), hasRefreshed: false, unread: 0, previewIndex: 0, installPrompt: null, scanStream: null, scanTimer: 0, scanDetector: null };
  const encoder = new TextEncoder();
  const decoder = new TextDecoder("utf-8", { fatal: true });
  const shortLinkAAD = encoder.encode("clipboard-exchange-short-link:v1");
  const shortLinkPayloadBytes = 512;
  const shortLinkKDFIterations = 600000;

  function b64url(bytes) {
    let binary = "";
    bytes.forEach((b) => { binary += String.fromCharCode(b); });
    return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
  }
  function fromB64url(value) {
    const normalized = value.replaceAll("-", "+").replaceAll("_", "/");
    const binary = atob(normalized + "===".slice((normalized.length + 3) % 4));
    return Uint8Array.from(binary, (c) => c.charCodeAt(0));
  }
  async function sha256(bytes) { return new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)); }
  async function sha256Hex(bytes) {
    if (globalThis.crypto?.subtle) return Array.from(await sha256(bytes), value => value.toString(16).padStart(2, "0")).join("");
    return sha256HexFallback(bytes);
  }
  function sha256HexFallback(input) {
    const k = [0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2];
    const h = [0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19];
    const bytes = input instanceof Uint8Array ? input : new Uint8Array(input);
    const padded = new Uint8Array((bytes.length + 9 + 63) & ~63); padded.set(bytes); padded[bytes.length] = 0x80;
    const view = new DataView(padded.buffer); const bits = bytes.length * 8;
    view.setUint32(padded.length - 8, Math.floor(bits / 0x100000000)); view.setUint32(padded.length - 4, bits >>> 0);
    const w = new Uint32Array(64), rotate = (x,n) => (x >>> n) | (x << (32-n));
    for (let offset = 0; offset < padded.length; offset += 64) {
      for (let i=0;i<16;i++) w[i]=view.getUint32(offset+i*4);
      for (let i=16;i<64;i++) { const a=w[i-15], b=w[i-2]; const s0=rotate(a,7)^rotate(a,18)^(a>>>3), s1=rotate(b,17)^rotate(b,19)^(b>>>10); w[i]=(w[i-16]+s0+w[i-7]+s1)>>>0; }
      let [a,b,c,d,e,f,g,z]=h;
      for (let i=0;i<64;i++) { const s1=rotate(e,6)^rotate(e,11)^rotate(e,25), ch=(e&f)^(~e&g), t1=(z+s1+ch+k[i]+w[i])>>>0, s0=rotate(a,2)^rotate(a,13)^rotate(a,22), maj=(a&b)^(a&c)^(b&c), t2=(s0+maj)>>>0; z=g; g=f; f=e; e=(d+t1)>>>0; d=c; c=b; b=a; a=(t1+t2)>>>0; }
      h[0]=(h[0]+a)>>>0; h[1]=(h[1]+b)>>>0; h[2]=(h[2]+c)>>>0; h[3]=(h[3]+d)>>>0; h[4]=(h[4]+e)>>>0; h[5]=(h[5]+f)>>>0; h[6]=(h[6]+g)>>>0; h[7]=(h[7]+z)>>>0;
    }
    return h.map(value => value.toString(16).padStart(8,"0")).join("");
  }
  async function importRawKey(bytes) { return crypto.subtle.importKey("raw", bytes, "AES-GCM", false, ["encrypt", "decrypt"]); }
  async function keyID(bytes) { return b64url(await sha256(bytes)); }
  async function keyFromInput(value) {
    if (value.startsWith("ce1_")) {
      const raw = fromB64url(value.slice(4));
      if (raw.length !== 32) throw new Error("Некорректный ключ");
      return raw;
    }
    const material = await crypto.subtle.importKey("raw", encoder.encode(value), "PBKDF2", false, ["deriveBits"]);
    const bits = await crypto.subtle.deriveBits({ name: "PBKDF2", salt: encoder.encode("clipboard-exchange:" + state.roomID), iterations: 310000, hash: "SHA-256" }, material, 256);
    return new Uint8Array(bits);
  }
  function rawKeyText(bytes) { return "ce1_" + b64url(bytes); }

  async function shortLinkKey(pin, salt, iterations) {
    const material = await crypto.subtle.importKey("raw", encoder.encode(pin), "PBKDF2", false, ["deriveKey"]);
    return crypto.subtle.deriveKey({ name:"PBKDF2", salt, iterations, hash:"SHA-256" }, material, { name:"AES-GCM", length:256 }, false, ["encrypt", "decrypt"]);
  }

  async function encryptShortTarget(target, pin) {
    const salt = crypto.getRandomValues(new Uint8Array(16));
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const redemptionToken = b64url(crypto.getRandomValues(new Uint8Array(32)));
    const encoded = encoder.encode(JSON.stringify({ target, redemptionToken }));
    if (encoded.length > shortLinkPayloadBytes - 2) throw new Error("Ссылка комнаты слишком длинная для сокращения");
    const padded = crypto.getRandomValues(new Uint8Array(shortLinkPayloadBytes));
    padded[0] = encoded.length >>> 8; padded[1] = encoded.length & 255; padded.set(encoded, 2);
    const key = await shortLinkKey(pin, salt, shortLinkKDFIterations);
    const ciphertext = new Uint8Array(await crypto.subtle.encrypt({ name:"AES-GCM", iv, additionalData:shortLinkAAD, tagLength:128 }, key, padded));
    return { ciphertext:b64url(ciphertext), iv:b64url(iv), salt:b64url(salt), redemptionToken, tokenHash:await sha256Hex(encoder.encode(redemptionToken)) };
  }

  async function decryptShortTarget(envelope, pin) {
    if (envelope?.version !== 1 || envelope.kdfIterations !== shortLinkKDFIterations) throw new Error("Недоступная короткая ссылка или неверный PIN");
    const key = await shortLinkKey(pin, fromB64url(envelope.salt), envelope.kdfIterations);
    const padded = new Uint8Array(await crypto.subtle.decrypt({ name:"AES-GCM", iv:fromB64url(envelope.iv), additionalData:shortLinkAAD, tagLength:128 }, key, fromB64url(envelope.ciphertext)));
    if (padded.length !== shortLinkPayloadBytes) throw new Error("Недоступная короткая ссылка или неверный PIN");
    const length = padded[0] * 256 + padded[1];
    if (length < 1 || length > padded.length - 2) throw new Error("Недоступная короткая ссылка или неверный PIN");
    const payload = JSON.parse(decoder.decode(padded.slice(2, 2 + length)));
    if (typeof payload.target !== "string" || typeof payload.redemptionToken !== "string" || fromB64url(payload.redemptionToken).length !== 32) throw new Error("Недоступная короткая ссылка или неверный PIN");
    return { target:scannedRoomTarget(payload.target), redemptionToken:payload.redemptionToken };
  }

  async function api(url, options = {}) {
    const { write = false, ...fetchOptions } = options;
    const headers = { ...(fetchOptions.body ? { "Content-Type": "application/json" } : {}), ...fetchOptions.headers };
    if (write && state.room?.writeProtected) {
      if (!state.writeToken) throw new Error("Комната открыта только для чтения");
      headers.Authorization = `ClipboardWrite ${state.writeToken}`;
    }
    const response = await fetch(url, { ...fetchOptions, headers });
    if (response.status === 204) return null;
    const data = await response.json().catch(() => ({}));
    if (!response.ok) { const error = new Error(data.error?.message || `Ошибка HTTP ${response.status}`); error.status = response.status; throw error; }
    return data;
  }

  function uuid() {
    if (typeof globalThis.crypto?.randomUUID === "function") return crypto.randomUUID();
    if (typeof globalThis.crypto?.getRandomValues !== "function") throw new Error("Браузер не поддерживает безопасную генерацию UUID");
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }
  function newWriteToken() { return "cw1_" + b64url(crypto.getRandomValues(new Uint8Array(32))); }
  function loadAlias() { try { return localStorage.getItem("clipboard-exchange:alias") || ""; } catch (_) { return ""; } }
  function saveAlias(value) { try { localStorage.setItem("clipboard-exchange:alias", value); } catch (_) {} }
  function loadRecentRooms() { try { return JSON.parse(localStorage.getItem("clipboard-exchange:rooms") || "[]"); } catch (_) { return []; } }
  function saveRecentRooms(rooms) { try { localStorage.setItem("clipboard-exchange:rooms", JSON.stringify(rooms.slice(0, 50))); } catch (_) {} }
  function updateRecentRoom(changes = {}) {
    if (!state.roomID) return;
    const rooms = loadRecentRooms(); const current = rooms.find(room => room.id === state.roomID) || { id:state.roomID, name:state.roomID, favorite:false };
    Object.assign(current, { encrypted:Boolean(state.room?.encrypted), lastVisited:Date.now() }, changes);
    saveRecentRooms([current, ...rooms.filter(room => room.id !== state.roomID)].sort((a,b) => Number(b.favorite)-Number(a.favorite) || b.lastVisited-a.lastVisited));
  }
  function openPwaDB() {
    return new Promise((resolve, reject) => {
      const request = indexedDB.open("clipboard-exchange-pwa", 2);
      request.onupgradeneeded = () => { if (!request.result.objectStoreNames.contains("shared")) request.result.createObjectStore("shared", { keyPath:"id" }); if (!request.result.objectStoreNames.contains("vault")) request.result.createObjectStore("vault", { keyPath:"id" }); };
      request.onerror = () => reject(request.error); request.onsuccess = () => resolve(request.result);
    });
  }
  async function idbOperation(storeName, mode, operation) {
    const db = await openPwaDB();
    return new Promise((resolve, reject) => { const tx = db.transaction(storeName, mode); const request = operation(tx.objectStore(storeName)); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error); tx.oncomplete = () => db.close(); });
  }
  const idbGet = (store, id) => idbOperation(store, "readonly", objectStore => objectStore.get(id));
  const idbPut = (store, value) => idbOperation(store, "readwrite", objectStore => objectStore.put(value));
  const idbDelete = (store, id) => idbOperation(store, "readwrite", objectStore => objectStore.delete(id));
  async function vaultKey() {
    let record = await idbGet("vault", "device-key");
    if (record?.key) return record.key;
    const key = await crypto.subtle.generateKey({ name:"AES-GCM", length:256 }, false, ["encrypt", "decrypt"]);
    await idbPut("vault", { id:"device-key", key }); return key;
  }
  async function rememberSecrets() {
    if (!crypto?.subtle) throw new Error("Безопасное запоминание ключа требует HTTPS или localhost");
    const key = await vaultKey(); const iv = crypto.getRandomValues(new Uint8Array(12));
    const plaintext = encoder.encode(JSON.stringify({ keyText:state.keyText, writeToken:state.writeToken }));
    const ciphertext = await crypto.subtle.encrypt({ name:"AES-GCM", iv, additionalData:encoder.encode(state.roomID) }, key, plaintext);
    await idbPut("vault", { id:`room:${state.roomID}`, iv:b64url(iv), ciphertext:b64url(new Uint8Array(ciphertext)) });
  }
  async function loadRememberedSecrets() {
    const record = await idbGet("vault", `room:${state.roomID}`).catch(() => null);
    if (!record || !crypto?.subtle) return false;
    try {
      const plaintext = await crypto.subtle.decrypt({ name:"AES-GCM", iv:fromB64url(record.iv), additionalData:encoder.encode(state.roomID) }, await vaultKey(), fromB64url(record.ciphertext));
      const value = JSON.parse(decoder.decode(plaintext));
      if (!state.writeToken && typeof value.writeToken === "string") state.writeToken = value.writeToken;
      if (!fragment.get("key") && typeof value.keyText === "string" && value.keyText) state.keyText = value.keyText;
      return true;
    } catch (_) { await idbDelete("vault", `room:${state.roomID}`).catch(() => {}); return false; }
  }
  function show(id, visible = true) { $(id).classList.toggle("hidden", !visible); }
  function message(id, text) { $(id).textContent = text; show(id, Boolean(text)); }
  function toast(text) {
    $("toast").textContent = text; show("toast");
    clearTimeout(toast.timer); toast.timer = setTimeout(() => show("toast", false), 1800);
  }
  async function copyText(text) {
    try { await navigator.clipboard.writeText(text); }
    catch (_) {
      const area = document.createElement("textarea"); area.value = text; area.style.position = "fixed"; area.style.opacity = "0";
      document.body.append(area); area.select(); document.execCommand("copy"); area.remove();
    }
  }
  function togglePassword(id, button) {
    const input = $(id); input.type = input.type === "password" ? "text" : "password";
    button.setAttribute("aria-label", input.type === "password" ? "Показать ключ" : "Скрыть ключ");
  }

  async function initPWA() {
    updateNetworkState();
    addEventListener("online", () => { updateNetworkState(); toast("Соединение восстановлено"); refreshVersionInfo().catch(() => {}); });
    addEventListener("offline", updateNetworkState);
    $("app-info").addEventListener("click", () => { renderVersionInfo(); $("version-dialog").showModal(); });
    $("version-dialog").querySelector(".dialog-close").addEventListener("click", () => $("version-dialog").close());
    $("check-update").addEventListener("click", checkForUpdate);
    $("scan-qr").addEventListener("click", startQRScanner);
    $("scan-dialog").querySelector(".dialog-close").addEventListener("click", closeQRScanner);
    $("scan-dialog").addEventListener("cancel", stopQRScanner);
    $("scan-dialog").addEventListener("close", stopQRScanner);
    $("scan-link-form").addEventListener("submit", event => { event.preventDefault(); openScannedRoom($("scan-link").value); });
    $("update-app").addEventListener("click", applyUpdate);
    if (navigator.serviceWorker) {
      let hadController = Boolean(navigator.serviceWorker.controller);
      navigator.serviceWorker.addEventListener("controllerchange", () => {
        if (hadController) markUpdateReady();
        hadController = true;
      });
      try {
        state.appRegistration = await navigator.serviceWorker.register("/assets/download-sw.js?v=13", { scope:"/" });
        observeAppRegistration(state.appRegistration);
      } catch (_) {}
    }
    await refreshVersionInfo().catch(() => renderVersionInfo());
    try {
      const previousShell = localStorage.getItem("clipboard-exchange:update-from");
      if (previousShell) {
        localStorage.removeItem("clipboard-exchange:update-from");
        if (previousShell !== shellVersion) toast(`Приложение обновлено до ${shellVersion}`);
      }
    } catch (_) {}
    addEventListener("beforeinstallprompt", event => { event.preventDefault(); state.installPrompt = event; show("install-app"); });
    $("install-app").addEventListener("click", async () => { if (!state.installPrompt) return; await state.installPrompt.prompt(); state.installPrompt = null; show("install-app", false); });
    const query = new URLSearchParams(location.search);
    if (query.get("scan") === "1") {
      query.delete("scan");
      const queryText = query.toString();
      history.replaceState(null, "", `${location.pathname}${queryText ? `?${queryText}` : ""}${location.hash}`);
      setTimeout(startQRScanner, 0);
    }
  }

  function updateNetworkState() {
    show("offline-notice", !navigator.onLine);
    renderVersionInfo();
  }

  function observeAppRegistration(registration) {
    if (registration.waiting) markUpdateReady();
    registration.addEventListener("updatefound", () => {
      const worker = registration.installing;
      const replacingExistingWorker = Boolean(navigator.serviceWorker.controller);
      if (!worker) return;
      if (replacingExistingWorker) $("update-status").textContent = "Загружаем новую версию…";
      worker.addEventListener("statechange", () => {
        if (worker.state === "activated" && replacingExistingWorker) markUpdateReady();
      });
    });
  }

  function markUpdateReady() {
    state.updateReady = true;
    show("update-app");
    renderVersionInfo("Новая версия загружена. Перезапустите приложение, чтобы применить её.");
  }

  function renderVersionInfo(statusText = "") {
    $("shell-version").textContent = shellVersion;
    const serverVersion = state.capabilities?.serverVersion || "—";
    $("server-version").textContent = serverVersion;
    if (statusText) $("update-status").textContent = statusText;
    else if (!navigator.onLine) $("update-status").textContent = "Проверка обновлений недоступна без сети.";
    else if (state.updateChecking) $("update-status").textContent = "Проверяем обновление…";
    else if (state.updateReady) $("update-status").textContent = "Новая версия загружена. Перезапустите приложение, чтобы применить её.";
    else if (serverVersion !== "—" && serverVersion !== shellVersion) $("update-status").textContent = `На сервере доступна версия ${serverVersion}. Выполняется проверка обновления.`;
    else $("update-status").textContent = "Установлена актуальная версия.";
  }

  async function refreshVersionInfo() {
    const capabilities = await api("/api/capabilities", { cache:"no-store" });
    state.capabilities = capabilities;
    renderVersionInfo();
    return capabilities;
  }

  async function checkForUpdate() {
    if (!navigator.onLine) { renderVersionInfo("Подключитесь к сети, чтобы проверить обновление."); return; }
    state.updateChecking = true; $("check-update").disabled = true; renderVersionInfo();
    try {
      await refreshVersionInfo();
      const registration = state.appRegistration || await navigator.serviceWorker?.getRegistration("/");
      if (!registration) { renderVersionInfo("Service Worker недоступен. Обновите страницу средствами браузера."); return; }
      state.appRegistration = registration;
      await registration.update();
      if (registration.waiting) markUpdateReady();
      else if (!state.updateReady && state.capabilities?.serverVersion === shellVersion) renderVersionInfo("Установлена актуальная версия.");
      else if (!state.updateReady) renderVersionInfo(`Найдена версия ${state.capabilities?.serverVersion || "новее текущей"}. Chrome завершает загрузку…`);
    } catch (error) {
      renderVersionInfo(`Не удалось проверить обновление: ${error.message}`);
    } finally {
      state.updateChecking = false; $("check-update").disabled = false;
    }
  }

  function applyUpdate() {
    try { localStorage.setItem("clipboard-exchange:update-from", shellVersion); } catch (_) {}
    location.reload();
  }

  async function startQRScanner() {
    const dialog = $("scan-dialog");
    if (!dialog.open) dialog.showModal();
    $("scan-link").value = "";
    $("scan-status").textContent = "Запрашиваем доступ к камере…";
    try {
      if (!isSecureContext || !navigator.mediaDevices?.getUserMedia) throw new Error("Камера доступна только через HTTPS");
      if (!("BarcodeDetector" in globalThis)) throw new Error("Этот браузер не поддерживает распознавание QR. Вставьте ссылку вручную.");
      const formats = await BarcodeDetector.getSupportedFormats();
      if (!formats.includes("qr_code")) throw new Error("Распознавание QR недоступно на этом устройстве. Вставьте ссылку вручную.");
      const stream = await navigator.mediaDevices.getUserMedia({ audio:false, video:{ facingMode:{ ideal:"environment" }, width:{ ideal:1280 }, height:{ ideal:720 } } });
      if (!dialog.open) { stream.getTracks().forEach(track => track.stop()); return; }
      state.scanStream = stream;
      state.scanDetector = new BarcodeDetector({ formats:["qr_code"] });
      const video = $("scan-video"); video.srcObject = stream; await video.play();
      $("scan-status").textContent = "Наведите камеру на QR-код со ссылкой комнаты.";
      scanQRFrame();
    } catch (error) {
      stopQRScanner();
      $("scan-status").textContent = `Камера недоступна: ${error.message}`;
    }
  }

  async function scanQRFrame() {
    if (!$("scan-dialog").open || !state.scanDetector) return;
    try {
      const video = $("scan-video");
      if (video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
        const codes = await state.scanDetector.detect(video);
        const value = codes.find(code => code.rawValue)?.rawValue;
        if (value && openScannedRoom(value)) return;
      }
    } catch (error) {
      $("scan-status").textContent = `Ошибка распознавания: ${error.message}`;
    }
    state.scanTimer = setTimeout(scanQRFrame, 160);
  }

  function scannedRoomTarget(value) {
    const text = String(value || "").trim();
    if (!text || text.length > 4096) throw new Error("QR-код не содержит корректную ссылку комнаты");
    const url = new URL(text, location.origin);
    if (url.origin !== location.origin || url.username || url.password || !/^\/r\/[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(url.pathname)) {
      throw new Error("Можно открыть только ссылку комнаты этого Clipboard Exchange");
    }
    return `${url.pathname}${url.search}${url.hash}`;
  }

  function openScannedRoom(value) {
    try {
      const target = scannedRoomTarget(value);
      stopQRScanner();
      if ($("scan-dialog").open) $("scan-dialog").close();
      location.assign(target);
      return true;
    } catch (error) {
      $("scan-status").textContent = error.message;
      return false;
    }
  }

  function stopQRScanner() {
    clearTimeout(state.scanTimer); state.scanTimer = 0; state.scanDetector = null;
    state.scanStream?.getTracks().forEach(track => track.stop()); state.scanStream = null;
    $("scan-video").pause(); $("scan-video").srcObject = null;
  }

  function closeQRScanner() {
    stopQRScanner();
    if ($("scan-dialog").open) $("scan-dialog").close();
  }

  function renderRecentRooms() {
    const rooms = loadRecentRooms(); const container = $("recent-rooms"); container.replaceChildren(); show("recent-section", rooms.length > 0);
    for (const room of rooms) {
      const row = document.createElement("div"); row.className = "recent-room";
      const link = document.createElement("a"); link.href = `/r/${encodeURIComponent(room.id)}`;
      const title = document.createElement("strong"); title.textContent = `${room.favorite ? "★ " : ""}${room.name || room.id}`;
      const details = document.createElement("small"); details.textContent = `${room.id}${room.encrypted ? " · зашифрована" : ""}`; link.append(title, details);
      const actions = document.createElement("div"); actions.className = "item-buttons";
      for (const [label, action] of [[room.favorite ? "Убрать ★" : "★", "favorite"], ["Название", "rename"], ["Забыть", "forget"]]) { const button=document.createElement("button"); button.type="button"; button.className="button secondary compact"; button.textContent=label; button.dataset.action=action; button.dataset.room=room.id; actions.append(button); }
      row.append(link, actions); container.append(row);
    }
  }

  async function recentRoomAction(event) {
    const button = event.target.closest("button[data-room]"); if (!button) return;
    let rooms = loadRecentRooms(); const room = rooms.find(value => value.id === button.dataset.room); if (!room) return;
    if (button.dataset.action === "favorite") room.favorite = !room.favorite;
    if (button.dataset.action === "rename") { const name = prompt("Локальное название комнаты", room.name || room.id); if (name !== null) room.name = name.trim().slice(0, 80) || room.id; }
    if (button.dataset.action === "forget") { rooms = rooms.filter(value => value.id !== room.id); await idbDelete("vault", `room:${room.id}`).catch(() => {}); }
    saveRecentRooms(rooms.sort((a,b) => Number(b.favorite)-Number(a.favorite) || b.lastVisited-a.lastVisited)); renderRecentRooms();
  }

  function initHome() {
    show("home");
    const requestedRoom = new URLSearchParams(location.search).get("room");
    $("room-id").value = requestedRoom && /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(requestedRoom) ? requestedRoom : uuid();
    $("random-id").addEventListener("click", () => { $("room-id").value = uuid(); });
    $("encrypted").addEventListener("change", () => show("encryption-options", $("encrypted").checked));
    $("toggle-create-key").addEventListener("click", (e) => togglePassword("room-key", e.currentTarget));
    $("create-form").addEventListener("submit", createRoom);
	$("recent-rooms").addEventListener("click", recentRoomAction);
	$("forget-all-rooms").addEventListener("click", async () => { if (!confirm("Удалить локальную историю комнат и сохранённые ключи?")) return; for (const room of loadRecentRooms()) await idbDelete("vault", `room:${room.id}`).catch(() => {}); saveRecentRooms([]); renderRecentRooms(); });
	renderRecentRooms();
  }

  function initShortLink() {
    const code = shortMatch[1].toUpperCase();
    show("short-link");
    $("short-open-code").value = code;
    $("short-open-form").addEventListener("submit", async (event) => {
      event.preventDefault(); message("short-open-error", "");
      const pin = $("short-open-pin").value;
      const submit = $("short-open-submit");
      if (!/^\d{4}$/.test(pin)) { message("short-open-error", "Введите PIN из четырёх цифр"); return; }
      if (!globalThis.crypto?.subtle) { message("short-open-error", "Открытие защищённой ссылки требует HTTPS или localhost"); return; }
      submit.disabled = true;
      try {
        const envelope = await api(`/api/short-links/${code}`, { cache:"no-store" });
        const payload = await decryptShortTarget(envelope, pin);
        await api(`/api/short-links/${code}/redeem`, { method:"POST", body:JSON.stringify({ redemptionToken:payload.redemptionToken }) });
        location.assign(payload.target);
      } catch (_) {
        message("short-open-error", "Ссылка недоступна, просрочена или PIN неверен");
        submit.disabled = false;
        $("short-open-pin").select();
      }
    });
  }

  async function createRoom(event) {
    event.preventDefault(); message("create-error", "");
    const submit = event.submitter; submit.disabled = true;
    try {
      state.roomID = $("room-id").value;
      const encrypted = $("encrypted").checked;
      const writeProtected = $("write-protected").checked;
      let keyId = ""; let keyText = "";
      if (encrypted) {
        if (!globalThis.crypto?.subtle) throw new Error("Шифрование требует HTTPS или localhost");
        const entered = $("room-key").value;
        const raw = entered ? await keyFromInput(entered) : crypto.getRandomValues(new Uint8Array(32));
        keyId = await keyID(raw); keyText = rawKeyText(raw);
      }
      const writeToken = writeProtected ? newWriteToken() : "";
	  const ttlSeconds = Number($("room-ttl").value);
      await api("/api/rooms", { method: "POST", body: JSON.stringify({ id: state.roomID, encrypted, keyId, writeProtected, writeToken, ttlSeconds }) });
      const nextFragment = new URLSearchParams();
      if (writeProtected) nextFragment.set("write", writeToken);
      if (encrypted) nextFragment.set("key", keyText);
      const nextFragmentText = nextFragment.toString();
      location.href = `/r/${encodeURIComponent(state.roomID)}${nextFragmentText ? `#${nextFragmentText}` : ""}`;
    } catch (error) { message("create-error", error.message); submit.disabled = false; }
  }

  async function initRoom() {
    show("room"); $("room-name").textContent = state.roomID; $("room-name").title = state.roomID;
	$("item-text").disabled = true;
	$("send").disabled = true;
	$("file-input").disabled = true;
	$("share").disabled = true;
	$("alias").value = loadAlias();
	const remembered = await loadRememberedSecrets();
	$("remember-room").checked = remembered;
    $("share").addEventListener("click", openShare);
    $("item-form").addEventListener("submit", addItem);
    $("items").addEventListener("click", itemAction);
    $("key-form").addEventListener("submit", unlockFromDialog);
    $("toggle-unlock-key").addEventListener("click", (e) => togglePassword("unlock-key", e.currentTarget));
    $("copy-link").addEventListener("click", async () => { await copyText($("share-url").value); toast("Ссылка скопирована"); });
    $("generate-short-pin").addEventListener("click", () => { $("short-pin").value = randomPIN(); });
    $("create-short-link").addEventListener("click", createShortLink);
    $("copy-short-link").addEventListener("click", async () => { await copyText($("short-url").value); toast("Короткая ссылка скопирована"); });
    $("short-code-length").addEventListener("change", () => { if ($("short-code-length").value === "4") { $("short-ttl").value = "600"; $("short-one-time").checked = true; } });
    $("short-one-time").addEventListener("change", () => { if (!$("short-one-time").checked && $("short-code-length").value === "4") $("short-code-length").value = "5"; });
    $("share-dialog").querySelector(".dialog-close").addEventListener("click", () => $("share-dialog").close());
    document.querySelectorAll('input[name="share-permission"], input[name="share-key"]').forEach((el) => el.addEventListener("change", updateShare));
    $("rotate-write").addEventListener("click", rotateWriteCapability);
	$("favorite-room").addEventListener("click", () => { const room=loadRecentRooms().find(value => value.id===state.roomID); updateRecentRoom({ favorite:!room?.favorite }); updateFavoriteButton(); });
	$("remember-room").addEventListener("change", async () => { try { if ($("remember-room").checked) await rememberSecrets(); else await idbDelete("vault", `room:${state.roomID}`); toast($("remember-room").checked ? "Доступ сохранён на этом устройстве" : "Сохранённый доступ удалён"); } catch (error) { $("remember-room").checked=false; message("room-error", error.message); } });
	$("read-clipboard").addEventListener("click", readClipboard);
	$("item-text").addEventListener("paste", pasteClipboardFiles);
	$("search").addEventListener("input", renderItems);
	$("type-filter").addEventListener("change", renderItems);
	$("new-items").addEventListener("click", () => { state.unread=0; updateUnread(); $("items").scrollIntoView({ behavior:"smooth" }); });
	$("notifications").checked = localStorage.getItem(`clipboard-exchange:notify:${state.roomID}`) === "1";
	$("notification-sound").checked = localStorage.getItem(`clipboard-exchange:sound:${state.roomID}`) === "1";
	$("notifications").addEventListener("change", configureNotifications);
	$("notification-sound").addEventListener("change", () => localStorage.setItem(`clipboard-exchange:sound:${state.roomID}`, $("notification-sound").checked ? "1" : "0"));
	$("clear-room").addEventListener("click", clearCurrentRoom);
	$("preview-dialog").querySelector(".dialog-close").addEventListener("click", () => closePreview());
	$("preview-prev").addEventListener("click", () => showPreview(state.previewIndex-1));
	$("preview-next").addEventListener("click", () => showPreview(state.previewIndex+1));
    $("alias").addEventListener("input", () => saveAlias($("alias").value));
    $("file-input").addEventListener("change", (event) => queueFiles(event.target.files));
    for (const type of ["dragenter", "dragover"]) $("file-drop").addEventListener(type, (event) => { event.preventDefault(); $("file-drop").classList.add("dragging"); });
    for (const type of ["dragleave", "drop"]) $("file-drop").addEventListener(type, (event) => { event.preventDefault(); $("file-drop").classList.remove("dragging"); });
    $("file-drop").addEventListener("drop", (event) => queueFiles(event.dataTransfer.files));
    try {
      state.capabilities = await api("/api/capabilities");
      await refresh();
      if (state.room.encrypted) {
        await prepareKey();
        ensureDownloadWorker().catch((error) => message("crypto-warning", `Потоковое скачивание пока недоступно: ${error.message}`));
      }
      else { $("encryption-state").textContent = "Без шифрования"; renderItems(); }
      connect();
	  $("share").disabled = false;
	  await consumeSharedPayload();
    } catch (error) { message("room-error", error.message); $("item-text").disabled = true; $("send").disabled = true; }
  }

  async function refresh() {
    const data = await api(`/api/rooms/${encodeURIComponent(state.roomID)}`);
	const nextIDs = new Set([...(data.entries || []).map(entry => entry.id), ...data.items.map(item => item.id), ...(data.files || []).map(file => file.entryId || file.id)]);
	const added = [...nextIDs].filter(id => !state.previousEntryIDs.has(id));
    state.room = data.room; state.entries = data.entries || []; state.items = data.items; state.files = data.files || [];
	state.previousEntryIDs = nextIDs;
    updateAccessState();
	updateRecentRoom(); updateFavoriteButton();
    if (!state.room.encrypted || state.key) await renderItems();
	if (state.hasRefreshed && added.length && document.hidden) { state.unread += added.length; updateUnread(); notifyNewEntries(added.length); }
	state.hasRefreshed = true;
  }

  function updateAccessState() {
    if (!state.room) return;
    state.canWrite = !state.room.writeProtected || Boolean(state.writeToken);
    const unlocked = !state.room.encrypted || Boolean(state.key);
    show("item-form", state.canWrite);
    show("access-notice", !state.canWrite);
    show("file-drop", state.canWrite);
	show("clear-room", state.canWrite);
    $("item-text").disabled = !state.canWrite || !unlocked;
    $("send").disabled = !state.canWrite || !unlocked;
    $("file-input").disabled = !state.canWrite || !unlocked;
  }

  function updateFavoriteButton() {
	const favorite = Boolean(loadRecentRooms().find(room => room.id === state.roomID)?.favorite);
	const button = $("favorite-room");
	button.textContent = favorite ? "★ В избранном" : "☆ В избранное";
	button.classList.toggle("favorite-active", favorite);
	button.setAttribute("aria-label", favorite ? "Удалить из избранного" : "Добавить в избранное");
	button.title = button.getAttribute("aria-label");
  }

  async function readClipboard() {
	try {
	  if (navigator.clipboard?.read) {
		const files=[]; const texts=[];
		for (const item of await navigator.clipboard.read()) for (const type of item.types) {
		  const blob = await item.getType(type);
		  if (type === "text/plain") texts.push(await blob.text());
		  else { const extension=(type.split("/")[1] || "bin").replace(/[^a-z0-9]+/gi,"-"); files.push(new File([blob], `clipboard-${Date.now()}.${extension}`, { type })); }
		}
		if (texts.length) $("item-text").value += ($("item-text").value ? "\n" : "") + texts.join("\n");
		if (files.length) queueFiles(files);
	  } else if (navigator.clipboard?.readText) $("item-text").value += await navigator.clipboard.readText();
	  else throw new Error("Браузер не поддерживает чтение буфера обмена");
	} catch (error) { message("room-error", `Буфер обмена недоступен: ${error.message}`); }
  }

  function pasteClipboardFiles(event) {
	const files = Array.from(event.clipboardData?.files || []); if (files.length) queueFiles(files);
  }

  async function consumeSharedPayload() {
	const shared = await idbGet("shared", "pending").catch(() => null); if (!shared) return;
	const text = [shared.title, shared.text, shared.url].filter(Boolean).join("\n");
	if (text) $("item-text").value = text;
	if (shared.files?.length) queueFiles(shared.files);
	await idbDelete("shared", "pending").catch(() => {});
	toast("Данные из системного меню «Поделиться» добавлены в черновик");
  }

  async function configureNotifications() {
	if ($("notifications").checked) {
	  if (!("Notification" in window)) { $("notifications").checked=false; message("room-error", "Браузер не поддерживает уведомления"); return; }
	  const permission = await Notification.requestPermission();
	  if (permission !== "granted") { $("notifications").checked=false; message("room-error", "Разрешение на уведомления не выдано"); }
	}
	localStorage.setItem(`clipboard-exchange:notify:${state.roomID}`, $("notifications").checked ? "1" : "0");
  }

  function notifyNewEntries(count) {
	if ($("notifications").checked && Notification.permission === "granted") new Notification("Clipboard Exchange", { body:`Новых сообщений: ${count}`, icon:"/assets/icon.svg", tag:`room-${state.roomID}` });
	if ($("notification-sound").checked) { try { const context=new AudioContext(); const oscillator=context.createOscillator(); const gain=context.createGain(); gain.gain.value=.035; oscillator.frequency.value=660; oscillator.connect(gain).connect(context.destination); oscillator.start(); oscillator.stop(context.currentTime+.12); oscillator.onended=()=>context.close(); } catch (_) {} }
  }

  function updateUnread() { $("new-items").textContent = `${state.unread} новых`; show("new-items", state.unread > 0); }

  async function clearCurrentRoom() {
	if (!confirm("Удалить все сообщения и незавершённые загрузки комнаты?")) return;
	try { await api(`/api/rooms/${encodeURIComponent(state.roomID)}/clear`, { method:"POST", write:true }); state.pendingFiles.forEach(entry => entry.row.remove()); state.pendingFiles=[]; await refresh(); }
	catch (error) { message("room-error", error.message); }
  }

  async function prepareKey() {
    if (!crypto?.subtle) {
      message("crypto-warning", "Эта комната зашифрована. Откройте её по HTTPS или через localhost: Web Crypto недоступен в обычном HTTP-подключении.");
      $("item-text").disabled = true; $("send").disabled = true; return;
    }
    const supplied = fragment.get("key") || state.keyText;
    if (supplied) {
      try { await unlock(supplied); return; } catch (_) { /* ask explicitly */ }
    }
    $("key-dialog").showModal();
  }

  async function unlock(value) {
    const raw = await keyFromInput(value);
    if (await keyID(raw) !== state.room.keyId) throw new Error("Ключ не подходит к этой комнате");
    state.key = await importRawKey(raw); state.keyText = rawKeyText(raw);
    updateAccessState();
    $("encryption-state").textContent = "Сквозное шифрование включено";
    message("crypto-warning", "");
    await renderItems();
	if ($("remember-room").checked) await rememberSecrets().catch(() => {});
  }

  async function unlockFromDialog(event) {
    event.preventDefault(); message("key-error", "");
    try { await unlock($("unlock-key").value); $("key-dialog").close(); }
    catch (error) { message("key-error", error.message); }
  }

  async function encrypt(text, alias) {
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const aad = encoder.encode(`clipboard-exchange:v2:${state.roomID}`);
    const plaintext = encoder.encode(JSON.stringify({ text, alias }));
    const ciphertext = await crypto.subtle.encrypt({ name: "AES-GCM", iv, additionalData: aad, tagLength: 128 }, state.key, plaintext);
    return { kind: "encrypted", ciphertext: b64url(new Uint8Array(ciphertext)), iv: b64url(iv), keyId: state.room.keyId, version: 2 };
  }

  async function decrypt(item) {
    const version = item.version === 2 ? 2 : 1;
    const aad = encoder.encode(`clipboard-exchange:v${version}:${state.roomID}`);
    const plaintext = await crypto.subtle.decrypt({ name: "AES-GCM", iv: fromB64url(item.iv), additionalData: aad, tagLength: 128 }, state.key, fromB64url(item.ciphertext));
    const parsed = JSON.parse(decoder.decode(plaintext));
    if (typeof parsed.text !== "string") throw new Error("Некорректные данные");
    return { text: parsed.text, alias: typeof parsed.alias === "string" ? parsed.alias : "" };
  }

  async function decryptFileManifest(file) {
    if (!state.key || file.version !== 1 || file.keyId !== state.room.keyId) throw new Error("Некорректный manifest файла");
    const aad = encoder.encode(`clipboard-exchange:file-manifest:v1:${state.roomID}:${file.id}`);
    const plaintext = await crypto.subtle.decrypt({ name:"AES-GCM", iv:fromB64url(file.manifestIv), additionalData:aad, tagLength:128 }, state.key, fromB64url(file.manifestCiphertext));
    const metadata = JSON.parse(decoder.decode(plaintext));
    const nameValid = typeof metadata.name === "string" && metadata.name.length > 0 && Array.from(metadata.name).length <= 255 && !metadata.name.includes("\0");
    const mimeValid = typeof metadata.mimeType === "string" && metadata.mimeType.length > 0 && metadata.mimeType.length <= 255 && !/[\r\n]/.test(metadata.mimeType);
    const aliasValid = typeof metadata.alias === "string" && Array.from(metadata.alias).length <= 64;
    const sizeValid = Number.isSafeInteger(metadata.size) && metadata.size >= 0 && metadata.size <= metadata.chunkSize * metadata.chunkCount && (metadata.chunkCount === 1 || metadata.size > metadata.chunkSize * (metadata.chunkCount - 1));
    if (!nameValid || !mimeValid || !aliasValid || !sizeValid || metadata.chunkSize !== file.chunkSize || metadata.chunkCount !== file.chunkCount) throw new Error("Некорректный manifest файла");
    return metadata;
  }

  async function encryptFileChunk(file, session, index) {
    const start = index * session.plainChunkSize, end = Math.min(file.size, start + session.plainChunkSize);
    const plaintext = new Uint8Array(session.plainChunkSize);
    plaintext.set(new Uint8Array(await file.slice(start, end).arrayBuffer()));
    session.ivs ||= {};
    const iv = session.ivs[index] ? fromB64url(session.ivs[index]) : crypto.getRandomValues(new Uint8Array(12));
    session.ivs[index] = b64url(iv); saveUploadSession(file, session);
    const aad = encoder.encode(`clipboard-exchange:file:v1:${state.roomID}:${session.fileId}:${index}:${session.plainChunkSize}`);
    const encrypted = new Uint8Array(await crypto.subtle.encrypt({ name:"AES-GCM", iv, additionalData:aad, tagLength:128 }, state.key, plaintext));
    const body = new Uint8Array(iv.length + encrypted.length); body.set(iv); body.set(encrypted, iv.length);
    return { body, iv:b64url(iv), plaintextBytes:end-start };
  }

  async function encryptFileManifest(session) {
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const aad = encoder.encode(`clipboard-exchange:file-manifest:v1:${state.roomID}:${session.fileId}`);
    const ciphertext = await crypto.subtle.encrypt({ name:"AES-GCM", iv, additionalData:aad, tagLength:128 }, state.key, encoder.encode(JSON.stringify(session.clientMeta)));
    return { ciphertext:b64url(new Uint8Array(ciphertext)), iv:b64url(iv), keyId:state.room.keyId, version:1 };
  }

  async function addItem(event) {
    event.preventDefault(); message("room-error", "");
    const text = $("item-text").value;
    const pending = state.pendingFiles.filter((entry) => !entry.started);
    if (!text && pending.length === 0) return;
    const alias = $("alias").value;
    if (Array.from(alias).length > 64) { message("room-error", "Alias не может быть длиннее 64 символов"); return; }
	const deleteAfterDownload = $("delete-after-download").checked;
	if (deleteAfterDownload && (text || pending.length !== 1)) { message("room-error", "Удаление после скачивания доступно только для сообщения из одного файла без текста"); return; }
    $("send").disabled = true;
    try {
      const resumedIDs = [...new Set(pending.map((entry) => loadUploadSession(entry.file)?.entryId).filter(Boolean))];
	  if (resumedIDs.length > 1) throw new Error("Выбраны файлы из разных незавершённых сообщений; отправьте их отдельно");
      const entryID = resumedIDs[0] || uuid();
	  if (resumedIDs.length && text) throw new Error("Текст незавершённого сообщения уже сохранён; очистите поле и продолжите загрузку файлов");
	  if (!resumedIDs.length) {
		let item = null;
		if (text) { item = state.room.encrypted ? await encrypt(text, alias) : { kind:"text", content:text, alias }; item.id = entryID; }
		await api(`/api/rooms/${encodeURIComponent(state.roomID)}/entries`, { method:"POST", body:JSON.stringify({ id:entryID, expectedFiles:pending.length, expiresInSeconds:Number($("entry-ttl").value), deleteAfterDownload, item }), write:true });
		if (text) $("item-text").value = "";
	  }
      state.pendingFiles = state.pendingFiles.filter((entry) => !pending.includes(entry));
	  const uploads = [];
      for (const [entryIndex, entry] of pending.entries()) {
        entry.started = true;
        entry.control.entryID = entryID;
		entry.control.group = pending;
        entry.status.textContent = "Подготовка…";
		uploads.push(runUpload(entry.file, entry.row, entry.progress, entry.status, entry.actions, entry.control, entryID, entryIndex));
      }
	  await Promise.all(uploads);
	  await commitEntry(entryID);
	  $("delete-after-download").checked = false;
      await refresh();
    } catch (error) { message("room-error", error.message); }
    finally { $("send").disabled = false; }
  }

  async function commitEntry(entryID) {
	await api(`/api/rooms/${encodeURIComponent(state.roomID)}/entries/${entryID}/commit`, { method:"POST", write:true });
  }

  async function renderItems() {
    const container = $("items"); container.replaceChildren(); let failures = 0;
    const entries = new Map();
    for (const item of state.items) {
      let text = item.content, alias = item.alias || "";
      if (item.kind === "encrypted") {
        try { const decrypted = await decrypt(item); text = decrypted.text; alias = decrypted.alias; } catch (_) { text = "[Не удалось расшифровать запись]"; alias = ""; failures++; }
      }
      entries.set(item.id, { id:item.id, text, alias, createdAt:item.createdAt, files:[] });
    }
    for (const file of state.files) {
      let metadata = file;
      if (file.encrypted) {
        try { metadata = { ...file, ...(await decryptFileManifest(file)) }; }
        catch (_) { metadata = { ...file, name:"[Не удалось расшифровать имя файла]", mimeType:"application/octet-stream", alias:"", size:0 }; failures++; }
      }
      const entryID = file.entryId || file.id;
      let entry = entries.get(entryID);
      if (!entry) {
        entry = { id:entryID, text:"", alias:metadata.alias || "", createdAt:file.createdAt, files:[] };
        entries.set(entryID, entry);
      }
      entry.files.push({ file, metadata });
      if (!entry.alias && metadata.alias) entry.alias = metadata.alias;
      if (new Date(file.createdAt) < new Date(entry.createdAt)) entry.createdAt = file.createdAt;
    }
	const metadataByID = new Map(state.entries.map(entry => [entry.id, entry]));
	for (const entry of entries.values()) Object.assign(entry, metadataByID.get(entry.id) || { pinned:false, expiresAt:"", deleteAfterDownload:false });
    for (const entry of entries.values()) entry.files.sort((left, right) => (left.file.entryIndex || 0) - (right.file.entryIndex || 0));
	const query = $("search").value.trim().toLocaleLowerCase(); const filter = $("type-filter").value;
	const matchesType = entry => filter === "all" || (filter === "text" && Boolean(entry.text)) || (filter === "files" && entry.files.length>0) || (filter === "images" && entry.files.some(value => String(value.metadata.mimeType).startsWith("image/"))) || (filter === "documents" && entry.files.some(value => !String(value.metadata.mimeType).startsWith("image/") && !String(value.metadata.mimeType).startsWith("audio/") && !String(value.metadata.mimeType).startsWith("video/")));
	const matchesQuery = entry => !query || [entry.text, entry.alias, ...entry.files.map(value => value.metadata.name)].some(value => String(value || "").toLocaleLowerCase().includes(query));
	const ordered = Array.from(entries.values()).filter(entry => matchesType(entry) && matchesQuery(entry)).sort((left, right) => Number(right.pinned)-Number(left.pinned) || new Date(right.createdAt) - new Date(left.createdAt));
	state.renderedEntries = ordered;
    for (const entry of ordered) {
	  const article = document.createElement("article"); article.className = `item${entry.text ? "" : " file-item"}${entry.pinned ? " pinned" : ""}`; article.dataset.id = entry.id;
      if (entry.text) {
        const pre = document.createElement("pre"); pre.className = "item-content"; pre.textContent = entry.text;
        article.append(pre);
      }
      if (entry.files.length) {
        const attachments = document.createElement("div"); attachments.className = "attachments";
        if (entry.files.length > 1) {
          attachments.classList.add("attachments-table-wrap");
          const table=document.createElement("table"); table.className="attachments-table"; table.setAttribute("aria-label","Файлы сообщения");
          const head=document.createElement("thead"), headRow=document.createElement("tr");
          for (const title of ["Файл","Размер","Действия"]) { const cell=document.createElement("th"); cell.scope="col"; cell.textContent=title; headRow.append(cell); }
          head.append(headRow);
          const body=document.createElement("tbody");
          for (const attached of entry.files) body.append(renderFileAttachment(attached.file,attached.metadata,true));
          table.append(head,body); attachments.append(table);
        } else {
          attachments.append(renderFileAttachment(entry.files[0].file,entry.files[0].metadata));
        }
        article.append(attachments);
      }
      const footer = document.createElement("div"); footer.className = "item-footer";
      const time = document.createElement("time"); time.className = "item-time"; time.dateTime = entry.createdAt;
      time.textContent = new Intl.DateTimeFormat(undefined, { dateStyle:"medium", timeStyle:"short" }).format(new Date(entry.createdAt));
      const meta = document.createElement("div"); meta.className = "item-meta";
	  if (entry.pinned) { const mark=document.createElement("span"); mark.className="pin-mark"; mark.textContent="★ Закреплено"; meta.append(mark); }
      if (entry.alias) { const author = document.createElement("span"); author.className = "item-alias"; author.textContent = entry.alias; meta.append(author); }
      meta.append(time);
	  if (entry.expiresAt) { const expires=document.createElement("span"); expires.className="muted"; expires.textContent=`до ${new Intl.DateTimeFormat(undefined,{dateStyle:"short",timeStyle:"short"}).format(new Date(entry.expiresAt))}`; meta.append(expires); }
	  if (entry.deleteAfterDownload) { const once=document.createElement("span"); once.className="muted"; once.textContent="удалится после скачивания"; meta.append(once); }
      const buttons = document.createElement("div"); buttons.className = "item-buttons";
      const toggle = document.createElement("button"); toggle.type = "button"; toggle.className = "button secondary hidden"; toggle.dataset.action = "toggle"; toggle.textContent = "Развернуть"; toggle.setAttribute("aria-expanded", "false");
      const copy = document.createElement("button"); copy.type = "button"; copy.className = "button secondary"; copy.dataset.action = "copy"; copy.textContent = "Копировать";
	  const copyAll = document.createElement("button"); copyAll.type="button"; copyAll.className="button secondary"; copyAll.dataset.action="copy-all"; copyAll.textContent="Копировать всё";
	  const archive = document.createElement(state.room.encrypted ? "button" : "a"); archive.className="button primary"; archive.textContent="Скачать всё архивом";
	  if (state.room.encrypted) { archive.type="button"; archive.dataset.action="encrypted-archive"; }
	  else { archive.download=`files-${entry.id.slice(0,8)}.zip`; archive.href=`/api/rooms/${encodeURIComponent(state.roomID)}/entries/${entry.id}/archive`; }
	  const pin = document.createElement("button"); pin.type="button"; pin.className="button secondary"; pin.dataset.action="pin"; pin.textContent=entry.pinned ? "Открепить" : "Закрепить";
      const del = document.createElement("button"); del.type = "button"; del.className = "button secondary delete"; del.dataset.action = "entry-delete"; del.textContent = "Удалить";
      if (entry.text) buttons.append(toggle, copy);
	  if (entry.files.length) buttons.append(copyAll);
	  if (entry.files.length > 1) buttons.append(archive);
	  if (state.canWrite) buttons.append(pin);
      if (state.canWrite) buttons.append(del);
      footer.append(meta, buttons); article.append(footer); container.append(article);
      if (entry.text) requestAnimationFrame(() => requestAnimationFrame(() => { if (article.isConnected) updateItemOverflow(article); }));
    }
	const total = entries.size; $("item-count").textContent = `${ordered.length}${ordered.length!==total ? ` из ${total}` : ""} сообщений · ${state.files.length} файлов`;
    show("empty", ordered.length === 0);
    if (failures) message("room-error", `${failures} элементов не удалось расшифровать`);
  }

  async function itemAction(event) {
    const button = event.target.closest("button[data-action]"); if (!button) return;
    const article = button.closest(".item");
    const attachment = button.closest(".file-attachment");
    if (button.dataset.action === "toggle") {
      const pre = article.querySelector("pre");
      const expanded = pre.classList.contains("collapsed");
      pre.classList.toggle("collapsed", !expanded);
      button.textContent = expanded ? "Свернуть" : "Развернуть";
      button.setAttribute("aria-expanded", String(expanded));
      return;
    }
    if (button.dataset.action === "copy") { await copyText(article.querySelector("pre").textContent); toast("Текст скопирован"); return; }
	if (button.dataset.action === "copy-all") { const entry=state.renderedEntries.find(value => value.id===article.dataset.id); const value=[entry?.text, ...(entry?.files || []).map(file => file.metadata.name)].filter(Boolean).join("\n"); await copyText(value); toast("Сообщение скопировано"); return; }
	if (button.dataset.action === "encrypted-archive") { const entry=state.renderedEntries.find(value => value.id===article.dataset.id); await startEncryptedArchive(entry); return; }
	if (button.dataset.action === "pin") { const entry=state.renderedEntries.find(value => value.id===article.dataset.id); button.disabled=true; try { await api(`/api/rooms/${encodeURIComponent(state.roomID)}/entries/${article.dataset.id}/pin`, { method:"PUT", body:JSON.stringify({ pinned:!entry?.pinned }), write:true }); await refresh(); } catch(error) { message("room-error",error.message); button.disabled=false; } return; }
    if (button.dataset.action === "file-copy") { await copyText(attachment.dataset.name); toast("Имя файла скопировано"); return; }
	if (button.dataset.action === "preview") { const files=state.renderedEntries.flatMap(entry => entry.files).filter(value => canPreview(value.metadata.mimeType)); state.previewIndex=Math.max(0,files.findIndex(value => value.file.id===attachment.dataset.id)); await showPreview(state.previewIndex); return; }
    if (button.dataset.action === "encrypted-download") { await startEncryptedDownload(attachment, false); return; }
    if (button.dataset.action === "encrypted-open") { await startEncryptedDownload(attachment, true); return; }
    if (button.dataset.action === "file-delete") {
      button.disabled = true;
      try { await api(`/api/rooms/${encodeURIComponent(state.roomID)}/files/${attachment.dataset.id}`, { method:"DELETE", write:true }); await refresh(); }
      catch (error) { message("room-error", error.message); button.disabled = false; }
      return;
    }
    if (button.dataset.action === "entry-delete") {
      button.disabled = true;
      try { await api(`/api/rooms/${encodeURIComponent(state.roomID)}/entries/${article.dataset.id}`, { method:"DELETE", write:true }); await refresh(); }
      catch (error) { message("room-error", error.message); button.disabled = false; }
    }
  }

  function renderFileAttachment(file, metadata, tableRow=false) {
    const attachment = document.createElement(tableRow ? "tr" : "div"); attachment.className = `file-attachment${tableRow ? " file-attachment-row" : ""}`; attachment.dataset.id = file.id; attachment.dataset.name = metadata.name;
	attachment.dataset.metadata = JSON.stringify(metadata);
    const details = document.createElement(tableRow ? "td" : "div"); details.className = "file-details";
    const name = document.createElement("strong"); name.className = "file-name"; name.textContent = metadata.name;
    const formattedSize=formatBytes(file.encrypted ? metadata.size : file.size);
    if (tableRow) details.append(name);
    else { const meta=document.createElement("div"); meta.className="item-meta"; const size=document.createElement("span"); size.className="muted"; size.textContent=formattedSize; meta.append(size); details.append(name,meta); }
    const actionCell=tableRow ? document.createElement("td") : null;
    const buttons = document.createElement("div"); buttons.className = "item-buttons";
	const open = document.createElement("button"); open.type="button"; open.className = "button secondary"; open.textContent = "Открыть"; open.dataset.action="preview";
    const download = document.createElement(file.encrypted ? "button" : "a"); download.className = "button primary"; download.textContent = "Скачать";
    if (file.encrypted) { download.type = "button"; download.dataset.action = "encrypted-download"; }
    else { download.download = file.name; download.href = `/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}`; }
    const copy = document.createElement("button"); copy.type = "button"; copy.className = "button secondary"; copy.dataset.action = "file-copy"; copy.textContent = "Копировать имя";
    if (canPreview(metadata.mimeType)) buttons.append(open);
    buttons.append(download, copy);
    if (tableRow) { const sizeCell=document.createElement("td"); sizeCell.className="file-size muted"; sizeCell.textContent=formattedSize; actionCell.className="file-actions"; actionCell.append(buttons); attachment.append(details,sizeCell,actionCell); }
    else attachment.append(details, buttons);
    return attachment;
  }

  function formatBytes(value) {
    if (value < 1024) return `${value} Б`;
    const units = ["КиБ", "МиБ", "ГиБ"]; let amount = value; let unit = -1;
    do { amount /= 1024; unit++; } while (amount >= 1024 && unit < units.length - 1);
    return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[unit]}`;
  }

  async function ensureDownloadWorker() {
    if (!navigator.serviceWorker) throw new Error("Браузер не поддерживает потоковое скачивание зашифрованных файлов");
    state.downloadRegistration = await navigator.serviceWorker.register("/assets/download-sw.js?v=13", { scope:"/" });
    await navigator.serviceWorker.ready;
    if (!navigator.serviceWorker.controller) {
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("Service Worker не активирован")), 5000);
        navigator.serviceWorker.addEventListener("controllerchange", () => { clearTimeout(timer); resolve(); }, { once:true });
      });
    }
  }

  async function startEncryptedDownload(attachment, inline) {
    const preview = inline ? window.open("about:blank", "_blank") : null;
    if (preview) preview.opener = null;
    try {
      if (inline && !preview) throw new Error("Браузер заблокировал окно просмотра");
      if (!state.key || !state.keyText.startsWith("ce1_")) throw new Error("Сначала откройте ключ комнаты");
      await ensureDownloadWorker();
      const file = state.files.find((entry) => entry.id === attachment.dataset.id);
      const metadata = JSON.parse(attachment.dataset.metadata);
      const token = uuid();
      const channel = new MessageChannel();
      const ready = new Promise((resolve, reject) => { const timer = setTimeout(() => reject(new Error("Service Worker не ответил")), 5000); channel.port1.onmessage = () => { clearTimeout(timer); resolve(); }; });
	  const entry=state.entries.find(value=>value.id===(file.entryId||file.id));
      navigator.serviceWorker.controller.postMessage({ type:"prepare-download", token, config:{ rawKey:state.keyText.slice(4), roomID:state.roomID, fileID:file.id, chunkSize:file.chunkSize, chunkCount:file.chunkCount, size:metadata.size, name:metadata.name, mimeType:metadata.mimeType, disposition:inline ? "inline" : "attachment", url:`/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}`, consumeURL:entry?.deleteAfterDownload ? `/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}/consume` : "" } }, [channel.port2]);
      await ready;
      if (inline) preview.location = `/client-download/${token}`;
      else { const frame = document.createElement("iframe"); frame.hidden = true; frame.src = `/client-download/${token}`; document.body.append(frame); setTimeout(() => frame.remove(), 60000); }
    } catch (error) { preview?.close(); message("room-error", error.message); }
  }

  async function startEncryptedArchive(entry) {
	try {
	  if (!state.key || !state.keyText.startsWith("ce1_")) throw new Error("Сначала откройте ключ комнаты");
	  await ensureDownloadWorker();
	  const token=uuid(), channel=new MessageChannel();
	  const ready=new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error("Service Worker не ответил")),5000);channel.port1.onmessage=()=>{clearTimeout(timer);resolve();};});
	  const files=entry.files.map(({file,metadata})=>({ rawKey:state.keyText.slice(4), roomID:state.roomID, fileID:file.id, chunkSize:file.chunkSize, chunkCount:file.chunkCount, size:metadata.size, name:metadata.name, url:`/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}` }));
	  const consumeURL=entry.deleteAfterDownload ? `/api/rooms/${encodeURIComponent(state.roomID)}/files/${entry.files[0].file.id}/consume` : "";
	  navigator.serviceWorker.controller.postMessage({ type:"prepare-archive", token, config:{ archive:true, name:`files-${entry.id.slice(0,8)}.zip`, files, consumeURL } }, [channel.port2]);
	  await ready;
	  const frame=document.createElement("iframe"); frame.hidden=true; frame.src=`/client-download/${token}`; document.body.append(frame); setTimeout(()=>frame.remove(),60000);
	} catch(error) { message("room-error",error.message); }
  }

  async function prepareEncryptedURL(file, metadata, disposition="inline") {
	if (!state.key || !state.keyText.startsWith("ce1_")) throw new Error("Сначала откройте ключ комнаты");
	await ensureDownloadWorker(); const token=uuid(); const channel=new MessageChannel();
	const ready=new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error("Service Worker не ответил")),5000);channel.port1.onmessage=()=>{clearTimeout(timer);resolve();};});
	const entry=state.entries.find(value=>value.id===(file.entryId||file.id));
	navigator.serviceWorker.controller.postMessage({ type:"prepare-download", token, config:{ rawKey:state.keyText.slice(4), roomID:state.roomID, fileID:file.id, chunkSize:file.chunkSize, chunkCount:file.chunkCount, size:metadata.size, name:metadata.name, mimeType:metadata.mimeType, disposition, url:`/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}`, consumeURL:entry?.deleteAfterDownload ? `/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}/consume` : "" } }, [channel.port2]);
	await ready; return `/client-download/${token}`;
  }

  async function showPreview(index) {
	const files=state.renderedEntries.flatMap(entry=>entry.files).filter(value=>canPreview(value.metadata.mimeType)); if(!files.length)return;
	state.previewIndex=(index+files.length)%files.length; const {file,metadata}=files[state.previewIndex];
	const dialog=$("preview-dialog"), content=$("preview-content"); content.replaceChildren(); $("preview-title").textContent=metadata.name;
	try {
	  const url=file.encrypted ? await prepareEncryptedURL(file,metadata,"inline") : `/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}?inline=1`;
	  const mime=String(metadata.mimeType||"").split(";",1)[0].toLowerCase(); let element;
	  if(mime.startsWith("image/")){element=document.createElement("img");element.alt=metadata.name;element.src=url;}
	  else if(mime.startsWith("video/")){element=document.createElement("video");element.controls=true;element.src=url;}
	  else if(mime.startsWith("audio/")){element=document.createElement("audio");element.controls=true;element.src=url;}
	  else if(mime==="application/pdf"){element=document.createElement("iframe");element.title=metadata.name;element.src=url;}
	  else { element=document.createElement("pre");element.className="preview-text";const response=await fetch(url);if(!response.ok)throw new Error(`Ошибка HTTP ${response.status}`);if(Number(response.headers.get("Content-Length")||0)>2*1024*1024)throw new Error("Текстовый preview ограничен 2 МиБ");let text=await response.text();if(mime==="application/json"){try{text=JSON.stringify(JSON.parse(text),null,2);}catch(_){}}renderHighlightedText(element,text,metadata.name,mime); }
	  content.append(element); $("preview-download").onclick=async event=>{if(file.encrypted){event.preventDefault();const attachment=document.querySelector(`.file-attachment[data-id="${file.id}"]`);await startEncryptedDownload(attachment,false);}}; $("preview-download").href=file.encrypted?"#":`/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}`; $("preview-download").download=metadata.name;
	  $("preview-prev").disabled=files.length<2; $("preview-next").disabled=files.length<2; if(!dialog.open)dialog.showModal();
	} catch(error){content.textContent=error.message;if(!dialog.open)dialog.showModal();}
  }

  function closePreview(){const dialog=$("preview-dialog");$("preview-content").replaceChildren();if(dialog.open)dialog.close();}

  function renderHighlightedText(container,text,name,mime){
	const codeLike=mime==="application/json" || /\.(go|js|ts|tsx|jsx|py|rb|rs|java|kt|swift|sh|bash|yaml|yml|toml|sql|css|html)$/i.test(name);
	if(!codeLike){container.textContent=text;return;}
	const pattern=/(\/\/[^\n]*|#[^\n]*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\b(?:true|false|null|nil|return|func|function|const|let|var|if|else|for|while|class|struct|package|import|from|def|async|await|SELECT|FROM|WHERE|INSERT|UPDATE|DELETE)\b|\b\d+(?:\.\d+)?\b)/g;
	let offset=0;for(const match of text.matchAll(pattern)){container.append(document.createTextNode(text.slice(offset,match.index)));const span=document.createElement("span");span.textContent=match[0];span.className=/^(\/\/|#|\/\*)/.test(match[0])?"syntax-comment":/^["']/.test(match[0])?"syntax-string":/^\d/.test(match[0])?"syntax-number":"syntax-keyword";container.append(span);offset=match.index+match[0].length;}container.append(document.createTextNode(text.slice(offset)));
  }

  function canPreview(value) {
    const mediaType = String(value || "").split(";", 1)[0].trim().toLowerCase();
    if (mediaType.startsWith("audio/") || mediaType.startsWith("video/")) return true;
    if (mediaType.startsWith("image/") && mediaType !== "image/svg+xml") return true;
    return ["text/plain", "text/csv", "application/json", "application/pdf"].includes(mediaType);
  }

  function uploadStorageKey(file) { return `clipboard-exchange:upload:${state.roomID}:${file.name}:${file.size}:${file.lastModified || 0}:${file.type || ""}`; }
  function loadUploadSession(file) { try { return JSON.parse(localStorage.getItem(uploadStorageKey(file)) || "null"); } catch (_) { return null; } }
  function saveUploadSession(file, session) { try { localStorage.setItem(uploadStorageKey(file), JSON.stringify(session)); } catch (_) {} }
  function removeUploadSession(file) { try { localStorage.removeItem(uploadStorageKey(file)); } catch (_) {} }

  async function verifyResumableFile(file, session) {
    for (const index of session.received || []) {
      let bytes;
      if (session.encrypted) {
        if (!session.ivs?.[index]) return false;
        bytes = (await encryptFileChunk(file, session, index)).body;
      } else {
        const start = index * session.plainChunkSize, end = Math.min(file.size, start + session.plainChunkSize);
        bytes = new Uint8Array(await file.slice(start, end).arrayBuffer());
      }
      if (await sha256Hex(bytes) !== session.digests?.[index]) return false;
    }
    return true;
  }

  async function uploadAPI(url, options, token) {
    const response = await fetch(url, { ...options, headers: { ...options?.headers, Authorization: `ClipboardUpload ${token}` } });
    if (response.status === 204) return null;
    const data = await response.json().catch(() => ({}));
    if (!response.ok) { const error = new Error(data.error?.message || `Ошибка HTTP ${response.status}`); error.status = response.status; throw error; }
    return data;
  }

  function queueFiles(fileList) {
    if (!state.canWrite) return;
    if (Array.from($("alias").value).length > 64) { message("room-error", "Alias не может быть длиннее 64 символов"); return; }
    for (const file of Array.from(fileList || [])) {
      if (!file.name || Array.from(file.name).length > 255) { message("room-error", "Имя файла не может быть длиннее 255 символов"); continue; }
      createUploadRow(file);
    }
    $("file-input").value = "";
  }

  function createUploadRow(file) {
    const row = document.createElement("div"); row.className = "upload-row";
    const name = document.createElement("span"); name.className = "upload-name"; name.textContent = file.name;
    const actions = document.createElement("div"); actions.className = "item-buttons";
    const cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button secondary"; cancel.textContent = "Убрать";
    const progress = document.createElement("progress"); progress.max = Math.max(file.size, 1); progress.value = 0;
    const status = document.createElement("span"); status.className = "muted"; status.textContent = "Готов к отправке";
    actions.append(cancel); row.append(name, actions, progress, status); $("upload-list").append(row);
    const control = { controller: null, cancelled: false, session: null, entryID: "", entryIndex: 0 };
    const pending = { file, row, progress, status, actions, control, started: false };
    state.pendingFiles.push(pending);
    cancel.addEventListener("click", async () => {
	  const group = control.group || [pending];
	  for (const member of group) { member.control.cancelled = true; member.control.controller?.abort(); removeUploadSession(member.file); member.row.remove(); }
	  cancel.disabled = true;
	  if (pending.started && control.entryID) await api(`/api/rooms/${encodeURIComponent(state.roomID)}/entries/${control.entryID}`, { method:"DELETE", write:true }).catch(() => {});
	  else if (control.session) await uploadAPI(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads/${control.session.id}`, { method:"DELETE" }, control.session.uploadToken).catch(() => {});
	  state.pendingFiles = state.pendingFiles.filter((entry) => !group.includes(entry));
    });
  }

  async function runUpload(file, row, progress, status, actions, control, requestedEntryID, requestedEntryIndex = 0) {
    try {
      control.cancelled = false;
      let session = loadUploadSession(file);
      if (session) {
        if (!session.entryId) { removeUploadSession(file); session = null; }
      }
      if (session) {
        try {
          const current = await uploadAPI(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads/${session.id}`, { method:"GET" }, session.uploadToken);
          session = { ...session, ...current };
          if (!await verifyResumableFile(file, session)) {
            await uploadAPI(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads/${session.id}`, { method:"DELETE" }, session.uploadToken).catch(() => {});
            removeUploadSession(file); session = null;
          }
        } catch (_) { removeUploadSession(file); session = null; }
      }
      const entryID = session?.entryId || requestedEntryID;
      const entryIndex = session?.entryIndex ?? requestedEntryIndex;
      control.entryID = entryID;
      control.entryIndex = entryIndex;
      if (!session) {
        status.textContent = "Создание загрузки…";
        const encrypted = state.room.encrypted;
        if (encrypted && !state.key) throw new Error("Сначала откройте ключ комнаты");
        const plainChunkSize = state.capabilities.limits.fileChunkBytes;
        const chunkCount = Math.max(1, Math.ceil(file.size / plainChunkSize));
        const storedSize = encrypted ? chunkCount * (plainChunkSize + 28) : file.size;
        const alias = $("alias").value;
        session = await api(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads`, { method:"POST", body:JSON.stringify({ entryId:entryID, entryIndex, name:encrypted ? "" : file.name, mimeType:encrypted ? "" : (file.type || "application/octet-stream"), alias:encrypted ? "" : alias, size:storedSize, encrypted, keyId:encrypted ? state.room.keyId : "" }), write:true });
        session.clientMeta = { name:file.name, mimeType:file.type || "application/octet-stream", size:file.size, alias, chunkSize:plainChunkSize, chunkCount };
        saveUploadSession(file, session);
      }
      control.session = session;
      const received = new Set(session.received || []);
      let uploaded = 0;
      for (const index of received) uploaded += Math.min(session.plainChunkSize, Math.max(0, file.size - index * session.plainChunkSize));
      progress.value = file.size === 0 ? 0 : uploaded;
      for (let index = 0; index < session.chunkCount; index++) {
        if (received.has(index)) continue;
        if (control.cancelled) return;
        const start = index * session.plainChunkSize, end = Math.min(file.size, start + session.plainChunkSize);
        status.textContent = `Загрузка ${index + 1}/${session.chunkCount}`;
        control.controller = new AbortController();
        let body = file.slice(start, end), iv = "";
        if (session.encrypted) { const encrypted = await encryptFileChunk(file, session, index); body = encrypted.body; iv = encrypted.iv; }
        const headers = { "Content-Type":"application/octet-stream" }; if (iv) headers["X-Clipboard-Chunk-IV"] = iv;
        await uploadAPI(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads/${session.id}/chunks/${index}`, { method:"PUT", body, signal:control.controller.signal, headers }, session.uploadToken);
        uploaded += end - start; progress.value = file.size === 0 ? 1 : uploaded;
      }
      status.textContent = "Завершение…";
      const manifest = session.encrypted ? await encryptFileManifest(session) : null;
      await uploadAPI(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads/${session.id}/complete`, { method:"POST", body:manifest ? JSON.stringify(manifest) : undefined, headers:manifest ? { "Content-Type":"application/json" } : {} }, session.uploadToken);
      removeUploadSession(file); status.textContent = "Готово"; setTimeout(() => row.remove(), 700);
	  return session;
    } catch (error) {
      if (control.cancelled) return;
      status.textContent = `Ошибка: ${error.message}`;
      let retry = actions.querySelector('[data-action="upload-retry"]');
      if (!retry) { retry = document.createElement("button"); retry.type = "button"; retry.className = "button secondary"; retry.dataset.action = "upload-retry"; retry.textContent = "Повторить"; actions.prepend(retry); }
	  retry.onclick = async () => {
		retry.remove();
		try { await runUpload(file, row, progress, status, actions, control, control.entryID, control.entryIndex); await commitEntry(control.entryID); await refresh(); }
		catch (retryError) { if (retryError.status !== 409) message("room-error", retryError.message); }
	  };
	  throw error;
    }
  }

  function updateItemOverflow(article) {
    const pre = article.querySelector(".item-content");
    const toggle = article.querySelector('[data-action="toggle"]');
    const expanded = toggle.getAttribute("aria-expanded") === "true";
    pre.classList.add("collapsed");
    const overflowing = pre.textContent.split("\n").length > 2 || pre.scrollHeight > pre.clientHeight + 1;
    toggle.classList.toggle("hidden", !overflowing);
    if (!overflowing) {
      pre.classList.remove("collapsed");
      toggle.textContent = "Развернуть";
      toggle.setAttribute("aria-expanded", "false");
    } else if (expanded) {
      pre.classList.remove("collapsed");
    }
  }

  addEventListener("resize", () => document.querySelectorAll(".item").forEach(updateItemOverflow));

  function connect() {
    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${protocol}//${location.host}/api/rooms/${encodeURIComponent(state.roomID)}/events`;
    const connection = $("connection"); show("connection"); connection.textContent = "Подключение…"; connection.classList.remove("online");
    const socket = new WebSocket(url); state.socket = socket;
    socket.addEventListener("open", () => { connection.textContent = "В сети"; connection.classList.add("online"); });
    socket.addEventListener("message", (event) => {
      try { if (JSON.parse(event.data).type === "refresh") { clearTimeout(state.refreshTimer); state.refreshTimer = setTimeout(() => refresh().catch((e) => message("room-error", e.message)), 60); } } catch (_) {}
    });
    socket.addEventListener("close", () => { connection.textContent = "Переподключение…"; connection.classList.remove("online"); setTimeout(connect, 1500 + Math.random()*1500); });
  }

  function openShare() { updateShare(); if (!$("short-pin").value) $("short-pin").value = randomPIN(); show("short-result", false); message("short-create-error", ""); $("share-dialog").showModal(); }
  function updateShare() {
    show("short-result", false);
    const writeProtected = Boolean(state.room?.writeProtected);
    const permission = writeProtected ? (document.querySelector('input[name="share-permission"]:checked')?.value || "read") : "write";
    const withKey = !state.room?.encrypted || document.querySelector('input[name="share-key"]:checked')?.value === "key";
    const base = `${location.origin}/r/${encodeURIComponent(state.roomID)}`;
    const sharedFragment = new URLSearchParams();
    if (state.room?.encrypted && withKey && state.keyText) sharedFragment.set("key", state.keyText);
    if (permission === "write" && state.writeToken) sharedFragment.set("write", state.writeToken);
    const sharedFragmentText = sharedFragment.toString();
    const url = sharedFragmentText ? `${base}#${sharedFragmentText}` : base;
    $("share-url").value = url;
    show("share-permission-choice", writeProtected);
    show("share-write-option", writeProtected && Boolean(state.writeToken));
    show("share-key-choice", Boolean(state.room?.encrypted));
    show("rotate-write", writeProtected && Boolean(state.writeToken));
    $("share-note").textContent = !writeProtected
      ? "Обычная R/W-комната: любой участник по этой ссылке может добавлять и удалять."
      : permission === "write" && state.writeToken
        ? "R/W-ссылка позволяет добавлять и удалять записи."
        : "R/O-ссылка позволяет только читать и копировать записи.";
    const qr = $("qr"); qr.replaceChildren();
    if (globalThis.QRCode) new QRCode(qr, { text:url, width:220, height:220, correctLevel:QRCode.CorrectLevel.M });
    else qr.textContent = "QR-код недоступен";
  }

  function randomPIN() {
    const values = new Uint16Array(1);
    do { crypto.getRandomValues(values); } while (values[0] >= 60000);
    return String(values[0] % 10000).padStart(4, "0");
  }

  async function createShortLink() {
    message("short-create-error", ""); show("short-result", false);
    const pin = $("short-pin").value;
    const expiresInSeconds = Number($("short-ttl").value);
    const codeLength = Number($("short-code-length").value);
    const maxUses = $("short-one-time").checked ? 1 : 0;
    if (!/^\d{4}$/.test(pin)) { message("short-create-error", "PIN должен содержать четыре цифры"); return; }
    if (codeLength === 4 && (maxUses !== 1 || expiresInSeconds > 600)) { message("short-create-error", "Код из 4 символов разрешён только для одноразовой ссылки на 10 минут"); return; }
    if (!globalThis.crypto?.subtle) { message("short-create-error", "Создание защищённой ссылки требует HTTPS или localhost"); return; }
    const button = $("create-short-link"); button.disabled = true;
    try {
      const encrypted = await encryptShortTarget($("share-url").value, pin);
      const created = await api("/api/short-links", { method:"POST", body:JSON.stringify({ ciphertext:encrypted.ciphertext, iv:encrypted.iv, salt:encrypted.salt, tokenHash:encrypted.tokenHash, kdfIterations:shortLinkKDFIterations, expiresInSeconds, maxUses, codeLength }) });
      $("short-url").value = `${location.origin}/s/${created.code}`;
      $("short-result-pin").textContent = pin;
      $("short-result-expiry").textContent = `${maxUses === 1 ? "Одно использование" : "Многократная"} · действует до ${new Date(created.expiresAt).toLocaleString()}`;
      show("short-result");
    } catch (error) { message("short-create-error", error.message); }
    finally { button.disabled = false; }
  }

  async function rotateWriteCapability() {
    if (!confirm("Отозвать все ранее созданные R/W-ссылки?")) return;
    const next = newWriteToken();
    try {
      await api(`/api/rooms/${encodeURIComponent(state.roomID)}/write-capability/rotate`, { method:"POST", body:JSON.stringify({ writeToken:next }), write:true });
      state.writeToken = next; state.canWrite = true; fragment.set("write", next);
      history.replaceState(null, "", `${location.pathname}#${fragment}`);
      updateShare(); toast("R/W-ссылка обновлена");
    } catch (error) { message("room-error", error.message); }
  }

	document.addEventListener("visibilitychange",()=>{if(!document.hidden){state.unread=0;updateUnread();}});
	initPWA();
  if (roomMatch) initRoom(); else if (shortMatch) initShortLink(); else if (location.pathname === "/" || location.pathname === "/share-target") initHome();
})();
