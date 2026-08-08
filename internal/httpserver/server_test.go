package httpserver

import (
	"bytes"
	"context"
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
	"github.com/vponomarev/clipboard-exchange/internal/store"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.Default()
	cfg.RateLimit = 0
	ts := httptest.NewServer(New(cfg, db, log.New(io.Discard, "", 0)))
	t.Cleanup(ts.Close)
	return ts
}

func requestJSON(t *testing.T, client *http.Client, method, url string, body any) *http.Response {
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
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRoomCRUDPreservesTextAndSecurityHeaders(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "my-ex", "encrypted": false, "keyId": ""})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %s", resp.Status)
	}
	resp.Body.Close()
	text := "  echo $HOME\n\tline two\n"
	resp = requestJSON(t, client, "POST", ts.URL+"/api/rooms/my-ex/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": text})
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
	if len(got.Items) != 1 || got.Items[0].Content != text {
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
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "live", "encrypted": false, "keyId": ""})
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
	resp = requestJSON(t, client, "POST", ts.URL+"/api/rooms/live/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": "hello"})
	resp.Body.Close()
	if err := wsjson.Read(ctx, conn, &event); err != nil || event["type"] != "refresh" {
		t.Fatalf("refresh: %#v %v", event, err)
	}
}

func TestRejectsPlaintextInEncryptedRoom(t *testing.T) {
	ts := testServer(t)
	client := ts.Client()
	keyID := strings.Repeat("a", 43)
	resp := requestJSON(t, client, "POST", ts.URL+"/api/rooms", map[string]any{"id": "secure", "encrypted": true, "keyId": keyID})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSON(t, client, "POST", ts.URL+"/api/rooms/secure/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174000", "kind": "text", "content": "leak"})
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
	if !bytes.Contains(body, []byte(`/assets/app.js?v=3`)) {
		t.Fatal("page does not cache-bust app.js")
	}

	resp, err = ts.Client().Get(ts.URL + "/assets/app.js?v=3")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache, max-age=0, must-revalidate" {
		t.Fatalf("unexpected asset cache policy: %q", got)
	}
}
