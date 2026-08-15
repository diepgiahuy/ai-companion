package voicemail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type FileSystem struct{ root string }

func NewFileSystem(root string) (*FileSystem, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("voice mail blob directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("create voice mail blob directory: %w", err)
	}
	return &FileSystem{root: abs}, nil
}

func (f *FileSystem) path(key string) (string, error) {
	if key == "" || strings.ContainsAny(key, `/\\`) || filepath.Base(key) != key {
		return "", fmt.Errorf("invalid opaque blob key")
	}
	return filepath.Join(f.root, key+".ogg"), nil
}

func (f *FileSystem) Put(ctx context.Context, key string, source io.Reader, expectedSize int64, expectedChecksum string) error {
	path, err := f.path(key)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(f.root, ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(&contextReader{ctx: ctx, reader: source}, expectedSize+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != expectedSize {
		return fmt.Errorf("voice mail size mismatch")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expectedChecksum) {
		return fmt.Errorf("voice mail checksum mismatch")
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit voice mail blob: %w", err)
	}
	return nil
}

func (f *FileSystem) Open(_ context.Context, key string) (io.ReadCloser, error) {
	path, err := f.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (f *FileSystem) Delete(_ context.Context, key string) error {
	path, err := f.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}
