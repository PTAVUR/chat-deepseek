package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenSeedsGroups(t *testing.T) {
	dataDir := t.TempDir()
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(context.Background(), `SELECT name, rank FROM groups ORDER BY rank`)
	if err != nil {
		t.Fatalf("query groups: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		var rank int
		if err := rows.Scan(&name, &rank); err != nil {
			t.Fatalf("scan group: %v", err)
		}
		names = append(names, name)
	}
	if len(names) != 3 || names[0] != "User" || names[1] != "Admin" || names[2] != "Owner" {
		t.Fatalf("groups = %v", names)
	}

	var version int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	first, err := Open(dataDir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = first.Close()
	second, err := Open(dataDir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	_ = second.Close()
}

func TestSchemaHasNoUnexpectedMissingTable(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, table := range []string{
		"users", "groups", "user_groups", "auth_sessions", "chats", "files",
		"user_settings",
	} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name)
		if err == sql.ErrNoRows {
			t.Fatalf("missing table %s", table)
		}
		if err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
	}
	var leases int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'generation_leases'`,
	).Scan(&leases); err != nil {
		t.Fatalf("query generation_leases: %v", err)
	}
	if leases != 0 {
		t.Fatal("generation_leases table should be removed")
	}
	for _, table := range []string{"chats", "files"} {
		if hasColumn(t, db, table, "deleted_at") {
			t.Fatalf("%s.deleted_at should be removed", table)
		}
	}
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table info %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column %s: %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table info %s: %v", table, err)
	}
	return false
}

func TestMigrateFromV1(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "chat.db")
	old, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open old database: %v", err)
	}
	if _, err := old.Exec(schemaV1); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	if _, err := old.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("set v1: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close old database: %v", err)
	}

	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("version = %d, want %d", version, schemaVersion)
	}
	rows, err := db.Query("PRAGMA table_info(files)")
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		if name == "purpose" {
			found = true
		}
	}
	if !found {
		t.Fatal("purpose column missing after migration")
	}
	rows, err = db.Query("PRAGMA table_info(users)")
	if err != nil {
		t.Fatalf("users table info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	found = false
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan user column: %v", err)
		}
		if name == "avatar" {
			found = true
		}
	}
	if !found {
		t.Fatal("avatar column missing after migration")
	}
	if hasColumn(t, db, "chats", "deleted_at") || hasColumn(t, db, "files", "deleted_at") {
		t.Fatal("deleted_at columns still present after migration")
	}
}
