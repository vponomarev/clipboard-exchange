"use strict";

const downloads = new Map();
const encoder = new TextEncoder();

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));
self.addEventListener("message", (event) => {
  if (event.data?.type !== "prepare-download" || !event.data.token) return;
  downloads.set(event.data.token, event.data.config);
  event.ports[0]?.postMessage({ ready:true });
});
self.addEventListener("fetch", (event) => {
  const match = new URL(event.request.url).pathname.match(/^\/client-download\/([0-9a-f-]{36})$/);
  if (!match) return;
  const config = downloads.get(match[1]);
  if (!config) { event.respondWith(new Response("Download session expired", { status:404 })); return; }
  event.respondWith(streamDownload(config));
});

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
        controller.close();
      } catch (error) { controller.error(error); }
    }
  });
  const filename = encodeURIComponent(config.name).replace(/[!'()*]/g, value => `%${value.charCodeAt(0).toString(16).toUpperCase()}`);
  return new Response(stream, { headers:{ "Content-Type":config.mimeType || "application/octet-stream", "Content-Length":String(config.size), "Content-Disposition":`attachment; filename*=UTF-8''${filename}`, "Cache-Control":"no-store" } });
}

function fromB64url(value) {
  const normalized = value.replaceAll("-", "+").replaceAll("_", "/");
  const binary = atob(normalized + "===".slice((normalized.length + 3) % 4));
  return Uint8Array.from(binary, (c) => c.charCodeAt(0));
}
