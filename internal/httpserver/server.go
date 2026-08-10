package httpserver

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/vponomarev/clipboard-exchange/internal/config"
	"github.com/vponomarev/clipboard-exchange/internal/filestore"
	"github.com/vponomarev/clipboard-exchange/internal/store"
)

//go:embed web/*
var webFiles embed.FS

var (
	roomIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	objectIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
)

type Server struct {
	cfg     config.Config
	store   *store.Store
	files   *filestore.Store
	log     *log.Logger
	hub     *hub
	limiter *rateLimiter
	static  http.Handler
	started time.Time
}

type roomResponse struct {
	Room    store.Room    `json:"room"`
	Entries []store.Entry `json:"entries"`
	Items   []store.Item  `json:"items"`
	Files   []store.File  `json:"files"`
}

func New(cfg config.Config, db *store.Store, files *filestore.Store, logger *log.Logger) http.Handler {
	assets, _ := fs.Sub(webFiles, "web")
	s := &Server{cfg: cfg, store: db, files: files, log: logger, hub: newHub(), limiter: newRateLimiter(cfg.RateLimit), static: http.FileServer(http.FS(assets)), started: time.Now()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/capabilities", s.capabilities)
	mux.HandleFunc("POST /api/rooms", s.createRoom)
	mux.HandleFunc("GET /api/rooms/{room}", s.getRoom)
	mux.HandleFunc("POST /api/rooms/{room}/entries", s.createEntry)
	mux.HandleFunc("POST /api/rooms/{room}/entries/{entry}/commit", s.commitEntry)
	mux.HandleFunc("PUT /api/rooms/{room}/entries/{entry}/pin", s.pinEntry)
	mux.HandleFunc("POST /api/rooms/{room}/clear", s.clearRoom)
	mux.HandleFunc("POST /api/rooms/{room}/write-capability/rotate", s.rotateWriteCapability)
	mux.HandleFunc("POST /api/rooms/{room}/uploads", s.createUpload)
	mux.HandleFunc("GET /api/rooms/{room}/uploads/{upload}", s.getUpload)
	mux.HandleFunc("PUT /api/rooms/{room}/uploads/{upload}/chunks/{index}", s.putChunk)
	mux.HandleFunc("POST /api/rooms/{room}/uploads/{upload}/complete", s.completeUpload)
	mux.HandleFunc("DELETE /api/rooms/{room}/uploads/{upload}", s.abortUpload)
	mux.HandleFunc("GET /api/rooms/{room}/files/{file}", s.downloadFile)
	mux.HandleFunc("POST /api/rooms/{room}/files/{file}/consume", s.consumeFile)
	mux.HandleFunc("DELETE /api/rooms/{room}/files/{file}", s.deleteFile)
	mux.HandleFunc("DELETE /api/rooms/{room}/entries/{entry}", s.deleteEntry)
	mux.HandleFunc("POST /api/rooms/{room}/items", s.addItem)
	mux.HandleFunc("DELETE /api/rooms/{room}/items/{item}", s.deleteItem)
	mux.HandleFunc("GET /api/rooms/{room}/events", s.events)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /assets/icon-192.png", func(w http.ResponseWriter, _ *http.Request) { serveIcon(w, 192) })
	mux.HandleFunc("GET /assets/icon-512.png", func(w http.ResponseWriter, _ *http.Request) { serveIcon(w, 512) })
	mux.HandleFunc("GET /assets/", s.asset)
	mux.HandleFunc("GET /r/{room}", s.page)
	mux.HandleFunc("GET /share-target", s.page)
	mux.HandleFunc("POST /share-target", s.shareTargetFallback)
	mux.HandleFunc("GET /", s.page)
	return s.security(s.logging(mux))
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/share-target" && !roomIDPattern.MatchString(r.PathValue("room")) {
		http.NotFound(w, r)
		return
	}
	b, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/download-sw.js") {
		w.Header().Set("Service-Worker-Allowed", "/")
	}
	r.URL.Path = strings.TrimPrefix(path.Clean(r.URL.Path), "/assets")
	// Asset URLs are stable across binary upgrades. Force revalidation so a
	// browser cannot keep JavaScript from an older server version for a day.
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	s.static.ServeHTTP(w, r)
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(w, r) {
		return
	}
	var req struct {
		ID             string `json:"id"`
		Encrypted      bool   `json:"encrypted"`
		KeyID          string `json:"keyId"`
		WriteProtected *bool  `json:"writeProtected"`
		WriteToken     string `json:"writeToken"`
		TTLSeconds     int64  `json:"ttlSeconds"`
	}
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	// A missing writeProtected field preserves compatibility with v2 clients,
	// which always supplied a write token.
	writeProtected := req.WriteToken != ""
	if req.WriteProtected != nil {
		writeProtected = *req.WriteProtected
	}
	writeHash, validWriteToken := "", req.WriteToken == ""
	if writeProtected {
		writeHash, validWriteToken = hashWriteToken(req.WriteToken)
	}
	if !roomIDPattern.MatchString(req.ID) || !validWriteToken || req.TTLSeconds < 0 || req.TTLSeconds > 365*24*60*60 || (req.Encrypted && !validToken(req.KeyID, 43, 64)) || (!req.Encrypted && req.KeyID != "") {
		writeError(w, http.StatusBadRequest, "invalid_room", "Room ID or encryption metadata is invalid")
		return
	}
	err := s.store.CreateRoom(r.Context(), store.Room{ID: req.ID, Encrypted: req.Encrypted, KeyID: req.KeyID, WriteProtected: writeProtected, WriteHash: writeHash, TTLSeconds: req.TTLSeconds}, s.cfg.MaxRooms)
	if err != nil {
		s.storeError(w, err)
		return
	}
	w.Header().Set("Location", "/r/"+req.ID)
	writeJSON(w, http.StatusCreated, map[string]string{"id": req.ID})
}

