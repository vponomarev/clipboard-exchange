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
	TTLSeconds     int64  `json:"ttlSeconds,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type Entry struct {
	ID                  string `json:"id"`
	RoomID              string `json:"-"`
	ExpectedFiles       int    `json:"expectedFiles"`
	Published           bool   `json:"published"`
	Pinned              bool   `json:"pinned,omitempty"`
	ExpiresAt           string `json:"expiresAt,omitempty"`
	DeleteAfterDownload bool   `json:"deleteAfterDownload,omitempty"`
	CreatedAt           string `json:"createdAt"`
}

type DeletedObjects struct {
	FileIDs   []string
	UploadIDs []string
}

type Statistics struct {
	Rooms         int64
	ShortLinks    int64
	Items         int64
	Entries       int64
	Files         int64
	FileBytes     int64
	ActiveUploads int64
	ReservedBytes int64
}

type ShortLink struct {
	Code          string `json:"code"`
	Ciphertext    string `json:"ciphertext"`
	IV            string `json:"iv"`
	Salt          string `json:"salt"`
	TokenHash     string `json:"-"`
	KDFIterations int    `json:"kdfIterations"`
	MaxUses       int    `json:"maxUses"`
	UseCount      int    `json:"useCount"`
	ExpiresAt     string `json:"expiresAt"`
	CreatedAt     string `json:"createdAt"`
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
	EntryID            string `json:"entryId"`
	EntryIndex         int    `json:"entryIndex"`
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
	EntryID        string         `json:"entryId"`
	EntryIndex     int            `json:"entryIndex"`
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
	} else if version != 2 && version != 3 && version != 4 && version != 5 && version != 6 {
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
		version = 3
	}
	if version == 3 {
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("start entry grouping migration: %w", err)
		}
		for _, table := range []string{"uploads", "files"} {
			var exists int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("inspect entry grouping schema: %w", err)
			}
			if exists == 0 {
				continue
			}
			idColumn := "file_id"
			if table == "files" {
				idColumn = "id"
			}
			if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN entry_id TEXT NOT NULL DEFAULT ''`, table)); err != nil {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("migrate entry grouping schema: %w", err)
			}
			if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN entry_index INTEGER NOT NULL DEFAULT 0`, table)); err != nil {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("migrate attachment ordering schema: %w", err)
			}
			if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET entry_id=%s WHERE entry_id=''`, table, idColumn)); err != nil {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("initialize entry grouping data: %w", err)
			}
		}
		for _, stmt := range []string{`PRAGMA user_version=4`} {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				db.Close()
				return nil, fmt.Errorf("migrate entry grouping schema: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			db.Close()
			return nil, fmt.Errorf("commit entry grouping migration: %w", err)
		}
		version = 4
	}
	if version == 4 {
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("start atomic entries migration: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE rooms ADD COLUMN ttl_seconds INTEGER NOT NULL DEFAULT 0`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("migrate room TTL schema: %w", err)
		}
		if _, err := tx.Exec(`PRAGMA user_version=5`); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("record atomic entries migration: %w", err)
		}
		if err := tx.Commit(); err != nil {
			db.Close()
			return nil, fmt.Errorf("commit atomic entries migration: %w", err)
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
			ttl_seconds INTEGER NOT NULL DEFAULT 0,
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
		`CREATE TABLE IF NOT EXISTS entries (
			id TEXT NOT NULL,
			room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			expected_files INTEGER NOT NULL DEFAULT 0,
			published INTEGER NOT NULL DEFAULT 0,
			pinned INTEGER NOT NULL DEFAULT 0,
			expires_at TEXT NOT NULL DEFAULT '',
			delete_after_download INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			PRIMARY KEY(room_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS entries_room_published ON entries(room_id, published, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS entries_expiry ON entries(expires_at)`,
		`CREATE INDEX IF NOT EXISTS rooms_updated ON rooms(updated_at)`,
		`CREATE TABLE IF NOT EXISTS uploads (
			id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
			file_id TEXT NOT NULL UNIQUE,
			entry_id TEXT NOT NULL,
			entry_index INTEGER NOT NULL DEFAULT 0,
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
			entry_id TEXT NOT NULL,
			entry_index INTEGER NOT NULL DEFAULT 0,
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
		`CREATE INDEX IF NOT EXISTS files_room_entry ON files(room_id, entry_id)`,
		`CREATE TABLE IF NOT EXISTS short_links (
			code TEXT PRIMARY KEY,
			ciphertext TEXT NOT NULL,
			iv TEXT NOT NULL,
			salt TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			kdf_iterations INTEGER NOT NULL,
			max_uses INTEGER NOT NULL,
			use_count INTEGER NOT NULL DEFAULT 0,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS short_links_expiry ON short_links(expires_at)`,
		`PRAGMA user_version=6`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize database: %w", err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateShortLink(ctx context.Context, link ShortLink, maxActive int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM short_links WHERE expires_at>? AND (max_uses=0 OR use_count<max_uses)`, now).Scan(&active); err != nil {
		return err
	}
	if active >= maxActive {
		return ErrLimit
	}
	link.CreatedAt = now
	_, err = tx.ExecContext(ctx, `INSERT INTO short_links(code,ciphertext,iv,salt,token_hash,kdf_iterations,max_uses,use_count,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, link.Code, link.Ciphertext, link.IV, link.Salt, link.TokenHash, link.KDFIterations, link.MaxUses, 0, link.ExpiresAt, link.CreatedAt)
	if isConstraint(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetShortLink(ctx context.Context, code string) (ShortLink, error) {
	var link ShortLink
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := s.db.QueryRowContext(ctx, `SELECT code,ciphertext,iv,salt,token_hash,kdf_iterations,max_uses,use_count,expires_at,created_at FROM short_links WHERE code=? AND expires_at>? AND (max_uses=0 OR use_count<max_uses)`, code, now).Scan(&link.Code, &link.Ciphertext, &link.IV, &link.Salt, &link.TokenHash, &link.KDFIterations, &link.MaxUses, &link.UseCount, &link.ExpiresAt, &link.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ShortLink{}, ErrNotFound
	}
	return link, err
}

func (s *Store) RedeemShortLink(ctx context.Context, code, candidateHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var tokenHash string
	err = tx.QueryRowContext(ctx, `SELECT token_hash FROM short_links WHERE code=? AND expires_at>? AND (max_uses=0 OR use_count<max_uses)`, code, now).Scan(&tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(tokenHash), []byte(candidateHash)) != 1 {
		return ErrForbidden
	}
	result, err := tx.ExecContext(ctx, `UPDATE short_links SET use_count=use_count+1 WHERE code=? AND expires_at>? AND (max_uses=0 OR use_count<max_uses)`, code, now)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) DeleteExpiredShortLinks(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM short_links WHERE expires_at<=?`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) Snapshot(ctx context.Context, target string) error {
	if target == "" {
		return errors.New("snapshot target is empty")
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, target); err != nil {
		return fmt.Errorf("create SQLite snapshot: %w", err)
	}
	return nil
}

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
	_, err = tx.ExecContext(ctx, `INSERT INTO rooms(id, encrypted, key_id, write_protected, write_hash, ttl_seconds, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`, room.ID, room.Encrypted, room.KeyID, room.WriteProtected, room.WriteHash, room.TTLSeconds, now, now)
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
	err := s.db.QueryRowContext(ctx, `SELECT id, encrypted, key_id, write_protected, write_hash, ttl_seconds, created_at, updated_at FROM rooms WHERE id=?`, id).Scan(&room.ID, &encrypted, &room.KeyID, &writeProtected, &room.WriteHash, &room.TTLSeconds, &room.CreatedAt, &room.UpdatedAt)
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.kind,i.content,i.alias,i.ciphertext,i.iv,i.key_id,i.version,i.created_at
		FROM items i LEFT JOIN entries e ON e.room_id=i.room_id AND e.id=i.id
		WHERE i.room_id=? AND (e.id IS NULL OR (e.published=1 AND (e.expires_at='' OR e.expires_at>?)))
		ORDER BY COALESCE(e.pinned,0) DESC,i.created_at DESC,i.id DESC`, roomID, now)
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,f.room_id,f.entry_id,f.entry_index,f.name,f.mime_type,f.alias,f.size,f.encrypted,f.key_id,f.manifest_ciphertext,f.manifest_iv,f.version,f.chunk_size,f.chunk_count,f.created_at
		FROM files f LEFT JOIN entries e ON e.room_id=f.room_id AND e.id=f.entry_id
		WHERE f.room_id=? AND (e.id IS NULL OR (e.published=1 AND (e.expires_at='' OR e.expires_at>?)))
		ORDER BY COALESCE(e.pinned,0) DESC,f.created_at DESC,f.id DESC`, roomID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]File, 0)
	for rows.Next() {
		var file File
		var encrypted int
		if err := rows.Scan(&file.ID, &file.RoomID, &file.EntryID, &file.EntryIndex, &file.Name, &file.MIMEType, &file.Alias, &file.Size, &encrypted, &file.KeyID, &file.ManifestCiphertext, &file.ManifestIV, &file.Version, &file.ChunkSize, &file.ChunkCount, &file.CreatedAt); err != nil {
			return nil, err
		}
		file.Encrypted = encrypted != 0
		files = append(files, file)
	}
	return files, rows.Err()
}

