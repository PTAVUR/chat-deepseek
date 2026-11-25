package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elegantgx/chat-deepseek/internal/config"
	"github.com/elegantgx/chat-deepseek/internal/service"
)

func useServiceManager(t *testing.T, manager *service.Manager) {
	t.Helper()
	previous := newServiceManager
	newServiceManager = func() *service.Manager { return manager }
	t.Cleanup(func() { newServiceManager = previous })
}

// parseKeyValues 解析 info / service status 的固定 key=value 输出。
func parseKeyValues(t *testing.T, output string) ([]string, map[string]string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	keys := make([]string, 0, len(lines))
	values := make(map[string]string, len(lines))
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("line %q is not key=value", line)
		}
		keys = append(keys, key)
		values[key] = value
	}
	return keys, values
}

func TestCLIInfoUninitialized(t *testing.T) {
	c := newTestCLI(t)
	useServiceManager(t, &service.Manager{Supported: false, UnitName: service.UnitName})

	output := c.must("", "info", "--data-dir", c.dataDir)
	keys, values := parseKeyValues(t, output)
	wantKeys := []string{
		"version", "os", "arch", "binary", "data_dir", "initialized",
		"listen", "public_url", "api_origin", "backup_dir",
		"service_supported", "service_scope", "service_installed", "service_active",
		"service_enabled", "service_unit", "service_linger",
	}
	if got := strings.Join(keys, ","); got != strings.Join(wantKeys, ",") {
		t.Fatalf("info keys = %v, want %v", keys, wantKeys)
	}
	if values["initialized"] != "false" {
		t.Fatalf("initialized = %q", values["initialized"])
	}
	if values["listen"] != config.Default().Listen {
		t.Fatalf("listen = %q", values["listen"])
	}
	if values["api_origin"] != config.Default().APIOrigin {
		t.Fatalf("api_origin = %q", values["api_origin"])
	}
	if values["public_url"] != "" {
		t.Fatalf("public_url = %q", values["public_url"])
	}
	if want := filepath.Join(c.dataDir, "backups"); values["backup_dir"] != want {
		t.Fatalf("backup_dir = %q, want %q", values["backup_dir"], want)
	}
	if values["service_supported"] != "false" || values["service_scope"] != "none" {
		t.Fatalf("service fields = %v", values)
	}
	if values["service_unit"] != "" {
		t.Fatalf("service_unit = %q", values["service_unit"])
	}
	// 只读命令不能创建 DB 或 config.json。
	for _, name := range []string{"chat.db", "chat.db-wal", "chat.db-shm", "config.json"} {
		if _, err := os.Stat(filepath.Join(c.dataDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("info created %s", name)
		}
	}
}

func TestCLIInfoInitializedAndConfig(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	custom := filepath.Join(t.TempDir(), "backups")
	c.must("", "config", "set", "listen", "0.0.0.0:9999", "--data-dir", c.dataDir)
	c.must("", "config", "set", "public_url", "https://chat.example.com", "--data-dir", c.dataDir)
	c.must("", "config", "set", "backup_dir", custom, "--data-dir", c.dataDir)
	useServiceManager(t, &service.Manager{Supported: false, UnitName: service.UnitName})

	_, values := parseKeyValues(t, c.must("", "info", "--data-dir", c.dataDir))
	if values["initialized"] != "true" {
		t.Fatalf("initialized = %q", values["initialized"])
	}
	if values["listen"] != "0.0.0.0:9999" {
		t.Fatalf("listen = %q", values["listen"])
	}
	if values["public_url"] != "https://chat.example.com" {
		t.Fatalf("public_url = %q", values["public_url"])
	}
	if values["backup_dir"] != custom {
		t.Fatalf("backup_dir = %q, want %q", values["backup_dir"], custom)
	}
	abs, err := filepath.Abs(c.dataDir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if values["data_dir"] != abs {
		t.Fatalf("data_dir = %q, want %q", values["data_dir"], abs)
	}
	if values["binary"] == "" || !filepath.IsAbs(values["binary"]) {
		t.Fatalf("binary = %q", values["binary"])
	}
}

func TestCLIInfoSystemScope(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("service 仅 Linux systemd")
	}
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	unitDir := t.TempDir()
	useServiceManager(t, &service.Manager{
		Supported:   true,
		UnitName:    service.UnitName,
		UnitDir:     func(service.Scope) (string, error) { return unitDir, nil },
		Run:         func(context.Context, string, ...string) ([]byte, error) { return []byte("inactive"), nil },
		CurrentUser: func() (string, error) { return "alice", nil },
	})
	_, values := parseKeyValues(t, c.must("", "info", "--system", "--data-dir", c.dataDir))
	if values["service_supported"] != "true" {
		t.Fatalf("service_supported = %q", values["service_supported"])
	}
	if values["service_scope"] != "system" {
		t.Fatalf("service_scope = %q", values["service_scope"])
	}
	if values["service_unit"] != filepath.Join(unitDir, service.UnitName) {
		t.Fatalf("service_unit = %q", values["service_unit"])
	}
}

func TestCLITextContracts(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)

	whoami := c.must("", "whoami", "--data-dir", c.dataDir)
	fields := strings.Split(strings.TrimRight(whoami, "\n"), "\t")
	if len(fields) != 3 || fields[0] != "owner" || !strings.Contains(fields[1], "Owner") {
		t.Fatalf("whoami output = %q", whoami)
	}

	users := c.must("", "user", "list", "--data-dir", c.dataDir)
	lines := strings.Split(strings.TrimRight(users, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("user list output = %q", users)
	}
	header := strings.Fields(lines[0])
	wantHeader := []string{"USERNAME", "GROUPS", "DISABLED", "ID"}
	if strings.Join(header, " ") != strings.Join(wantHeader, " ") {
		t.Fatalf("user list header = %v", header)
	}
	row := strings.Fields(lines[1])
	if len(row) != 4 || (row[2] != "true" && row[2] != "false") {
		t.Fatalf("user list row = %q", lines[1])
	}

	backups := c.must("", "backup", "list", "--data-dir", c.dataDir)
	if !strings.HasPrefix(backups, "NAME\tSIZE\tTIME\n") {
		t.Fatalf("backup list header = %q", backups)
	}

	version := strings.TrimSpace(c.must("", "version"))
	if !strings.HasPrefix(version, "chat-deepseek ") || !strings.Contains(version, "/") {
		t.Fatalf("version output = %q", version)
	}

	// 用法错误统一退出 2。
	c.expectStatus(exitUsage, "", "user", "--data-dir", c.dataDir)
	c.expectStatus(exitUsage, "", "config", "--data-dir", c.dataDir)
}
