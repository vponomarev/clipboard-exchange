"use strict";

const downloads = new Map();
const encoder = new TextEncoder();
const appCache = "clipboard-exchange-shell-v9";
const shell = ["/", "/assets/style.css?v=14", "/assets/app.js?v=17", "/assets/qrcode.min.js", "/assets/manifest.webmanifest?v=2", "/assets/icon.svg", "/assets/icon-192.png", "/assets/icon-512.png"];

self.addEventListener("install", (event) => event.waitUntil(caches.open(appCache).then(cache => cache.addAll(shell)).then(() => self.skipWaiting())));
self.addEventListener("activate", (event) => event.waitUntil(Promise.all([caches.keys().then(keys => Promise.all(keys.filter(key => key !== appCache).map(key => caches.delete(key)))), self.clients.claim()])));
self.addEventListener("message", (event) => {
  if (event.data?.type !== "prepare-download" || !event.data.token) return;
  downloads.set(event.data.token, event.data.config);
  event.ports[0]?.postMessage({ ready:true });
});
self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  const match = url.pathname.match(/^\/client-download\/([0-9a-f-]{36})$/);
  if (match) {
    const config = downloads.get(match[1]);
    if (!config) { event.respondWith(new Response("Download session expired", { status:404 })); return; }
    event.respondWith(streamDownload(config));
    return;
  }
  if (event.request.method === "POST" && url.pathname === "/share-target") {
    event.respondWith(receiveShare(event.request));
    return;
  }
  if (event.request.method !== "GET" || url.origin !== location.origin || url.pathname.startsWith("/api/") || url.pathname === "/metrics") return;
  if (event.request.mode === "navigate") {
    event.respondWith(fetch(event.request).catch(() => caches.match("/")));
    return;
  }
  if (url.pathname.startsWith("/assets/")) event.respondWith(cacheAsset(event.request));
});

async function cacheAsset(request) {
  const cache = await caches.open(appCache);
  const cached = await cache.match(request);
  const network = fetch(request).then(response => { if (response.ok) cache.put(request, response.clone()); return response; }).catch(() => cached || Response.error());
  return cached || network;
}

async function receiveShare(request) {
  const form = await request.formData();
  const files = form.getAll("files").filter(value => value instanceof File && value.size >= 0);
  await putShared({ id:"pending", title:String(form.get("title") || ""), text:String(form.get("text") || ""), url:String(form.get("url") || ""), files, createdAt:Date.now() });
  return Response.redirect("/?shared=1", 303);
}

function putShared(value) {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open("clipboard-exchange-pwa", 2);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains("shared")) request.result.createObjectStore("shared", { keyPath:"id" });
      if (!request.result.objectStoreNames.contains("vault")) request.result.createObjectStore("vault", { keyPath:"id" });
    };
    request.onerror = () => reject(request.error);
    request.onsuccess = () => {
      const tx = request.result.transaction("shared", "readwrite");
      tx.objectStore("shared").put(value);
      tx.oncomplete = () => { request.result.close(); resolve(); };
      tx.onerror = () => reject(tx.error);
    };
  });
}

async function streamDownload(config) {
  const key = await crypto.subtle.importKey("raw", fromB64url(config.rawKey), "AES-GCM", false, ["decrypt"]);
  const stream = new ReadableStream({
    async start(controller) {
      try {
        const storedChunkSize = config.chunkSize + 28;
        for (let index = 0; index < config.chunkCount; index++) {
          const start = index * storedChunkSize;
          const response = await fetch(config.url, { headers:{ Range:`bytes=${start}-${start + storedChunkSize - 1}` }, cache:"no-store" });
          if (response.status !== 206 && !(response.status === 200 && config.chunkCount === 1)) throw new Error(`ciphertext HTTP ${response.status}`);
          const envelope = new Uint8Array(await response.arrayBuffer());
          if (envelope.length !== storedChunkSize) throw new Error("invalid encrypted chunk size");
          const iv = envelope.slice(0, 12);
          const aad = encoder.encode(`clipboard-exchange:file:v1:${config.roomID}:${config.fileID}:${index}:${config.chunkSize}`);
          const plaintext = new Uint8Array(await crypto.subtle.decrypt({ name:"AES-GCM", iv, additionalData:aad, tagLength:128 }, key, envelope.slice(12)));
          const remaining = config.size - index * config.chunkSize;
          controller.enqueue(plaintext.slice(0, Math.min(config.chunkSize, remaining)));
        }
		if (config.consumeURL) await fetch(config.consumeURL, { method:"POST", cache:"no-store" });
        controller.close();
      } catch (error) { controller.error(error); }
    }
  });
  const filename = encodeURIComponent(config.name).replace(/[!'()*]/g, value => `%${value.charCodeAt(0).toString(16).toUpperCase()}`);
  const disposition = config.disposition === "inline" && canPreview(config.mimeType) ? "inline" : "attachment";
  return new Response(stream, { headers:{ "Content-Type":config.mimeType || "application/octet-stream", "Content-Length":String(config.size), "Content-Disposition":`${disposition}; filename*=UTF-8''${filename}`, "Cache-Control":"no-store" } });
}

function canPreview(value) {
  const mediaType = String(value || "").split(";", 1)[0].trim().toLowerCase();
  if (mediaType.startsWith("audio/") || mediaType.startsWith("video/")) return true;
  if (mediaType.startsWith("image/") && mediaType !== "image/svg+xml") return true;
  return ["text/plain", "text/csv", "application/json", "application/pdf"].includes(mediaType);
}

function fromB64url(value) {
  const normalized = value.replaceAll("-", "+").replaceAll("_", "/");
  const binary = atob(normalized + "===".slice((normalized.length + 3) % 4));
  return Uint8Array.from(binary, (c) => c.charCodeAt(0));
}
