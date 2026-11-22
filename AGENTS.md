# AGENTS.md

> 本文件记录项目现状与协作约定，会作为工作区上下文注入给每个 agent。
> 改造前先读这里，再读相关模块；修改完成后更新受影响的章节。

## 1. 项目是什么

- 项目：`chat-deepseek`，本地目录 `chat-deepseek`，版本 `0.1.0-official0.1.3`，MIT。
- 来源：`https://github.com/deepseek-ai/chat-demo.git` 的克隆。远程 `upstream`（原 `origin`）指向上游，`origin` 指向自己的 GitLab 仓库 `git@gitlab.com:elegantgx/chat-deepseek.git`（SSH）。
- 分支策略：`main` 只镜像上游、不在上面开发；日常改造在 `feature/*` 分支进行，定期把 `main` 同步到上游后合并进 feature 分支，再推送到 `origin`。
- 定位：正在从**纯前端静态聊天应用**改造为 **Go 自托管服务端 + Web 客户端**；服务端保存账户、会话、附件、设置和 API Key，浏览器不再保存聊天或 Key。当前改造进行中。
- 能力：多会话、消息分支 / 编辑重答 / 重新生成 / 继续生成、流式回复、深度思考、联网搜索与链接阅读、附件上传、Markdown / 代码高亮 / KaTeX / Mermaid、中英双语、明暗主题、移动端、语音输入与朗读、历史导入导出。
- 技术栈：React 18.3.1、TypeScript 7.0.2（strict）、rspack 2.0.2（SWC + lightningcss）、zustand 4.4.7、idb 8.0.3、fflate、mdast / micromark、katex、prismjs、mermaid；服务端目标技术栈为 Go 1.27+、SQLite（纯 Go 驱动）。
- 运行环境：Node.js 22.12+、pnpm、Go 1.27+。开发端口 `8090`。
- 部署形态：便携式二进制 + 同级 `data/` 目录，由普通用户运行；不要求 root、不写 `/var`、不依赖 systemd。Web 客户端通过浏览器访问。`file://` 静态形态仅用于上游兼容/过渡，最终由服务端托管。

## 2. 当前状态（2026-11-23 核验）

- 改造状态：v1 服务端主体已落地（Go module、便携 `data/`、SQLite schema v2、Owner/Admin/User、CLI、登录/CSRF、设置与 API Key 加密、会话版本锁/回收站、附件存储、DeepSeek Files/SSE/ASR/TTS 代理）；前端仍在用 localStorage/IndexedDB，尚未接入服务端。
- Git：当前在 `feature/dev`；`main` 跟踪 `upstream/main`，`feature/dev` 跟踪 `origin/feature/dev`；`AGENTS.md` 提交在 feature 分支，工作区干净。
- `pnpm type`：通过。
- `pnpm lint`：通过，`0 warnings and 0 errors`（121 个文件，101 条规则）。
- `pnpm build`：通过（rspack 约 1.6s），生成 `dist/`（已在 `.gitignore` 中忽略）。
- `pnpm dev`：本次未长时间启动；配置端口为 `8090`。
- 测试：Go 已有单元/接口测试（`internal/auth`、`account`、`database`、`config`、`secret`、`settings`、`chat`、`filestore`、`deepseek`、`server`），`go test ./...` 通过；前端仍没有测试框架/`test` 脚本。仓库里仍保留上游的 `.github/workflows/pages.yml`（GitHub Pages 用）；当前 GitLab 仓库尚未配置 CI。
- 代码规模：`src` 约 19.4k 行（其中 TS/TSX 约 14.7k 行）；最大文件是 `src/services/chat/store.ts`（约 1.1k 行）。
- 源码中没有 `TODO` / `FIXME` 标记。

## 3. 目录与关键文件

