# Yauzin 设计记录

本文档汇总当前代码里**仍然成立**的设计决策、分层契约、踩坑约定，以及面向开发者的环境、构建、字体图标与目录说明。[README](../README.md)（[中文版](../README.zh-CN.md)）只保留产品介绍、功能清单与使用方式，不再承载构建步骤。

## 1. 已否决路线

项目定位与功能清单见 [README](../README.md)（[中文版](../README.zh-CN.md)）。以下路线已明确放弃（阅读旧计划时不要照抄）：

- Fyne 实现（已整体重写为 Gio，仅托盘经 `fyne.io/systray` 使用 Fyne 生态）。
- 应用内 HTML 富文本渲染（含 `internal/htmlrich` + 自绘排版方案）；HTML 正文只交给系统浏览器。
- Fyne 内建图标、`page/perPage` 双模式分页、窗口装饰自动探测。
- tooltip 跨帧重放、设置页胶囊分段控件。
- 日志落文件（`YAUZIN_LOG` 环境变量 + 文件 handler）：日志继续只写 stderr，不引入文件轮转与路径配置。
- 演示数据本地化：`internal/mail/mock` 的标签名与正文摘要保持硬编码，不随界面语言切换；真实账号的标签来自服务端，不受影响。

## 2. 分层

依赖方向单向：`cmd` → `internal/ui` → 领域包；`internal/ui` 只依赖 `mail.Service` 接口，不依赖任何具体后端。各文件职责见 [§15 目录结构](#15-目录结构)，契约与不变量见下文。

- **后端装配点**：`internal/ui/app.go` 的 `appOptions{accounts, newService, store, proxy, factory}` 是唯一装配后端的地方。`newApp()` 读取 `settings.json` 与 `accounts.json`，账号列表 = 已存 Gmail 账号 + 始终保留的 demo 账号（`demo-gmail`，仍由 `internal/mail/mock` 服务）；`newAppWith(pref, opts)` 接受已解析的设置与注入的工厂，测试经它构造 `App`，不触碰磁盘。UI 其余部分继续只认 `mail.Service`。

## 3. 后端契约（`internal/mail`）

- **会话聚合在服务层**，UI 不做分组：`ThreadSummary` 恒描述整段会话，搜索命中也只决定会话是否出现，不会裁剪会话内的邮件。
- **会话粒度的读取**：正文只通过 `GetThread(ctx, threadID)` 按会话读取，`Service` 不提供单封 `Get`。真实后端因此不必为 UI 不使用的懒加载路径写实现；若将来确实需要单封读取，先在设计里说明用途再重新加回。
- **原文查看是独立的读取路径**：`RawMessage(ctx, messageID) ([]byte, error)` 只在用户点「查看原始邮件」时调用，返回后端存储的原始 RFC 822 源码（Gmail 为 `messages.get?format=raw`）。它不是正文懒加载：解析后的正文仍只来自 `GetThread`，因此不受上一条约束。
- **纯返回值（无 emit）**：`Folders(ctx) ([]Folder, error)` 返回侧栏名称（不含未读数/颜色），`FolderDetails(ctx, folders) ([]Folder, error)` 补未读数与颜色，`ListThreads(ctx, req)` 与 `SearchThreads(ctx, req)` 返回整页会话；四个方法都一次性返回，不再有 emit 回调。渐进体验由 UI 串行编排（见 §4.2）：侧栏先显示标签名、列表保持加载态直到详情就绪后一次性显示整页，最后补计数器。
- **游标分页**：`ListThreads(ctx, ListRequest{FolderID, PageToken, Limit})` 浏览单个文件夹，`SearchThreads(ctx, SearchRequest{Query, PageToken, Limit})` 跨全部文件夹搜索；两者的 `FolderID` / `Query` 都是必填，空值返回 `ErrInvalidArgument`。`ThreadPage{Threads, Total, NextPageToken}`，`NextPageToken` 为空表示最后一页，`Total` 为负表示后端无法估算。UI 只在 `tokens []string` 里压栈游标，不向下传页码。
- **搜索是独立的读路径**：query 原样交给后端（Gmail 支持 `from:` / `subject:` / `has:attachment` 等原生语法），不带 folder 过滤；Gmail 的 `includeSpamTrash` 取默认 false，因此天然排除垃圾邮件与垃圾箱，与 mock 的 `matchFolder("")` 语义一致。`Total` 取后端估算，无法估算时为 -1。
- **读取缓存**：gmail 后端对 `Folders` / `FolderDetails` / `ListThreads` / `SearchThreads` 各做一份无过期 LRU（容量 4 / 4 / 64 / 32；键为固定键、标签 ID 集合、`(labelID, pageToken, limit)`、`(query, pageToken, limit)`），命中返回深拷贝，`Invalidate()` 整体清空。列表与搜索的缓存分开，互不挤占。
- **错误是分类不是句子**：`ErrNotFound` / `ErrUnsupported` / `ErrInvalidArgument` / `ErrUnauthorized` 由 `internal/ui/errors.go` 用 `errors.Is` 映射到 i18n 键，未知错误才回退 `err.Error()`。`ErrUnauthorized` 只表示「账号需要重新授权」，不让传输层文案冒充原因。
- **能力边界按方法表达**：只读后端（当前 Gmail）对 `SetFlag` / `Archive` / `Delete` / `Send` / `SaveAttachment` 统一返回 `ErrUnsupported`，UI 保留原入口并由既有错误映射提示「当前账号不支持该操作」；`RawMessage` 属于读操作，只读账号照常实现。打开未读会话触发的自动已读在只读后端上静默回滚徽标，不打断阅读。
- 实现必须并发安全并尊重 `ctx`（UI 从多个 goroutine 并发调用、不串行化）。mock 的延迟存在的意义就是暴露加载态与竞态。
- 跨层数据必须复制：mock 的 `cloneMessage` / 附件切片深拷贝，避免 UI 通过返回指针改到存储。

### 3.1 Gmail 只读接入（`internal/mail/gmail`）

- **授权**：用户自带 Google Cloud「桌面应用」OAuth 客户端，在「设置 → 账号 → 添加账号」中填写 Client ID / Secret。流程是授权码 + PKCE（`oauth2.GenerateVerifier` / `S256ChallengeOption`），回调走 `http://127.0.0.1:<随机端口>/oauth2/callback` 的一次性本地服务；scope 为 `gmail.readonly` + `userinfo.profile`（不加 `openid`：不需要 ID Token），并带 `access_type=offline` 与 `prompt=consent` 以取得 refresh token。没有 refresh token 视为授权失败。授权链接不再自动交给浏览器，而是显示在对话框里（只读可选中，另有「复制链接」「在浏览器中打开」按钮），回环回调仍要求浏览器在本机。姓名取自 `https://www.googleapis.com/oauth2/v3/userinfo`，属于尽力而为：旧授权没有 profile scope 时该请求失败只记告警，账号仍以邮箱显示，重新登录一次即补齐。授权码、令牌、client secret、含密码的代理地址一律不写日志。
- **存储**：`accounts.json`（与 `settings.json` 同目录，0600，临时文件 + rename 原子写）保存 `oauth{provider, clientId, clientSecret}` 与每账号 `{id,kind,email,name,useProxy,token{accessToken,refreshToken,tokenType,expiresAt}}`；`provider`/`kind` 都写 `gmail`（`oauth.provider` 标明凭据属于哪个后端，`kind` 复现 `mail.Kind`）。`accounts.Store.Credentials(kind)` 只在 `provider` 匹配时返回客户端，`Upsert` 拒绝空 `kind`。文件是人可读的显式格式，不兼容旧版无 `provider`/`kind` 的文件：旧条目在启动时跳过、旧凭据不被读取，重新登录一次即写出新格式（不做迁移）。`id` 为 `gmail:` + 小写邮箱，重复登录同一邮箱即覆盖（重新授权）。`name` 来自 userinfo，为空时 UI 回退显示邮箱；重新登录取不到姓名时保留已存的值。账号行本身是手风琴开关：点击展开该账号的详情（再次点击或展开另一个账号即收起），演示账号无可配置项，不显示展开箭头。详情是一块浅灰子卡片，与账号行同属一个卡片项（两者之间没有分隔线）：第一行是「通过代理连接」开关，第二行是右对齐的描边小按钮「重新登录」「删除账号」（1dp 边框、8dp 圆角、32dp 高；危险操作为红框红字，悬停浅红底）。重新登录复用已存客户端凭据，若授权得到的邮箱与目标账号不一致则报错且不写盘。删除是两段式：首次点击把操作行原地替换为「确认删除该账号？」与「取消」「确认删除」，点「取消」或收起账号、切换设置页签、面板重建都会取消待确认状态，点「确认删除」才从 `accounts.json` 移除该条目与令牌（`oauth` 客户端凭据保留；demo 账号不在文件里，不提供删除）。删除当前使用的账号会让邮箱进入空状态（文件夹、列表、阅读器清空，顶栏回到品牌名），下一个账号由用户在顶栏账号菜单选择。设置页不再高亮当前账号：切换标记只保留在顶栏菜单里。一个 `*accounts.Store` 同时被 UI 与所有后端持有，后端刷新令牌后立即回写。文件损坏只告警并退化为「仅 demo 账号」，不阻止启动。
- **代理**：代理地址是通用设置（`settings.json` 的 `proxy`，在「设置 → 通用 → 网络 → 代理」维护），账号条目只记 `useProxy`。开关在两处出现：添加账号/重新登录浮层里决定本次授权与该账号的初始选择；「设置 → 账号」里点击账号行展开后，浅灰子卡片的第一行「通过代理连接」可随时切换。切换只重写该账号的条目并只重建它自己的后端（开启与关闭都重建，先取消在途请求），正在浏览的邮箱会重新加载；写盘失败时开关按存储状态自动回退。未配置代理地址时该行禁用并提示先去通用页填写。保存代理后，使用代理的 Gmail 后端被关闭并重建（先取消在途请求），新 Transport 立即生效。`net/url` 只接受 `http` / `https` / `socks5` 且必须有 host 与端口；`socks5` 由 `http.Transport` 原生支持（`socks5h` 不接受：net/http 没有对应 dialer）。回环回调不经代理。
- **请求日志**：每个账号的每次 HTTP 请求在 DEBUG 级别各记一行 `gmail request`，字段为 `account`（账号 ID，新增账号授权时为 `-`）、`method`、`endpoint`（host + path，不含 query）与 `status`；传输层失败时改记 `err`、不记状态。日志装在 `transport.go` 的 `http.RoundTripper` 上，覆盖单次 REST、batch、令牌刷新与授权交换；包装只克隆 `*http.Client`，不改 `http.DefaultClient` 或测试注入的 client。级别来自「设置 → 通用 → 日志」（`settings.json` 的 `logLevel`），默认 `info`，改后对运行中的后端立即生效。
- **读取映射**：Gmail 系统标签 `INBOX/STARRED/SENT/TRASH` 映射为 `inbox/starred/sent/trash` 与对应 `FolderKind`（名称留给 UI 按 kind 翻译），`DRAFT/SPAM/IMPORTANT/UNREAD/CATEGORY_*` 不展示；用户标签保留 Gmail label ID 与名称；`labels.list` 返回的是部分资源（只有 id/name/type），因此未读数与颜色来自一次 **batch** 请求：`POST /batch/gmail/v1`（multipart/mixed）里每个子请求是 `labels.get?fields=messagesUnread,color/backgroundColor`，**单批 25 个子请求**、超出则分片，子请求用 `Content-ID` 索引回填，响应乱序也能对应；全部批次完成后一次性返回，补齐对应标签的未读数/颜色。一次文件夹加载因此是 `1 + ceil(L/25)` 个 HTTP 请求；单个子请求失败（标签刚被删除）只告警并按 0 显示，全部失败才报错。列表用 `threads.list`，随后用同样的一次 **batch** 请求补全本页每个会话的 `threads.get?format=metadata`（参与者、未读数、星标、附件标记与最新时间），同样**每批 25**、按 `Content-ID` 回填并按 list 顺序输出，全部批次完成后一次性返回；子请求 404（会话在两次调用之间被删除）只丢弃该行，其它子请求失败或整批失败则该次调用报错，空页不发 batch。一次列表加载因此是 `1 + ceil(N/25)` 个 HTTP 请求。搜索走同一个 `threads.list`，只带 `q`、`maxResults` 与 `fields`（不带 `labelIds`），因此也是 `1 + ceil(N/25)` 个请求。**batch 按账号串行**：`client` 持有一个容量 1、可被 ctx 取消的信号量，从发起到响应体读闭一直持有（含整批 401 重发），因此侧栏的 labels batch 与列表的 threads batch 不会叠加——Gmail 会并发执行 batch 内的子请求并按用户计并发，叠加正是触发 429 的原因。`Total` 取 `resultSizeEstimate`，缺省或用空时为 -1。正文用 `format=full` 递归解析 MIME：首个 `text/plain` 与首个 `text/html` 作正文，带 `filename` 的部件只留元数据（附件下载不在本期范围）。
- **类型来源**：`discovery_types.go` 由 `internal/cmd/gmailgen` 从 Google 的 Discovery 文档生成（`//go:generate`，文件头记录文档 `revision`），字段名按 initialism 表保持 `ID` / `IDs` / `MIMEType` / `AttachmentID` 等写法；`api.go` 只留 `headers` / `hasFlag` / `date` / `hasAttachment` 这些把文档折算成领域模型的辅助方法。文档本身不入库，每次生成都取官方当前版本，schema 变化靠 `git diff` 审阅。
- **已知取舍**：一页 25 个会话 = 1 次 list + 1 次 metadata batch（`1 + ceil(N/25)`），首屏延迟仍高于 mock；每批 25 是并发预算而非 Gmail 协议上限（协议仍允许 100），串行后 batch 子请求峰值固定为 25，若线上仍偶发 429 再下调 `batchLimit`；标签多时文件夹加载仍拆成多批（每次 `FolderDetails` 一次性返回，不再逐批渐进渲染），缓存未命中时仍是一次 `labels.list` 加 `ceil(L/25)` 个 `labels.get`，且缓存无过期、邮箱变化不会自动反映，只能靠 `Invalidate()` 清空；Gmail 按子请求计配额，batch 只省往返与连接开销、不省配额，`metadataHeaders` 也仍是每会话一份；`resultSizeEstimate` 是估算值；列表中附件标记依赖 metadata 返回的 part 树，若某条消息未返回 parts 则该行可能漏标记；token 与代理凭据都是明文 0600 存储，没有系统密钥环集成。

## 4. UI 架构（`internal/ui`）

### 4.1 单点状态

`App` 持有全部可变状态；各视图（`topBar`、`sidebarView`、`listToolbar`、`messageListView`、`readerView`、`statusBar`、`perfMonitor`、`composeView`、`settingsView`、`menuOverlay`）只读 `App`，并各自保存瞬态（`layout.List` 滚动偏移、hover 锚点、`widget.Selectable` 等）。视图之间不互相引用。

状态分四类：

1. 会话上下文：`activeAcc`、`activeFolder`、`query`、`folders`。`activeFolder == ""` 就是搜索视图（此时 `query` 非空），侧栏不高亮任何项；非空则是该文件夹/标签的浏览视图。
2. 分页：`page`（1 基）+ `tokens`（`tokens[i]` 打开第 `i+1` 页，`tokens[0]` 恒为空游标）。
3. 阅读：`selectedThread`、`thread`、`msgs`、`expanded`、`loadingThread`。
4. 窗口/浮层：`decorated`、`maximized`、`csd`、`windowActions`、`overlay` + `compose`/`settings`/`menu`。

窗口句柄、post 队列、退出标志是唯一跨 goroutine 共享的部分（`winMu`、`postMu`、`quitting`）。

状态条的 4s 消退定时器由 `statusBar` 自己持有（`timer`，UI goroutine 独占）：`show`/`clear` 先 `Stop` 旧定时器再改文案，`notify` 随后重新装一个，因此新通知、点击状态条与 Esc 都不会留下上一条文案继续撑到超时。定时器回调仍经 `post` 回投并校验 `gen`，覆盖「回调已入队、新消息刚到」的窗口。

### 4.2 并发模型

- 所有 `mail.Service` 调用在 goroutine + `context.WithTimeout` 中执行；结果只通过 `App.post(fn)` 进入**单一队列**，由 UI goroutine 在每帧开头 `drain()`。goroutine 内禁止改 UI 状态。
- 所有 Service 调用走 `App.async`：它持有超时、`post` 回投与错误提示。需要被替代的请求（列表/文件夹/会话）把 `cancel` 存在 `App` 字段里，下一次同类请求先 cancel 旧请求，窗口销毁时 `cancelRequests` 全部停止。
- 三阶段加载由 `onSuccess` 链式编排：`loadFolderNames` 成功后在其回调里发起 `loadThreadsKeepingScroll`，列表就绪后再发起 `loadFolderDetails`。每次调用各走一次 `async`，结果经 `post` 回投并套用同一代际/账号校验，迟到结果直接丢弃。列表在 detail 到达前保持加载态，页数据到达后一次性 `setThreads`（切页/切换，回顶）或 `mergeThreads`（同页刷新，保留滚动与仍存在的勾选）；成功回调只收尾页码/工具栏，不再重设行数据。
- 过期响应靠**代际号**丢弃：`listGen`（列表/分页）、`folderGen`（文件夹未读数）、`msgGen`（阅读器）；回调里同时校验 `activeAcc`、页码或会话 ID。`context.Canceled` 是预期结果，不弹错误提示。
- 切账号 / 切文件夹 / 搜索时先清空视图（`App.clearList` 清 `setThreads(nil)` 并抹掉工具栏范围文案，`resetSelection` 关阅读器）再发起加载，避免旧行、旧标签色块与旧页码闪现。
- 列表请求按查询分流：`query` 非空走 `SearchThreads`，否则 `ListThreads`。两者共用同一个 `listGen` / `listCancel` / `tokens`，因此搜索、翻页、切文件夹互相取代；worker 启动前快照 `folder` 与 `query`，worker 内不读 `App` 字段。
- 列表只渲染可见行（`layout.List`），行内控件状态按会话 ID 缓存在视图结构里。

### 4.3 浮层

- `App.overlay` 是**单一槽位**：`menuOverlay` / `settingsView` / `composeView` 互相替换。
- 吞点击层必须是 `Stack` 的第一个子对象，否则挡住卡片自身的交互。菜单用 pass-through blocker（点击外部只关菜单），设置保持模态阻塞；写信浮窗**不设全窗 blocker**，由卡片内 `Clickable` 自己消费空白点击。
- Esc 优先级：状态条 / 菜单 → 写信 / 设置 → 阅读器返回列表。Esc 不清空查询、不在搜索时回退文件夹；搜索框胶囊的 ✕ 只清空输入框文字，列表保持当前搜索结果（`query` 与 `activeFolder` 都不变）。
- 面板内的下拉**必须页内展开**，不能复用 `App.showMenu`：`showOverlay` 先 `closeOverlay()` 把 `a.settings` 置 nil，菜单关闭时 `hideOverlay()` 又直接清空 `a.overlay`，设置面板不会回来。设置页的语言选择即按此实现（`settingsView.langOpen`，候选行在同一张卡片内展开）。

### 4.5 键盘

- 所有按键在 `internal/ui/keys.go` 的单一入口路由：先镜像搜索框焦点，再分发全局层（Ctrl+W 关窗口、Ctrl+Q 退出进程），然后按「状态条 → 浮层 → 阅读器」的 Esc 顺序处理，最后才分发其余快捷键。
- 键表 `shortcutTable` 同时驱动分发与「设置 → 快捷键」页：每条记录带分组、位、修饰键 (`mods`)、`global` 标志、Gio 键名与键帽文本，`readShortcuts(gtx, global)` 只读其中一半，设置页遍历整张表，新增按键只需改一处。列表与阅读器都生效的 E / Delete / S / U 归入「全局」组，帮助页因此不出现重复行；Esc 也在表内，按位在浮层/搜索门禁之前处理。
- `global` 行（Ctrl+W / Ctrl+Q）要求修饰键，因此在两道门禁之前读取，写信浮窗、设置页、搜索框聚焦时同样可用；Ctrl+W 置位 `windowActions` 的 `ActionClose`（与标题栏关闭按钮同一条路径），Ctrl+Q 调 `requestQuit()`，同帧同时按下以退出优先。
- 搜索框焦点用 `gtx.Focused(&a.top.search)` 每帧采样，**不能**用 `gtx.Event(key.FocusFilter{...})` 去读：`FocusFilter` 同时匹配目标的 `EditEvent`/`SnippetEvent`/`SelectionEvent`/`CompositionEvent`，而 `Router.Event` 命中即从该帧删除事件，读焦点会把编辑器的文本输入一起吃掉（表现为能聚焦、光标会闪、但打不出字）。
- 读快捷键是破坏性的，因此裸键只在没有编辑器持有键盘时才读：搜索框聚焦时只读 Esc（其余字符、退格、回车全部留给编辑器，回车才能触发 `Editor.Submit`），浮层打开时同样只处理 Esc。未读取的事件在帧末由 `Router.Frame` 直接丢弃，不会残留到编辑器失焦后才触发。全局层是例外：它只要修饰键组合，而 `widget.Editor` 只绑定 Ctrl+Z/C/V/X/A、Ctrl+Backspace/Delete、Home/End 与方向键，Ctrl+W / Ctrl+Q 不会被编辑器消费。
- 字母键名是 Gio 的大写形式（`J`/`K`/`C`…），普通字母过滤不带修饰键，所以 `Ctrl+C` 不会被当成「写信」；反过来 `key.Filter.Required` 的匹配要求事件修饰键是该集合的子集，`Ctrl+Shift+W` 也不会触发「关闭窗口」。
- 列表有一个键盘光标（`cursorThread`，页/文件夹切换时清空），行高亮复用选中色；移出视口时通过设置 `layout.List.Position.First` 拉回可见区域。动作快捷键复用与鼠标相同的 `App` 方法，不另开一条数据通路。
- 焦点归属由 App 自己声明：打开会话、切文件夹、开关浮层都会请求把焦点交还窗口根，避免搜索框在别处继续吃字符；`/` 主动把焦点交给搜索框。

### 4.4 窗口生命周期

- `Run()` 掌管生命周期循环：`App` 实例**跨窗口复用**，`attach`/`detach` 维护 `win`，托盘通过容量 1 的 `openCh`/`quitCh` 唤醒。
- 关闭窗口默认销毁窗口、进程留在托盘；无通知区时降级为「关闭即退出」。
- `windowActions` 收集标题栏请求，**帧绘制结束后**统一 `w.Perform`，避免布局中途销毁窗口；Ctrl+W 与标题栏关闭按钮共用这条路径，Ctrl+Q 直接走 `requestQuit()`。
- `startInitialLoad()` 由 `startOnce` 门禁，首帧加载只跑一次，后续窗口重建不重复。
- Gio 的 `app.Main()` 在 Linux 不返回，退出必须显式 `cleanupTemp()` + `os.Exit(0)`；不要调用 systray 返回的 `end()`。
- 生命周期循环之外的结束方式也要收拾现场：`watchInterrupt()` 在 `newApp()` 之后就开始监听 `SIGINT`（`Ctrl+C`，`signal.Notify` 就地注册），收到后先 `cleanupTemp()` 再 `os.Exit(0)`，否则托盘驻留期间被 `Ctrl+C` 停止的运行都会留下临时目录。`SIGTERM`/`SIGHUP` 故意不接管，保留默认行为：桌面会话结束、`kill`、终端挂断都按系统语义立即结束进程，不等待清理。信号路径与 UI goroutine 会并发访问临时目录，`tmpMu` 保护 `tmpDir`，`tmpClosed` 保证清理后不会再建一个新目录。

## 5. 布局与视觉规格

- 顶栏高度：平台装饰 64dp，CSD 单行 48dp，一律经 `App.headerHeight()`；侧栏 256dp；列表行高 40dp；内容卡片圆角 16dp；窗口默认 1200×840、最小 560×420。
- 两栏：侧栏 + 单一内容卡片；列表与阅读器在同一个 `Stack` 里按 `selectedThread` 切换，不存在第三栏。
- 色板令牌集中在 `palette.go`（页面底 `#F6F8FC`、选中 `#EAF1FB`、主色 `#D3E3FD`、行 hover `#F2F6FC` 等），视图里不写裸色值。
- 列表行：勾选 | 星标 | 发件人 | 标签–主题–摘要 | 附件 | 日期。发件人固定 168dp，尾部附件/hover 操作叠在固定 108dp 底层上（宽度恒定，正文列不漂移）；会话数量紧跟发件人；悬停换操作不改变行布局。主题之前的标签是 Gmail 风格的实心色块：圆角 4dp、左右内边距 6dp、上下内边距 2dp、字号 11sp、块间距 4dp，底色取 `labels` 的颜色、文字按 WCAG 对比度在深色与白色之间取更清楚的一个（无颜色标签用 `colChipFallback` 浅灰底），标签名保留完整层级路径；只显示用户标签，系统标签（`INBOX`/`UNREAD`/`CATEGORY_*`）与当前浏览的标签本身不显示。标签段最多占主题-摘要区宽度的一半，低于 48dp 时整段隐藏，剩余宽度仍由主题与摘要均分。
- 分页与全选在列表工具栏：页码由 `tokens` 推出，工具栏显示范围与总数；全选只作用于当前页，切页 / 切文件夹 / 搜索即清空。
- 标签区按 Gmail 的层级展示：标签名里的 `/` 是层级分隔（`工作/项目A` 是 `工作` 下的标签），子标签每层缩进 18dp（最多 3 层）且只显示末段名称；有子标签的父标签把颜色圆点换成展开符号（折叠 ›、展开 ▾，取标签自身颜色），符号占圆点原来的 20dp 单元格、悬停时显示同色圆底并切换手型光标，点击只折叠子树、不选中标签；叶子标签仍显示颜色圆点；折叠状态只存内存（默认全展开），选中标签的严格祖先强制展开——此时祖先行的符号保留 ▾ 外观，但不显示悬停圆底、不切换手型、点击无效（折叠会把选中项藏起来）；选中父标签本身时仍可折叠其子标签；缺少父标签的数据按根级显示完整名称。
- 滚动位置：切页 / 切文件夹 / 搜索走 `setThreads`（回到顶部），归档、删除、加星后的同页刷新走 `refreshThreads`（保留偏移）。
- 后端没有的能力不做假入口、不放禁用占位。
- **省略号截断不能交给 `MinSize`**：`TruncateEllipsis` 的最小宽度会退化成「…」，再被 Center/Border 压缩。必须给固定宽度槽位。
- 顶栏中任何随语言变宽的元素都必须放进固定宽度单元格：品牌 wordmark 用 `brandNameEms`（em 倍数）定宽、单元格内左对齐（居中会把中文「邮笺」两侧的余量变成印章与文字之间约 20dp 的空隙），窗口窄于 820dp 隐藏；搜索胶囊是固定 dp 宽度且编辑器占满单元格（`Constraints.Min.X = Max.X`，否则文字从中间开始）。

## 6. Gio 实践约定与踩坑

1. **点击后立即请求重绘**：`Clickable.Clicked` 在布局 pass 中消费，状态翻转可能发生在整窗绘制之后。任何自绘控件命中后统一走 `invalidateAfterTap(gtx)`（`gtx.Execute(op.InvalidateCmd{})`），禁止各控件内联。
2. **首帧的 Invalidate 会被吞**：Gio 在处理帧期间丢弃 `w.Invalidate()`。首次加载必须推迟到事件循环（`startInitialLoad`），并且 `post()` 用一个共享槽位（`retry`，与队列同受 `postMu` 保护）在 `postRetryDelay=100ms` 后补一次重绘：一次回调爆发只装一个定时器，回调触发时清空槽位并重新检查队列，队列非空才再 invalidate（重试幂等，定时器数量与调用次数无关）。
3. **`layout.List` 必须挂在视图结构体上**：每帧新建会丢滚动偏移，并且只有可见行会被布局。
4. **clip 是交集，画不出「环」**：描边要「整体先涂边框色 → 内缩涂填充色」，见 `strokeRect`。
5. **父子同一帧都收点击**：父行 `Clickable` 与子控件（复选框、图标按钮）会同时命中。子控件命中后打 `hit` 标记，父级判定**后移到布局之后**再执行。
6. **禁止跨帧重放录制宏**：`op.Record` 的宏在帧末以当前变换重放，跨帧重放会错位并触发 GPU `collectOps index out of range` panic。tooltip 队列 `th.tips` 每帧清空，只存放本帧录制的标签，帧末在无 clip 的窗口坐标系重放。
7. **tooltip 锚点要冻结**：窗口指针坐标与按钮局部坐标分别取整会 ±1px 抖动；进入 hover 时算一次 `anchor` 并冻结，离开时重置。只给语义不直观的图标提示（归档 / 删除 / 标记已读 / 标记未读），其余按钮无提示。
8. **光标必须 clip 到控件矩形**：`cursor.Apply` 作用于当前 clip，裸调用会让整片区域变手型。
9. **字体**：`text.Shaper` 只建一次，界面族固定 `sans-serif, emoji`（字体来源与顺序原因见 [§14.1](#141-文本字体)）；图标字形尺寸按 (fill, size, pxPerSp) 缓存，避免每帧 shaping。
10. **不要在视图构造期长期缓存译文**：语言切换会重建视图，缓存的旧语言字符串不会自动更新。

## 7. 窗口装饰（CSD）与光标

### 7.1 机制

- 应用主动 `app.Decorated(false)`；唯一判定出口是 `clientSide() = !decorated || (csd && WAYLAND_DISPLAY != "")`。**X11 永不画自绘标题栏**（Gio 在 X11 恒要求服务端装饰，会重复）。
- 自绘标题栏承载侧栏按钮 / logo+品牌 / 搜索胶囊 / 设置 / 头像 / 分隔线 / 最小化·最大化·关闭（高度见上节，`windowcontrols.go` 用 clip 自绘，字体无窗口字形）。
- KDE Wayland 上无法自动识别：KWin 对强制无边框的窗口在 `xdg-decoration` 仍回 `server_side`（源码注释 *Faked as server_side mode*），协议层探测不到。因此只提供 `clientDecorations` 显式开关，操作步骤见 [§7.2](#72-使用者配置)。
- 拖动只把 `system.ActionInputOp(system.ActionMove)` 登记在空白抓手区（clip 包裹、绘制顺序在按钮之后）；按在搜索框/按钮上不能被 move 消费。
- 光标主题解析顺序：`XCURSOR_THEME`/`XCURSOR_SIZE`（用户显式设置）→ XDG portal `org.freedesktop.portal.Settings`（`org.gnome.desktop.interface`，750ms 超时）→ KDE `kreadconfig6`（回退 `kreadconfig5`）→ GNOME `gsettings get`（750ms 超时）。导出前必须校验 `<theme>/cursors` 真实存在，否则 Wayland `wl_cursor_theme_load` 失败会导致无法建窗。
- 门户必须排在 KDE 之前：`org.gnome.desktop.interface` 由 `xdg-desktop-portal-gtk` 转发 dconf 实现，Flatpak 内也靠它透视宿主设置；KDE 的 portal 只注册 `org.freedesktop.appearance` 与 `org.kde.*`，查询该命名空间会失败并自然落到 KDE 来源，因此不做会话判断。
- KDE 不能直接读 `~/.config/kcminputrc`：KWin 用 `KConfig::NoGlobals` 打开它，而该标志的字面值就是 `CascadeConfig`，会把 `$XDG_CONFIG_DIRS/kcminputrc`（通常 `/etc/xdg/kcminputrc`）当默认层读进来；KCM 也只在用户改过光标时才把 `cursorTheme`/`cursorSize` 写进用户文件。所以改用 `kreadconfig6 --file kcminputrc --group Mouse --key cursorTheme`（不传 `--default`），由 KDE 自己完成级联。
- `gsettings` 放最后兜底：它在任何桌面都会返回 schema 默认值（`Adwaita`），只有门户与 KDE 都没结果时才轮到它，避免把其他桌面的真实设置盖掉。Flatpak 内 `gsettings`/`kreadconfig6` 读不到宿主配置，这是从纯 D-Bus 门户改为 CLI 的已知代价。
- 未导出尺寸时 Gio 的 Wayland 后端自己用 32（`os_wayland.go`），KDE 未设置时是 24，所以 `kreadconfig6` 读不到值时两侧尺寸可能差一档。
- 应用自己导出的值会被下一次解析读到（误判为「用户设置」并且不再回查 portal），因此 `cursor` 包用 `lastExport` 在读取 env 时屏蔽自写值；`Apply` 只在每次建窗前调用一次。运行中换主题需重开窗口。
- **CSD 下没有边缘缩放**：Gio v0.10.3 的 Wayland 后端只监听 `zxdg_toplevel_decoration_v1` 的 `configure`，从不发送 `set_mode`；KWin 对强制无边框窗口答复 `server_side`（源码注释 *Faked as server_side mode*），于是 `Config.Decorated == true`。缩放热区 `systemGesture()` 首行即 `if Mode != Windowed || Decorated { return nil, 0 }`，所以既没有缩放光标也不会发出 `xdg_toplevel.resize`。应用只拿得到 `io/system` 的 `ActionMove`（`ActionInputOp` 注释：only ActionMove is supported），拿不到 `wl_surface`/`xdg_toplevel`，不改 vendor 无法实现；替代操作见 [§7.3](#73-可感知限制)。
- **最小化后的模式同步**：Gio 的 Wayland 后端把 `Mode` 置为 `Minimized` 后只靠 `wl_surface.enter` 清除，而窗口在同一 output 恢复不会触发 enter；`Configure` 又会丢弃 `prev.Mode == Minimized` 时的一切模式切换，于是恢复后最大化按钮、标题栏拖动与缩放热区全部失效。应用不做可视化补偿：`performWindowActions`（`app_lifecycle.go`）在执行动作后，按纯函数 `windowSteps` 的判断在最小化时补一次 `app.Windowed`，复位这条僵死记录（协议层窗口仍然是最小化的）。代价是幂等的最小化前已最大化情形需要点两次最大化：第一次只把状态同步回合成器本已保持的最大化。

### 7.2 使用者配置

- **跟随系统（默认）**：平台报告「不装饰」时应用自绘单行标题栏（GNOME Wayland、Windows、macOS）；平台已装饰时保持系统边框（KDE 默认的 Breeze 标题栏）。
- **KDE Wayland 需要两步**：① 在 *系统设置 → 窗口管理 → 窗口规则* 新建规则（窗口类 `yauzin`，即二进制名），把「无标题栏和边框」设为「强制」；② 在应用 *设置 → 通用* 打开「使用客户端标题栏」。切换后立即生效，无需重启。第二步不能省：`xdg-decoration` 协议没有「无装饰」取值，KWin 对强制无边框的窗口仍报告服务端装饰，应用无法自动分辨（原因见 §7.1）。
- **X11 无法使用客户端标题栏**，该开关无效。
- **光标主题覆盖**：环境变量优先级最高，可绕过自动解析直接指定；运行中更换系统主题后，从托盘关闭再打开窗口即可生效。

  ```bash
  XCURSOR_THEME=breeze_cursors XCURSOR_SIZE=24 ./yauzin
  ```

### 7.3 可感知限制（Gio v0.10.3）

- Wayland 后端不解析 `xdg_toplevel` 状态：由窗口管理器（快捷键 / 边框双击）最大化或还原时，标题栏的最大化图标不会同步；应用内的最大化按钮可以正常切换。
- 同时使用 KDE Wayland 的「无标题栏和边框」规则与「使用客户端标题栏」时，窗口无法用鼠标拖边缘缩放：边缘没有缩放光标，拖动也没有反应（机制见 §7.1 末条）。替代操作：用 KWin 的 `Alt+F8` 缩放快捷键，或关闭「使用客户端标题栏」改用系统边框（可正常拖拽缩放）。

## 8. 正文与 HTML 安全

- 阅读器只渲染 `text/plain`（可选中复制），HTML 与原文都从每封邮件卡片头部的「更多」菜单打开，回复/转发旁不再有浏览器按钮。选区按 `msg.ID` 变化时重建/清空，避免跨邮件残留。
- 菜单三项：`安全预览`（注入严格 CSP）、`完整预览（不安全）`（只声明 charset，不加策略，脚本与远程资源可用，风险写在文案里且不再二次确认）、`查看原始邮件`（`RawMessage` → 原文页）。前两项仅在有 HTML 正文时出现，第三项恒有。菜单项文案不复用旧键 `reader.openInBrowser`。
- 原文页（`htmlsource.go`）是独立文档：顶部元信息表（邮件 ID、账号、发件人、收件人、抄送、主题、日期），下方 `<pre>` 展示完整源码。源码经 `html.EscapeString` + `strings.ToValidUTF8` 处理，绝不当 HTML 渲染；页面自身固定 `default-src 'none'; style-src 'unsafe-inline'`。
- HTML 落盘：文件名 = 净化后的消息 ID（`[A-Za-z0-9._-]`，截断 64）+ 内容 sha256 前 8 字节（防目录穿越、绕开 `file://` 缓存），模式后缀 `-unsafe` / `-source` 保证三种产物互不覆盖（安全预览沿用原命名）。
- CSP 用 `x/net/html` 解析后插到**真实 `<head>` 的第一个子节点**（防注释/脚本里的假 `<head>`），`html.Render` 重新序列化是有意取舍；解析失败走 `wrapperDocument` 安全包裹。策略为 `default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'`，远程图片/追踪像素故意禁用。默认路径不被弱化：不点「完整预览」时策略照旧注入。
- 临时目录惰性创建于 `$XDG_RUNTIME_DIR/yauzin`（0700；该变量缺失、非绝对路径或不可用时回退 `os.MkdirTemp` 到系统临时目录）：会话运行目录在登录会话结束时由系统回收，被 `SIGKILL` 的运行最多留下一个会自动过期的目录，不会在 `/tmp` 里逐次累积。会话内路径固定，首次创建前先删掉上次异常退出留下的同名目录；正常退出与 `Ctrl+C`（`SIGINT`）都会整体删除，删除后 `tempDir()` 返回错误而不再重建。
- `internal/htmltext` 只服务写信时引用原文；输入上限 1MiB。

## 9. 国际化

- 自建 `internal/i18n`（`embed` + go-i18n）：en 是权威源，zh-Hans 必须 key 完全对齐（`i18n_test.go` 守护）；`T`/`N` 总是带 `DefaultMessage`，缺 key 时回退英文而不是空白。
- 语言列表由内嵌文件与各文件的 `language.name` 推导；`"auto"` 跟随系统 locale。
- 运行时切换顺序敏感：`i18n.SetLanguage` → `prefs.Save` → 重建侧栏 / 清空阅读器卡片缓存 → 更新窗口标题 → `tray.Relabel` → 新状态条 → **最后**重建设置面板。
- 系统文件夹名按 `FolderKind` 翻译，用户标签名与账号名不翻译。

## 10. 持久化

`internal/prefs` 以 0600 + 临时文件 + rename 原子替换写入 `os.UserConfigDir()/yauzin/settings.json`（`YAUZIN_CONFIG_DIR` 可覆盖目录）。字段：

| 字段 | 含义 |
| --- | --- |
| `language` | 语言标签，空/`auto` 表示跟随系统 |
| `clientDecorations` | Wayland 下强制自绘标题栏 |
| `showPerfMonitor` | 顶栏显示性能读数 |
| `proxy` | 出站邮件请求使用的代理（`http`/`https`/`socks5`，空表示直连） |
| `logLevel` | 控制台日志级别（`error`/`warn`/`info`/`debug`，空或未知表示 `info`） |
| `activeAccount` | 上次使用的账号 ID，下次启动优先打开；为空、账号已删除或后端构建失败时回退列表第一个账号 |

约定：`LoadOrZero` 把「文件不存在」当首次运行，其余错误只告警、不覆盖；修改设置一律「先落盘成功再改内存」，失败提示并回滚。`activeAccount` 是例外：账号切换已经发生，写盘失败只记日志、不弹提示；删除账号也不清该字段，下次启动匹配不到账号即回退，行为仍然正确。

账号凭据与令牌另存 `accounts.json`（`internal/accounts`，同为 0600 原子写，目录由 `prefs.Dir()` 统一解析，`YAUZIN_CONFIG_DIR` 对两者同时生效）。它不放进 `settings.json`：令牌刷新频繁且由后端在 worker goroutine 写回，与 UI 设置的读写节奏不同。

## 11. 托盘与单实例

- `internal/tray` 是 `fyne.io/systray` 的薄封装：左键 `SetOnTapped` 直接开窗，右键菜单只有「打开 / 退出」；回调在库的 goroutine 上，必须经 `post` / channel 回 UI 线程。通知区预检失败时 `Start` 返回错误，应用降级。托盘只持有 PNG 字节：`Icons.Idle`/`Icons.Active` 分别对应灰点与红点两态，`SetActive` 在状态未变化时不重复调用 `SetIcon`。
- 图标设计源为 `internal/ui/assets/icon-gray.svg`（灰封蜡，托盘 idle）与 `icon-red.svg`（红封蜡，托盘 unread 与顶栏 mark），由 `scripts/gen_app_icons.py` 用 rsvg-convert 派生 PNG 后提交；运行时不解析 SVG。顶栏 mark 用红点母版，cell 固定 36dp：内嵌 32–128px 多个尺寸，按设备像素取刚好覆盖的一张（`internal/ui/appicon.go`），1x / 1.25x / 1.5x / 2x / 3x 正好命中 36 / 45 / 54 / 72 / 108px（旧的 32/40/48/64/96/128px 变体保留，覆盖 1.1x、1.75x 这类零散比例），缩放比例为 1，不会被线性滤波插值而发糊。Gio v0.10.3 没有窗口图标 API，窗口/任务栏图标由桌面集成使用 `icon-red-512.png`（与顶栏 mark 同一母版）。
- `internal/singleinstance` 用会话总线名 `me.zhsj.yauzin`（同时是 Gio application ID）保证单实例：第一个进程持有名字并导出 `Open`，后续启动调用 `Open` 唤起窗口后 `os.Exit(0)`。Wayland 不允许强制前置已有窗口，这是已知限制。

## 12. 性能监视器

- 数据源只用标准库：CPU 取 `runtime/metrics` 的 `/cpu/classes/total:cpu-seconds` 两次采样差 ÷ 时间差；内存取 `runtime.ReadMemStats().HeapAlloc`；goroutine 用 `runtime.NumGoroutine()`；FPS 用相邻 `app.FrameEvent.Time` 差做 EMA。
- 仅在可见时跑 1s ticker，隐藏即 `Stop`；采样结果走 `post` 回 UI 线程（同时维持 1Hz 帧循环以便测 FPS）。
- CPU 差分可能因 GC 突发出现脏样本（> `100×GOMAXPROCS` 视为无效，沿用上次值），正常样本用 alpha=0.5 EMA。数值定宽填充防抖动。

## 13. 环境与构建

### 13.1 环境要求

- Go 1.26+（`go.mod` 声明 `go 1.26.0`）；开发机工具链是 `/run/host/lib/go-1.27/bin/go`（go1.27.2），构建镜像内为 Debian unstable 源的 `golang-go` 1.27。
- CGO 开启：Gio 没有纯 Go 后端。
- Linux 运行需要 X11/OpenGL 或 Vulkan 运行库（`libx11`、`libxkbcommon`、`libGL`/`libvulkan`）；从源码编译还需要 `libx11-dev`、`libxkbcommon-x11-dev`、`libxcursor-dev`、`libxfixes-dev`、`libwayland-dev`、`libegl-dev`、`libvulkan-dev`。
- 文件选择与附件保存通过 `xdg-desktop-portal` 提供的系统对话框；portal 不可用时附件保存回退到 `~/Downloads`。
- macOS 需要 Xcode Command Line Tools，Windows 需要 MinGW-w64。
- 开发沙箱 `CGO_ENABLED=0` 且缺少上述开发库：任何（直接或间接）import `gioui.org/app` 的目标（`internal/ui`、`cmd/yauzin`）只能在 Docker 内编译，典型报错 `build constraints exclude all Go files in .../gioui.org/internal/vk`。
- 沙箱内访问外网走本地代理，命令前置 `https_proxy=http://127.0.0.1:1081`；`internal/cmd/gmailgen` 只在下载 Discovery 文档时需要它，代码本身不读代理配置。
- 格式化使用 `gofmt`（沙箱 `PATH` 内，go1.27.2）；import 增删与分组使用 Go 扩展安装的 `goimports`（`~/go/bin/goimports`，**不在沙箱 `PATH`**），命令见 §13.5。

### 13.2 依赖与 vendor

GUI 使用 Gio v0.10.3，文件选择使用 `gioui.org/x/explorer`，Gmail 授权使用 `golang.org/x/oauth2`。`vendor/` 已被 `.gitignore` 排除、不入库，但构建镜像使用 `-mod=vendor`，所以本地必须先生成。依赖变更后（沙箱无外网直连，走镜像代理）：

```bash
GOFLAGS=-mod=mod CGO_ENABLED=1 \
  GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.google.cn \
  go mod tidy
GOFLAGS=-mod=mod CGO_ENABLED=1 \
  GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.google.cn \
  go mod vendor
```

Gmail 只引入 `golang.org/x/oauth2`，不引入 `google.golang.org/api`：官方生成客户端的 `option` 包直接依赖 `google.golang.org/grpc` 与 `cloud.google.com/go/auth`（`protobuf` / `OpenTelemetry` 由 `grpc` 间接带入），而本项目只用到 4 个 REST 端点。

API 类型不手写也不引官方客户端：`internal/cmd/gmailgen` 读取 Google 的 Discovery 文档生成 `internal/mail/gmail/discovery_types.go`。Gmail 没有 OpenAPI 规范，Discovery 文档是唯一的机器可读来源；它不入库，生成器每次自行下载当前版本，并把 `revision` 写进生成文件头部。重新生成：

```bash
https_proxy=http://127.0.0.1:1081 go generate ./internal/mail/gmail
```

生成后按 `git diff` 审阅：新增/改名字段可能让调用点编译失败，这正是需要人工确认 schema 变化的地方。

### 13.3 本地构建与运行

本机已安装 X11/OpenGL/Vulkan 开发库时：

```bash
go build -o yauzin ./cmd/yauzin
./yauzin
```

### 13.4 Docker 构建

本机缺少 C 依赖时使用仓库内的构建镜像 `Dockerfile.build`：基于 `debian:unstable`，apt 源为 USTC 镜像，Go 工具链（`golang-go`）与 Gio 需要的 X11 / Wayland / EGL / Vulkan 开发库全部从 Debian 源安装（不下载官方 Go tarball，也不需要 `zig cc`）。镜像内已固化 `CGO_ENABLED=1`、`GOFLAGS=-mod=vendor`、`GOTOOLCHAIN=local`、`GOCACHE=/gocache`，运行时不必再传 `-e`。

```bash
docker build -t yauzin-gio-builder -f Dockerfile.build .

docker run --rm \
  -v "$PWD":/app -w /app \
  -v yauzin-gocache:/gocache \
  yauzin-gio-builder \
  go build -o /tmp/yauzin ./cmd/yauzin
```

- `yauzin-gocache` 是命名卷，挂到容器内 `/gocache`，编译缓存跨容器保留：热缓存一次编译约 1–2 秒，冷缓存或强制全量（`go build -a`）约 10–15 秒；清空用 `docker volume rm yauzin-gocache`。漏掉这个卷等于每次冷编译。
- 改动 `Dockerfile.build` 后必须重建 `yauzin-gio-builder`（layer cache 命中通常几秒），否则仍在用旧工具链/旧依赖。
- 只做编译验证时产物写容器内 `/tmp`，避免仓库根出现 root 属主的二进制；确实要交付二进制时用 `docker cp` 取出后再 `chown $USER: yauzin`。
- 运行 GUI 需在宿主桌面会话中进行，不要在容器或沙箱里启动。

### 13.5 静态检查与测试

全量检查（编译 Gio 的部分必须在容器内执行）：

```bash
docker run --rm -v "$PWD":/app -w /app -v yauzin-gocache:/gocache \
  yauzin-gio-builder sh -c 'go test -vet=all ./...'
```

纯 Go 包可直接在本地运行（`internal/ui` 与 `cmd/*` 导入 Gio，必须进容器）：

```bash
go test -vet=all ./internal/prefs ./internal/i18n ./internal/htmltext ./internal/mail/... ./internal/tray ./internal/cursor
```

格式化与 import 分组（沙箱直接执行，两条检查都无输出即合格）：

```bash
gofmt -l internal/ cmd/
gofmt -w internal/ui

~/go/bin/goimports -local github.com/zhsj/yauzin -l internal/ cmd/
~/go/bin/goimports -local github.com/zhsj/yauzin -w internal/ cmd/
```

- import 块固定三段：标准库 / 第三方 / `github.com/zhsj/yauzin/...`。分组由 `-local` 决定，不要手工插空行。
- `goimports` 接受目录与多个路径，不需要 `git ls-files` 展开；它顺带做 gofmt 等价格式化，所以 `-w` 之后 `gofmt -l` 仍为空，两条检查都保留。
- `goimports` 只处理磁盘文件，编辑器里未保存的改动需先保存。
- 不用 `gopls` 做格式化或分组：`gopls format` 等价 gofmt、只排序不分组（`local` 设置只在 Organize Imports 或插入新 import 时生效，格式化请求 "merely sorts the existing imports"），命令行 `gopls imports` 也没有 `-local` 参数，只能分出两组。

## 14. 字体与图标资产

### 14.1 文本字体

文本字体完全使用系统字体：Gio 的 text shaper 自 v0.2.0 起默认加载系统字体（基于 fontconfig 目录与配置解析，首次扫描约 0.2–0.5s 并缓存到用户缓存目录），中文由系统安装的中文字体提供；Flatpak 沙箱内的 `/run/host/fonts` 也通过运行时的 fontconfig 配置自动可达。系统一个字体都没有时，界面回退到内置的 Go 字体（仅拉丁字符）。界面文本使用 `sans-serif, emoji` 字体族列表（`internal/ui/fonts.go` 的 `textTypeface`），第一段跟随系统默认 UI 字体，第二段只兜底前者渲染不了的 emoji 码点。

顺序不能反过来。emoji 属于 Common 脚本，go-text 会把它并入前一个 Latin/CJK run 一起做字体回退；只用 `sans-serif` 时，覆盖 Latin 且数据库顺序更靠前的单色符号字体（如 Symbola）会抢到 emoji，显示成黑白。把通用族 `emoji` 放在第二段后，它在精确族匹配阶段命中彩色 emoji 字体（fontconfig 替换表优先 Noto Color Emoji），不受前一 run 脚本影响。也不能把 `emoji` 放第一段：Noto Color Emoji 的 cmap 同样覆盖 ASCII 数字、`#`、`*` 和空格，会以 1.245em 的 emoji 宽度渲染它们。

彩色 emoji 依赖字体自带位图（CBDT/CBLC、sbix）：Gio 只渲染位图与轮廓字形，COLR/CPAL 彩色字形不支持，仅有 COLR 字体的系统会显示空白 emoji。已知限制：go-text 的 `splitByFace` 让空白不触发换字体，紧跟在 emoji 后面的空格会沿用 emoji 字体的空格度量（约 1.245em，明显比正常的 0.26em 宽）；应用层不改文本无法规避，待上游修正。

### 14.2 图标字体

图标使用内置的 Material Symbols Rounded 子集（Apache-2.0，见 `internal/ui/assets/LICENSE-Apache-2.0.txt`）。子集只保留 `internal/ui/icons.go` 中列出的码点，体积仅几 KB；新增图标后重新生成：

```bash
python3 scripts/gen_icon_fonts.py            # 需要 Python3 + fonttools
python3 scripts/gen_icon_fonts.py --check    # 校验现有字体是否与代码同步
```

脚本默认从 Google material-design-icons 仓库下载上游变量字体，也可用 `--font <路径>` 指定本地副本；输出字节级幂等。`icons_test.go` 会在生成字体缺少码点时失败。

### 14.3 应用图标

设计源是 `internal/ui/assets/icon-gray.svg`（灰色封蜡）与 `icon-red.svg`（红色封蜡），两者几何完全一致、只有封蜡颜色不同。程序不解析 SVG，二进制只嵌入由二者派生的小尺寸 PNG；`icon-red-512.png` 不进二进制，留给桌面集成（`.desktop` 的 `Icon=` 或任务栏），因为 Gio v0.10.3 没有窗口图标 API。改图标后重新生成：

```bash
python3 scripts/gen_app_icons.py            # 需要 rsvg-convert（librsvg2-bin）
```

母版是 174×118 的宽扁 `viewBox`，而产物必须是正方形。脚本渲染前会改写根标签：去掉 `width`/`height`，把 `viewBox` 沿短边补成正方形（信封本来就贴着四边，补齐只加边距、不裁内容），再按宽度渲染（`rsvg-convert -w`）。这样既不会拉伸图形，图标也能填满方形 cell。母版保持原样即可。

托盘图标只有两态：没有未读时封蜡圆点为灰色（`icon-gray.svg`），有未读时变红（`icon-red.svg`），两者圆点大小一致。顶栏品牌 mark 取红点母版，内嵌 32–128px 多个尺寸，按设备像素选择刚好覆盖的一张（`internal/ui/appicon.go`）；mark 固定 36dp，所以 1x / 1.25x / 1.5x / 2x / 3x 下设备像素正好是 36 / 45 / 54 / 72 / 108px，缩放比例为 1，不会被线性滤波插值而发糊（旧的 32/40/48/64/96/128px 变体保留，用于 1.1x、1.75x 这类零散比例）。

## 15. 目录结构

```
cmd/yauzin/main.go             入口：app.Main + 窗口事件循环
internal/mail/mail.go          领域模型（会话摘要 / 分页）与 Service 接口
internal/mail/mock/            内存 mock 后端（140ms 模拟延迟，按会话聚合）
internal/mail/gmail/           Gmail 只读后端：OAuth（PKCE 回环）、REST 客户端、请求日志 transport、MIME 解析、Discovery 生成的 API 类型
internal/cmd/gmailgen/         Gmail Discovery 文档 → Go 类型生成器（go generate 入口）
internal/accounts/             accounts.json：OAuth 凭据（provider）与每账号 kind/令牌/useProxy
internal/ui/app.go             状态与构造：App 字段、构造器、异步原语
internal/ui/app_lifecycle.go   窗口生命周期、post 队列、临时目录与 Ctrl+C 清理
internal/ui/app_data.go        账号、文件夹、分页与加载
internal/ui/app_thread.go      会话打开与批量动作
internal/ui/app_overlay.go     帧骨架、指针区域与浮层编排
internal/ui/app_settings.go    设置读写与语言/装饰/性能/日志级别/代理（全局与单账号）、账号删除
internal/ui/errors.go          错误分类 → i18n 键映射
internal/ui/keys.go            键盘路由、列表光标与快捷键
internal/ui/shell.go           顶栏 + 侧栏/内容区两栏布局
internal/ui/panel.go           浮层尺寸夹紧与定位
internal/ui/perfmon.go         性能监视器（CPU / 内存 / goroutine / FPS）
internal/ui/topbar.go          顶栏品牌与两侧控件
internal/ui/searchfield.go     搜索框（含 CSD 紧凑变体）
internal/ui/windowcontrols.go  标题栏的最小化/最大化/关闭按钮
internal/ui/sidebar.go         侧栏文件夹/标签
internal/ui/labels.go          标签层级：按名称建树、排序与折叠扁平化
internal/ui/listtoolbar.go     列表工具栏与分页
internal/ui/messagelist.go     会话列表容器（layout.List 虚拟化）
internal/ui/messagelist_model.go  列表数据与批量选择
internal/ui/messagelist_row.go    会话行布局与 hover 动作
internal/ui/messagelist_text.go   发件人/主题/摘要格式化
internal/ui/reader.go          阅读器容器与工具栏
internal/ui/messagecard.go     可折叠消息卡片
internal/ui/compose_view.go    写信浮层布局
internal/ui/compose_actions.go 发送、附件暂存与丢弃
internal/ui/settings.go        设置浮层（通用/账号/快捷键，含网络代理、日志级别与账号手风琴：展开后是浅灰子卡片——代理开关 + 描边按钮操作，删除在同一行原地确认）
internal/ui/account_add.go     添加账号 / 重新登录浮层（类型选择、授权链接复制与打开）
internal/ui/menu.go            下拉菜单
internal/ui/status.go          轻量状态条
internal/ui/fonts.go           字体集合（系统字体 + Material Symbols）与 Shaper
internal/ui/icons.go           图标码点与字体资源
internal/ui/appicon.go         应用图标：托盘未读徽标与顶栏 logo
internal/ui/palette.go         Gmail 配色与共用尺寸
internal/ui/theme.go           文本、字形与度量辅助
internal/ui/draw.go            圆角卡片、矩形、分隔线等绘制原语
internal/ui/widgets.go         组件共用的失效与光标辅助
internal/ui/widgets_button.go  iconButton / pillButton
internal/ui/widgets_input.go   复选框、开关、点击拦截与输入框
internal/ui/widgets_nav.go     侧栏导航项与菜单行
internal/ui/format.go          时间、地址与主题格式化
internal/ui/filepicker.go      xdg-desktop-portal 文件选择/保存
internal/ui/openurl.go         跨平台打开链接/文件
internal/ui/htmlfile.go        HTML 临时文件与 CSP 注入
internal/ui/htmlsource.go      原始邮件页（元信息 + 转义源码）
internal/prefs/                设置持久化
internal/logging/              进程级 slog level var 与控制台 text handler（stderr）
internal/cursor/               系统光标主题解析（门户 / kreadconfig6 / gsettings）
internal/htmltext/             HTML → 纯文本
internal/i18n/                 中英文翻译与系统 locale
internal/tray/                 系统托盘封装（fyne.io/systray）
internal/singleinstance/       D-Bus 单实例（会话总线名 me.zhsj.yauzin）
scripts/gen_icon_fonts.py      Material Symbols 子集字体生成（fonttools）
scripts/gen_app_icons.py       应用图标 SVG → PNG 生成（rsvg-convert）
```

## 16. 记录约定

- 本文只沉淀**当前仍然成立**的结论：计划完成或被后续方案取代后，把有效结论合并到这里，再由人工定期清理过期计划。
- 尚未实施的事项（含暂缓项与恢复条件）记在 [PLAN.md](PLAN.md)；完成一项就把结论并入本文并删除该条目。
