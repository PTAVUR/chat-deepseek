package backup

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	path, err := Create(dir, "", "")
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
			if _, err := Create(dir, "", ""); !errors.Is(err, ErrDatabaseInUse) {
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
	path, err := Create(dir, "", output)
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
	if _, err := Create(t.TempDir(), "", ""); err == nil {
		t.Fatal("Create without chat.db should fail")
	}
}

func TestCreateWithCustomDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat.db"), "database")
	writeFile(t, filepath.Join(dir, "snapshots", "old.zip"), "old")
	backupDir := filepath.Join(dir, "snapshots")

	path, err := Create(dir, backupDir, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if filepath.Dir(path) != backupDir {
		t.Fatalf("backup path = %s", path)
	}
	files := readArchive(t, path)
	for name := range files {
		if strings.HasPrefix(name, "snapshots/") || strings.HasPrefix(name, "backups/") {
			t.Fatalf("backup directory should be excluded: %s", name)
		}
	}
}

func TestCreateRejectsDataDirectoryAsBackupDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat.db"), "database")
	if _, err := Create(dir, dir, ""); err == nil {
		t.Fatal("Create should reject the data directory as backup directory")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat-deepseek-20260101-010101.zip"), "old")
	writeFile(t, filepath.Join(dir, "chat-deepseek-20261123-120220.zip"), "new")
	writeFile(t, filepath.Join(dir, "notes.txt"), "ignored")

	archives, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(archives) != 2 {
		t.Fatalf("archives = %+v", archives)
	}
	if archives[0].Name != "chat-deepseek-20261123-120220.zip" {
		t.Fatalf("first archive = %s", archives[0].Name)
	}
	if archives[0].Size != 3 {
		t.Fatalf("first size = %d", archives[0].Size)
	}
	if !archives[0].Time.Equal(time.Date(2026, 11, 23, 12, 2, 20, 0, time.Local)) {
		t.Fatalf("first time = %s", archives[0].Time)
	}
}

func TestListMissingDirectory(t *testing.T) {
	archives, err := List(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(archives) != 0 {
		t.Fatalf("archives = %+v", archives)
	}
}

func TestRestore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat.db"), "database")
	writeFile(t, filepath.Join(dir, "config.json"), `{"listen":"127.0.0.1:8090"}`)
	writeFile(t, filepath.Join(dir, "master.key"), "secret")
	writeFile(t, filepath.Join(dir, "files", "user-1", "file-1"), "attachment")
	writeFile(t, filepath.Join(dir, "backups", "keep.zip"), "keep")
	path, err := Create(dir, "", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	writeFile(t, filepath.Join(dir, "chat.db"), "changed")
	writeFile(t, filepath.Join(dir, "config.json"), "changed")
	writeFile(t, filepath.Join(dir, "master.key"), "changed")
	writeFile(t, filepath.Join(dir, "files", "user-1", "file-1"), "changed")
	writeFile(t, filepath.Join(dir, "files", "extra", "file-2"), "extra")
	if _, err := Restore(dir, "", filepath.Base(path)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	for name, want := range map[string]string{
		"chat.db":             "database",
		"config.json":         `{"listen":"127.0.0.1:8090"}`,
		"master.key":          "secret",
		"files/user-1/file-1": "attachment",
	} {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", name, data, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "extra")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("extra files should be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", "keep.zip")); err != nil {
		t.Fatalf("backups should be preserved: %v", err)
	}
}

func TestRestoreMissingItemsRemoveCurrentData(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat.db"), "old database")
	writeFile(t, filepath.Join(dir, "config.json"), "{}")
	writeFile(t, filepath.Join(dir, "master.key"), "old secret")
	writeFile(t, filepath.Join(dir, "files", "user-1", "file-1"), "old attachment")

	archive := filepath.Join(dir, "minimal.zip")
	writeZip(t, archive, map[string]string{"chat.db": "new database"})
	if _, err := Restore(dir, "", archive); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "chat.db")); err != nil || string(data) != "new database" {
		t.Fatalf("chat.db = %q, err = %v", data, err)
	}
	for _, name := range []string{"config.json", "master.key", "files"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be removed: %v", name, err)
		}
	}
}

func TestRestoreRefusesRunningDatabase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "chat.db"), "database")
	writeFile(t, filepath.Join(dir, "chat.db-wal"), "live")
	archive := filepath.Join(dir, "backup.zip")
	writeZip(t, archive, map[string]string{"chat.db": "restored"})
	if _, err := Restore(dir, "", archive); !errors.Is(err, ErrDatabaseInUse) {
		t.Fatalf("Restore error = %v, want ErrDatabaseInUse", err)
	}
}

func TestRestoreRejectsUnsafePath(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "bad.zip")
	writeZip(t, archive, map[string]string{"../escape": "bad", "chat.db": "database"})
	if _, err := Restore(dir, "", archive); err == nil {
		t.Fatal("Restore should reject unsafe paths")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe file escaped the staging directory")
	}
}

func TestRestoreRequiresDatabase(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "empty.zip")
	writeZip(t, archive, map[string]string{"config.json": "{}"})
	if _, err := Restore(dir, "", archive); err == nil {
		t.Fatal("Restore should require chat.db")
	}
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}
