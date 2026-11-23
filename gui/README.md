# chat-deepseek GUI

`app.py` 是 `chat-deepseek` 的 Python + PySide6（Qt 6）管理界面。窗口只负责拼出
`chat-deepseek` 子命令；输出由 `parsing.py` 解析成账户列表、服务状态和提示，已知失败会翻译成可操作说明，只有无法识别时才展示原始输出。
账户、会话、备份逻辑仍在 Go 二进制里，本目录不读写 `data/`。

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
没有图形桌面的服务器直接用 CLI，不需要安装这些依赖。

## 结构

```text
app.py          主窗口、状态和操作流程
cli.py          子进程执行与 serve 进程管理
parsing.py      解析 user list / backup list / serve / version 输出
server_config.py 读取 config.json 与默认参数
dialogs.py      Qt 输入、确认、路径选择和设置对话框
widgets.py      自绘卡片、按钮、输入框、状态点、组标签与成员行
fonts.py        选择 Windows/macOS/Linux 的系统 UI 字体
theme.py        明暗配色、字号与全局样式表
assets/whale.svg DeepSeek 鲸鱼标志（Simple Icons，CC0-1.0）
tests/          解析器与配置读取测试
```

解析器与配置读取测试：

```sh
gui/.venv/bin/python -m unittest discover -s gui/tests -v
```

## 注意

- 密码通过 `--password-stdin` 传给子进程，不出现在命令行参数和输出里。
- GUI 直接解析 CLI 的中文输出；CLI 提示或表格格式变化时，改 `parsing.py` 并补测试。已知失败条件（服务运行中、端口占用、最后一个 Owner、账户不存在、密码不合规等）会翻译成可操作的提示；只有完全识别不了时才显示“未知错误”和原始输出。
- 备份地址默认是数据目录下的 `backups/`，可在设置里改。启动时会自动读取备份列表，信息栏显示最近一条；备份完成后也优先显示文件名里的时间（`chat-deepseek-YYYYMMDD-HHMMSS.zip`），名称里没有可解析的时间时显示完整路径。文件存在时同时显示大小。
- “备份记录…”读取 `backup list`，可以查看时间、文件名和大小，并恢复选中的备份；恢复前先停止服务，会覆盖 `chat.db`、`config.json`、`master.key` 和 `files/`。
- 关闭窗口会停止由本窗口启动的 `serve`；外部启动的进程不会被接管。
- Windows 上停止服务是强制结束进程；同一个数据目录同时只能有一个服务实例。
- 设置页可改二进制目录、数据目录、备份地址、监听地址、Public URL 和 API 地址；这些值用于本窗口后续命令，不写 `config.json`，重启后按二进制默认值和 `config.json` 重新预填。
- 字体优先使用 Microsoft YaHei UI / PingFang SC / Noto Sans CJK SC，关闭子像素抗锯齿并使用灰度抗锯齿，字号比常规桌面控件大一档。
- 窗口标题栏、边框和圆角全部使用系统原生：Windows 是 x/□/-，GNOME 只有关闭按钮，macOS 是交通灯。不再自绘标题栏、不设置 frameless 或透明窗口。对话框也使用原生标题栏，内容高度已预留标题栏。设置和命令行提示的标题不带应用名后缀。
- 账户列表是 60px 两行成员行：头像、用户名、组标签和右侧状态点；悬停/选中为长胶囊，选中头像变实心蓝，底部信息栏显示“已选择 / 取消选择”和操作。
- 卡片使用五次贝塞尔连续曲率（32px），按钮、输入框、开关用胶囊端（20px），账户悬停/选中为长胶囊。
- 界面跟随系统深浅色，并在系统主题变化时重刷配色、QSS 与原生控件调色板。
- 依赖不随仓库提交；PySide6 / Qt 的许可证见各自发布包。
- Linux 需要 Qt 的 xcb 运行库；Debian/Ubuntu 若提示缺少 `xcb-cursor`，安装 `libxcb-cursor0`。
- Linux 没有图形桌面时继续用 CLI，不需要安装 PySide6。
