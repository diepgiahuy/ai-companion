package voicemail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"
)

func TestFileSystemValidatesAndDeletesIdempotently(t *testing.T) {
	store, err := NewFileSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("synthetic ogg opus fixture")
	sum := sha256.Sum256(body)
	checksum := hex.EncodeToString(sum[:])
	if err := store.Put(context.Background(), "opaque-1", bytes.NewReader(body), int64(len(body)), checksum); err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), "opaque-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if err := store.Delete(context.Background(), "opaque-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), "opaque-1"); err != nil {
		t.Fatal(err)
	}
}

func TestFileSystemRejectsMismatchAndTraversal(t *testing.T) {
	store, err := NewFileSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "../escape", bytes.NewReader(nil), 0, ""); err == nil {
		t.Fatal("expected invalid key")
	}
	if err := store.Put(context.Background(), "opaque-2", bytes.NewReader([]byte("x")), 2, "00"); err == nil {
		t.Fatal("expected mismatch")
	}
	if _, err := store.Open(context.Background(), "opaque-2"); !os.IsNotExist(err) {
		t.Fatalf("partial blob became visible: %v", err)
	}
}

func TestFileSystemContextCancellationAndErrors(t *testing.T) {
	if _, err := NewFileSystem(""); err == nil {
		t.Fatal("expected error on empty root directory")
	}

	store, err := NewFileSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// 1. Context already cancelled before read
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := []byte("audio payload")
	sum := sha256.Sum256(body)
	checksum := hex.EncodeToString(sum[:])
	err = store.Put(ctx, "cancelled-key", bytes.NewReader(body), int64(len(body)), checksum)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}

	// 2. Open with invalid key
	if _, err := store.Open(context.Background(), "../invalid"); err == nil {
		t.Fatal("expected invalid key error on Open")
	}

	// 3. Delete with invalid key
	if err := store.Delete(context.Background(), "../invalid"); err == nil {
		t.Fatal("expected invalid key error on Delete")
	}
}
