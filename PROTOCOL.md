# Clipboard Exchange protocol v2

`GET /api/capabilities` — источник negotiated limits и поддержанных версий.
Клиент не должен считать настроечные значения сервера равными defaults.

## Capability tokens

- write token: `cw1_` + 32 random bytes в unpadded base64url;
- upload token: `cu1_` + 32 random bytes в unpadded base64url;
- mutations используют `Authorization: ClipboardWrite <token>`;
- chunks, status, complete и abort используют
  `Authorization: ClipboardUpload <token>`.

Сервер хранит только unpadded-base64url SHA-256 полного token. Upload token
ограничен одной upload session. Секреты комнаты находятся в URL fragment и не
попадают в обычный request URL.

## Encrypted text v2

AES-256-GCM, random 96-bit IV, tag 128 bit. AAD:

```text
clipboard-exchange:v2:{roomID}
```

UTF-8 plaintext — compact JSON `{"text":"...","alias":"..."}`. Чтение v1
с AAD `clipboard-exchange:v1:{roomID}` остаётся поддержано.

## File upload v1

Server capability `limits.fileChunkBytes` задаёт plaintext chunk size. Open file
посылается без изменения. Encrypted file всегда состоит из `chunkCount` объектов
фиксированного размера `fileChunkBytes + 28`: 12-byte IV, AES-GCM ciphertext
полного plaintext chunk и 16-byte tag. Последний plaintext chunk дополняется
нулями; точная длина находится только в encrypted manifest.

AAD чанка:

```text
clipboard-exchange:file:v1:{roomID}:{fileID}:{index}:{plaintextChunkSize}
```

Manifest — AES-256-GCM JSON со случайным 96-bit IV и AAD:

```text
clipboard-exchange:file-manifest:v1:{roomID}:{fileID}
```

Payload:

```json
{"name":"exact name","mimeType":"application/octet-stream","size":123,"alias":"optional","chunkSize":1048576,"chunkCount":1}
```

`X-Clipboard-Chunk-IV` содержит unpadded-base64url IV и обязан совпадать с
первыми 12 байтами stored chunk. Nonce не может повторяться внутри upload.
Manifest отправляется в `POST .../complete`; до успешного complete файл не
появляется в комнате.

## Deterministic AES-256-GCM test vector

Вектор использует искусственно фиксированные IV только для interoperability test.
В production IV всегда генерируется CSPRNG.

```text
roomID=test-room
fileID=123e4567-e89b-12d3-a456-426614174000
key(base64url)=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8
chunkSize=16
chunkAAD=clipboard-exchange:file:v1:test-room:123e4567-e89b-12d3-a456-426614174000:0:16
chunkPlaintext(hex)=68656c6c6f0000000000000000000000
chunkIV(base64url)=AAECAwQFBgcICQoL
chunkCiphertextAndTag(base64url)=L2e6d6rlwhuNQZeLsel4bdiaIdDEtuOJ3wUwgkxN3fU
storedEnvelope(base64url)=AAECAwQFBgcICQoLL2e6d6rlwhuNQZeLsel4bdiaIdDEtuOJ3wUwgkxN3fU
manifestJSON={"name":"hello.txt","mimeType":"text/plain","size":5,"alias":"tester","chunkSize":16,"chunkCount":1}
manifestAAD=clipboard-exchange:file-manifest:v1:test-room:123e4567-e89b-12d3-a456-426614174000
manifestIV(base64url)=DA0ODxAREhMUFRYX
manifestCiphertextAndTag(base64url)=49wHuRIT3mkVQi25_eQIky13z8LUEFLH_vaDkIGC-thYkhJRzrVKAHadLH7M33Og6XSB5IXWdd8nngl-gM8-3WheJbnTnv4ynQ3ZEBNlcO0YEv1xffRjXuNDpIhx0Uvvl5nvlUDFaUm9X4D3Xu_-j18BhkQ
```
