# chat-deepseek GUI

`app.py` 是 `chat-deepseek` 的 Python + PySide6（Qt 6）管理界面。窗口只负责拼出
`chat-deepseek` 子命令；输出由 `parsing.py` 解析成账户列表、服务状态和提示，已知失败会翻译成可操作说明，只有无法识别时才展示原始输出。
账户、会话、备份逻辑仍在 Go 二进制里；GUI 通过 `info` 读取服务端配置和 systemd 状态，通过 `config set` 写配置，本目录不直接读写 `data/`。

## 登录与权限

- 不登录也能启动、停止、重启、安装和更新服务，以及改设置；账户区显示锁图标，备份区提示“登录后查看备份记录”。
- 点击“登录”、刷新、添加账户或任一备份按钮都会弹出登录框；登录通过 CLI `login` 完成，密码只经 stdin 传给子进程。
- 登录态绑定当前 GUI 进程，有效期 15 分钟；关闭窗口时会自动执行 `logout`。重新打开 GUI 需要重新登录。
- 只有 Admin 和 Owner 可以登录；`User` 账户会被 CLI 拒绝。Owner 多出授组、备份和操作其他 Owner 的能力。
- 界面按登录身份置灰不可执行的操作：Admin 只能管理普通 User，不能授组或备份；不能禁用/删除自己；Owner 可以操作任何账户，但仍不能禁用/删除自己。
- 登录用户名记在 `config.gui.json` 对应连接的 `username`，不保存密码。切换数据目录或恢复备份后，当前登录态失效，需要重新登录。

## SSH 远程管理

- 设置里的“连接方式”可选“本机”或“SSH 远程”。SSH 模式要求填“远端二进制目录”（绝对路径，必填），数据目录可留空使用远端 CLI 默认值；主机直接写 `~/.ssh/config` 的别名或 `user@host`。
- 只使用系统 `ssh`，只支持密钥/agent 认证：GUI 用 `BatchMode` 启动，不会弹出密码框。第一次连接前先在终端执行一次 `ssh <主机>`，让 known_hosts 记下主机密钥。
- GUI 启动后保持一个 `ssh -T <主机> 'exec /bin/sh'` 常驻通道，所有 `chat-deepseek` 命令都在这个 shell 里执行。CLI 的登录态本来就绑定发起 login 的 shell，所以 `login` 之后的 `whoami` / `user ...` / `backup ...` 共享同一个登录态，不需要改 CLI，也不需要每条命令重新登录。
- 连接断开、命令超时或登录态被清掉时，GUI 会关闭通道并提示重新登录；下一条命令会重新建立通道。
- 远端路径不在本机：路径旁不再有“浏览”，`备份到…` 也不可用（远端备份写到远端配置的备份地址）。打开页面需要远端配置了 Public URL；GUI 不做端口映射。
- 远端用户级 systemd 服务需要 linger：SSH 通道断开后，没有 linger 的 user manager 会被 logind 停掉，服务也跟着停止。服务卡片和安装对话框会提示 `loginctl enable-linger`；常驻场景应优先开启 linger，或在远端用系统级 unit。
- 远端前台 `serve` 和本地一样，关闭 GUI 或 SSH 断开就会结束；远端常驻请安装 systemd 服务。点“停止服务”会按远端 PID 发 `SIGTERM`（`ssh -T` 没有 pty，断开客户端不会给远端进程发 SIGHUP），停止失败时按钮会提示残留的 PID。
- 备份地址、监听地址、Public URL 和 API 地址都按当前主机自己的 `config.json` 读取（远端通过 `info`）；切换主机后 GUI 会重新读一遍，不会把上一台或 GUI 所在机器的值套过来。

## 服务管理