func (s *Server) capabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"protocolVersion":        5,
		"writeCapabilities":      true,
		"openWriteRooms":         true,
		"groupedAttachments":     true,
		"inlineFiles":            true,
		"atomicEntries":          true,
		"entryTTL":               true,
		"roomTTL":                true,
		"pwa":                    true,
		"qrScanner":              true,
		"aliases":                true,
		"encryptedTextVersions":  []int{1, 2},
		"files":                  true,
		"encryptedFiles":         true,
		"fileEncryptionVersions": []int{1},
		"limits": map[string]any{
			"maxItemBytes":     s.cfg.MaxItemBytes,
			"maxItemsPerRoom":  s.cfg.MaxItemsPerRoom,
			"maxRooms":         s.cfg.MaxRooms,
			"maxAliasRunes":    64,
			"maxFileBytes":     s.cfg.MaxFileBytes,
			"maxRoomFileBytes": s.cfg.MaxRoomFileBytes,
			"fileChunkBytes":   s.cfg.FileChunkBytes,
		},
	})
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) {
		writeError(w, 400, "invalid_room", "Invalid room ID")
		return
	}
	room, err := s.store.GetRoom(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	items, err := s.store.ListItems(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	files, err := s.store.ListFiles(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	entries, err := s.store.ListEntries(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, roomResponse{Room: room, Entries: entries, Items: items, Files: files})
}

func (s *Server) shareTargetFallback(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	_, _ = io.Copy(io.Discard, r.Body)
	http.Redirect(w, r, "/?share=unsupported", http.StatusSeeOther)
}

func (s *Server) createEntry(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) || !s.authorizeMutation(w, r, roomID) {
		return
	}
	var req struct {
		ID                  string      `json:"id"`
		ExpectedFiles       int         `json:"expectedFiles"`
		ExpiresInSeconds    int64       `json:"expiresInSeconds"`
		DeleteAfterDownload bool        `json:"deleteAfterDownload"`
		Item                *store.Item `json:"item"`
	}
	if !decodeJSON(w, r, &req, s.cfg.MaxItemBytes*2+8192) {
		return
	}
	if !objectIDPattern.MatchString(req.ID) || req.ExpectedFiles < 0 || req.ExpectedFiles > 100 || req.ExpiresInSeconds < 0 || req.ExpiresInSeconds > 30*24*60*60 || (req.Item == nil && req.ExpectedFiles == 0) || (req.DeleteAfterDownload && (req.ExpectedFiles != 1 || req.Item != nil)) {
		writeError(w, http.StatusBadRequest, "invalid_entry", "Invalid entry metadata")
		return
	}
	if req.Item != nil {
		req.Item.ID = req.ID
		req.Item.CreatedAt = ""
		if !validItem(*req.Item, s.cfg.MaxItemBytes) {
			writeError(w, http.StatusBadRequest, "invalid_item", "Invalid entry text")
			return
		}
	}
	expiresAt := ""
	if req.ExpiresInSeconds > 0 {
		expiresAt = time.Now().Add(time.Duration(req.ExpiresInSeconds) * time.Second).UTC().Format(time.RFC3339Nano)
	}
	entry := store.Entry{ID: req.ID, ExpectedFiles: req.ExpectedFiles, ExpiresAt: expiresAt, DeleteAfterDownload: req.DeleteAfterDownload}
	if err := s.store.CreateEntry(r.Context(), roomID, entry, req.Item, s.cfg.MaxItemsPerRoom); err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": req.ID})
}

func (s *Server) commitEntry(w http.ResponseWriter, r *http.Request) {
	roomID, entryID := r.PathValue("room"), r.PathValue("entry")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(entryID) || !s.authorizeMutation(w, r, roomID) {
		return
	}
	if err := s.store.CommitEntry(r.Context(), roomID, entryID); err != nil {
		s.storeError(w, err)
		return
	}
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pinEntry(w http.ResponseWriter, r *http.Request) {
	roomID, entryID := r.PathValue("room"), r.PathValue("entry")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(entryID) || !s.authorizeMutation(w, r, roomID) {
		return
	}
	var req struct {
		Pinned bool `json:"pinned"`
	}
	if !decodeJSON(w, r, &req, 128) {
		return
	}
	if err := s.store.PinEntry(r.Context(), roomID, entryID, req.Pinned); err != nil {
		s.storeError(w, err)
		return
	}
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) || !s.authorizeMutation(w, r, roomID) {
		return
	}
	deleted, err := s.store.ClearRoom(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.removeDeletedObjects(deleted)
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addItem(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) {
		writeError(w, 400, "invalid_room", "Invalid room ID")
		return
	}
	if !s.authorizeMutation(w, r, roomID) {
		return
	}
	var item store.Item
	if !decodeJSON(w, r, &item, s.cfg.MaxItemBytes*2+4096) {
		return
	}
	if !objectIDPattern.MatchString(item.ID) {
		writeError(w, 400, "invalid_item", "Invalid item ID")
		return
	}
	if !validItem(item, s.cfg.MaxItemBytes) {
		writeError(w, 400, "invalid_item", "Unsupported item kind")
		return
	}
	item.CreatedAt = ""
	if err := s.store.AddItem(r.Context(), roomID, item, s.cfg.MaxItemsPerRoom); err != nil {
		s.storeError(w, err)
		return
	}
	s.hub.broadcast(roomID)
	writeJSON(w, http.StatusCreated, map[string]string{"id": item.ID})
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	roomID, itemID := r.PathValue("room"), r.PathValue("item")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(itemID) {
		writeError(w, 400, "invalid_item", "Invalid room or item ID")
		return
	}
	if !s.authorizeMutation(w, r, roomID) {
		return
	}
	if err := s.store.DeleteItem(r.Context(), roomID, itemID); err != nil {
		s.storeError(w, err)
		return
	}
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateWriteCapability(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) {
		writeError(w, http.StatusBadRequest, "invalid_room", "Invalid room ID")
		return
	}
	room, err := s.store.GetRoom(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if !room.WriteProtected {
		writeError(w, http.StatusConflict, "write_capability_disabled", "This room does not use a write capability")
		return
	}
	currentHash, ok := s.authorizeMutationHash(w, r, roomID)
	if !ok {
		return
	}
	var req struct {
		WriteToken string `json:"writeToken"`
	}
	if !decodeJSON(w, r, &req, 1024) {
		return
	}
	nextHash, valid := hashWriteToken(req.WriteToken)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_write_capability", "Invalid write capability")
		return
	}
	if err := s.store.RotateWriteHash(r.Context(), roomID, currentHash, nextHash); err != nil {
		s.storeError(w, err)
		return
	}
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) {
		writeError(w, http.StatusBadRequest, "invalid_room", "Invalid room ID")
		return
	}
	if !s.authorizeMutation(w, r, roomID) {
		return
	}
	room, err := s.store.GetRoom(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	var req struct {
		Name       string `json:"name"`
		MIMEType   string `json:"mimeType"`
		Alias      string `json:"alias"`
		EntryID    string `json:"entryId"`
		EntryIndex int    `json:"entryIndex"`
		Size       int64  `json:"size"`
		Encrypted  bool   `json:"encrypted"`
		KeyID      string `json:"keyId"`
	}
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	chunkSize := s.cfg.FileChunkBytes
	chunkCount := 0
	validMetadata := !room.Encrypted && !req.Encrypted && validFileName(req.Name) && validMIMEType(req.MIMEType) && validAlias(req.Alias) && req.KeyID == ""
	if room.Encrypted {
		chunkSize += 28
		validMetadata = req.Encrypted && req.KeyID == room.KeyID && req.Name == "" && req.MIMEType == "" && req.Alias == "" && req.Size > 0 && req.Size%chunkSize == 0
		chunkCount = int(req.Size / chunkSize)
	}
	if !validMetadata || req.EntryIndex < 0 || req.EntryIndex > 999 || req.Size < 0 || req.Size > s.cfg.MaxFileBytes {
		writeError(w, http.StatusBadRequest, "invalid_file", "Invalid file metadata or size")
		return
	}
	uploadID, err := randomID()
	if err != nil {
		s.storeError(w, err)
		return
	}
	fileID, err := randomID()
	if err != nil {
		s.storeError(w, err)
		return
	}
	entryID := req.EntryID
	if entryID == "" {
		// Compatibility with clients that predate grouped attachments.
		entryID = fileID
	} else if !objectIDPattern.MatchString(entryID) {
		writeError(w, http.StatusBadRequest, "invalid_entry", "Invalid entry ID")
		return
	}
	token, err := newOpaqueToken("cu1_")
	if err != nil {
		s.storeError(w, err)
		return
	}
	tokenHash, _ := hashCapability(token, "cu1_")
	if !room.Encrypted {
		chunkCount = int((req.Size + chunkSize - 1) / chunkSize)
		if chunkCount == 0 {
			chunkCount = 1
		}
	}
	upload := store.Upload{ID: uploadID, RoomID: roomID, FileID: fileID, EntryID: entryID, EntryIndex: req.EntryIndex, Name: req.Name, MIMEType: req.MIMEType, Alias: req.Alias, Size: req.Size, ChunkSize: chunkSize, PlainChunkSize: s.cfg.FileChunkBytes, ChunkCount: chunkCount, Encrypted: req.Encrypted, KeyID: req.KeyID, TokenHash: tokenHash, ExpiresAt: time.Now().Add(s.cfg.UploadTTL).UTC().Format(time.RFC3339Nano)}
	if err := s.store.CreateUpload(r.Context(), upload, s.cfg.MaxRoomFileBytes, s.cfg.MaxActiveUploads); errors.Is(err, store.ErrLimit) {
		writeError(w, http.StatusConflict, "quota_exceeded", "File quota or active upload limit reached")
		return
	} else if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": upload.ID, "fileId": upload.FileID, "entryId": upload.EntryID, "entryIndex": upload.EntryIndex, "uploadToken": token, "chunkSize": upload.ChunkSize, "plainChunkSize": upload.PlainChunkSize, "chunkCount": upload.ChunkCount, "encrypted": upload.Encrypted, "keyId": upload.KeyID, "expiresAt": upload.ExpiresAt, "received": []int{}, "digests": map[int]string{}})
}

func (s *Server) getUpload(w http.ResponseWriter, r *http.Request) {
	upload, ok := s.authorizeUpload(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, upload)
}

func (s *Server) putChunk(w http.ResponseWriter, r *http.Request) {
	upload, ok := s.authorizeUpload(w, r)
	if !ok {
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= upload.ChunkCount {
		writeError(w, http.StatusBadRequest, "invalid_chunk", "Invalid chunk index")
		return
	}
	expected := upload.ChunkSize
	if index == upload.ChunkCount-1 {
		expected = upload.Size - int64(index)*upload.ChunkSize
	}
	if r.ContentLength >= 0 && r.ContentLength != expected {
		writeError(w, http.StatusBadRequest, "invalid_chunk", "Chunk size does not match upload metadata")
		return
	}
	nonce := ""
	var rawNonce []byte
	if upload.Encrypted {
		nonce = r.Header.Get("X-Clipboard-Chunk-IV")
		rawNonce, err = base64.RawURLEncoding.DecodeString(nonce)
		if err != nil || len(rawNonce) != 12 {
			writeError(w, http.StatusBadRequest, "invalid_chunk", "Encrypted chunk nonce is invalid")
			return
		}
	}
	size, digest, prefix, err := s.files.WriteChunk(upload.ID, index, r.Body, expected)
	if errors.Is(err, filestore.ErrConflict) {
		writeError(w, http.StatusConflict, "chunk_conflict", "A different chunk already exists at this index")
		return
	}
	if err != nil {
		s.log.Printf("write upload chunk: %v", err)
		writeError(w, http.StatusBadRequest, "invalid_chunk", "Could not store chunk")
		return
	}
	if upload.Encrypted {
		if len(prefix) != 12 || subtle.ConstantTimeCompare(rawNonce, prefix) != 1 {
			_ = s.files.RemoveChunk(upload.ID, index)
			writeError(w, http.StatusBadRequest, "invalid_chunk", "Encrypted chunk nonce is invalid")
			return
		}
	}
	if err := s.store.RecordChunk(r.Context(), upload.ID, index, size, digest, nonce); errors.Is(err, store.ErrConflict) {
		if upload.Encrypted {
			_ = s.files.RemoveChunk(upload.ID, index)
		}
		writeError(w, http.StatusConflict, "chunk_conflict", "A different chunk already exists at this index")
		return
	} else if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"index": index, "size": size, "sha256": digest})
}

func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request) {
	upload, ok := s.authorizeUpload(w, r)
	if !ok {
		return
	}
	var manifest struct {
		Ciphertext string `json:"ciphertext"`
		IV         string `json:"iv"`
		KeyID      string `json:"keyId"`
		Version    int    `json:"version"`
	}
	if upload.Encrypted {
		if !decodeJSON(w, r, &manifest, s.cfg.MaxItemBytes*2+4096) {
			return
		}
		if manifest.Version != 1 || manifest.KeyID != upload.KeyID || !validToken(manifest.IV, 16, 24) || !validToken(manifest.Ciphertext, 20, int(s.cfg.MaxItemBytes*2)) {
			writeError(w, http.StatusBadRequest, "invalid_manifest", "Encrypted file manifest is invalid")
			return
		}
	} else if r.ContentLength > 0 && !decodeJSON(w, r, &struct{}{}, 128) {
		return
	}
	if len(upload.Received) != upload.ChunkCount {
		writeError(w, http.StatusConflict, "incomplete_upload", "Not all chunks have been uploaded")
		return
	}
	if _, err := s.files.Assemble(upload.ID, upload.FileID, upload.ChunkCount, upload.Size); err != nil {
		s.log.Printf("assemble upload: %v", err)
		writeError(w, http.StatusInternalServerError, "storage_unavailable", "Could not finalize file")
		return
	}
	file, err := s.store.CompleteUpload(r.Context(), upload.RoomID, upload.ID, manifest.Ciphertext, manifest.IV, manifest.Version)
	if err != nil {
		_ = s.files.RemoveObject(upload.FileID)
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "incomplete_upload", "Upload metadata is incomplete")
			return
		}
		s.storeError(w, err)
		return
	}
	_ = s.files.RemoveUpload(upload.ID)
	entry, entryErr := s.store.GetEntry(r.Context(), upload.RoomID, upload.EntryID)
	if errors.Is(entryErr, store.ErrNotFound) || (entryErr == nil && entry.Published) {
		s.hub.broadcast(upload.RoomID)
	}
	writeJSON(w, http.StatusCreated, file)
}

