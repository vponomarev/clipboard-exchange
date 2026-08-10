package filestore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var ErrConflict = errors.New("storage object conflict")
var objectIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

type Store struct {
	root string
}

func (s *Store) Root() string { return s.root }

func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{filepath.Join(abs, "uploads"), filepath.Join(abs, "objects")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create file storage: %w", err)
		}
	}
	return &Store{root: abs}, nil
}

func (s *Store) WriteChunk(uploadID string, index int, body io.Reader, expected int64) (int64, string, []byte, error) {
	if !objectIDPattern.MatchString(uploadID) || index < 0 || expected < 0 {
		return 0, "", nil, errors.New("invalid chunk path")
	}
	dir := filepath.Join(s.root, "uploads", uploadID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, "", nil, err
	}
	tmp, err := os.CreateTemp(dir, ".chunk-*")
	if err != nil {
		return 0, "", nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(body, expected+1))
	if copyErr == nil && n != expected {
		copyErr = fmt.Errorf("chunk size is %d, expected %d", n, expected)
	}
	if copyErr == nil {
		copyErr = tmp.Sync()
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return n, "", nil, copyErr
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	prefix, err := readPrefix(tmpName, 12)
	if err != nil {
		return 0, "", nil, err
	}
	target := filepath.Join(dir, strconv.Itoa(index))
	if err := os.Link(tmpName, target); err == nil {
		return n, digest, prefix, nil
	} else if errors.Is(err, os.ErrExist) {
		existing, hashErr := digestFile(target)
		if hashErr != nil {
			return 0, "", nil, hashErr
		}
		if existing != digest {
			return 0, "", nil, ErrConflict
		}
		return n, digest, prefix, nil
	} else {
		return 0, "", nil, err
	}
}

func (s *Store) Assemble(uploadID, fileID string, chunkCount int, expected int64) (string, error) {
	if !objectIDPattern.MatchString(uploadID) || !objectIDPattern.MatchString(fileID) || chunkCount < 1 || expected < 0 {
		return "", errors.New("invalid object path")
	}
	objectDir := filepath.Join(s.root, "objects", fileID[:2])
	if err := os.MkdirAll(objectDir, 0o750); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(objectDir, ".object-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	var total int64
	for index := 0; index < chunkCount; index++ {
		chunk, err := os.Open(filepath.Join(s.root, "uploads", uploadID, strconv.Itoa(index)))
		if err != nil {
			tmp.Close()
			return "", err
		}
		n, copyErr := io.Copy(tmp, chunk)
		closeErr := chunk.Close()
		if copyErr != nil {
			tmp.Close()
			return "", copyErr
		}
		if closeErr != nil {
			tmp.Close()
			return "", closeErr
		}
		total += n
	}
	if total != expected {
		tmp.Close()
		return "", fmt.Errorf("assembled size is %d, expected %d", total, expected)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	target := filepath.Join(objectDir, fileID)
	if err := os.Link(tmpName, target); err == nil {
		return target, nil
	} else if errors.Is(err, os.ErrExist) {
		existingDigest, existingErr := digestFile(target)
		newDigest, newErr := digestFile(tmpName)
		if existingErr != nil || newErr != nil {
			return "", errors.Join(existingErr, newErr)
		}
		if existingDigest != newDigest {
			return "", ErrConflict
		}
		return target, nil
	} else {
		return "", err
	}
}

func (s *Store) OpenObject(fileID string) (*os.File, error) {
	if !objectIDPattern.MatchString(fileID) {
		return nil, errors.New("invalid object path")
	}
	return os.Open(filepath.Join(s.root, "objects", fileID[:2], fileID))
}

func (s *Store) RemoveObject(fileID string) error {
	if !objectIDPattern.MatchString(fileID) {
		return errors.New("invalid object path")
	}
	err := os.Remove(filepath.Join(s.root, "objects", fileID[:2], fileID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) RemoveUpload(uploadID string) error {
	if !objectIDPattern.MatchString(uploadID) {
		return errors.New("invalid upload path")
	}
	return os.RemoveAll(filepath.Join(s.root, "uploads", uploadID))
}

func (s *Store) RemoveChunk(uploadID string, index int) error {
	if !objectIDPattern.MatchString(uploadID) || index < 0 {
		return errors.New("invalid chunk path")
	}
	err := os.Remove(filepath.Join(s.root, "uploads", uploadID, strconv.Itoa(index)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) Reconcile(activeUploads, activeObjects map[string]bool) error {
	var result error
	uploadEntries, err := os.ReadDir(filepath.Join(s.root, "uploads"))
	if err != nil {
		return err
	}
	for _, entry := range uploadEntries {
		if !activeUploads[entry.Name()] {
			result = errors.Join(result, os.RemoveAll(filepath.Join(s.root, "uploads", entry.Name())))
		}
	}
	objectRoot := filepath.Join(s.root, "objects")
	err = filepath.WalkDir(objectRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !activeObjects[entry.Name()] {
			result = errors.Join(result, os.Remove(path))
		}
		return nil
	})
	return errors.Join(result, err)
}

func digestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readPrefix(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}
