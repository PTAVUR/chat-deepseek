package cli

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/auth"
	"github.com/elegantgx/chat-deepseek/internal/database"
)

const (
	testOwnerPassword = "owner-password-123"
	testAlicePassword = "alice-password-123"
	testBobPassword   = "bob-password-12345"
)

type testCLI struct {
	t       *testing.T
	dataDir string
	state   string
	clock   *time.Time
}

func newTestCLI(t *testing.T) *testCLI {
	t.Helper()
	state := t.TempDir()
	root := t.TempDir()
	clock := time.Date(2026, 11, 25, 12, 0, 0, 0, time.Local)
	oldRoot := sessionRootFunc
	oldNow := timeNow
	sessionRootFunc = func() string { return state }
	timeNow = func() time.Time { return clock }
	t.Cleanup(func() {
		sessionRootFunc = oldRoot
		timeNow = oldNow
	})
	return &testCLI{
		t:       t,
		dataDir: filepath.Join(root, "data"),
		state:   state,
		clock:   &clock,
	}
}

func (c *testCLI) run(stdin string, args ...string) (string, error) {
	c.t.Helper()
	oldReader := stdinReader
	stdinReader = bufio.NewReader(strings.NewReader(stdin))
	defer func() { stdinReader = oldReader }()

	oldOut, oldErr := os.Stdout, os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		c.t.Fatalf("create pipe: %v", err)
	}
	os.Stdout, os.Stderr = writer, writer
	runErr := Run(args)
	_ = writer.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	output, _ := io.ReadAll(reader)
	_ = reader.Close()
	return string(output), runErr
}

func (c *testCLI) must(stdin string, args ...string) string {
	c.t.Helper()
	output, err := c.run(stdin, args...)
	if err != nil {
		c.t.Fatalf("run %v: %v", args, err)
	}
	return output
}

func (c *testCLI) expectStatus(code int, stdin string, args ...string) error {
	c.t.Helper()
	_, err := c.run(stdin, args...)
	if err == nil {
		c.t.Fatalf("run %v: expected exit %d, got success", args, code)
	}
	if got := ExitStatus(err); got != code {
		c.t.Fatalf("run %v: exit %d, want %d (%v)", args, got, code, err)
	}
	return err
}

func (c *testCLI) setup(username, password string) {
	c.t.Helper()
	c.must(password+"\n",
		"setup", "--data-dir", c.dataDir, "--username", username, "--password-stdin")
}

func (c *testCLI) login(username, password string) {
	c.t.Helper()
	c.must(password+"\n",
		"login", "--data-dir", c.dataDir, "--username", username, "--password-stdin")
}

func (c *testCLI) logout() {
	c.t.Helper()
	c.must("", "logout", "--data-dir", c.dataDir)
}

func (c *testCLI) addUser(username, password string, extra ...string) {
	c.t.Helper()
	args := []string{"user", "add", "--data-dir", c.dataDir, "--password-stdin"}
	args = append(args, extra...)
	args = append(args, username)
	c.must(password+"\n", args...)
}

func (c *testCLI) openDB() *sql.DB {
	c.t.Helper()
	db, err := database.Open(c.dataDir)
	if err != nil {
		c.t.Fatalf("open database: %v", err)
	}
	return db
}

func (c *testCLI) userID(db *sql.DB, username string) string {
	c.t.Helper()
	user, err := account.NewStore(db).GetByUsername(context.Background(), username)
	if err != nil {
		c.t.Fatalf("load user %s: %v", username, err)
	}
	return user.ID
}

func TestCLILoginRequired(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)

	c.expectStatus(exitAuthRequired, "", "user", "list", "--data-dir", c.dataDir)
	c.expectStatus(exitAuthRequired, "", "whoami", "--data-dir", c.dataDir)

	c.expectStatus(1, "wrong-password\n",
		"login", "--data-dir", c.dataDir, "--username", "owner", "--password-stdin")
	if _, _, ok := findSession(); ok {
		t.Fatal("failed login left a session file")
	}

	c.login("owner", testOwnerPassword)
	output := c.must("", "whoami", "--data-dir", c.dataDir)
	fields := strings.Split(strings.TrimSpace(output), "\t")
	if len(fields) != 3 || fields[0] != "owner" {
		t.Fatalf("whoami output = %q", output)
	}
	c.must("", "user", "list", "--data-dir", c.dataDir)

	c.logout()
	c.expectStatus(exitAuthRequired, "", "whoami", "--data-dir", c.dataDir)
	// logout 是幂等的。
	c.must("", "logout", "--data-dir", c.dataDir)
}

func TestCLIPermissionMatrix(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)
	c.addUser("alice", testAlicePassword, "--group", "Admin")
	c.addUser("bob", testBobPassword)
	c.addUser("carol", "carol-password-123")
	c.logout()

	c.login("alice", testAlicePassword)
	c.must("", "user", "list", "--data-dir", c.dataDir)
	c.must("", "user", "disable", "--data-dir", c.dataDir, "bob")

	c.expectStatus(exitForbidden, "",
		"user", "grant", "--data-dir", c.dataDir, "bob", "Admin")
	c.expectStatus(exitForbidden, "",
		"user", "add", "--data-dir", c.dataDir, "--password-stdin", "--group", "Admin", "dave")
	c.expectStatus(exitForbidden, "",
		"user", "disable", "--data-dir", c.dataDir, "owner")
	c.expectStatus(exitForbidden, "",
		"user", "reset-password", "--data-dir", c.dataDir, "owner")
	c.expectStatus(exitForbidden, "",
		"user", "delete", "--data-dir", c.dataDir, "alice")
	c.logout()

	err := c.expectStatus(1, testBobPassword+"\n",
		"login", "--data-dir", c.dataDir, "--username", "bob", "--password-stdin")
	if !strings.Contains(err.Error(), "禁用") {
		t.Fatalf("disabled login error = %v", err)
	}

	c.expectStatus(exitForbidden, "carol-password-123\n",
		"login", "--data-dir", c.dataDir, "--username", "carol", "--password-stdin")
}

