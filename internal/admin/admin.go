package admin

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vponomarev/clipboard-exchange/internal/config"
	"github.com/vponomarev/clipboard-exchange/internal/filestore"
	"github.com/vponomarev/clipboard-exchange/internal/store"
)

var commands = map[string]bool{"status": true, "rooms": true, "backup": true, "restore": true, "storage": true}

type manifest struct {
	Format    int    `json:"format"`
	Version   string `json:"version"`
	CreatedAt string `json:"createdAt"`
}

func IsCommand(value string) bool { return commands[value] }

func Run(args []string, version string) error {
	if len(args) == 0 {
		return errors.New("admin command is required")
	}
	switch args[0] {
	case "status":
		return runStatus(args[1:], version)
	case "rooms":
		return runRooms(args[1:])
	case "backup":
		return runBackup(args[1:], version)
	case "restore":
		return runRestore(args[1:])
	case "storage":
		return runStorage(args[1:])
	default:
		return fmt.Errorf("unknown admin command %q", args[0])
	}
}

func baseConfig(name string) (*config.Config, *flag.FlagSet, error) {
	cfg := config.Default()
	if err := config.ApplyEnvironment(&cfg); err != nil {
		return nil, nil, err
	}
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.StringVar(&cfg.DatabasePath, "database", cfg.DatabasePath, "SQLite database path")
	set.StringVar(&cfg.FilesDir, "files-dir", cfg.FilesDir, "file storage directory")
	return &cfg, set, nil
}

func open(cfg config.Config) (*store.Store, *filestore.Store, error) {
	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return nil, nil, err
	}
	files, err := filestore.Open(cfg.FilesDir)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, files, nil
}

