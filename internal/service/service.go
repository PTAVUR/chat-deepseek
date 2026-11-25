// Package service 生成并管理 systemd 服务单元。
//
// 便携模式（前台 serve / tmux / nohup）在所有平台都可用；本包只负责
// Linux systemd。Windows、macOS 和没有 systemd 的系统返回 Unsupported，
// 由 CLI 提示用户改用各自平台的 supervisor。
package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// UnitName 是固定的 systemd unit 名称。
const UnitName = "chat-deepseek.service"

// Scope 是 systemd unit 的安装范围。
type Scope string

const (
	// ScopeUser 是当前用户的 user manager（systemctl --user）。
	ScopeUser Scope = "user"
	// ScopeSystem 是系统级 systemd（需要 root）。
	ScopeSystem Scope = "system"
	// ScopeNone 表示当前平台不支持 systemd。
	ScopeNone Scope = "none"
)

// ErrUnsupported 表示当前平台或环境没有可用的 systemd。
var ErrUnsupported = errors.New("当前平台不支持 systemd 服务")

// Status 是 `service status` / `info` 的服务状态快照。
type Status struct {
	Supported bool
	Scope     Scope
	Installed bool
	Active    bool
	Enabled   bool
	Unit      string
	Linger    bool
}

// InstallOptions 是生成和安装 unit 所需的输入。
type InstallOptions struct {
	Scope   Scope
	DataDir string
	Binary  string
	// RunAs 是系统级服务的运行用户；用户级服务忽略。
	RunAs string
	// Group 是系统级服务的运行组；为空时由 Manager 查询主组。
	Group string
	// Enable 只执行 enable，不启动服务。
	Enable bool
	// Disable 取消开机自启；与 Enable 互斥。
	Disable bool
	// Force 允许覆盖已有 unit。
	Force bool
}

// InstallResult 是安装结果；Warnings 交给 CLI 打到 stderr。
type InstallResult struct {
	UnitPath string
	Enabled  bool
	Warnings []string
}

// RunFunc 执行外部命令并返回合并后的输出，便于测试替换。
type RunFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// Manager 封装 systemd 探测与调用。字段导出仅为测试替换，正常使用 New()。
type Manager struct {
	Supported bool
	UnitName  string
	UnitDir   func(Scope) (string, error)
	Run       RunFunc
	// CurrentUser 用于 linger 查询和提示。
	CurrentUser func() (string, error)
	// PrimaryGroup 查询系统级服务的运行组。
	PrimaryGroup func(user string) (string, error)
	// Euid 在 Linux 上是 os.Geteuid，测试可替换。
	Euid func() int
	// CanWrite 检查运行用户对数据目录的写权限；只用于 warning。
	CanWrite func(path, user string) (bool, error)
}

func (m *Manager) unitName() string {
	if m.UnitName != "" {
		return m.UnitName
	}
	return UnitName
}

func (m *Manager) unitPath(scope Scope) (string, error) {
	dir, err := m.UnitDir(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, m.unitName()), nil
}

// renderUnit 生成 unit 文本。测试直接断言这里的输出。
func renderUnit(opts InstallOptions) string {
	var builder strings.Builder
	builder.WriteString("[Unit]\n")
	builder.WriteString("Description=chat-deepseek\n")
	builder.WriteString("After=network-online.target\n")
	builder.WriteString("Wants=network-online.target\n\n")
	builder.WriteString("[Service]\n")
	builder.WriteString("Type=simple\n")
	fmt.Fprintf(&builder, "WorkingDirectory=%s\n", quoteUnitValue(filepath.Dir(opts.Binary)))
	fmt.Fprintf(
		&builder,
		"ExecStart=%s serve --data-dir %s --no-browser\n",
		quoteUnitValue(opts.Binary),
		quoteUnitValue(opts.DataDir),
	)
	if opts.Scope == ScopeSystem {
		fmt.Fprintf(&builder, "User=%s\n", opts.RunAs)
		fmt.Fprintf(&builder, "Group=%s\n", opts.Group)
	}
	builder.WriteString("Restart=on-failure\n")
	builder.WriteString("RestartSec=5\n")
	builder.WriteString("TimeoutStopSec=15\n\n")
	builder.WriteString("[Install]\n")
	if opts.Scope == ScopeSystem {
		builder.WriteString("WantedBy=multi-user.target\n")
	} else {
		builder.WriteString("WantedBy=default.target\n")
	}
	return builder.String()
}

// quoteUnitValue 按 systemd 规则引用一个值：含空白或引号时加双引号，
// 并转义 %（specifier）和 $（环境变量展开）。
func quoteUnitValue(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "$", "$$")
	if value != "" && !strings.ContainsAny(value, " \t\n\"'\\;") {
		return value
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

// validRunAs 拒绝会破坏 unit 语法的用户名；实际存在性由 PrimaryGroup 校验。
func validRunAs(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '.', char == '_', char == '-', char == '@':
		default:
			return false
		}
	}
	return true
}
