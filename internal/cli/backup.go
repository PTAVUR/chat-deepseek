package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"gitlab.com/elegantgx/chat-deepseek/internal/backup"
	"gitlab.com/elegantgx/chat-deepseek/internal/config"
)

func runBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	output := fs.String("output", "", "输出 ZIP 路径，默认是 data/backups/chat-deepseek-<时间>.zip")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("backup 不接受位置参数")
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	path, err := backup.Create(dir, *output)
	if err != nil {
		return err
	}
	fmt.Printf("备份已写入 %s\n", path)
	return nil
}
