//go:build linux

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// New 返回 Linux 上的 systemd 管理器。systemd 未运行或 systemctl 不可用时
// Supported 为 false，命令会返回 Unsupported，但 Status 仍可安全调用。
func New() *Manager {
	return &Manager{
		Supported:    systemdAvailable(),
		UnitName:     UnitName,
		UnitDir:      defaultUnitDir,
		Run:          execRun,
		CurrentUser:  currentUser,
		PrimaryGroup: primaryGroup,
		Euid:         os.Geteuid,
		CanWrite:     canWrite,
	}
}

func systemdAvailable() bool {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}

func defaultUnitDir(scope Scope) (string, error) {
	switch scope {
	case ScopeUser:
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("定位用户配置目录: %w", err)
		}
		return filepath.Join(dir, "systemd", "user"), nil
	case ScopeSystem:
		return "/etc/systemd/system", nil
	default:
		return "", fmt.Errorf("未知服务范围 %q", scope)
	}
}

func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func currentUser() (string, error) {
	account, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("读取当前用户: %w", err)
	}
	return account.Username, nil
}

func userUnitDir(name string) (string, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(account.HomeDir, ".config", "systemd", "user"), nil
}

func primaryGroup(name string) (string, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return "", fmt.Errorf("用户 %q 不存在: %w", name, err)
	}
	group, err := user.LookupGroupId(account.Gid)
	if err != nil {
		return "", fmt.Errorf("读取用户 %q 的主组: %w", name, err)
	}
	return group.Name, nil
}

// canWrite 近似判断运行用户对数据目录的写权限，只用于安装后的 warning。
func canWrite(path, name string) (bool, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return true, nil
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return false, err
	}
	mode := info.Mode().Perm()
	if uint32(uid) == stat.Uid {
		return mode&0o200 != 0, nil
	}
	groupIDs, err := account.GroupIds()
	if err != nil {
		return false, err
	}
	for _, value := range groupIDs {
		gid, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			continue
		}
		if uint32(gid) == stat.Gid {
			return mode&0o020 != 0, nil
		}
	}
	return mode&0o002 != 0, nil
}

// Status 查询 unit 是否安装以及 systemd 的 active/enabled/linger 状态。
func (m *Manager) Status(ctx context.Context, scope Scope) (Status, error) {
	if !m.Supported || scope == ScopeNone {
		return Status{Supported: false, Scope: ScopeNone}, nil
	}
	path, err := m.unitPath(scope)
	if err != nil {
		return Status{}, err
	}
	status := Status{Supported: true, Scope: scope, Unit: path}
	if _, err := os.Stat(path); err == nil {
		status.Installed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, fmt.Errorf("检查 unit 文件: %w", err)
	}
	status.Active = m.queryUnit(ctx, scope, "is-active", "active")
	status.Enabled = strings.HasPrefix(m.queryUnitOutput(ctx, scope, "is-enabled"), "enabled")
	if scope == ScopeUser {
		status.Linger = m.linger(ctx)
	}
	return status, nil
}

func (m *Manager) queryUnit(ctx context.Context, scope Scope, verb, want string) bool {
	return m.queryUnitOutput(ctx, scope, verb) == want
}

func (m *Manager) queryUnitOutput(ctx context.Context, scope Scope, verb string) string {
	args := scopeArgs(scope)
	args = append(args, verb, m.unitName())
	out, _ := m.Run(ctx, "systemctl", args...)
	return strings.TrimSpace(string(out))
}

func (m *Manager) linger(ctx context.Context) bool {
	name, err := m.CurrentUser()
	if err != nil || name == "" {
		return false
	}
	out, err := m.Run(ctx, "loginctl", "show-user", name, "-p", "Linger")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "Linger=yes"
}

