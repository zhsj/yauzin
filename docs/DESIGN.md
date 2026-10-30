# Yauzin 设计记录

本文档汇总当前代码里**仍然成立**的设计决策、分层契约、踩坑约定，以及面向开发者的环境、构建、字体图标与目录说明。[README](../README.md)（[中文版](../README.zh-CN.md)）只保留产品介绍、功能清单与使用方式，不再承载构建步骤。

## 1. 已否决路线

项目定位与功能清单见 [README](../README.md)（[中文版](../README.zh-CN.md)）。以下路线已明确放弃（阅读旧计划时不要照抄）：

- Fyne 实现（已整体重写为 Gio；托盘也改为项目内自实现，见 §11）。
- 应用内 HTML 富文本渲染（含 `internal/htmlrich` + 自绘排版方案）；HTML 正文只交给系统浏览器。
- HTML 正文的纯文本降级（`internal/htmltext`，以及正则剥标签、`x/net/html` 取文本等替代方案）：没有 `text/plain` 即视为无正文。
- Fyne 内建图标、`page/perPage` 双模式分页。
- tooltip 跨帧重放、设置页胶囊分段控件。
- 日志落文件（`YAUZIN_LOG` 环境变量 + 文件 handler）：日志继续只写 stderr，不引入文件轮转与路径配置。
- 演示数据本地化：`internal/mail/mock` 的标签名与正文摘要保持硬编码，不随界面语言切换；真实账号的标签来自服务端，不受影响。

## 2. 分层

