package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/database"
)

func newTestStore(t *testing.T) (*Store, *sql.DB, string, string) {
	t.Helper()
	db, err := database.Open(t.TempDir())
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
	return NewStore(db), db, first.ID, second.ID
}

func sessionJSON(id, title string) []byte {
	data, _ := json.Marshal(map[string]any{
		"id":        id,
		"title":     title,
		"createdAt": 1000,
		"updatedAt": 2000,
		"messages":  []any{},
	})
	return data
}

func sessionWithFiles(id string, fileIDs ...string) []byte {
	files := make([]any, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		files = append(files, map[string]any{"id": fileID})
	}
	data, _ := json.Marshal(map[string]any{
		"id":    id,
		"title": id,
		"messages": []any{
			map[string]any{"role": "user", "content": "hi", "files": files},
		},
	})
	return data
}

func TestCreateGetListUpdate(t *testing.T) {
	store, _, userID, otherID := newTestStore(t)
	ctx := context.Background()

	created, err := store.Create(ctx, userID, sessionJSON("session-1", " Hello "))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("version = %d", created.Version)
	}
	got, err := store.Get(ctx, userID, "session-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 1 || string(got.Session) != string(sessionJSON("session-1", " Hello ")) {
		t.Fatalf("got = %+v", got)
	}
	if _, err := store.Get(ctx, otherID, "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user get error = %v", err)
	}

	summaries, err := store.List(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != "session-1" || summaries[0].Title != "Hello" {
		t.Fatalf("summaries = %+v", summaries)
	}

	updatedRaw := sessionJSON("session-1", "Renamed")
	updated, err := store.Update(ctx, userID, "session-1", updatedRaw, 1)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("updated version = %d", updated.Version)
	}
	if _, err := store.Update(ctx, userID, "session-1", updatedRaw, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	if _, err := store.Update(ctx, otherID, "session-1", updatedRaw, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user update error = %v", err)
	}
	if _, err := store.Update(ctx, userID, "missing", sessionJSON("missing", "x"), 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing update error = %v", err)
	}
}

func TestDeleteReturnsFileReferences(t *testing.T) {
	store, _, userID, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, userID, sessionWithFiles("session-a", "file-1", "file-2")); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := store.Create(ctx, userID, sessionWithFiles("session-b", "file-2", "file-3")); err != nil {
		t.Fatalf("create b: %v", err)
	}

	fileIDs, err := store.Delete(ctx, userID, "session-a")
	if err != nil {
		t.Fatalf("delete a: %v", err)
	}
	if len(fileIDs) != 2 || fileIDs[0] == fileIDs[1] {
		t.Fatalf("deleted file ids = %v", fileIDs)
	}
	referenced, err := store.ReferencedFileIDs(ctx, userID)
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	if _, ok := referenced["file-2"]; !ok {
		t.Fatalf("references = %v, want file-2 kept", referenced)
	}
	if _, ok := referenced["file-1"]; ok {
		t.Fatalf("references = %v, file-1 should be gone", referenced)
	}
	if _, err := store.Delete(ctx, userID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing error = %v", err)
	}
}

func TestValidation(t *testing.T) {
	store, _, userID, _ := newTestStore(t)
	ctx := context.Background()

	for _, raw := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"id":"bad/id","title":"x"}`),
		[]byte(`{"id":"` + strings.Repeat("a", MaxIDLength+1) + `","title":"x"}`),
		[]byte(`{"id":"session-1","title":"` + strings.Repeat("x", MaxTitleLength+1) + `"}`),
		[]byte(`not json`),
		[]byte(`{"id":"session-1","title":"x","rootMessageId":123}`),
	} {
		if _, err := store.Create(ctx, userID, raw); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("Create(%s) error = %v", raw, err)
		}
	}

	oversized := []byte(`{"id":"session-1","title":"x","data":"` + strings.Repeat("a", MaxSessionSize) + `"}`)
	if _, err := store.Create(ctx, userID, oversized); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("oversized error = %v", err)
	}

	raw := sessionJSON("session-1", "x")
	if _, err := store.Create(ctx, userID, raw); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Create(ctx, userID, raw); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := store.Update(ctx, userID, "session-1", sessionJSON("other", "x"), 1); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("id mismatch error = %v", err)
	}
	if _, err := store.Update(ctx, userID, "session-1", raw, 0); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("invalid version error = %v", err)
	}
}