```text
src/
  main.tsx                    启动：setupTheme、ensureKatex、createRoot(<App/>)
  app/                        应用外壳、主题、视口与全局样式
    App.tsx                   侧边栏 / 主区域 / 设置弹窗 / Toast 的组装
    theme.ts                  明暗主题（支持跟随系统），body class + theme-color
    viewport.ts               移动端断点（<768px）、键盘视口、阻止整页滚动
  components/
    base/                     Button、IconButton、Menu、Modal、Select、Toast、Tooltip、icons
    chatInput/                输入框、草稿附件、拖放、语音按钮、录音频谱
    messages/                 MessageList、UserMessage、AssistantMessage、ReplyArea（思考/搜索步骤时间线）、BranchSwitcher、CopyButton、ReadAloudButton
    markdown/                 Markdown（节流 → mdast → 渲染）、AST 渲染器、代码高亮、KaTeX、Mermaid
    search/                   搜索结果面板、来源卡片、悬浮卡、引用角标
    session/                  SessionView（标题 + 消息列表 + 吸底输入框）、Welcome、MobileHeader
    settings/                 SettingsDialog（通用 / 数据两组）、API Key 编辑、弹窗状态
    sider/                    会话侧边栏、移动端抽屉、折叠态
    files/                    附件卡片、文件图标、文件预览
  i18n/                       zh.ts（key 的源头）、en.ts（satisfies 同一组 key）、index.ts
  services/
    api/                      client.ts（fetch / SSE / Files）、types.ts（协议类型）、sse.ts、parseEvent.ts、errors.ts、speech.ts（ASR/TTS）
    chat/                     store.ts（会话状态与全部动作）、types.ts、reply.ts（事件合并 / 上下文）、branches.ts（消息树）、view.ts（界面派生）、describe.ts（错误文案）、db.ts（IndexedDB）、drafts.ts、backup.ts、latestReminder.ts、sessionGroups.ts
    chat/history/             历史归档格式、校验、ZIP 读写、导入导出（细节见该目录 README.md）
    attachments.ts            上传队列、限额、purpose、错误文案
    settings.ts               localStorage 设置
    location.ts / locationData.ts  地区数据
    uploadExts.ts             附件扩展名白名单（约 1k 行）
    markdown/                 parse、highlight、katex、mermaid
    speech/                   recorder（录音）、readAloud（TTS 播放）
  utils/                      id、format、url、clipboard、图片缩放
  env.d.ts                    CSS Modules / __API_ORIGIN__ / prismjs / katex 的模块声明

cmd/chat-deepseek/            Go CLI 入口（v1 服务端，见 §13）
internal/
  config/                     数据目录、config.json 与 DeepSeek API 地址
  database/                   SQLite 打开与 schema 迁移（当前 v2）
  account/                    账户、固定组、最后 Owner 保护
  auth/                       Argon2id、session、CSRF
  secret/                     master.key 与 AES-GCM
  settings/                   每用户偏好与加密 API Key
  chat/                       会话 JSON、版本锁、回收站
  filestore/                  附件原件与元数据
  deepseek/                   Files / Messages / ASR / TTS 上游客户端
  server/                     /api/v1 认证、设置、会话、文件、SSE/语音代理
  cli/                        setup/serve/user 命令
```

`README.md` 是唯一面向用户的功能、运行与数据说明（中文）；英文版已删除，不要恢复双语结构。`src/services/chat/history/README.md` 是历史导入导出的详细设计文档。改功能时同步更新对应文档。

Go 代码遵循 `gofmt`；服务端包不写全局状态，CLI 负责装配依赖。新增 Go 代码尽量放在 `internal/`，与上游 TS 分开，方便合并。

## 4. 数据模型与本地存储

### 4.1 消息模型（`services/chat/types.ts`）

- `Session`：`id`、`title`、`createdAt`、`updatedAt`、`messages`、`rootMessageId?`。
- 消息树：每条消息可带 `parentId` / `selectedChildId`。`rootMessageId === undefined` 表示旧版线性列表；`null` 表示空会话。当前展示与请求上下文都由 `getVisibleMessages()` 沿 `selectedChildId` 走出**当前分支**。
- `UserMessage`：`content` + `files: FileAttachment[]`。
- `AssistantMessage`：`model`、`thinking` / `search`（本次请求开关）、`latestReminder`（系统信息快照）、`prefix`（能否作为续写前缀）、`status`（`pending | streaming | done | stopped | error`）、`blocks`（`thinking`、`text`、`server_tool_use`、`web_search_tool_result`、`web_fetch_tool_result`）、`stopReason`、`stopDetails`、`usage`、`error`。
- 附件：`id` 是本地 IndexedDB 主键（同一附件可被多条消息共用）；`serverId` 是 Files API 的 `file_id`；`isImage` 决定上传 purpose 与请求中的块类型；`imageSizeChecked` 表示该 `serverId` 的上传副本已确认图片长边合规。

### 4.2 持久化

- IndexedDB 数据库 `chat-demo`，当前 `DB_VERSION = 11`，对象仓库：
  - `sessions`（完整会话）、`sessionSummaries`（列表元数据 + `fileIds` 多值索引）
  - `files`（附件原件 Blob，按附件本地 id 存）
  - `sessionsStaging` / `filesStaging`（历史导入暂存，成功后原子替换正式数据）
