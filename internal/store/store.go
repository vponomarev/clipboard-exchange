package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrLimit    = errors.New("limit exceeded")
)

type Store struct{ db *sql.DB }

type Room struct {
	ID        string `json:"id"`
	Encrypted bool   `json:"encrypted"`
	KeyID     string `json:"keyId,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Item struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Content    string `json:"content,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
	IV         string `json:"iv,omitempty"`
	KeyID      string `json:"keyId,omitempty"`
	Version    int    `json:"version,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS rooms (
			id TEXT PRIMARY KEY,
			encrypted INTEGER NOT NULL,
			key_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS items (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			ciphertext TEXT NOT NULL DEFAULT '',
			iv TEXT NOT NULL DEFAULT '',
			key_id TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS items_room_created ON items(room_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS rooms_updated ON rooms(updated_at)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize database: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateRoom(ctx context.Context, room Room, maxRooms int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms`).Scan(&count); err != nil {
		return err
	}
	if count >= maxRooms {
		return ErrLimit
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO rooms(id, encrypted, key_id, created_at, updated_at) VALUES(?,?,?,?,?)`, room.ID, room.Encrypted, room.KeyID, now, now)
	if err != nil {
		if isConstraint(err) {
			return ErrConflict
		}
		return err
	}
	return tx.Commit()
}

func (s *Store) GetRoom(ctx context.Context, id string) (Room, error) {
	var room Room
	var encrypted int
	err := s.db.QueryRowContext(ctx, `SELECT id, encrypted, key_id, created_at, updated_at FROM rooms WHERE id=?`, id).Scan(&room.ID, &encrypted, &room.KeyID, &room.CreatedAt, &room.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	room.Encrypted = encrypted != 0
	return room, err
}

func (s *Store) ListItems(ctx context.Context, roomID string) ([]Item, error) {
	if _, err := s.GetRoom(ctx, roomID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, content, ciphertext, iv, key_id, version, created_at FROM items WHERE room_id=? ORDER BY created_at DESC, id DESC`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.Kind, &item.Content, &item.Ciphertext, &item.IV, &item.KeyID, &item.Version, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AddItem(ctx context.Context, roomID string, item Item, maxItems int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var encrypted int
	var roomKeyID string
	if err := tx.QueryRowContext(ctx, `SELECT encrypted, key_id FROM rooms WHERE id=?`, roomID).Scan(&encrypted, &roomKeyID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if (encrypted != 0) != (item.Kind == "encrypted") || (encrypted != 0 && roomKeyID != item.KeyID) {
		return ErrConflict
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE room_id=?`, roomID).Scan(&count); err != nil {
		return err
	}
	if count >= maxItems {
		return ErrLimit
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO items(id, room_id, kind, content, ciphertext, iv, key_id, version, created_at) VALUES(?,?,?,?,?,?,?,?,?)`, item.ID, roomID, item.Kind, item.Content, item.Ciphertext, item.IV, item.KeyID, item.Version, now)
	if err != nil {
		if isConstraint(err) {
			return ErrConflict
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE rooms SET updated_at=? WHERE id=?`, now, roomID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteItem(ctx context.Context, roomID, itemID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id=? AND room_id=?`, itemID, roomID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE rooms SET updated_at=? WHERE id=?`, now, roomID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteInactive(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE updated_at < ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) RunCleanup(ctx context.Context, ttl, interval time.Duration) {
	if ttl == 0 {
		return
	}
	_, _ = s.DeleteInactive(ctx, time.Now().Add(-ttl))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.DeleteInactive(ctx, time.Now().Add(-ttl))
		}
	}
}

func isConstraint(err error) bool {
	return err != nil && (contains(err.Error(), "constraint failed") || contains(err.Error(), "UNIQUE constraint"))
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
