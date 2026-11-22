package account

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitlab.com/elegantgx/chat-deepseek/internal/auth"
	"gitlab.com/elegantgx/chat-deepseek/internal/database"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db)
}

func testHash(t *testing.T) string {
	t.Helper()
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return hash
}

func TestCreateAndLookup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	owner, err := store.Create(ctx, "owner", testHash(t), []Group{GroupOwner})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if !owner.HasGroup(GroupUser) || !owner.HasGroup(GroupOwner) {
		t.Fatalf("owner groups = %v", owner.Groups)
	}
	found, err := store.GetByUsername(ctx, "OWNER")
	if err != nil {
		t.Fatalf("case-insensitive lookup: %v", err)
	}
	if found.ID != owner.ID {
		t.Fatalf("found %s, want %s", found.ID, owner.ID)
	}
	if _, err := store.Create(ctx, "Owner", testHash(t), nil); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username error = %v", err)
	}
}

func TestProfileUpdates(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	user, err := store.Create(ctx, "gx", testHash(t), nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	other, err := store.Create(ctx, "other", testHash(t), nil)
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	if err := store.SetUsername(ctx, user.ID, "renamed"); err != nil {
		t.Fatalf("set username: %v", err)
	}
	if err := store.SetUsername(ctx, user.ID, "other"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username error = %v", err)
	}
	if err := store.SetAvatar(ctx, user.ID, "data:image/png;base64,avatar"); err != nil {
		t.Fatalf("set avatar: %v", err)
	}
	updated, err := store.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("get updated user: %v", err)
	}
	if updated.Username != "renamed" || updated.Avatar != "data:image/png;base64,avatar" {
		t.Fatalf("updated user = %+v", updated)
	}
	if other.Username != "other" {
		t.Fatalf("other user changed: %+v", other)
	}
}

func TestLastOwnerGuard(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	owner, err := store.Create(ctx, "owner", testHash(t), []Group{GroupOwner})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := store.RemoveGroup(ctx, owner.ID, GroupOwner); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("remove last owner error = %v", err)
	}
	if err := store.SetDisabled(ctx, owner.ID, true); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("disable last owner error = %v", err)
	}
	if err := store.Delete(ctx, owner.ID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("delete last owner error = %v", err)
	}
}

func TestMultipleGroupsAndOwnerTransfer(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	owner, err := store.Create(ctx, "owner", testHash(t), []Group{GroupOwner})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	other, err := store.Create(ctx, "other", testHash(t), []Group{GroupAdmin, GroupOwner})
	if err != nil {
		t.Fatalf("create second owner: %v", err)
	}
	if len(other.Groups) != 3 {
		t.Fatalf("other groups = %v", other.Groups)
	}
	if err := store.RemoveGroup(ctx, owner.ID, GroupOwner); err != nil {
		t.Fatalf("remove owner group: %v", err)
	}
	updated, err := store.GetByID(ctx, owner.ID)
	if err != nil {
		t.Fatalf("get owner: %v", err)
	}
	if updated.HasGroup(GroupOwner) || !updated.HasGroup(GroupUser) {
		t.Fatalf("owner groups after revoke = %v", updated.Groups)
	}
	if err := store.SetDisabled(ctx, other.ID, true); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("disable last active owner error = %v", err)
	}
}

func TestBaseGroupAndValidation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	user, err := store.Create(ctx, "alice", testHash(t), nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.RemoveGroup(ctx, user.ID, GroupUser); !errors.Is(err, ErrBaseGroup) {
		t.Fatalf("remove base group error = %v", err)
	}
	if err := store.AddGroup(ctx, user.ID, GroupAdmin); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	updated, err := store.GetByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !updated.HasGroup(GroupAdmin) {
		t.Fatalf("user groups = %v", updated.Groups)
	}
	if err := ValidateUsername("a b"); err == nil {
		t.Fatal("username with space accepted")
	}
	if err := ValidateUsername("a"); err != nil {
		t.Fatalf("one-character username rejected: %v", err)
	}
	if err := ValidateUsername(""); err == nil {
		t.Fatal("empty username accepted")
	}
	if err := ValidateUsername(strings.Repeat("a", 65)); err == nil {
		t.Fatal("65-character username accepted")
	}
}
