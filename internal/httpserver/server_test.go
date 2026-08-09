package httpserver

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/vponomarev/clipboard-exchange/internal/config"
	"github.com/vponomarev/clipboard-exchange/internal/filestore"
	"github.com/vponomarev/clipboard-exchange/internal/store"
)

const testWriteToken = "cw1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.Default()
	cfg.RateLimit = 0
	files, err := filestore.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(cfg, db, files, log.New(io.Discard, "", 0)))
	t.Cleanup(ts.Close)
	return ts
}

func requestJSON(t *testing.T, client *http.Client, method, url string, body any) *http.Response {
	return requestJSONWithToken(t, client, method, url, body, "")
}

func requestJSONWithToken(t *testing.T, client *http.Client, method, url string, body any, token string) *http.Response {
	t.Helper()
	var data io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, url, data)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "ClipboardWrite "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func requestUpload(t *testing.T, client *http.Client, method, url string, body io.Reader, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "ClipboardUpload "+token)
	if method == http.MethodPost && body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func requestEncryptedChunk(t *testing.T, client *http.Client, url string, body []byte, token, iv string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "ClipboardUpload "+token)
	req.Header.Set("X-Clipboard-Chunk-IV", iv)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRoomCRUDPreservesTextAndSecurityHeaders(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "my-ex", "encrypted": false, "keyId": "", "writeProtected": false, "writeToken": ""})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %s", resp.Status)
	}
	resp.Body.Close()
	text := "  echo $HOME\n\tline two\n"
	resp = requestJSON(t, client, "POST", ts.URL+"/api/rooms/my-ex/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": text, "alias": "Вася"})
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("add: %s %s", resp.Status, b)
	}
	resp.Body.Close()
	resp, err := client.Get(ts.URL + "/api/rooms/my-ex")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("missing CSP")
	}
	var got roomResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Room.WriteProtected || len(got.Items) != 1 || got.Items[0].Content != text || got.Items[0].Alias != "Вася" {
		t.Fatalf("content changed: %#v", got.Items)
	}
	resp = requestJSON(t, client, "DELETE", ts.URL+"/api/rooms/my-ex/items/123e4567-e89b-12d3-a456-426614174000", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("delete: %s", resp.Status)
	}
	resp.Body.Close()
}

func TestRealtimeNotification(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "live", "encrypted": false, "keyId": "", "writeToken": testWriteToken})
	resp.Body.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/rooms/live/events"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var event map[string]string
	if err := wsjson.Read(ctx, conn, &event); err != nil || event["type"] != "ready" {
		t.Fatalf("ready: %#v %v", event, err)
	}
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/live/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": "hello"}, testWriteToken)
	resp.Body.Close()
	if err := wsjson.Read(ctx, conn, &event); err != nil || event["type"] != "refresh" {
		t.Fatalf("refresh: %#v %v", event, err)
	}
}

func TestWriteCapabilityReadOnlyWrongAndRotation(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "rights", "encrypted": false, "keyId": "", "writeToken": testWriteToken})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %s", resp.Status)
	}
	resp.Body.Close()

	item := map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": "hello"}
	resp = requestJSON(t, client, "POST", ts.URL+"/api/rooms/rights/items", item)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing capability: %s", resp.Status)
	}
	resp.Body.Close()
	wrong := "cw1_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/rights/items", item, wrong)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong capability: %s", resp.Status)
	}
	resp.Body.Close()

	next := "cw1_ccccccccccccccccccccccccccccccccccccccccccc"
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/rights/write-capability/rotate", map[string]string{"writeToken": next}, testWriteToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rotate: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/rights/items", item, testWriteToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("old capability remained valid: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/rights/items", item, next)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("new capability: %s", resp.Status)
	}
	resp.Body.Close()
}

func TestOpenWriteRoomRejectsCapabilityAndRotation(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	open := false
	resp := requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms", map[string]any{
		"id": "open-rights", "encrypted": false, "keyId": "", "writeProtected": open, "writeToken": testWriteToken,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("open room accepted a write capability: %s", resp.Status)
	}
	resp.Body.Close()

	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms", map[string]any{
		"id": "open-rights", "encrypted": false, "keyId": "", "writeProtected": open,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create open room: %s", resp.Status)
	}
	resp.Body.Close()

	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/open-rights/write-capability/rotate", map[string]string{"writeToken": testWriteToken})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("open room allowed capability rotation: %s", resp.Status)
	}
	var failure map[string]map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if failure["error"]["code"] != "write_capability_disabled" {
		t.Fatalf("unexpected rotation error: %#v", failure)
	}
}

