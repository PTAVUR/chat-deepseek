package backup

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readArchive(t *testing.T, path string) map[string]string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer func() { _ = reader.Close() }()
	files := make(map[string]string)
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		files[file.Name] = string(data)
	}
	return files
}

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat.db"), "database")
	writeFile(t, filepath.Join(dir, "config.json"), `{"listen":"127.0.0.1:8090"}`)
	writeFile(t, filepath.Join(dir, "master.key"), "secret")
	writeFile(t, filepath.Join(dir, "files", "user-1", "file-1"), "attachment")
	writeFile(t, filepath.Join(dir, "backups", "old.zip"), "old backup")

	path, err := Create(dir, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if filepath.Dir(path) != filepath.Join(dir, "backups") {
		t.Fatalf("backup path = %s", path)
	}
	files := readArchive(t, path)
	for name, want := range map[string]string{
		"chat.db":             "database",
		"config.json":         `{"listen":"127.0.0.1:8090"}`,
		"master.key":          "secret",
		"files/user-1/file-1": "attachment",
	} {
		if files[name] != want {
			t.Fatalf("%s = %q, want %q", name, files[name], want)
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "backups/") {
			t.Fatalf("backups directory should be excluded: %s", name)
		}
	}
}

func TestCreateRefusesRunningDatabase(t *testing.T) {
	for _, name := range []string{"chat.db-wal", "chat.db-shm"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "chat.db"), "database")
			writeFile(t, filepath.Join(dir, name), "live")
			if _, err := Create(dir, ""); !errors.Is(err, ErrDatabaseInUse) {
				t.Fatalf("Create error = %v, want ErrDatabaseInUse", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "backups")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("backup directory should not be created: %v", err)
			}
		})
	}
}

func TestCreateSkipsItsOwnOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat.db"), "database")
	output := filepath.Join(dir, "snapshot.zip")
	path, err := Create(dir, output)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if path != output {
		t.Fatalf("path = %s, want %s", path, output)
	}
	files := readArchive(t, path)
	if _, ok := files["snapshot.zip"]; ok {
		t.Fatal("backup archive contains itself")
	}
	if files["chat.db"] != "database" {
		t.Fatalf("chat.db = %q", files["chat.db"])
	}
}

func TestCreateWithoutDatabase(t *testing.T) {
	if _, err := Create(t.TempDir(), ""); err == nil {
		t.Fatal("Create without chat.db should fail")
	}
}
