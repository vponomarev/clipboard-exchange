package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRoomAndExactMultilineText(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateRoom(ctx, Room{ID: "home", WriteHash: "hash-one"}, 10); err != nil {
		t.Fatal(err)
	}
	want := "  printf '%s\\n' \"$PATH\"  \n\tsecond line\n"
	if err := s.AddItem(ctx, "home", Item{ID: "123e4567-e89b-12d3-a456-426614174000", Kind: "text", Content: want, Alias: "Вася"}, 10); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListItems(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Content != want || items[0].Alias != "Вася" {
		t.Fatalf("text changed: %#v", items)
	}
	if err := s.DeleteItem(ctx, "home", items[0].ID); err != nil {
		t.Fatal(err)
	}
	items, _ = s.ListItems(ctx, "home")
	if len(items) != 0 {
		t.Fatal("item was not deleted")
	}
}

func TestWriteAuthorizationAndRotation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateRoom(ctx, Room{ID: "rights", WriteProtected: true, WriteHash: "hash-one"}, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeWrite(ctx, "rights", "wrong"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong hash: %v", err)
	}
	if err := s.AuthorizeWrite(ctx, "rights", "hash-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.RotateWriteHash(ctx, "rights", "hash-one", "hash-two"); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeWrite(ctx, "rights", "hash-one"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("old hash remains valid: %v", err)
	}
	if err := s.AuthorizeWrite(ctx, "rights", "hash-two"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenWriteRoomDoesNotUseCapability(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateRoom(ctx, Room{ID: "open"}, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeWrite(ctx, "open", ""); err != nil {
		t.Fatalf("open room rejected mutation: %v", err)
	}
	if err := s.RotateWriteHash(ctx, "open", "", "hash"); !errors.Is(err, ErrConflict) {
		t.Fatalf("open room allowed capability rotation: %v", err)
	}
}

func TestVersionTwoRoomsMigrateAsWriteProtected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE rooms (
		id TEXT PRIMARY KEY,
		encrypted INTEGER NOT NULL,
		key_id TEXT NOT NULL DEFAULT '',
		write_hash TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	); INSERT INTO rooms VALUES ('existing',0,'','hash','created','updated'); PRAGMA user_version=2`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	room, err := s.GetRoom(context.Background(), "existing")
	if err != nil || !room.WriteProtected {
		t.Fatalf("migrated room: %#v err=%v", room, err)
	}
}

func TestVersionThreeFilesMigrateToIndependentEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE rooms (
		id TEXT PRIMARY KEY, encrypted INTEGER NOT NULL, key_id TEXT NOT NULL DEFAULT '',
		write_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		write_protected INTEGER NOT NULL DEFAULT 1
	);
	CREATE TABLE uploads (
		id TEXT PRIMARY KEY, room_id TEXT NOT NULL, file_id TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL, mime_type TEXT NOT NULL, alias TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL,
		chunk_size INTEGER NOT NULL, chunk_count INTEGER NOT NULL, plain_chunk_size INTEGER NOT NULL,
		encrypted INTEGER NOT NULL DEFAULT 0, key_id TEXT NOT NULL DEFAULT '', token_hash TEXT NOT NULL,
		expires_at TEXT NOT NULL, created_at TEXT NOT NULL
	);
	CREATE TABLE files (
		id TEXT PRIMARY KEY, room_id TEXT NOT NULL, name TEXT NOT NULL, mime_type TEXT NOT NULL,
		alias TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL, encrypted INTEGER NOT NULL DEFAULT 0,
		key_id TEXT NOT NULL DEFAULT '', manifest_ciphertext TEXT NOT NULL DEFAULT '', manifest_iv TEXT NOT NULL DEFAULT '',
		version INTEGER NOT NULL DEFAULT 0, chunk_size INTEGER NOT NULL DEFAULT 0, chunk_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL
	);
	INSERT INTO rooms VALUES ('room',0,'','hash','now','now',1);
	INSERT INTO uploads VALUES ('upload','room','pending','pending.txt','text/plain','',1,1,1,1,0,'','token','expires','now');
	INSERT INTO files VALUES ('existing','room','old.txt','text/plain','',1,0,'','','',0,1,1,'now');
	PRAGMA user_version=3`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	upload, err := s.GetUpload(context.Background(), "room", "upload")
	if err != nil || upload.EntryID != "pending" || upload.EntryIndex != 0 {
		t.Fatalf("migrated upload: %#v err=%v", upload, err)
	}
	files, err := s.ListFiles(context.Background(), "room")
	if err != nil || len(files) != 1 || files[0].EntryID != "existing" || files[0].EntryIndex != 0 {
		t.Fatalf("migrated files: %#v err=%v", files, err)
	}
}

func TestLegacyDatabaseRequiresCleanStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE rooms (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("legacy database was opened without an explicit migration")
	}
}

func TestEncryptedRoomRejectsWrongKeyAndPlaintext(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateRoom(ctx, Room{ID: "secret", Encrypted: true, KeyID: "key-one", WriteHash: "hash"}, 10); err != nil {
		t.Fatal(err)
	}
	base := Item{ID: "123e4567-e89b-12d3-a456-426614174000", Kind: "encrypted", Ciphertext: "cipher", IV: "nonce", KeyID: "wrong", Version: 1}
	if err := s.AddItem(ctx, "secret", base, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong key: %v", err)
	}
	base.Kind, base.Content, base.KeyID = "text", "leak", ""
	if err := s.AddItem(ctx, "secret", base, 10); !errors.Is(err, ErrConflict) {
		t.Fatalf("plaintext accepted: %v", err)
	}
}

func TestLimitsAndCleanup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateRoom(ctx, Room{ID: "one", WriteHash: "hash"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRoom(ctx, Room{ID: "two", WriteHash: "hash"}, 1); !errors.Is(err, ErrLimit) {
		t.Fatalf("room limit: %v", err)
	}
	first := Item{ID: "123e4567-e89b-12d3-a456-426614174000", Kind: "text", Content: "one"}
	if err := s.AddItem(ctx, "one", first, 1); err != nil {
		t.Fatal(err)
	}
	first.ID = "123e4567-e89b-12d3-a456-426614174001"
	if err := s.AddItem(ctx, "one", first, 1); !errors.Is(err, ErrLimit) {
		t.Fatalf("item limit: %v", err)
	}
	n, err := s.DeleteInactive(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("cleanup: n=%d err=%v", n, err)
	}
	if _, err := s.GetRoom(ctx, "one"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("room remains: %v", err)
	}
}

func TestUploadReservationsEnforceRoomQuota(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateRoom(ctx, Room{ID: "files", WriteHash: "hash"}, 10); err != nil {
		t.Fatal(err)
	}
	first := Upload{ID: "123e4567-e89b-12d3-a456-426614174000", RoomID: "files", FileID: "123e4567-e89b-12d3-a456-426614174001", Name: "one", MIMEType: "application/octet-stream", Size: 7, ChunkSize: 4, ChunkCount: 2, TokenHash: "token", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	if err := s.CreateUpload(ctx, first, 10, 10); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = "123e4567-e89b-12d3-a456-426614174002"
	second.FileID = "123e4567-e89b-12d3-a456-426614174003"
	second.Size = 4
	if err := s.CreateUpload(ctx, second, 10, 10); !errors.Is(err, ErrLimit) {
		t.Fatalf("quota reservation was bypassed: %v", err)
	}
	if err := s.RecordChunk(ctx, first.ID, 0, 4, "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordChunk(ctx, first.ID, 1, 3, "b", ""); err != nil {
		t.Fatal(err)
	}
	file, err := s.CompleteUpload(ctx, "files", first.ID, "", "", 0)
	if err != nil || file.Size != 7 {
		t.Fatalf("complete upload: file=%#v err=%v", file, err)
	}
	if err := s.CreateUpload(ctx, second, 10, 10); !errors.Is(err, ErrLimit) {
		t.Fatalf("completed file was not counted in quota: %v", err)
	}
}
