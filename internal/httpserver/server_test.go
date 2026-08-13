package httpserver

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	return testServerWithVersion(t, "dev")
}

func testServerWithVersion(t *testing.T, version string) *httptest.Server {
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
	ts := httptest.NewServer(NewWithVersion(cfg, db, files, log.New(io.Discard, "", 0), version))
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

func TestShortLinkEnvelopeAndOneTimeRedemption(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	ciphertext := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, shortLinkCipherBytes))
	iv := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 12))
	salt := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 16))
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{10}, 32))
	digest := sha256.Sum256([]byte(token))
	resp := requestJSON(t, client, http.MethodPost, ts.URL+"/api/short-links", map[string]any{"ciphertext": ciphertext, "iv": iv, "salt": salt, "tokenHash": fmt.Sprintf("%x", digest[:]), "kdfIterations": shortLinkKDFIterations, "expiresInSeconds": 3600, "maxUses": 1, "codeLength": 5})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create short link: %s %s", resp.Status, body)
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !shortCodePattern.MatchString(created.Code) || len(created.Code) != 5 {
		t.Fatalf("invalid generated code %q", created.Code)
	}
	getEnvelope := func() map[string]any {
		resp, err := client.Get(ts.URL + "/api/short-links/" + strings.ToLower(created.Code))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("get envelope: %s cache=%q", resp.Status, resp.Header.Get("Cache-Control"))
		}
		var envelope map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		return envelope
	}
	if got := getEnvelope(); got["ciphertext"] != ciphertext || got["tokenHash"] != nil {
		t.Fatalf("server exposed wrong envelope: %#v", got)
	}
	wrongToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{11}, 32))
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/short-links/"+created.Code+"/redeem", map[string]string{"redemptionToken": wrongToken})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong redemption: %s", resp.Status)
	}
	resp.Body.Close()
	if got := getEnvelope(); got["ciphertext"] != ciphertext {
		t.Fatal("wrong token consumed the link")
	}
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/short-links/"+created.Code+"/redeem", map[string]string{"redemptionToken": token})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("redeem: %s", resp.Status)
	}
	resp.Body.Close()
	if got := getEnvelope(); got["ciphertext"] == ciphertext || len(got["ciphertext"].(string)) != len(ciphertext) {
		t.Fatalf("consumed link response is distinguishable by shape: %#v", got)
	}
	resp, err := client.Get(ts.URL + "/s/" + strings.ToLower(created.Code))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("short-link page: %s", resp.Status)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(resp.Header.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("short-link page privacy headers: cache=%q robots=%q", resp.Header.Get("Cache-Control"), resp.Header.Get("X-Robots-Tag"))
	}
}