- localStorage 键均为 `chat-demo:*`：`api-key`、`locale`、`location`、`theme-preference`、`tts-voice`、`thinking`、`search`、`sider-collapsed`。
- 草稿只存在内存（`drafts.ts`，按 `string | null` 区分会话 / 新对话），刷新即丢失。
- 会话列表只存摘要；详情按需加载，内存缓存上限 `SESSION_CACHE_LIMIT = 5`，淘汰时避开当前会话、生成中、未保存、删除中的会话。
- 保存失败不会中断对话，界面顶部出现重试条；`pendingFiles` 在保存成功前保留附件原件。

### 4.3 历史归档

- 格式 `export_format_version: 3`，ZIP 内容：`manifest.json`（首条）、`chat_sessions/{id}.json`、`chat_session_index.json`、`chat_sessions/{id}_files/...`（仅图片原件）。
- 导入先校验格式版本，写入暂存区，全部校验通过后才在**一个事务**里替换全部本地会话与附件；失败保留原数据。
- 导出只包含当前分支与本地存在的图片原件；不包含 API Key / 设置 / 非图片附件原件。
- 导入/导出/全部删除由 `backup.ts` 串行队列执行；全部删除通过“空数据替换”实现，不删除服务端已上传文件。
- 详细规则见 `src/services/chat/history/README.md`，改动格式必须同步改文档与 `format.ts` / `schema.ts`。

## 5. API 与协议

- 构建期常量 `__API_ORIGIN__`（rspack `DefinePlugin` 注入），默认 `https://api.deepseek.com`，在 `rspack.config.ts` 的 `createConfig({ apiOrigin, devServer })` 中修改。**改地址需要重新构建**，本地代理也在这里配置。
- 聊天（Anthropic 兼容）：
  - `POST ${origin}/anthropic/v1/messages`，`stream: true`，SSE；请求头 `x-api-key`，带附件时加 `anthropic-beta: files-api-2025-04-14`。
  - 请求体：`model`、`system`、`messages`、`thinking`、`tools`、`safety_check: { level: 'high' }`，无标题时加 `title: { enabled: true }`。
  - 模型固定 `deepseek-flash`（`store.ts` 的 `MODEL`）；类型里还有 `deepseek-v4-pro`，但界面没有模型选择。
  - 工具：始终声明 `deepseek_read_url`；开启“智能搜索”时追加 `deepseek_web_search`。
  - `latest_reminder` 是扩展角色，插在对应提问前；`system` 固定为 DeepSeek 身份说明。
  - SSE 事件解析见 `api/sse.ts`（严格按 WHATWG 实现）与 `api/parseEvent.ts`；以 `message_stop` 结束，错误帧抛错。
  - 事件合并见 `chat/reply.ts::applyEvent`；`message_delta.delta.title` 用于自动标题。
- 文件：
  - `POST ${origin}/anthropic/v1/files`（multipart，`purpose` 可为 `rich_layout_recognition`、`user_data` 或省略）；
  - `DELETE ${origin}/anthropic/v1/files/{id}`；
  - `GET ${origin}/models` 校验 API Key。
- 语音：`POST ${origin}/v1/tools/asr`（multipart WAV / Ogg Opus）、`POST ${origin}/v1/tools/tts`（JSON，返回裸 PCM：24 kHz 单声道 16-bit 小端）。
- 错误统一为 `ApiError`（`http | stream | network | interrupted | protocol`），`api/errors.ts` 负责分类（401 / 402 / 413 / 429 / 5xx、文件失效、上传限额等）。**分类不直接生成文案**，各场景自己选 i18n key。

## 6. 核心流程

### 6.1 启动与会话切换

1. `main.tsx`：`ensureKatex()`、`setupTheme()`、渲染 `<App/>`。
2. `App` 调 `initChat()` 读取会话摘要；首次没有 API Key 时 `ensureApiKey()` 打开设置。
3. `selectSession()` 只设置 `activeId` 并触发懒加载；加载中用 `historyEpoch` 防止旧请求覆盖新状态。
4. 打开会话时把上次未结束的 `pending/streaming` 回复结算为 `stopped`，再写回。

### 6.2 发送一条消息

