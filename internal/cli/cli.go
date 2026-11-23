// Package cli 实现 chat-deepseek 的命令行入口。
package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/auth"
	"github.com/elegantgx/chat-deepseek/internal/buildinfo"
	"github.com/elegantgx/chat-deepseek/internal/config"
	"github.com/elegantgx/chat-deepseek/internal/database"
	"github.com/elegantgx/chat-deepseek/internal/filestore"
	"github.com/elegantgx/chat-deepseek/internal/secret"
	"github.com/elegantgx/chat-deepseek/internal/server"
)

// Run 执行命令行；返回错误由 main 打印。
func Run(args []string) error {
	if len(args) == 0 {
		return runDefault()
	}
	switch args[0] {
	case "setup":
		return runSetup(args[1:])
	case "serve":
		return runServe(args[1:])
	case "user":
		return runUser(args[1:])
	case "backup":
		return runBackup(args[1:])
	case "version", "--version", "-v":
		fmt.Printf("chat-deepseek %s %s/%s\n", buildinfo.Version, runtime.GOOS, runtime.GOARCH)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("未知命令 %q；运行 chat-deepseek help 查看用法", args[0])
	}
}

func printUsage() {
	fmt.Print(`chat-deepseek

用法：
  chat-deepseek                 首次运行 setup，之后启动服务并打开浏览器
  chat-deepseek setup           创建第一个 Owner
  chat-deepseek serve           启动 HTTP 服务
  chat-deepseek backup          停止服务后把 data/ 打包成 ZIP（默认写到 data/backups/）
  chat-deepseek backup list     列出备份地址里的 ZIP
  chat-deepseek backup restore  恢复备份，覆盖 config.json、chat.db、master.key、files/
  chat-deepseek user ...        管理账户/组（仅服务端 CLI）
  chat-deepseek version         版本信息

常用参数：
  --data-dir <dir>              数据目录，默认是二进制同级的 data/
  --listen <addr>               监听地址，默认 127.0.0.1:8090
  --public-url <url>            对外访问地址（HTTPS 反代时用于 Secure Cookie）
  --api-origin <url>            DeepSeek API 地址，默认 https://api.deepseek.com
  --no-browser                  不自动打开浏览器（serve）
  --backup-dir <dir>            备份地址，默认是 data/backups（backup）
  --output <file>               备份输出文件路径（backup，覆盖默认文件名）
  --yes                         跳过确认（backup restore）
  --password-stdin              从标准输入读取密码（setup/user add/user reset-password）

首次使用：
  ./chat-deepseek setup --username owner
  ./chat-deepseek serve
`)
}

func runDefault() error {
	dir, err := config.ResolveDataDir("")
	if err != nil {
		return err
	}
	if _, err := os.Stat(config.DatabasePath(dir)); errors.Is(err, os.ErrNotExist) {
		if err := runSetup(nil); err != nil {
			return err
		}
	}
	return runServe(nil)
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	listen := fs.String("listen", "", "监听地址")
	username := fs.String("username", "", "Owner 用户名")
	passwordStdin := fs.Bool("password-stdin", false, "从标准输入读取密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("setup 不接受位置参数")
	}

	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	if err := config.EnsureDataDir(dir); err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = strings.TrimSpace(*listen)
	}
	db, err := database.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	accounts := account.NewStore(db)
	count, err := accounts.CountActiveOwners(context.Background())
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.New("这个数据目录已经初始化过（存在活动 Owner）；如需新增用户请用 chat-deepseek user add")
	}

	name := strings.TrimSpace(*username)
	if name == "" {
		name, err = promptLine("Owner 用户名: ")
		if err != nil {
			return err
		}
	}
	if err := account.ValidateUsername(name); err != nil {
		return fmt.Errorf("用户名需为 1-64 个字母、数字或 . _ -: %w", err)
	}
	password, err := readNewPassword(*passwordStdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("密码不符合要求: %w", err)
	}
	user, err := accounts.Create(
		context.Background(),
		name,
		hash,
		[]account.Group{account.GroupUser, account.GroupOwner},
	)
	if err != nil {
		return err
	}
	if err := config.Save(dir, cfg); err != nil {
		return err
	}
	fmt.Printf("已创建 Owner：%s（id=%s）\n", user.Username, user.ID)
	fmt.Printf("数据目录：%s\n", dir)
	fmt.Printf("下一步：%s serve\n", filepath.Base(os.Args[0]))
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	listen := fs.String("listen", "", "监听地址")
	publicURL := fs.String("public-url", "", "对外访问地址，例如 https://chat.example.com")
	apiOrigin := fs.String("api-origin", "", "DeepSeek API 地址")
	noBrowser := fs.Bool("no-browser", false, "不自动打开浏览器")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("serve 不接受位置参数")
	}

	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = strings.TrimSpace(*listen)
	}
	if *publicURL != "" {
		cfg.PublicURL = strings.TrimSpace(*publicURL)
	}
	if *apiOrigin != "" {
		cfg.APIOrigin = strings.TrimSpace(*apiOrigin)
	}
	db, err := database.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	accounts := account.NewStore(db)
	count, err := accounts.CountActiveOwners(context.Background())
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New("还没有活动 Owner；先运行 chat-deepseek setup")
	}

	key, err := secret.LoadOrCreate(dir)
	if err != nil {
		return err
	}
	files, err := filestore.NewStore(db, dir)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	api := server.New(cfg, db, logger, key, files)
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", cfg.Listen, err)
	}
	httpServer := &http.Server{
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// 流式响应不能设置 WriteTimeout。
	}
	url := browserURL(cfg, listener.Addr().String())
	fmt.Printf("chat-deepseek 正在监听 %s\n", listener.Addr().String())
	fmt.Printf("打开：%s\n", url)
	if !*noBrowser {
		if err := openBrowser(url); err != nil {
			logger.Warn("无法自动打开浏览器", "error", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		logger.Info("正在关闭服务")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP 服务退出: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 HTTP 服务: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务退出: %w", err)
	}
	return nil
}

func openStore(dataDir string) (string, *sql.DB, error) {
	dir, err := config.ResolveDataDir(dataDir)
	if err != nil {
		return "", nil, err
	}
	db, err := database.Open(dir)
	if err != nil {
		return "", nil, err
	}
	return dir, db, nil
}

func browserURL(cfg config.Config, listenAddr string) string {
	if cfg.PublicURL != "" {
		return strings.TrimRight(cfg.PublicURL, "/")
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "http://" + listenAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func openBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			command = exec.Command("xdg-open", target)
		} else {
			command = exec.Command("gio", "open", target)
		}
	}
	return command.Start()
}
