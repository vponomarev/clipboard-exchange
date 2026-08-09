package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrLimit     = errors.New("limit exceeded")
	ErrForbidden = errors.New("forbidden")
)

type Store struct{ db *sql.DB }

type Room struct {
	ID             string `json:"id"`
	Encrypted      bool   `json:"encrypted"`
	KeyID          string `json:"keyId,omitempty"`
	WriteProtected bool   `json:"writeProtected"`
	WriteHash      string `json:"-"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type Item struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Content    string `json:"content,omitempty"`
	Alias      string `json:"alias,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
	IV         string `json:"iv,omitempty"`
	KeyID      string `json:"keyId,omitempty"`
	Version    int    `json:"version,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

type File struct {
	ID                 string `json:"id"`
	RoomID             string `json:"-"`
	Name               string `json:"name,omitempty"`
	MIMEType           string `json:"mimeType,omitempty"`
	Alias              string `json:"alias,omitempty"`
	Size               int64  `json:"size"`
	Encrypted          bool   `json:"encrypted,omitempty"`
	KeyID              string `json:"keyId,omitempty"`
	ManifestCiphertext string `json:"manifestCiphertext,omitempty"`
	ManifestIV         string `json:"manifestIv,omitempty"`
	Version            int    `json:"version,omitempty"`
	ChunkSize          int64  `json:"chunkSize,omitempty"`
	ChunkCount         int    `json:"chunkCount,omitempty"`
	CreatedAt          string `json:"createdAt"`
}

type Upload struct {
	ID             string         `json:"id"`
	RoomID         string         `json:"-"`
	FileID         string         `json:"fileId"`
	Name           string         `json:"name"`
	MIMEType       string         `json:"mimeType"`
	Alias          string         `json:"alias,omitempty"`
	Size           int64          `json:"size"`
	ChunkSize      int64          `json:"chunkSize"`
	ChunkCount     int            `json:"chunkCount"`
	PlainChunkSize int64          `json:"plainChunkSize"`
	Encrypted      bool           `json:"encrypted,omitempty"`
	KeyID          string         `json:"keyId,omitempty"`
	TokenHash      string         `json:"-"`
	ExpiresAt      string         `json:"expiresAt"`
	CreatedAt      string         `json:"createdAt"`
	Received       []int          `json:"received"`
	Digests        map[int]string `json:"digests"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		return nil, fmt.Errorf("read schema version: %w", err)
	}
	if version == 0 {
		var legacyTables int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('rooms','items')`).Scan(&legacyTables); err != nil {
			db.Close()
			return nil, fmt.Errorf("inspect database schema: %w", err)
		}
		if legacyTables != 0 {
			db.Close()
			return nil, errors.New("legacy database schema is unsupported; archive it and start with an empty database")
		}
	} else if version != 2 && version != 3 {
		db.Close()
		return nil, fmt.Errorf("unsupported database schema version %d", version)
	}
	if version == 2 {
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("start schema migration: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE rooms ADD COLUMN write_protected INTEGER NOT NULL DEFAULT 1`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("migrate database schema: %w", err)
		}
		if _, err := tx.Exec(`PRAGMA user_version=3`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record database schema migration: %w", err)
		}
		if err := tx.Commit(); err != nil {
			db.Close()
			return nil, fmt.Errorf("commit database schema migration: %w", err)
		}
	}
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA foreign_keys=ON`,
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS rooms (
			id TEXT PRIMARY KEY,
			encrypted INTEGER NOT NULL,
			key_id TEXT NOT NULL DEFAULT '',
			write_protected INTEGER NOT NULL DEFAULT 1,
			write_hash TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS items (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			alias TEXT NOT NULL DEFAULT '',
			ciphertext TEXT NOT NULL DEFAULT '',
			iv TEXT NOT NULL DEFAULT '',
			key_id TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS items_room_created ON items(room_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS rooms_updated ON rooms(updated_at)`,
		`CREATE TABLE IF NOT EXISTS uploads (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			file_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			alias TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL,
			chunk_size INTEGER NOT NULL,
			chunk_count INTEGER NOT NULL,
			plain_chunk_size INTEGER NOT NULL,
			encrypted INTEGER NOT NULL DEFAULT 0,
			key_id TEXT NOT NULL DEFAULT '',
			token_hash TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS upload_chunks (
			upload_id TEXT NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
			chunk_index INTEGER NOT NULL,
			size INTEGER NOT NULL,
			digest TEXT NOT NULL,
			nonce TEXT,
			PRIMARY KEY(upload_id, chunk_index)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS upload_chunk_nonce ON upload_chunks(upload_id, nonce)`,
		`CREATE TABLE IF NOT EXISTS files (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			alias TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL,
			encrypted INTEGER NOT NULL DEFAULT 0,
			key_id TEXT NOT NULL DEFAULT '',
			manifest_ciphertext TEXT NOT NULL DEFAULT '',
			manifest_iv TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 0,
			chunk_size INTEGER NOT NULL DEFAULT 0,
			chunk_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS uploads_room ON uploads(room_id)`,
		`CREATE INDEX IF NOT EXISTS uploads_expiry ON uploads(expires_at)`,
		`CREATE INDEX IF NOT EXISTS files_room_created ON files(room_id, created_at, id)`,
		`PRAGMA user_version=3`,
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
	_, err = tx.ExecContext(ctx, `INSERT INTO rooms(id, encrypted, key_id, write_protected, write_hash, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`, room.ID, room.Encrypted, room.KeyID, room.WriteProtected, room.WriteHash, now, now)
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
	var encrypted, writeProtected int
	err := s.db.QueryRowContext(ctx, `SELECT id, encrypted, key_id, write_protected, write_hash, created_at, updated_at FROM rooms WHERE id=?`, id).Scan(&room.ID, &encrypted, &room.KeyID, &writeProtected, &room.WriteHash, &room.CreatedAt, &room.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	room.Encrypted = encrypted != 0
	room.WriteProtected = writeProtected != 0
	return room, err
}

