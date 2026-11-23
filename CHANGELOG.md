# Changelog

只记录本分支相对 [chat-demo](https://github.com/deepseek-ai/chat-demo) 的变化；上游已有的聊天界面功能不重复。

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
- Windows 可执行文件和 macOS `.app` 使用鲸鱼应用图标；macOS 发布包改为 `.app` 加同名启动器。
- 启动时自动读取最近备份；备份记录可直接恢复；已知错误翻译成可操作提示，无法识别时才显示原始输出。

### 发布

- 提供 Linux amd64、Windows amd64、macOS arm64 发布 ZIP；根目录的 `src/` 改为完整项目源码（内部保留前端 `src/src/`）。
- Linux 包不再附带 GUI 二进制，改为附带 `gui/` 源码，体积明显减小；Windows/macOS 仍附带预编译 GUI。
- 三个平台各自通过 GitHub Actions 手动构建；在 tag 上运行会创建或更新 GitHub Release。

### 注意

- 从上游 chat-demo 升级时，浏览器里的旧 localStorage/IndexedDB 数据不会自动迁移；先在旧版导出历史 ZIP，再在新版导入。
- `file://` 静态页面不再支持，请通过 `chat-deepseek serve` 使用。
