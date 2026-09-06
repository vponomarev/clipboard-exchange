package httpserver

import (
	"context"
	"errors"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) storageOperations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/rooms") && !strings.HasSuffix(r.URL.Path, "/events") {
			done := s.files.BeginOperation()
			defer done()
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) lockUpload(id string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	lock := &s.uploadLocks[h.Sum32()%uint32(len(s.uploadLocks))]
	lock.Lock()
	return lock.Unlock
}

// Stream deadlines bound stalled I/O, not the duration of a progressing transfer.
const transferIdleTimeout = 30 * time.Second

type transferWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	status     int
	bytes      int64
	err        error
}

func newTransferWriter(w http.ResponseWriter) *transferWriter {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(transferIdleTimeout))
	return &transferWriter{ResponseWriter: w, controller: rc}
}
func (w *transferWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *transferWriter) WriteHeader(status int) {
	if w.status == 0 {
		_ = w.controller.SetWriteDeadline(time.Now().Add(transferIdleTimeout))
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *transferWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = w.controller.SetWriteDeadline(time.Now().Add(transferIdleTimeout))
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	if err != nil {
		w.err = err
	} else if n != len(data) {
		w.err = io.ErrShortWrite
		err = w.err
	}
	return n, err
}
func (w *transferWriter) complete(r *http.Request, size int64) bool {
	if r.Method != http.MethodGet || r.Header.Get("Range") != "" || r.Context().Err() != nil || w.status != http.StatusOK || w.err != nil || (size >= 0 && w.bytes != size) {
		return false
	}
	err := w.controller.Flush()
	return err == nil || errors.Is(err, http.ErrNotSupported)
}

type transferReader struct {
	io.Reader
	controller *http.ResponseController
}

func (r transferReader) Read(p []byte) (int, error) {
	_ = r.controller.SetReadDeadline(time.Now().Add(transferIdleTimeout))
	return r.Reader.Read(p)
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	s.readinessMu.Lock()
	if time.Since(s.readinessAt) < time.Second {
		err := s.readinessErr
		s.readinessMu.Unlock()
		readinessResponse(w, err)
		return
	}
	if s.readinessDone == nil {
		done := make(chan struct{})
		s.readinessDone = done
		go func() {
			checkCtx, checkCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer checkCancel()
			err := s.store.Ping(checkCtx)
			if err == nil {
				release := s.files.BeginOperation()
				err = s.files.Check()
				release()
			}
			s.readinessMu.Lock()
			s.readinessErr, s.readinessAt, s.readinessDone = err, time.Now(), nil
			close(done)
			s.readinessMu.Unlock()
		}()
	}
	done := s.readinessDone
	s.readinessMu.Unlock()
	select {
	case <-done:
		s.readinessMu.Lock()
		err := s.readinessErr
		s.readinessMu.Unlock()
		readinessResponse(w, err)
	case <-ctx.Done():
		readinessResponse(w, ctx.Err())
	}
}
func readinessResponse(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "Storage is unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
