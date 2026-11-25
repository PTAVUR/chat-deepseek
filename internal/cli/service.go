package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/elegantgx/chat-deepseek/internal/config"
	"github.com/elegantgx/chat-deepseek/internal/database"
	"github.com/elegantgx/chat-deepseek/internal/service"
)

// newServiceManager 便于测试替换 systemd 探测结果。
var newServiceManager = service.New

const serviceUsage = `用法：chat-deepseek service <install|uninstall|start|stop|restart|status> [--system]

  install    写入 unit 并重新加载（--enable / --disable 开机自启）
  uninstall  停止并删除 unit
  start      启动服务
  stop       停止服务
  restart    重启服务
  status     查看状态

--system 操作系统级服务，需要 root；install 还需要 --run-as 和 --data-dir。
`

func runService(args []string) error {
	if len(args) == 1 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Fprint(os.Stdout, serviceUsage)
			return nil
		}
	}
	if len(args) == 0 {
		return usagef("用法：chat-deepseek service <install|uninstall|start|stop|restart|status>")
	}
	switch args[0] {
	case "install":
		return runServiceInstall(args[1:])
	case "uninstall":
		return runServiceUninstall(args[1:])
	case "start":
		return runServiceSimple(args[1:], "start")
	case "stop":
		return runServiceSimple(args[1:], "stop")
	case "restart":
		return runServiceSimple(args[1:], "restart")
	case "status":
		return runServiceStatus(args[1:])
	default:
		return usagef("未知 service 子命令 %q；可用：install、uninstall、start、stop、restart、status", args[0])
	}
}

func runServiceInstall(args []string) error {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", "", "数据目录")
	system := fs.Bool("system", false, "安装系统级服务（需要 root）")
	enable := fs.Bool("enable", false, "设置开机自启（不启动）")
	disable := fs.Bool("disable", false, "取消开机自启")
	force := fs.Bool("force", false, "覆盖已有 unit")
	runAs := fs.String("run-as", "", "系统级服务的运行用户")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 0 {
		return usagef("service install 不接受位置参数")
	}
	if *enable && *disable {
		return usagef("service install --enable 和 --disable 不能同时使用")
	}
	if *system && strings.TrimSpace(*dataDir) == "" {
		return usagef("service install --system 需要显式 --data-dir <dir>")
	}
	mgr := newServiceManager()
	// 先判断平台能力：Windows/macOS 上不应该先报“数据目录未初始化”。
	if err := mgr.SupportError(); err != nil {
		return err
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	initialized, err := database.Initialized(dir)
	if err != nil {
		return err
	}
	if !initialized {
		return fmt.Errorf("数据目录尚未初始化：%s；先运行 chat-deepseek setup", dir)
	}
	binary, err := executablePath()
	if err != nil {
		return err
	}
	scope := service.ScopeUser
	user := ""
	if *system {
		scope = service.ScopeSystem
		user = strings.TrimSpace(*runAs)
		if user == "" {
			user = strings.TrimSpace(os.Getenv("SUDO_USER"))
		}
		if user == "" {
			return usagef("service install --system 需要 --run-as <user>（或通过 sudo 运行）")
		}
	}
	result, err := mgr.Install(context.Background(), service.InstallOptions{
		Scope:   scope,
		DataDir: dir,
		Binary:  binary,
		RunAs:   user,
		Enable:  *enable,
		Disable: *disable,
		Force:   *force,
	})
	if err != nil {
		return err
	}
	fmt.Printf("已写入 systemd unit：%s\n", result.UnitPath)
	if result.Enabled {
		fmt.Println("已设置开机自启；服务尚未启动")
	} else if *disable {
		fmt.Println("已取消开机自启")
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(os.Stderr, warning)
	}
	if *system {
		fmt.Println("下一步：chat-deepseek service start --system")
	} else {
		fmt.Println("下一步：chat-deepseek service start")
	}
	return nil
}

func runServiceUninstall(args []string) error {
	fs := flag.NewFlagSet("service uninstall", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	system := fs.Bool("system", false, "卸载系统级服务")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 0 {
		return usagef("service uninstall 不接受位置参数")
	}
	removed, err := newServiceManager().Uninstall(context.Background(), serviceScope(*system))
	if removed {
		fmt.Println("已卸载 chat-deepseek 服务；数据目录未改动")
	} else if err == nil {
		fmt.Println("服务未安装")
	}
	return err
}

func runServiceSimple(args []string, action string) error {
	fs := flag.NewFlagSet("service "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	system := fs.Bool("system", false, "操作系统级服务")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 0 {
		return usagef("service %s 不接受位置参数", action)
	}
	scope := serviceScope(*system)
	mgr := newServiceManager()
	ctx := context.Background()
	var err error
	switch action {
	case "start":
		err = mgr.Start(ctx, scope)
	case "stop":
		err = mgr.Stop(ctx, scope)
	case "restart":
		err = mgr.Restart(ctx, scope)
	}
	if err != nil {
		return err
	}
	label := "systemd user"
	if scope == service.ScopeSystem {
		label = "systemd system"
	}
	switch action {
	case "start":
		fmt.Printf("已启动 chat-deepseek 服务（%s）\n", label)
	case "stop":
		fmt.Printf("已停止 chat-deepseek 服务（%s）\n", label)
	case "restart":
		fmt.Printf("已重启 chat-deepseek 服务（%s）\n", label)
	}
	if action != "stop" {
		warnMissingLinger(mgr, scope)
	}
	return nil
}

func runServiceStatus(args []string) error {
	fs := flag.NewFlagSet("service status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	system := fs.Bool("system", false, "查询系统级服务")
	if err := fs.Parse(args); err != nil {
		return usageError(err)
	}
	if fs.NArg() != 0 {
		return usagef("service status 不接受位置参数")
	}
	status, err := newServiceManager().Status(context.Background(), serviceScope(*system))
	if err != nil {
		return err
	}
	// 不支持 systemd 也返回 0，字段里明确 supported=false。
	printServiceStatus(os.Stdout, status)
	return nil
}

func serviceScope(system bool) service.Scope {
	if system {
		return service.ScopeSystem
	}
	return service.ScopeUser
}

func warnMissingLinger(mgr *service.Manager, scope service.Scope) {
	if scope != service.ScopeUser {
		return
	}
	status, err := mgr.Status(context.Background(), scope)
	if err != nil || status.Linger {
		return
	}
	user := "<user>"
	if mgr.CurrentUser != nil {
		if name, err := mgr.CurrentUser(); err == nil && name != "" {
			user = name
		}
	}
	fmt.Fprintf(
		os.Stderr,
		"警告：未开启 linger；注销后服务会停止。以该用户执行：loginctl enable-linger %s\n",
		user,
	)
}