func (s *Server) abortUpload(w http.ResponseWriter, r *http.Request) {
	upload, ok := s.authorizeUpload(w, r)
	if !ok {
		return
	}
	if err := s.store.AbortUpload(r.Context(), upload.RoomID, upload.ID); err != nil {
		s.storeError(w, err)
		return
	}
	_ = s.files.RemoveUpload(upload.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	roomID, fileID := r.PathValue("room"), r.PathValue("file")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(fileID) {
		writeError(w, http.StatusBadRequest, "invalid_file", "Invalid room or file ID")
		return
	}
	file, err := s.store.GetFile(r.Context(), roomID, fileID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	object, err := s.files.OpenObject(file.ID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "file_missing", "Stored file is missing")
			return
		}
		s.log.Printf("open stored file: %v", err)
		writeError(w, http.StatusInternalServerError, "storage_unavailable", "Could not open stored file")
		return
	}
	created, _ := time.Parse(time.RFC3339Nano, file.CreatedAt)
	contentType, filename := file.MIMEType, file.Name
	if file.Encrypted {
		contentType, filename = "application/octet-stream", file.ID+".cex"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" && !file.Encrypted && inlineMIMEType(file.MIMEType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	w.Header().Set("ETag", `"`+file.ID+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, file.Name, created, object)
	if err := object.Close(); err != nil {
		s.log.Printf("close stored file %s: %v", fileID, err)
	}
	if r.Header.Get("Range") == "" {
		entry, err := s.store.EntryForFile(context.Background(), roomID, fileID)
		if err == nil && entry.DeleteAfterDownload {
			deleted, deleteErr := s.store.DeleteEntry(context.Background(), roomID, entry.ID)
			if deleteErr == nil {
				s.removeDeletedObjects(deleted)
				s.hub.broadcast(roomID)
			}
		}
	}
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	roomID, fileID := r.PathValue("room"), r.PathValue("file")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(fileID) {
		writeError(w, http.StatusBadRequest, "invalid_file", "Invalid room or file ID")
		return
	}
	if !s.authorizeMutation(w, r, roomID) {
		return
	}
	if err := s.store.DeleteFile(r.Context(), roomID, fileID); err != nil {
		s.storeError(w, err)
		return
	}
	if err := s.files.RemoveObject(fileID); err != nil {
		s.log.Printf("remove stored file %s: %v", fileID, err)
	}
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) consumeFile(w http.ResponseWriter, r *http.Request) {
	roomID, fileID := r.PathValue("room"), r.PathValue("file")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(fileID) {
		writeError(w, http.StatusBadRequest, "invalid_file", "Invalid room or file ID")
		return
	}
	entry, err := s.store.EntryForFile(r.Context(), roomID, fileID)
	if err != nil || !entry.DeleteAfterDownload {
		writeError(w, http.StatusConflict, "not_download_once", "File is not configured for deletion after download")
		return
	}
	deleted, err := s.store.DeleteEntry(r.Context(), roomID, entry.ID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.removeDeletedObjects(deleted)
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	roomID, entryID := r.PathValue("room"), r.PathValue("entry")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(entryID) {
		writeError(w, http.StatusBadRequest, "invalid_entry", "Invalid room or entry ID")
		return
	}
	if !s.authorizeMutation(w, r, roomID) {
		return
	}
	deleted, err := s.store.DeleteEntry(r.Context(), roomID, entryID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.removeDeletedObjects(deleted)
	s.hub.broadcast(roomID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) {
		writeError(w, 400, "invalid_room", "Invalid room ID")
		return
	}
	if _, err := s.store.GetRoom(r.Context(), roomID); err != nil {
		s.storeError(w, err)
		return
	}
	// HTTP server write deadlines are absolute and unsuitable for long-lived
	// upgraded connections. WebSocket writes have their own bounded contexts.
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	updates, unsubscribe := s.hub.subscribe(roomID)
	defer unsubscribe()
	ctx := r.Context()
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "ready"}); err != nil {
		return
	}
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
			if err := wsjson.Write(ctx, conn, map[string]string{"type": "refresh"}); err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) allowMutation(w http.ResponseWriter, r *http.Request) bool {
	if s.limiter.allow(s.clientIP(r)) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests")
	return false
}

func (s *Server) authorizeMutation(w http.ResponseWriter, r *http.Request, roomID string) bool {
	_, ok := s.authorizeMutationHash(w, r, roomID)
	return ok
}

func (s *Server) authorizeMutationHash(w http.ResponseWriter, r *http.Request, roomID string) (string, bool) {
	if !s.allowMutation(w, r) {
		return "", false
	}
	room, err := s.store.GetRoom(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return "", false
	}
	if !room.WriteProtected {
		return "", true
	}
	const prefix = "ClipboardWrite "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		writeError(w, http.StatusForbidden, "write_capability_required", "A write capability is required")
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	hash, valid := hashWriteToken(token)
	if !valid {
		writeError(w, http.StatusForbidden, "invalid_write_capability", "The write capability is invalid")
		return "", false
	}
	if err := s.store.AuthorizeWrite(r.Context(), roomID, hash); err != nil {
		if errors.Is(err, store.ErrForbidden) {
			writeError(w, http.StatusForbidden, "invalid_write_capability", "The write capability is invalid")
			return "", false
		}
		s.storeError(w, err)
		return "", false
	}
	return hash, true
}

func (s *Server) authorizeUpload(w http.ResponseWriter, r *http.Request) (store.Upload, bool) {
	roomID, uploadID := r.PathValue("room"), r.PathValue("upload")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(uploadID) {
		writeError(w, http.StatusBadRequest, "invalid_upload", "Invalid room or upload ID")
		return store.Upload{}, false
	}
	const prefix = "ClipboardUpload "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		writeError(w, http.StatusForbidden, "upload_capability_required", "An upload capability is required")
		return store.Upload{}, false
	}
	hash, valid := hashCapability(strings.TrimPrefix(header, prefix), "cu1_")
	if !valid {
		writeError(w, http.StatusForbidden, "invalid_upload_capability", "The upload capability is invalid")
		return store.Upload{}, false
	}
	upload, err := s.store.AuthorizeUpload(r.Context(), roomID, uploadID, hash)
	if err != nil {
		if errors.Is(err, store.ErrForbidden) {
			writeError(w, http.StatusForbidden, "invalid_upload_capability", "The upload capability is invalid")
			return store.Upload{}, false
		}
		s.storeError(w, err)
		return store.Upload{}, false
	}
	expires, err := time.Parse(time.RFC3339Nano, upload.ExpiresAt)
	if err != nil || time.Now().After(expires) {
		writeError(w, http.StatusGone, "upload_expired", "The upload session has expired")
		return store.Upload{}, false
	}
	return upload, true
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if forwarded := r.Header.Get("Forwarded"); forwarded != "" {
			for _, part := range strings.Split(forwarded, ";") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(part), "for="); ok {
					return strings.Trim(v, `"[]`)
				}
			}
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not_found", "Room or item not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, 409, "conflict", "Room already exists or encryption key does not match")
	case errors.Is(err, store.ErrLimit):
		writeError(w, 409, "limit_reached", "Configured limit reached")
	case errors.Is(err, store.ErrForbidden):
		writeError(w, 403, "invalid_write_capability", "The write capability is invalid")
	default:
		s.log.Printf("request failed: %v", err)
		writeError(w, 500, "internal_error", "Internal server error")
	}
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data: blob:; media-src 'self' blob:; frame-src 'self'; manifest-src 'self'; style-src 'self'; script-src 'self'; worker-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/healthz" {
			s.log.Printf("request method=%s path=%s duration=%s", r.Method, r.URL.Path, time.Since(start))
		}
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, max int64) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, 415, "content_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "invalid_json", "Invalid JSON request")
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid_json", "Request must contain one JSON value")
		return false
	}
	return true
}

