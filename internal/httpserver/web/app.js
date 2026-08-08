(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const roomMatch = location.pathname.match(/^\/r\/([A-Za-z0-9][A-Za-z0-9_-]{0,63})$/);
  const state = { roomID: roomMatch ? roomMatch[1] : "", room: null, key: null, keyText: "", items: [], socket: null, refreshTimer: 0 };
  const encoder = new TextEncoder();
  const decoder = new TextDecoder("utf-8", { fatal: true });

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

  async function api(url, options = {}) {
    const response = await fetch(url, { ...options, headers: { ...(options.body ? { "Content-Type": "application/json" } : {}), ...options.headers } });
    if (response.status === 204) return null;
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error?.message || `Ошибка HTTP ${response.status}`);
    return data;
  }

  function uuid() { return crypto.randomUUID(); }
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

  function initHome() {
    show("home");
    $("room-id").value = uuid();
    $("random-id").addEventListener("click", () => { $("room-id").value = uuid(); });
    $("encrypted").addEventListener("change", () => show("encryption-options", $("encrypted").checked));
    $("toggle-create-key").addEventListener("click", (e) => togglePassword("room-key", e.currentTarget));
    $("create-form").addEventListener("submit", createRoom);
  }

  async function createRoom(event) {
    event.preventDefault(); message("create-error", "");
    const submit = event.submitter; submit.disabled = true;
    try {
      state.roomID = $("room-id").value;
      const encrypted = $("encrypted").checked;
      let keyId = ""; let keyText = "";
      if (encrypted) {
        if (!globalThis.crypto?.subtle) throw new Error("Шифрование требует HTTPS или localhost");
        const entered = $("room-key").value;
        const raw = entered ? await keyFromInput(entered) : crypto.getRandomValues(new Uint8Array(32));
        keyId = await keyID(raw); keyText = rawKeyText(raw);
      }
      await api("/api/rooms", { method: "POST", body: JSON.stringify({ id: state.roomID, encrypted, keyId }) });
      location.href = `/r/${encodeURIComponent(state.roomID)}${encrypted ? "#key=" + encodeURIComponent(keyText) : ""}`;
    } catch (error) { message("create-error", error.message); submit.disabled = false; }
  }

  async function initRoom() {
    show("room"); $("room-name").textContent = state.roomID;
    $("share").addEventListener("click", openShare);
    $("item-form").addEventListener("submit", addItem);
    $("items").addEventListener("click", itemAction);
    $("key-form").addEventListener("submit", unlockFromDialog);
    $("toggle-unlock-key").addEventListener("click", (e) => togglePassword("unlock-key", e.currentTarget));
    $("copy-link").addEventListener("click", async () => { await copyText($("share-url").value); toast("Ссылка скопирована"); });
    $("share-dialog").querySelector(".dialog-close").addEventListener("click", () => $("share-dialog").close());
    document.querySelectorAll('input[name="share-mode"]').forEach((el) => el.addEventListener("change", updateShare));
    try {
      await refresh();
      if (state.room.encrypted) await prepareKey();
      else { $("encryption-state").textContent = "Без шифрования"; renderItems(); }
      connect();
    } catch (error) { message("room-error", error.message); $("item-text").disabled = true; $("send").disabled = true; }
  }

  async function refresh() {
    const data = await api(`/api/rooms/${encodeURIComponent(state.roomID)}`);
    state.room = data.room; state.items = data.items;
    $("item-count").textContent = `${state.items.length} записей`;
    if (!state.room.encrypted || state.key) await renderItems();
  }

  async function prepareKey() {
    if (!crypto?.subtle) {
      message("crypto-warning", "Эта комната зашифрована. Откройте её по HTTPS или через localhost: Web Crypto недоступен в обычном HTTP-подключении.");
      $("item-text").disabled = true; $("send").disabled = true; return;
    }
    const fragment = new URLSearchParams(location.hash.slice(1));
    const supplied = fragment.get("key");
    if (supplied) {
      try { await unlock(supplied); return; } catch (_) { /* ask explicitly */ }
    }
    $("key-dialog").showModal();
  }

  async function unlock(value) {
    const raw = await keyFromInput(value);
    if (await keyID(raw) !== state.room.keyId) throw new Error("Ключ не подходит к этой комнате");
    state.key = await importRawKey(raw); state.keyText = rawKeyText(raw);
    $("encryption-state").textContent = "Сквозное шифрование включено";
    message("crypto-warning", "");
    await renderItems();
  }

  async function unlockFromDialog(event) {
    event.preventDefault(); message("key-error", "");
    try { await unlock($("unlock-key").value); $("key-dialog").close(); }
    catch (error) { message("key-error", error.message); }
  }

  async function encrypt(text) {
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const aad = encoder.encode(`clipboard-exchange:v1:${state.roomID}`);
    const plaintext = encoder.encode(JSON.stringify({ text }));
    const ciphertext = await crypto.subtle.encrypt({ name: "AES-GCM", iv, additionalData: aad, tagLength: 128 }, state.key, plaintext);
    return { kind: "encrypted", ciphertext: b64url(new Uint8Array(ciphertext)), iv: b64url(iv), keyId: state.room.keyId, version: 1 };
  }

  async function decrypt(item) {
    const aad = encoder.encode(`clipboard-exchange:v1:${state.roomID}`);
    const plaintext = await crypto.subtle.decrypt({ name: "AES-GCM", iv: fromB64url(item.iv), additionalData: aad, tagLength: 128 }, state.key, fromB64url(item.ciphertext));
    const parsed = JSON.parse(decoder.decode(plaintext));
    if (typeof parsed.text !== "string") throw new Error("Некорректные данные");
    return parsed.text;
  }

  async function addItem(event) {
    event.preventDefault(); message("room-error", "");
    const text = $("item-text").value;
    if (!text) return;
    $("send").disabled = true;
    try {
      const payload = state.room.encrypted ? await encrypt(text) : { kind: "text", content: text };
      payload.id = uuid();
      await api(`/api/rooms/${encodeURIComponent(state.roomID)}/items`, { method: "POST", body: JSON.stringify(payload) });
      $("item-text").value = ""; await refresh();
    } catch (error) { message("room-error", error.message); }
    finally { $("send").disabled = false; }
  }

  async function renderItems() {
    const container = $("items"); container.replaceChildren(); let failures = 0;
    for (const item of state.items) {
      let text = item.content;
      if (item.kind === "encrypted") {
        try { text = await decrypt(item); } catch (_) { text = "[Не удалось расшифровать запись]"; failures++; }
      }
      const article = document.createElement("article"); article.className = "item"; article.dataset.id = item.id;
      const pre = document.createElement("pre"); pre.className = "item-content"; pre.textContent = text;
      const footer = document.createElement("div"); footer.className = "item-footer";
      const time = document.createElement("time"); time.className = "item-time"; time.dateTime = item.createdAt;
      time.textContent = new Intl.DateTimeFormat(undefined, { dateStyle:"medium", timeStyle:"short" }).format(new Date(item.createdAt));
      const buttons = document.createElement("div"); buttons.className = "item-buttons";
      const copy = document.createElement("button"); copy.type = "button"; copy.className = "button secondary"; copy.dataset.action = "copy"; copy.textContent = "Копировать";
      const del = document.createElement("button"); del.type = "button"; del.className = "button secondary delete"; del.dataset.action = "delete"; del.textContent = "Удалить";
      buttons.append(copy, del); footer.append(time, buttons); article.append(pre, footer); container.append(article);
    }
    show("empty", state.items.length === 0);
    if (failures) message("room-error", `${failures} записей не удалось расшифровать`);
  }

  async function itemAction(event) {
    const button = event.target.closest("button[data-action]"); if (!button) return;
    const article = button.closest(".item");
    if (button.dataset.action === "copy") { await copyText(article.querySelector("pre").textContent); toast("Текст скопирован"); return; }
    if (button.dataset.action === "delete") {
      button.disabled = true;
      try { await api(`/api/rooms/${encodeURIComponent(state.roomID)}/items/${article.dataset.id}`, { method:"DELETE" }); await refresh(); }
      catch (error) { message("room-error", error.message); button.disabled = false; }
    }
  }

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

  function openShare() { updateShare(); $("share-dialog").showModal(); }
  function updateShare() {
    const withKey = !state.room?.encrypted || document.querySelector('input[name="share-mode"]:checked')?.value === "key";
    const base = `${location.origin}/r/${encodeURIComponent(state.roomID)}`;
    const url = state.room?.encrypted && withKey ? `${base}#key=${encodeURIComponent(state.keyText)}` : base;
    $("share-url").value = url;
    show("share-choice", Boolean(state.room?.encrypted));
    $("share-note").textContent = state.room?.encrypted && withKey ? "Эта ссылка содержит ключ и даёт полный доступ к комнате." : state.room?.encrypted ? "Ключ потребуется передать отдельно." : "Любой, у кого есть ссылка, сможет добавлять и удалять записи.";
    const qr = $("qr"); qr.replaceChildren();
    if (globalThis.QRCode) new QRCode(qr, { text:url, width:220, height:220, correctLevel:QRCode.CorrectLevel.M });
    else qr.textContent = "QR-код недоступен";
  }

  if (roomMatch) initRoom(); else if (location.pathname === "/") initHome();
})();
