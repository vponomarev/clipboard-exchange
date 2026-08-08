package httpserver

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/vponomarev/clipboard-exchange/internal/config"
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
	log     *log.Logger
	hub     *hub
	limiter *rateLimiter
	static  http.Handler
}

type roomResponse struct {
	Room  store.Room   `json:"room"`
	Items []store.Item `json:"items"`
}

func New(cfg config.Config, db *store.Store, logger *log.Logger) http.Handler {
	assets, _ := fs.Sub(webFiles, "web")
	s := &Server{cfg: cfg, store: db, log: logger, hub: newHub(), limiter: newRateLimiter(cfg.RateLimit), static: http.FileServer(http.FS(assets))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /api/rooms", s.createRoom)
	mux.HandleFunc("GET /api/rooms/{room}", s.getRoom)
	mux.HandleFunc("POST /api/rooms/{room}/items", s.addItem)
	mux.HandleFunc("DELETE /api/rooms/{room}/items/{item}", s.deleteItem)
	mux.HandleFunc("GET /api/rooms/{room}/events", s.events)
	mux.HandleFunc("GET /assets/", s.asset)
	mux.HandleFunc("GET /r/{room}", s.page)
	mux.HandleFunc("GET /", s.page)
	return s.security(s.logging(mux))
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && !roomIDPattern.MatchString(r.PathValue("room")) {
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
	r.URL.Path = strings.TrimPrefix(path.Clean(r.URL.Path), "/assets")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	s.static.ServeHTTP(w, r)
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(w, r) {
		return
	}
	var req struct {
		ID        string `json:"id"`
		Encrypted bool   `json:"encrypted"`
		KeyID     string `json:"keyId"`
	}
	if !decodeJSON(w, r, &req, 4096) {
		return
	}
	if !roomIDPattern.MatchString(req.ID) || (req.Encrypted && !validToken(req.KeyID, 43, 64)) || (!req.Encrypted && req.KeyID != "") {
		writeError(w, http.StatusBadRequest, "invalid_room", "Room ID or encryption metadata is invalid")
		return
	}
	err := s.store.CreateRoom(r.Context(), store.Room{ID: req.ID, Encrypted: req.Encrypted, KeyID: req.KeyID}, s.cfg.MaxRooms)
	if err != nil {
		s.storeError(w, err)
		return
	}
	w.Header().Set("Location", "/r/"+req.ID)
	writeJSON(w, http.StatusCreated, map[string]string{"id": req.ID})
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
	writeJSON(w, http.StatusOK, roomResponse{Room: room, Items: items})
}

func (s *Server) addItem(w http.ResponseWriter, r *http.Request) {
	if !s.allowMutation(w, r) {
		return
	}
	roomID := r.PathValue("room")
	if !roomIDPattern.MatchString(roomID) {
		writeError(w, 400, "invalid_room", "Invalid room ID")
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
	switch item.Kind {
	case "text":
		if item.Content == "" || int64(len([]byte(item.Content))) > s.cfg.MaxItemBytes || item.Ciphertext != "" || item.IV != "" || item.KeyID != "" {
			writeError(w, 400, "invalid_item", "Invalid text item")
			return
		}
	case "encrypted":
		if item.Version != 1 || !validToken(item.IV, 16, 24) || !validToken(item.KeyID, 43, 64) || !validToken(item.Ciphertext, 20, int(s.cfg.MaxItemBytes*2)) || item.Content != "" {
			writeError(w, 400, "invalid_item", "Invalid encrypted item")
			return
		}
	default:
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
	if !s.allowMutation(w, r) {
		return
	}
	roomID, itemID := r.PathValue("room"), r.PathValue("item")
	if !roomIDPattern.MatchString(roomID) || !objectIDPattern.MatchString(itemID) {
		writeError(w, 400, "invalid_item", "Invalid room or item ID")
		return
	}
	if err := s.store.DeleteItem(r.Context(), roomID, itemID); err != nil {
		s.storeError(w, err)
		return
	}
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
	default:
		s.log.Printf("request failed: %v", err)
		writeError(w, 500, "internal_error", "Internal server error")
	}
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
