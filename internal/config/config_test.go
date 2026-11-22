package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	defaults, err := Load(dir)
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if defaults.Listen != Default().Listen {
		t.Fatalf("default listen = %q", defaults.Listen)
	}
	want := Config{Listen: "0.0.0.0:9000", PublicURL: "https://chat.example.com"}
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
