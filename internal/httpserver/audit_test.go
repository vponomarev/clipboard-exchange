package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vponomarev/clipboard-exchange/internal/config"
	"github.com/vponomarev/clipboard-exchange/internal/filestore"
	"github.com/vponomarev/clipboard-exchange/internal/store"
)

type auditFixture struct {
	db      *store.Store
	files   *filestore.Store
	handler http.Handler
	ts      *httptest.Server
}

func newAuditFixture(t *testing.T) auditFixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	files, err := filestore.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.RateLimit = 0
	h := New(cfg, db, files, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return auditFixture{db, files, h, ts}
}

type auditUpload struct {
	ID          string `json:"id"`
	FileID      string `json:"fileId"`
	UploadToken string `json:"uploadToken"`
}

func prepareAuditUpload(t *testing.T, f auditFixture) (auditUpload, string) {
	t.Helper()
	base := f.ts.URL + "/api/rooms/audit"
	resp := requestJSON(t, f.ts.Client(), "POST", f.ts.URL+"/api/rooms", map[string]any{"id": "audit", "writeProtected": false})
	resp.Body.Close()
	entry := "123e4567-e89b-12d3-a456-426614174040"
	resp = requestJSON(t, f.ts.Client(), "POST", base+"/entries", map[string]any{"id": entry, "expectedFiles": 1, "deleteAfterDownload": true})
	if resp.StatusCode != 201 {
		t.Fatalf("entry: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSON(t, f.ts.Client(), "POST", base+"/uploads", map[string]any{"entryId": entry, "entryIndex": 0, "name": "once.txt", "mimeType": "text/plain", "size": 4})
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %s", resp.Status)
	}
	var upload auditUpload
	if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp = requestUpload(t, f.ts.Client(), "PUT", base+"/uploads/"+upload.ID+"/chunks/0", strings.NewReader("once"), upload.UploadToken)
	if resp.StatusCode != 200 {
		t.Fatalf("chunk: %s", resp.Status)
	}
	resp.Body.Close()
	return upload, entry
}
func publishAuditUpload(t *testing.T, f auditFixture, upload auditUpload, entry string) string {
	t.Helper()
	base := f.ts.URL + "/api/rooms/audit"
	resp := requestUpload(t, f.ts.Client(), "POST", base+"/uploads/"+upload.ID+"/complete", nil, upload.UploadToken)
	if resp.StatusCode != 201 {
		t.Fatalf("complete: %s", resp.Status)
	}
	resp.Body.Close()
	resp = requestJSON(t, f.ts.Client(), "POST", base+"/entries/"+entry+"/commit", nil)
	if resp.StatusCode != 204 {
		t.Fatalf("commit: %s", resp.Status)
	}
	resp.Body.Close()
	return base + "/files/" + upload.FileID
}

func TestDownloadOncePreservesFileForNonDownloads(t *testing.T) {
	f := newAuditFixture(t)
	upload, entry := prepareAuditUpload(t, f)
	url := publishAuditUpload(t, f, upload, entry)
	for _, tc := range []struct {
		method, suffix, header, value string
		status                        int
	}{
		{"HEAD", "", "", "", 200}, {"GET", "", "If-None-Match", `"` + upload.FileID + `"`, 304},
		{"GET", "", "If-Match", `"wrong"`, 412}, {"GET", "", "Range", "bytes=0-0", 206},
		{"GET", "", "Range", "bytes=99-100", 416}, {"GET", "?inline=1", "", "", 200},
	} {
		req, _ := http.NewRequest(tc.method, url+tc.suffix, nil)
		if tc.header != "" {
			req.Header.Set(tc.header, tc.value)
		}
		resp, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s %s: %s", tc.method, tc.header, resp.Status)
		}
		if _, err := f.db.GetFile(context.Background(), "audit", upload.FileID); err != nil {
			t.Fatalf("%s consumed file: %v", tc.method, err)
		}
	}
	req, _ := http.NewRequest("HEAD", f.ts.URL+"/api/rooms/audit/entries/"+entry+"/archive", nil)
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, err := f.db.GetFile(context.Background(), "audit", upload.FileID); err != nil {
		t.Fatal("archive HEAD consumed file")
	}
}

type brokenDownloadWriter struct {
	header    http.Header
	failFlush bool
}

func (w *brokenDownloadWriter) Header() http.Header { return w.header }
func (w *brokenDownloadWriter) WriteHeader(int)     {}
func (w *brokenDownloadWriter) Write(p []byte) (int, error) {
	if w.failFlush {
		return len(p), nil
	}
	return 0, io.ErrClosedPipe
}
func (w *brokenDownloadWriter) FlushError() error { return io.ErrClosedPipe }

