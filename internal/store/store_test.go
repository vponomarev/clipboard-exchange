package store

import (
	"context"
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
	if err := s.CreateRoom(ctx, Room{ID: "home"}, 10); err != nil {
		t.Fatal(err)
	}
	want := "  printf '%s\\n' \"$PATH\"  \n\tsecond line\n"
	if err := s.AddItem(ctx, "home", Item{ID: "123e4567-e89b-12d3-a456-426614174000", Kind: "text", Content: want}, 10); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListItems(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Content != want {
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

func TestEncryptedRoomRejectsWrongKeyAndPlaintext(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateRoom(ctx, Room{ID: "secret", Encrypted: true, KeyID: "key-one"}, 10); err != nil {
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
	if err := s.CreateRoom(ctx, Room{ID: "one"}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRoom(ctx, Room{ID: "two"}, 1); !errors.Is(err, ErrLimit) {
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
