//go:build linux

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elegantgx/chat-deepseek/internal/service"
)

type cliFakeSystemd struct {
	calls   [][]string
	active  string
	enabled string
	linger  string
}

func (f *cliFakeSystemd) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	switch name {
	case "systemctl":
		for _, arg := range args {
			switch arg {
			case "is-active":
				return []byte(f.active), nil
			case "is-enabled":
				return []byte(f.enabled), nil
			}
		}
	case "loginctl":
		return []byte(f.linger), nil
	}
	return nil, nil
}

func (f *cliFakeSystemd) has(parts ...string) bool {
	for _, call := range f.calls {
		if len(call) < len(parts) {
			continue
		}
		matched := true
		for i, part := range parts {
			if call[i] != part {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func cliTestManager(t *testing.T, runner *cliFakeSystemd) (*service.Manager, string) {
	t.Helper()
	root := t.TempDir()
	return &service.Manager{
		Supported:    true,
		UnitName:     service.UnitName,
		UnitDir:      func(scope service.Scope) (string, error) { return filepath.Join(root, string(scope)), nil },
		Run:          runner.run,
		CurrentUser:  func() (string, error) { return "alice", nil },
		PrimaryGroup: func(string) (string, error) { return "users", nil },
		Euid:         func() int { return 1000 },
		CanWrite:     func(string, string) (bool, error) { return true, nil },
	}, root
}

func TestCLIServiceHelp(t *testing.T) {
	c := newTestCLI(t)
	output := c.must("", "service", "--help")
	for _, command := range []string{
		"install", "uninstall", "start", "stop", "restart", "status",
	} {
		if !strings.Contains(output, command) {
			t.Fatalf("service help missing %q: %q", command, output)
		}
	}
}

func TestCLIServiceStatusUnsupported(t *testing.T) {
	c := newTestCLI(t)
	useServiceManager(t, &service.Manager{Supported: false, UnitName: service.UnitName})

	output := c.must("", "service", "status")
	keys, values := parseKeyValues(t, output)
	wantKeys := []string{
		"service_supported", "service_scope", "service_installed", "service_active",
		"service_enabled", "service_unit", "service_linger",
	}
	if got := strings.Join(keys, ","); got != strings.Join(wantKeys, ",") {
		t.Fatalf("status keys = %v", keys)
	}
	if values["service_supported"] != "false" || values["service_scope"] != "none" {
		t.Fatalf("status = %v", values)
	}
	if values["service_unit"] != "" {
		t.Fatalf("service_unit = %q", values["service_unit"])
	}

	err := c.expectStatus(exitRuntime, "", "service", "start")
	if !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("start error = %v", err)
	}
	// 未初始化的数据目录也不能挡在平台能力之前。
	err = c.expectStatus(exitRuntime, "", "service", "install", "--data-dir", c.dataDir)
	if !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("install error = %v", err)
	}
}

func TestCLIServiceInstallUser(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	runner := &cliFakeSystemd{linger: "Linger=no"}
	manager, root := cliTestManager(t, runner)
	useServiceManager(t, manager)

	output := c.must("", "service", "install", "--enable", "--data-dir", c.dataDir)
	if !strings.Contains(output, "已写入 systemd unit") {
		t.Fatalf("install output = %q", output)
	}
	if !strings.Contains(output, "已设置开机自启") {
		t.Fatalf("enable output = %q", output)
	}
	if !strings.Contains(output, "loginctl enable-linger alice") {
		t.Fatalf("linger warning missing: %q", output)
	}
	unitPath := filepath.Join(root, "user", service.UnitName)
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if !strings.Contains(string(content), "WantedBy=default.target") {
		t.Fatalf("unit:\n%s", content)
	}
	if !runner.has("systemctl", "--user", "enable", service.UnitName) {
		t.Fatalf("enable missing, calls = %v", runner.calls)
	}
	if runner.has("systemctl", "--user", "start", service.UnitName) {
		t.Fatal("install started the service")
	}
}

func TestCLIServiceInstallDisable(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	runner := &cliFakeSystemd{linger: "Linger=yes"}
	manager, _ := cliTestManager(t, runner)
	useServiceManager(t, manager)

	output := c.must("", "service", "install", "--disable", "--data-dir", c.dataDir)
	if !strings.Contains(output, "已取消开机自启") {
		t.Fatalf("disable output = %q", output)
	}
	if !runner.has("systemctl", "--user", "disable", service.UnitName) {
		t.Fatalf("disable missing, calls = %v", runner.calls)
	}
	if runner.has("systemctl", "--user", "enable", service.UnitName) {
		t.Fatalf("unexpected enable, calls = %v", runner.calls)
	}

	err := c.expectStatus(exitUsage, "", "service", "install", "--enable", "--disable", "--data-dir", c.dataDir)
	if !strings.Contains(err.Error(), "不能同时使用") {
		t.Fatalf("both flags error = %v", err)
	}
}

func TestCLIServiceLifecycle(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	runner := &cliFakeSystemd{active: "active", enabled: "enabled", linger: "Linger=yes"}
	manager, root := cliTestManager(t, runner)
	useServiceManager(t, manager)
	unitPath := filepath.Join(root, "user", service.UnitName)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatalf("create unit dir: %v", err)
	}
	if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}

	if output := c.must("", "service", "start"); !strings.Contains(output, "已启动") {
		t.Fatalf("start output = %q", output)
	}
	if output := c.must("", "service", "status"); !strings.Contains(output, "service_active=true") {
		t.Fatalf("status output = %q", output)
	}
	if output := c.must("", "service", "restart"); !strings.Contains(output, "已重启") {
		t.Fatalf("restart output = %q", output)
	}
	if output := c.must("", "service", "stop"); !strings.Contains(output, "已停止") {
		t.Fatalf("stop output = %q", output)
	}
	if output := c.must("", "service", "uninstall"); !strings.Contains(output, "已卸载") {
		t.Fatalf("uninstall output = %q", output)
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("unit still exists: %v", err)
	}
	if output := c.must("", "service", "uninstall"); !strings.Contains(output, "未安装") {
		t.Fatalf("second uninstall output = %q", output)
	}
	for _, parts := range [][]string{
		{"systemctl", "--user", "start", service.UnitName},
		{"systemctl", "--user", "restart", service.UnitName},
		{"systemctl", "--user", "stop", service.UnitName},
		{"systemctl", "--user", "disable", service.UnitName},
	} {
		if !runner.has(parts...) {
			t.Fatalf("missing %v, calls = %v", parts, runner.calls)
		}
	}
}

