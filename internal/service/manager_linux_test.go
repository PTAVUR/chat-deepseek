//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls   [][]string
	active  string
	enabled string
	linger  string
	err     error
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.err != nil {
		return []byte("boom"), f.err
	}
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

func (f *fakeRunner) has(parts ...string) bool {
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

func testManager(t *testing.T, root string, runner *fakeRunner) *Manager {
	t.Helper()
	return &Manager{
		Supported:    true,
		UnitName:     UnitName,
		UnitDir:      func(scope Scope) (string, error) { return filepath.Join(root, string(scope)), nil },
		Run:          runner.run,
		CurrentUser:  func() (string, error) { return "alice", nil },
		PrimaryGroup: func(string) (string, error) { return "users", nil },
		Euid:         func() int { return 1000 },
		CanWrite:     func(string, string) (bool, error) { return true, nil },
	}
}

func TestInstallWritesUserUnitWithoutStarting(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{linger: "Linger=no"}
	manager := testManager(t, root, runner)
	dataDir := t.TempDir()
	binary := filepath.Join(t.TempDir(), "chat-deepseek")

	result, err := manager.Install(t.Context(), InstallOptions{
		Scope:   ScopeUser,
		DataDir: dataDir,
		Binary:  binary,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	unitPath := filepath.Join(root, "user", UnitName)
	if result.UnitPath != unitPath {
		t.Fatalf("unit path = %q, want %q", result.UnitPath, unitPath)
	}
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	wantExec := fmt.Sprintf("ExecStart=%s serve --data-dir %s --no-browser", binary, dataDir)
	if !strings.Contains(string(content), wantExec) {
		t.Fatalf("unit missing %q:\n%s", wantExec, content)
	}
	if !strings.Contains(string(content), "WantedBy=default.target") {
		t.Fatalf("unit missing user target:\n%s", content)
	}
	if !runner.has("systemctl", "--user", "daemon-reload") {
		t.Fatalf("missing daemon-reload, calls = %v", runner.calls)
	}
	if runner.has("systemctl", "--user", "start", UnitName) {
		t.Fatal("install started the service")
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "loginctl enable-linger alice") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing linger warning: %v", result.Warnings)
	}
}

func TestInstallEnableDoesNotStart(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{linger: "Linger=yes"}
	manager := testManager(t, root, runner)
	if _, err := manager.Install(t.Context(), InstallOptions{
		Scope:   ScopeUser,
		DataDir: t.TempDir(),
		Binary:  filepath.Join(t.TempDir(), "chat-deepseek"),
		Enable:  true,
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !runner.has("systemctl", "--user", "enable", UnitName) {
		t.Fatalf("missing enable, calls = %v", runner.calls)
	}
	if runner.has("systemctl", "--user", "start", UnitName) {
		t.Fatal("install --enable started the service")
	}
}

func TestInstallRefusesExistingWithoutForce(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{linger: "Linger=yes"}
	manager := testManager(t, root, runner)
	unitPath := filepath.Join(root, "user", UnitName)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatalf("create unit dir: %v", err)
	}
	if err := os.WriteFile(unitPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old unit: %v", err)
	}
	opts := InstallOptions{Scope: ScopeUser, DataDir: t.TempDir(), Binary: filepath.Join(t.TempDir(), "chat-deepseek")}
	if _, err := manager.Install(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("Install existing = %v, want 已存在", err)
	}
	opts.Force = true
	if _, err := manager.Install(t.Context(), opts); err != nil {
		t.Fatalf("Install force: %v", err)
	}
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if !strings.Contains(string(content), "ExecStart=") {
		t.Fatalf("unit was not overwritten:\n%s", content)
	}
}

func TestInstallSystemUnit(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{linger: "Linger=no"}
	manager := testManager(t, root, runner)
	manager.Euid = func() int { return 0 }
	if _, err := manager.Install(t.Context(), InstallOptions{
		Scope:   ScopeSystem,
		DataDir: t.TempDir(),
		Binary:  filepath.Join(t.TempDir(), "chat-deepseek"),
		RunAs:   "alice",
	}); err != nil {
		t.Fatalf("Install system: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "system", UnitName))
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	for _, want := range []string{"User=alice", "Group=users", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("system unit missing %q:\n%s", want, content)
		}
	}
	if !runner.has("systemctl", "daemon-reload") {
		t.Fatalf("system daemon-reload missing, calls = %v", runner.calls)
	}
	if runner.has("systemctl", "--user", "daemon-reload") {
		t.Fatalf("system install used --user: %v", runner.calls)
	}
}

func TestInstallSystemValidation(t *testing.T) {
	root := t.TempDir()
	manager := testManager(t, root, &fakeRunner{})
	opts := InstallOptions{Scope: ScopeSystem, DataDir: t.TempDir(), Binary: filepath.Join(t.TempDir(), "chat-deepseek"), RunAs: "alice"}
	if _, err := manager.Install(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("non-root system install = %v, want root error", err)
	}
	manager.Euid = func() int { return 0 }
	rootOpts := opts
	rootOpts.RunAs = "root"
	if _, err := manager.Install(t.Context(), rootOpts); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("run-as root = %v, want rejection", err)
	}
	missing := opts
	missing.RunAs = ""
	if _, err := manager.Install(t.Context(), missing); err == nil || !strings.Contains(err.Error(), "--run-as") {
		t.Fatalf("missing run-as = %v", err)
	}
}

func TestStatusParsesSystemdValues(t *testing.T) {
	root := t.TempDir()
	unitPath := filepath.Join(root, "user", UnitName)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatalf("create unit dir: %v", err)
	}
	if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	runner := &fakeRunner{active: "active\n", enabled: "enabled\n", linger: "Linger=yes\n"}
	manager := testManager(t, root, runner)
	status, err := manager.Status(t.Context(), ScopeUser)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Supported || status.Scope != ScopeUser || !status.Installed {
		t.Fatalf("status = %+v", status)
	}
	if !status.Active || !status.Enabled || !status.Linger {
		t.Fatalf("status = %+v", status)
	}
	if status.Unit != unitPath {
		t.Fatalf("unit = %q, want %q", status.Unit, unitPath)
	}
}

func TestStatusMissingUnit(t *testing.T) {
	root := t.TempDir()
	manager := testManager(t, root, &fakeRunner{active: "inactive", enabled: "disabled"})
	status, err := manager.Status(t.Context(), ScopeUser)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Installed || status.Active || status.Enabled || status.Linger {
		t.Fatalf("status = %+v", status)
	}
}

func TestUninstallRemovesUnit(t *testing.T) {
	root := t.TempDir()
	unitPath := filepath.Join(root, "user", UnitName)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatalf("create unit dir: %v", err)
	}
	if err := os.WriteFile(unitPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	runner := &fakeRunner{}
	manager := testManager(t, root, runner)
	removed, err := manager.Uninstall(t.Context(), ScopeUser)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !removed {
		t.Fatal("Uninstall reported nothing removed")
	}
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("unit still exists: %v", err)
	}
	for _, parts := range [][]string{
		{"systemctl", "--user", "stop", UnitName},
		{"systemctl", "--user", "disable", UnitName},
		{"systemctl", "--user", "daemon-reload"},
	} {
		if !runner.has(parts...) {
			t.Fatalf("missing %v, calls = %v", parts, runner.calls)
		}
	}
	removed, err = manager.Uninstall(t.Context(), ScopeUser)
	if err != nil || removed {
		t.Fatalf("second Uninstall = %v, %v", removed, err)
	}
}

func TestStartRequiresInstalledUnit(t *testing.T) {
	root := t.TempDir()
	runner := &fakeRunner{}
	manager := testManager(t, root, runner)
	if err := manager.Start(t.Context(), ScopeUser); err == nil || !strings.Contains(err.Error(), "未安装") {
		t.Fatalf("Start without unit = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("systemctl was called: %v", runner.calls)
	}
}

func TestCommandErrorTranslation(t *testing.T) {
	for _, tc := range []struct {
		detail string
		want   string
	}{
		{"Failed to connect to bus: No medium found", "无法连接 systemd"},
		{"Access denied", "权限不足"},
		{"Unit chat-deepseek.service not found.", "找不到该服务"},
		{"something unexpected", "something unexpected"},
	} {
		err := commandError("systemctl start", []byte(tc.detail), fmt.Errorf("exit status 1"))
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("commandError(%q) = %v, want %q", tc.detail, err, tc.want)
		}
	}
}

func TestDefaultUnitDirRespectsXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir, err := defaultUnitDir(ScopeUser)
	if err != nil {
		t.Fatalf("defaultUnitDir: %v", err)
	}
	if want := filepath.Join(home, "systemd", "user"); dir != want {
		t.Fatalf("unit dir = %q, want %q", dir, want)
	}
	system, err := defaultUnitDir(ScopeSystem)
	if err != nil {
		t.Fatalf("defaultUnitDir system: %v", err)
	}
	if system != "/etc/systemd/system" {
		t.Fatalf("system unit dir = %q", system)
	}
}