func runStatus(args []string, version string) error {
	cfg, set, err := baseConfig("status")
	if err != nil {
		return err
	}
	jsonOutput := set.Bool("json", false, "write JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	db, files, err := open(*cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	stats, err := db.Stats(context.Background())
	if err != nil {
		return err
	}
	available, total, _ := files.DiskSpace()
	result := map[string]any{"version": version, "database": cfg.DatabasePath, "filesDir": files.Root(), "rooms": stats.Rooms, "items": stats.Items, "entries": stats.Entries, "files": stats.Files, "fileBytes": stats.FileBytes, "activeUploads": stats.ActiveUploads, "reservedBytes": stats.ReservedBytes, "diskAvailableBytes": available, "diskTotalBytes": total}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	fmt.Printf("version: %s\nrooms: %d\nentries: %d\ntext items: %d\nfiles: %d (%d bytes)\nactive uploads: %d (%d reserved bytes)\ndisk available: %d of %d bytes\n", version, stats.Rooms, stats.Entries, stats.Items, stats.Files, stats.FileBytes, stats.ActiveUploads, stats.ReservedBytes, available, total)
	return nil
}

func runRooms(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rooms list | rooms purge ROOM")
	}
	subcommand := args[0]
	cfg, set, err := baseConfig("rooms " + subcommand)
	if err != nil {
		return err
	}
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	db, files, err := open(*cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	switch subcommand {
	case "list":
		rooms, err := db.ListRooms(context.Background())
		if err != nil {
			return err
		}
		for _, room := range rooms {
			fmt.Printf("%s\tencrypted=%t\twriteProtected=%t\tttlSeconds=%d\tupdated=%s\n", room.ID, room.Encrypted, room.WriteProtected, room.TTLSeconds, room.UpdatedAt)
		}
		return nil
	case "purge":
		if set.NArg() != 1 {
			return errors.New("usage: rooms purge [flags] ROOM")
		}
		deleted, err := db.DeleteRoom(context.Background(), set.Arg(0))
		if err != nil {
			return err
		}
		removeDeleted(files, deleted)
		fmt.Printf("purged room %s\n", set.Arg(0))
		return nil
	default:
		return fmt.Errorf("unknown rooms command %q", subcommand)
	}
}

func runStorage(args []string) error {
	if len(args) == 0 || args[0] != "reconcile" {
		return errors.New("usage: storage reconcile")
	}
	cfg, set, err := baseConfig("storage reconcile")
	if err != nil {
		return err
	}
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("unexpected storage reconcile arguments")
	}
	db, files, err := open(*cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	uploads, objects, err := db.StorageReferences(context.Background())
	if err != nil {
		return err
	}
	if err := files.Reconcile(uploads, objects); err != nil {
		return err
	}
	fmt.Printf("storage reconciled: %d uploads, %d objects referenced\n", len(uploads), len(objects))
	return nil
}

func runBackup(args []string, version string) error {
	cfg, set, err := baseConfig("backup")
	if err != nil {
		return err
	}
	output := set.String("output", "", "backup tar.gz path")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *output == "" || set.NArg() != 0 {
		return errors.New("usage: backup [--database FILE] [--files-dir DIR] --output FILE")
	}
	absOutput, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	db, files, err := open(*cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	temp, err := os.MkdirTemp("", "clipboard-exchange-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	snapshot := filepath.Join(temp, "database.db")
	if err := db.Snapshot(context.Background(), snapshot); err != nil {
		return err
	}
	manifestPath := filepath.Join(temp, "manifest.json")
	manifestFile, err := os.OpenFile(manifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(manifestFile).Encode(manifest{Format: 1, Version: version, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	closeErr := manifestFile.Close()
	if encodeErr != nil || closeErr != nil {
		return errors.Join(encodeErr, closeErr)
	}
	if err := writeArchive(absOutput, map[string]string{"manifest.json": manifestPath, "database.db": snapshot}, files.Root()); err != nil {
		return err
	}
	fmt.Printf("backup written: %s\n", absOutput)
	return nil
}

func runRestore(args []string) error {
	cfg, set, err := baseConfig("restore")
	if err != nil {
		return err
	}
	input := set.String("input", "", "backup tar.gz path")
	force := set.Bool("force", false, "replace destination data")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *input == "" || !*force || set.NArg() != 0 {
		return errors.New("restore requires --input FILE --force and must run while the service is stopped")
	}
	temp, err := os.MkdirTemp("", "clipboard-exchange-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := extractArchive(*input, temp); err != nil {
		return err
	}
	var meta manifest
	manifestFile, err := os.Open(filepath.Join(temp, "manifest.json"))
	if err != nil {
		return err
	}
	decodeErr := json.NewDecoder(manifestFile).Decode(&meta)
	manifestFile.Close()
	if decodeErr != nil || meta.Format != 1 {
		return errors.New("unsupported or invalid backup manifest")
	}
	validation, err := store.Open(filepath.Join(temp, "database.db"))
	if err != nil {
		return fmt.Errorf("validate backup database: %w", err)
	}
	validation.Close()
	if err := os.MkdirAll(filepath.Dir(cfg.DatabasePath), 0o750); err != nil {
		return err
	}
	for _, path := range []string{cfg.DatabasePath, cfg.DatabasePath + "-wal", cfg.DatabasePath + "-shm"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.RemoveAll(cfg.FilesDir); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(temp, "database.db"), cfg.DatabasePath, 0o660); err != nil {
		return err
	}
	// Archive modes can carry a read-only attribute on Windows. SQLite must be
	// able to create its WAL and SHM files next to the restored database.
	if err := os.Chmod(cfg.DatabasePath, 0o660); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(temp, "storage"), cfg.FilesDir); err != nil {
		return err
	}
	restored, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("verify restored database: %w", err)
	}
	if err := restored.Close(); err != nil {
		return err
	}
	fmt.Printf("backup restored from %s; start the service and verify /readyz\n", *input)
	return nil
}

func removeDeleted(files *filestore.Store, deleted store.DeletedObjects) {
	for _, id := range deleted.FileIDs {
		_ = files.RemoveObject(id)
	}
	for _, id := range deleted.UploadIDs {
		_ = files.RemoveUpload(id)
	}
}

func writeArchive(output string, fixed map[string]string, storageRoot string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	writeFile := func(name, source string, info fs.FileInfo) error {
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			input, err := os.Open(source)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tarWriter, input)
			closeErr := input.Close()
			return errors.Join(copyErr, closeErr)
		}
		return nil
	}
	for name, source := range fixed {
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		if err := writeFile(name, source, info); err != nil {
			return err
		}
	}
	err = filepath.Walk(storageRoot, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(storageRoot, path)
		if err != nil || relative == "." {
			return err
		}
		return writeFile(filepath.Join("storage", relative), path, info)
	})
	closeErr := tarWriter.Close()
	gzipErr := gzipWriter.Close()
	fileErr := file.Close()
	if err != nil || closeErr != nil || gzipErr != nil || fileErr != nil {
		_ = os.Remove(output)
		return errors.Join(err, closeErr, gzipErr, fileErr)
	}
	return nil
}

func extractArchive(input, destination string) error {
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("backup contains an unsafe path")
		}
		target := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(header.Mode)&0o660)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(output, tarReader, header.Size)
			closeErr := output.Close()
			if copyErr != nil || closeErr != nil {
				return errors.Join(copyErr, closeErr)
			}
		default:
			return errors.New("backup contains unsupported filesystem entry")
		}
	}
}

func copyTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !info.Mode().IsRegular() {
			return errors.New("backup storage contains unsupported entry")
		}
		return copyFile(path, target, 0o640)
	})
}

func copyFile(source, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	return errors.Join(copyErr, syncErr, closeErr)
}