func TestFourCharacterShortLinkRequiresOneTimeTenMinutes(t *testing.T) {
	ts := testServer(t)
	request := map[string]any{"ciphertext": base64.RawURLEncoding.EncodeToString(make([]byte, shortLinkCipherBytes)), "iv": base64.RawURLEncoding.EncodeToString(make([]byte, 12)), "salt": base64.RawURLEncoding.EncodeToString(make([]byte, 16)), "tokenHash": strings.Repeat("a", 64), "kdfIterations": shortLinkKDFIterations, "expiresInSeconds": 3600, "maxUses": 0, "codeLength": 4}
	resp := requestJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/short-links", request)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsafe four-character link accepted: %s", resp.Status)
	}
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
	readResult := make(chan error, 1)
	go func() {
		readResult <- wsjson.Read(ctx, conn, &event)
	}()
	pingCtx, cancelPing := context.WithTimeout(ctx, time.Second)
	if err := conn.Ping(pingCtx); err != nil {
		cancelPing()
		t.Fatalf("ping: %v", err)
	}
	cancelPing()
	resp = requestJSONWithToken(t, client, "POST", ts.URL+"/api/rooms/live/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": "hello"}, testWriteToken)
	resp.Body.Close()
	if err := <-readResult; err != nil || event["type"] != "refresh" {
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
	if capabilities["serverVersion"] != "dev" || capabilities["protocolVersion"] != float64(6) || capabilities["writeCapabilities"] != true || capabilities["openWriteRooms"] != true || capabilities["groupedAttachments"] != true || capabilities["entryArchives"] != true || capabilities["inlineFiles"] != true || capabilities["aliases"] != true || capabilities["atomicEntries"] != true || capabilities["entryTTL"] != true || capabilities["roomTTL"] != true || capabilities["pwa"] != true || capabilities["qrScanner"] != true || capabilities["shortLinks"] != true || capabilities["shortLinkKDFIterations"] != float64(shortLinkKDFIterations) {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("capabilities must not be cached: %q", got)
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

func TestAtomicTextEntryPinClearMetricsAndPWA(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp := requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms", map[string]any{"id": "atomic-api", "writeProtected": false, "ttlSeconds": 3600})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create room: %s", resp.Status)
	}
	resp.Body.Close()
	entryID := "123e4567-e89b-12d3-a456-426614174030"
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/atomic-api/entries", map[string]any{"id": entryID, "expectedFiles": 0, "expiresInSeconds": 60, "item": map[string]any{"kind": "text", "content": "exact\ntext", "alias": "Вася"}})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create draft: %s", resp.Status)
	}
	resp.Body.Close()
	getRoom := func() roomResponse {
		response, err := client.Get(ts.URL + "/api/rooms/atomic-api")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var room roomResponse
		if err := json.NewDecoder(response.Body).Decode(&room); err != nil {
			t.Fatal(err)
		}
		return room
	}
	if room := getRoom(); len(room.Items) != 0 || len(room.Entries) != 0 {
		t.Fatalf("draft leaked through room API: %#v", room)
	}
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/atomic-api/entries/"+entryID+"/commit", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("commit: %s", resp.Status)
	}
	resp.Body.Close()
	if room := getRoom(); len(room.Items) != 1 || len(room.Entries) != 1 || room.Room.TTLSeconds != 3600 {
		t.Fatalf("committed room response: %#v", room)
	}
	resp = requestJSON(t, client, http.MethodPut, ts.URL+"/api/rooms/atomic-api/entries/"+entryID+"/pin", map[string]bool{"pinned": true})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("pin: %s", resp.Status)
	}
	resp.Body.Close()
	if room := getRoom(); len(room.Entries) != 1 || !room.Entries[0].Pinned {
		t.Fatalf("pin not returned: %#v", room.Entries)
	}
	resp, err := client.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(metrics, []byte("clipboard_exchange_rooms 1")) || bytes.Contains(metrics, []byte("exact")) {
		t.Fatalf("unsafe or incomplete metrics: %s", metrics)
	}
	for _, asset := range []string{"/assets/manifest.webmanifest?v=2", "/assets/download-sw.js?v=13", "/assets/icon-192.png", "/assets/icon-512.png"} {
		response, err := client.Get(ts.URL + asset)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || len(body) < 8 {
			t.Fatalf("PWA asset %s: status=%s bytes=%d", asset, response.Status, len(body))
		}
		if strings.Contains(asset, "manifest") && (!bytes.Contains(body, []byte(`"id": "/"`)) || !bytes.Contains(body, []byte(`"shortcuts"`)) || !bytes.Contains(body, []byte(`/?scan=1`))) {
			t.Fatalf("incomplete install manifest: %s", body)
		}
		if strings.Contains(asset, ".png") && !bytes.Equal(body[:8], []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("invalid PNG signature for %s", asset)
		}
	}
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/atomic-api/clear", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear: %s", resp.Status)
	}
	resp.Body.Close()
	if room := getRoom(); len(room.Items)+len(room.Files)+len(room.Entries) != 0 {
		t.Fatalf("room was not cleared: %#v", room)
	}
}