// Install 写入 unit 并执行 daemon-reload；不会启动服务。
func (m *Manager) Install(ctx context.Context, opts InstallOptions) (InstallResult, error) {
	if err := m.SupportError(); err != nil {
		return InstallResult{}, err
	}
	if opts.Scope != ScopeUser && opts.Scope != ScopeSystem {
		return InstallResult{}, errors.New("service install 需要 user 或 system 范围")
	}
	if !filepath.IsAbs(opts.Binary) {
		return InstallResult{}, fmt.Errorf("二进制路径必须是绝对路径: %s", opts.Binary)
	}
	if !filepath.IsAbs(opts.DataDir) {
		return InstallResult{}, fmt.Errorf("数据目录必须是绝对路径: %s", opts.DataDir)
	}
	opts.Binary = filepath.Clean(opts.Binary)
	opts.DataDir = filepath.Clean(opts.DataDir)

	var warnings []string
	if opts.Scope == ScopeSystem {
		if m.euid() != 0 {
			return InstallResult{}, errors.New("安装系统级服务需要 root 权限；请用 sudo 执行")
		}
		if opts.RunAs == "" {
			return InstallResult{}, errors.New("--system 需要 --run-as <user>（或用 sudo 从 SUDO_USER 推断）")
		}
		if opts.RunAs == "root" {
			return InstallResult{}, errors.New("服务不能以 root 运行；请用 --run-as 指定普通用户")
		}
		if !validRunAs(opts.RunAs) {
			return InstallResult{}, fmt.Errorf("非法的运行用户 %q", opts.RunAs)
		}
		group, err := m.PrimaryGroup(opts.RunAs)
		if err != nil {
			return InstallResult{}, err
		}
		opts.Group = group
		if m.CanWrite != nil {
			if writable, err := m.CanWrite(opts.DataDir, opts.RunAs); err == nil && !writable {
				warnings = append(warnings, fmt.Sprintf(
					"警告：%s 可能无法写入数据目录 %s；请先调整目录所有者",
					opts.RunAs,
					opts.DataDir,
				))
			}
		}
		// 用运行用户的家目录而不是当前进程（sudo 下是 root）的配置目录，
		// 否则检测不到该用户已有的用户级 unit。
		if userDir, err := userUnitDir(opts.RunAs); err == nil {
			if _, err := os.Stat(filepath.Join(userDir, m.unitName())); err == nil {
				warnings = append(warnings, fmt.Sprintf(
					"警告：同时存在用户级 unit（%s）；两个服务会争抢同一端口，不要同时启动",
					filepath.Join(userDir, m.unitName()),
				))
			}
		}
	}

	dir, err := m.UnitDir(opts.Scope)
	if err != nil {
		return InstallResult{}, err
	}
	path := filepath.Join(dir, m.unitName())
	if _, err := os.Stat(path); err == nil {
		if !opts.Force {
			return InstallResult{}, fmt.Errorf("unit 已存在：%s；如需覆盖请加 --force", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return InstallResult{}, fmt.Errorf("检查 unit 文件: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("创建 unit 目录: %w", err)
	}
	if err := os.WriteFile(path, []byte(renderUnit(opts)), 0o644); err != nil {
		return InstallResult{}, fmt.Errorf("写入 unit 文件: %w", err)
	}
	if err := m.systemctl(ctx, opts.Scope, "daemon-reload"); err != nil {
		return InstallResult{}, fmt.Errorf("unit 已写入 %s，但重新加载 systemd 失败：%w", path, err)
	}

	enabled := false
	if opts.Enable {
		if err := m.systemctl(ctx, opts.Scope, "enable", m.unitName()); err != nil {
			return InstallResult{}, fmt.Errorf("unit 已写入 %s，但 enable 失败：%w", path, err)
		}
		enabled = true
	} else if opts.Disable {
		if err := m.systemctl(ctx, opts.Scope, "disable", m.unitName()); err != nil {
			return InstallResult{}, fmt.Errorf("unit 已写入 %s，但 disable 失败：%w", path, err)
		}
	}
	if opts.Scope == ScopeUser && !m.linger(ctx) {
		name, err := m.CurrentUser()
		if err != nil {
			name = "<user>"
		}
		warnings = append(warnings, fmt.Sprintf(
			"未开启 linger：注销后服务会停止。以该用户执行：loginctl enable-linger %s",
			name,
		))
	}
	return InstallResult{UnitPath: path, Enabled: enabled, Warnings: warnings}, nil
}

// Uninstall 停止并删除 unit；未安装时返回 false, nil（幂等）。
func (m *Manager) Uninstall(ctx context.Context, scope Scope) (bool, error) {
	if err := m.SupportError(); err != nil {
		return false, err
	}
	if scope == ScopeSystem && m.euid() != 0 {
		return false, errors.New("卸载系统级服务需要 root 权限；请用 sudo 执行")
	}
	path, err := m.unitPath(scope)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("检查 unit 文件: %w", err)
	}
	// 未运行或未启用时 stop/disable 会失败，属于正常情况，忽略。
	args := scopeArgs(scope)
	_, _ = m.Run(ctx, "systemctl", append(args, "stop", m.unitName())...)
	_, _ = m.Run(ctx, "systemctl", append(args, "disable", m.unitName())...)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("删除 unit 文件: %w", err)
	}
	if err := m.systemctl(ctx, scope, "daemon-reload"); err != nil {
		return true, err
	}
	return true, nil
}

// Start 启动已安装的服务。
func (m *Manager) Start(ctx context.Context, scope Scope) error {
	if err := m.requireInstalled(scope); err != nil {
		return err
	}
	return m.systemctl(ctx, scope, "start", m.unitName())
}

// Stop 停止服务。
func (m *Manager) Stop(ctx context.Context, scope Scope) error {
	if err := m.requireInstalled(scope); err != nil {
		return err
	}
	return m.systemctl(ctx, scope, "stop", m.unitName())
}

// Restart 重启服务。
func (m *Manager) Restart(ctx context.Context, scope Scope) error {
	if err := m.requireInstalled(scope); err != nil {
		return err
	}
	return m.systemctl(ctx, scope, "restart", m.unitName())
}

func (m *Manager) requireInstalled(scope Scope) error {
	if err := m.SupportError(); err != nil {
		return err
	}
	path, err := m.unitPath(scope)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("服务未安装（%s）；先运行 chat-deepseek service install", path)
		}
		return fmt.Errorf("检查 unit 文件: %w", err)
	}
	return nil
}