func TestCLIServiceInstallSystemValidation(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	t.Setenv("SUDO_USER", "")
	runner := &cliFakeSystemd{linger: "Linger=yes"}
	manager, root := cliTestManager(t, runner)
	useServiceManager(t, manager)

	c.expectStatus(exitUsage, "",
		"service", "install", "--system", "--run-as", "alice")
	c.expectStatus(exitUsage, "",
		"service", "install", "--system", "--data-dir", c.dataDir)
	err := c.expectStatus(exitRuntime, "",
		"service", "install", "--system", "--run-as", "alice", "--data-dir", c.dataDir)
	if !strings.Contains(err.Error(), "root") {
		t.Fatalf("non-root install error = %v", err)
	}

	manager.Euid = func() int { return 0 }
	output := c.must("", "service", "install", "--system", "--run-as", "alice", "--data-dir", c.dataDir)
	if !strings.Contains(output, "service start --system") {
		t.Fatalf("system install output = %q", output)
	}
	content, err := os.ReadFile(filepath.Join(root, "system", service.UnitName))
	if err != nil {
		t.Fatalf("read system unit: %v", err)
	}
	for _, want := range []string{"User=alice", "Group=users", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("system unit missing %q:\n%s", want, content)
		}
	}
	if runner.has("systemctl", "--user", "daemon-reload") {
		t.Fatalf("system install used --user: %v", runner.calls)
	}
}

func TestCLIServiceInstallRequiresInitializedDataDir(t *testing.T) {
	c := newTestCLI(t)
	runner := &cliFakeSystemd{linger: "Linger=yes"}
	manager, root := cliTestManager(t, runner)
	useServiceManager(t, manager)
	err := c.expectStatus(exitRuntime, "", "service", "install", "--data-dir", c.dataDir)
	if !strings.Contains(err.Error(), "尚未初始化") {
		t.Fatalf("install error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "user", service.UnitName)); !os.IsNotExist(err) {
		t.Fatalf("unit written before initialization check: %v", err)
	}
}
