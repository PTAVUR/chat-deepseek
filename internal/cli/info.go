package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/elegantgx/chat-deepseek/internal/buildinfo"
	"github.com/elegantgx/chat-deepseek/internal/config"
	"github.com/elegantgx/chat-deepseek/internal/database"
	"github.com/elegantgx/chat-deepseek/internal/service"
)

func runInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	system := fs.Bool("system", false, "查询系统级服务（默认用户级）")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 0 {
		return usagef("info 不接受位置参数")
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	// 只读：Config 读取不会创建 config.json，Initialized 不会创建 DB/WAL。
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	backupDir, err := config.ResolveBackupDir(dir, cfg.BackupDir)
	if err != nil {
		return err
	}
	initialized, err := database.Initialized(dir)
	if err != nil {
		return err
	}
	binary, err := executablePath()
	if err != nil {
		return err
	}
	scope := service.ScopeUser
	if *system {
		scope = service.ScopeSystem
	}
	status, err := newServiceManager().Status(context.Background(), scope)
	if err != nil {
		return err
	}
	fmt.Printf("version=%s\n", buildinfo.Version)
	fmt.Printf("os=%s\n", runtime.GOOS)
	fmt.Printf("arch=%s\n", runtime.GOARCH)
	fmt.Printf("binary=%s\n", binary)
	fmt.Printf("data_dir=%s\n", dir)
	fmt.Printf("initialized=%t\n", initialized)
	fmt.Printf("listen=%s\n", cfg.Listen)
	fmt.Printf("public_url=%s\n", cfg.PublicURL)
	fmt.Printf("api_origin=%s\n", cfg.APIOrigin)
	fmt.Printf("backup_dir=%s\n", backupDir)
	printServiceStatus(os.Stdout, status)
	return nil
}

// printServiceStatus 输出 info 和 service status 共用的固定字段顺序。
func printServiceStatus(w io.Writer, status service.Status) {
	fmt.Fprintf(w, "service_supported=%t\n", status.Supported)
	fmt.Fprintf(w, "service_scope=%s\n", status.Scope)
	fmt.Fprintf(w, "service_installed=%t\n", status.Installed)
	fmt.Fprintf(w, "service_active=%t\n", status.Active)
	fmt.Fprintf(w, "service_enabled=%t\n", status.Enabled)
	fmt.Fprintf(w, "service_unit=%s\n", status.Unit)
	fmt.Fprintf(w, "service_linger=%t\n", status.Linger)
}

func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位当前二进制: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析二进制路径: %w", err)
	}
	return abs, nil
}