func (m *Manager) systemctl(ctx context.Context, scope Scope, verb string, args ...string) error {
	command := append(scopeArgs(scope), verb)
	command = append(command, args...)
	out, err := m.Run(ctx, "systemctl", command...)
	if err != nil {
		return commandError("systemctl "+verb, out, err)
	}
	return nil
}

func scopeArgs(scope Scope) []string {
	if scope == ScopeSystem {
		return nil
	}
	return []string{"--user"}
}

// commandError 把常见的 systemd 错误翻译成可操作的中文提示；未知错误保留原文。
func commandError(action string, out []byte, err error) error {
	detail := strings.TrimSpace(string(out))
	lowered := strings.ToLower(detail)
	switch {
	case strings.Contains(lowered, "permission denied"),
		strings.Contains(lowered, "access denied"),
		strings.Contains(lowered, "interactive authentication required"),
		strings.Contains(lowered, "authentication is required"):
		return fmt.Errorf("%s 失败：权限不足（系统级操作需要 root）：%s", action, detail)
	case strings.Contains(lowered, "failed to connect to bus"),
		strings.Contains(lowered, "no medium found"):
		return fmt.Errorf("%s 失败：无法连接 systemd，确认系统由 systemd 启动且当前用户有 user manager：%s", action, detail)
	case strings.Contains(lowered, "not found"),
		strings.Contains(lowered, "no such file"):
		return fmt.Errorf("%s 失败：systemd 中找不到该服务：%s", action, detail)
	}
	if detail != "" {
		return fmt.Errorf("%s 失败：%s", action, detail)
	}
	return fmt.Errorf("%s 失败：%w", action, err)
}

// SupportError 返回 Linux 上缺少 systemd 时的可操作提示。
func (m *Manager) SupportError() error {
	if m.Supported {
		return nil
	}
	return fmt.Errorf(
		"%w；没有检测到运行中的 systemd，可继续用 chat-deepseek serve 前台运行，或交给 tmux/nohup 托管",
		ErrUnsupported,
	)
}

func (m *Manager) euid() int {
	if m.Euid == nil {
		return os.Geteuid()
	}
	return m.Euid()
}
