package database

import (
	"context"
	"database/sql"
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
		"user_settings", "generation_leases",
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
}