func TestCLISessionExpiry(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)
	c.must("", "user", "list", "--data-dir", c.dataDir)

	*c.clock = c.clock.Add(cliSessionTTL + time.Minute)
	c.expectStatus(exitAuthRequired, "", "user", "list", "--data-dir", c.dataDir)
	c.expectStatus(exitAuthRequired, "", "whoami", "--data-dir", c.dataDir)
	if _, _, ok := findSession(); ok {
		t.Fatal("expired session file was not removed")
	}

	// 过期后可以重新登录。
	c.login("owner", testOwnerPassword)
	c.must("", "user", "list", "--data-dir", c.dataDir)
}

func TestCLIDataDirMismatch(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)
	other := filepath.Join(filepath.Dir(c.dataDir), "other")

	c.expectStatus(exitAuthRequired, "",
		"user", "list", "--data-dir", other)
	c.expectStatus(exitAuthRequired, "",
		"whoami", "--data-dir", other)
	c.expectStatus(exitAuthRequired, "",
		"logout", "--data-dir", other)
}

func TestCLIWebTokenCannotActAsCLI(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)

	db := c.openDB()
	defer func() { _ = db.Close() }()
	userID := c.userID(db, "owner")
	token, tokenHash, csrf, err := auth.NewSessionSecrets()
	if err != nil {
		t.Fatalf("new session secrets: %v", err)
	}
	session, err := auth.NewSessionStore(db).Create(context.Background(), auth.SessionParams{
		UserID:    userID,
		TokenHash: tokenHash,
		CSRF:      csrf,
		Scope:     auth.ScopeWeb,
		TTL:       time.Hour,
	}, *c.clock)
	if err != nil {
		t.Fatalf("create web session: %v", err)
	}
	host, ok := currentHost()
	if !ok {
		t.Fatal("no host process")
	}
	if err := writeSession(host, &cliSession{
		DataDir:   c.dataDir,
		UserID:    userID,
		Username:  "owner",
		Token:     token,
		ExpiresAt: session.ExpiresAt,
	}); err != nil {
		t.Fatalf("write session: %v", err)
	}
	defer removeSession(host)

	c.expectStatus(exitAuthRequired, "", "user", "list", "--data-dir", c.dataDir)
}

func TestCLIDisabledAccountLosesSession(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)
	c.addUser("alice", testAlicePassword, "--group", "Admin")
	c.logout()
	c.login("alice", testAlicePassword)

	db := c.openDB()
	aliceID := c.userID(db, "alice")
	if err := account.NewStore(db).SetDisabled(context.Background(), aliceID, true); err != nil {
		t.Fatalf("disable alice: %v", err)
	}
	_ = db.Close()

	c.expectStatus(exitAuthRequired, "", "user", "list", "--data-dir", c.dataDir)
	c.expectStatus(1, testAlicePassword+"\n",
		"login", "--data-dir", c.dataDir, "--username", "alice", "--password-stdin")
}

func TestCLISessionFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 Chmod 语义不同")
	}
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)

	host, ok := currentHost()
	if !ok {
		t.Fatal("no host process")
	}
	path := sessionPath(host)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(sessionDir())
	if err != nil {
		t.Fatalf("stat session dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("session dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
}

func TestCLIBackupPermissions(t *testing.T) {
	c := newTestCLI(t)
	c.setup("owner", testOwnerPassword)
	c.login("owner", testOwnerPassword)
	c.addUser("alice", testAlicePassword, "--group", "Admin")
	c.logout()

	c.login("alice", testAlicePassword)
	c.expectStatus(exitForbidden, "", "backup", "list", "--data-dir", c.dataDir)
	c.expectStatus(exitForbidden, "", "backup", "--data-dir", c.dataDir)
	c.expectStatus(exitForbidden, "",
		"backup", "restore", "--yes", "--data-dir", c.dataDir, "missing.zip")
	c.logout()

	c.login("owner", testOwnerPassword)
	c.must("", "backup", "list", "--data-dir", c.dataDir)
	// 备份文件不存在，应该是普通错误而不是权限错误。
	_, err := c.run("", "backup", "restore", "--yes", "--data-dir", c.dataDir, "missing.zip")
	if err == nil || ExitStatus(err) != 1 {
		t.Fatalf("owner restore missing archive = %v, want exit 1", err)
	}
}

func TestCLIBackupRestoreBootstrapNeedsNoLogin(t *testing.T) {
	c := newTestCLI(t)
	// 数据目录没有数据库：等同于首次部署，恢复不能要求登录。
	_, err := c.run("", "backup", "restore", "--yes", "--data-dir", c.dataDir, "missing.zip")
	if err == nil {
		t.Fatal("restore missing archive succeeded")
	}
	status := ExitStatus(err)
	if status == exitAuthRequired || status == exitForbidden {
		t.Fatalf("bootstrap restore was gated by auth: %v", err)
	}
}