func TestDownloadOnceDeletesOnlyAfterFullResponse(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp := requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms", map[string]any{"id": "download-once", "writeProtected": false})
	resp.Body.Close()
	entryID := "123e4567-e89b-12d3-a456-426614174040"
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/download-once/entries", map[string]any{"id": entryID, "expectedFiles": 1, "deleteAfterDownload": true})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create entry: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/download-once/uploads", map[string]any{"entryId": entryID, "entryIndex": 0, "name": "once.txt", "mimeType": "text/plain", "size": 4})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create upload: %s", resp.Status)
	}
	var upload struct {
		ID          string `json:"id"`
		FileID      string `json:"fileId"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = requestUpload(t, client, http.MethodPut, ts.URL+"/api/rooms/download-once/uploads/"+upload.ID+"/chunks/0", strings.NewReader("once"), upload.UploadToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put chunk: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestUpload(t, client, http.MethodPost, ts.URL+"/api/rooms/download-once/uploads/"+upload.ID+"/complete", nil, upload.UploadToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("complete upload: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/download-once/entries/"+entryID+"/commit", nil)
	resp.Body.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/rooms/download-once/files/"+upload.FileID, nil)
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range: %s", resp.Status)
	}
	resp, err = client.Get(ts.URL + "/api/rooms/download-once/files/" + upload.FileID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "once" {
		t.Fatalf("full download: status=%s body=%q", resp.Status, body)
	}
	resp, err = client.Get(ts.URL + "/api/rooms/download-once/files/" + upload.FileID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("download-once file remains: %s", resp.Status)
	}
}

func TestEntryArchiveStreamsStoredFilesWithoutCompression(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp := requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms", map[string]any{"id": "archive", "writeProtected": false})
	resp.Body.Close()
	entryID := "123e4567-e89b-12d3-a456-426614174041"
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/archive/entries", map[string]any{"id": entryID, "expectedFiles": 2})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create entry: %s", resp.Status)
	}
	resp.Body.Close()

	type createdUpload struct {
		ID          string `json:"id"`
		FileID      string `json:"fileId"`
		UploadToken string `json:"uploadToken"`
	}
	fileIDs := make([]string, 0, 2)
	for index, testFile := range []struct{ name, body string }{{"../same.txt", "first"}, {"same.txt", "second"}} {
		resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/archive/uploads", map[string]any{"entryId": entryID, "entryIndex": index, "name": testFile.name, "mimeType": "text/plain", "size": len(testFile.body)})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create upload %d: %s", index, resp.Status)
		}
		var upload createdUpload
		if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		fileIDs = append(fileIDs, upload.FileID)
		resp = requestUpload(t, client, http.MethodPut, ts.URL+"/api/rooms/archive/uploads/"+upload.ID+"/chunks/0", strings.NewReader(testFile.body), upload.UploadToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("put chunk %d: %s", index, resp.Status)
		}
		resp.Body.Close()
		resp = requestUpload(t, client, http.MethodPost, ts.URL+"/api/rooms/archive/uploads/"+upload.ID+"/complete", nil, upload.UploadToken)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("complete upload %d: %s", index, resp.Status)
		}
		resp.Body.Close()
	}
	resp = requestJSON(t, client, http.MethodPost, ts.URL+"/api/rooms/archive/entries/"+entryID+"/commit", nil)
	resp.Body.Close()

	resp, err := client.Get(ts.URL + "/api/rooms/archive/entries/" + entryID + "/archive")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("archive: status=%s err=%v", resp.Status, err)
	}
	if resp.Header.Get("Content-Type") != "application/zip" || !strings.Contains(resp.Header.Get("Content-Disposition"), "files-123e4567.zip") {
		t.Fatalf("archive headers: type=%q disposition=%q", resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 2 {
		t.Fatalf("archive members: %d", len(reader.File))
	}
	for index, expected := range []struct{ name, body string }{{"same.txt", "first"}, {"same (2).txt", "second"}} {
		member := reader.File[index]
		if member.Name != expected.name || member.Method != zip.Store {
			t.Fatalf("member %d: name=%q method=%d", index, member.Name, member.Method)
		}
		opened, openErr := member.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		contents, readErr := io.ReadAll(opened)
		opened.Close()
		if readErr != nil || string(contents) != expected.body {
			t.Fatalf("member %d: body=%q err=%v", index, contents, readErr)
		}
	}
	resp, err = client.Get(ts.URL + "/api/rooms/archive/files/" + fileIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archive unexpectedly consumed entry: %s", resp.Status)
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
	ts := testServerWithVersion(t, "v-test")
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
	if !bytes.Contains(body, []byte(`/assets/app.js?v=19`)) {
		t.Fatal("page does not cache-bust app.js")
	}
	if !bytes.Contains(body, []byte(`/assets/style.css?v=15`)) {
		t.Fatal("page does not cache-bust style.css")
	}
	if !bytes.Contains(body, []byte(`name="clipboard-exchange-version" content="v-test"`)) || bytes.Contains(body, []byte("__CLIPBOARD_EXCHANGE_VERSION__")) {
		t.Fatal("page does not expose the embedded shell version")
	}

	resp, err = ts.Client().Get(ts.URL + "/assets/app.js?v=19")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache, max-age=0, must-revalidate" {
		t.Fatalf("unexpected asset cache policy: %q", got)
	}
}
