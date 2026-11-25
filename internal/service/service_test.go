package service

import (
	"strings"
	"testing"
)

func TestRenderUnitUser(t *testing.T) {
	unit := renderUnit(InstallOptions{
		Scope:   ScopeUser,
		Binary:  "/opt/chat-deepseek/chat-deepseek",
		DataDir: "/home/alice/My Data",
	})
	for _, want := range []string{
		"Description=chat-deepseek",
		"WorkingDirectory=/opt/chat-deepseek",
		`ExecStart=/opt/chat-deepseek/chat-deepseek serve --data-dir "/home/alice/My Data" --no-browser`,
		"Restart=on-failure",
		"RestartSec=5",
		"TimeoutStopSec=15",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
	for _, unwanted := range []string{"User=", "Group=", "multi-user.target"} {
		if strings.Contains(unit, unwanted) {
			t.Fatalf("user unit contains %q:\n%s", unwanted, unit)
		}
	}
}

func TestRenderUnitSystem(t *testing.T) {
	unit := renderUnit(InstallOptions{
		Scope:   ScopeSystem,
		Binary:  "/srv/chat deepseek/chat-deepseek",
		DataDir: "/srv/chat data",
		RunAs:   "alice",
		Group:   "users",
	})
	for _, want := range []string{
		`WorkingDirectory="/srv/chat deepseek"`,
		`ExecStart="/srv/chat deepseek/chat-deepseek" serve --data-dir "/srv/chat data" --no-browser`,
		"User=alice",
		"Group=users",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestQuoteUnitValue(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"/home/alice", "/home/alice"},
		{"/home/alice/my data", `"/home/alice/my data"`},
		{"/home/100%", "/home/100%%"},
		{"/home/$USER", "/home/$$USER"},
		{`/home/back\slash`, `"/home/back\\slash"`},
	} {
		if got := quoteUnitValue(tc.in); got != tc.want {
			t.Fatalf("quoteUnitValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestManagerUnsupported(t *testing.T) {
	manager := &Manager{Supported: false, UnitName: UnitName}
	status, err := manager.Status(t.Context(), ScopeUser)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Supported || status.Scope != ScopeNone || status.Unit != "" {
		t.Fatalf("unsupported status = %+v", status)
	}
	if _, err := manager.Install(t.Context(), InstallOptions{Scope: ScopeUser}); err == nil {
		t.Fatal("Install succeeded on unsupported platform")
	}
	if _, err := manager.Uninstall(t.Context(), ScopeUser); err == nil {
		t.Fatal("Uninstall succeeded on unsupported platform")
	}
	for _, action := range []func() error{
		func() error { return manager.Start(t.Context(), ScopeUser) },
		func() error { return manager.Stop(t.Context(), ScopeUser) },
		func() error { return manager.Restart(t.Context(), ScopeUser) },
	} {
		if err := action(); err == nil {
			t.Fatal("service command succeeded on unsupported platform")
		}
	}
}