1. 输入框内容与附件进入内存草稿；添加附件时立即校验（扩展名 → 大小 → 数量）并开始上传（最多 3 个并发，按附件本地 id 复用上传任务）。
2. `sendMessage(sessionId | null)`：要求所有附件上传完成；没有会话则新建；通过 `appendBranch` 把「用户消息 + 空的助手消息」挂到当前分支末尾；先 `persist`，再清空草稿，最后异步 `streamReply`。
3. `streamReply`：
   - 注册 `AbortController` 到 `streaming`，用 `requestAnimationFrame` 合并流式更新；
   - `prepareFiles` 补齐 `serverId`：本轮附件失败则整体失败（可重试）；历史附件失败静默跳过；旧图片先检查长边，超限则清空 id 让它重新上传；
   - 生成本轮 `latestReminder`；由 `toRequestMessages` 组装上下文；
   - 逐个消费 SSE 事件，`applyEvent` 累积思考 / 正文 / 引用 / 工具块；
   - `message_delta` 中若带标题且会话仍无标题，写入自动标题（用户手动重命名后取消本轮标题）；
   - 结束、失败、停止都经 `settleReply` 收尾并落盘；`finally` 中移除 `streaming`。
4. 服务端判定 `file_id` 失效时，清掉对应 `serverId`，重新上传后重发，最多 2 次退避重试；已收到内容块后不自动重发。

### 6.3 重新生成 / 编辑 / 版本切换 / 续写

- `regenerateReply`、`editMessage` 都是在同一父节点下**新增兄弟版本**，不覆盖旧回答和后续对话；`appendBranch` 更新 `selectedChildId`。
- `selectMessageVersion` 只允许切换当前分支路径上节点的兄弟版本；生成中禁止切换。
- `continueReply` 复用原助手消息，把新流追加到已有 `blocks` 后；只有 `canContinueReply` 为真（`prefix`、非拒绝、非 `tool_use`、无服务端工具块、无模板、状态为 `stopped` 或 `done + max_tokens`）才允许。
- 历史上下文只取「用户消息 + 可用助手回复」成对轮次；可用 = `done`/`stopped`、非拒绝、非模板、正文非空。历史思考与引用不回传，历史工具块只回传 `read_link`（且要求有签名）。

### 6.4 界面派生

- `buildReplyView()` 把 `blocks` 换算成：工具步骤时间线（思考 / 搜索 / 打开网页 / 页内查找 / 阅读链接）、最终正文 markdown（插入 `[citation:N]`）、来源列表、搜索结果汇总。
- `useReplyView` 只在来源真正变化时更新数组，避免流式正文导致 Markdown 反复解析。
- Markdown 链路：节流 → `toMdAst` → `MarkdownAstRenderer`；代码高亮、KaTeX、Mermaid 按需加载。
- 语音：输入框录音 → ASR 返回全文并插入光标处；朗读把回复正文转成 TTS PCM，用 `AudioContext` 顺序排程播放，同一时间只读一条，切会话 / 发送 / 停止生成时中断。

### 6.5 历史替换

`replaceHistory(write)`：置 `importingHistory` 锁 → 中止所有生成 → 等删除任务与流收尾 → 递增 `historyEpoch` → 清暂存 → 执行写入 → `commitStaging` 原子替换 → 重置会话缓存与草稿。失败时清暂存、恢复原记录，并在 `finally` 中恢复当前会话加载。

## 7. 技术约定（改动时必须遵守）

### 代码风格

- 格式由 oxfmt 决定：单引号、**不写分号**、`trailingComma: es5`、`printWidth: 100`、2 空格缩进。提交前跑 `pnpm format`（会改全仓库，注意只提交自己的文件）。
- 模块导入带扩展名且写成 `.js`（TypeScript `moduleResolution: NodeNext`）：`import { x } from './x.js'`，`.tsx` 同样写 `.js`。
- 类型导入用 `import type`；`strict`、`noUnusedLocals`、`noUnusedParameters`、`noImplicitReturns` 全开。
- oxlint 关键规则：禁止 default export（`rspack.config.ts` 是唯一例外）、禁止 `console`（除已有内联豁免）、禁止 import 循环、禁止 `any`、禁止非空断言等。
- 注释与文档使用中文，解释「为什么」而不是复述代码。
- 相对路径导入，不引入未使用的路径别名。

### React / 状态

- 全部函数组件；流式链路上的列表项、消息组件使用 `memo`，`memo` 必须具名函数表达式（`react/display-name`）。
- 全局状态用 zustand：`services/settings.ts`、`services/chat/store.ts`、`services/chat/drafts.ts`、`services/attachments.ts`。服务模块导出动作函数，组件用 selector 订阅最小状态。
- 局部 UI 状态留在组件内（如展开/收起、滚动跟随）；跨组件的短暂 UI 状态放 `xxxState.ts`（如 `settingsDialogState.ts`、`searchPanelState.ts`、`mobileSiderState.ts`）。
- 不要绕过 store 直接改 `sessionCache` / `sessions`；所有写操作走 `store.ts` 里的动作，保证持久化、摘要同步与并发保护。

