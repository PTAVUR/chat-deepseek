package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/elegantgx/chat-deepseek/internal/account"
	"github.com/elegantgx/chat-deepseek/internal/auth"
)

type groupList []account.Group

func (g *groupList) String() string {
	items := make([]string, 0, len(*g))
	for _, group := range *g {
		items = append(items, string(group))
	}
	return strings.Join(items, ",")
}

func (g *groupList) Set(value string) error {
	group, ok := account.ParseGroup(value)
	if !ok {
		return fmt.Errorf("未知组 %q（可用：User、Admin、Owner）", value)
	}
	*g = append(*g, group)
	return nil
}

func runUser(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：chat-deepseek user <add|list|groups|grant|revoke|disable|enable|reset-password|delete>")
	}
	switch args[0] {
	case "add":
		return runUserAdd(args[1:])
	case "list":
		return runUserList(args[1:])
	case "groups":
		return runUserGroups(args[1:])
	case "grant":
		return runUserGroup(args[1:], true)
	case "revoke":
		return runUserGroup(args[1:], false)
	case "disable":
		return runUserDisabled(args[1:], true)
	case "enable":
		return runUserDisabled(args[1:], false)
	case "reset-password":
		return runUserResetPassword(args[1:])
	case "delete":
		return runUserDelete(args[1:])
	default:
		return fmt.Errorf("未知 user 子命令 %q", args[0])
	}
}

func runUserAdd(args []string) error {
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	passwordStdin := fs.Bool("password-stdin", false, "从标准输入读取密码")
	var groups groupList
	fs.Var(&groups, "group", "附加组，可重复（User/Admin/Owner）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：chat-deepseek user add [--group Admin] <username>")
	}
	username := fs.Arg(0)
	if err := account.ValidateUsername(username); err != nil {
		return fmt.Errorf("用户名需为 1-64 个字母、数字或 . _ -: %w", err)
	}
	password, err := readNewPassword(*passwordStdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, db, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	store := account.NewStore(db)
	user, err := store.Create(context.Background(), username, hash, groups)
	if err != nil {
		return err
	}
	fmt.Printf("已创建账户：%s（id=%s，组=%s）\n", user.Username, user.ID, formatGroups(user.Groups))
	return nil
}

func runUserList(args []string) error {
	fs := flag.NewFlagSet("user list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, db, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	users, err := account.NewStore(db).List(context.Background())
	if err != nil {
		return err
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "USERNAME\tGROUPS\tDISABLED\tID")
	for _, user := range users {
		fmt.Fprintf(
			writer,
			"%s\t%s\t%t\t%s\n",
			user.Username,
			formatGroups(user.Groups),
			user.Disabled,
			user.ID,
		)
	}
	return writer.Flush()
}

func runUserGroups(args []string) error {
	fs := flag.NewFlagSet("user groups", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：chat-deepseek user groups <username>")
	}
	store, closeDB, err := openAccountStore(*dataDir)
	if err != nil {
		return err
	}
	defer closeDB()
	user, err := store.GetByUsername(context.Background(), fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Println(formatGroups(user.Groups))
	return nil
}

func runUserGroup(args []string, add bool) error {
	fs := flag.NewFlagSet("user grant/revoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("用法：chat-deepseek user grant|revoke <username> <User|Admin|Owner>")
	}
	group, ok := account.ParseGroup(fs.Arg(1))
	if !ok {
		return fmt.Errorf("未知组 %q", fs.Arg(1))
	}
	store, closeDB, err := openAccountStore(*dataDir)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx := context.Background()
	user, err := store.GetByUsername(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if add {
		err = store.AddGroup(ctx, user.ID, group)
	} else {
		err = store.RemoveGroup(ctx, user.ID, group)
	}
	if err != nil {
		return err
	}
	updated, err := store.GetByID(ctx, user.ID)
	if err != nil {
		return err
	}
	fmt.Printf("%s 的组：%s\n", updated.Username, formatGroups(updated.Groups))
	return nil
}

func runUserDisabled(args []string, disabled bool) error {
	fs := flag.NewFlagSet("user disable/enable", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：chat-deepseek user disable|enable <username>")
	}
	store, closeDB, err := openAccountStore(*dataDir)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx := context.Background()
	user, err := store.GetByUsername(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := store.SetDisabled(ctx, user.ID, disabled); err != nil {
		return err
	}
	state := "已启用"
	if disabled {
		state = "已禁用"
	}
	fmt.Printf("%s %s\n", user.Username, state)
	return nil
}

func runUserResetPassword(args []string) error {
	fs := flag.NewFlagSet("user reset-password", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	passwordStdin := fs.Bool("password-stdin", false, "从标准输入读取密码")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：chat-deepseek user reset-password <username>")
	}
	password, err := readNewPassword(*passwordStdin)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	_, db, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	store := account.NewStore(db)
	user, err := store.GetByUsername(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := store.SetPassword(ctx, user.ID, hash); err != nil {
		return err
	}
	if err := auth.NewSessionStore(db).DeleteByUser(ctx, user.ID); err != nil {
		return err
	}
	fmt.Printf("已重置 %s 的密码，并吊销其全部登录会话\n", user.Username)
	return nil
}

func runUserDelete(args []string) error {
	fs := flag.NewFlagSet("user delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	yes := fs.Bool("yes", false, "跳过确认")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：chat-deepseek user delete [--yes] <username>")
	}
	store, closeDB, err := openAccountStore(*dataDir)
	if err != nil {
		return err
	}
	defer closeDB()
	ctx := context.Background()
	user, err := store.GetByUsername(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if !*yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("非交互环境删除账户需要 --yes")
		}
		confirmation, err := promptLine(fmt.Sprintf("输入用户名 %s 确认删除: ", user.Username))
		if err != nil {
			return err
		}
		if confirmation != user.Username {
			return errors.New("用户名不匹配，已取消")
		}
	}
	if err := store.Delete(ctx, user.ID); err != nil {
		return err
	}
	fmt.Printf("已删除账户 %s；其附件文件不会自动删除，留在数据目录中\n", user.Username)
	return nil
}

func formatGroups(groups []account.Group) string {
	items := make([]string, 0, len(groups))
	for _, group := range groups {
		items = append(items, string(group))
	}
	return strings.Join(items, ",")
}

func openAccountStore(dataDir string) (*account.Store, func(), error) {
	_, db, err := openStore(dataDir)
	if err != nil {
		return nil, nil, err
	}
	return account.NewStore(db), func() { _ = db.Close() }, nil
}