- Linux 且 systemd 可用时，服务卡片显示 systemd 服务的安装位置（用户目录）、运行、开机自启和 linger 状态；可以启动、停止、重启、安装、更新和卸载。
- 未安装 systemd 服务时，“启动服务”走原来的前台 `serve` 子进程，关闭窗口会停止；安装后启动/停止/重启改走 `service start` / `service stop` / `service restart`，关闭窗口不会停止 systemd 服务。
- “安装服务”打开对话框，用下拉框选择“用户目录”或“系统目录”。“开机自启”对两者都生效；用户目录还可以设置“安装后立即启动”。用户目录直接执行安装；系统目录需要 root，GUI 不执行，只给出“安装命令”和“CLI 帮助”两条单行字段，各自带“复制”按钮；单行显示不下时中间省略，复制的是完整命令。安装后在终端运行 CLI 帮助查看启动、停止、重启和卸载命令。
- “更新服务”按当前配置重写 unit（`service install --force`），可以调整开机自启；默认更新后重启服务，也可以关掉开关只写 unit。
- linger 行按当前状态显示 `loginctl enable-linger <user>` 或 `loginctl disable-linger <user>`，右侧可直接复制。
- Windows/macOS 和没有 systemd 的 Linux 不支持 `service`，继续使用前台会话模式；常驻请用任务计划程序/NSSM、LaunchAgent 或 tmux/nohup。

## 安装与运行

需要 Python 3.10+。在仓库根目录执行（依赖已装在 `gui/.venv`，但该目录不提交）：

```sh
python3 -m venv gui/.venv
gui/.venv/bin/pip install -i https://pypi.tuna.tsinghua.edu.cn/simple -r gui/requirements.txt
gui/.venv/bin/python gui/app.py
```

Windows PowerShell：

```powershell
py -3 -m venv gui\.venv
gui\.venv\Scripts\pip install -i https://pypi.tuna.tsinghua.edu.cn/simple -r gui\requirements.txt
gui\.venv\Scripts\python gui\app.py
```