### 样式

- CSS Modules：默认导出，`import styles from './X.module.css'`；class 用 camelCase。rspack 中 `namedExports: false`，`localIdentName: '[local]-[hash:6]'`。
- 设计令牌是 CSS 变量：`--dsw-*`（颜色 / 字体等）、`--ds-*`（控件尺寸、动效等）；主题切换通过 `body[data-ds-dark-theme]` 与 `body.dark`。
- 移动端断点固定 `< 768px`（`app/viewport.ts`）；输入框、顶栏、视口高度依赖 `--app-viewport-*` 变量，改布局时不要破坏键盘弹出行为。
- 全局样式在 `app/global.css` / `app/theme.css`，组件样式就近放 `*.module.css`。
- 注意：`Button.module.css.d.ts`、`Select.module.css.d.ts`、`Settings.module.css.d.ts` 是手写的 CSS Modules 类型声明，会覆盖 `env.d.ts` 的通用声明；给这三个模块新增 class 时要同步补 `.d.ts`，否则 `pnpm type` 报错。

### 文案与可访问性

- 所有用户可见文案走 i18n：`zh.ts` 定义 key，`en.ts` 用 `satisfies Record<keyof typeof zh, string>` 保证齐全。不要在组件里硬编码中文 / 英文。
- 图标按钮提供 `aria-label`；状态变化用 `role="status"` / `aria-live`；弹窗、菜单、选择器保留键盘操作与焦点管理。
- 外链统一 `target="_blank" rel="noreferrer"`；Markdown 渲染不允许裸 HTML 注入（当前走 AST 渲染，不要改成 `dangerouslySetInnerHTML`）。

### 数据与兼容

- 改 IndexedDB 结构必须递增 `DB_VERSION`，并在 `upgrade` 中处理老库；不要静默丢数据。
- 历史归档格式如果需要变更，先评估 `schema.ts` 的向后兼容，并同步 `history/README.md` 与 `format.ts`。
- 导出的数据绝不能包含 API Key 或应用设置。
- 附件可能被多个会话引用，删除会话只清理「无引用」的本地原件；远端文件不维护引用计数、也不删除。
- `latestReminder` 是写入消息的历史快照，续写沿用，不要用当前时间重新计算覆盖历史快照。

### 保持 `file://` 可用

- `publicPath: 'auto'`、经典 `defer` 脚本、相对资源路径是刻意的；不要改成绝对 `/` 路径、Service Worker 或依赖 HTTP 服务器的能力。
- 存储能力在 `file://` 下可能受限，相关代码必须容忍 localStorage / IndexedDB 失败（现有代码已做 try/catch）。
- 新增运行时依赖前先确认 ESM、体积、浏览器 target（Chrome 105 / Edge 105 / Safari 16 / Firefox 110）与 `file://` 兼容性。

## 8. 验证方式

```sh
pnpm type     # tsc 严格类型检查
pnpm lint     # oxlint
pnpm build    # 产物 dist/
pnpm dev      # http://localhost:8090
pnpm format   # oxfmt 全仓库格式化
go test ./... # Go 单元/接口测试
go vet ./...  # Go 静态检查
```

前端仍没有自动化测试；Go 已有测试。改动后除门禁外需要手动冒烟（按改动范围挑选）：

1. 新建对话、发送、流式输出、停止、重新生成、编辑提问、切换版本、继续生成。
2. 上传图片 / 文档、拖放 / 粘贴、刷新后补传、删除会话。
3. 思考开关、搜索开关、来源面板、链接阅读、引用角标。
4. 明暗主题、中英切换、移动端布局（<768px）、键盘弹出。
5. 设置里保存 / 更换 API Key、导入导出 ZIP、删除全部会话。
6. 构建产物用 `file://` 打开 `dist/index.html`（涉及打包 / 路径 / 资源改动时必测）。

当前 GitLab 仓库还没有 CI；上游遗留的 GitHub Pages workflow 只跑前端门禁。Go 测试/`go vet` 先在本地执行，GitLab CI 待配置。前端新增测试框架前先与用户确认。

## 9. 高风险点（改 store / 数据 / 流式时重点看）

