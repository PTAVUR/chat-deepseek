package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	defaults, err := Load(dir)
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if defaults.Listen != Default().Listen || defaults.APIOrigin != Default().APIOrigin {
		t.Fatalf("defaults = %+v", defaults)
	}
	want := Default()
	want.Listen = "0.0.0.0:9000"
	want.PublicURL = "https://chat.example.com"
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config permissions = %o", info.Mode().Perm())
	}
}

func TestResolveDataDirExplicit(t *testing.T) {
	dir := t.TempDir()
	got, err := ResolveDataDir(dir)
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if got != abs {
		t.Fatalf("ResolveDataDir = %q, want %q", got, abs)
	}
}

func TestSetPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	original := `{
  "listen": "127.0.0.1:9000",
  "public_url": "https://chat.example.com",
  "custom_future_field": {"keep": true}
}
`
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	backupDir := filepath.Join(dir, "backups-custom")
	normalized, err := Set(dir, "backup_dir", backupDir)
	if err != nil {
		t.Fatalf("Set backup_dir: %v", err)
	}
	if normalized != backupDir {
		t.Fatalf("normalized = %q, want %q", normalized, backupDir)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BackupDir != backupDir {
		t.Fatalf("BackupDir = %q, want %q", cfg.BackupDir, backupDir)
	}
	fields := readTestFields(t, path)
	if _, ok := fields["custom_future_field"]; !ok {
		t.Fatal("unknown field was dropped")
	}
	if got := rawString(t, fields["listen"]); got != "127.0.0.1:9000" {
		t.Fatalf("listen = %q", got)
	}
	if got := rawString(t, fields["public_url"]); got != "https://chat.example.com" {
		t.Fatalf("public_url = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSavePreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"custom":"keep"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := Save(dir, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fields := readTestFields(t, path)
	if _, ok := fields["custom"]; !ok {
		t.Fatal("unknown field was dropped by Save")
	}
}

func TestSetValidation(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		key   string
		value string
		want  string
	}{
		{"listen", "", "不能为空"},
		{"listen", "not-an-address", "host:port"},
		{"public_url", "ftp://chat.example.com", "http:// 或 https://"},
		{"public_url", "not-a-url", "http:// 或 https://"},
		{"api_origin", "", "不能为空"},
		{"api_origin", "chat.example.com", "http:// 或 https://"},
		{"backup_dir", "relative/path", "绝对路径"},
		{"unknown", "x", "未知配置项"},
	} {
		_, err := Set(dir, tc.key, tc.value)
		if err == nil {
			t.Fatalf("Set(%q, %q) succeeded", tc.key, tc.value)
		}
		var invalid *InvalidError
		if !errors.As(err, &invalid) {
			t.Fatalf("Set(%q, %q) error = %v, want InvalidError", tc.key, tc.value, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("Set(%q, %q) error = %v, want %q", tc.key, tc.value, err, tc.want)
		}
	}
}

func TestSetClearsBackupDir(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom")
	if _, err := Set(dir, "backup_dir", custom); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := Set(dir, "backup_dir", ""); err != nil {
		t.Fatalf("clear Set: %v", err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BackupDir != "" {
		t.Fatalf("BackupDir = %q, want empty", cfg.BackupDir)
	}
	fields := readTestFields(t, filepath.Join(dir, "config.json"))
	if _, ok := fields["backup_dir"]; ok {
		t.Fatal("empty backup_dir should be removed from config.json")
	}
}

func TestSetCreatesDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if _, err := Set(dir, "listen", "0.0.0.0:9000"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9000" {
		t.Fatalf("Listen = %q", cfg.Listen)
	}
}

func TestResolveBackupDir(t *testing.T) {
	dir := t.TempDir()
	wantDefault := filepath.Join(dir, "backups")
	got, err := ResolveBackupDir(dir, "")
	if err != nil {
		t.Fatalf("ResolveBackupDir default: %v", err)
	}
	if got != wantDefault {
		t.Fatalf("default = %q, want %q", got, wantDefault)
	}
	custom := filepath.Join(dir, "custom")
	got, err = ResolveBackupDir(dir, custom)
	if err != nil {
		t.Fatalf("ResolveBackupDir custom: %v", err)
	}
	if got != custom {
		t.Fatalf("custom = %q, want %q", got, custom)
	}
	got, err = ResolveBackupDir(dir, "relative-backups")
	if err != nil {
		t.Fatalf("ResolveBackupDir relative: %v", err)
	}
	if want := filepath.Join(dir, "relative-backups"); got != want {
		t.Fatalf("relative = %q, want %q", got, want)
	}
}

func readTestFields(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return fields
}

func rawString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("parse string: %v", err)
	}
	return value
}
