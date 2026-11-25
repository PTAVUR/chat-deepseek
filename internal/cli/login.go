package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/auth"
	"github.com/elegantgx/chat-deepseek/internal/config"
)

// cliSessionTTL 是 CLI 登录态的有效期；固定值，不随命令续期。
const cliSessionTTL = 15 * time.Minute

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	username := fs.String("username", "", "账户名")
	passwordStdin := fs.Bool("password-stdin", false, "从标准输入读取密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("login 不接受位置参数")
	}

	dir, db, err := openExistingStore(*dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	name := strings.TrimSpace(*username)
	if name == "" {
		name, err = promptLine("用户名: ")
		if err != nil {
			return err
		}
	}
	password, err := readLoginPassword(*passwordStdin)
	if err != nil {
		return err
	}
	if len(password) > auth.MaxPasswordLength {
		return errors.New("密码过长")
	}
	ctx := context.Background()
	accounts := account.NewStore(db)
	user, err := accounts.GetByUsername(ctx, name)
	if err != nil {
		if errors.Is(err, account.ErrUserNotFound) {
			auth.VerifyDummy(password)
			return errors.New("账户不存在")
		}
		return err
	}
	valid, err := accounts.VerifyPassword(ctx, user.ID, password)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("密码错误")
	}
	if user.Disabled {
		return errors.New("账户已被禁用")
	}
	if highestRank(user) < account.GroupAdmin.Rank() {
		return forbidden("该账户没有 CLI 管理权限")
	}
	host, ok := currentHost()
	if !ok {
		return errors.New("无法确定当前 shell 进程，不能保存登录态")
	}

	now := timeNow()
	sessions := auth.NewSessionStore(db)
	// CLI 登录次数会随使用增加，顺手清掉过期会话行，避免数据库只增不减。
	_, _ = sessions.DeleteExpired(ctx, now)
	token, tokenHash, _, err := auth.NewSessionSecrets()
	if err != nil {
		return err
	}
	session, err := sessions.Create(ctx, auth.SessionParams{
		UserID:    user.ID,
		TokenHash: tokenHash,
		Scope:     auth.ScopeCLI,
		UserAgent: "chat-deepseek cli",
		TTL:       cliSessionTTL,
	}, now)
	if err != nil {
		return err
	}
	// 同一 shell 重新登录时清掉旧会话，避免数据库里留下孤儿行。
	if previous, err := readSession(host); err == nil {
		if old, err := sessions.GetByTokenHash(ctx, auth.ScopeCLI, auth.HashToken(previous.Token)); err == nil {
			_ = sessions.Delete(ctx, old.ID)
		}
	}
	if err := writeSession(host, &cliSession{
		DataDir:   dir,
		UserID:    user.ID,
		Username:  user.Username,
		Token:     token,
		ExpiresAt: session.ExpiresAt,
	}); err != nil {
		_ = sessions.Delete(ctx, session.ID)
		return err
	}
	cleanupSessions(now)

	fmt.Printf("已登录：%s（%s）\n", user.Username, formatGroups(user.Groups))
	fmt.Printf("%s后过期：%s\n", humanDuration(cliSessionTTL), formatLocalTime(session.ExpiresAt))
	return nil
}

func runLogout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("logout 不接受位置参数")
	}
	file, host, ok := findSession()
	if !ok {
		fmt.Println("当前未登录")
		return nil
	}
	if *dataDir != "" {
		dir, err := config.ResolveDataDir(*dataDir)
		if err != nil {
			return err
		}
		if !samePath(dir, file.DataDir) {
			return authRequired(fmt.Sprintf(
				"当前登录的数据目录是 %s，本次命令指向 %s；请重新 login",
				file.DataDir,
				dir,
			))
		}
	}
	// 数据库可能已被删除或恢复，删不掉行也要清掉本地文件。
	if db, err := openExistingDatabase(file.DataDir); err == nil {
		ctx := context.Background()
		sessions := auth.NewSessionStore(db)
		if session, err := sessions.GetByTokenHash(
			ctx,
			auth.ScopeCLI,
			auth.HashToken(file.Token),
		); err == nil {
			_ = sessions.Delete(ctx, session.ID)
		}
		_ = db.Close()
	}
	removeSession(host)
	fmt.Println("已退出登录")
	return nil
}

func runWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("whoami 不接受位置参数")
	}
	file, host, ok := findSession()
	if !ok {
		return authRequired("未登录")
	}
	if *dataDir != "" {
		dir, err := config.ResolveDataDir(*dataDir)
		if err != nil {
			return err
		}
		if !samePath(dir, file.DataDir) {
			return authRequired(fmt.Sprintf(
				"当前登录的数据目录是 %s，本次命令指向 %s；请重新 login",
				file.DataDir,
				dir,
			))
		}
	}
	now := timeNow()
	if file.ExpiresAt <= now.UnixMilli() {
		removeSession(host)
		return authRequired("登录已过期，请重新 login")
	}
	db, err := openExistingDatabase(file.DataDir)
	if err != nil {
		removeSession(host)
		return authRequired("登录已失效，请重新 login")
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	sessions := auth.NewSessionStore(db)
	session, err := sessions.GetByTokenHash(ctx, auth.ScopeCLI, auth.HashToken(file.Token))
	if err != nil {
		removeSession(host)
		return authRequired("登录已失效，请重新 login")
	}
	if session.ExpiresAt <= now.UnixMilli() {
		_ = sessions.Delete(ctx, session.ID)
		removeSession(host)
		return authRequired("登录已过期，请重新 login")
	}
	user, err := account.NewStore(db).GetByID(ctx, session.UserID)
	if err != nil || user.Disabled {
		removeSession(host)
		return authRequired("登录已失效，请重新 login")
	}
	fmt.Printf(
		"%s\t%s\t%s\n",
		user.Username,
		formatGroups(user.Groups),
		formatLocalTime(session.ExpiresAt),
	)
	return nil
}

// readLoginPassword 读取登录密码；非终端输入必须显式使用 --password-stdin。
func readLoginPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := stdinReader.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("读取密码失败: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("标准输入不是终端；请用 --password-stdin 传入密码")
	}
	return readSecret("密码: ")
}

func humanDuration(value time.Duration) string {
	minutes := int(value / time.Minute)
	if minutes > 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return value.String()
}

func formatLocalTime(millis int64) string {
	return time.UnixMilli(millis).Format("2006-01-02 15:04:05")
}
