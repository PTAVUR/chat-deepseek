//go:build !linux

package service

import (
	"context"
	"fmt"
	"runtime"
)

// New 返回不支持 systemd 的管理器；Windows/macOS 只能前台运行 serve，
// 需要常驻时交给各自平台的 supervisor。
func New() *Manager {
	return &Manager{
		Supported: false,
		UnitName:  UnitName,
		UnitDir:   func(Scope) (string, error) { return "", ErrUnsupported },
		Run:       func(context.Context, string, ...string) ([]byte, error) { return nil, ErrUnsupported },
	}
}

// SupportError 返回当前平台不支持 systemd 时的可操作提示。
func (m *Manager) SupportError() error {
	return unsupportedError()
}

func (m *Manager) Status(context.Context, Scope) (Status, error) {
	return Status{Supported: false, Scope: ScopeNone}, nil
}

func (m *Manager) Install(context.Context, InstallOptions) (InstallResult, error) {
	return InstallResult{}, unsupportedError()
}

func (m *Manager) Uninstall(context.Context, Scope) (bool, error) {
	return false, unsupportedError()
}

func (m *Manager) Start(context.Context, Scope) error {
	return unsupportedError()
}

func (m *Manager) Stop(context.Context, Scope) error {
	return unsupportedError()
}

func (m *Manager) Restart(context.Context, Scope) error {
	return unsupportedError()
}

func unsupportedError() error {
	var guidance string
	switch runtime.GOOS {
	case "windows":
		guidance = "Windows 请用任务计划程序或 NSSM 托管 chat-deepseek serve，或直接在终端运行"
	case "darwin":
		guidance = "macOS 请用 LaunchAgent 托管 chat-deepseek serve，或直接在终端运行"
	default:
		guidance = "请用 tmux、nohup 或系统自带的服务管理器托管 chat-deepseek serve"
	}
	return fmt.Errorf("%w；%s", ErrUnsupported, guidance)
}
