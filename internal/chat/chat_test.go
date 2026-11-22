package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gitlab.com/elegantgx/chat-deepseek/internal/account"
	"gitlab.com/elegantgx/chat-deepseek/internal/database"
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

	summaries, err := store.List(ctx, userID, false)
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

func TestSoftDeleteRestorePurge(t *testing.T) {
	store, _, userID, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, userID, sessionJSON("session-1", "Hello")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SoftDelete(ctx, userID, "session-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, userID, "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted error = %v", err)
	}
	active, err := store.List(ctx, userID, false)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active = %+v", active)
	}
	deleted, err := store.List(ctx, userID, true)
	if err != nil {
		t.Fatalf("list deleted: %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != "session-1" {
		t.Fatalf("deleted = %+v", deleted)
	}
	if err := store.SoftDelete(ctx, userID, "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice error = %v", err)
	}

	if err := store.Restore(ctx, userID, "session-1"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := store.Get(ctx, userID, "session-1"); err != nil {
		t.Fatalf("get restored: %v", err)
	}

	if err := store.SoftDelete(ctx, userID, "session-1"); err != nil {
		t.Fatalf("delete again: %v", err)
	}
	if err := store.Purge(ctx, userID, "session-1"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if err := store.Restore(ctx, userID, "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore purged error = %v", err)
	}
	if _, err := store.Get(ctx, userID, "session-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get purged error = %v", err)
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
