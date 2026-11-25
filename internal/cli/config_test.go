package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elegantgx/chat-deepseek/internal/config"
)

func TestCLIConfigSetAllKeys(t *testing.T) {
	c := newTestCLI(t)
	if err := os.MkdirAll(c.dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	original := `{"custom_future_field": {"keep": true}}`
	configPath := filepath.Join(c.dataDir, "config.json")
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	backupDir := filepath.Join(t.TempDir(), "backups")

	output := c.must("", "config", "set", "listen", "0.0.0.0:9999", "--data-dir", c.dataDir)
	if !strings.Contains(output, "重启 chat-deepseek serve 后生效") {
		t.Fatalf("listen output = %q", output)
	}
	output = c.must("", "config", "set", "public_url", "https://chat.example.com/", "--data-dir", c.dataDir)
	if !strings.Contains(output, "重启 chat-deepseek serve 后生效") {
		t.Fatalf("public_url output = %q", output)
	}
	output = c.must("", "config", "set", "api_origin", "https://api.example.com", "--data-dir", c.dataDir)
	if !strings.Contains(output, "重启 chat-deepseek serve 后生效") {
		t.Fatalf("api_origin output = %q", output)
	}
	output = c.must("", "config", "set", "backup_dir", backupDir, "--data-dir", c.dataDir)
	if !strings.Contains(output, "下次 backup 生效") {
		t.Fatalf("backup_dir output = %q", output)
	}

	cfg, err := config.Load(c.dataDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "0.0.0.0:9999" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	// public_url 末尾斜杠会被去掉。
	if cfg.PublicURL != "https://chat.example.com" {
		t.Fatalf("public_url = %q", cfg.PublicURL)
	}
	if cfg.APIOrigin != "https://api.example.com" {
		t.Fatalf("api_origin = %q", cfg.APIOrigin)
	}
	if cfg.BackupDir != backupDir {
		t.Fatalf("backup_dir = %q", cfg.BackupDir)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if _, ok := fields["custom_future_field"]; !ok {
		t.Fatal("unknown field was dropped")
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestCLIConfigSetClearsOptionalValues(t *testing.T) {
	c := newTestCLI(t)
	c.must("", "config", "set", "public_url", "https://chat.example.com", "--data-dir", c.dataDir)
	c.must("", "config", "set", "backup_dir", filepath.Join(t.TempDir(), "backups"), "--data-dir", c.dataDir)

	output := c.must("", "config", "set", "public_url", "", "--data-dir", c.dataDir)
	if !strings.Contains(output, "已清除 public_url") {
		t.Fatalf("clear public_url output = %q", output)
	}
	output = c.must("", "config", "set", "backup_dir", "", "--data-dir", c.dataDir)
	if !strings.Contains(output, "已清除 backup_dir") || !strings.Contains(output, "backups") {
		t.Fatalf("clear backup_dir output = %q", output)
	}
	cfg, err := config.Load(c.dataDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PublicURL != "" || cfg.BackupDir != "" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestCLIConfigSetValidation(t *testing.T) {
	c := newTestCLI(t)
	for _, args := range [][]string{
		{"config", "set", "listen", "", "--data-dir", c.dataDir},
		{"config", "set", "listen", "not-an-address", "--data-dir", c.dataDir},
		{"config", "set", "public_url", "ftp://example.com", "--data-dir", c.dataDir},
		{"config", "set", "api_origin", "", "--data-dir", c.dataDir},
		{"config", "set", "backup_dir", "relative/path", "--data-dir", c.dataDir},
		{"config", "set", "unknown", "x", "--data-dir", c.dataDir},
		{"config", "set", "listen", "--data-dir", c.dataDir},
	} {
		c.expectStatus(exitUsage, "", args...)
	}
}

func TestCLIConfigSetDoesNotOverwriteBrokenFile(t *testing.T) {
	c := newTestCLI(t)
	if err := os.MkdirAll(c.dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	configPath := filepath.Join(c.dataDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{broken"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	err := c.expectStatus(exitRuntime, "", "config", "set", "listen", "127.0.0.1:9999", "--data-dir", c.dataDir)
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("error = %v", err)
	}
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(data) != "{broken" {
		t.Fatalf("broken config was rewritten: %q", data)
	}
}

func TestCLIBackupDirPrecedence(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)
	configDir := filepath.Join(t.TempDir(), "configured")
	flagDir := filepath.Join(t.TempDir(), "flagged")
	c.must("", "config", "set", "backup_dir", configDir, "--data-dir", c.dataDir)

	output := c.must("", "backup", "--data-dir", c.dataDir)
	created := strings.TrimPrefix(strings.TrimSpace(output), "备份已写入 ")
	if filepath.Dir(created) != configDir {
		t.Fatalf("default backup path = %q, want dir %q", created, configDir)
	}
	if _, err := os.Stat(created); err != nil {
		t.Fatalf("backup file: %v", err)
	}

	flagged := filepath.Join(flagDir, "custom-flag.zip")
	output = c.must("", "backup", "--backup-dir", flagDir, "--output", flagged, "--data-dir", c.dataDir)
	if got := strings.TrimPrefix(strings.TrimSpace(output), "备份已写入 "); got != flagged {
		t.Fatalf("flag backup path = %q, want %q", got, flagged)
	}

	output = c.must("", "backup", "list", "--data-dir", c.dataDir)
	if !strings.Contains(output, filepath.Base(created)) {
		t.Fatalf("backup list missing %s: %q", filepath.Base(created), output)
	}
	if strings.Contains(output, filepath.Base(flagged)) {
		t.Fatalf("backup list used flag dir instead of config: %q", output)
	}

	// restore 也必须使用 config.json 的 backup_dir。
	c.must("", "backup", "restore", "--yes", "--data-dir", c.dataDir, filepath.Base(created))
}
