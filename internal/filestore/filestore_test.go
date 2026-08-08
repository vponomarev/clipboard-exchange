package filestore

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

const (
	testUploadID = "123e4567-e89b-12d3-a456-426614174000"
	testFileID   = "123e4567-e89b-12d3-a456-426614174001"
)

func TestChunksAreIdempotentAndAssembleExactly(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range []string{"abc", "de"} {
		size, digest, _, err := store.WriteChunk(testUploadID, index, strings.NewReader(value), int64(len(value)))
		if err != nil || size != int64(len(value)) || len(digest) != 64 {
			t.Fatalf("chunk %d: size=%d digest=%q err=%v", index, size, digest, err)
		}
	}
	if _, _, _, err := store.WriteChunk(testUploadID, 0, strings.NewReader("abc"), 3); err != nil {
		t.Fatalf("idempotent chunk: %v", err)
	}
	if _, _, _, err := store.WriteChunk(testUploadID, 0, strings.NewReader("ABC"), 3); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting chunk: %v", err)
	}
	if _, err := store.Assemble(testUploadID, testFileID, 2, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Assemble(testUploadID, testFileID, 2, 5); err != nil {
		t.Fatalf("idempotent assembly after crash: %v", err)
	}
	object, err := store.OpenObject(testFileID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object)
	object.Close()
	if err != nil || string(data) != "abcde" {
		t.Fatalf("assembled data=%q err=%v", data, err)
	}
}

func TestRejectsPathTraversalAndWrongSize(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.WriteChunk("../../outside", 0, strings.NewReader("x"), 1); err == nil {
		t.Fatal("path traversal upload ID accepted")
	}
	if _, _, _, err := store.WriteChunk(testUploadID, 0, strings.NewReader("too long"), 3); err == nil {
		t.Fatal("oversized chunk accepted")
	}
	if _, err := os.Stat(root + string(os.PathSeparator) + "outside"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected path created: %v", err)
	}
}

func TestReconcileRemovesOnlyOrphans(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.WriteChunk(testUploadID, 0, strings.NewReader("abc"), 3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Assemble(testUploadID, testFileID, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.Reconcile(map[string]bool{testUploadID: true}, map[string]bool{testFileID: true}); err != nil {
		t.Fatal(err)
	}
	object, err := store.OpenObject(testFileID)
	if err != nil {
		t.Fatalf("active object removed: %v", err)
	}
	object.Close()
	if err := store.Reconcile(map[string]bool{}, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenObject(testFileID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan object remains: %v", err)
	}
}
