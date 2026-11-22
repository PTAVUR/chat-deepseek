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
		"",
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

	if _, err := store.Create(ctx, userID, "", "empty.txt", "text/plain", "", false, 0, strings.NewReader("")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("empty error = %v", err)
	}
	if _, err := store.Create(ctx, userID, "", "x.txt", "text/plain", "bad", false, 1, strings.NewReader("x")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("purpose error = %v", err)
	}
	if _, err := store.Create(ctx, userID, "", "big.bin", "application/octet-stream", "", false, MaxFileSize+1, strings.NewReader("x")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size error = %v", err)
	}
	if _, err := store.Create(ctx, userID, "", "big.txt", "text/plain", "user_data", false, MaxUserDataSize+1, strings.NewReader("x")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("user data size error = %v", err)
	}
	if _, err := store.Create(ctx, "../alice", "", "x.txt", "text/plain", "", false, 1, strings.NewReader("x")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("user id error = %v", err)
	}
}

func TestCreateImageMetadata(t *testing.T) {
	store, userID, _ := newTestStore(t)
	file, err := store.Create(context.Background(), userID, "", "photo.png", "image/png", "", true, 4, strings.NewReader("data"))
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

func TestExplicitIDIsIdempotentAndScoped(t *testing.T) {
	store, userID, otherID := newTestStore(t)
	ctx := context.Background()
	first, err := store.Create(ctx, userID, "same-id", "a.txt", "text/plain", "user_data", false, 3, strings.NewReader("one"))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.ID != "same-id" {
		t.Fatalf("id = %q", first.ID)
	}
	if _, err := store.Create(ctx, userID, "same-id", "b.txt", "text/plain", "user_data", false, 3, strings.NewReader("two")); err != nil {
		t.Fatalf("replace: %v", err)
	}
	metadata, handle, err := store.Open(ctx, userID, "same-id")
	if err != nil {
		t.Fatalf("open replaced: %v", err)
	}
	data, _ := io.ReadAll(handle)
	_ = handle.Close()
	if metadata.Name != "b.txt" || string(data) != "two" {
		t.Fatalf("replaced = %+v, data = %q", metadata, data)
	}

	if _, err := store.Create(ctx, otherID, "same-id", "c.txt", "text/plain", "user_data", false, 3, strings.NewReader("bad")); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("other user error = %v", err)
	}
	metadata, handle, err = store.Open(ctx, userID, "same-id")
	if err != nil {
		t.Fatalf("open after conflict: %v", err)
	}
	data, _ = io.ReadAll(handle)
	_ = handle.Close()
	if metadata.Name != "b.txt" || string(data) != "two" {
		t.Fatalf("cross-user overwrite = %+v, data = %q", metadata, data)
	}
}

func TestDeleteAll(t *testing.T) {
	store, userID, _ := newTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"file-a", "file-b"} {
		if _, err := store.Create(ctx, userID, id, id+".txt", "text/plain", "user_data", false, 1, strings.NewReader("x")); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if err := store.DeleteAll(ctx, userID); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	for _, id := range []string{"file-a", "file-b"} {
		if _, err := store.Get(ctx, userID, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get %s error = %v", id, err)
		}
	}
}
