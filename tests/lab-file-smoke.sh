#!/bin/sh
set -eu

binary=${1:-./clipboard-exchange-linux-amd64}
port=${2:-18081}
work=/tmp/clipboard-exchange-file-smoke
case "$work" in /tmp/clipboard-exchange-file-smoke) ;; *) exit 2 ;; esac
rm -rf "$work"
mkdir -p "$work/files"

"$binary" --listen="127.0.0.1:$port" --database="$work/data.db" --files-dir="$work/files" --room-ttl=0 --rate-limit=0 >"$work/server.log" 2>&1 &
server_pid=$!
cleanup() { kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

attempt=0
until curl -fsS "http://127.0.0.1:$port/readyz" >/dev/null; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 50 ]; then cat "$work/server.log"; exit 1; fi
  sleep 0.1
done

write_token=cw1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
auth="Authorization: ClipboardWrite $write_token"
curl -fsS -H 'Content-Type: application/json' -d "{\"id\":\"large-file\",\"encrypted\":false,\"keyId\":\"\",\"writeToken\":\"$write_token\"}" "http://127.0.0.1:$port/api/rooms" >/dev/null

size=524288000
truncate -s "$size" "$work/input.bin"
upload_json=$(curl -fsS -H "$auth" -H 'Content-Type: application/json' -d "{\"name\":\"500MiB.bin\",\"mimeType\":\"application/octet-stream\",\"alias\":\"lab\",\"size\":$size}" "http://127.0.0.1:$port/api/rooms/large-file/uploads")
upload_id=$(printf '%s' "$upload_json" | jq -r .id)
file_id=$(printf '%s' "$upload_json" | jq -r .fileId)
upload_token=$(printf '%s' "$upload_json" | jq -r .uploadToken)
chunk_count=$(printf '%s' "$upload_json" | jq -r .chunkCount)

index=0
while [ "$index" -lt "$chunk_count" ]; do
  dd if="$work/input.bin" bs=1048576 skip="$index" count=1 status=none | curl -fsS -X PUT -H "Authorization: ClipboardUpload $upload_token" --data-binary @- "http://127.0.0.1:$port/api/rooms/large-file/uploads/$upload_id/chunks/$index" >/dev/null
  index=$((index + 1))
  if [ $((index % 100)) -eq 0 ]; then printf 'uploaded_chunks=%s\n' "$index"; fi
done

curl -fsS -X POST -H "Authorization: ClipboardUpload $upload_token" "http://127.0.0.1:$port/api/rooms/large-file/uploads/$upload_id/complete" >/dev/null
input_sha=$(sha256sum "$work/input.bin" | awk '{print $1}')
download_sha=$(curl -fsS "http://127.0.0.1:$port/api/rooms/large-file/files/$file_id" | sha256sum | awk '{print $1}')
[ "$input_sha" = "$download_sha" ]
range_size=$(curl -fsS -H 'Range: bytes=524287900-524287999' "http://127.0.0.1:$port/api/rooms/large-file/files/$file_id" | wc -c)
[ "$range_size" -eq 100 ]
rss_kib=$(ps -o rss= -p "$server_pid" | tr -d ' ')
printf 'FILE_SMOKE_OK size=%s chunks=%s sha256=%s range_bytes=%s server_rss_kib=%s\n' "$size" "$chunk_count" "$input_sha" "$range_size" "$rss_kib"