- `store.ts` 是单文件核心，`historyEpoch`、`deletingSessions`、`deletionTasks`、`unsavedSessions`、`pendingFiles`、`streamFlushes`、`titleRequests`、`sessionLoads`、`importingHistory` 互相配合防止竞态；新增动作必须复用这些保护，否则容易出现「删除后又被写回」「导入覆盖新消息」「旧请求覆盖新会话」。
- 流式更新只按动画帧刷新内存，离开页面时 `bindChatPersistence` 会 flush；新增状态字段要确认能随 `persist` / `settleReply` 一起落盘。
- 消息树不变量：`parentId` / `selectedChildId` 必须一致；新分支用 `appendBranch`，切换用 `selectVersion`，不要手改 `messages` 顺序。
- 上下文规则（哪些消息进请求、哪些块回传、能否续写）集中在 `reply.ts`；改动会影响「继续生成」、历史导入与重新生成，需一起验证。
- 安全拒绝（`stop_reason: refusal`）时已下发的思考和正文会被丢弃、只保留模板；拒绝轮次不进入上下文。
- 上传任务按附件本地 id 复用；`serverId` 失效重传和图片长边检查会回写会话并持久化，注意不要在请求快照里用旧 `serverId`。
- 历史替换会中止所有生成、清空草稿与缓存；导入失败必须保持原数据可用。
- i18n key 增删必须同时改 `zh.ts` / `en.ts`；`TranslationKey` 以 `zh.ts` 为准。
- `__API_ORIGIN__` 是编译期常量，运行时改不了；改 API 地址或代理要同时看 `rspack.config.ts` 与部署环境。

## 10. 与用户协作改造

- **当前改造目标：按 §13 的 v1 服务端方案实施。** 不再停留在讨论阶段；新增功能优先按 §13 的边界落地。
- `README.md` 只写用户和开发者需要的事实与用法，保持与上游 README 同等的信息密度；试错过程、环境限制、临时决定写到 §12，不要把 README 变成日志或垃圾桶。
- 建议流程：确认目标与验收标准 → 读相关模块与数据流 → 给出影响面 / 方案 → 实现 → `type` / `lint` / `build` → 按 §8 手动冒烟 → 汇总改动、风险与未覆盖项。
- 需要向用户确认的典型问题：
  - 改造是功能扩展、UI 改版还是底层重构？需要兼容上游 `deepseek-ai/chat-demo` 的后续合并吗？
  - 是否仍需支持 `file://` 打开与 GitHub Pages 部署？是否允许引入新依赖 / 测试框架？
  - 涉及数据结构时，是否需要兼容已有 IndexedDB 数据和 v3 历史归档？是否需要迁移脚本？
  - 验收方式：手动演示、截图，还是补自动化测试？
- 未明确要求时不要提交 git commit、不要改 `package.json` 版本、不要动 `dist/`。

## 11. Git 工作流

- 远程：
  - `origin` = `git@gitlab.com:elegantgx/chat-deepseek.git`（自己的 GitLab，SSH，日常推送目标）
  - `upstream` = `https://github.com/deepseek-ai/chat-demo.git`（上游，只读同步）
- `main` 是上游镜像：只同步、不开发、不在上面提交或合并本地改动；`feature/*` 承载本地改造。
- 本仓库已设置 `remote.pushDefault = origin`，`git push` 默认推到自己的 GitLab；`git pull` 仍按各分支自己的跟踪关系。
- 定期同步上游并合并：

  ```sh
  git switch main
  git pull --ff-only          # main 跟踪 upstream/main
  git push origin main        # 让 origin/main 也保持上游内容
  git switch feature/dev
  git merge main
  git push                    # 推 feature/dev 到 origin
  ```

- 冲突只在 `feature/*` 分支解决，不要为了让合并通过而修改 `main`。
- 标签：上游 tag 原样镜像到 `origin`（`v0.1.0`–`v0.1.3` 已推送）；同步上游 tag 用 `git fetch upstream --tags`，再 `git push origin --tags`。
- 版本：`package.json` 的名称是 `chat-deepseek`，版本是 `0.1.0-official0.1.3`；版本号第一位（major）跟随上游，当前上游也是 `0.x`。`package.json` 的 `version` 不带 `v`，但带 `official` 后缀。
- 我们自己的发布 tag 带 `v` 前缀，格式为 `v<我们的版本>-official<上游版本>`，例如 `v1.0.0-official0.3.0`；当前版本对应 `v0.1.0-official0.1.3`，不与上游 `vX.Y.Z` 混用。`package.json` 的版本与 git tag 一一对应，tag 只多一个 `v`。
- `AGENTS.md` 提交在 `feature/dev`，不要提交到 `main`。
- 本机 SSH 注意：系统配置 `/etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf` 权限异常，仓库已设置 `core.sshCommand = ssh -F /dev/null` 绕过；换到 SSH 正常的机器时可删除该配置。
- 查看当前跟踪关系：`git remote -v`、`git branch -vv`。