func validToken(v string, min, max int) bool {
	if len(v) < min || len(v) > max {
		return false
	}
	for _, c := range v {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validAlias(alias string) bool {
	return utf8.ValidString(alias) && utf8.RuneCountInString(alias) <= 64
}

func (s *Server) removeDeletedObjects(deleted store.DeletedObjects) {
	for _, fileID := range deleted.FileIDs {
		if err := s.files.RemoveObject(fileID); err != nil {
			s.log.Printf("remove entry file object: %v", err)
		}
	}
	for _, uploadID := range deleted.UploadIDs {
		if err := s.files.RemoveUpload(uploadID); err != nil {
			s.log.Printf("remove entry upload: %v", err)
		}
	}
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		s.storeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_rooms Current rooms.\n# TYPE clipboard_exchange_rooms gauge\nclipboard_exchange_rooms %d\n", stats.Rooms)
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_entries Atomic entry metadata rows.\n# TYPE clipboard_exchange_entries gauge\nclipboard_exchange_entries %d\n", stats.Entries)
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_items Text payload rows.\n# TYPE clipboard_exchange_items gauge\nclipboard_exchange_items %d\n", stats.Items)
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_files Stored files.\n# TYPE clipboard_exchange_files gauge\nclipboard_exchange_files %d\n", stats.Files)
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_file_bytes Stored file bytes.\n# TYPE clipboard_exchange_file_bytes gauge\nclipboard_exchange_file_bytes %d\n", stats.FileBytes)
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_active_uploads Active upload sessions.\n# TYPE clipboard_exchange_active_uploads gauge\nclipboard_exchange_active_uploads %d\n", stats.ActiveUploads)
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_reserved_bytes Reserved upload bytes.\n# TYPE clipboard_exchange_reserved_bytes gauge\nclipboard_exchange_reserved_bytes %d\n", stats.ReservedBytes)
	_, _ = fmt.Fprintf(w, "# HELP clipboard_exchange_uptime_seconds Process uptime.\n# TYPE clipboard_exchange_uptime_seconds gauge\nclipboard_exchange_uptime_seconds %.0f\n", time.Since(s.started).Seconds())
}

func validItem(item store.Item, maxItemBytes int64) bool {
	switch item.Kind {
	case "text":
		return item.Content != "" && int64(len([]byte(item.Content))) <= maxItemBytes && validAlias(item.Alias) && item.Ciphertext == "" && item.IV == "" && item.KeyID == ""
	case "encrypted":
		return (item.Version == 1 || item.Version == 2) && validToken(item.IV, 16, 24) && validToken(item.KeyID, 43, 64) && validToken(item.Ciphertext, 20, int(maxItemBytes*2)) && item.Content == "" && item.Alias == ""
	default:
		return false
	}
}

func hashWriteToken(token string) (string, bool) {
	return hashCapability(token, "cw1_")
}

func hashCapability(token, prefix string) (string, bool) {
	if !strings.HasPrefix(token, prefix) || !validToken(strings.TrimPrefix(token, prefix), 43, 43) {
		return "", false
	}
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:]), true
}

