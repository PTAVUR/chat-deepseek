# chat-deepseek

基于 DeepSeek API 的自托管聊天应用：Go 服务端 + React Web 客户端。账户、会话、附件、设置和 API Key 都保存在服务器上，浏览器只保留登录 Cookie 和内存中的 CSRF token。

本项目是 [deepseek-ai/chat-demo](https://github.com/deepseek-ai/chat-demo) 的 fork，当前版本 `0.1.0-official0.1.3`，代码基线为上游 `v0.1.3`。仓库位于 [GitHub](https://github.com/elegantgx/chat-deepseek)。

## 运行

需要 Node.js 22.12+、pnpm、Go 1.27+。先构建前端并把产物嵌入二进制：

```sh
pnpm install
pnpm build
CGO_ENABLED=0 go build -o chat-deepseek ./cmd/chat-deepseek
```

初始化第一个 Owner 并启动服务：

```sh
./chat-deepseek setup --username owner
./chat-deepseek serve
```

也可以直接运行 `./chat-deepseek`：没有数据目录时先进入 setup，之后启动服务并打开浏览器。默认监听 `127.0.0.1:8090`，在页面登录后到「系统设置 → 数据管理」填写 DeepSeek API Key。

常用参数：

```text
--data-dir <dir>       数据目录，默认是二进制同级的 data/
--listen <addr>        监听地址，默认 127.0.0.1:8090
--public-url <url>     对外访问地址（HTTPS 反代时用于 Secure Cookie）
--api-origin <url>     DeepSeek API 地址，默认 https://api.deepseek.com
--no-browser           启动时不自动打开浏览器
--backup-dir <dir>     备份地址，默认是 data/backups（backup）
--output <file>        备份输出文件路径（backup，覆盖默认文件名）
--yes                  跳过恢复确认（backup restore）
--password-stdin       从标准输入读取密码（setup / user add / user reset-password）
```

服务面向自托管使用，默认只监听本机。需要局域网或公网访问时，改成 `--listen 0.0.0.0:8090`，并建议放在 HTTPS 反向代理后面。v1 不提供 Docker 镜像、systemd 单元或 Web 管理页；进程由用户自己的 supervisor、tmux 或 nohup 托管。

## 图形界面（可选）

`gui/app.py` 是 Python + PySide6（Qt 6）管理界面：按钮只负责拼出 `chat-deepseek` 子命令，输出由 `gui/parsing.py` 解析成账户列表、服务状态和提示；已知失败会翻译成可操作说明，识别不了才显示原始输出。账户、会话、备份逻辑仍全部在 `chat-deepseek` 里，不另写一套。它覆盖 `setup`、`serve`、`backup`、`user ...`、`version` 和 `help`；聊天仍由 `serve` 提供的现有 Web UI 完成。

依赖装在 `gui/.venv`（已 gitignore）。在仓库根目录：

```sh
python3 -m venv gui/.venv
gui/.venv/bin/pip install -i https://pypi.tuna.tsinghua.edu.cn/simple -r gui/requirements.txt
gui/.venv/bin/python gui/app.py
```

Windows 用 `gui\.venv\Scripts\python gui\app.py`；更完整的命令见 [gui/README.md](gui/README.md)。需要 Python 3.10+ 和 `requirements.txt` 里的 PySide6。Linux 还缺 Qt 的 xcb 运行库时，Debian/Ubuntu 可安装 `libxcb-cursor0`、`libxkbcommon-x11-0`、`libgl1`、`libegl1` 等系统包。

`gui/app.py` 默认使用 `chat-deepseek` 二进制所在目录（仓库根目录），也可以在“设置”里改。设置页的二进制目录、数据目录、备份地址和监听地址会按当前二进制目录与 `data/config.json` 预填，Public URL 对应 `--public-url`，API 地址对应 `--api-origin`；左下角的“恢复默认”回到二进制同级 `data/`、`data/backups/`、`127.0.0.1:8090` 和官方 API 地址。数据卡片可以立即备份、查看备份记录并从记录中恢复；恢复前必须先停止服务。密码通过 `--password-stdin` 从 GUI 传给子进程，不出现在命令行参数或输出日志里。

关闭窗口会停止由它启动的服务；外部启动的 `chat-deepseek serve` 不会被它接管。GUI 依赖 CLI 的参数名、退出码和中文输出；CLI 文案或表格格式变化时需要同步改 `gui/parsing.py`。Windows 上停止服务是强制结束进程；同一个数据目录同时只能有一个服务实例。无图形桌面的服务器继续直接用 CLI，GUI 不是运行必需组件。

## 发布流水线

`.github/workflows/release-{linux,windows,macos}.yml` 是三个手动触发的 GitHub Actions 工作流：在 Actions 里选择 Release Linux、Release Windows 或 Release macOS，点击 Run workflow；push、标签、Pull Request 和定时任务都不会自动触发。

每个工作流都在自己的 runner 上完整执行 Web 构建、Go CLI 编译、PyInstaller GUI 打包和 ZIP 生成，最后得到：

```text
chat-deepseek-<版本>-linux-amd64.zip
chat-deepseek-<版本>-windows-amd64.zip
chat-deepseek-<版本>-macos-arm64.zip
```

版本取自 tag 名；在分支上手动运行时使用提交的 SHA。每个 ZIP 根目录都包含 `chat-deepseek`（Windows 为 `.exe`）、`chat-deepseek-gui`（Windows 为 `.exe`）、`src/`，以及 `README.md`、`LICENSE`、`THIRD_PARTY_NOTICES.md`。`chat-deepseek-gui` 需要和同目录的 `chat-deepseek` 一起使用。

- Linux 使用 `ubuntu-latest`，Windows 使用 `windows-latest`，macOS 使用 `macos-latest`（arm64）。
- 在 tag 上运行会把 ZIP 和 SHA256 上传到对应的 GitHub Release；在分支上运行只保留 Actions artifacts。
- 三个工作流共用并发组，同一 tag 的发布串行执行，避免同时创建 Release。
- macOS 包没有签名/公证，首次打开可能需要在 Finder 右键“打开”，或执行 `xattr -cr`。Windows 包也未签名，可能出现 SmartScreen 提示。

## 账户

账户和组管理只通过服务端 CLI 完成，没有公开注册。

```sh
./chat-deepseek user add --group Admin --password-stdin alice
./chat-deepseek user list
./chat-deepseek user groups bob
./chat-deepseek user grant bob Admin
./chat-deepseek user revoke bob Admin
./chat-deepseek user disable alice
./chat-deepseek user enable alice
./chat-deepseek user reset-password --password-stdin alice
./chat-deepseek user delete --yes alice
```

组只有 `User`、`Admin`、`Owner`。所有账户默认属于 `User`；`Admin`、`Owner` 是附加组，能力取并集。不能移除、禁用或删除最后一名活动 `Owner`。忘记密码由 CLI 重置，没有邮件或 TOTP 找回。用户名可以是 1-64 个 ASCII 字母、数字或 `. _ -`。登录后可在「系统设置 → 账户管理」修改自己的用户名、密码和头像，保存后立即生效，不需要刷新页面。

## 数据

默认数据目录与二进制同级：

```text
data/
  config.json          # 监听地址等非敏感配置
  chat.db              # SQLite（账户、会话、附件元数据、设置）
  master.key           # API Key 加密主密钥（0600）
  files/<user_id>/     # 附件原件
  backups/             # backup 命令生成的 ZIP
```

Chat 会话以完整 JSON 保存，使用 `version` 乐观锁处理多标签页并发；Web 端删除会话是永久删除，会同时清理只属于该会话的附件。附件按用户隔离，聊天请求由服务端把本地附件 ID 换成 DeepSeek `file_id`，浏览器不接触 API Key。

API Key 使用 AES-256-GCM 加密后写入 SQLite，`master.key` 单独保存；接口只返回是否已配置，不返回密文或明文。备份请用下面的 `chat-deepseek backup`，ZIP 会包含 `config.json`、`chat.db`、`master.key` 和 `files/`；缺少 `master.key` 将无法解密已有 API Key。

浏览器不使用 localStorage 或 IndexedDB；登录状态是 HttpOnly Cookie，CSRF token 只保存在当前页面内存中。

## 备份

备份前先停止服务。`backup` 会检查数据目录里是否存在 `chat.db-wal` 或 `chat.db-shm`，任一存在就拒绝备份或恢复，因为它们通常说明服务仍在运行。

备份地址默认是 `data/backups/`。可以用 `--backup-dir` 改到其他目录；备份地址不能是数据目录本身。

```sh
# 默认写到 data/backups/chat-deepseek-<时间>.zip
./chat-deepseek backup

# 改备份地址
./chat-deepseek backup --backup-dir /path/to/backups

# 也可以指定单个输出文件（默认文件名之外的路径）
./chat-deepseek backup --output /path/to/chat-deepseek-backup.zip

# 列出备份：NAME、SIZE、TIME
./chat-deepseek backup list
./chat-deepseek backup list --backup-dir /path/to/backups

# 恢复指定的备份
./chat-deepseek backup restore chat-deepseek-20261123-120220.zip
./chat-deepseek backup restore --backup-dir /path/to/backups --yes chat-deepseek-20261123-120220.zip
```

ZIP 内容相对于数据目录：`config.json`、`chat.db`、`master.key`、`files/`，不会把 `backups/` 或自定义备份目录打进去。

`backup restore` 会用备份覆盖这四项；备份里没有的项会被删除，以回到备份时的状态。恢复时会保留 `backups/`。交互式终端会要求输入 `yes` 确认；脚本或 GUI 使用 `--yes`。恢复完成后需要重新启动服务。

## 功能

- 多会话、自动标题、重命名与删除；编辑提问、重新生成和切换消息分支。
- 流式回答、深度思考、智能搜索、链接阅读与来源引用；支持停止和符合条件的继续生成。
- 附件上传、粘贴与拖拽，语音输入和回复朗读；移动端录音时输入框首行显示实时频谱。
- Markdown、代码高亮、KaTeX 公式和 Mermaid 图表。
- 简体中文与英文、浅色与深色主题、移动端布局、位置设置、历史导入导出；导入在服务端原子替换，失败不会留下半套数据。
- 服务端账户登录、CSRF 防护、登录限流；每个用户独立会话、附件、设置和 API Key；可修改自己的用户名、密码和头像。
- 系统设置沿用官方左右分栏布局：通用设置、账户管理、数据管理、关于；偏好即时生效，账户资料保存后弹窗保持打开并就地预览。

## 开发

前端开发服务器在 8090，Go 服务在 8091，`/api` 由 rspack 代理到 Go：

```sh
# 终端 1
go run ./cmd/chat-deepseek serve --data-dir ./data-dev --listen 127.0.0.1:8091 --no-browser

# 终端 2
pnpm dev
```

打开 http://localhost:8090 。修改前端后热更新；修改 Go 后重启服务。生产构建用 `pnpm build`：构建到 `dist/` 后自动复制到 `internal/webui/dist/`，再由 `go:embed` 嵌进二进制。

| 命令                 | 用途                               |
| -------------------- | ---------------------------------- |
| `pnpm dev`           | 启动前端开发服务器                 |
| `pnpm build`         | 构建前端并复制到 Go 嵌入目录       |
| `pnpm type`          | TypeScript 类型检查                |
| `pnpm lint`          | oxlint 检查                        |
| `pnpm format`        | oxfmt 格式化                       |
| `go test ./...`      | Go 单元/接口测试                   |
| `go vet ./...`       | Go 静态检查                        |

技术栈以 Go、SQLite（纯 Go 驱动）、React 18、TypeScript、rspack、zustand 为主；Markdown 使用 mdast 渲染，公式与图表按需加载 KaTeX / Mermaid。

```text
cmd/chat-deepseek/    Go CLI 入口
internal/             Go 服务端（config/database/account/auth/settings/chat/filestore/backup/deepseek/server/cli/webui 等）
src/                  React 客户端
  app/                应用外壳、认证门、主题
  components/         聊天、设置、登录、Markdown 等界面组件
  i18n/               中英文文案
  services/           服务端 API、会话状态、语音、Markdown
  utils/              通用工具
```

聊天使用 `deepseek-flash`，通过服务端代理 Anthropic 兼容的 Messages / Files API；语音通过服务端代理 ASR / TTS。请求中的搜索、链接阅读、标题和 `latest_reminder` 扩展保持不变。

## 与上游的差异

- 从静态前端改为 Go 服务端 + Web 客户端；`file://` 直接打开不再受支持，必须通过服务端访问。
- 账户、会话、附件、设置和 API Key 移到服务端；不再使用浏览器本地存储。
- 增加 CLI 账户管理、登录/CSRF、服务端文件代理和 SSE 代理。
- 界面品牌改为 `Deepseek`，字标使用 DM Sans；侧边栏与登录页使用 DeepSeek 鲸鱼标志，登录页跟随明暗主题（亮色白底 / 暗色官方风格）。来源与商标说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
- `main` 只镜像上游，日常改造在 `feature/*` 分支进行；分支、版本与协作约定见 [AGENTS.md](AGENTS.md)。

## 许可证

[MIT](LICENSE)。构建会把许可证复制到产物，分发时请一并保留；第三方依赖遵循各自的许可证，界面鲸鱼标志的来源与商标说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
