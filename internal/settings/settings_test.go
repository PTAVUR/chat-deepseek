package settings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/database"
	"github.com/elegantgx/chat-deepseek/internal/secret"
)

func newTestStore(t *testing.T) (*Store, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := secret.LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	user, err := account.NewStore(db).Create(
		context.Background(),
		"alice",
		"hash",
		[]account.Group{account.GroupUser},
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return NewStore(db, key), db, user.ID
}

func TestPreferencesRoundTrip(t *testing.T) {
	store, _, userID := newTestStore(t)
	ctx := context.Background()

	initial, err := store.Get(ctx, userID)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if string(initial.Preferences) != `{}` || initial.HasAPIKey {
		t.Fatalf("initial = %+v", initial)
	}

	saved, err := store.SavePreferences(ctx, userID, []byte(`{"locale":"zh-CN","thinking":true}`))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.HasAPIKey {
		t.Fatal("has api key = true")
	}
	var preferences map[string]any
	if err := json.Unmarshal(saved.Preferences, &preferences); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	if preferences["locale"] != "zh-CN" || preferences["thinking"] != true {
		t.Fatalf("preferences = %v", preferences)
	}
}

func TestAPIKeyIsEncryptedAndPreservesPreferences(t *testing.T) {
	store, db, userID := newTestStore(t)
	ctx := context.Background()
	if _, err := store.SavePreferences(ctx, userID, []byte(`{"themePreference":"dark"}`)); err != nil {
		t.Fatalf("save preferences: %v", err)
	}

	hasKey, err := store.SetAPIKey(ctx, userID, "sk-test-secret")
	if err != nil {
		t.Fatalf("set api key: %v", err)
	}
	if !hasKey {
		t.Fatal("has api key = false")
	}
	apiKey, err := store.APIKey(ctx, userID)
	if err != nil {
		t.Fatalf("read api key: %v", err)
	}
	if apiKey != "sk-test-secret" {
		t.Fatalf("api key = %q", apiKey)
	}

	var ciphertext, nonce []byte
	if err := db.QueryRowContext(
		ctx,
		`SELECT api_key_ciphertext, api_key_nonce FROM user_settings WHERE user_id = ?`,
		userID,
	).Scan(&ciphertext, &nonce); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if len(ciphertext) == 0 || len(nonce) == 0 {
		t.Fatal("empty ciphertext or nonce")
	}
	if bytes.Contains(ciphertext, []byte("sk-test-secret")) {
		t.Fatal("ciphertext contains plaintext")
	}

	current, err := store.Get(ctx, userID)
	if err != nil {
		t.Fatalf("get after key: %v", err)
	}
	if string(current.Preferences) != `{"themePreference":"dark"}` || !current.HasAPIKey {
		t.Fatalf("settings after key = %+v", current)
	}

	hasKey, err = store.SetAPIKey(ctx, userID, "")
	if err != nil {
		t.Fatalf("clear api key: %v", err)
	}
	if hasKey {
		t.Fatal("has api key after clear = true")
	}
	if _, err := store.APIKey(ctx, userID); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("api key after clear error = %v", err)
	}
	current, err = store.Get(ctx, userID)
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if string(current.Preferences) != `{"themePreference":"dark"}` {
		t.Fatalf("preferences after clear = %s", current.Preferences)
	}
}

func TestInvalidPreferencesAndAPIKey(t *testing.T) {
	store, _, userID := newTestStore(t)
	ctx := context.Background()

	for _, raw := range []string{
		`[]`,
		`null`,
		`{"apiKey":"sk-leak"}`,
		`{"API_KEY":"sk-leak"}`,
		`{"locale":"zh-CN"} trailing`,
	} {
		if _, err := store.SavePreferences(ctx, userID, []byte(raw)); !errors.Is(err, ErrInvalidPreferences) {
			t.Fatalf("SavePreferences(%s) error = %v", raw, err)
		}
	}

	overlong := `{"blob":"` + strings.Repeat("a", MaxPreferencesSize) + `"}`
	if _, err := store.SavePreferences(ctx, userID, []byte(overlong)); !errors.Is(err, ErrInvalidPreferences) {
		t.Fatalf("oversized preferences error = %v", err)
	}

	if _, err := store.SetAPIKey(ctx, userID, strings.Repeat("k", MaxAPIKeyLength+1)); !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("oversized api key error = %v", err)
	}
}

func TestSettingsAreScopedToUser(t *testing.T) {
	store, db, firstUser := newTestStore(t)
	secondUser, err := account.NewStore(db).Create(
		context.Background(),
		"bob",
		"hash",
		[]account.Group{account.GroupUser},
	)
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	ctx := context.Background()
	if _, err := store.SavePreferences(ctx, firstUser, []byte(`{"locale":"zh-CN"}`)); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if _, err := store.SetAPIKey(ctx, firstUser, "sk-first"); err != nil {
		t.Fatalf("set first key: %v", err)
	}
	second, err := store.Get(ctx, secondUser.ID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if string(second.Preferences) != `{}` || second.HasAPIKey {
		t.Fatalf("second settings = %+v", second)
	}
	if _, err := store.APIKey(ctx, secondUser.ID); !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("second api key error = %v", err)
	}
}

func TestNormalizePreferencesCompactsAndAcceptsEmpty(t *testing.T) {
	normalized, err := NormalizePreferences([]byte("  { \"a\" : 1 }  "))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if string(normalized) != `{"a":1}` {
		t.Fatalf("normalized = %s", normalized)
	}
	empty, err := NormalizePreferences(nil)
	if err != nil {
		t.Fatalf("normalize empty: %v", err)
	}
	if string(empty) != `{}` {
		t.Fatalf("empty = %s", empty)
	}
}