func (s *Store) ListItems(ctx context.Context, roomID string) ([]Item, error) {
	if _, err := s.GetRoom(ctx, roomID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, content, alias, ciphertext, iv, key_id, version, created_at FROM items WHERE room_id=? ORDER BY created_at DESC, id DESC`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.Kind, &item.Content, &item.Alias, &item.Ciphertext, &item.IV, &item.KeyID, &item.Version, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListFiles(ctx context.Context, roomID string) ([]File, error) {
	if _, err := s.GetRoom(ctx, roomID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,room_id,name,mime_type,alias,size,encrypted,key_id,manifest_ciphertext,manifest_iv,version,chunk_size,chunk_count,created_at FROM files WHERE room_id=? ORDER BY created_at DESC, id DESC`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]File, 0)
	for rows.Next() {
		var file File
		var encrypted int
		if err := rows.Scan(&file.ID, &file.RoomID, &file.Name, &file.MIMEType, &file.Alias, &file.Size, &encrypted, &file.KeyID, &file.ManifestCiphertext, &file.ManifestIV, &file.Version, &file.ChunkSize, &file.ChunkCount, &file.CreatedAt); err != nil {
			return nil, err
		}
		file.Encrypted = encrypted != 0
		files = append(files, file)
	}
	return files, rows.Err()
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
	_, err = tx.ExecContext(ctx, `INSERT INTO items(id, room_id, kind, content, alias, ciphertext, iv, key_id, version, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, item.ID, roomID, item.Kind, item.Content, item.Alias, item.Ciphertext, item.IV, item.KeyID, item.Version, now)
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

func (s *Store) AuthorizeWrite(ctx context.Context, roomID, candidateHash string) error {
	var expected string
	var writeProtected int
	if err := s.db.QueryRowContext(ctx, `SELECT write_protected,write_hash FROM rooms WHERE id=?`, roomID).Scan(&writeProtected, &expected); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if writeProtected == 0 {
		return nil
	}
	if len(expected) != len(candidateHash) || subtle.ConstantTimeCompare([]byte(expected), []byte(candidateHash)) != 1 {
		return ErrForbidden
	}
	return nil
}

func (s *Store) RotateWriteHash(ctx context.Context, roomID, currentHash, nextHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var expected string
	var writeProtected int
	if err := tx.QueryRowContext(ctx, `SELECT write_protected,write_hash FROM rooms WHERE id=?`, roomID).Scan(&writeProtected, &expected); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if writeProtected == 0 {
		return ErrConflict
	}
	if len(expected) != len(currentHash) || subtle.ConstantTimeCompare([]byte(expected), []byte(currentHash)) != 1 {
		return ErrForbidden
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET write_hash=?, updated_at=? WHERE id=?`, nextHash, now, roomID); err != nil {
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

func (s *Store) CreateUpload(ctx context.Context, upload Upload, maxRoomBytes int64, maxActive int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var encrypted int
	var roomKeyID string
	if err := tx.QueryRowContext(ctx, `SELECT encrypted,key_id FROM rooms WHERE id=?`, upload.RoomID).Scan(&encrypted, &roomKeyID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if (encrypted != 0) != upload.Encrypted || (upload.Encrypted && roomKeyID != upload.KeyID) {
		return ErrConflict
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads`).Scan(&active); err != nil {
		return err
	}
	if active >= maxActive {
		return ErrLimit
	}
	var used, reserved int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM files WHERE room_id=?`, upload.RoomID).Scan(&used); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM uploads WHERE room_id=?`, upload.RoomID).Scan(&reserved); err != nil {
		return err
	}
	if upload.Size > maxRoomBytes-used-reserved {
		return ErrLimit
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	upload.CreatedAt = now
	_, err = tx.ExecContext(ctx, `INSERT INTO uploads(id,room_id,file_id,name,mime_type,alias,size,chunk_size,chunk_count,plain_chunk_size,encrypted,key_id,token_hash,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, upload.ID, upload.RoomID, upload.FileID, upload.Name, upload.MIMEType, upload.Alias, upload.Size, upload.ChunkSize, upload.ChunkCount, upload.PlainChunkSize, upload.Encrypted, upload.KeyID, upload.TokenHash, upload.ExpiresAt, now)
	if err != nil {
		if isConstraint(err) {
			return ErrConflict
		}
		return err
	}
	return tx.Commit()
}

func (s *Store) GetUpload(ctx context.Context, roomID, uploadID string) (Upload, error) {
	var upload Upload
	var encrypted int
	err := s.db.QueryRowContext(ctx, `SELECT id,room_id,file_id,name,mime_type,alias,size,chunk_size,chunk_count,plain_chunk_size,encrypted,key_id,token_hash,expires_at,created_at FROM uploads WHERE id=? AND room_id=?`, uploadID, roomID).Scan(&upload.ID, &upload.RoomID, &upload.FileID, &upload.Name, &upload.MIMEType, &upload.Alias, &upload.Size, &upload.ChunkSize, &upload.ChunkCount, &upload.PlainChunkSize, &encrypted, &upload.KeyID, &upload.TokenHash, &upload.ExpiresAt, &upload.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, err
	}
	upload.Encrypted = encrypted != 0
	rows, err := s.db.QueryContext(ctx, `SELECT chunk_index,digest FROM upload_chunks WHERE upload_id=? ORDER BY chunk_index`, uploadID)
	if err != nil {
		return Upload{}, err
	}
	defer rows.Close()
	upload.Received = make([]int, 0)
	upload.Digests = make(map[int]string)
	for rows.Next() {
		var index int
		var digest string
		if err := rows.Scan(&index, &digest); err != nil {
			return Upload{}, err
		}
		upload.Received = append(upload.Received, index)
		upload.Digests[index] = digest
	}
	return upload, rows.Err()
}

func (s *Store) AuthorizeUpload(ctx context.Context, roomID, uploadID, candidateHash string) (Upload, error) {
	upload, err := s.GetUpload(ctx, roomID, uploadID)
	if err != nil {
		return Upload{}, err
	}
	if len(upload.TokenHash) != len(candidateHash) || subtle.ConstantTimeCompare([]byte(upload.TokenHash), []byte(candidateHash)) != 1 {
		return Upload{}, ErrForbidden
	}
	return upload, nil
}

func (s *Store) RecordChunk(ctx context.Context, uploadID string, index int, size int64, digest, nonce string) error {
	var nonceValue any
	if nonce != "" {
		nonceValue = nonce
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO upload_chunks(upload_id,chunk_index,size,digest,nonce) VALUES(?,?,?,?,?)`, uploadID, index, size, digest, nonceValue)
	if err == nil {
		return nil
	}
	if !isConstraint(err) {
		return err
	}
	var existingSize int64
	var existingDigest string
	if err := s.db.QueryRowContext(ctx, `SELECT size,digest FROM upload_chunks WHERE upload_id=? AND chunk_index=?`, uploadID, index).Scan(&existingSize, &existingDigest); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	} else if err != nil {
		return err
	}
	if existingSize != size || existingDigest != digest {
		return ErrConflict
	}
	return nil
}

func (s *Store) CompleteUpload(ctx context.Context, roomID, uploadID, manifestCiphertext, manifestIV string, version int) (File, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	var file File
	var chunkCount, received int
	var receivedSize int64
	var encrypted int
	err = tx.QueryRowContext(ctx, `SELECT file_id,room_id,name,mime_type,alias,size,chunk_count,encrypted,key_id,plain_chunk_size FROM uploads WHERE id=? AND room_id=?`, uploadID, roomID).Scan(&file.ID, &file.RoomID, &file.Name, &file.MIMEType, &file.Alias, &file.Size, &chunkCount, &encrypted, &file.KeyID, &file.ChunkSize)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(size),0) FROM upload_chunks WHERE upload_id=?`, uploadID).Scan(&received, &receivedSize); err != nil {
		return File{}, err
	}
	if received != chunkCount || receivedSize != file.Size {
		return File{}, ErrConflict
	}
	file.Encrypted = encrypted != 0
	file.ChunkCount = chunkCount
	file.ManifestCiphertext = manifestCiphertext
	file.ManifestIV = manifestIV
	file.Version = version
	file.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO files(id,room_id,name,mime_type,alias,size,encrypted,key_id,manifest_ciphertext,manifest_iv,version,chunk_size,chunk_count,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, file.ID, file.RoomID, file.Name, file.MIMEType, file.Alias, file.Size, file.Encrypted, file.KeyID, file.ManifestCiphertext, file.ManifestIV, file.Version, file.ChunkSize, file.ChunkCount, file.CreatedAt); err != nil {
		if isConstraint(err) {
			return File{}, ErrConflict
		}
		return File{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE id=?`, uploadID); err != nil {
		return File{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET updated_at=? WHERE id=?`, file.CreatedAt, roomID); err != nil {
		return File{}, err
	}
	if err := tx.Commit(); err != nil {
		return File{}, err
	}
	return file, nil
}

func (s *Store) AbortUpload(ctx context.Context, roomID, uploadID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id=? AND room_id=?`, uploadID, roomID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetFile(ctx context.Context, roomID, fileID string) (File, error) {
	var file File
	var encrypted int
	err := s.db.QueryRowContext(ctx, `SELECT id,room_id,name,mime_type,alias,size,encrypted,key_id,manifest_ciphertext,manifest_iv,version,chunk_size,chunk_count,created_at FROM files WHERE id=? AND room_id=?`, fileID, roomID).Scan(&file.ID, &file.RoomID, &file.Name, &file.MIMEType, &file.Alias, &file.Size, &encrypted, &file.KeyID, &file.ManifestCiphertext, &file.ManifestIV, &file.Version, &file.ChunkSize, &file.ChunkCount, &file.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	file.Encrypted = encrypted != 0
	return file, err
}

func (s *Store) DeleteFile(ctx context.Context, roomID, fileID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM files WHERE id=? AND room_id=?`, fileID, roomID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), roomID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteExpiredUploads(ctx context.Context, before time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM uploads WHERE expires_at < ?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return ids, nil
	}
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id=?`, id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func (s *Store) StorageReferences(ctx context.Context) (map[string]bool, map[string]bool, error) {
	uploads := make(map[string]bool)
	objects := make(map[string]bool)
	rows, err := s.db.QueryContext(ctx, `SELECT id,file_id FROM uploads`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var uploadID, fileID string
		if err := rows.Scan(&uploadID, &fileID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		uploads[uploadID] = true
		objects[fileID] = true
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id FROM files`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fileID string
		if err := rows.Scan(&fileID); err != nil {
			return nil, nil, err
		}
		objects[fileID] = true
	}
	return uploads, objects, rows.Err()
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
