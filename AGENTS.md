# AGENTS.md

> 本文件记录项目现状与协作约定，会作为工作区上下文注入给每个 agent。
> 改造前先读这里，再读相关模块；修改完成后更新受影响的章节。

## 1. 项目是什么

- 项目：`chat-demo`，本地目录 `chat-deepseek`，版本 `0.1.3`，MIT。
- 来源：`https://github.com/deepseek-ai/chat-demo.git` 的克隆。远程 `upstream`（原 `origin`）指向上游，`origin` 指向自己的 GitLab 仓库 `git@gitlab.com:elegantgx/chat-deepseek.git`（SSH）。
- 分支策略：`main` 只镜像上游、不在上面开发；日常改造在 `feature/*` 分支进行，定期把 `main` 同步到上游后合并进 feature 分支，再推送到 `origin`。
- 定位：**纯前端静态聊天应用**，没有应用后端。浏览器直接调用 DeepSeek API，API Key 存在浏览器本地。
- 能力：多会话、消息分支 / 编辑重答 / 重新生成 / 继续生成、流式回复、深度思考、联网搜索与链接阅读、附件上传、Markdown / 代码高亮 / KaTeX / Mermaid、中英双语、明暗主题、移动端、语音输入与朗读、历史导入导出。
- 技术栈：React 18.3.1、TypeScript 7.0.2（strict）、rspack 2.0.2（SWC + lightningcss）、zustand 4.4.7、idb 8.0.3、fflate、mdast / micromark、katex、prismjs、mermaid。
- 运行环境：Node.js 22.12+、pnpm。开发端口 `8090`。
- 部署形态：静态站点（GitHub Pages）或直接打开 `dist/index.html`。**保留 `file://` 可用性是现有约束**（见 §7）。

## 2. 当前状态（2026-11-22 核验）

- Git：当前在 `feature/dev`；`main` 跟踪 `upstream/main`，`feature/dev` 跟踪 `origin/feature/dev`；`AGENTS.md` 提交在 feature 分支，工作区干净。
- `pnpm type`：通过。
- `pnpm lint`：通过，`0 warnings and 0 errors`（121 个文件，101 条规则）。
- `pnpm build`：通过（rspack 约 1.4s），生成 `dist/`（已在 `.gitignore` 中忽略）。
- `pnpm dev`：本次未长时间启动；配置端口为 `8090`。
- 测试：**没有测试框架、没有测试文件、没有 `test` 脚本**。CI（`.github/workflows/pages.yml`）只跑 `type` → `lint` → `build` → 部署 Pages。
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
```

`README.md`、`README.zh-CN.md` 是面向用户的功能与数据说明；`src/services/chat/history/README.md` 是历史导入导出的详细设计文档。改功能时同步更新对应文档。

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
```

没有自动化测试，改动后除三条门禁外需要手动冒烟（按改动范围挑选）：

1. 新建对话、发送、流式输出、停止、重新生成、编辑提问、切换版本、继续生成。
2. 上传图片 / 文档、拖放 / 粘贴、刷新后补传、删除会话。
3. 思考开关、搜索开关、来源面板、链接阅读、引用角标。
4. 明暗主题、中英切换、移动端布局（<768px）、键盘弹出。
5. 设置里保存 / 更换 API Key、导入导出 ZIP、删除全部会话。
6. 构建产物用 `file://` 打开 `dist/index.html`（涉及打包 / 路径 / 资源改动时必测）。

CI 与本地门禁一致：`pnpm type` + `pnpm lint` + `pnpm build`。新增测试框架前先与用户确认（当前没有测试依赖）。

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

- **当前改造目标：待用户说明。** 在目标明确前不要大改结构，也不要擅自引入依赖、测试框架或数据迁移。
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
- `AGENTS.md` 提交在 `feature/dev`，不要提交到 `main`。
- 本机 SSH 注意：系统配置 `/etc/ssh/ssh_config.d/20-systemd-ssh-proxy.conf` 权限异常，仓库已设置 `core.sshCommand = ssh -F /dev/null` 绕过；换到 SSH 正常的机器时可删除该配置。
- 查看当前跟踪关系：`git remote -v`、`git branch -vv`。
