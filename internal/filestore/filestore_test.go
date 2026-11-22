package filestore

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"gitlab.com/elegantgx/chat-deepseek/internal/account"
	"gitlab.com/elegantgx/chat-deepseek/internal/database"
)

func newTestStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	accounts := account.NewStore(db)
	first, err := accounts.Create(context.Background(), "alice", "hash", []account.Group{account.GroupUser})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	second, err := accounts.Create(context.Background(), "bob", "hash", []account.Group{account.GroupUser})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	store, err := NewStore(db, dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, first.ID, second.ID
}

func TestCreateOpenSetServerIDDelete(t *testing.T) {
	store, userID, otherID := newTestStore(t)
	ctx := context.Background()
	file, err := store.Create(
		ctx,
		userID,
		"../报告.txt",
		"text/plain",
		"user_data",
		false,
		5,
		strings.NewReader("hello"),
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if file.Name != "报告.txt" || file.Size != 5 || file.MIMEType != "text/plain" {
		t.Fatalf("file = %+v", file)
	}
	metadata, handle, err := store.Open(ctx, userID, file.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = handle.Close() }()
	data, err := io.ReadAll(handle)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" || metadata.ID != file.ID {
		t.Fatalf("data = %q, metadata = %+v", data, metadata)
	}

	if _, _, err := store.Open(ctx, otherID, file.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user open error = %v", err)
	}
	if _, err := store.Get(ctx, otherID, file.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user get error = %v", err)
	}
	if err := store.SetServerID(ctx, userID, file.ID, "file-remote"); err != nil {
		t.Fatalf("set server id: %v", err)
	}
	got, err := store.Get(ctx, userID, file.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ServerID != "file-remote" {
		t.Fatalf("server id = %q", got.ServerID)
	}

	if err := store.Delete(ctx, userID, file.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, userID, file.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted error = %v", err)
	}
	if _, _, err := store.Open(ctx, userID, file.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open deleted error = %v", err)
	}
	if err := store.Delete(ctx, userID, file.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice error = %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	store, userID, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Create(ctx, userID, "empty.txt", "text/plain", "", false, 0, strings.NewReader("")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("empty error = %v", err)
	}
	if _, err := store.Create(ctx, userID, "x.txt", "text/plain", "bad", false, 1, strings.NewReader("x")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("purpose error = %v", err)
	}
	if _, err := store.Create(ctx, userID, "big.bin", "application/octet-stream", "", false, MaxFileSize+1, strings.NewReader("x")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size error = %v", err)
	}
	if _, err := store.Create(ctx, userID, "big.txt", "text/plain", "user_data", false, MaxUserDataSize+1, strings.NewReader("x")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("user data size error = %v", err)
	}
	if _, err := store.Create(ctx, "../alice", "x.txt", "text/plain", "", false, 1, strings.NewReader("x")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("user id error = %v", err)
	}
}

func TestCreateImageMetadata(t *testing.T) {
	store, userID, _ := newTestStore(t)
	file, err := store.Create(context.Background(), userID, "photo.png", "image/png", "", true, 4, strings.NewReader("data"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !file.IsImage || file.MIMEType != "image/png" {
		t.Fatalf("file = %+v", file)
	}
	info, err := os.Stat(store.root + "/" + userID + "/" + file.ID)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.Size() != 4 {
		t.Fatalf("size = %d", info.Size())
	}
}
