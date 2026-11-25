package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elegantgx/chat-deepseek/internal/database"
)

func newSessionTestStore(t *testing.T) *SessionStore {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(
		`INSERT INTO users (id, username, password_hash, created_at, updated_at)
		 VALUES ('u1', 'u1', 'x', 0, 0)`,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return NewSessionStore(db)
}

func TestSessionScopeAndTTL(t *testing.T) {
	store := newSessionTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.Local)
	_, hash, csrf, err := NewSessionSecrets()
	if err != nil {
		t.Fatalf("new secrets: %v", err)
	}
	session, err := store.Create(ctx, SessionParams{
		UserID:    "u1",
		TokenHash: hash,
		CSRF:      csrf,
		Scope:     ScopeCLI,
		TTL:       15 * time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("create cli session: %v", err)
	}
	if session.ExpiresAt != now.Add(15*time.Minute).UnixMilli() {
		t.Fatalf("expires_at = %d", session.ExpiresAt)
	}

	found, err := store.GetByTokenHash(ctx, ScopeCLI, hash)
	if err != nil {
		t.Fatalf("get cli session: %v", err)
	}
	if found.ID != session.ID || found.Scope != ScopeCLI {
		t.Fatalf("session = %+v", found)
	}
	// 同一 token 不能被浏览器会话冒用。
	if _, err := store.GetByTokenHash(ctx, ScopeWeb, hash); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("web lookup of cli token = %v", err)
	}

	if _, err := store.Create(ctx, SessionParams{
		UserID: "u1", TokenHash: "h2", Scope: "bogus", TTL: time.Minute,
	}, now); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if _, err := store.Create(ctx, SessionParams{
		UserID: "u1", TokenHash: "h3", Scope: ScopeCLI, TTL: 0,
	}, now); err == nil {
		t.Fatal("zero ttl accepted")
	}
}

func TestDeleteByUserRemovesBothScopes(t *testing.T) {
	store := newSessionTestStore(t)
	ctx := context.Background()
	now := time.Now()
	type created struct {
		scope SessionScope
		hash  string
	}
	var sessions []created
	for _, scope := range []SessionScope{ScopeWeb, ScopeCLI} {
		_, hash, csrf, err := NewSessionSecrets()
		if err != nil {
			t.Fatalf("new secrets: %v", err)
		}
		if _, err := store.Create(ctx, SessionParams{
			UserID: "u1", TokenHash: hash, CSRF: csrf, Scope: scope, TTL: time.Hour,
		}, now); err != nil {
			t.Fatalf("create %s session: %v", scope, err)
		}
		sessions = append(sessions, created{scope: scope, hash: hash})
	}
	if err := store.DeleteByUser(ctx, "u1"); err != nil {
		t.Fatalf("delete by user: %v", err)
	}
	for _, item := range sessions {
		if _, err := store.GetByTokenHash(ctx, item.scope, item.hash); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("%s session survived: %v", item.scope, err)
		}
	}
}
