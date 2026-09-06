package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/vponomarev/clipboard-exchange/internal/store"
)

// history is a bounded text-only snapshot; limit=0 retrieves only room metadata.
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("room")
	limit := 30
	if value := r.URL.Query().Get("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 30 {
			writeError(w, 400, "invalid_limit", "Limit must be between 0 and 30")
			return
		}
		limit = n
	}
	if !roomIDPattern.MatchString(roomID) {
		writeError(w, 400, "invalid_room", "Invalid room ID")
		return
	}
	room, err := s.store.GetRoom(r.Context(), roomID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	items, err := s.store.RecentItems(r.Context(), roomID, limit)
	if err != nil {
		s.storeError(w, err)
		return
	}
	used := 0
	for i, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			s.storeError(w, err)
			return
		}
		used += len(encoded) + 1
		if used > 8<<20 {
			if i == 0 {
				writeError(w, 413, "item_too_large", "Latest item exceeds the history response budget")
				return
			}
			items = items[:i]
			break
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Room  store.Room   `json:"room"`
		Items []store.Item `json:"items"`
	}{room, items})
}

func (s *Server) completedUpload(w http.ResponseWriter, r *http.Request) bool {
	hash, valid := hashCapability(strings.TrimPrefix(r.Header.Get("Authorization"), "ClipboardUpload "), "cu1_")
	if !valid || !strings.HasPrefix(r.Header.Get("Authorization"), "ClipboardUpload ") {
		return false
	}
	file, err := s.store.CompletedUpload(r.Context(), r.PathValue("room"), r.PathValue("upload"), hash)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if errors.Is(err, store.ErrForbidden) {
		writeError(w, http.StatusForbidden, "invalid_upload_capability", "The upload capability is invalid")
		return true
	}
	if err != nil {
		s.storeError(w, err)
		return true
	}
	writeJSON(w, http.StatusCreated, file)
	return true
}
