"use strict";

const downloads = new Map();
const encoder = new TextEncoder();
const appCache = "clipboard-exchange-shell-v11";
const shell = ["/", "/assets/style.css?v=15", "/assets/app.js?v=19", "/assets/qrcode.min.js", "/assets/manifest.webmanifest?v=2", "/assets/icon.svg", "/assets/icon-192.png", "/assets/icon-512.png"];

self.addEventListener("install", (event) => event.waitUntil(caches.open(appCache).then(cache => cache.addAll(shell)).then(() => self.skipWaiting())));
self.addEventListener("activate", (event) => event.waitUntil(Promise.all([caches.keys().then(keys => Promise.all(keys.filter(key => key !== appCache).map(key => caches.delete(key)))), self.clients.claim()])));
self.addEventListener("message", (event) => {
  if (!["prepare-download", "prepare-archive"].includes(event.data?.type) || !event.data.token) return;
  downloads.set(event.data.token, event.data.config);
  event.ports[0]?.postMessage({ ready:true });
});
self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  const match = url.pathname.match(/^\/client-download\/([0-9a-f-]{36})$/);
  if (match) {
    const config = downloads.get(match[1]);
    if (!config) { event.respondWith(new Response("Download session expired", { status:404 })); return; }
    event.respondWith(config.archive ? streamArchive(config) : streamDownload(config));
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

async function streamArchive(config) {
  const key = await crypto.subtle.importKey("raw", fromB64url(config.files[0].rawKey), "AES-GCM", false, ["decrypt"]);
  const names = uniqueArchiveNames(config.files.map(file => file.name));
  const stream = new ReadableStream({
    async start(controller) {
      try {
        let offset=0;
        const central=[];
        for (let index=0; index<config.files.length; index++) {
          const file=config.files[index], name=encoder.encode(names[index]);
          const stamp=dosTimestamp(new Date());
          const local=concatBytes(le32(0x04034b50),le16(20),le16(0x0808),le16(0),le16(stamp.time),le16(stamp.date),le32(0),le32(0),le32(0),le16(name.length),le16(0),name);
          controller.enqueue(local);
          const localOffset=offset; offset+=local.length;
          let crc=0xffffffff, size=0;
          for await (const chunk of decryptedChunks(file,key)) {
            crc=crc32Update(crc,chunk); size+=chunk.length; offset+=chunk.length; controller.enqueue(chunk);
          }
          if (size > 0xffffffff || offset > 0xffffffff) throw new Error("Архив больше 4 ГиБ пока не поддерживается браузером");
          crc=(crc^0xffffffff)>>>0;
          const descriptor=concatBytes(le32(0x08074b50),le32(crc),le32(size),le32(size));
          controller.enqueue(descriptor); offset+=descriptor.length;
          central.push({name,crc,size,offset:localOffset,stamp});
        }
        const centralOffset=offset;
        for (const item of central) {
          const header=concatBytes(le32(0x02014b50),le16(20),le16(20),le16(0x0808),le16(0),le16(item.stamp.time),le16(item.stamp.date),le32(item.crc),le32(item.size),le32(item.size),le16(item.name.length),le16(0),le16(0),le16(0),le16(0),le32(0),le32(item.offset),item.name);
          controller.enqueue(header); offset+=header.length;
        }
        if (central.length > 0xffff) throw new Error("В архиве слишком много файлов");
        controller.enqueue(concatBytes(le32(0x06054b50),le16(0),le16(0),le16(central.length),le16(central.length),le32(offset-centralOffset),le32(centralOffset),le16(0)));
		if (config.consumeURL) await fetch(config.consumeURL,{method:"POST",cache:"no-store"});
        controller.close();
      } catch(error) { controller.error(error); }
    }
  });
  const filename=encodeURIComponent(config.name).replace(/[!'()*]/g,value=>`%${value.charCodeAt(0).toString(16).toUpperCase()}`);
  return new Response(stream,{headers:{"Content-Type":"application/zip","Content-Disposition":`attachment; filename*=UTF-8''${filename}`,"Cache-Control":"no-store"}});
}

async function* decryptedChunks(config,key) {
  const storedChunkSize=config.chunkSize+28;
  for (let index=0; index<config.chunkCount; index++) {
    const start=index*storedChunkSize;
    const response=await fetch(config.url,{headers:{Range:`bytes=${start}-${start+storedChunkSize-1}`},cache:"no-store"});
    if (response.status!==206 && !(response.status===200 && config.chunkCount===1)) throw new Error(`ciphertext HTTP ${response.status}`);
    const envelope=new Uint8Array(await response.arrayBuffer());
    if (envelope.length!==storedChunkSize) throw new Error("invalid encrypted chunk size");
    const iv=envelope.slice(0,12), aad=encoder.encode(`clipboard-exchange:file:v1:${config.roomID}:${config.fileID}:${index}:${config.chunkSize}`);
    const plaintext=new Uint8Array(await crypto.subtle.decrypt({name:"AES-GCM",iv,additionalData:aad,tagLength:128},key,envelope.slice(12)));
    const remaining=config.size-index*config.chunkSize;
    yield plaintext.slice(0,Math.min(config.chunkSize,remaining));
  }
}

const crcTable=Array.from({length:256},(_,index)=>{let value=index;for(let bit=0;bit<8;bit++)value=(value&1)?(0xedb88320^(value>>>1)):(value>>>1);return value>>>0;});
function crc32Update(crc,bytes) { for(const byte of bytes) crc=crcTable[(crc^byte)&0xff]^(crc>>>8); return crc>>>0; }
function le16(value) { const bytes=new Uint8Array(2); new DataView(bytes.buffer).setUint16(0,value,true); return bytes; }
function le32(value) { const bytes=new Uint8Array(4); new DataView(bytes.buffer).setUint32(0,value>>>0,true); return bytes; }
function concatBytes(...parts) { const result=new Uint8Array(parts.reduce((sum,part)=>sum+part.length,0)); let offset=0; for(const part of parts){result.set(part,offset);offset+=part.length;} return result; }
function dosTimestamp(date) { const year=Math.max(1980,date.getFullYear()); return {time:(date.getHours()<<11)|(date.getMinutes()<<5)|(date.getSeconds()>>1),date:((year-1980)<<9)|((date.getMonth()+1)<<5)|date.getDate()}; }
function uniqueArchiveNames(names) {
  const used=new Set();
  return names.map(value=>{
    const parts=String(value||"file").replaceAll("\\","/").split("/"), name=parts.at(-1)||"file";
    let candidate=name, suffix=2;
    while(used.has(candidate.toLocaleLowerCase())) { const dot=name.lastIndexOf("."); candidate=dot>0 ? `${name.slice(0,dot)} (${suffix++})${name.slice(dot)}` : `${name} (${suffix++})`; }
    used.add(candidate.toLocaleLowerCase()); return candidate;
  });
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
