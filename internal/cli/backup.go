package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/backup"
	"github.com/elegantgx/chat-deepseek/internal/config"
)

func runBackup(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return runBackupList(args[1:])
		case "restore":
			return runBackupRestore(args[1:])
		case "create":
			return runBackupCreate(args[1:])
		}
		if !strings.HasPrefix(args[0], "-") {
			return usagef("未知 backup 子命令 %q；可用：list、restore", args[0])
		}
	}
	return runBackupCreate(args)
}

func runBackupCreate(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	backupDir := fs.String("backup-dir", "", "备份地址，默认是 data/backups")
	output := fs.String("output", "", "输出 ZIP 路径（默认写到备份地址）")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 0 {
		return usagef("backup 不接受位置参数")
	}
	op, err := authorize(*dataDir, account.GroupOwner)
	if err != nil {
		return err
	}
	op.close()
	dir := op.dataDir
	backupDirectory, err := resolveBackupDir(dir, *backupDir)
	if err != nil {
		return err
	}
	path, err := backup.Create(dir, backupDirectory, *output)
	if err != nil {
		return err
	}
	fmt.Printf("备份已写入 %s\n", path)
	return nil
}

func runBackupList(args []string) error {
	fs := flag.NewFlagSet("backup list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	backupDir := fs.String("backup-dir", "", "备份地址，默认是 data/backups")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 0 {
		return usagef("backup list 不接受位置参数")
	}
	op, err := authorize(*dataDir, account.GroupOwner)
	if err != nil {
		return err
	}
	op.close()
	dir := op.dataDir
	backupDirectory, err := resolveBackupDir(dir, *backupDir)
	if err != nil {
		return err
	}
	archives, err := backup.List(backupDirectory)
	if err != nil {
		return err
	}
	fmt.Println("NAME\tSIZE\tTIME")
	for _, archive := range archives {
		fmt.Printf(
			"%s\t%s\t%s\n",
			archive.Name,
			humanSize(archive.Size),
			archive.Time.Format(time.DateTime),
		)
	}
	return nil
}

func runBackupRestore(args []string) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	backupDir := fs.String("backup-dir", "", "备份地址，默认是 data/backups")
	yes := fs.Bool("yes", false, "跳过确认")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 1 {
		return usagef("用法：chat-deepseek backup restore [--yes] <备份文件>")
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	// 空数据目录恢复是引导路径（备份里就有账户），没有可登录的身份；
	// 覆盖已有实例则必须是 Owner。
	if _, statErr := os.Stat(config.DatabasePath(dir)); statErr == nil {
		op, err := authorize(*dataDir, account.GroupOwner)
		if err != nil {
			return err
		}
		op.close()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("检查数据库文件: %w", statErr)
	}
	name := fs.Arg(0)
	if !*yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("非交互环境恢复备份需要 --yes")
		}
		confirmation, err := promptLine(fmt.Sprintf("恢复会覆盖当前数据，输入 yes 确认 %s: ", name))
		if err != nil {
			return err
		}
		if confirmation != "yes" {
			return errors.New("已取消恢复")
		}
	}
	backupDirectory, err := resolveBackupDir(dir, *backupDir)
	if err != nil {
		return err
	}
	archivePath, err := backup.Restore(dir, backupDirectory, name)
	if err != nil {
		return err
	}
	if _, host, ok := findSession(); ok {
		removeSession(host)
	}
	fmt.Printf("已恢复备份 %s；登录态已清除，请重新 login 后启动服务\n", filepath.Base(archivePath))
	return nil
}

func humanSize(size int64) string {
	switch {
	case size >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(size)/1024/1024)
	case size >= 1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// resolveBackupDir 按 --backup-dir > config.json backup_dir > data/backups 解析。
func resolveBackupDir(dataDir, flagValue string) (string, error) {
	if value := strings.TrimSpace(flagValue); value != "" {
		return filepath.Abs(value)
	}
	cfg, err := config.Load(dataDir)
	if err != nil {
		return "", err
	}
	return config.ResolveBackupDir(dataDir, cfg.BackupDir)
}