func TestFailedDownloadAndArchivePreserveObject(t *testing.T) {
	for _, archive := range []bool{false, true} {
		for _, failFlush := range []bool{false, true} {
			t.Run(fmt.Sprintf("archive=%t/flush=%t", archive, failFlush), func(t *testing.T) {
				f := newAuditFixture(t)
				upload, entry := prepareAuditUpload(t, f)
				url := publishAuditUpload(t, f, upload, entry)
				if archive {
					url = f.ts.URL + "/api/rooms/audit/entries/" + entry + "/archive"
				}
				f.handler.ServeHTTP(&brokenDownloadWriter{make(http.Header), failFlush}, httptest.NewRequest("GET", url, nil))
				object, err := f.files.OpenObject(upload.FileID)
				if err != nil {
					t.Fatal(err)
				}
				object.Close()
				if _, err := f.db.GetFile(context.Background(), "audit", upload.FileID); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestConcurrentCompletionIsIdempotentAndAuthenticated(t *testing.T) {
	f := newAuditFixture(t)
	upload, entry := prepareAuditUpload(t, f)
	url := f.ts.URL + "/api/rooms/audit/uploads/" + upload.ID + "/complete"
	start := make(chan struct{})
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			<-start
			req, _ := http.NewRequest("POST", url, nil)
			req.Header.Set("Authorization", "ClipboardUpload "+upload.UploadToken)
			resp, err := f.ts.Client().Do(req)
			if err == nil {
				var file store.File
				err = json.NewDecoder(resp.Body).Decode(&file)
				resp.Body.Close()
				if resp.StatusCode != 201 || file.ID != upload.FileID {
					err = fmt.Errorf("completion: %s, file %s", resp.Status, file.ID)
				}
			}
			results <- err
		}()
	}
	close(start)
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	resp := requestUpload(t, f.ts.Client(), "POST", url, nil, "cu1_"+strings.Repeat("b", 43))
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("wrong token: %s", resp.Status)
	}
	// A new handler has no in-memory completion state.
	h := New(config.Default(), f.db, f.files, log.New(io.Discard, "", 0))
	req := httptest.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "ClipboardUpload "+upload.UploadToken)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 201 {
		t.Fatalf("restarted completion: %d", rr.Code)
	}
	download := publishAuditUpload(t, f, upload, entry)
	resp, err := f.ts.Client().Get(download)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(body) != "once" {
		t.Fatalf("published object lost: %q %v", body, err)
	}
}

func TestProxyClientAddressIgnoresSpoofedPrefix(t *testing.T) {
	cfg := config.Default()
	cfg.TrustProxy = true
	s := &Server{cfg: cfg}
	for _, prefix := range []string{"1.2.3.4", "evil", "198.51.100.1, 192.0.2.2"} {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = "127.0.0.1:80"
		r.Header.Set("Forwarded", "for="+prefix)
		r.Header.Set("X-Forwarded-For", prefix+", 203.0.113.7")
		if got := s.clientIP(r); got != "203.0.113.7" {
			t.Fatalf("spoof changed client IP: %s", got)
		}
	}
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "127.0.0.1:80"
	r.Header.Set("X-Forwarded-For", "garbage")
	if got := s.clientIP(r); got != "127.0.0.1" {
		t.Fatal(got)
	}
}

func TestReadinessDetectsStorageFailureAndRecovers(t *testing.T) {
	f := newAuditFixture(t)
	check := func(want int) {
		t.Helper()
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
		if rr.Code != want {
			t.Fatalf("ready: %d want %d", rr.Code, want)
		}
	}
	check(204)
	objects := filepath.Join(f.files.Root(), "objects")
	if err := os.Remove(objects); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	check(503)
	if err := os.Mkdir(objects, 0700); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	check(204)
	_ = f.db.Close()
	time.Sleep(1100 * time.Millisecond)
	check(503)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != 204 {
		t.Fatal("liveness depends on DB")
	}
}

func TestBoundedHistoryKeepsLargeRoomUsable(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	if err := f.db.CreateRoom(ctx, store.Room{ID: "large"}, 10); err != nil {
		t.Fatal(err)
	}
	// Over 16 MiB of valid text; the native client's old full-snapshot limit.
	for i := 0; i < 270; i++ {
		item := store.Item{ID: fmt.Sprintf("123e4567-e89b-12d3-a456-%012d", i), Kind: "text", Content: strings.Repeat("a", 65536)}
		if err := f.db.AddItem(ctx, "large", item, 500); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, 30} {
		resp, err := f.ts.Client().Get(fmt.Sprintf("%s/api/rooms/large/history?limit=%d", f.ts.URL, limit))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var snapshot roomResponse
		if err := json.Unmarshal(body, &snapshot); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || len(snapshot.Items) != limit || len(body) > 8<<20 {
			t.Fatalf("history: %s items=%d bytes=%d", resp.Status, len(snapshot.Items), len(body))
		}
	}
	resp := requestJSON(t, f.ts.Client(), "POST", f.ts.URL+"/api/rooms/large/items", map[string]any{"id": "123e4567-e89b-12d3-a456-426614174999", "kind": "text", "content": "still works"})
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("send to large room: %s", resp.Status)
	}
}

func TestTransferSurvivesExpiredAbsoluteDeadline(t *testing.T) {
	for _, upload := range []bool{false, true} {
		t.Run(fmt.Sprint(upload), func(t *testing.T) {
			ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if upload {
					n, err := io.Copy(io.Discard, transferReader{r.Body, http.NewResponseController(w)})
					if err != nil || n != 3 {
						http.Error(w, "read failed", 500)
						return
					}
				} else {
					time.Sleep(100 * time.Millisecond)
				}
				out := newTransferWriter(w)
				_, _ = out.Write([]byte("ok"))
			}))
			ts.Config.ReadTimeout = 40 * time.Millisecond
			ts.Config.WriteTimeout = 40 * time.Millisecond
			ts.Start()
			defer ts.Close()
			var body io.Reader
			if upload {
				rd, wr := io.Pipe()
				body = rd
				go func() {
					defer wr.Close()
					for i := 0; i < 3; i++ {
						if _, err := wr.Write([]byte("x")); err != nil {
							return
						}
						time.Sleep(60 * time.Millisecond)
					}
				}()
			}
			resp, err := ts.Client().Post(ts.URL, "application/octet-stream", body)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(data) != "ok" {
				t.Fatalf("transfer: %q %v", data, err)
			}
		})
	}
}
