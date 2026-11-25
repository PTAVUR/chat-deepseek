package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// cliSessionVersion 是 CLI 会话文件的格式版本。
const cliSessionVersion = 1

// cliSession 是写在用户运行时目录里的登录态；数据库只存 token 哈希。
type cliSession struct {
	Version   int    `json:"version"`
	DataDir   string `json:"data_dir"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// hostKey 标识持有登录态的宿主进程（通常是发起 login 的 shell）。
// PID 会被系统回收，因此同时记录进程启动时间。
type hostKey struct {
	PID   int
	Start uint64
}

func (h hostKey) filename() string {
	return fmt.Sprintf("%d-%d.json", h.PID, h.Start)
}

// sessionRootFunc 便于测试替换；生产环境按平台选择目录。
var sessionRootFunc = defaultSessionRoot

func sessionRoot() string {
	return sessionRootFunc()
}

func sessionDir() string {
	return filepath.Join(sessionRoot(), "sessions")
}

// defaultSessionRoot 返回第一个可写的会话目录。
// Linux 优先 tmpfs；容器或只读运行时目录下自动退到状态目录、缓存目录或临时目录。
func defaultSessionRoot() string {
	candidates := sessionRootCandidates()
	for _, root := range candidates {
		if ensureSessionDir(root) {
			return root
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return filepath.Join(os.TempDir(), "chat-deepseek")
}

func sessionRootCandidates() []string {
	var roots []string
	add := func(base string) {
		if base != "" {
			roots = append(roots, filepath.Join(base, "chat-deepseek"))
		}
	}
	switch runtime.GOOS {
	case "linux":
		add(os.Getenv("XDG_RUNTIME_DIR"))
		add(os.Getenv("XDG_STATE_HOME"))
		if home, err := os.UserHomeDir(); err == nil {
			add(filepath.Join(home, ".local", "state"))
		}
	case "darwin":
		add(os.TempDir())
	case "windows":
		if dir, err := os.UserCacheDir(); err == nil {
			roots = append(roots, filepath.Join(dir, "chat-deepseek", "run"))
		}
	}
	if dir, err := os.UserCacheDir(); err == nil {
		roots = append(roots, filepath.Join(dir, "chat-deepseek"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".chat-deepseek"))
	}
	roots = append(roots, filepath.Join(os.TempDir(), tempSessionName()))
	return roots
}

func tempSessionName() string {
	if uid := os.Getuid(); uid >= 0 {
		return fmt.Sprintf("chat-deepseek-%d", uid)
	}
	return "chat-deepseek"
}

// ensureSessionDir 确认目录属于当前用户且权限为 0700。
// Chmod 失败通常意味着目录是别人预建的，直接跳到下一个候选。
func ensureSessionDir(root string) bool {
	dir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	return os.Chmod(dir, 0o700) == nil
}

func sessionPath(host hostKey) string {
	return filepath.Join(sessionDir(), host.filename())
}

// readSession 读取指定宿主进程的会话文件。
func readSession(host hostKey) (*cliSession, error) {
	data, err := os.ReadFile(sessionPath(host))
	if err != nil {
		return nil, err
	}
	var session cliSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parse session file: %w", err)
	}
	if session.Version != cliSessionVersion || session.Token == "" || session.DataDir == "" {
		return nil, errors.New("invalid session file")
	}
	return &session, nil
}

// writeSession 原子写入会话文件，权限 0600。
func writeSession(host hostKey, session *cliSession) error {
	dir := sessionDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	session.Version = cliSessionVersion
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	data = append(data, '\n')
	tmp := sessionPath(host) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write session: %w", err)
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, sessionPath(host)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace session: %w", err)
	}
	return nil
}

// removeSession 删除会话文件；文件不存在也算成功。
func removeSession(host hostKey) {
	_ = os.Remove(sessionPath(host))
}

// findSession 沿父进程链找到最近的登录态。
// 先由 shell 自己持有会话，管道、脚本里的命令向上找到同一个 shell。
func findSession() (*cliSession, hostKey, bool) {
	for _, host := range processChain() {
		session, err := readSession(host)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			// 损坏的会话文件直接清掉，避免挡住更上层的有效会话。
			removeSession(host)
			continue
		}
		return session, host, true
	}
	return nil, hostKey{}, false
}

// currentHost 返回发起 login 的宿主进程。
func currentHost() (hostKey, bool) {
	chain := processChain()
	if len(chain) == 0 {
		return hostKey{}, false
	}
	return chain[0], true
}

// cleanupSessions 删除已过期或无法解析的会话文件。
func cleanupSessions(now time.Time) {
	entries, err := os.ReadDir(sessionDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(sessionDir(), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var session cliSession
		if err := json.Unmarshal(data, &session); err != nil || session.ExpiresAt <= now.UnixMilli() {
			_ = os.Remove(path)
		}
	}
}