`app.py` 默认使用 `chat-deepseek` 二进制所在目录（仓库根目录；Windows 为 `chat-deepseek.exe`），也可以在“设置”里选择。
Linux 发布包根目录的 `gui/` 就是本目录，CLI 在上一级；解压后按上面的命令执行即可，发布包不再提供 Linux GUI 二进制。
GUI 的本地配置保存在用户配置目录的 `config.gui.json`（v3）：Linux 是 `~/.config/chat-deepseek/`，macOS 是 `~/Library/Application Support/chat-deepseek/`，Windows 是 `%APPDATA%\chat-deepseek\`。v1 的顶层 `binary` / `data_dir` / `backup_dir` / `username` 和 v2 的 `local` 段会自动迁移；`backup_dir` 会通过 `config set` 写进数据目录的 `config.json`。配置里 `local` 和 `ssh` 各存一份路径，切换连接不用重新输入。
没有图形桌面的服务器直接用 CLI，不需要安装这些依赖。

## 结构

```text
app.py          主窗口、状态和操作流程
cli.py          本机子进程执行与长进程管理
ssh_session.py  SSH 常驻通道（握手、哨兵、stdin、断线与超时）
parsing.py      解析 info / user list / whoami / backup list / serve / version 输出
server_config.py 计算服务端默认路径和默认监听/API 地址
service_plan.py  生成 systemd unit 路径与 service 命令（含远端 ssh 前缀）
gui_config.py    读写 v3 config.gui.json（连接方式、local/ssh 两段路径、用户名）
dialogs.py      Qt 登录、输入、确认、路径选择、设置和服务安装/更新对话框
widgets.py      自绘卡片、按钮、输入框、状态点、组标签与成员行
fonts.py        选择 Windows/macOS/Linux 的系统 UI 字体
theme.py        明暗配色、字号与全局样式表
assets/whale.svg DeepSeek 鲸鱼标志（Simple Icons，CC0-1.0）
assets/whale.ico Windows 可执行文件图标
assets/whale.icns macOS .app 图标
assets/whale.png 512px 图标（备用）
tests/          解析器、配置、SSH 通道和 service 命令拼装测试
```

解析器、配置与 service 命令拼装测试：

```sh
gui/.venv/bin/python -m unittest discover -s gui/tests -v
```

## 注意

- 密码通过 `--password-stdin` 传给子进程，不出现在命令行参数和输出里。SSH 模式下密码走常驻 shell 的 here-doc，同样不进 argv，也不会被 shell 当成下一条命令。
- GUI 直接解析 CLI 的中文输出；CLI 提示或表格格式变化时，改 `parsing.py` 并补测试。已知失败条件（服务运行中、端口占用、最后一个 Owner、账户不存在、密码不合规等）会翻译成可操作的提示；只有完全识别不了时才显示“未知错误”和原始输出。
- 备份地址由 `config.json` 的 `backup_dir` 决定（默认数据目录下的 `backups/`），可在设置里改；设置页保存时通过 `config set backup_dir` 写入，留空表示默认。Owner 登录后会自动读取备份列表，信息栏显示最近一条；备份完成后也优先显示文件名里的时间（`chat-deepseek-YYYYMMDD-HHMMSS.zip`），名称里没有可解析的时间时显示完整路径。文件存在时同时显示大小。
- “备份记录…”读取 `backup list`，可以查看时间、文件名和大小，并恢复选中的备份；恢复前先停止服务，会覆盖 `chat.db`、`config.json`、`master.key` 和 `files/`。
- 关闭窗口会停止由本窗口启动的 `serve`，并退出当前 GUI 的 CLI 登录；systemd 服务、外部启动的进程不会被接管或停止。
- Windows 上停止会话进程是强制结束；同一个数据目录同时只能有一个服务实例。
- 备份地址、监听地址、Public URL 和 API 地址属于每台主机自己的 `data/config.json`：设置页会按所选主机读取，切到另一台时重新读那一台的（SSH 下走一次 `info`），切回来恢复当前主机的值。另一台主机的那四项只做展示、不能改：点“保存”会切换连接，切换之后才能修改。
- 设置页可改连接方式、二进制目录、数据目录、备份地址、监听地址、Public URL 和 API 地址。本机连接的二进制目录和数据目录写入 `config.gui.json` 的 `local`，SSH 主机和远端目录写入 `ssh`；监听地址、Public URL、API 地址和备份地址通过 `config set` 写入当前目标数据目录的 `config.json`；保存后重启服务生效。`config.gui.json` 是运行时文件，不提交；它会记住上次登录的用户名，但不保存密码。macOS 默认数据目录是 `~/Library/Application Support/chat-deepseek/data`。
- 切换连接方式或远端主机后，旧目标的登录态和前台 `serve` 会失效，需要重新登录；服务端配置（监听/Public URL/API/备份地址）属于旧目标，不会被顺手写到新目标。
- 字体优先使用 Microsoft YaHei UI / PingFang SC / Noto Sans CJK SC，关闭子像素抗锯齿并使用灰度抗锯齿，字号比常规桌面控件大一档。
- 窗口标题栏、边框和圆角全部使用系统原生：Windows 是 x/□/-，GNOME 只有关闭按钮，macOS 是交通灯。不再自绘标题栏、不设置 frameless 或透明窗口。对话框也使用原生标题栏，内容高度已预留标题栏。设置和命令行提示的标题不带应用名后缀。
- 账户列表是 60px 两行成员行：头像、用户名、组标签和右侧状态点；悬停/选中为长胶囊，选中头像变实心蓝，底部信息栏显示“已选择 / 取消选择”和操作。
- 卡片使用五次贝塞尔连续曲率（32px），按钮、输入框、开关用胶囊端（20px），账户悬停/选中为长胶囊。
- 界面跟随系统深浅色，并在系统主题变化时重刷配色、QSS 与原生控件调色板。启动时先读取平台调色板再切换 Fusion；GNOME 下直接读 `gsettings` 的 `color-scheme` / `gtk-theme`，避免 Qt 还没上报 `colorScheme` 时把暗色系统判成亮色，并用 `dconf watch` 跟随系统切换。
- 依赖不随仓库提交；PySide6 / Qt 的许可证见各自发布包。
- 发布包中 Windows 用 `.ico` 设置 EXE 图标；macOS 用 `.app` 承载 `.icns` 图标并打进 DMG。Linux 发布包不含 GUI 二进制；从源码运行或自行打包时，ELF 文件不携带文件图标，窗口/任务栏图标由 Qt 在运行时设置。
- Linux 需要 Qt 的 xcb 运行库；Debian/Ubuntu 若提示缺少 `xcb-cursor`，安装 `libxcb-cursor0`。
- Linux 没有图形桌面时继续用 CLI，不需要安装 PySide6。