func (s *Store) ListEntries(ctx context.Context, roomID string) ([]Entry, error) {
	if _, err := s.GetRoom(ctx, roomID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(ctx, `SELECT id,room_id,expected_files,published,pinned,expires_at,delete_after_download,created_at
		FROM entries WHERE room_id=? AND published=1 AND (expires_at='' OR expires_at>?)
		ORDER BY pinned DESC,created_at DESC,id DESC`, roomID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		var entry Entry
		var published, pinned, deleteAfterDownload int
		if err := rows.Scan(&entry.ID, &entry.RoomID, &entry.ExpectedFiles, &published, &pinned, &entry.ExpiresAt, &deleteAfterDownload, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entry.Published = published != 0
		entry.Pinned = pinned != 0
		entry.DeleteAfterDownload = deleteAfterDownload != 0
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *Store) GetEntry(ctx context.Context, roomID, entryID string) (Entry, error) {
	var entry Entry
	var published, pinned, deleteAfterDownload int
	err := s.db.QueryRowContext(ctx, `SELECT id,room_id,expected_files,published,pinned,expires_at,delete_after_download,created_at FROM entries WHERE room_id=? AND id=?`, roomID, entryID).Scan(&entry.ID, &entry.RoomID, &entry.ExpectedFiles, &published, &pinned, &entry.ExpiresAt, &deleteAfterDownload, &entry.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	entry.Published = published != 0
	entry.Pinned = pinned != 0
	entry.DeleteAfterDownload = deleteAfterDownload != 0
	return entry, err
}

func (s *Store) CreateEntry(ctx context.Context, roomID string, entry Entry, item *Item, maxItems int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var encrypted int
	var roomKeyID string
	if err := tx.QueryRowContext(ctx, `SELECT encrypted,key_id FROM rooms WHERE id=?`, roomID).Scan(&encrypted, &roomKeyID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT id FROM entries WHERE room_id=?
		UNION SELECT id FROM items WHERE room_id=?
		UNION SELECT entry_id FROM files WHERE room_id=?)`, roomID, roomID, roomID).Scan(&count); err != nil {
		return err
	}
	if count >= maxItems {
		return ErrLimit
	}
	if item != nil && ((encrypted != 0) != (item.Kind == "encrypted") || (encrypted != 0 && roomKeyID != item.KeyID)) {
		return ErrConflict
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entry.RoomID, entry.CreatedAt = roomID, now
	_, err = tx.ExecContext(ctx, `INSERT INTO entries(id,room_id,expected_files,published,pinned,expires_at,delete_after_download,created_at) VALUES(?,?,?,?,?,?,?,?)`, entry.ID, roomID, entry.ExpectedFiles, false, false, entry.ExpiresAt, entry.DeleteAfterDownload, now)
	if err != nil {
		if isConstraint(err) {
			return ErrConflict
		}
		return err
	}
	if item != nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO items(id,room_id,kind,content,alias,ciphertext,iv,key_id,version,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, entry.ID, roomID, item.Kind, item.Content, item.Alias, item.Ciphertext, item.IV, item.KeyID, item.Version, now)
		if err != nil {
			if isConstraint(err) {
				return ErrConflict
			}
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CommitEntry(ctx context.Context, roomID, entryID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var expected, published int
	if err := tx.QueryRowContext(ctx, `SELECT expected_files,published FROM entries WHERE room_id=? AND id=?`, roomID, entryID).Scan(&expected, &published); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if published != 0 {
		return nil
	}
	var completed, active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE room_id=? AND entry_id=?`, roomID, entryID).Scan(&completed); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads WHERE room_id=? AND entry_id=?`, roomID, entryID).Scan(&active); err != nil {
		return err
	}
	if completed != expected || active != 0 {
		return ErrConflict
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE entries SET published=1 WHERE room_id=? AND id=?`, roomID, entryID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET updated_at=? WHERE id=?`, now, roomID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) EntryForFile(ctx context.Context, roomID, fileID string) (Entry, error) {
	var entry Entry
	var published, pinned, deleteAfterDownload int
	err := s.db.QueryRowContext(ctx, `SELECT e.id,e.room_id,e.expected_files,e.published,e.pinned,e.expires_at,e.delete_after_download,e.created_at
		FROM files f JOIN entries e ON e.room_id=f.room_id AND e.id=f.entry_id
		WHERE f.room_id=? AND f.id=?`, roomID, fileID).Scan(&entry.ID, &entry.RoomID, &entry.ExpectedFiles, &published, &pinned, &entry.ExpiresAt, &deleteAfterDownload, &entry.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	entry.Published = published != 0
	entry.Pinned = pinned != 0
	entry.DeleteAfterDownload = deleteAfterDownload != 0
	return entry, err
}

func (s *Store) PinEntry(ctx context.Context, roomID, entryID string, pinned bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT id FROM items WHERE room_id=? AND id=? UNION SELECT entry_id FROM files WHERE room_id=? AND entry_id=?)`, roomID, entryID, roomID, entryID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	var fileCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE room_id=? AND entry_id=?`, roomID, entryID).Scan(&fileCount); err != nil {
		return err
	}
	var createdAt string
	if err := tx.QueryRowContext(ctx, `SELECT MIN(created_at) FROM (SELECT created_at FROM items WHERE room_id=? AND id=? UNION ALL SELECT created_at FROM files WHERE room_id=? AND entry_id=?)`, roomID, entryID, roomID, entryID).Scan(&createdAt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO entries(id,room_id,expected_files,published,pinned,created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(room_id,id) DO UPDATE SET pinned=excluded.pinned`, entryID, roomID, fileCount, true, pinned, createdAt)
	if err != nil {
		return err
	}
	return tx.Commit()
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
	if upload.EntryID == "" {
		upload.EntryID = upload.FileID
	}
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
	var expectedFiles, published int
	err = tx.QueryRowContext(ctx, `SELECT expected_files,published FROM entries WHERE room_id=? AND id=?`, upload.RoomID, upload.EntryID).Scan(&expectedFiles, &published)
	if err == nil {
		if published != 0 || upload.EntryIndex >= expectedFiles {
			return ErrConflict
		}
		var duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM uploads WHERE room_id=? AND entry_id=? AND entry_index=?)+
			(SELECT COUNT(*) FROM files WHERE room_id=? AND entry_id=? AND entry_index=?)`, upload.RoomID, upload.EntryID, upload.EntryIndex, upload.RoomID, upload.EntryID, upload.EntryIndex).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate != 0 {
			return ErrConflict
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
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
	_, err = tx.ExecContext(ctx, `INSERT INTO uploads(id,room_id,file_id,entry_id,entry_index,name,mime_type,alias,size,chunk_size,chunk_count,plain_chunk_size,encrypted,key_id,token_hash,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, upload.ID, upload.RoomID, upload.FileID, upload.EntryID, upload.EntryIndex, upload.Name, upload.MIMEType, upload.Alias, upload.Size, upload.ChunkSize, upload.ChunkCount, upload.PlainChunkSize, upload.Encrypted, upload.KeyID, upload.TokenHash, upload.ExpiresAt, now)
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
	err := s.db.QueryRowContext(ctx, `SELECT id,room_id,file_id,entry_id,entry_index,name,mime_type,alias,size,chunk_size,chunk_count,plain_chunk_size,encrypted,key_id,token_hash,expires_at,created_at FROM uploads WHERE id=? AND room_id=?`, uploadID, roomID).Scan(&upload.ID, &upload.RoomID, &upload.FileID, &upload.EntryID, &upload.EntryIndex, &upload.Name, &upload.MIMEType, &upload.Alias, &upload.Size, &upload.ChunkSize, &upload.ChunkCount, &upload.PlainChunkSize, &encrypted, &upload.KeyID, &upload.TokenHash, &upload.ExpiresAt, &upload.CreatedAt)
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
	err = tx.QueryRowContext(ctx, `SELECT file_id,room_id,entry_id,entry_index,name,mime_type,alias,size,chunk_count,encrypted,key_id,plain_chunk_size FROM uploads WHERE id=? AND room_id=?`, uploadID, roomID).Scan(&file.ID, &file.RoomID, &file.EntryID, &file.EntryIndex, &file.Name, &file.MIMEType, &file.Alias, &file.Size, &chunkCount, &encrypted, &file.KeyID, &file.ChunkSize)
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO files(id,room_id,entry_id,entry_index,name,mime_type,alias,size,encrypted,key_id,manifest_ciphertext,manifest_iv,version,chunk_size,chunk_count,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, file.ID, file.RoomID, file.EntryID, file.EntryIndex, file.Name, file.MIMEType, file.Alias, file.Size, file.Encrypted, file.KeyID, file.ManifestCiphertext, file.ManifestIV, file.Version, file.ChunkSize, file.ChunkCount, file.CreatedAt); err != nil {
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := s.db.QueryRowContext(ctx, `SELECT f.id,f.room_id,f.entry_id,f.entry_index,f.name,f.mime_type,f.alias,f.size,f.encrypted,f.key_id,f.manifest_ciphertext,f.manifest_iv,f.version,f.chunk_size,f.chunk_count,f.created_at
		FROM files f LEFT JOIN entries e ON e.room_id=f.room_id AND e.id=f.entry_id
		WHERE f.id=? AND f.room_id=? AND (e.id IS NULL OR (e.published=1 AND (e.expires_at='' OR e.expires_at>?)))`, fileID, roomID, now).Scan(&file.ID, &file.RoomID, &file.EntryID, &file.EntryIndex, &file.Name, &file.MIMEType, &file.Alias, &file.Size, &encrypted, &file.KeyID, &file.ManifestCiphertext, &file.ManifestIV, &file.Version, &file.ChunkSize, &file.ChunkCount, &file.CreatedAt)
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

// DeleteEntry removes a text item, completed files, active uploads and entry
// metadata. Filesystem payloads are returned for cleanup after commit.
func (s *Store) DeleteEntry(ctx context.Context, roomID, entryID string) (DeletedObjects, error) {
	return s.deleteEntry(ctx, roomID, entryID, true)
}

func (s *Store) deleteEntry(ctx context.Context, roomID, entryID string, touchRoom bool) (DeletedObjects, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeletedObjects{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM files WHERE room_id=? AND entry_id=?`, roomID, entryID)
	if err != nil {
		return DeletedObjects{}, err
	}
	var deleted DeletedObjects
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return DeletedObjects{}, err
		}
		deleted.FileIDs = append(deleted.FileIDs, id)
	}
	if err := rows.Close(); err != nil {
		return DeletedObjects{}, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id FROM uploads WHERE room_id=? AND entry_id=?`, roomID, entryID)
	if err != nil {
		return DeletedObjects{}, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return DeletedObjects{}, err
		}
		deleted.UploadIDs = append(deleted.UploadIDs, id)
	}
	if err := rows.Close(); err != nil {
		return DeletedObjects{}, err
	}
	itemResult, err := tx.ExecContext(ctx, `DELETE FROM items WHERE room_id=? AND id=?`, roomID, entryID)
	if err != nil {
		return DeletedObjects{}, err
	}
	fileResult, err := tx.ExecContext(ctx, `DELETE FROM files WHERE room_id=? AND entry_id=?`, roomID, entryID)
	if err != nil {
		return DeletedObjects{}, err
	}
	uploadResult, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE room_id=? AND entry_id=?`, roomID, entryID)
	if err != nil {
		return DeletedObjects{}, err
	}
	entryResult, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE room_id=? AND id=?`, roomID, entryID)
	if err != nil {
		return DeletedObjects{}, err
	}
	itemsDeleted, _ := itemResult.RowsAffected()
	filesDeleted, _ := fileResult.RowsAffected()
	uploadsDeleted, _ := uploadResult.RowsAffected()
	entriesDeleted, _ := entryResult.RowsAffected()
	if itemsDeleted+filesDeleted+uploadsDeleted+entriesDeleted == 0 {
		return DeletedObjects{}, ErrNotFound
	}
	if touchRoom {
		if _, err := tx.ExecContext(ctx, `UPDATE rooms SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), roomID); err != nil {
			return DeletedObjects{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return DeletedObjects{}, err
	}
	return deleted, nil
}

func (s *Store) ClearRoom(ctx context.Context, roomID string) (DeletedObjects, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeletedObjects{}, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms WHERE id=?`, roomID).Scan(&exists); err != nil {
		return DeletedObjects{}, err
	}
	if exists == 0 {
		return DeletedObjects{}, ErrNotFound
	}
	deleted := DeletedObjects{}
	for query, target := range map[string]*[]string{
		`SELECT id FROM files WHERE room_id=?`:   &deleted.FileIDs,
		`SELECT id FROM uploads WHERE room_id=?`: &deleted.UploadIDs,
	} {
		rows, err := tx.QueryContext(ctx, query, roomID)
		if err != nil {
			return DeletedObjects{}, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return DeletedObjects{}, err
			}
			*target = append(*target, id)
		}
		if err := rows.Close(); err != nil {
			return DeletedObjects{}, err
		}
	}
	for _, query := range []string{`DELETE FROM items WHERE room_id=?`, `DELETE FROM files WHERE room_id=?`, `DELETE FROM uploads WHERE room_id=?`, `DELETE FROM entries WHERE room_id=?`} {
		if _, err := tx.ExecContext(ctx, query, roomID); err != nil {
			return DeletedObjects{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), roomID); err != nil {
		return DeletedObjects{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeletedObjects{}, err
	}
	return deleted, nil
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

func (s *Store) DeleteExpiredEntries(ctx context.Context, before time.Time) (DeletedObjects, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT room_id,id FROM entries WHERE expires_at<>'' AND expires_at<=?`, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return DeletedObjects{}, err
	}
	type ref struct{ roomID, entryID string }
	var refs []ref
	for rows.Next() {
		var value ref
		if err := rows.Scan(&value.roomID, &value.entryID); err != nil {
			rows.Close()
			return DeletedObjects{}, err
		}
		refs = append(refs, value)
	}
	if err := rows.Close(); err != nil {
		return DeletedObjects{}, err
	}
	var deleted DeletedObjects
	for _, value := range refs {
		objects, err := s.deleteEntry(ctx, value.roomID, value.entryID, false)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return deleted, err
		}
		deleted.FileIDs = append(deleted.FileIDs, objects.FileIDs...)
		deleted.UploadIDs = append(deleted.UploadIDs, objects.UploadIDs...)
	}
	return deleted, nil
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

func (s *Store) DeleteExpiredRooms(ctx context.Context, now time.Time, defaultTTL time.Duration) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,ttl_seconds,updated_at FROM rooms`)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id      string
		ttl     int64
		updated string
	}
	var candidates []candidate
	for rows.Next() {
		var value candidate
		if err := rows.Scan(&value.id, &value.ttl, &value.updated); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, value)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var deleted int64
	for _, value := range candidates {
		ttl := defaultTTL
		if value.ttl > 0 {
			ttl = time.Duration(value.ttl) * time.Second
		}
		if ttl == 0 {
			continue
		}
		updated, err := time.Parse(time.RFC3339Nano, value.updated)
		if err != nil || now.Sub(updated) < ttl {
			continue
		}
		result, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE id=?`, value.id)
		if err != nil {
			return deleted, err
		}
		n, _ := result.RowsAffected()
		deleted += n
	}
	return deleted, nil
}

func (s *Store) ListRooms(ctx context.Context) ([]Room, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,encrypted,key_id,write_protected,write_hash,ttl_seconds,created_at,updated_at FROM rooms ORDER BY updated_at DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]Room, 0)
	for rows.Next() {
		var room Room
		var encrypted, writeProtected int
		if err := rows.Scan(&room.ID, &encrypted, &room.KeyID, &writeProtected, &room.WriteHash, &room.TTLSeconds, &room.CreatedAt, &room.UpdatedAt); err != nil {
			return nil, err
		}
		room.Encrypted = encrypted != 0
		room.WriteProtected = writeProtected != 0
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *Store) DeleteRoom(ctx context.Context, roomID string) (DeletedObjects, error) {
	deleted, err := s.ClearRoom(ctx, roomID)
	if err != nil {
		return DeletedObjects{}, err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM rooms WHERE id=?`, roomID)
	if err != nil {
		return DeletedObjects{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return DeletedObjects{}, ErrNotFound
	}
	return deleted, nil
}

func (s *Store) Stats(ctx context.Context) (Statistics, error) {
	var stats Statistics
	queries := []struct {
		query  string
		target *int64
	}{
		{`SELECT COUNT(*) FROM rooms`, &stats.Rooms},
		{`SELECT COUNT(*) FROM short_links`, &stats.ShortLinks},
		{`SELECT COUNT(*) FROM items`, &stats.Items},
		{`SELECT COUNT(*) FROM entries`, &stats.Entries},
		{`SELECT COUNT(*) FROM files`, &stats.Files},
		{`SELECT COALESCE(SUM(size),0) FROM files`, &stats.FileBytes},
		{`SELECT COUNT(*) FROM uploads`, &stats.ActiveUploads},
		{`SELECT COALESCE(SUM(size),0) FROM uploads`, &stats.ReservedBytes},
	}
	for _, value := range queries {
		if err := s.db.QueryRowContext(ctx, value.query).Scan(value.target); err != nil {
			return Statistics{}, err
		}
	}
	return stats, nil
}

func (s *Store) RunCleanup(ctx context.Context, ttl, interval time.Duration) {
	_, _ = s.DeleteExpiredRooms(ctx, time.Now(), ttl)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.DeleteExpiredRooms(ctx, time.Now(), ttl)
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
