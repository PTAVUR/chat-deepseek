package database

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// TestDSNHasNoURIAuthority 覆盖 Windows 盘符路径：file://C:/... 会让
// SQLite 报 "invalid uri authority: C:"，必须序列化成 file:///C:/...。
func TestDSNHasNoURIAuthority(t *testing.T) {
	wantPragmas := []string{
		"busy_timeout(5000)",
		"journal_mode(WAL)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	}
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{
			name: "windows drive",
			path: `C:/Users/Alice/My Data/chat-deepseek/data/chat.db`,
			want: `file:///C:/Users/Alice/My%20Data/chat-deepseek/data/chat.db`,
		},
		{
			name: "windows UNC",
			path: `//server/share/chat-deepseek/data/chat.db`,
			want: `file:////server/share/chat-deepseek/data/chat.db`,
		},
		{
			name: "unix absolute",
			path: `/home/alice/chat-deepseek/data/chat.db`,
			want: `file:///home/alice/chat-deepseek/data/chat.db`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dsn(tc.path)
			if !strings.HasPrefix(got, tc.want+"?") {
				t.Fatalf("dsn(%q) = %q, want prefix %q", tc.path, got, tc.want)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse dsn %q: %v", got, err)
			}
			if parsed.Scheme != "file" {
				t.Fatalf("scheme = %q, want file", parsed.Scheme)
			}
			if parsed.Host != "" {
				t.Fatalf("authority = %q, want empty", parsed.Host)
			}
			pragmas := parsed.Query()["_pragma"]
			if len(pragmas) != len(wantPragmas) {
				t.Fatalf("pragmas = %v, want %v", pragmas, wantPragmas)
			}
			for i, want := range wantPragmas {
				if pragmas[i] != want {
					t.Fatalf("pragma[%d] = %q, want %q", i, pragmas[i], want)
				}
			}
		})
	}
}

// TestOpenRelativeDataDir 保证相对数据目录也能打开：补绝对路径前，
// dsn 会生成 file://data/chat.db，同样被 SQLite 当成非法 authority。
func TestOpenRelativeDataDir(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := Open("data")
	if err != nil {
		t.Fatalf("Open relative data dir: %v", err)
	}
	defer func() { _ = db.Close() }()

	want, err := filepath.Abs(filepath.Join("data", "chat.db"))
	if err != nil {
		t.Fatalf("resolve database path: %v", err)
	}
	var seq int
	var name, file string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err != nil {
		t.Fatalf("database_list: %v", err)
	}
	if filepath.Clean(file) != filepath.Clean(want) {
		t.Fatalf("database file = %q, want %q", file, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("stat database: %v", err)
	}
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
