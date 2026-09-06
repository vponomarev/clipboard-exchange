'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { webcrypto } = require('node:crypto');

async function fixture() {
  const raw = webcrypto.getRandomValues(new Uint8Array(32));
  const key = await webcrypto.subtle.importKey('raw', raw, 'AES-GCM', false, ['encrypt']);
  const config = {rawKey:Buffer.from(raw).toString('base64url'), roomID:'test', fileID:'file', chunkSize:4, chunkCount:3, size:10, name:'file.txt', mimeType:'text/plain', url:'/cipher', consumeURL:'/consume'};
  const plaintext = Buffer.from('abcdefghij');
  const envelopes = [];
  for (let i=0; i<3; i++) {
    const iv=webcrypto.getRandomValues(new Uint8Array(12)), padded=new Uint8Array(4);
    padded.set(plaintext.subarray(i*4,i*4+4));
    const aad=new TextEncoder().encode(`clipboard-exchange:file:v1:test:file:${i}:4`);
    const cipher=await webcrypto.subtle.encrypt({name:'AES-GCM',iv,additionalData:aad},key,padded);
    envelopes.push(Buffer.concat([iv,Buffer.from(cipher)]));
  }
  const calls=[];
  const context=vm.createContext({self:{addEventListener(){}},crypto:webcrypto,TextEncoder,Uint8Array,DataView,ReadableStream,Response,AbortController,atob,URL,
    fetch:async(url,options)=>{
      calls.push({url,options});
      if (url==='/consume') return new Response(null,{status:204});
      const start=Number(options.headers.Range.match(/bytes=(\d+)-/)[1]);
      return new Response(envelopes[start/32],{status:206});
    }});
  vm.runInContext(fs.readFileSync(path.join(__dirname,'../../internal/httpserver/web/download-sw.js'),'utf8'),context);
  return {context,config,calls,envelopes,plaintext};
}

test('encrypted download is demand driven and cancellation keeps the source',async()=>{
  const f=await fixture(), response=await f.context.streamDownload(f.config);
  await new Promise(setImmediate);
  assert.equal(f.calls.length,0);
  const reader=response.body.getReader();
  assert.equal(Buffer.from((await reader.read()).value).toString(),'abcd');
  await new Promise(setImmediate);
  assert.equal(f.calls.length,1);
  await reader.cancel();
  assert.equal(f.calls.length,1);
  assert.equal(f.calls[0].options.signal.aborted,true);
});

test('complete encrypted file preserves bytes and acknowledges only after consumption',async()=>{
  const f=await fixture(), response=await f.context.streamDownload(f.config);
  const body=Buffer.from(await response.arrayBuffer());
  assert.deepEqual(body,f.plaintext);
  assert.deepEqual(f.calls.map(call=>call.url),['/cipher','/cipher','/cipher','/consume']);
});

test('inline preview does not consume an encrypted file',async()=>{
  const f=await fixture(), response=await f.context.streamDownload({...f.config,disposition:'inline'});
  await response.arrayBuffer();
  assert.equal(f.calls.filter(call=>call.url==='/consume').length,0);
});

test('authentication failure does not acknowledge a corrupt download',async()=>{
  const f=await fixture();f.envelopes[1][15]^=1;
  const response=await f.context.streamDownload(f.config);
  await assert.rejects(response.arrayBuffer());
  assert.equal(f.calls.filter(call=>call.url==='/consume').length,0);
});

test('cancelling a pending read aborts its fetch and never consumes the file',async()=>{
  const f=await fixture();
  let began;const started=new Promise(resolve=>{began=resolve;});let aborted=false;
  f.context.fetch=(_url,{signal})=>new Promise((_resolve,reject)=>{
    signal.addEventListener('abort',()=>{aborted=true;reject(new Error('aborted'));});began();
  });
  const response=await f.context.streamDownload(f.config), reader=response.body.getReader();
  const read=reader.read();await started;await reader.cancel();await read;
  assert.equal(aborted,true);
});

test('archive respects backpressure and produces a valid stored ZIP member',async()=>{
  const f=await fixture();
  const response=await f.context.streamArchive({files:[f.config],name:'files.zip',consumeURL:'/consume'});
  assert.equal(f.calls.length,0);
  const reader=response.body.getReader(), first=await reader.read();
  assert.equal(Buffer.from(first.value).readUInt32LE(),0x04034b50);
  assert.equal(f.calls.length,0); // Only the requested local header was produced.
  const chunks=[Buffer.from(first.value)];
  for (;;) {const next=await reader.read();if(next.done)break;chunks.push(Buffer.from(next.value));}
  const zip=Buffer.concat(chunks), nameLength=zip.readUInt16LE(26);
  assert.deepEqual(zip.subarray(30+nameLength,30+nameLength+10),f.plaintext);
  const end=zip.length-22, central=zip.readUInt32LE(end+16);
  assert.equal(zip.readUInt32LE(end),0x06054b50);
  assert.equal(zip.readUInt32LE(central),0x02014b50);
  assert.equal(zip.readUInt32LE(central+20),10);
  assert.equal(zip.readUInt32LE(central+16),0x3981703a); // CRC32 of abcdefghij.
  assert.equal(f.calls.at(-1).url,'/consume');
});

test('cancelled archive does not acknowledge receipt',async()=>{
  const f=await fixture();
  const response=await f.context.streamArchive({files:[f.config],name:'files.zip',consumeURL:'/consume'});
  const reader=response.body.getReader();await reader.read();await reader.cancel();
  assert.equal(f.calls.length,0);
});