## 12. 环境与试错记录

> README 只保留用户文档；排查过程、环境限制和临时决定记录在这里。

- **GitLab SSH**：本机 `/etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf` 权限异常，直接 `ssh` / `git push` 会报 `Bad owner or permissions`。仓库已设置 `core.sshCommand = ssh -F /dev/null` 绕过；换到 SSH 正常的机器时可以删除该配置。
- **pnpm 12 预检**：本机全局 store 在 workspace 外（`~/.local/share/pnpm/store/v11`），DSH 沙箱下 `pnpm type` / `lint` / `build` 前的自动安装检查会报 `ERR_PNPM_STORE_DIR_OPEN_OPERATION_LOCK` / 只读文件系统；脚本本身仍会继续执行并通过。依赖未变化时可忽略；真正需要安装依赖时可尝试把 store 指到可写目录（如 `--store-dir /tmp/pnpm-store`），当前环境尚未验证。
- **上游连通性**：检查 GitHub upstream 时 `git ls-remote` 曾超时；本地 `upstream/main` 跟踪关系正常，同步失败先排查网络或代理。
- **package version**：`0.1.0-official0.1.3` 是合法的 semver 预发布版本（prerelease），npm 已验证可解析，pnpm 构建也能读取；私有项目不发布 npm，所以可以采用。注意它排序在 `0.1.0` 之前，不要把它当作正式版 `0.1.0` 的替代。
- **存储命名**：IndexedDB 数据库名和 localStorage 前缀仍是 `chat-demo`（代码未改）；直接改名会让已有本地数据不可见。后续如果要统一成 `chat-deepseek`，需要迁移或明确接受数据重置。
- **README 语言**：只保留中文 `README.md`，英文版已删除；README 保持用户文档定位，试错与决策记录放这里。
- **当前 UI 文案**：应用标题 / `appName` 仍是上游的“API 使用 demo”，`src/services/chat/history/README.md` 仍写 `chat-demo`；这些属于后续代码或文档统一事项。
- **GitLab 保护分支**：`feature/dev` 在 GitLab 上被保护，`git push --force-with-lease` 会被拒绝（protected branch）。不要重写已推送的提交；需要修历史时先在 GitLab 取消保护，或换新分支。
- **Go 缓存**：本机默认 `GOPATH`/模块缓存位于 workspace 外，DSH 沙箱下只读。构建/测试需把 `GOPATH`、`GOMODCACHE`、`GOCACHE` 指到 workspace 的 `tmp/`（已 gitignore）；例如 `export GOPATH=$PWD/tmp/gopath GOMODCACHE=$PWD/tmp/gopath/pkg/mod GOCACHE=$PWD/tmp/go-build`。`/tmp` 在部分工具调用间不保留，不要依赖它。

## 13. v1 服务端改造（进行中）

> 2026-11-23 起实施。以下是已确认的设计决策；实现进度同步更新本节。

### 目标

- 自托管（Jellyfin 式）：服务端保存账户、会话、附件、设置和 API Key；浏览器只做客户端。
- 便携：二进制与 `data/` 同级；普通用户运行，不要求 root、不写 `/var`、不依赖 systemd。
- 账户/组管理全部走 CLI；Web 只登录、改自己的密码、设置自己的 API Key。
- 保留现有 React 聊天逻辑（`store.ts`、`reply.ts`、`branches.ts`、`view.ts` 和组件），服务端暂做存储 + DeepSeek SSE 代理，减少与上游同步的冲突。
- v1 只做 Web；不做 PWA、Android 原生、Docker 交付。

### 便携目录

```text
chat-deepseek/
  chat-deepseek          # 二进制
  data/
    config.json          # 监听地址等非敏感配置
    chat.db              # SQLite
    master.key           # API Key 加密主密钥（0600）
    files/<user_id>/     # 附件原件
    backups/             # 快照/导出
```

- 默认数据目录是二进制所在目录下的 `data/`；`--data-dir` 仅用于测试或特殊安装。
- 监听高位端口（默认 `8090`），不申请特权；前台运行，SIGINT/SIGTERM 优雅关闭。
- 不内置 systemd/服务安装；用户可自行用 tmux、nohup 或自己的 supervisor 托管。
- 数据目录 0700、数据库和密钥 0600；不把敏感数据写到用户主目录之外。

### 账户与组

