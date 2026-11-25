# chat-deepseek GUI

`app.py` 是 `chat-deepseek` 的 Python + PySide6（Qt 6）管理界面。窗口只负责拼出
`chat-deepseek` 子命令；输出由 `parsing.py` 解析成账户列表、服务状态和提示，已知失败会翻译成可操作说明，只有无法识别时才展示原始输出。
账户、会话、备份逻辑仍在 Go 二进制里，本目录不读写 `data/`。

## 登录与权限

- 不登录也能启动、停止服务和改设置；账户区显示锁图标，备份区提示“登录后查看备份记录”。
- 点击“登录”、刷新、添加账户或任一备份按钮都会弹出登录框；登录通过 CLI `login` 完成，密码只经 stdin 传给子进程。
- 登录态绑定当前 GUI 进程，有效期 15 分钟；关闭窗口时会自动执行 `logout`。重新打开 GUI 需要重新登录。
- 只有 Admin 和 Owner 可以登录；`User` 账户会被 CLI 拒绝。Owner 多出授组、备份和操作其他 Owner 的能力。
- 界面按登录身份置灰不可执行的操作：Admin 只能管理普通 User，不能授组或备份；不能禁用/删除自己；Owner 可以操作任何账户，但仍不能禁用/删除自己。
- 登录用户名记在 `config.gui.json`，不保存密码。切换数据目录或恢复备份后，当前登录态失效，需要重新登录。

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
GUI 的路径设置保存在用户配置目录的 `config.gui.json`：Linux 是 `~/.config/chat-deepseek/`，macOS 是 `~/Library/Application Support/chat-deepseek/`，Windows 是 `%APPDATA%\chat-deepseek\`。
没有图形桌面的服务器直接用 CLI，不需要安装这些依赖。

## 结构

```text
app.py          主窗口、状态和操作流程
cli.py          子进程执行与 serve 进程管理
parsing.py      解析 user list / whoami / backup list / serve / version 输出
server_config.py 读写服务端 config.json 与默认参数
gui_config.py    读写 GUI 的 config.gui.json（路径与上次登录用户名）
dialogs.py      Qt 登录、输入、确认、路径选择和设置对话框
widgets.py      自绘卡片、按钮、输入框、状态点、组标签与成员行
fonts.py        选择 Windows/macOS/Linux 的系统 UI 字体
theme.py        明暗配色、字号与全局样式表
assets/whale.svg DeepSeek 鲸鱼标志（Simple Icons，CC0-1.0）
assets/whale.ico Windows 可执行文件图标
assets/whale.icns macOS .app 图标
assets/whale.png 512px 图标（备用）
tests/          解析器与配置读写测试
```

解析器与配置读写测试：

```sh
gui/.venv/bin/python -m unittest discover -s gui/tests -v
```

## 注意

- 密码通过 `--password-stdin` 传给子进程，不出现在命令行参数和输出里。
- GUI 直接解析 CLI 的中文输出；CLI 提示或表格格式变化时，改 `parsing.py` 并补测试。已知失败条件（服务运行中、端口占用、最后一个 Owner、账户不存在、密码不合规等）会翻译成可操作的提示；只有完全识别不了时才显示“未知错误”和原始输出。
- 备份地址默认是数据目录下的 `backups/`，可在设置里改。Owner 登录后会自动读取备份列表，信息栏显示最近一条；备份完成后也优先显示文件名里的时间（`chat-deepseek-YYYYMMDD-HHMMSS.zip`），名称里没有可解析的时间时显示完整路径。文件存在时同时显示大小。
- “备份记录…”读取 `backup list`，可以查看时间、文件名和大小，并恢复选中的备份；恢复前先停止服务，会覆盖 `chat.db`、`config.json`、`master.key` 和 `files/`。
- 关闭窗口会停止由本窗口启动的 `serve`，并退出当前 GUI 的 CLI 登录；外部启动的进程不会被接管。
- Windows 上停止服务是强制结束进程；同一个数据目录同时只能有一个服务实例。
- 设置页可改二进制目录、数据目录、备份地址、监听地址、Public URL 和 API 地址。路径写入用户配置目录的 `config.gui.json`，监听地址、Public URL 和 API 地址写入数据目录的 `config.json`；保存后重启服务生效。`config.gui.json` 是运行时文件，不提交；它会记住上次登录的用户名，但不保存密码。macOS 默认数据目录是 `~/Library/Application Support/chat-deepseek/data`。
- 字体优先使用 Microsoft YaHei UI / PingFang SC / Noto Sans CJK SC，关闭子像素抗锯齿并使用灰度抗锯齿，字号比常规桌面控件大一档。
- 窗口标题栏、边框和圆角全部使用系统原生：Windows 是 x/□/-，GNOME 只有关闭按钮，macOS 是交通灯。不再自绘标题栏、不设置 frameless 或透明窗口。对话框也使用原生标题栏，内容高度已预留标题栏。设置和命令行提示的标题不带应用名后缀。
- 账户列表是 60px 两行成员行：头像、用户名、组标签和右侧状态点；悬停/选中为长胶囊，选中头像变实心蓝，底部信息栏显示“已选择 / 取消选择”和操作。
- 卡片使用五次贝塞尔连续曲率（32px），按钮、输入框、开关用胶囊端（20px），账户悬停/选中为长胶囊。
- 界面跟随系统深浅色，并在系统主题变化时重刷配色、QSS 与原生控件调色板。启动时先读取平台调色板再切换 Fusion；GNOME 下直接读 `gsettings` 的 `color-scheme` / `gtk-theme`，避免 Qt 还没上报 `colorScheme` 时把暗色系统判成亮色，并用 `dconf watch` 跟随系统切换。
- 依赖不随仓库提交；PySide6 / Qt 的许可证见各自发布包。
- 发布包中 Windows 用 `.ico` 设置 EXE 图标；macOS 用 `.app` 承载 `.icns` 图标并打进 DMG。Linux 发布包不含 GUI 二进制；从源码运行或自行打包时，ELF 文件不携带文件图标，窗口/任务栏图标由 Qt 在运行时设置。
- Linux 需要 Qt 的 xcb 运行库；Debian/Ubuntu 若提示缺少 `xcb-cursor`，安装 `libxcb-cursor0`。
- Linux 没有图形桌面时继续用 CLI，不需要安装 PySide6。