func TestCapabilitiesAndAliasLimit(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp, err := client.Get(ts.URL + "/api/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	var capabilities map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&capabilities); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if capabilities["protocolVersion"] != float64(4) || capabilities["writeCapabilities"] != true || capabilities["openWriteRooms"] != true || capabilities["groupedAttachments"] != true || capabilities["inlineFiles"] != true || capabilities["aliases"] != true {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}

	resp = requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "alias", "encrypted": false, "keyId": "", "writeToken": testWriteToken})
	resp.Body.Close()
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/alias/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": "x", "alias": strings.Repeat("a", 65)}, testWriteToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("long alias accepted: %s", resp.Status)
	}
	resp.Body.Close()
}

func TestInlineMIMETypeRejectsActiveContent(t *testing.T) {
	for _, value := range []string{"text/plain; charset=utf-8", "application/pdf", "image/png", "audio/ogg", "video/mp4"} {
		if !inlineMIMEType(value) {
			t.Errorf("safe preview type rejected: %s", value)
		}
	}
	for _, value := range []string{"text/html", "image/svg+xml", "application/javascript", "application/octet-stream"} {
		if inlineMIMEType(value) {
			t.Errorf("active or unsupported preview type accepted: %s", value)
		}
	}
}

func TestOpenFileUploadResumeRangeAndDelete(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	entryID := "123e4567-e89b-12d3-a456-426614174099"
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "files", "encrypted": false, "keyId": "", "writeToken": testWriteToken})
	resp.Body.Close()
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/files/uploads", map[string]any{"entryId": entryID, "entryIndex": 7, "name": "exact name.txt", "mimeType": "text/plain", "alias": "Вася", "size": 5}, testWriteToken)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create upload: %s %s", resp.Status, body)
	}
	var created struct {
		ID          string `json:"id"`
		FileID      string `json:"fileId"`
		EntryID     string `json:"entryId"`
		EntryIndex  int    `json:"entryIndex"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if created.EntryID != entryID || created.EntryIndex != 7 {
		t.Fatalf("entry metadata changed: %#v", created)
	}
	chunkURL := ts.URL + "/api/rooms/files/uploads/" + created.ID + "/chunks/0"
	resp = requestUpload(t, client, "PUT", chunkURL, strings.NewReader("hello"), created.UploadToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put chunk: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestUpload(t, client, "PUT", chunkURL, strings.NewReader("hello"), created.UploadToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("idempotent put: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestUpload(t, client, "PUT", chunkURL, strings.NewReader("HELLO"), created.UploadToken)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting put: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestUpload(t, client, "POST", ts.URL+"/api/rooms/files/uploads/"+created.ID+"/complete", nil, created.UploadToken)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("complete: %s %s", resp.Status, body)
	}
	resp.Body.Close()
	resp = requestJSONWithToken(t, client, http.MethodPost, ts.URL+"/api/rooms/files/items", map[string]any{"id": entryID, "kind": "text", "content": "message with attachment", "alias": "Вася"}, testWriteToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create grouped text: %s", resp.Status)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/api/rooms/files/files/"+created.FileID, nil)
	req.Header.Set("Range", "bytes=1-3")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "ell" || !strings.Contains(resp.Header.Get("Content-Disposition"), "exact name.txt") {
		t.Fatalf("range download: status=%s body=%q disposition=%q", resp.Status, body, resp.Header.Get("Content-Disposition"))
	}
	resp, err = client.Get(ts.URL + "/api/rooms/files/files/" + created.FileID + "?inline=1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("inline view: status=%s disposition=%q", resp.Status, resp.Header.Get("Content-Disposition"))
	}
	resp.Body.Close()

	resp = requestJSON(t, client, "GET", ts.URL+"/api/rooms/files", nil)
	var room roomResponse
	if err := json.NewDecoder(resp.Body).Decode(&room); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(room.Files) != 1 || room.Files[0].EntryID != entryID || room.Files[0].EntryIndex != 7 || room.Files[0].Name != "exact name.txt" || room.Files[0].Alias != "Вася" || room.Files[0].Size != 5 {
		t.Fatalf("file metadata changed: %#v", room.Files)
	}
	resp = requestJSON(t, client, "DELETE", ts.URL+"/api/rooms/files/entries/"+entryID, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only delete: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSONWithToken(t, client, "DELETE", ts.URL+"/api/rooms/files/entries/"+entryID, nil, testWriteToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete entry: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSON(t, client, http.MethodGet, ts.URL+"/api/rooms/files", nil)
	if err := json.NewDecoder(resp.Body).Decode(&room); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(room.Items) != 0 || len(room.Files) != 0 {
		t.Fatalf("entry was only partially deleted: %#v", room)
	}
}

func TestEncryptedFileMetadataAndBytesRemainOpaque(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	keyID := strings.Repeat("k", 43)
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "encrypted-files", "encrypted": true, "keyId": keyID, "writeToken": testWriteToken})
	resp.Body.Close()
	storedSize := int64((1 << 20) + 28)
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/encrypted-files/uploads", map[string]any{"name": "", "mimeType": "", "alias": "", "size": storedSize, "encrypted": true, "keyId": keyID}, testWriteToken)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create encrypted upload: %s %s", resp.Status, body)
	}
	var created struct {
		ID          string `json:"id"`
		FileID      string `json:"fileId"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ivBytes := bytes.Repeat([]byte{7}, 12)
	iv := base64.RawURLEncoding.EncodeToString(ivBytes)
	ciphertext := append(append([]byte{}, ivBytes...), bytes.Repeat([]byte{9}, int(storedSize)-12)...)
	resp = requestEncryptedChunk(t, client, ts.URL+"/api/rooms/encrypted-files/uploads/"+created.ID+"/chunks/0", ciphertext, created.UploadToken, iv)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("encrypted chunk: %s %s", resp.Status, body)
	}
	resp.Body.Close()
	manifest := `{"ciphertext":"` + strings.Repeat("m", 24) + `","iv":"` + strings.Repeat("i", 16) + `","keyId":"` + keyID + `","version":1}`
	resp = requestUpload(t, client, http.MethodPost, ts.URL+"/api/rooms/encrypted-files/uploads/"+created.ID+"/complete", strings.NewReader(manifest), created.UploadToken)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("complete encrypted upload: %s %s", resp.Status, body)
	}
	resp.Body.Close()
	resp = requestJSON(t, client, http.MethodGet, ts.URL+"/api/rooms/encrypted-files", nil)
	var room roomResponse
	if err := json.NewDecoder(resp.Body).Decode(&room); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(room.Files) != 1 || !room.Files[0].Encrypted || room.Files[0].Name != "" || room.Files[0].Alias != "" || room.Files[0].ManifestCiphertext == "" {
		t.Fatalf("encrypted metadata leaked or missing: %#v", room.Files)
	}
}