依赖方向单向：`cmd/yauzin` → `internal/uihost` → `internal/ui` → 领域包；`internal/ui` 只依赖 `mail.Service` 接口，不依赖任何具体后端。`internal/uihost` 是唯一 import `gioui.org/app` 的包（进程入口、窗口、事件循环与文件选择器），`internal/ui` 通过 `Driver`/`Window`/`FilePicker` 接口（`driver.go`）使用它，自身不碰 cgo，因此可以脱离图形栈编译与测试。各文件职责见 [§15 目录结构](#15-目录结构)，契约与不变量见下文。

- **后端装配点**：`internal/ui/app.go` 的 `appOptions{accounts, newService, dev, store, proxy, factory, sendSMTP}` 是唯一装配后端的地方（`sendSMTP` 是发信账号的出站 SMTP 发送函数，默认由 `smtp.New` + 代理组装，测试注入替身）。`newApp()` 读取 `settings.json` 与 `accounts.json`，账号列表 = 已存 Gmail 账号；开发模式（`YAUZIN_DEV`）下再在末尾追加演示账号（`demo-gmail`，由 `internal/mail/mock` 服务），它是唯一使用 mock 后端的账号。没有任何账号时窗口以空列表打开（不阻塞启动，设置页仍可添加）。`newAppWith(pref, opts)` 接受已解析的设置与注入的工厂，测试经它构造 `App`，不触碰磁盘。UI 其余部分继续只认 `mail.Service`。

- **环境变量**：`internal/env` 是本应用 `YAUZIN_*` 变量的唯一读取点（叶子包，只依赖标准库），其它包不再直接 `os.Getenv` 这些名字。目前三个：`YAUZIN_CONFIG_DIR`（覆盖配置目录，见 §10）、`YAUZIN_PPROF_PORT`（pprof 端口，未设置或空即关闭，非法值只告警）、`YAUZIN_DEV`（非空即开启开发模式，追加演示账号）。locale、光标主题、XDG 等系统变量仍由 `i18n` / `cursor` / `ui` 各自解析，不属于本包。开发模式只在 `newApp()` 启动时读取一次，经 `appOptions.dev` 传入，测试不受环境变量影响。

## 3. 后端契约（`internal/mail`）

- **会话聚合在服务层**，UI 不做分组：`ThreadSummary` 恒描述整段会话，搜索命中也只决定会话是否出现，不会裁剪会话内的邮件。
- **会话粒度的读取**：正文只通过 `GetThread(ctx, threadID)` 按会话读取，`Service` 不提供单封 `Get`。真实后端因此不必为 UI 不使用的懒加载路径写实现；若将来确实需要单封读取，先在设计里说明用途再重新加回。
- **原文下载是独立的流式读取路径**：`SaveMessage(ctx, messageID, w, progress)` 只在用户点「下载邮件」时调用，把后端存储的原始 RFC 822 源码直接写入 .eml（Gmail 为 `messages.get?format=raw`，响应里的 base64 字段边到边解码）。它不是正文懒加载：解析后的正文仍只来自 `GetThread`，因此不受上一条约束；整封邮件也不进入内存。
- **纯返回值（无 emit）**：`Folders(ctx) ([]Folder, error)` 返回侧栏名称（不含未读数/颜色），`FolderDetails(ctx, folders) ([]Folder, error)` 补未读数与颜色，`ListThreads(ctx, req)` 与 `SearchThreads(ctx, req)` 返回整页会话；四个方法都一次性返回，不再有 emit 回调。渐进体验由 UI 串行编排（见 §4.2）：侧栏先显示标签名、列表保持加载态直到详情就绪后一次性显示整页，最后补计数器。
- **游标分页**：`ListThreads(ctx, ListRequest{FolderID, PageToken, Limit})` 浏览单个文件夹，`SearchThreads(ctx, SearchRequest{Query, PageToken, Limit})` 跨全部文件夹搜索；两者的 `FolderID` / `Query` 都是必填，空值返回 `ErrInvalidArgument`。`ThreadPage{Threads, Total, NextPageToken}`，`NextPageToken` 为空表示最后一页，`Total` 为负表示后端无法估算。UI 只在 `tokens []string` 里压栈游标，不向下传页码。
- **搜索是独立的读路径**：query 原样交给后端（Gmail 支持 `from:` / `subject:` / `has:attachment` 等原生语法），不带 folder 过滤；Gmail 的 `includeSpamTrash` 取默认 false，因此天然排除垃圾邮件与垃圾箱，与 mock 的 `matchFolder("")` 语义一致。**浏览垃圾邮件与垃圾箱时这条不适用**：`includeSpamTrash` 默认 false 会把 SPAM/TRASH 整个排除在结果集外，只带 `labelIds=SPAM` 或 `TRASH` 会得到空页，所以列表路径单独为这两个文件夹显式发送 `includeSpamTrash=true`（见 §读取映射）；搜索路径保持默认 false，语义不变。`Total` 取后端估算，无法估算时为 -1。
- **读取缓存**：gmail 后端对 `Folders` / `FolderDetails` / `ListThreads` / `SearchThreads` 各做一份无过期 LRU（容量 4 / 4 / 64 / 32；键为固定键、标签 ID 集合、`(labelID, pageToken, limit)`、`(query, pageToken, limit)`），命中返回深拷贝，检查发现变化时整体清空。列表与搜索的缓存分开，互不挤占。
- **缓存失效由检查驱动**：`CheckChanges(ctx) (bool, error)` 请求一次 `users.getProfile?fields=historyId` 与上次基线比较；首次调用只记基线、清掉之前缓存并返回 false，值不相等（回退也算）则清空该账号全部读缓存并返回 true。检查在途时**缓存缺失**的读取先等检查结束（尊重 ctx），命中则立即返回；每个 LRU 自带纪元，读取发出请求前取纪元、写回走 `setAt`，期间发生 clear（检查）就丢弃结果。全量加载入口统一「先检查再加载」（`reloadChecked`），因此基线总先于它所描述的数据；轮询与界面刷新共用 UI 层同一条检查通道（见 §4.2），`internal/inboxwatch` 只接收结论、不再自查。
- **错误是分类不是句子**：`ErrNotFound` / `ErrUnsupported` / `ErrInvalidArgument` / `ErrUnauthorized` 由 `internal/ui/errors.go` 用 `errors.Is` 映射到 i18n 键，未知错误才回退 `err.Error()`。`ErrUnauthorized` 只表示「账号需要重新授权」，不让传输层文案冒充原因。
- **能力边界按方法表达**：后端对做不到的方法统一返回 `ErrUnsupported`，UI 保留原入口并由既有错误映射提示「当前账号不支持该操作」。Gmail 已实现 `SetFlag` / `Archive` / `MarkSpam` / `Delete`（见 §3.1「写操作」）、`Send`（见 §3.2）与 `SaveAttachment` / `SaveMessage`（见 §3.1「附件下载」），`mail.Service` 全量可用。写操作上的 `ErrUnauthorized` 还表示「授权范围不足」：旧令牌只带 `gmail.readonly` 时 Gmail 回 403 `insufficientPermissions`，客户端把它归入该分类，界面提示重新授权。
- 实现必须并发安全并尊重 `ctx`（UI 从多个 goroutine 并发调用、不串行化）。mock 的延迟存在的意义就是暴露加载态与竞态。
- 跨层数据必须复制：mock 的 `cloneMessage` / 附件切片深拷贝，避免 UI 通过返回指针改到存储。

### 3.1 Gmail 接入（`internal/mail/gmail`）

- **授权**：用户自带 Google Cloud「桌面应用」OAuth 客户端，在「设置 → 账号 → 添加账号」中填写 Client ID / Secret。流程是授权码 + PKCE（`oauth2.GenerateVerifier` / `S256ChallengeOption`），回调走 `http://127.0.0.1:<随机端口>/oauth2/callback` 的一次性本地服务；scope 为 `gmail.modify` + `userinfo.profile`（不加 `openid`：不需要 ID Token；也不申请 `https://mail.google.com/`，那条更宽的权限只为永久删除所需），并带 `access_type=offline` 与 `prompt=consent` 以取得 refresh token。没有 refresh token 视为授权失败。扩大 scope 会让 Google 重新显示授权页：升级前授权的账号令牌只带 `gmail.readonly`，读邮件照常，写操作会得到 403 `insufficientPermissions`（归为 `ErrUnauthorized`，界面提示「该账号需要重新授权」），「设置 → 账号 → 重新登录」一次即补齐。授权链接不再自动交给浏览器，而是显示在对话框里（只读可选中，另有「复制链接」「在浏览器中打开」按钮），回环回调仍要求浏览器在本机。姓名取自 `https://www.googleapis.com/oauth2/v3/userinfo`，属于尽力而为：旧授权没有 profile scope 时该请求失败只记告警，账号仍以邮箱显示，重新登录一次即补齐。授权码、令牌、client secret、含密码的代理地址一律不写日志。
- **存储**：`accounts.json`（与 `settings.json` 同目录，0600，临时文件 + rename 原子写）保存 `oauth{provider, clientId, clientSecret}` 与每账号 `{id,kind,email,name,useProxy,token{accessToken,refreshToken,tokenType,expiresAt}}`；`provider`/`kind` 都写 `gmail`（`oauth.provider` 标明凭据属于哪个后端，`kind` 复现 `mail.Kind`）。`accounts.Store.Credentials(kind)` 只在 `provider` 匹配时返回客户端，`Upsert` 拒绝空 `kind`。文件是人可读的显式格式，不兼容旧版无 `provider`/`kind` 的文件：旧条目在启动时跳过、旧凭据不被读取，重新登录一次即写出新格式（不做迁移）。`id` 为 `gmail:` + 小写邮箱，重复登录同一邮箱即覆盖（重新授权）。`name` 来自 userinfo，为空时 UI 回退显示邮箱；重新登录取不到姓名时保留已存的值。账号行本身是手风琴开关：点击展开该账号的详情（再次点击或展开另一个账号即收起），演示账号无可配置项，不显示展开箭头。详情是一块浅灰子卡片，与账号行同属一个卡片项（两者之间没有分隔线）：第一行是「通过代理连接」开关，第二行是右对齐的描边小按钮「重新登录」「删除账号」（1dp 边框、8dp 圆角、32dp 高；危险操作为红框红字，悬停浅红底）。重新登录复用已存客户端凭据，若授权得到的邮箱与目标账号不一致则报错且不写盘。删除是两段式：首次点击把操作行原地替换为「确认删除该账号？」与「取消」「确认删除」，点「取消」或收起账号、切换设置页签、面板重建都会取消待确认状态，点「确认删除」才从 `accounts.json` 移除该条目与令牌（`oauth` 客户端凭据保留；demo 账号不在文件里，不提供删除）。删除当前使用的账号会让邮箱进入空状态（文件夹、列表、阅读器清空，顶栏回到品牌名），下一个账号由用户在顶栏账号菜单选择。设置页不再高亮当前账号：切换标记只保留在顶栏菜单里。一个 `*accounts.Store` 同时被 UI 与所有后端持有，后端刷新令牌后立即回写。文件损坏只告警并退化为无账号（开发模式下仍只剩 demo 账号），不阻止启动。
- **代理**：代理地址是通用设置（`settings.json` 的 `proxy`，在「设置 → 通用 → 网络 → 代理」维护），账号条目只记 `useProxy`。开关在两处出现：添加账号/重新登录浮层里决定本次授权与该账号的初始选择；「设置 → 账号」里点击账号行展开后，浅灰子卡片的第一行「通过代理连接」可随时切换。切换只重写该账号的条目并只重建它自己的后端（开启与关闭都重建，先取消在途请求），正在浏览的邮箱会重新加载；写盘失败时开关按存储状态自动回退。未配置代理地址时该行禁用并提示先去通用页填写。保存代理后，使用代理的 Gmail 后端被关闭并重建（先取消在途请求），新 Transport 立即生效。`net/url` 只接受 `http` / `https` / `socks5` 且必须有 host 与端口；`socks5` 由 `http.Transport` 原生支持（`socks5h` 不接受：net/http 没有对应 dialer）。回环回调不经代理。
- **请求日志**：Gmail API 的每次调用在 DEBUG 级别各记一行。单次 REST 为 `gmail request`，字段为 `account`（邮箱，去掉 `gmail:` 前缀）、`method`、`endpoint`（API path，不含 host 与 query）与 `status`；传输失败时改记 `err`、不记状态。batch 单独记一行 `gmail batch`，字段为 `account`、`endpoint`（内嵌调用共享的集合路径，如 `/gmail/v1/users/me/threads`）、`calls`（本批子请求数，满批 25）与 `status`/`err`；`endpoint` 由 `batchFetch` 显式传入而不是从 target 推断，因为 trash 的 target 含 message ID。每批一次 POST，401 重发各记一行。日志直接写在 `client.send` 与 `sendBatch` 的调用点，不包装 `http.RoundTripper`；由 `oauth2` 库发起的令牌刷新与登录授权交换不记请求日志。级别来自「设置 → 通用 → 日志」（`settings.json` 的 `logLevel`），默认 `info`，改后对运行中的后端立即生效。
- **读取映射**：Gmail 系统标签 `INBOX/STARRED/SENT/SPAM/TRASH` 映射为 `inbox/starred/sent/spam/trash` 与对应 `FolderKind`（名称留给 UI 按 kind 翻译），侧栏顺序即 `systemFolders` 的顺序（垃圾邮件在已发送与垃圾箱之间，跟随 Gmail 网页），`DRAFT/IMPORTANT/UNREAD/CATEGORY_*` 不展示；用户标签保留 Gmail label ID 与名称；`labels.list` 返回的是部分资源（只有 id/name/type），因此未读数与颜色来自一次 **batch** 请求：`POST /batch/gmail/v1`（multipart/mixed）里每个子请求是 `labels.get?fields=messagesUnread,color/backgroundColor`，**单批 25 个子请求**、超出则分片，子请求用 `Content-ID` 索引回填，响应乱序也能对应；全部批次完成后一次性返回，补齐对应标签的未读数/颜色。一次文件夹加载因此是 `1 + ceil(L/25)` 个 HTTP 请求；单个子请求失败（标签刚被删除）只告警并按 0 显示，全部失败才报错。列表用 `threads.list`，随后用同样的一次 **batch** 请求补全本页每个会话的 `threads.get?format=metadata`（参与者、未读数、星标、附件标记与最新时间），同样**每批 25**、按 `Content-ID` 回填并按 list 顺序输出，全部批次完成后一次性返回；子请求 404（会话在两次调用之间被删除）只丢弃该行，其它子请求失败或整批失败则该次调用报错，空页不发 batch。一次列表加载因此是 `1 + ceil(N/25)` 个 HTTP 请求。搜索走同一个 `threads.list`，只带 `q`、`maxResults` 与 `fields`（不带 `labelIds`），因此也是 `1 + ceil(N/25)` 个请求。**batch 按账号串行**：`client` 持有一个容量 1、可被 ctx 取消的信号量，从发起到响应体读闭一直持有（含整批 401 重发），因此侧栏的 labels batch 与列表的 threads batch 不会叠加——Gmail 会并发执行 batch 内的子请求并按用户计并发，叠加正是触发 429 的原因。`Total` 取 `resultSizeEstimate`，缺省或用空时为 -1。浏览垃圾邮件与垃圾箱时 `threads.list` 额外带 `includeSpamTrash=true`（`hidesByDefault` 判定）：该参数默认 false 会把 SPAM/TRASH 排除在结果集外，只靠 `labelIds` 会得到空页；搜索与其它文件夹不发送它，因此语义不变。表头计数来自 `labels.get`（不受该参数影响），所以这两个文件夹的计数与列表可能不一致，这正是排查时的第一个线索。`threads.list` 的 `labelIds` 是会话内消息标签的并集，只被部分移入垃圾箱的会话会整条出现在垃圾箱列表里，与网页版按邮件展示略有差别。mock 用独立的 `stored.spam` 表达垃圾邮件（与 `trash` 分开）：垃圾邮件不出现在收件箱、标签与搜索结果里，删除后进垃圾箱并去掉 spam 标记；垃圾邮件文件夹的行只描述它自己的垃圾邮件。`MarkSpam` 两个方向对称：标记后会话离开收件箱、已发送与标签，取消后回到收件箱（`archived` 一并清掉，否则「不是垃圾邮件」可能落进归档）。正文用 `format=full` 递归解析 MIME：首个 `text/plain` 与首个 `text/html` 作正文，带 `filename` 的部件只留元数据（内容按需下载，见下一条）。正文一律按 UTF-8 解释：Gmail 已在服务端完成 transfer 与字符集转换，返回的字节就是 UTF-8（原件 `charset=iso-8859-1`、quoted-printable 的 `Gr=FC=DFe` 也返回 UTF-8 的 `Grüße`），而部件头仍保留原始 `charset`，所以不按头部再转一次，否则 `Grüße` 会被当成 latin-1 解成 `GrÃ¼ÃŸe`。
- **附件下载**：`SaveAttachment(ctx, messageID, attachmentID, w io.Writer, progress)` 把内容直接写进调用方给的 writer——目标位置由 UI 先通过门户对话框确定，后端不碰文件名与暂存目录。附件只有一种来源：`messages.attachments.get`（`GET /users/me/messages/{id}/attachments/{attachmentId}`）。正文 payload 自带内容却没有 `attachmentId` 的部件没有可请求的地址，`mime.go` 仍把它列进附件列表（ID 为空，`HasAttachments` 照常为真），保存时以 `ErrNotFound` 失败；没有合成 ID，也没有 payload 回退。**保存是流式的**：`jsonstream.go` 的 `scanDataField` 把响应 JSON 扫到 `data` 成员即停，只把该字符串的字符流交给 base64 解码器，解码结果按 32 KiB 块直接写进目标 writer——扫描缓冲 4 KiB、拷贝缓冲 32 KiB，内存与附件大小无关，文件大小随下载增长，不再出现「进度到 100% 后文件瞬间出现」。扫描是 JSON 字符串 token 上的最小状态机（内容与长度都等于 `data`、且后面紧跟 `:` 才算键），依赖 base64url 字符集不含引号与反斜杠，因此不处理转义；`paddingReader` 在流末尾按已读长度补 `=`，让标准 `URLEncoding` 解码器同时接受带填充与无填充的两种写法；响应在字段中途截断则报错，不把半个附件当成功。进度由 `client` 包装响应体回调：总长取 `Content-Length`，按 1%（长度未知时每 64 KiB）上报，401 重试从头计数（重试发生在读取之前，目标仍是空的），调用方保持 `Done` 单调；`data` 读完后排空 JSON 尾部（最多 64 KiB）再收尾，进度因此仍能到 100%。写入按固定缓冲分块进行，写错误与短写（`io.ErrShortWrite`）都让保存失败，已经写入的部分保留、任务按 `task.failedPartial` 提示。参数为空（`messageID` 为空或 writer 为 nil）报 `ErrInvalidArgument`；附件 ID 为空、blob 404、`data` 缺失或为空都报 `ErrNotFound`。成功记一行 DEBUG `gmail attachment saved`（`account`、`message`、`attachment`、`bytes`），失败记一行 `gmail attachment save failed`（另有 `stage`：`no blob` 部件没有可下载的 blob、`download` 请求失败、`empty` 响应没有内容、`write` 目标写入失败，附 `err`）；日志不含附件内容。下载是读操作，不清读缓存。
- **写操作（标记 / 归档 / 垃圾邮件 / 删除）**：UI 的已读/未读、星标、归档、垃圾邮件、删除都按 `ThreadSummary.MessageIDs` 传**邮件 ID**。`SetFlag` / `Archive` / `MarkSpam` 折成 `POST /users/me/messages/batchModify`：`FlagRead` 增删 `UNREAD`、`FlagStarred` 增删 `STARRED`、`Archive` 删除 `INBOX`（Gmail 没有归档接口，移出收件箱即归档，邮件仍留在其它标签与搜索里），`MarkSpam(true)` 加 `SPAM` 并删 `INBOX`、`MarkSpam(false)` 加 `INBOX` 并删 `SPAM`（两个方向都是一次标签交换，会话事后只可能落在其中一个文件夹），整页几十封邮件因此是一次请求（协议上限 1000 个 ID，按 `modifyBatchSize` 分片）。`Delete` 是「移入垃圾箱」而非永久删除（UI 的提示就是「已移至垃圾箱」）：Gmail 没有 batch trash，每个 ID 一个 `messages/{id}/trash` 子请求装进同一个 multipart batch，按 `batchLimit` 25 分片，子请求 404 视为「已经不在了」。为此 `batchTarget` 增加 `method` / `body`，`batchBody` 支持 POST + JSON 子请求（`Content-Length` 标出正文边界），`client.postJSON` 把 204 No Content 也当成功（batchModify 的应答）。未知 flag 报 `ErrInvalidArgument`，空 ID 列表直接返回成功；批量只部分成功时仍返回首个错误，界面回滚并提示，已改动的部分由随后的刷新体现。
- **变更检查（historyId）**：`CheckChanges` 是 `users.getProfile?fields=historyId`（1 次请求、1 配额、无消息数据），与上次基线比较后由后端自己清缓存，调用方不需要模式参数。`checkMu` 只在「登记检查状态」和「比较 + 清缓存」两处短持有，HTTP 往返不在锁内；检查在途时缓存缺失的读取在 `checkDone` 上等待（ctx 取消即返回），命中不等待。读取发请求前取 LRU 纪元，返回时 `setAt` 校验：clear 之后写入的只可能是在该次 clear 之后发起的请求。`GetThread` / `SaveMessage` 不是缓存读取，不等检查。清空缓存有两条路径：`CheckChanges` 发现邮箱变化，以及本账号的写操作——`SetFlag` / `Archive` / `MarkSpam` / `Delete` 完成后立即 `clear()`（部分失败也清，因为前面的子请求已经改动了邮箱），因为 UI 的成功回调直接重读列表与侧栏、不会先跑一次检查。没有对外的整体失效入口。变化时以 DEBUG 记一行 `gmail mailbox changed`（`account` / `from` / `to`，只有 historyId，不含邮件内容），首次检查另记 `gmail history baseline`；默认 `info` 级别下不输出。
- **配额成本**：各方法的配额单位、每分钟与每天上限、错误表现，以及本项目每个动作的成本算式，单独记在 [`GMAIL-QUOTA.md`](GMAIL-QUOTA.md)；改动读取/缓存策略前先对照那份表。
- **类型来源**：`discovery_types.go` 由 `internal/cmd/gmailgen` 从 Google 的 Discovery 文档生成（`//go:generate`，文件头记录文档 `revision`），字段名按 initialism 表保持 `ID` / `IDs` / `MIMEType` / `AttachmentID` 等写法；`api.go` 只留 `headers` / `hasFlag` / `date` / `hasAttachment` 这些把文档折算成领域模型的辅助方法。文档本身不入库，每次生成都取官方当前版本，schema 变化靠 `git diff` 审阅。
- **已知取舍**：一页 25 个会话 = 1 次 list + 1 次 metadata batch（`1 + ceil(N/25)`），首屏延迟仍高于 mock；每批 25 是并发预算而非 Gmail 协议上限（协议仍允许 100），串行后 batch 子请求峰值固定为 25，若线上仍偶发 429 再下调 `batchLimit`；标签多时文件夹加载仍拆成多批（每次 `FolderDetails` 一次性返回，不再逐批渐进渲染），缓存未命中时仍是一次 `labels.list` 加 `ceil(L/25)` 个 `labels.get`；缓存本身无过期，邮箱变化靠每分钟一次 `users.getProfile` 检查（托盘驻留期间也在跑），检查只换来 historyId，内容仍要重新拉；变更检查的失败不打扰界面（后台只记 DEBUG），由随后的加载自己报错；Gmail 按子请求计配额，batch 只省往返与连接开销、不省配额，`metadataHeaders` 也仍是每会话一份；`resultSizeEstimate` 是估算值；列表中附件标记依赖 metadata 返回的 part 树，若某条消息未返回 parts 则该行可能漏标记；token 与代理凭据都是明文 0600 存储，没有系统密钥环集成。

### 3.2 发信（`internal/mail/rfc822`、`mail/smtp`、`netproxy`）

- **一份报文，两条传输**：`rfc822.Prepare(draft, from, opts)` 先解析出整封报文的结构与精确字节数（`Message.Size()`），`Message.WriteTo(io.Writer)` 再按顺序写出（CRLF 行尾、`Date` / `Message-ID` / `MIME-Version`、RFC 2047 编码的主题与显示名、`text/plain` 正文 base64、有附件时 `multipart/mixed` + 逐块读取的 base64 附件），Gmail 与 SMTP 共用，避免两套 MIME 行为分叉。`Message` 只保存头部与每个附件的路径/大小，不持有文件内容，因此渲染内存与附件大小无关；`Prepare` 会打开每个附件做前置校验，缺文件在拨号或建上传会话之前失败。`Build` 仍保留为 `Prepare + WriteTo(bytes.Buffer)` 的兼容包装，只用于测试等需要完整字节的场景。显示名、主题与附件名里的 CR/LF 一律替换成空格，防止用户输入注入报文头。
- **Bcc 头是传输差异**：`Draft.Bcc` 始终属于信封（`Draft.Recipients()` = To + Cc + Bcc 去重）。Gmail API 只能看到 raw 报文，所以 `rfc822.Options{BccHeader: true}` 把密送地址写进 `Bcc` 头（Gmail 投递时剔除该头）；直连 SMTP 时密送地址只进 `RCPT TO`，报文头必须不含 `Bcc`，否则其他收件人都能看到密送对象。
- **线程头只在回复与 mailto 里写**：回复把被回复邮件的 Message-ID 写进 `In-Reply-To`，`References` 保留原文祖链（去重）并追加该 Message-ID；mailto 链接自带的 RFC 2368 `in-reply-to` 只给出父 ID，因此 `References` 只含这一个 ID；转发与新邮件都不写，避免把转发硬塞进原会话。`mail.ReplyReferences` 是回复的唯一拼接点，`mail.NormalizeMessageID` 校验尖括号形态并丢弃畸形值（同时挡住头注入），mailto 的 `in-reply-to` 也走同一校验。读取侧没有新增请求：Gmail `GetThread` 已是 `format=full`，`payload.headers` 里直接取 `message-id` / `references`；mock 的种子数据按 `<id@yauzin.invalid>` 补齐，`SaveMessage` 也写出这些头，便于对照检查。
- **Reply-To / Mail-Followup-To 决定回信地址**：读取侧在 `gmail.message()` 里从 `payload.headers` 取 `reply-to` 与 `mail-followup-to`，分别解析成 `Message.ReplyTo`、`Message.MailFollowupTo`；`mail.ReplyRecipients` 是回信收件人的唯一计算点，优先级是 `Reply-To` > `Mail-Followup-To` > 兜底——这两个头任一存在时，回复与回复全部都只发给该头的地址（忽略原 To/Cc，回复全部的 Cc 为空）；都不存在时回复只发给发件人，回复全部再补上原 To/Cc 中的其他人，并按邮箱（大小写不敏感）排除自己的地址、去重，Cc 不重复 To 已有地址。Debian 等列表邮件通常没有 `Reply-To`，靠 `Mail-Followup-To` 把回信指向列表，因此不解析 `List-Post` / `Resent-From` 等其它列表头；头发存在但解析不出一份可用地址时按没有该头处理。发送层不感知这些头，`Draft` 只带算好的 To/Cc。
- **Date 与 Message-ID 由写信浮层决定**：两者在打开写信时确定（切换发件人只重算 Message-ID 的域名），随 `Draft` 交给渲染器，取值顺序是 `Draft > Options > 现场生成`。这样写信浮层的表头详情显示的就是报文里真正写出的值；`Options` 仍只作测试注入点。
- **Gmail 发送**：`messages.send` 走 resumable 上传（`POST <host>/upload/gmail/v1/users/me/messages/send?uploadType=resumable`，`Content-Type: message/rfc822`，`X-Upload-Content-Length` 为 `Message.Size()`），既避开 `{"raw": base64}` 的 33% 体积膨胀，也不需要在内存里持有整封报文。会话 URI 从响应的 `Location` 取得；报文通过 `io.Pipe` 由 `Message.WriteTo` 按 8 MiB（256 KiB 整数倍）切块 `PUT`，`308 Resume Incomplete` 按 `Range` 续传，`401` 刷新令牌后重放当前块，`5xx`/`429` 先用 `Content-Range: bytes */total` 查询已收字节再续传。客户端不设附件大小上限：超过 Gmail 的 35 MiB（36,700,160 字节，编码后）由服务端拒绝，413/400 统一归为 `ErrInvalidArgument`。`From` 一律取账号自身地址，不采信 `Draft.From`：Gmail 只接受属于该账号的身份。发送成功的邮件由 Gmail 自己放进「已发送」。`client` 因此有两个 API 根：`apiPath`（普通 REST）与 `uploadURL`（`/upload` + `apiPath`），两者共用请求槽、401 重试与 `apiError` 分类。
- **SMTP 发信**：`smtp.Sender` 每条发信条目一个实例、每次发送一条连接（`net/smtp`）。`Security` 三选一：`tls`（465 隐式 TLS，本包先包裹连接）、`starttls`（587，服务器不广告 STARTTLS 就报错而不是退回明文）、`none`（25，仅限自有网络的中继）。`Username` 为空跳过 AUTH；否则按服务器广告选 `PLAIN`（优先）或 `LOGIN`——两者都在本包实现，因为 `net/smtp` 的 `PlainAuth` 拒绝在未加密连接上发密码，而「不加密」是用户显式选择。认证被拒（501/504/530/534/535）包成 `smtp.ErrAuth`，由 `errors.go` 翻成「检查用户名和密码」，其余协议错误保留原文（用户要看到具体原因）。连接、TLS 与 AUTH 各阶段都受 `ctx` 的 deadline 与取消约束；DEBUG 只记 host/port/发件地址/收件人数，不记密码与报文。
- **代理**：`internal/netproxy` 是裸 TCP 的代理拨号器——`net/http` 的代理支持在它的 internal bundle 里，给不了别的 dialer。`Parse` 规则（`http`/`https`/`socks5`，必须有 host 与端口，`socks5h` 不接受）由 `gmail.ParseProxy` 与 SMTP 共用；`Dial` 对 socks5 用 `golang.org/x/net/proxy`，对 `http`/`https` 手写 `CONNECT`（含 `Proxy-Authorization: Basic`，https 时先对代理做 TLS）。发信条目与 Gmail 账号一样有 `useProxy`，沿用 `settings.json` 的全局代理地址。
- **不写 Gmail「已发送」**：通过 SMTP 发出的邮件不会出现在 Gmail 的已发送里（`messages.insert` 不接受未配置 `sendAs` 的伪造 From，配置 sendAs 需要额外验证流程）。留档靠 SMTP 条目上的「默认密送到当前 Gmail 账号」开关：选中该发件人时写信浮层把当前邮箱地址预填进密送字段，用户可清空或改填。

## 4. UI 架构（`internal/ui`）

### 4.1 单点状态

`App` 持有全部可变状态；各视图（`topBar`、`sidebarView`、`listToolbar`、`messageListView`、`readerView`、`statusBar`、`perfMonitor`、`composeView`、`settingsView`、`menuOverlay`）只读 `App`，并各自保存瞬态（`layout.List` 滚动偏移、hover 锚点、`widget.Selectable` 等）。视图之间不互相引用。

状态分四类：

1. 会话上下文：`activeAcc`、`activeFolder`、`query`、`folders`。`activeFolder == ""` 就是搜索视图（此时 `query` 非空），侧栏不高亮任何项；非空则是该文件夹/标签的浏览视图。
2. 分页：`page`（1 基）+ `tokens`（`tokens[i]` 打开第 `i+1` 页，`tokens[0]` 恒为空游标）。
3. 阅读：`selectedThread`、`thread`、`msgs`、`expanded`、`loadingThread`；阅读器查找条的查询、命中与当前项也是这一层的瞬态，随会话重置。
4. 窗口/浮层：`decorated`、`maximized`、`windowActions`、`overlay` + `compose`/`settings`/`menu`。

窗口句柄、post 队列、退出标志是唯一跨 goroutine 共享的部分（`winMu`、`postMu`、`quitting`）。

状态条的 4s 消退定时器由 `statusBar` 自己持有（`timer`，UI goroutine 独占）：`show`/`clear` 先 `Stop` 旧定时器再改文案，`notify` 随后重新装一个，因此新通知、点击状态条与 Esc 都不会留下上一条文案继续撑到超时。定时器回调仍经 `post` 回投并校验 `gen`，覆盖「回调已入队、新消息刚到」的窗口。

后台任务列表（`taskPanel`）与状态条一样只由 UI goroutine 写，统一记录附件保存与邮件发送。下载：`saveAttachment` 先 `begin` 一个任务并弹出保存对话框，用户确认位置后才开始下载（超时也从此计时），进度回调经 `post` 更新（`Done` 只增不减，迟到的回调不会让已完成的任务回到进行中）；门户 writer 关闭成功才标记完成；用户取消对话框时 `drop(id)` 直接移除任务（没有下载、没有历史）；下载失败保留错误原因，且因为目标文件已在确认位置时创建，任务用 `task.failedPartial` 追加「目标文件可能不完整」，不删除文件以免误删用户原有文件。发送：`composeView.send` 在发起请求前 `begin` 一条任务（key 为空，允许并发），标题取单行化的主题、空主题用 `list.noSubject`，成功标 `task.sent`、失败保留 `task.failed` 原因；发送期间关掉草稿任务仍在栏里，结果照常回写。每行末尾的关闭按钮在任务运行中即取消（下载取消父 ctx 并在对话框返回后直接退出，发送取消请求并把草稿复位为可再次发送），已结束时仅从历史移除；取消是用户主动行为，因此不再弹失败提示（`saveAttachment` 与 `send` 的回调先查任务是否还在）。`layout.List` 的回调按索引访问任务，删除只标记 `removed`，切片在下一次 `LayoutSection` 开头 `compact()`，迟到的进度/结果回调对已移除任务成为 no-op。分栏固定在侧栏底部（`sidebarView.Layout` 用垂直 `Flex`：导航区 `Flexed(1)`、任务区 `Rigid`），没有任务时整个分栏不占位，最多显示 3 行、更多在分栏内滚动，文件夹列表在剩余高度内继续滚动。分栏本身是一张白色圆角卡片（`colSurface`、1dp `colBorder`、8dp 圆角），卡片上方以间距代替原来的通栏分隔线；行与行之间一条 1dp 细线，左缩进到文字左缘、最后一行不画线。任务只存在内存里，完成后不自动消失、不写配置文件，进程退出才清空；同一附件仍在下载时再次点击直接返回（不会重复下载），重新保存已完成过的附件则新建一条任务。历史上限 50 条，溢出先淘汰最旧的已完成项，运行中的任务不会被淘汰。侧栏折叠时任务列表随侧栏隐藏。

### 4.2 并发模型

- 所有 `mail.Service` 调用在 goroutine + `context.WithTimeout` 中执行；结果只通过 `App.post(fn)` 进入**单一队列**，由 UI goroutine 在每帧开头 `drain()`。goroutine 内禁止改 UI 状态。
- 所有 Service 调用走 `App.async`：它持有超时、`post` 回投与错误提示。需要被替代的请求（列表/会话）把 `cancel` 存在 `App` 字段里，下一次同类请求先 cancel 旧请求，窗口销毁时 `cancelRequests` 全部停止。文件夹请求不在此列：新加载不取消在途的那一次，迟到的结果由 `folderGen` 丢弃——抢先取消会让下一次刷新杀掉「检查发现变化」刚发起的侧栏加载，导致 changed 结论落不了地。
- 三阶段加载由 `onSuccess` 链式编排：`loadFolderNames` 成功后在其回调里发起 `loadThreadsKeepingScroll`，列表就绪后再发起 `loadFolderDetails`。每次调用各走一次 `async`，结果经 `post` 回投并套用同一代际/账号校验，迟到结果直接丢弃。列表在 detail 到达前保持加载态，页数据到达后一次性 `setThreads`（切页/切换，回顶）或 `mergeThreads`（同页刷新，保留滚动与仍存在的勾选）；成功回调只收尾页码/工具栏，不再重设行数据。
- 过期响应靠**代际号**丢弃：`listGen`（列表/分页）、`folderGen`（文件夹未读数）、`msgGen`（阅读器）；回调里同时校验 `activeAcc`、页码或会话 ID。`context.Canceled` 是预期结果，不弹错误提示。
- 切账号 / 切文件夹 / 搜索时先清空视图（`App.clearList` 清 `setThreads(nil)` 并抹掉工具栏范围文案，`resetSelection` 关阅读器）再发起加载，避免旧行、旧标签色块与旧页码闪现。
- 列表请求按查询分流：`query` 非空走 `SearchThreads`，否则 `ListThreads`。两者共用同一个 `listGen` / `listCancel` / `tokens`，因此搜索、翻页、切文件夹互相取代；worker 启动前快照 `folder` 与 `query`，worker 内不读 `App` 字段。
- **先检查再加载**：所有全量加载入口（首屏、窗口重建、切账号、工具栏刷新、代理/凭据变更后重建后端、新增或重登账号）都经 `reloadChecked`（自动）或 `reloadCheckedNow`（用户发起）+ 加载回调——`CheckChanges` 成功与否都继续加载，失败只记 DEBUG，真实的加载错误由加载自己报，界面因此离线也可用。检查阶段就开始转圈，所以手动刷新立刻有反馈。
- **检查单飞与冷却**：检查在 UI 层（`app_check.go`）是**唯一通道**，且单飞 + 冷却。所有检查（首屏、窗口重建、刷新、切账号、代理重建、分钟轮询）都经 `requestCheck`：同一后端同一时刻只发一次 `users.getProfile`，期间的重复意图按槽位合并——`slotReload`（重载当前视图）与 `slotPoll`（读未读、更新托盘）各留最新一个，两者共存，合并后依次执行，不会出现「后到的刷新把托盘更新吞掉」。自动入口在 `checkCooldown`（30s）内直接复用上一次结论，只有 `reloadCheckedNow` 强制检查。后端被替换（重登、代理重建）时旧基线描述的是已断开的连接，必须重新检查。
- **进程级轮询只维护托盘与通知**：`Run` 启动一次 1 分钟 ticker（`app_poll.go`），退出前停止，不随窗口开关起停。每次 tick 经 `checkChangesForPoll` 走同一条检查通道（冷却内会复用结论，不会为了轮询再发一次 profile），拿到结论后才交给 `internal/inboxwatch`：变化或尚无基线时读取 `label:inbox is:unread`（`SearchThreads`，上限 3 条，threads.list + metadata batch）。`internal/inboxwatch.Poll` 因此只接受 `changed` 参数，不再自己调用 `CheckChanges`——包内自查会让轮询绕过单飞/冷却，正是检查风暴的来源。轮询**不刷新侧栏、不刷新列表/阅读器**，也不取消用户请求；结果只更新托盘两态与未读快照。未读会话新建、未读数增加或同一会话最新 message 变化时，`internal/notify` 经 D-Bus 发桌面通知（最多 3 条单行截断主题 + 未读会话数）；首次读取只建基线，不因启动时已有未读而弹窗。窗口内容只在首屏、`attach`、手动刷新、切账号/账号变更与写操作后更新。
- **通知契约与两条通道**：`internal/notify` 对外只承诺两条通道的公共最小集——标题、正文、单个点击动作；同一应用同时最多一条通知，新通知替换旧的；点击后通知消失并打开收件箱；应用名、图标、历史与「按应用设置」一律由 `me.zhsj.yauzin.desktop` 决定（直连用 `desktop-entry = me.zhsj.yauzin` hint，与 Gio application ID、单实例总线名同一字符串；门户按 app id 解析同一个文件），通知名因此不再随界面语言变化。两条通道实现同一个 channel 契约（`show`/`activated`/`closed`/`dismiss`），一方做不到的字段留在各自实现里：直连显式传 `expire_timeout = 30s`（Plasma 默认约 5 秒，装不下 3 条主题 + 未读数；服务端仍可按应用设置覆盖），门户没有这个字段、停留时长由桌面决定——这是唯一保留的体验差异。直连的 `replaces_id` 只在上一条通知**还在屏幕上**时才使用，服务端发来 `NotificationClosed` 清掉该 ID 后下一条就是新通知——Plasma 6.4 起替换一条已超时收进托盘的通知只更新托盘条目、不再弹气泡，且替换复用同一条历史条目，只有新建才会重新弹出并在历史里另起一条；门户没有关闭信号，固定 ID（`unread`）就地更新，点击后 `RemoveNotification` 清掉条目。直连不再传应用名（传空串），名字只来自 `.desktop`：Flatpak 包已安装该文件，原生运行未安装时 Plasma 与门户都解析不到应用身份，弹窗照常显示，但通知不进历史、也不出现在「系统设置 → 通知 → 应用程序」。
- **轮询与通知分层**：`internal/inboxwatch` 是纯 Go 的未读快照状态机（未读快照、主题截断、通知文案），变化结论由调用方给出；`internal/notify` 用同一个 channel 契约实现两条 Linux D-Bus 通道（直连 `org.freedesktop.Notifications`，以及门户 `org.freedesktop.portal.Notification`），通道在**第一次要发通知时**才选：用 `org.freedesktop.DBus.GetNameOwner` 探一次会话总线上有没有通知服务，有就走直连、没有就整个进程走门户（探测与选择各一次，走门户时记一条 INFO；不在 `start` 时探测，否则登录早期 shell 尚未占用名字会被误判），之后不再切换，单条发送失败只记 DEBUG；点击信号的 match rule 也只为选中的那条通道加（`start` 只连总线并起监听，不碰任何通知接口名；`Close` 只为已选通道退订）；`internal/ui` 保留 ticker、检查通道、`async` 超时/回投、托盘落地与点击后打开收件箱，三者都不反向依赖 UI。
- **托盘期间的回投**：`post` 在没有窗口时也向容量 1 的 `wake` 投递，生命周期循环的 `waitForWindow` 在 `openCh` / `quitCh` 之外 select `wake` 并 `drain()`，于是 App 字段仍只有一个 goroutine 写。`wake` **只执行回调、不建窗**：建窗的唯一入口是 `requestOpen` → `openCh`（托盘打开、通知点击、单实例激活都走它），否则一次轮询或状态条过期的回投就会把用户刚关掉的窗口重新弹出来。应用内状态条在无窗口（只有托盘能让进程无窗口存活）时早退，避免重开窗口看到几分钟前的提示。
- 列表只渲染可见行（`layout.List`），行内控件状态按会话 ID 缓存在视图结构里。
- **已知限制**：`loading` 是单一布尔，侧栏阶段失败的清除可能让仍在跑的列表请求提前停止转圈（影响仅为观感，由「新请求取代旧请求」兜底）；托盘期间打开着的阅读器不随后台刷新更新正文（`openThread` 对同一会话直接早退）；轮询不再刷新窗口内容，新邮件需手动刷新、重开窗口或点击通知才出现；未读总数来自 Gmail 的 `resultSizeEstimate`（会话数估算），且只跟踪最新 3 条未读会话，同一 tick 内「读掉一封 + 新到一封」可能互相抵消；桌面通知仅 Linux；Flatpak 沙箱内不申请 `org.freedesktop.Notifications`（xdg-dbus-proxy 会把被过滤的名字报成 `ServiceUnknown`），通知一律走门户，直连只有原生运行且桌面确实有通知服务时才用得上，两条通道都不可用或其它平台才静默降级；门户没有 `expire_timeout` 和关闭信号，弹窗停留时长由桌面决定，应用也无法覆盖服务端关掉的历史记录开关；应用身份全依赖 `me.zhsj.yauzin.desktop`（Flatpak 包装到 `/app/share/applications`，原生运行需自行装到 `~/.local/share/applications/`），缺失时不进历史、名字退化成该 ID。

### 4.3 浮层

- `App.overlay` 是**单一槽位**：`menuOverlay` / `settingsView` / `composeView` 互相替换。
- 吞点击层必须是 `Stack` 的第一个子对象，否则挡住卡片自身的交互。菜单用 pass-through blocker（点击外部只关菜单），设置保持模态阻塞；写信浮窗**不设全窗 blocker**，由卡片内 `Clickable` 自己消费空白点击。
- Esc 优先级：状态条 / 菜单 → 写信 / 设置 → 阅读器返回列表。Esc 不清空查询、不在搜索时回退文件夹；搜索框胶囊的 ✕ 只清空输入框文字，列表保持当前搜索结果（`query` 与 `activeFolder` 都不变）。
- 面板内的下拉**必须页内展开**，不能复用 `App.showMenu`：`showOverlay` 先 `closeOverlay()` 把 `a.settings` 置 nil，菜单关闭时 `hideOverlay()` 又直接清空 `a.overlay`，设置面板不会回来。设置页的语言选择即按此实现（`settingsView.langOpen`，候选行在同一张卡片内展开）；写信浮层的「发件人」同理（`composeView.fromOpen`），因为菜单会顺带把草稿丢掉。
- 写信浮层的「发件人」行只在存在 SMTP 发信账号时出现（否则整行不占位）：候选在卡片内就地展开，当前身份带勾，选中即收起。身份决定传输——邮箱身份走它自己的后端（`services[mailboxID]`，写信开始时固定，中途切账号不会改投），发信账号走 `App.sendSMTP`（可注入，测试不碰网络）。「密送」行始终显示；自动预填的值记在 `composeView.bccAuto` 里，切走发件人时只清除仍是该自动值的文本，用户改过的内容不动。
- 主题下方的「邮件头」折叠行是本层唯一的表头可见入口：展开后只读列出 `Date` / `Message-ID` / `In-Reply-To` / `References`，空值不显示（新邮件与转发因此只有前两行；mailto 打开的信件若带 `in-reply-to`，这里同样显示对应两行）。回复打开时由 `mail.ReplyReferences` 预置线程头，被回复邮件没有 Message-ID 就整条链留空，不伪造 ID。长引用链在界面上只显示最后 8 条（前置 `… `），`Draft.References` 仍是完整链。标签列宽 `headersLabelWidth`（96dp）比通用 `composeLabelWidth` 宽，头部名称不会截断；只用现有 chevron 字形，避免为一个折叠箭头重建图标字体子集。
- 回复全部（`composeReplyAll`）与普通回复共用主题前缀、引用块、选区快照与线程头，区别只在 To/Cc：`newComposeView` 调 `mail.ReplyRecipients` 预填，编辑器文本由 `mail.FormatAddressList` 生成。入口只有每封邮件卡 ⋮ 菜单的第一项与阅读器的 A 快捷键，没有单独的按钮；菜单图标复用 `iconReply`，避免为一个入口重建图标字体子集。
- 下拉菜单的水平位置由调用点声明的 `menuAlign` 决定：默认 `menuAlignStart` 与锚点左边缘对齐（从左往右展开），靠窗口右缘的锚点——阅读器消息卡的 ⋮ 与顶栏账号菜单——显式用 `menuAlignEnd` 与右边缘对齐（从右往左展开）。两种对齐最后都 clamp 进窗口，下方放不下时仍向上展开。
- 写信浮窗的引用块由 `format.go` 的 `replyQuote` / `forwardQuote` 生成，文案固定英文、不走 i18n（它们属于邮件内容而非界面文案，中文界面下同样是英文）：回复是 Gmail 的 `On <时间> <发件人> wrote:` 抬头（本地时区的 `Mon, Jan 2, 2006 at 3:04 PM`）加逐行 `> ` 前缀的正文，转发是 `---------- Forwarded message ----------` 加 `From:` / `Date:` / `Subject:` 与完整正文。回复前阅读器里该封邮件正文若有选区，只引用选中片段（`messageCard.selectedText` → `readerView.replySelection` → `openCompose` 快照一次），选区为空或只有空白时回退完整正文；转发不使用选区。回复把引用块放在正文最前、其后留一个空行，打开写信浮窗时光标与键盘焦点就落在引用末尾（`SetText` 会把光标置为开头，因此回复分支在写入后显式 `SetCaret` 到末尾，按 rune 计数）；转发仍是顶部两行空行之后书写，光标在开头、首帧焦点在「收件人」。`replySubject` / `forwardSubject` 的 `Re: ` / `Fwd: ` 同理写死英文。

### 4.5 键盘

- 所有按键在 `internal/ui/keys.go` 的单一入口路由：先镜像搜索框焦点，再分发全局层（Ctrl+W 关窗口、Ctrl+Q 退出进程），然后按「状态条 → 浮层 → 阅读器」的 Esc 顺序处理，最后才分发其余快捷键。
- 键表 `shortcutTable` 同时驱动分发与「设置 → 快捷键」页：每条记录带分组、位、修饰键 (`mods`)、`global` 标志、Gio 键名与键帽文本，`readShortcuts(gtx, global)` 只读其中一半，设置页遍历整张表，新增按键只需改一处。列表与阅读器都生效的 E / Delete / S / U 归入「全局」组，帮助页因此不出现重复行；方向键与 PgUp/PgDn/Home/End 归入「滚动」组；Esc 也在表内，按位在浮层/搜索门禁之前处理。
- `global` 行（Ctrl+W / Ctrl+Q / Ctrl+F）要求修饰键，因此在两道门禁之前读取，写信浮窗、设置页、搜索框聚焦时同样可用；Ctrl+W 置位 `windowActions` 的 `ActionClose`（与标题栏关闭按钮同一条路径），Ctrl+Q 调 `requestQuit()`，同帧同时按下以退出优先。Ctrl+F 是「阅读器内查找」，只在 `overlay == nil && selectedThread != ""` 时打开查找条，否则整帧忽略；查找编辑器聚焦时只让出 Esc（Enter 由编辑器的 `Submit` 触发「下一个」），因此阅读器的其他快捷键不会吞掉正在输入的字符。Esc 顺序为状态条 → 浮层 → 查找条 → 会话。
- 搜索框焦点用 `gtx.Focused(&a.top.search)` 每帧采样，**不能**用 `gtx.Event(key.FocusFilter{...})` 去读：`FocusFilter` 同时匹配目标的 `EditEvent`/`SnippetEvent`/`SelectionEvent`/`CompositionEvent`，而 `Router.Event` 命中即从该帧删除事件，读焦点会把编辑器的文本输入一起吃掉（表现为能聚焦、光标会闪、但打不出字）。
- 读快捷键是破坏性的，因此裸键只在没有编辑器持有键盘时才读：搜索框聚焦时只读 Esc（其余字符、退格、回车全部留给编辑器，回车才能触发 `Editor.Submit`），浮层打开时同样只处理 Esc。未读取的事件在帧末由 `Router.Frame` 直接丢弃，不会残留到编辑器失焦后才触发。全局层是例外：它只要修饰键组合，而 `widget.Editor` 只绑定 Ctrl+Z/C/V/X/A、Ctrl+Backspace/Delete、Home/End 与方向键，Ctrl+W / Ctrl+Q 不会被编辑器消费。
- 字母键名是 Gio 的大写形式（`J`/`K`/`C`…），普通字母过滤不带修饰键，所以 `Ctrl+C` 不会被当成「写信」；反过来 `key.Filter.Required` 的匹配要求事件修饰键是该集合的子集，`Ctrl+Shift+W` 也不会触发「关闭窗口」。
- 列表有一个键盘光标（`cursorThread`，页/文件夹切换时清空），行高亮复用选中色；移出视口时通过设置 `layout.List.Position.First` 拉回可见区域。动作快捷键复用与鼠标相同的 `App` 方法，不另开一条数据通路。
- 「滚动」组在两个视图中语义随上下文：列表里 ↑/↓ 与 J/K 逐行移动光标，PgUp/PgDn 一步移动上一帧可见行数（`layout.List.Position.Count`），Home/End 跳到首/末行；阅读器里 ↑/↓ 每次滚动 `readerScrollStep`（约三行正文），PgUp/PgDn 滚动「上一帧可视高度 − `readerPageOverlap`」，Home/End 回到正文首/末条。J/K 在阅读器里切换相邻会话（`openAdjacentThread` 同步列表光标，到页首/页尾即停，不循环），因此键表把方向键从原来的 `J`/`↓` 共用行里拆了出来。阅读器的 R / A / F 分别对最新一封邮件打开回复、回复全部与转发。
- 阅读器滚动直接改 `layout.List.Position.Offset`：`layout.List` 只处理指针滚轮，`ScrollBy` 以条目为单位、对高度差异极大的消息卡会跳，所以按像素偏移滚动。键盘在布局之前读取，翻页高度取上一帧在 `layout.Flexed` 子树记录的 `readerView.viewH`，首帧为 0 时回退窗口高度；跨卡与两端由 `layout.List` 自行纠正（负偏移回补上一条，末尾夹到底部），`openThread` 的 `reader.reset()` 负责切会话时回到顶部。
- 焦点归属由 App 自己声明：打开会话、切文件夹、关闭浮层都会请求把焦点交还窗口根，避免搜索框在别处继续吃字符；`/` 主动把焦点交给搜索框。写信浮窗没有发送快捷键（Ctrl+Enter 不发送），打开后按模式分派首帧焦点：回复与回复全部给正文，新邮件与转发给「收件人」。因此 `showOverlay` 不替写信浮窗排队 `blurRequested`：那条失焦要等下一帧才执行，正好落在浮窗刚拿到焦点之后，会把焦点抢回窗口根（表现为光标闪一帧、随后打不出字）。设置、菜单、账号对话框这类不自行聚焦的浮层仍然排队失焦。

### 4.4 窗口生命周期

- `Run(args, drv)` 掌管生命周期循环：`App` 实例**跨窗口复用**，`attach`/`detach` 维护 `win`，托盘通过容量 1 的 `openCh`/`quitCh` 唤醒。`drv` 是窗口层的唯一入口（`newApp` 不知道它），循环每轮 `applyCursorTheme()` → `drv.NewWindow()` → `attach` → `eventLoop` → `detach`；光标主题必须在建窗前解析，Gio 在创建窗口时读取 `XCURSOR_THEME`。
- **窗口层是端口加实现**：`internal/ui/driver.go` 声明 `Driver`（建窗）、`Window`（`Events`/`Invalidate`/`Perform`/`SetTitle`/`Picker`）、`WindowConfig`、`WindowActions` 与 `FilePicker`；`internal/uihost` 用 `app.Window` 与 `*explorer.Explorer` 实现它们，并由 `uihost.Main` 充当进程入口（设 `app.ID`、起 UI goroutine、把主线程交给 `app.Main()`）。依赖方向因此是 `cmd/yauzin → uihost → ui`，ui 侧一行 cgo 也没有。
- 关闭窗口默认销毁窗口、进程留在托盘；无通知区时降级为「关闭即退出」。
- `windowActions` 收集标题栏请求，帧回调把它连同 `windowSteps` 的复位结论一起以 `WindowActions` 返回给窗口层，**帧提交给 Gio 之后**才 `Perform`，避免布局中途销毁窗口；Ctrl+W 与标题栏关闭按钮共用这条路径，Ctrl+Q 直接走 `requestQuit()`（经 `Window.Perform`）。
- `startInitialLoad()` 由 `startOnce` 门禁只跑一次：一次检查同时驱动全量加载与收件箱未读基线（`pollInboxUnreadKnown`）；后续窗口重建由 `attach` 的 `reloadChecked` 刷新，冷却窗口内复用上一次检查结论。
- Gio 的 `app.Main()` 在 Linux 不返回，退出必须显式 `cleanupTemp()` + `os.Exit(0)`；托盘没有库自己的事件循环，退出路径只调 `t.Close()` 做增量卸载。
- 生命周期循环之外的结束方式也要收拾现场：`watchInterrupt()` 在 `newApp()` 之后就开始监听 `SIGINT`（`Ctrl+C`，`signal.Notify` 就地注册），收到后先 `cleanupTemp()` 再 `os.Exit(0)`，否则托盘驻留期间被 `Ctrl+C` 停止的运行都会留下临时目录。`SIGTERM`/`SIGHUP` 故意不接管，保留默认行为：桌面会话结束、`kill`、终端挂断都按系统语义立即结束进程，不等待清理。信号路径与 UI goroutine 会并发访问临时目录，`tmpMu` 保护 `tmpDir`，`tmpClosed` 保证清理后不会再建一个新目录。

### 4.6 阅读器正文查找

- **范围与顺序**：当前会话每封邮件的 `text/plain` 正文（`plainBody`，即卡片实际渲染的那串文本，去掉首尾空白），按邮件顺序、正文内偏移顺序；不区分大小写；空查询不算查找。偏移是 **rune 偏移**，因为 `Selectable` 的选区与 `Regions` 都按 rune 计。大小写折叠必须逐 rune 做（`unicode.ToLower`）：先把整串 `strings.ToLower` 再按字节索引，遇到小写化改变字节长度的字符会让后面所有偏移错位。
- **上限**：一次查询最多保留 1000 个命中（`findHitLimit`），超出的部分不再扫描，计数器显示 `n/1000+`；否则「一个字母 + 一兆正文」会让每帧的高亮遍历上万条区间。
- **高亮几何来自 `Selectable.Regions`**：这是 Gio 唯一暴露命中位置的地方，返回相对正文控件原点的矩形。底色必须画在文字之下，而 `Regions` 只有在该文本 `Layout` 之后才有效，所以 `MessageCard` 用**上一帧**的矩形在画文字之前落笔，画完文字后再刷新缓存；缓存按 `(findBar.gen, 正文宽度, 正文文本)` 三者失效，最多闪一帧。缓存里每个矩形还记录它属于哪条命中（`hitOf`），**当前命中每帧按 `findBar.cur` 现查，不写进缓存键**：上一个 / 下一个只换归属，几何不必重测，强调色当帧就换，也不会因为缓存整体失效让整页高亮闪一下。**不要用 `SetCaret` 做高亮**：那会破坏「一个阅读器只有一处选区」，还会让回复把命中的片段当成引用选区。
- **跳转是两段式**：`layout.List` 只能从 `Position.First` 起布局，拿不到未布局卡片的像素位置，因此第一段 `First = 邮件下标`（必要时 `Offset = 0`）把卡片带到视口顶部；`reader.Layout` 在列表布局之后调 `findBar.afterLayout`，用 `bodyY = 10dp + card.headH`（卡片上内边距加头部与分隔线的实测高度）和命中矩形（由 `findMarks.current(cur)` 现查）算出屏幕位置，把 `Position.Offset` 校正到「视口上部三分之一」，带 2px 死区、最多 3 次、卡片比视口矮时把 Offset 夹回 0。卡片已经在视口顶部时保留原 Offset，避免长邮件先闪回顶部。命中所在邮件会被展开（折叠的卡片没有正文，也就没有几何）。
- **状态**：`readerView.reset()`（打开 / 关闭会话）与 `findBar.hide()` 都清空查询、命中与高亮；关闭查找条把键盘交还窗口根（`blurRequested`）。

## 5. 布局与视觉规格

- 顶栏高度：平台装饰 64dp，CSD 单行 48dp，一律经 `App.headerHeight()`；侧栏 256dp；列表行高 40dp；内容卡片圆角 16dp；窗口默认 1200×840、最小 560×420。
- 左下角状态条仍单独浮在窗口底部（距左 24dp、距底 18dp），4 秒内可能覆盖侧栏底部的任务列表，属于可接受的临时遮挡。
- 任务列表分栏在侧栏最下方：与导航之间不画分隔线，改用 10dp 间距；标题 13sp Medium 左缩进 14dp、留在卡片外，任务行装在 8dp 圆角、1dp `colBorder` 描边的 `colSurface` 白色卡片里，卡片上下各 4dp 内边距；任务行高 48dp、左右内边距 10dp / 6dp，最多显示 3 行、更多行在卡片内滚动；行与行之间一条 1dp `colBorder` 细线，起点缩进到文字左缘（10 + 20 + 10 = 40dp）、右端留 6dp，最后一行不画线；每行图标 16dp、文件名或主题 13sp 单行省略、状态行 12sp，末尾 20dp 关闭按钮（运行中提示「取消任务」，结束后「删除任务」）；下载行的状态行是进度条加百分比：进度条高 4dp、2dp 圆角（轨道 `colSurface2`、填充 `colPrimary`），已知总长按比例填充并在右侧固定宽度单元格里显示百分比，未知总长用往返移动的段并逐帧请求重绘；发送行没有总长，状态行是「正在发送…」「已发送」或失败原因；完成用 `iconCheckPrimary`，失败用 `colDanger` 的警告三角，运行中的下载用文件图标、发送用发送箭头。
- 两栏：侧栏 + 单一内容卡片；列表与阅读器在同一个 `Stack` 里按 `selectedThread` 切换，不存在第三栏。
- 色板令牌集中在 `palette.go`（页面底 `#F6F8FC`、选中 `#EAF1FB`、主色 `#D3E3FD`、行 hover `#F2F6FC` 等），视图里不写裸色值。
- 列表行：勾选 | 星标 | 发件人 | 标签–主题–摘要 | 附件 | 日期。发件人固定 168dp，尾部附件/hover 操作叠在固定 112dp 底层上（四个 28dp 悬停按钮比附件+日期列宽，取两者较大值；宽度恒定，正文列不漂移）；会话数量紧跟发件人；悬停换操作不改变行布局。主题之前的标签是 Gmail 风格的实心色块：圆角 4dp、左右内边距 6dp、上下内边距 2dp、字号 11sp、块间距 4dp，底色取 `labels` 的颜色、文字按 WCAG 对比度在深色与白色之间取更清楚的一个（无颜色标签用 `colChipFallback` 浅灰底），标签名保留完整层级路径；只显示用户标签，系统标签（`INBOX`/`UNREAD`/`CATEGORY_*`）与当前浏览的标签本身不显示。标签段最多占主题-摘要区宽度的一半，低于 48dp 时整段隐藏，剩余宽度仍由主题与摘要均分。
- 分页与全选在列表工具栏：页码由 `tokens` 推出，工具栏显示范围与总数；全选只作用于当前页，切页 / 切文件夹 / 搜索即清空。
- 标签区按 Gmail 的层级展示：标签名里的 `/` 是层级分隔（`工作/项目A` 是 `工作` 下的标签），子标签每层缩进 18dp（最多 3 层）且只显示末段名称；有子标签的父标签把颜色圆点换成展开符号（折叠 ›、展开 ▾，取标签自身颜色），符号占圆点原来的 20dp 单元格、悬停时显示同色圆底并切换手型光标，点击只折叠子树、不选中标签；叶子标签仍显示颜色圆点；折叠状态只存内存（默认全展开），选中标签的严格祖先强制展开——此时祖先行的符号保留 ▾ 外观，但不显示悬停圆底、不切换手型、点击无效（折叠会把选中项藏起来）；选中父标签本身时仍可折叠其子标签；缺少父标签的数据按根级显示完整名称。
- 滚动位置：切页 / 切文件夹 / 搜索走 `setThreads`（回到顶部），归档、删除、加星后的同页刷新走 `refreshThreads`（保留偏移）。
- 垃圾邮件按钮在阅读器工具栏、行悬停组与批量工具栏各出现一次，方向由当前文件夹决定：在垃圾邮件文件夹里是「这不是垃圾邮件」（图标改用收件箱）并移回收件箱，其它文件夹（含搜索）是「标记为垃圾邮件」；垃圾箱不显示该按钮。判定只看 `activeFolder` 的 `FolderKind`（`viewingSpam` / `showsSpamAction`），不引入逐会话状态——垃圾邮件被搜索与标签排除，一页会话的垃圾状态因此与所在文件夹一致。
- 后端没有的能力不做假入口、不放禁用占位。
- **省略号截断不能交给 `MinSize`**：`TruncateEllipsis` 的最小宽度会退化成「…」，再被 Center/Border 压缩。必须给固定宽度槽位。
- 顶栏中任何随语言变宽的元素都必须放进固定宽度单元格：品牌 wordmark 用 `brandNameEms`（em 倍数）定宽、单元格内左对齐（居中会把中文「邮笺」两侧的余量变成印章与文字之间约 20dp 的空隙），窗口窄于 820dp 隐藏；搜索胶囊是固定 dp 宽度且编辑器占满单元格（`Constraints.Min.X = Max.X`，否则文字从中间开始）。
- 阅读器查找条（`findBarHeight` 40dp）紧贴工具栏下方：左侧搜索图标、占满剩余宽度的编辑器、`n/m` 计数（`textMeta` + `colMuted`，无命中为 `colFaint`）、上一个 / 下一个 / 关闭三个 28dp 图标按钮（无命中时两个跳转按钮禁用）。命中底色用 `colFindMark`（浅黄，覆盖全部命中）与 `colFindCurrent`（琥珀，当前命中），都是半透明令牌，文字画在底色之上以保持对比度。

## 6. Gio 实践约定与踩坑

1. **点击后立即请求重绘**：`Clickable.Clicked` 在布局 pass 中消费，状态翻转可能发生在整窗绘制之后。任何自绘控件命中后统一走 `invalidateAfterTap(gtx)`（`gtx.Execute(op.InvalidateCmd{})`），禁止各控件内联。
2. **首帧的 Invalidate 会被吞**：Gio 在处理帧期间丢弃 `w.Invalidate()`。首次加载必须推迟到事件循环（`startInitialLoad`），并且 `post()` 用一个共享槽位（`retry`，与队列同受 `postMu` 保护）在 `postRetryDelay=100ms` 后补一次重绘：一次回调爆发只装一个定时器，回调触发时清空槽位并重新检查队列，队列非空才再 invalidate（重试幂等，定时器数量与调用次数无关）。
3. **`layout.List` 必须挂在视图结构体上**：每帧新建会丢滚动偏移，并且只有可见行会被布局。
4. **clip 是交集，画不出「环」**：描边要「整体先涂边框色 → 内缩涂填充色」，见 `strokeRect`。
5. **父子同一帧都收点击**：父行 `Clickable` 与子控件（复选框、图标按钮）会同时命中。子控件命中后打 `hit` 标记，父级判定**后移到布局之后**再执行。
6. **禁止跨帧重放录制宏**：`op.Record` 的宏在帧末以当前变换重放，跨帧重放会错位并触发 GPU `collectOps index out of range` panic。tooltip 队列 `th.tips` 每帧清空，只存放本帧录制的标签，帧末在无 clip 的窗口坐标系重放。
7. **tooltip 锚点要冻结**：窗口指针坐标与按钮局部坐标分别取整会 ±1px 抖动；进入 hover 时算一次 `anchor` 并冻结，离开时重置。只给语义不直观的图标提示（归档 / 删除 / 标记已读 / 标记未读），其余按钮无提示。
8. **光标必须 clip 到控件矩形**：`cursor.Apply` 作用于当前 clip，裸调用会让整片区域变手型。
9. **字体**：`text.Shaper` 只建一次，界面族是 `Noto Sans, Noto Sans CJK <按语言>, sans-serif, emoji`，字号只有六级阶梯（族顺序、CJK 变体与阶梯取值见 [§14.1](#141-文本字体)）；图标字形尺寸按 (fill, size, pxPerSp) 缓存，避免每帧 shaping。
10. **不要在视图构造期长期缓存译文**：语言切换会重建视图，缓存的旧语言字符串不会自动更新。
11. **转圈自驱帧**：列表工具栏刷新按钮的 `spinning` 在绘制时 `gtx.Execute(op.InvalidateCmd{})` 请求下一帧，`spinStart` 只在旋转期间非零（停止时归零，下次从正立开始）。`listToolbar.setLoading` 是唯一切换点，并给 45s 看门狗（`loadingUntil`，`Layout` 里过期即清除）：任何遗漏的清除路径最多多转一个看门狗周期，不会留下永久 60fps。转圈期间按钮吞点击，避免连点叠出多次 reload；不要用 `setDisabled` 表达转圈，它会把字形压到 35% 不透明度。
12. **可选文字与可点区域共存**：`widget.Selectable` 的点击/拖拽手势只认主键与触摸（`gesture.Click` / `gesture.Drag` 内部过滤），右键既不会清空选区也不会被它消费；要抓右键必须自己声明区域并在读事件时判断 `e.Buttons`，`pointer.Filter` 没有按键维度。该区域必须是 pass-through（`pointer.PassOp`）且声明在文字之后，否则会挡住下层的选区手势。父级 `Clickable` 与文字同时收到点击，所以「整行可点 + 文字可选」无法靠事件顺序区分：展开的邮件卡把可点区域拆到非文字部分（头像 / 时间 / 箭头），点击发件人姓名与地址只定位光标。选区状态由 `textSelection` 按 owner（消息或会话 ID）重建，见 `internal/ui/textselection.go`。
13. **失去焦点的选区仍要可见，剪贴板写入要等一帧**：`Selectable` 默认只在获得焦点时绘制高亮，而右键菜单会通过 `showOverlay` 排队 `blurRequested` 把焦点交还窗口根；阅读器因此在 `textSelection.Select` 统一置 `PaintSelectionWhenUnfocused`，菜单打开后选区仍然可见。剪贴板写入不能发生在 `menuItem.OnTap` 里（没有 `layout.Context`）：`App.copyText` 只把文本放进 `clipPending`，`App.layout` 开头的 `flushClipboard` 在下一帧执行 `clipboard.WriteCmd`。
14. **一个阅读器只有一处选区**：`Selectable` 不提供选区变化回调，所以 `textSelection` 每帧比较 `sel.Selection()` 的 `(start, end)` 与上一帧记录（`seenStart`/`seenEnd`），范围变化即上报 `readerView.retireSelections`，由后者把标题与所有卡片的其余 run `ClearSelection()`。另外在读 `pointer.Press` 时，主键（或 `pointer.Touch`）按下也要清空其它 run，这样"点一下别处"就取消旧高亮；右键不能清，否则菜单拿不到选区。清空必须主动遍历 `r.cards`，不能等下一次布局：折叠或滚出视口的卡片不会被绘制，但 `replySelection` 仍会读到它。清掉非空选区后用 `invalidateAfterTap` 请求一帧，因为先绘制的 run 已经带着旧高亮画完了。

## 7. 窗口装饰（CSD）与光标

### 7.1 机制

- 应用建窗时 `app.Decorated(false)`，唯一判定出口是 `clientSide() = !decorated`：`Config.Decorated` 是「平台是否装饰」的唯一事实来源。**X11 永不画自绘标题栏**（Gio 在 X11 恒要求服务端装饰并报告 `Decorated == true`，自绘会重复）。
- 自绘标题栏承载侧栏按钮 / logo+品牌 / 搜索胶囊 / 设置 / 头像 / 分隔线 / 最小化·最大化·关闭（高度见上节，`windowcontrols.go` 用 clip 自绘，字体无窗口字形）。
- Wayland 上由 `internal/waylanddeco` 让合成器回答「客户端装饰」：Gio 建窗时只注册 `zxdg_toplevel_decoration_v1` 的 `configure` 监听，从不发送 `set_mode`，装饰模式由合成器默认决定（KWin 对强制无边框的窗口回 `server_side`，源码注释 *Faked as server_side mode*）。该包用 `#cgo LDFLAGS: -Wl,--wrap=wl_proxy_add_listener` 在链接期包装符号：生成的 `zxdg_toplevel_decoration_v1_add_listener` 是 static inline，最终落到非变参的 `wl_proxy_add_listener`，包装函数按 proxy 的 interface 名认出装饰对象后立即补发 `set_mode(client_side)`，时序仍在首次 `wl_surface.commit` 之前。`vendor/` 不改动，去掉这个包即回到原行为。
- 拖动只把 `system.ActionInputOp(system.ActionMove)` 登记在空白抓手区（clip 包裹、绘制顺序在按钮之后）；按在搜索框/按钮上不能被 move 消费。
- 光标主题解析顺序：`XCURSOR_THEME`/`XCURSOR_SIZE`（用户显式设置）→ XDG portal `org.freedesktop.portal.Settings`（`org.gnome.desktop.interface`，750ms 超时）→ KDE `kreadconfig6`（回退 `kreadconfig5`）→ GNOME `gsettings get`（750ms 超时）。导出前必须校验 `<theme>/cursors` 真实存在，否则 Wayland `wl_cursor_theme_load` 失败会导致无法建窗。
- 门户必须排在 KDE 之前：`org.gnome.desktop.interface` 由 `xdg-desktop-portal-gtk` 转发 dconf 实现，Flatpak 内也靠它透视宿主设置；KDE 的 portal 只注册 `org.freedesktop.appearance` 与 `org.kde.*`，查询该命名空间会失败并自然落到 KDE 来源，因此不做会话判断。
- KDE 不能直接读 `~/.config/kcminputrc`：KWin 用 `KConfig::NoGlobals` 打开它，而该标志的字面值就是 `CascadeConfig`，会把 `$XDG_CONFIG_DIRS/kcminputrc`（通常 `/etc/xdg/kcminputrc`）当默认层读进来；KCM 也只在用户改过光标时才把 `cursorTheme`/`cursorSize` 写进用户文件。所以改用 `kreadconfig6 --file kcminputrc --group Mouse --key cursorTheme`（不传 `--default`），由 KDE 自己完成级联。
- `gsettings` 放最后兜底：它在任何桌面都会返回 schema 默认值（`Adwaita`），只有门户与 KDE 都没结果时才轮到它，避免把其他桌面的真实设置盖掉。Flatpak 内 `gsettings`/`kreadconfig6` 读不到宿主配置，这是从纯 D-Bus 门户改为 CLI 的已知代价。
- 未导出尺寸时 Gio 的 Wayland 后端自己用 32（`os_wayland.go`），KDE 未设置时是 24，所以 `kreadconfig6` 读不到值时两侧尺寸可能差一档。
- 应用自己导出的值会被下一次解析读到（误判为「用户设置」并且不再回查 portal），因此 `cursor` 包用 `lastExport` 在读取 env 时屏蔽自写值；`Apply` 只在每次建窗前调用一次。运行中换主题需重开窗口。
- **边缘缩放挂在 `Config.Decorated` 上**：Gio 的缩放热区 `systemGesture()` 首行即 `if Mode != Windowed || Decorated { return nil, 0 }`，只有合成器确认客户端装饰时才有缩放光标与 `xdg_toplevel.resize`。`internal/waylanddeco` 的 `set_mode(client_side)` 就是让合成器回 `configure(client_side)` 的那一步；应用自己拿不到 `wl_surface`/`xdg_toplevel`（`io/system` 只暴露 `ActionMove`），也不需要拿。
- **最小化后的模式同步**：Gio 的 Wayland 后端把 `Mode` 置为 `Minimized` 后只靠 `wl_surface.enter` 清除，而窗口在同一 output 恢复不会触发 enter；`Configure` 又会丢弃 `prev.Mode == Minimized` 时的一切模式切换，于是恢复后最大化按钮、标题栏拖动与缩放热区全部失效。应用不做可视化补偿：窗口层在 `Perform` 之后，按纯函数 `windowSteps`（`windowcontrols.go`，帧回调里算出并放进 `WindowActions.ResetMode`）的判断在最小化时补一次 `app.Windowed`，复位这条僵死记录（协议层窗口仍然是最小化的）。代价是幂等的最小化前已最大化情形需要点两次最大化：第一次只把状态同步回合成器本已保持的最大化。

### 7.2 使用者配置

- **Wayland 恒用自绘标题栏**：`internal/waylanddeco` 让合成器确认客户端装饰，KWin 与 Mutter 都不再画边框，**不需要任何窗口规则，也没有应用内开关**。
- **平台自己装饰时保持系统边框**：X11（Gio 恒要求服务端装饰）、Windows、macOS 由平台画标题栏（原因见 §7.1）。
- **X11 无法使用客户端标题栏**。
- **光标主题覆盖**：环境变量优先级最高，可绕过自动解析直接指定；运行中更换系统主题后，从托盘关闭再打开窗口即可生效。

  ```bash
  XCURSOR_THEME=breeze_cursors XCURSOR_SIZE=24 ./yauzin
  ```

### 7.3 可感知限制（Gio v0.10.3）

- Wayland 后端不解析 `xdg_toplevel` 状态：由窗口管理器（快捷键 / 边框双击）最大化或还原时，标题栏的最大化图标不会同步；应用内的最大化按钮可以正常切换。

## 8. 正文与 HTML 安全

- 阅读器只渲染 `text/plain`（可选中复制），HTML 从每封邮件卡片头部的「更多」菜单打开，原文可在同一菜单下载为 `.eml`；回复/转发旁不再有浏览器按钮。选区按 `msg.ID` 变化时重建/清空，避免跨邮件残留；回复时该邮件自身的选区会成为引用内容（见 §4.3）。
- **可选文字**：会话主题（归属会话 ID）与展开卡片里的发件人姓名、邮箱地址（归属消息 ID）都用 `theme.selectableText` / `selectableLine` 绘制，可拖选与 Ctrl+C；姓名与地址是单行省略号，卡片头部行高不变。选区高亮在失焦时仍绘制（见 §6.13）。切换会话由 `readerView.reset()` 清空，切换消息由 owner 变化重建。
- **单一选区**：整个阅读器同一时刻只有一处选区。在标题、某封邮件的姓名、地址或正文里开始新的选择，会清掉其它所有位置的选区（含同一会话其它邮件、折叠或滚出视口的卡片），点击另一段文字（即使只是定位光标）也会取消旧高亮；右键不清空，以便菜单作用于该段选区。实现见 §6.14。
- **选区右键菜单**：主题、姓名、地址、正文共用 `App.selectionMenuItems`，菜单锚点是窗口指针位置（`App.pointer`）。恒有「复制」；选区去掉首尾空白后是完整的 `http://` / `https://` 地址（`webURL`：`url.Parse` 成功且 host 非空）时多出「在浏览器中打开」，交给 `openPath`；其它 scheme（`file:`、`javascript:`、`mailto:` 等）一律不提供该项。没有选区时右键不弹菜单。
- **折叠区域**：展开卡片不再整行可点——点击发件人姓名/地址只定位光标；折叠由头像、时间与右上箭头三个小 `Clickable` 承担（`messageCard.foldTarget`）。折叠状态的摘要行保持整行可点。
- 菜单三项：`安全预览`（注入严格 CSP）、`完整预览（不安全）`（只声明 charset，不加策略，脚本与远程资源可用，风险写在文案里且不再二次确认）、`下载邮件`（`SaveMessage` → 门户保存对话框 → 侧栏任务行）。前两项仅在有 HTML 正文时出现，第三项恒有。菜单项文案不复用旧键 `reader.openInBrowser`。
- 下载邮件与附件下载共用同一条路径（`filepicker.go`）：先选目标文件，再在 `timeoutAttach` 内流式写入，任务行报告进度、支持取消并保留保存路径；默认文件名 = 净化后的主题（保留 Unicode，去掉路径分隔符与控制字符，按 200 字节在 rune 边界截断）+ `.eml`，空主题回退 `message.eml`。
- HTML 落盘：文件名 = 净化后的消息 ID（`[A-Za-z0-9._-]`，截断 64）+ 内容 sha256 前 8 字节（防目录穿越、绕开 `file://` 缓存），模式后缀 `-unsafe` 保证两种预览互不覆盖（安全预览沿用原命名）。
- CSP 用 `x/net/html` 解析后插到**真实 `<head>` 的第一个子节点**（防注释/脚本里的假 `<head>`），`html.Render` 重新序列化是有意取舍；解析失败走 `wrapperDocument` 安全包裹。策略为 `default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:; form-action 'none'; base-uri 'none'`，远程图片/追踪像素故意禁用。默认路径不被弱化：不点「完整预览」时策略照旧注入。
- 临时目录惰性创建于 `$XDG_RUNTIME_DIR/yauzin`（0700；该变量缺失、非绝对路径或不可用时回退 `os.MkdirTemp` 到系统临时目录）：会话运行目录在登录会话结束时由系统回收，被 `SIGKILL` 的运行最多留下一个会自动过期的目录，不会在 `/tmp` 里逐次累积。会话内路径固定，首次创建前先删掉上次异常退出留下的同名目录；正常退出与 `Ctrl+C`（`SIGINT`）都会整体删除，删除后 `tempDir()` 返回错误而不再重建。
- 正文只认 `text/plain`：没有纯文本的邮件就是没有正文，阅读器显示 `reader.noPlainText`，回复/转发的引用写英文占位 `(no body)`（`quoteNoBody`，不加 `> ` 前缀）。HTML 正文不做任何文本化（正则剥标签、`x/net/html` 取文本都不做），只有浏览器预览会读到它。
- 正文解码不看部件 `Content-Type` 的 `charset`，一律按 UTF-8 取字符串（理由见 §3.1 读取映射）。后端若真把单字节字节流原样透传，界面显示替换符，而不是按标签猜测式转码。

## 9. 国际化

- 自建 `internal/i18n`（`embed` + go-i18n）：en 是权威源，zh-Hans 必须 key 完全对齐（`i18n_test.go` 守护）；`T`/`N` 总是带 `DefaultMessage`，缺 key 时回退英文而不是空白。
- 语言列表由内嵌文件与各文件的 `language.name` 推导；`"auto"` 跟随系统 locale。
- 运行时切换顺序敏感：`i18n.SetLanguage` → `prefs.Save` → 重建侧栏 / 清空阅读器卡片缓存 → 更新窗口标题 → `tray.Relabel` → 新状态条 → **最后**重建设置面板。
- 系统文件夹名按 `FolderKind` 翻译，用户标签名与账号名不翻译。
- 桌面通知文案同样走 i18n：`notify.title`、`notify.count`（复数，按未读会话数）、`notify.open`；空主题复用 `list.noSubject`。通知在发送时取当前语言，切换语言后随即生效。**应用名不在通知里传**：它固定来自 `me.zhsj.yauzin.desktop`（直连的 `desktop-entry` hint 与门户的 app id 指向同一个文件），因此不随界面语言变化，Plasma 的按应用设置也始终落在同一条目上。`app.brand` 仍用于窗口标题、顶栏与托盘。

## 10. 持久化

`internal/prefs` 以 0600 + 临时文件 + rename 原子替换写入 `os.UserConfigDir()/yauzin/settings.json`（`YAUZIN_CONFIG_DIR` 可覆盖目录，该变量由 `internal/env` 统一读取）。字段：

| 字段 | 含义 |
| --- | --- |
| `language` | 语言标签，空/`auto` 表示跟随系统 |
| `showPerfMonitor` | 顶栏显示性能读数 |
| `proxy` | 出站邮件请求使用的代理（`http`/`https`/`socks5`，空表示直连） |
| `logLevel` | 控制台日志级别（`error`/`warn`/`info`/`debug`，空或未知表示 `info`） |
| `activeAccount` | 上次使用的账号 ID，下次启动优先打开；为空、账号已删除或后端构建失败时回退列表第一个账号 |

约定：`LoadOrZero` 把「文件不存在」当首次运行，其余错误只告警、不覆盖；修改设置一律「先落盘成功再改内存」，失败提示并回滚。`activeAccount` 是例外：账号切换已经发生，写盘失败只记日志、不弹提示；删除账号也不清该字段，下次启动匹配不到账号即回退，行为仍然正确。

账号凭据与令牌另存 `accounts.json`（`internal/accounts`，同为 0600 原子写，目录由 `prefs.Dir()` 统一解析，`YAUZIN_CONFIG_DIR` 对两者同时生效）。它不放进 `settings.json`：令牌刷新频繁且由后端在 worker goroutine 写回，与 UI 设置的读写节奏不同。

同一个文件还存 SMTP 发信账号：`kind` 为 `smtp`，`smtp{host,port,username,password,security,bccToAccount}` 是它唯一的配置，没有令牌。它们**不是邮箱**——`configuredAccounts` 按 kind 跳过，顶栏账号菜单、侧栏与后端工厂都看不到它们，写信浮层与「设置 → 账号」的发信账号分组才读它们。`id` 为 `smtp:` + 小写发件地址，同地址再次保存即覆盖（与 Gmail 的 `gmail:` + 小写邮箱同一规则）；编辑时改了发件地址就用 `Store.Replace(oldID, entry)` 一次写入完成改名，避免写盘失败留下两份或零份。密码与 OAuth client secret 同策略：明文存 0600 文件、不写日志，没有系统密钥环集成。

## 11. 托盘与单实例

- `internal/tray` 自己发布 Linux 托盘：用 `github.com/godbus/dbus/v5` 导出 `org.kde.StatusNotifierItem`（`/StatusNotifierItem`）与 `com.canonical.dbusmenu`（`/StatusNotifierItem/menu`），再调 `org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem` 注册。**不申请** `org.kde.StatusNotifierItem-<pid>-1` 这类总线名：宿主按消息 sender 的唯一名 + object path 定位 item，申请名字在 Flatpak 的 xdg-dbus-proxy 下只会失败并刷日志。左键 `Activate` 直接开窗，右键菜单只有「打开 / 退出」；回调在 D-Bus goroutine 上，必须经 `post` / channel 回 UI 线程。watcher 不存在、或权限被沙箱过滤（`ServiceUnknown`/`NameHasNoOwner`）时 `Start` 返回错误，应用降级。属性由一份状态快照生成、手写 `org.freedesktop.DBus.Properties`（不用 `dbus/prop`，`Set` 一律返回只读），`NewIcon`/`NewTitle`/`NewToolTip` 与 `PropertiesChanged` 从同一出口发出；watcher 重启（`NameOwnerChanged`）时自动重注册。托盘只持有 PNG 字节：`Icons.Idle`/`Icons.Active` 分别对应灰点与红点两态，启动时各解码一次成 ARGB32 缓存，`SetActive` 在状态未变化时不发信号。
- 图标设计源为 `artwork/icon-gray.svg`（灰封蜡，托盘 idle）与 `artwork/icon-red.svg`（红封蜡，托盘 unread 与顶栏 mark），由 `scripts/gen_app_icons.py` 用 rsvg-convert 派生 PNG 后提交（小尺寸写 `internal/ui/assets/`，512px 留在 `artwork/`）；运行时不解析 SVG。顶栏 mark 用红点母版，cell 固定 36dp：内嵌 32–128px 多个尺寸，按设备像素取刚好覆盖的一张（`internal/ui/appicon.go`），1x / 1.25x / 1.5x / 2x / 3x 正好命中 36 / 45 / 54 / 72 / 108px（旧的 32/40/48/64/96/128px 变体保留，覆盖 1.1x、1.75x 这类零散比例），缩放比例为 1，不会被线性滤波插值而发糊。Gio v0.10.3 没有窗口图标 API，窗口/任务栏图标由桌面集成使用 `artwork/icon-red-512.png`（与顶栏 mark 同一母版）。
- **轮询与徽标**：轮询属于进程而不是窗口——`Run` 在 `tray.Start` 之后 `startPolling()`，退出前 `stopPolling()`。托盘驻留期间每分钟经同一检查通道（单飞 + 冷却，见 §4.2）检查一次，变化时只重载侧栏（标签名 + 未读数/颜色），不拉列表；徽标在 `loadFolderDetails` 的成功回调里统一刷新（收件箱未读 > 0 红、= 0 灰），因此启动、手动刷新、轮询、切账号、重建后端都会更新它。重开窗口时 `attach` 的「检查 + 全量加载」补齐列表。
- `internal/singleinstance` 用会话总线名 `me.zhsj.yauzin`（同时是 Gio application ID）保证单实例：第一个进程持有名字并导出 `Open` 与 `OpenURI`，后续启动唤起窗口后 `os.Exit(0)`。带 mailto 参数时调 `OpenURI(uri)`，正在运行的实例若是旧版本、没有该方法，则回退 `Open`（窗口照开、URI 丢失并记日志），避免升级期间激活整个失败。Wayland 不允许强制前置已有窗口，这是已知限制。
- **会话总线归进程所有**：单实例、通知、托盘都用 `dbus.SessionBus()` 这条进程级共享连接。任何包都不 `Close` 它：通知与托盘在 `Close` 里只 `RemoveSignal` / `RemoveMatchSignal` / `Export(nil, …)` 撤销自己挂载的东西，`singleinstance.Acquire`/`call` 失败路径也不关闭；进程退出（`os.Exit(0)`）时由内核回收 fd。需要独立生命周期时才用 `SessionBusPrivate()` 自建连接，但那就不再共享。
- **mailto**：`uihost.Main` 把 `os.Args[1:]` 原样交给 `ui.Run`，解析放在纯 Go 的 `internal/mailto`（RFC 6068 的 path + hfields；按 `&` 与首个 `=` 手工切分、用 `url.PathUnescape` 解码，因为 `url.ParseQuery` 会把 RFC 6068 里字面的 `+` 当空格）。认 `to/cc/bcc/subject/body` 与 RFC 2368 遗留的 `in-reply-to`，地址类按出现顺序累加并去重，单行字段把 CR/LF 换成空格，正文把行尾归一为 LF。启动参数与「D-Bus 回调早于 `newApp` 返回」的 URI 都只 `post` 不 `requestOpen`（首个 `FrameEvent` 的 `drain` 就会打开撰写框；乱发 `openCh` 令牌会让用户刚关的窗口被重新弹出），外部激活才 `post + requestOpen`，`attach` 会清掉残留令牌。撰写框是单槽位，收到 mailto 一律替换当前草稿（丢弃未发送内容，属既定取舍）；有收件人时焦点落在正文并置于链接正文末尾。`in-reply-to` 经 `mail.NormalizeMessageID` 校验后写进 `composeView.inReplyTo`，`references` 只含这个父 ID；非法值只丢该头，主题不加 `Re:`。日志只说「拒绝了 mailto URI」，不记 URI（含收件人与正文）。

## 12. 性能监视器

- 数据源只用标准库：CPU 取 `runtime/metrics` 的 `/cpu/classes/total:cpu-seconds` 两次采样差 ÷ 时间差；内存取 `runtime.ReadMemStats().HeapAlloc`；goroutine 用 `runtime.NumGoroutine()`；FPS 用相邻 `app.FrameEvent.Time` 差做 EMA。
- 仅在可见时跑 1s ticker，隐藏即 `Stop`；采样结果走 `post` 回 UI 线程（同时维持 1Hz 帧循环以便测 FPS）。
- CPU 差分可能因 GC 突发出现脏样本（> `100×GOMAXPROCS` 视为无效，沿用上次值），正常样本用 alpha=0.5 EMA。数值定宽填充防抖动。

## 13. 环境与构建

### 13.1 环境要求

- Go 1.26+（`go.mod` 声明 `go 1.26.0`）；开发机工具链是 `/run/host/lib/go-1.27/bin/go`（go1.27.2），构建镜像内为 Debian unstable 源的 `golang-go` 1.27。
- CGO 开启：Gio 没有纯 Go 后端。
- Linux 运行需要 X11/OpenGL 或 Vulkan 运行库（`libx11`、`libxkbcommon`、`libGL`/`libvulkan`）；从源码编译还需要 `libx11-dev`、`libxkbcommon-x11-dev`、`libxcursor-dev`、`libxfixes-dev`、`libwayland-dev`、`libegl-dev`、`libvulkan-dev`。
- 文件选择与附件保存通过 `xdg-desktop-portal` 提供的系统对话框；对话框不可用时保存直接报错并提示「系统文件选择器不可用」，不回退到 `~/Downloads`，也不写任何文件。附件保存先选位置再下载，内容直接写入所选文件；选择附件只记录用户文件的原始路径，不复制到临时目录，发送时才按需从原文件流式读取，草稿关闭或移除附件都不会删除原文件（文件在发送前被移动/改动会给出明确错误）；附件保存与邮件发送都显示在侧栏底部的「任务列表」分栏，任务完成后保留到进程退出。
- macOS 需要 Xcode Command Line Tools，Windows 需要 MinGW-w64。
- 开发沙箱 `CGO_ENABLED=0` 且缺少上述开发库：任何（直接或间接）import `gioui.org/app` 的目标（`internal/uihost`、`cmd/yauzin`）只能在 Docker 内编译，典型报错 `build constraints exclude all Go files in .../gioui.org/internal/vk`。`internal/ui` 及其全部测试不在此列：它只经接口用到窗口层，可以在沙箱直接 `go test -vet=all ./internal/ui`。
- 沙箱内访问外网走本地代理，命令前置 `https_proxy=http://127.0.0.1:1081`；`internal/cmd/gmailgen` 只在下载 Discovery 文档时需要它，代码本身不读代理配置。
- 格式化使用 `gofmt`（沙箱 `PATH` 内，go1.27.2）；import 增删与分组使用 Go 扩展安装的 `goimports`（`~/go/bin/goimports`，**不在沙箱 `PATH`**），命令见 §13.5。
- 静态检查用 `golangci-lint`（`staticcheck` + `gofumpt`/`goimports`），它随构建镜像提供，只在容器内运行（Gio 的包需要 cgo 与 C 头文件），命令见 §13.5。

### 13.2 依赖与 vendor

GUI 使用 Gio v0.10.3，文件选择使用 `gioui.org/x/explorer`，Gmail 授权使用 `golang.org/x/oauth2`，SMTP 的 socks5 拨号用 `golang.org/x/net/proxy`（`golang.org/x/net` 本来就是直接依赖，只多 vendor 一个子包）。`vendor/` 已被 `.gitignore` 排除、不入库，但构建镜像使用 `-mod=vendor`，所以本地必须先生成。依赖变更后（沙箱无外网直连，走镜像代理）：

```bash
GOFLAGS=-mod=mod CGO_ENABLED=1 \
  GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.google.cn \
  go mod tidy
GOFLAGS=-mod=mod CGO_ENABLED=1 \
  GOPROXY=https://goproxy.cn GOSUMDB=sum.golang.google.cn \
  go mod vendor
```

Gmail 只引入 `golang.org/x/oauth2`，不引入 `google.golang.org/api`：官方生成客户端的 `option` 包直接依赖 `google.golang.org/grpc` 与 `cloud.google.com/go/auth`（`protobuf` / `OpenTelemetry` 由 `grpc` 间接带入），而本项目的 REST 调用面很小。

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

本机缺少 C 依赖时使用仓库内的构建镜像 `Dockerfile.build`：基于 `debian:unstable`，apt 源为 USTC 镜像，Go 工具链（`golang-go`）与 Gio 需要的 X11 / Wayland / EGL / Vulkan 开发库全部从 Debian 源安装（不下载官方 Go tarball，也不需要 `zig cc`）。镜像内已固化 `CGO_ENABLED=1`、`GOFLAGS=-mod=vendor`、`GOTOOLCHAIN=local`、`GOCACHE=/gocache`、`GOLANGCI_LINT_CACHE=/gocache/golangci-lint`，运行时不必再传 `-e`。

镜像还内置 golangci-lint：Debian 源里的版本太旧，因此用
`GOPROXY=https://goproxy.cn go install` 从模块代理装到 `/usr/local/bin`，版本由 `ARG GOLANGCI_LINT_VERSION` 钉住（当前 `v2.15.0`），这是整个镜像构建里唯一需要联网的一步（因此 apt 层补装了 `ca-certificates`）；改版本即重建该层，其余层命中缓存。

```bash
docker build -t yauzin-gio-builder -f Dockerfile.build .

docker run --rm --user $(id -u):$(id -g) \
  -v "$PWD":/app -w /app \
  -v yauzin-gocache:/gocache \
  yauzin-gio-builder \
  go build -o /tmp/yauzin ./cmd/yauzin
```

- 容器一律以 `--user $(id -u):$(id -g)` 运行：写回的源文件与缓存都属当前用户，仓库不会出现 root 属主文件。
- `yauzin-gocache` 是命名卷，挂到容器内 `/gocache`，编译缓存跨容器保留：热缓存一次编译约 1–2 秒，冷缓存或强制全量（`go build -a`）约 10–15 秒；清空用 `docker volume rm yauzin-gocache`。漏掉这个卷等于每次冷编译。
- 缓存卷若曾被不带 `--user` 的 root 运行写过，里面的子目录不属当前用户，Go 会因此加载不到包（`no go files to analyze`）；`docker volume rm yauzin-gocache` 清一次即可，里面只有缓存。
- 改动 `Dockerfile.build` 后必须重建 `yauzin-gio-builder`（layer cache 命中通常几秒），否则仍在用旧工具链/旧依赖。
- 只做编译验证时产物写容器内 `/tmp`，避免仓库根出现 root 属主的二进制；确实要交付二进制时用 `docker cp` 取出后再 `chown $USER: yauzin`。
- 运行 GUI 需在宿主桌面会话中进行，不要在容器或沙箱里启动。

### 13.5 静态检查与测试

容器内跑测试与 lint（`internal/uihost`、`cmd/*` 导入 Gio，必须有 cgo 与 C 头文件；`./...` 因此要整个模块一起跑）：

```bash
docker run --rm --user $(id -u):$(id -g) \
  -v "$PWD":/app -w /app -v yauzin-gocache:/gocache \
  yauzin-gio-builder go test -vet=all ./...

docker run --rm --user $(id -u):$(id -g) \
  -v "$PWD":/app -w /app -v yauzin-gocache:/gocache \
  yauzin-gio-builder golangci-lint run --fix --timeout=15m ./...
```

- 终端在 Flatpak 沙箱内时，这两条前面加 `flatpak-spawn --host`（工作目录用 `--directory=<仓库>`），命令本身不变。
- 配置是仓库根的 `.golangci.yml`：linters 用默认集（errcheck / govet / ineffassign / staticcheck / unused），formatters 是 `gofumpt` + `goimports`。
- `run --fix` 一步完成格式化与可自动修的 lint 修复，修不了的仍会列出来并以非零码退出，所以它同时是“修”和“查”；只想检查、不动工作区时去掉 `--fix`。
- `--fix` 会就地改写源文件；两条命令都带 `--user $(id -u):$(id -g)`（理由见 §13.4），写回的文件仍属当前用户。
- 合格标准是输出 `0 issues.`；`issues.max-issues-per-linter` 与 `max-same-issues` 都是 0，报告不会被截断。
- v2 里 `gosimple` / `stylecheck` 已经并入 `staticcheck`；`gofumpt` / `goimports` 属于 `formatters` 段，`run` 会以 “File is not properly formatted” 报出来。
- 生成代码（`discovery_types.go`）靠文件头的 `Code generated ... DO NOT EDIT.` 被默认的 `exclusions.generated: strict` 跳过。
- `GOLANGCI_LINT_CACHE=/gocache/golangci-lint` 与 Go 编译缓存同一个命名卷，重复运行明显更快。

纯 Go 包测试与格式化检查不需要容器，直接在本地（沙箱）跑：

```bash
go test -vet=all ./internal/env ./internal/prefs ./internal/i18n ./internal/mail/... ./internal/tray ./internal/cursor ./internal/ui

gofmt -l internal/ cmd/
~/go/bin/goimports -local github.com/zhsj/yauzin -l internal/ cmd/
```

- 两条格式检查都无输出即合格；写回分别用 `gofmt -w` 与 `goimports -w`，`gofumpt` 只在镜像里，需要它时用上面的 `golangci-lint run --fix`。
- import 块固定三段：标准库 / 第三方 / `github.com/zhsj/yauzin/...`，由 `.golangci.yml` 的 `goimports.local-prefixes` 与命令行的 `-local` 共同保证，不要手工插空行。
- `goimports` 接受目录与多个路径，不需要 `git ls-files` 展开；它顺带做 gofmt 等价格式化，所以 `-w` 之后 `gofmt -l` 仍为空，两条检查都保留。
- `goimports` 只处理磁盘文件，编辑器里未保存的改动需先保存。
- 不用 `gopls` 做格式化或分组：`gopls format` 等价 gofmt、只排序不分组（`local` 设置只在 Organize Imports 或插入新 import 时生效，格式化请求 "merely sorts the existing imports"），命令行 `gopls imports` 也没有 `-local` 参数，只能分出两组。

## 14. 字体与图标资产

### 14.1 文本字体

文本字体完全使用系统字体：Gio 的 text shaper 自 v0.2.0 起默认加载系统字体（基于 fontconfig 目录与配置解析，首次扫描约 0.2–0.5s 并缓存到用户缓存目录），中文由系统安装的中文字体提供；Flatpak 沙箱内的 `/run/host/fonts` 也通过运行时的 fontconfig 配置自动可达。系统一个字体都没有时，界面回退到内置的 Go 字体（仅拉丁字符）。界面文本的族列表由 `internal/ui/fonts.go` 的 `textTypeface()` 按语言算出，形如 `Noto Sans, Noto Sans CJK SC, sans-serif, emoji`：第一段画拉丁，第二段按 locale 选 CJK 变体（`zh-Hant` / `zh-TW` / `zh-HK` / `zh-MO` → TC，`ja` → JP，`ko` → KR，其余含 `zh-Hans`、`en` 与无 locale → SC），第三段 `sans-serif` 是前两段都缺失时的系统兜底，第四段只兜底前几段渲染不了的 emoji 码点。语言来源优先 `i18n.SystemLocale()`，其次当前界面语言 `i18n.Language()`，因此运行中切换界面语言不需要重建 shaper。

族顺序有三条硬约束：

- `Noto Sans` 必须排在 CJK 族之前。Noto Sans CJK 同样覆盖拉丁，排在前面会让拉丁字形变成它自带的 Source Sans。
- CJK 族必须显式写出。fontscan 的语言相关替换只认脚本，而 go-text 的 `language.ScriptToLang` 把 Han 映射为 0（没有哪个语言能同时代表中日月韩汉字字形），语言替换对汉字整体失效，脚本兜底会落到 **Noto Sans CJK JP**，中文于是显示日文字形（「直」「骨」这类部件的写法不同）。写明目标族之后，`fontmap.resolveForRune` 逐族检查字符覆盖：拉丁命中第一段，汉字顺延到第二段，互不干扰。缺对应 Noto Sans 变体的机器上该段被跳过，仍按 `sans-serif` 兜底，不会出现空白或方框。
- `emoji` 必须最后。emoji 属于 Common 脚本，go-text 会把它并入前一个 Latin/CJK run 一起做字体回退；放在前面时，覆盖 Latin 且数据库顺序更靠前的单色符号字体（如 Symbola）会抢到 emoji，显示成黑白。放在末尾时它在精确族匹配阶段命中彩色 emoji 字体（fontconfig 替换表优先 Noto Color Emoji），不受前一 run 脚本影响。也不能把 `emoji` 放开头：Noto Color Emoji 的 cmap 同样覆盖 ASCII 数字、`#`、`*` 和空格，会以 1.245em 的 emoji 宽度渲染它们。

自查选族：`GIODEBUG=text ./yauzin` 会打印 `[text] loaded face ...`，汉字应解析到 `notosanscjksc`、拉丁到 `notosans`；出现 `notosanscjkjp` 说明族列表没生效或系统缺该变体。

已知限制：Gio 把字形轮廓转成 `clip.Path` 在 GPU 上按几何覆盖率填充，没有 FreeType 的 hinting，并且在线性光空间做 alpha 混合（上游 `~eliasnaur/gio#70` 称为 "correct sRGB blending"，`vendor/gioui.org/gpu/gpu.go` 的 `f32color.LinearFromSRGB`），部分覆盖的像素因此比浏览器（在 sRGB 空间混合）更亮；13–16px 下 1px 竖干只能拿到部分覆盖率，同字号下看起来比浏览器偏淡、偏软。`text.Parameters` 没有画质开关，应用层改不了，不要用放大字号或加深文字颜色的方式「补偿」——浏览器正文同为 Noto Sans 13px 时差距依旧存在。上游在换渲染器，方向是把字形绘制搬回 CPU，届时才有望对齐。

字号阶梯：界面文字只有六级，定义在 `internal/ui/typography.go`——`textTitle` 24sp（阅读页主题、标题栏品牌名）、`textHeading` 20sp（页面与对话框标题）、`textMain` 16sp（正文、侧边栏标签、列表行、按钮、输入框）、`textSecondary` 14sp（会话消息数、列表时间、工具栏状态）、`textMeta` 13sp（地址、完整时间戳、设置说明）、`textTiny` 12sp（标签 chip、性能条）。正文、侧边栏标签与列表行主题刻意留在 `textMain` 同一档，这是三者「一样大」的来源；新增文字必须复用其中一级，`typography_test.go` 用 `go/ast` 扫描拒绝给文本助手传整数字面量。图标字号（`theme.glyph` / `glyphIn`）不在这套阶梯里。

阅读正文另外设置 1.5 倍行高（`bodyLineHeight = textMain * 3 / 2`，即 24sp）：Gio 默认的 1.2 倍对多行邮件过挤，而「没有纯文本正文」的占位提示只是单行，沿用默认。

彩色 emoji 依赖字体自带位图（CBDT/CBLC、sbix）：Gio 只渲染位图与轮廓字形，COLR/CPAL 彩色字形不支持，仅有 COLR 字体的系统会显示空白 emoji。已知限制：go-text 的 `splitByFace` 让空白不触发换字体，紧跟在 emoji 后面的空格会沿用 emoji 字体的空格度量（约 1.245em，明显比正常的 0.26em 宽）；应用层不改文本无法规避，待上游修正。

### 14.2 图标字体

图标使用内置的 Material Symbols Rounded 子集（Apache-2.0，见 `internal/ui/assets/LICENSE-Apache-2.0.txt`）。子集只保留 `internal/ui/icons.go` 中列出的码点，体积仅几 KB；新增图标后重新生成：

```bash
python3 scripts/gen_icon_fonts.py            # 需要 Python3 + fonttools
python3 scripts/gen_icon_fonts.py --check    # 校验现有字体是否与代码同步
```

脚本默认从 Google material-design-icons 仓库下载上游变量字体，也可用 `--font <路径>` 指定本地副本；输出字节级幂等。`icons_test.go` 会在生成字体缺少码点时失败。

### 14.3 应用图标

设计源是 `artwork/icon-gray.svg`（灰色封蜡）与 `artwork/icon-red.svg`（红色封蜡），两者几何完全一致、只有封蜡颜色不同。程序不解析 SVG，二进制只嵌入由二者派生的小尺寸 PNG（写进 `internal/ui/assets/`）；`artwork/icon-red-512.png` 不进二进制，留给桌面集成（`.desktop` 的 `Icon=` 或任务栏），因为 Gio v0.10.3 没有窗口图标 API。改图标后重新生成：

```bash
python3 scripts/gen_app_icons.py            # 需要 rsvg-convert（librsvg2-bin）
```

母版是 174×118 的宽扁 `viewBox`，而产物必须是正方形。脚本渲染前会改写根标签：去掉 `width`/`height`，把 `viewBox` 沿短边补成正方形（信封本来就贴着四边，补齐只加边距、不裁内容），再按宽度渲染（`rsvg-convert -w`）。这样既不会拉伸图形，图标也能填满方形 cell。母版保持原样即可。

托盘图标只有两态：没有未读时封蜡圆点为灰色（`icon-gray.svg`），有未读时变红（`icon-red.svg`），两者圆点大小一致。顶栏品牌 mark 取红点母版，内嵌 32–128px 多个尺寸，按设备像素选择刚好覆盖的一张（`internal/ui/appicon.go`）；mark 固定 36dp，所以 1x / 1.25x / 1.5x / 2x / 3x 下设备像素正好是 36 / 45 / 54 / 72 / 108px，缩放比例为 1，不会被线性滤波插值而发糊（旧的 32/40/48/64/96/128px 变体保留，用于 1.1x、1.75x 这类零散比例）。

## 15. 目录结构

```
cmd/yauzin/main.go             入口：只有一行 uihost.Main(os.Args[1:])
internal/mail/mail.go          领域模型（会话摘要 / 分页）与 Service 接口
internal/mail/mock/            内存 mock 后端（140ms 模拟延迟，按会话聚合）
internal/mail/gmail/           Gmail 后端：OAuth（PKCE 回环）、REST 客户端、请求日志 transport、MIME 解析、写操作（mutate.go：标记/归档/垃圾邮件/删除）、发信（send.go：messages.send 的 media 上传）、Discovery 生成的 API 类型
internal/mail/rfc822/          Draft → RFC 5322 报文（头编码、折行、附件 multipart、可选 Bcc 头）；Prepare 定长、WriteTo 流式，Gmail 与 SMTP 共用
internal/mail/smtp/            SMTP 发信：三种加密、PLAIN/LOGIN、信封收件人（含 Bcc）、代理拨号
internal/netproxy/             裸 TCP 代理拨号：http/https CONNECT 与 socks5（与 Gmail 共用的地址校验）
internal/cmd/gmailgen/         Gmail Discovery 文档 → Go 类型生成器（go generate 入口）
internal/accounts/             accounts.json：OAuth 凭据（provider）、每账号 kind/令牌/useProxy、SMTP 发信条目（smtp 配置，无令牌）
internal/uihost/uihost.go      唯一 import gioui.org/app 的包：进程入口（app.ID / app.Main）、建窗与初始选项、事件循环、窗口动作、waylanddeco 副作用引入
internal/uihost/picker.go      把 gioui.org/x/explorer 适配成 ui.FilePicker（错误映射为 ui 的两个哨兵）
internal/ui/driver.go          窗口层端口：Driver / Window / WindowConfig / WindowActions / FilePicker 与文件选择器错误
internal/ui/app.go             状态与构造：App 字段、构造器、异步原语
internal/ui/app_lifecycle.go   生命周期循环（Run + attach/detach + 帧与配置回调）、post 队列、临时目录与 Ctrl+C 清理
internal/ui/app_data.go        账号、文件夹、分页与加载（含先检查再加载）
internal/ui/app_poll.go        1 分钟轮询入口（进程生命周期，检查走 app_check.go）
internal/ui/app_thread.go      会话打开与批量动作
internal/ui/app_overlay.go     帧骨架、指针区域与浮层编排
internal/ui/app_settings.go    设置读写与语言/性能/日志级别/代理（全局与单账号）、账号删除
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
internal/ui/reader_find.go     阅读器正文查找：匹配内核、查找条、命中高亮几何与两段式跳转
internal/ui/messagecard.go     可折叠消息卡片
internal/ui/textselection.go   阅读器可选文字：选区状态、右键区域与选区菜单（复制 / 打开链接）
internal/ui/compose_view.go    写信浮层布局（含发件人就地展开、密送行与只读表头详情）
internal/ui/compose_actions.go 发送（按发件人分派到后端或 SMTP）、附件路径列表的增删
internal/ui/settings.go        设置浮层（通用/账号/快捷键，含网络代理、日志级别与账号手风琴：展开后是浅灰子卡片——代理开关 + 描边按钮操作，删除在同一行原地确认；账号卡下方是发信账号分组）
internal/ui/smtp_account.go    SMTP 发信账号：新增/编辑表单浮层、设置页分组行与其展开块
internal/ui/account_add.go     添加账号 / 重新登录浮层（类型选择含 SMTP 发信账号、授权链接复制与打开）
internal/ui/menu.go            下拉菜单
internal/ui/status.go          轻量状态条
internal/ui/tasks.go           侧栏任务列表分栏（附件保存与邮件发送的进度、结果与常驻历史）
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
internal/ui/format.go          时间、地址、主题与回复/转发引用块排版
internal/ui/filepicker.go      xdg-desktop-portal 文件选择；发送附件只取原路径不复制，附件与整封邮件下载都先选位置再流式写入
internal/ui/openurl.go         跨平台打开链接/文件
internal/ui/htmlfile.go        HTML 临时文件与 CSP 注入
internal/env/                  YAUZIN_* 环境变量的唯一读取点（配置目录 / pprof 端口 / 开发模式）
internal/prefs/                设置持久化
internal/logging/              进程级 slog level var 与控制台 text handler（stderr）
internal/cursor/               系统光标主题解析（门户 / kreadconfig6 / gsettings）
internal/i18n/                 中英文翻译与系统 locale
internal/tray/                 系统托盘（自实现 StatusNotifierItem + DBusMenu）
internal/waylanddeco/          Wayland 客户端装饰（链接期包装 wl_proxy_add_listener 补发 set_mode）
internal/singleinstance/       D-Bus 单实例（会话总线名 me.zhsj.yauzin）
internal/mailto/               mailto URI 解析（收件人、抄送、密送、主题、正文、in-reply-to）
artwork/                       图标设计母版（SVG）、512px 桌面图标与截图，均不进二进制
scripts/gen_icon_fonts.py      Material Symbols 子集字体生成（fonttools）
scripts/gen_app_icons.py       应用图标 SVG → PNG 生成（rsvg-convert）
```

## 16. 记录约定

- 本文只沉淀**当前仍然成立**的结论：计划完成或被后续方案取代后，把有效结论合并到这里，再由人工定期清理过期计划。
- 尚未实施的事项（含暂缓项与恢复条件）记在 [PLAN.md](PLAN.md)；完成一项就把结论并入本文并删除该条目。