func newOpaqueToken(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func validFileName(name string) bool {
	return name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 255 && !strings.ContainsRune(name, 0)
}

func validMIMEType(value string) bool {
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	_, _, err := mime.ParseMediaType(value)
	return err == nil
}

func inlineMIMEType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	if strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/") {
		return true
	}
	if strings.HasPrefix(mediaType, "image/") {
		return mediaType != "image/svg+xml"
	}
	switch mediaType {
	case "text/plain", "text/csv", "application/json", "application/pdf":
		return true
	default:
		return false
	}
}

func serveIcon(w http.ResponseWriter, size int) {
	pixels := make([]byte, size*size*4)
	set := func(x, y int, color [4]byte) {
		if x < 0 || x >= size || y < 0 || y >= size {
			return
		}
		offset := (y*size + x) * 4
		copy(pixels[offset:offset+4], color[:])
	}
	green, white := [4]byte{23, 107, 91, 255}, [4]byte{255, 255, 255, 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			set(x, y, green)
		}
	}
	drawArrow := func(y int, right bool) {
		thickness, start, end := size/11, size/5, size*4/5
		for py := y - thickness/2; py <= y+thickness/2; py++ {
			for px := start; px <= end; px++ {
				set(px, py, white)
			}
		}
		tip := end
		if !right {
			tip = start
		}
		for delta := 0; delta < size/7; delta++ {
			for width := 0; width <= delta; width++ {
				x := tip - delta
				if !right {
					x = tip + delta
				}
				set(x, y-width, white)
				set(x, y+width, white)
			}
		}
	}
	drawArrow(size*2/5, true)
	drawArrow(size*3/5, false)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_ = encodePNG(w, size, pixels)
}

func encodePNG(w io.Writer, size int, pixels []byte) error {
	var raw, compressed bytes.Buffer
	for y := 0; y < size; y++ {
		raw.WriteByte(0) // PNG filter: none.
		raw.Write(pixels[y*size*4 : (y+1)*size*4])
	}
	zipper := zlib.NewWriter(&compressed)
	if _, err := io.Copy(zipper, &raw); err != nil {
		return err
	}
	if err := zipper.Close(); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
		return err
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(size))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(size))
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA.
	if err := writePNGChunk(w, "IHDR", ihdr); err != nil {
		return err
	}
	if err := writePNGChunk(w, "IDAT", compressed.Bytes()); err != nil {
		return err
	}
	return writePNGChunk(w, "IEND", nil)
}

func writePNGChunk(w io.Writer, name string, data []byte) error {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	if _, err := w.Write(length[:]); err != nil {
		return err
	}
	chunk := append([]byte(name), data...)
	if _, err := w.Write(chunk); err != nil {
		return err
	}
	var checksum [4]byte
	binary.BigEndian.PutUint32(checksum[:], crc32.ChecksumIEEE(chunk))
	_, err := w.Write(checksum[:])
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
