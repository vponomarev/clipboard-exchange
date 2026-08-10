package admin

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vponomarev/clipboard-exchange/internal/store"
)

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "source", "exchange.db")
	sourceFiles := filepath.Join(root, "source", "files")
	if err := os.MkdirAll(filepath.Join(sourceFiles, "objects"), 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(sourceDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoom(context.Background(), store.Room{ID: "backed-up"}, 10); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sourceFiles, "objects", "marker")
	if err := os.WriteFile(marker, []byte("payload"), 0o640); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "exchange.tar.gz")
	if err := Run([]string{"backup", "--database", sourceDB, "--files-dir", sourceFiles, "--output", archive}, "test-version"); err != nil {
		t.Fatal(err)
	}
	destinationDB := filepath.Join(root, "restored", "exchange.db")
	destinationFiles := filepath.Join(root, "restored", "files")
	if err := Run([]string{"restore", "--database", destinationDB, "--files-dir", destinationFiles, "--input", archive, "--force"}, "test-version"); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(destinationDB)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.GetRoom(context.Background(), "backed-up"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destinationFiles, "objects", "marker"))
	if err != nil || string(content) != "payload" {
		t.Fatalf("restored file: %q err=%v", content, err)
	}
}

func TestExtractArchiveRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "unsafe.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zipper := gzip.NewWriter(file)
	writer := tar.NewWriter(zipper)
	if err := writer.WriteHeader(&tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Size: 1, Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(archive, t.TempDir()); err == nil {
		t.Fatal("path traversal archive was accepted")
	}
}