- 组名单数：`Owner`、`Admin`、`User`。
- 所有账户默认属于 `User`；`Admin`、`Owner` 是附加组；可同时属于多个组，能力取并集。
- 最后一名 `Owner` 不能被移除、禁用或删除。
- `Admin` 可创建/禁用/重置/删除普通 `User`；不能操作 `Admin`/`Owner`，不能授予/撤销组。
- `Owner` 拥有全部能力：授组、服务器设置、备份/恢复、清理实例。
- 不提供跨用户聊天浏览；Owner 的物理访问来自服务器目录和备份。
- Web 没有用户/组管理页；CLI 是唯一管理入口，CLI 是 OS 信任边界。
- 无公开注册、无邮件/TOTP；忘记密码由 CLI 重置。

### 数据与设置

- SQLite 是唯一数据源：账户、组关系、登录会话、聊天会话 JSON、文件元数据、每用户设置。
- 附件按用户隔离：`data/files/<user_id>/`。
- 所有用户设置（主题、语言、位置、朗读音色、thinking/search、侧栏等）存服务端；**前端不使用 localStorage/IndexedDB**，只保留 HttpOnly 会话 cookie。
- API Key 用 AES-GCM 加密，`master.key` 单独保管；不返回前端、不写日志。
- 聊天会话软删除/回收站；备份用 `VACUUM INTO` + 附件目录。

### API 与前端边界

- 统一 `/api/v1`；同源 Cookie 会话 + CSRF；登录限流。
- 主要接口：`auth/login|logout|me|password`、`settings`、`chats`（带 `version`）、`files`、`stream`（DeepSeek SSE 代理）、`export/import`、`healthz`。
- 聊天分支与上下文构建暂留在 Web TS；服务端保存 `Session` JSON 并代理 SSE。二期原生 App 前再把上下文构建下沉 Go 或复用现有逻辑。
- 多端并发：会话 `version` 乐观锁（409）+ 单会话生成租约。
- 前端改造尽量保持 `db.ts`、`settings.ts`、`attachments.ts` 的函数签名，新增 `/api/v1` 客户端；不改 `reply.ts`、`branches.ts`、`view.ts` 和聊天组件，方便上游合并。

### 非目标（v1）

- 公开注册、TOTP、邮件找回、Web 管理页。
- 自定义组、细粒度权限。
- 离线、双向同步、CRDT。
- PWA、Android 原生、Docker 交付、多实例/集群、E2E 加密。

### 实施顺序

1. Go module、配置、`data/` 解析、CLI 骨架。
2. SQLite 迁移与账户/组模型。
3. Argon2id、session、CSRF、登录 API、CLI `setup/user`。
4. 设置、API Key 加密、聊天存储、文件与 DeepSeek SSE 代理。
5. 前端登录与存储适配，移除 localStorage/IndexedDB。
6. 备份/恢复、旧 ZIP 数据迁移、测试与文档。

### 上游同步

- `main` 仍只镜像上游；feature 分支承接改造。
- 尽量不改聊天核心逻辑；服务端和适配逻辑放新文件/新目录。
- 上游对 `db.ts`、`settings.ts`、`client.ts`、`store.ts` 的改动可能与改造冲突，合并时优先保留上游行为，再补适配层。
- `package.json`、README、AGENTS 的分叉是预期内的。

### 当前进度

已完成：

- Go module 与依赖；`cmd/chat-deepseek` CLI。
- 便携 `data/` 解析、`config.json`、SQLite WAL 与 schema v2（`files.purpose`）。
- 固定组 `Owner/Admin/User`、账户 CRUD、最后活动 Owner 保护。
- Argon2id、登录会话、CSRF、登录限流、`auth/login|logout|me|password`。
- CLI `setup`、`serve`、`user add/list/groups/grant/revoke/disable/enable/reset-password/delete`。
- `master.key` 与 AES-256-GCM；每用户偏好/API Key API，Key 不出服务端、不写日志。
- 会话 CRUD、`version` 乐观锁、软删除/回收站/恢复/永久删除，按用户隔离。
- 附件上传/下载/软删除，按用户目录保存；chat 请求里的本地 `file_id` 会惰性上传到 DeepSeek 并缓存远端 ID。
- DeepSeek SSE 代理、ASR/TTS 代理、API Key 校验；上游错误状态原样透传。
- Go 单元/接口测试覆盖 secret、settings、chat、filestore、deepseek、server。

未完成：

- 单会话生成租约（表已建，尚未接入 API/流）。
- 前端登录页、API 适配、移除 localStorage/IndexedDB。
- 备份/恢复、旧 ZIP 数据迁移、静态 UI 嵌入、GitLab CI。
