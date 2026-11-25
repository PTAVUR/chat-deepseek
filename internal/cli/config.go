package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elegantgx/chat-deepseek/internal/config"
)

func runConfig(args []string) error {
	if len(args) == 0 {
		return usagef("用法：chat-deepseek config set <key> <value> [--data-dir <dir>]")
	}
	switch args[0] {
	case "set":
		return runConfigSet(args[1:])
	default:
		return usagef("未知 config 子命令 %q；可用：set", args[0])
	}
}

func runConfigSet(args []string) error {
	// 位置参数和 --data-dir 允许任意顺序：`config set key value --data-dir dir`
	// 比 flag 包默认的“flag 必须在前”更符合文档。
	var dataDir string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		case arg == "--data-dir" || arg == "-data-dir":
			if i+1 >= len(args) {
				return usagef("--data-dir 需要一个目录参数")
			}
			dataDir = args[i+1]
			i++
		case strings.HasPrefix(arg, "--data-dir="):
			dataDir = strings.TrimPrefix(arg, "--data-dir=")
		case strings.HasPrefix(arg, "-") && arg != "-":
			return usagef("未知参数 %q", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		return usagef("用法：chat-deepseek config set <listen|public_url|api_origin|backup_dir> <value> [--data-dir <dir>]")
	}
	key, value := positional[0], positional[1]
	dir, err := config.ResolveDataDir(dataDir)
	if err != nil {
		return err
	}
	normalized, err := config.Set(dir, key, value)
	if err != nil {
		var invalid *config.InvalidError
		if errors.As(err, &invalid) {
			return usageError(err)
		}
		return err
	}
	switch key {
	case "listen", "api_origin":
		fmt.Printf("已更新 %s=%s；重启 chat-deepseek serve 后生效\n", key, normalized)
	case "public_url":
		if normalized == "" {
			fmt.Println("已清除 public_url；重启 chat-deepseek serve 后生效")
		} else {
			fmt.Printf("已更新 public_url=%s；重启 chat-deepseek serve 后生效\n", normalized)
		}
	case "backup_dir":
		if normalized == "" {
			fallback, err := config.ResolveBackupDir(dir, "")
			if err != nil {
				return err
			}
			fmt.Printf("已清除 backup_dir；备份将写入 %s\n", fallback)
		} else {
			fmt.Printf("已更新 backup_dir=%s；下次 backup 生效\n", normalized)
		}
	}
	return nil
}
