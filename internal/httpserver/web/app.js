(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const roomMatch = location.pathname.match(/^\/r\/([A-Za-z0-9][A-Za-z0-9_-]{0,63})$/);
  const fragment = new URLSearchParams(location.hash.slice(1));
  const state = { roomID: roomMatch ? roomMatch[1] : "", room: null, key: null, keyText: "", writeToken: fragment.get("write") || "", canWrite: Boolean(fragment.get("write")), items: [], files: [], capabilities: null, downloadRegistration: null, socket: null, refreshTimer: 0 };
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
    if (!response.ok) throw new Error(data.error?.message || `Ошибка HTTP ${response.status}`);
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
    const requestedRoom = new URLSearchParams(location.search).get("room");
    $("room-id").value = requestedRoom && /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(requestedRoom) ? requestedRoom : uuid();
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
      const writeProtected = $("write-protected").checked;
      let keyId = ""; let keyText = "";
      if (encrypted) {
        if (!globalThis.crypto?.subtle) throw new Error("Шифрование требует HTTPS или localhost");
        const entered = $("room-key").value;
        const raw = entered ? await keyFromInput(entered) : crypto.getRandomValues(new Uint8Array(32));
        keyId = await keyID(raw); keyText = rawKeyText(raw);
      }
      const writeToken = writeProtected ? newWriteToken() : "";
      await api("/api/rooms", { method: "POST", body: JSON.stringify({ id: state.roomID, encrypted, keyId, writeProtected, writeToken }) });
      const nextFragment = new URLSearchParams();
      if (writeProtected) nextFragment.set("write", writeToken);
      if (encrypted) nextFragment.set("key", keyText);
      const nextFragmentText = nextFragment.toString();
      location.href = `/r/${encodeURIComponent(state.roomID)}${nextFragmentText ? `#${nextFragmentText}` : ""}`;
    } catch (error) { message("create-error", error.message); submit.disabled = false; }
  }

  async function initRoom() {
    show("room"); $("room-name").textContent = state.roomID;
    $("file-input").disabled = true;
    $("share").addEventListener("click", openShare);
    $("item-form").addEventListener("submit", addItem);
    $("items").addEventListener("click", itemAction);
    $("key-form").addEventListener("submit", unlockFromDialog);
    $("toggle-unlock-key").addEventListener("click", (e) => togglePassword("unlock-key", e.currentTarget));
    $("copy-link").addEventListener("click", async () => { await copyText($("share-url").value); toast("Ссылка скопирована"); });
    $("share-dialog").querySelector(".dialog-close").addEventListener("click", () => $("share-dialog").close());
    document.querySelectorAll('input[name="share-permission"], input[name="share-key"]').forEach((el) => el.addEventListener("change", updateShare));
    $("rotate-write").addEventListener("click", rotateWriteCapability);
    $("alias").value = loadAlias();
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
    } catch (error) { message("room-error", error.message); $("item-text").disabled = true; $("send").disabled = true; }
  }

  async function refresh() {
    const data = await api(`/api/rooms/${encodeURIComponent(state.roomID)}`);
    state.room = data.room; state.items = data.items; state.files = data.files || [];
    updateAccessState();
    $("item-count").textContent = `${state.items.length} записей · ${state.files.length} файлов`;
    if (!state.room.encrypted || state.key) await renderItems();
  }

  function updateAccessState() {
    if (!state.room) return;
    state.canWrite = !state.room.writeProtected || Boolean(state.writeToken);
    show("item-form", state.canWrite);
    show("access-notice", !state.canWrite);
    show("file-drop", state.canWrite);
    $("file-input").disabled = !state.canWrite || (state.room.encrypted && !state.key);
  }

  async function prepareKey() {
    if (!crypto?.subtle) {
      message("crypto-warning", "Эта комната зашифрована. Откройте её по HTTPS или через localhost: Web Crypto недоступен в обычном HTTP-подключении.");
      $("item-text").disabled = true; $("send").disabled = true; return;
    }
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
    updateAccessState();
    $("encryption-state").textContent = "Сквозное шифрование включено";
    message("crypto-warning", "");
    await renderItems();
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
    if (!text) return;
    const alias = $("alias").value;
    if (Array.from(alias).length > 64) { message("room-error", "Alias не может быть длиннее 64 символов"); return; }
    $("send").disabled = true;
    try {
      const payload = state.room.encrypted ? await encrypt(text, alias) : { kind: "text", content: text, alias };
      payload.id = uuid();
      await api(`/api/rooms/${encodeURIComponent(state.roomID)}/items`, { method: "POST", body: JSON.stringify(payload), write: true });
      $("item-text").value = ""; await refresh();
    } catch (error) { message("room-error", error.message); }
    finally { $("send").disabled = false; }
  }

  async function renderItems() {
    const container = $("items"); container.replaceChildren(); let failures = 0;
    for (const item of state.items) {
      let text = item.content, alias = item.alias || "";
      if (item.kind === "encrypted") {
        try { const decrypted = await decrypt(item); text = decrypted.text; alias = decrypted.alias; } catch (_) { text = "[Не удалось расшифровать запись]"; alias = ""; failures++; }
      }
      const article = document.createElement("article"); article.className = "item"; article.dataset.id = item.id;
      const pre = document.createElement("pre"); pre.className = "item-content"; pre.textContent = text;
      const footer = document.createElement("div"); footer.className = "item-footer";
      const time = document.createElement("time"); time.className = "item-time"; time.dateTime = item.createdAt;
      time.textContent = new Intl.DateTimeFormat(undefined, { dateStyle:"medium", timeStyle:"short" }).format(new Date(item.createdAt));
      const meta = document.createElement("div"); meta.className = "item-meta";
      if (alias) { const author = document.createElement("span"); author.className = "item-alias"; author.textContent = alias; meta.append(author); }
      meta.append(time);
      const buttons = document.createElement("div"); buttons.className = "item-buttons";
      const toggle = document.createElement("button"); toggle.type = "button"; toggle.className = "button secondary hidden"; toggle.dataset.action = "toggle"; toggle.textContent = "Развернуть"; toggle.setAttribute("aria-expanded", "false");
      const copy = document.createElement("button"); copy.type = "button"; copy.className = "button secondary"; copy.dataset.action = "copy"; copy.textContent = "Копировать";
      const del = document.createElement("button"); del.type = "button"; del.className = "button secondary delete"; del.dataset.action = "delete"; del.textContent = "Удалить";
      buttons.append(toggle, copy); if (state.canWrite) buttons.append(del); footer.append(meta, buttons); article.append(pre, footer); container.append(article);
      updateItemOverflow(article);
    }
    for (const file of state.files) await renderFile(container, file);
    show("empty", state.items.length === 0 && state.files.length === 0);
    if (failures) message("room-error", `${failures} записей не удалось расшифровать`);
  }

  async function itemAction(event) {
    const button = event.target.closest("button[data-action]"); if (!button) return;
    const article = button.closest(".item");
    if (button.dataset.action === "toggle") {
      const pre = article.querySelector("pre");
      const expanded = pre.classList.contains("collapsed");
      pre.classList.toggle("collapsed", !expanded);
      button.textContent = expanded ? "Свернуть" : "Развернуть";
      button.setAttribute("aria-expanded", String(expanded));
      return;
    }
    if (button.dataset.action === "copy") { await copyText(article.querySelector("pre").textContent); toast("Текст скопирован"); return; }
    if (button.dataset.action === "file-copy") { await copyText(article.dataset.name); toast("Имя файла скопировано"); return; }
    if (button.dataset.action === "encrypted-download") { await startEncryptedDownload(article); return; }
    if (button.dataset.action === "file-delete") {
      button.disabled = true;
      try { await api(`/api/rooms/${encodeURIComponent(state.roomID)}/files/${article.dataset.id}`, { method:"DELETE", write:true }); await refresh(); }
      catch (error) { message("room-error", error.message); button.disabled = false; }
      return;
    }
    if (button.dataset.action === "delete") {
      button.disabled = true;
      try { await api(`/api/rooms/${encodeURIComponent(state.roomID)}/items/${article.dataset.id}`, { method:"DELETE", write:true }); await refresh(); }
      catch (error) { message("room-error", error.message); button.disabled = false; }
    }
  }

  async function renderFile(container, file) {
    let metadata = file;
    if (file.encrypted) {
      try { metadata = { ...file, ...(await decryptFileManifest(file)) }; }
      catch (_) { metadata = { ...file, name:"[Не удалось расшифровать имя файла]", mimeType:"application/octet-stream", alias:"", plaintextSize:0 }; }
    }
    const article = document.createElement("article"); article.className = "item file-item"; article.dataset.id = file.id; article.dataset.name = file.name;
    const details = document.createElement("div"); details.className = "file-details";
    article.dataset.name = metadata.name;
    const name = document.createElement("strong"); name.className = "file-name"; name.textContent = metadata.name;
    const meta = document.createElement("div"); meta.className = "item-meta";
    if (metadata.alias) { const alias = document.createElement("span"); alias.className = "item-alias"; alias.textContent = metadata.alias; meta.append(alias); }
    const size = document.createElement("span"); size.className = "muted"; size.textContent = formatBytes(file.encrypted ? metadata.size : file.size); meta.append(size);
    const time = document.createElement("time"); time.className = "item-time"; time.dateTime = file.createdAt;
    time.textContent = new Intl.DateTimeFormat(undefined, { dateStyle:"medium", timeStyle:"short" }).format(new Date(file.createdAt)); meta.append(time);
    details.append(name, meta);
    const buttons = document.createElement("div"); buttons.className = "item-buttons";
    const download = document.createElement(file.encrypted ? "button" : "a"); download.className = "button primary"; download.textContent = "Скачать";
    if (file.encrypted) { download.type = "button"; download.dataset.action = "encrypted-download"; article.dataset.metadata = JSON.stringify(metadata); }
    else { download.download = file.name; download.href = `/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}`; }
    const copy = document.createElement("button"); copy.type = "button"; copy.className = "button secondary"; copy.dataset.action = "file-copy"; copy.textContent = "Копировать имя";
    buttons.append(download, copy);
    if (state.canWrite) { const del = document.createElement("button"); del.type = "button"; del.className = "button secondary delete"; del.dataset.action = "file-delete"; del.textContent = "Удалить"; buttons.append(del); }
    article.append(details, buttons); container.append(article);
  }

  function formatBytes(value) {
    if (value < 1024) return `${value} Б`;
    const units = ["КиБ", "МиБ", "ГиБ"]; let amount = value; let unit = -1;
    do { amount /= 1024; unit++; } while (amount >= 1024 && unit < units.length - 1);
    return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[unit]}`;
  }

  async function ensureDownloadWorker() {
    if (!navigator.serviceWorker) throw new Error("Браузер не поддерживает потоковое скачивание зашифрованных файлов");
    state.downloadRegistration = await navigator.serviceWorker.register("/assets/download-sw.js?v=4", { scope:"/" });
    await navigator.serviceWorker.ready;
    if (!navigator.serviceWorker.controller) {
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("Service Worker не активирован")), 5000);
        navigator.serviceWorker.addEventListener("controllerchange", () => { clearTimeout(timer); resolve(); }, { once:true });
      });
    }
  }

  async function startEncryptedDownload(article) {
    try {
      if (!state.key || !state.keyText.startsWith("ce1_")) throw new Error("Сначала откройте ключ комнаты");
      await ensureDownloadWorker();
      const file = state.files.find((entry) => entry.id === article.dataset.id);
      const metadata = JSON.parse(article.dataset.metadata);
      const token = uuid();
      const channel = new MessageChannel();
      const ready = new Promise((resolve, reject) => { const timer = setTimeout(() => reject(new Error("Service Worker не ответил")), 5000); channel.port1.onmessage = () => { clearTimeout(timer); resolve(); }; });
      navigator.serviceWorker.controller.postMessage({ type:"prepare-download", token, config:{ rawKey:state.keyText.slice(4), roomID:state.roomID, fileID:file.id, chunkSize:file.chunkSize, chunkCount:file.chunkCount, size:metadata.size, name:metadata.name, mimeType:metadata.mimeType, url:`/api/rooms/${encodeURIComponent(state.roomID)}/files/${file.id}` } }, [channel.port2]);
      await ready;
      const frame = document.createElement("iframe"); frame.hidden = true; frame.src = `/client-download/${token}`; document.body.append(frame); setTimeout(() => frame.remove(), 60000);
    } catch (error) { message("room-error", error.message); }
  }

  function uploadStorageKey(file) { return `clipboard-exchange:upload:${state.roomID}:${file.name}:${file.size}`; }
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
    const cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button secondary"; cancel.textContent = "Отмена";
    const progress = document.createElement("progress"); progress.max = Math.max(file.size, 1); progress.value = 0;
    const status = document.createElement("span"); status.className = "muted"; status.textContent = "Подготовка…";
    actions.append(cancel); row.append(name, actions, progress, status); $("upload-list").append(row);
    const control = { controller: null, cancelled: false, session: null };
    cancel.addEventListener("click", async () => {
      control.cancelled = true; control.controller?.abort(); cancel.disabled = true;
      if (control.session) await uploadAPI(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads/${control.session.id}`, { method:"DELETE" }, control.session.uploadToken).catch(() => {});
      removeUploadSession(file); row.remove();
    });
    runUpload(file, row, progress, status, actions, control).catch(() => {});
  }

  async function runUpload(file, row, progress, status, actions, control) {
    try {
      control.cancelled = false;
      let session = loadUploadSession(file);
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
      if (!session) {
        status.textContent = "Создание загрузки…";
        const encrypted = state.room.encrypted;
        if (encrypted && !state.key) throw new Error("Сначала откройте ключ комнаты");
        const plainChunkSize = state.capabilities.limits.fileChunkBytes;
        const chunkCount = Math.max(1, Math.ceil(file.size / plainChunkSize));
        const storedSize = encrypted ? chunkCount * (plainChunkSize + 28) : file.size;
        const alias = $("alias").value;
        session = await api(`/api/rooms/${encodeURIComponent(state.roomID)}/uploads`, { method:"POST", body:JSON.stringify({ name:encrypted ? "" : file.name, mimeType:encrypted ? "" : (file.type || "application/octet-stream"), alias:encrypted ? "" : alias, size:storedSize, encrypted, keyId:encrypted ? state.room.keyId : "" }), write:true });
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
      removeUploadSession(file); status.textContent = "Готово"; await refresh(); setTimeout(() => row.remove(), 700);
    } catch (error) {
      if (control.cancelled || error.name === "AbortError") return;
      status.textContent = `Ошибка: ${error.message}`;
      let retry = actions.querySelector('[data-action="upload-retry"]');
      if (!retry) { retry = document.createElement("button"); retry.type = "button"; retry.className = "button secondary"; retry.dataset.action = "upload-retry"; retry.textContent = "Повторить"; actions.prepend(retry); }
      retry.onclick = () => { retry.remove(); runUpload(file, row, progress, status, actions, control).catch(() => {}); };
    }
  }

  function updateItemOverflow(article) {
    const pre = article.querySelector(".item-content");
    const toggle = article.querySelector('[data-action="toggle"]');
    const expanded = toggle.getAttribute("aria-expanded") === "true";
    pre.classList.add("collapsed");
    const overflowing = pre.scrollHeight > pre.clientHeight + 1;
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

  function openShare() { updateShare(); $("share-dialog").showModal(); }
  function updateShare() {
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

  if (roomMatch) initRoom(); else if (location.pathname === "/") initHome();
})();