func TestFileEncryptionProtocolVector(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	fileID := "123e4567-e89b-12d3-a456-426614174000"
	plaintext := append([]byte("hello"), make([]byte, 11)...)
	iv := make([]byte, 12)
	for i := range iv {
		iv[i] = byte(i)
	}
	got := base64.RawURLEncoding.EncodeToString(gcm.Seal(nil, iv, plaintext, []byte("clipboard-exchange:file:v1:test-room:"+fileID+":0:16")))
	if want := "L2e6d6rlwhuNQZeLsel4bdiaIdDEtuOJ3wUwgkxN3fU"; got != want {
		t.Fatalf("chunk vector changed: %s", got)
	}
	manifest := `{"name":"hello.txt","mimeType":"text/plain","size":5,"alias":"tester","chunkSize":16,"chunkCount":1}`
	for i := range iv {
		iv[i] = byte(i + 12)
	}
	got = base64.RawURLEncoding.EncodeToString(gcm.Seal(nil, iv, []byte(manifest), []byte("clipboard-exchange:file-manifest:v1:test-room:"+fileID)))
	if want := "49wHuRIT3mkVQi25_eQIky13z8LUEFLH_vaDkIGC-thYkhJRzrVKAHadLH7M33Og6XSB5IXWdd8nngl-gM8-3WheJbnTnv4ynQ3ZEBNlcO0YEv1xffRjXuNDpIhx0Uvvl5nvlUDFaUm9X4D3Xu_-j18BhkQ"; got != want {
		t.Fatalf("manifest vector changed: %s", got)
	}
}

func TestRejectsPlaintextInEncryptedRoom(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	keyID := strings.Repeat("a", 43)
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "secure", "encrypted": true, "keyId": keyID, "writeToken": testWriteToken})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/secure/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": "leak"}, testWriteToken)
	if resp.StatusCode != 409 {
		t.Fatalf("expected conflict, got %s", resp.Status)
	}
	resp.Body.Close()
}

func TestServesEmbeddedApplication(t *testing.T) {
	ts := testServer(t)
	for _, path := range []string{"/", "/assets/app.js", "/assets/style.css", "/assets/qrcode.min.js"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("%s: %s", path, resp.Status)
		}
		resp.Body.Close()
	}

	resp, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`/assets/app.js?v=8`)) {
		t.Fatal("page does not cache-bust app.js")
	}
	if !bytes.Contains(body, []byte(`/assets/style.css?v=8`)) {
		t.Fatal("page does not cache-bust style.css")
	}

	resp, err = ts.Client().Get(ts.URL + "/assets/app.js?v=8")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache, max-age=0, must-revalidate" {
		t.Fatalf("unexpected asset cache policy: %q", got)
	}
}
