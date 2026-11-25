package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/auth"
	"github.com/elegantgx/chat-deepseek/internal/config"
	"github.com/elegantgx/chat-deepseek/internal/database"
)

const (
	// exitRuntime 是普通运行错误。
	exitRuntime = 1
	// exitUsage 表示命令或参数用法错误。
	exitUsage = 2
	// exitAuthRequired 表示未登录、登录过期或登录态失效。
	exitAuthRequired = 3
	// exitForbidden 表示已登录但权限不足。
	exitForbidden = 4
)

// timeNow 便于测试替换。
var timeNow = time.Now

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	return e.err.Error()
}

func (e *exitError) Unwrap() error {
	return e.err
}

// ExitStatus 返回进程应使用的退出码：2 用法错误，3 需要登录，4 权限不足，其余为 1。
func ExitStatus(err error) int {
	var target *exitError
	if errors.As(err, &target) {
		return target.code
	}
	return exitRuntime
}

func usageError(err error) error {
	return &exitError{code: exitUsage, err: err}
}

func usagef(format string, args ...any) error {
	return usageError(fmt.Errorf(format, args...))
}

func authRequired(message string) error {
	return &exitError{code: exitAuthRequired, err: errors.New(message)}
}

func forbidden(message string) error {
	return &exitError{code: exitForbidden, err: errors.New(message)}
}

// operator 是一次已认证的 CLI 管理操作。
type operator struct {
	user     *account.User
	accounts *account.Store
	sessions *auth.SessionStore
	db       *sql.DB
	dataDir  string
	host     hostKey
	file     *cliSession
}

func (o *operator) close() {
	_ = o.db.Close()
}

func (o *operator) rank() int {
	return highestRank(o.user)
}

func (o *operator) require(minimum account.Group) error {
	if o.rank() >= minimum.Rank() {
		return nil
	}
	if minimum == account.GroupAdmin {
		return forbidden("需要 Admin 或 Owner 权限")
	}
	return forbidden(fmt.Sprintf("需要 %s 权限", minimum))
}

// canOperate 要求操作者的权限严格高于目标账户；Owner 可以操作任何账户。
func (o *operator) canOperate(target *account.User) error {
	if o.rank() >= account.GroupOwner.Rank() {
		return nil
	}
	if highestRank(target) >= o.rank() {
		return forbidden(fmt.Sprintf("只有 Owner 可以操作 %s 账户", target.Username))
	}
	return nil
}

func (o *operator) requireNotSelf(target *account.User, action string) error {
	if target.ID == o.user.ID {
		return forbidden(fmt.Sprintf("不能%s自己", action))
	}
	return nil
}

func highestRank(user *account.User) int {
	rank := 0
	for _, group := range user.Groups {
		if value := group.Rank(); value > rank {
			rank = value
		}
	}
	return rank
}

// authorize 解析当前终端最近一次 login，并校验最小权限。
func authorize(dataDirFlag string, minimum account.Group) (*operator, error) {
	dir, err := config.ResolveDataDir(dataDirFlag)
	if err != nil {
		return nil, err
	}
	file, host, ok := findSession()
	if !ok {
		return nil, authRequired("需要先登录：chat-deepseek login")
	}
	if !samePath(file.DataDir, dir) {
		return nil, authRequired(fmt.Sprintf(
			"当前登录的数据目录是 %s，本次命令指向 %s；请重新 login",
			file.DataDir,
			dir,
		))
	}
	now := timeNow()
	if file.ExpiresAt <= now.UnixMilli() {
		removeSession(host)
		return nil, authRequired("登录已过期，请重新 login")
	}

	db, err := openExistingDatabase(dir)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	sessions := auth.NewSessionStore(db)
	session, err := sessions.GetByTokenHash(ctx, auth.ScopeCLI, auth.HashToken(file.Token))
	if err != nil {
		_ = db.Close()
		removeSession(host)
		return nil, authRequired("登录已失效，请重新 login")
	}
	if session.ExpiresAt <= now.UnixMilli() {
		_ = sessions.Delete(ctx, session.ID)
		_ = db.Close()
		removeSession(host)
		return nil, authRequired("登录已过期，请重新 login")
	}
	accounts := account.NewStore(db)
	user, err := accounts.GetByID(ctx, session.UserID)
	if err != nil || user.Disabled {
		_ = db.Close()
		removeSession(host)
		return nil, authRequired("登录已失效，请重新 login")
	}

	result := &operator{
		user:     user,
		accounts: accounts,
		sessions: sessions,
		db:       db,
		dataDir:  dir,
		host:     host,
		file:     file,
	}
	if err := result.require(minimum); err != nil {
		result.close()
		return nil, err
	}
	return result, nil
}

// openExistingStore 打开已初始化的数据目录；不会凭空创建数据库。
func openExistingStore(dataDirFlag string) (string, *sql.DB, error) {
	dir, err := config.ResolveDataDir(dataDirFlag)
	if err != nil {
		return "", nil, err
	}
	db, err := openExistingDatabase(dir)
	if err != nil {
		return "", nil, err
	}
	return dir, db, nil
}

// samePath 在 Windows 上按大小写不敏感比较，避免同一目录被当成两个实例。
func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func openExistingDatabase(dir string) (*sql.DB, error) {
	if _, err := os.Stat(config.DatabasePath(dir)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("数据目录未初始化：%s；先运行 setup 或 backup restore", dir)
		}
		return nil, fmt.Errorf("检查数据库文件: %w", err)
	}
	db, err := database.Open(dir)
	if err != nil {
		return nil, err
	}
	return db, nil
}
