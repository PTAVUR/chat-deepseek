# Changelog

只记录本分支相对 [chat-demo](https://github.com/deepseek-ai/chat-demo) 的变化；上游已有的聊天界面功能不重复。

## 0.1.4-official0.1.4 - 2026-11-26

### 命令行

- 新增 `info`：只读输出固定 `key=value`（版本、路径、配置、初始化状态、systemd 状态），不创建数据库、`config.json` 或 WAL。
- 新增 `config set`：修改 `listen` / `public_url` / `api_origin` / `backup_dir`，保留 `config.json` 里的未知字段并原子写 `0600`；`backup_dir` 支持清空回默认。
- 新增 `service`：Linux systemd 托管，支持 user/system 两种范围（`install` / `uninstall` / `start` / `stop` / `restart` / `status`）。`install` 只生成 unit 并 `daemon-reload`，不启动；`--enable` 只设置开机自启。Windows/macOS 明确不支持，继续前台 `serve` 或平台 supervisor。
- `backup` / `backup list` / `backup restore` 统一按 `--backup-dir` > `config.json` 的 `backup_dir` > `data/backups` 解析备份地址。
- 退出码补齐用法错误 `2`；`info`、`config set`、`service` 不需要应用登录。

### 图形界面

- `config.gui.json` 升级到 v2（`version`、`connection`、`local{binary_dir,data_dir,username}`）；旧版顶层字段自动迁移，`backup_dir` 改由 `config set backup_dir` 写入 `config.json`。
- 设置页和服务状态改用 `info` / `config set`，GUI 不再直接读写 `data/config.json`；监听地址、Public URL、API 地址和备份地址保存后由 `serve` / `backup` 读取。
- Linux + systemd 增加服务卡片：显示安装、运行、开机自启和 linger 状态，支持 `service install --enable`、启动、停止、卸载；未安装时保留前台会话模式，关闭 GUI 不会停止 systemd 服务。
- Windows/macOS 不支持 systemd，继续使用会话模式；常驻交给平台 supervisor。
- GUI 测试增加到 80 个，新增 v1→v2 迁移、`info` 解析、设置写 `config.json` 和 fake systemd 服务流程验证。

## 0.1.3-official0.1.4 - 2026-11-25

### 上游同步

- 合并上游 `v0.1.4`：历史导入新增进度条，附件按会话随归档一起读取，ZIP 读取改为读窗口缓存与按需随机访问，并补上旧浏览器兼容。
- 导入进度按已处理会话数显示；关闭设置再打开仍能看到进行中的导入，关闭时会清理待确认状态。
- 导入时从图片原件识别 MIME 并回填到会话附件，上传后服务端能返回正确的 `Content-Type`。
- 版本号 `0.1.3-official0.1.4`，代码基线上游 `v0.1.4`。

## 0.1.2-official0.1.3 - 2026-11-25

### 命令行

- 新增 `login`、`logout`、`whoami`：登录态按 shell 隔离，固定 15 分钟过期；token 只放在用户运行时目录，数据库保存哈希。
- `user ...` 和 `backup ...` 现在需要登录并按组校验：`Admin` 管理普通 `User`，`Owner` 负责授组、操作 `Admin`/`Owner` 和备份恢复；未登录退出码 `3`，权限不足 `4`。
- 认证会话增加 `scope`（schema v5），浏览器 Cookie 与 CLI token 不能互相冒用。

### 管理界面

- GUI 适配 CLI 登录：未登录时账户区显示锁图标、备份区提示登录后查看；点登录、刷新、添加或备份会弹出登录框，密码仍只经 stdin 传给子进程。
- 账户操作按当前身份置灰：Admin 只能管理普通 User，不能授组或备份；Owner 可以授组、备份和操作 Admin/Owner；不能禁用或删除自己。
- 登录态绑定 GUI 进程，15 分钟过期，关闭窗口自动 `logout`；切换数据目录或恢复备份后需要重新登录。
- `config.gui.json` 记住上次登录的用户名，不保存密码。
- 修复暗色系统直接启动时被 Fusion 浅色调色板误判为亮色的问题：先读平台调色板，再切换 Fusion；GNOME 下额外读取 `gsettings` 的 `color-scheme` / `gtk-theme`，并用 `dconf watch` 跟随系统主题切换，不依赖 Qt 延迟上报的 `colorScheme`。

## 0.1.1-official0.1.3 - 2026-11-23

### 服务端

- 修复 Windows 上 `setup` 初始化时 SQLite 报 `invalid uri authority: C:` 的问题：数据库路径现在生成合法的 `file:///C:/...` URI，相对数据目录也会先解析成绝对路径。

## 0.1.0-official0.1.3 - 2026-11-23

首个自托管版本。

### 服务端

- 新增 Go 服务端并内嵌 Web UI；账户、会话、附件、设置和 API Key 全部保存在 `data/`，浏览器不再使用 localStorage/IndexedDB。
- 新增 `Owner` / `Admin` / `User` 组与账户管理；最后一名 Owner 受保护；Web 登录后可修改自己的用户名、密码、头像和 API Key。
- 密码使用 Argon2id；登录使用 HttpOnly Cookie、CSRF 和限流；API Key 使用 AES-GCM 加密，主密钥单独保存。
- 会话和附件按用户隔离，会话带版本锁；DeepSeek 请求由服务端代理，API Key 不下发浏览器。
- 新增 `setup`、`serve`、`user ...`、`backup ...` CLI；数据目录可整体搬移。

### 备份

- `backup` 生成数据目录 ZIP；`backup list` 按时间列出备份；`backup restore` 恢复 `config.json`、`chat.db`、`master.key` 和 `files/`。
- 支持自定义备份目录；服务运行中或存在 SQLite WAL/SHM 时拒绝备份或恢复。

### 管理界面

- 新增 Python/PySide6 管理界面，覆盖初始化、服务启停、账户管理、备份和命令输出；窗口使用系统原生标题栏。
- Windows 可执行文件和 macOS `.app` 使用鲸鱼应用图标；macOS 发布包改为只含一个 DMG。
- 启动时自动读取最近备份；备份记录可直接恢复；已知错误翻译成可操作提示，无法识别时才显示原始输出。
- GUI 设置持久化：二进制、数据、备份路径写入用户配置目录的 `config.gui.json`，监听地址、Public URL 和 API 地址写入数据目录的 `config.json`。

### 发布

- 提供 Linux/Windows ZIP 和 macOS DMG；Linux/Windows 根目录的 `src/` 为完整项目源码（内部保留前端 `src/src/`），macOS ZIP 只含 DMG。
- Linux 包不再附带 GUI 二进制，改为附带 `gui/` 源码，体积明显减小；Windows 仍附带预编译 GUI。
- macOS DMG 只有 `.app` 和 `/Applications` 链接；CLI、预配置的 `config.gui.json` 和文档都在 `.app` 内，数据目录默认在 `~/Library/Application Support/chat-deepseek/`。
- 三个平台各自通过 GitHub Actions 手动构建；在 tag 上运行会创建或更新 GitHub Release。

### 注意

- 从上游 chat-demo 升级时，浏览器里的旧 localStorage/IndexedDB 数据不会自动迁移；先在旧版导出历史 ZIP，再在新版导入。
- `file://` 静态页面不再支持，请通过 `chat-deepseek serve` 使用。
