# Gmail API 用量限制（quota）

本文只记录 Gmail API 的配额规则与单位成本，作为 `internal/mail/gmail` 请求与缓存设计的依据。数据来源：官方
<https://developers.google.com/workspace/gmail/api/reference/quota>（2026-10-30 抓取，页面存档见仓库外的 `quota`
HTML 文件）。页面本身写明：**2026-05-01 起该 API 的用量限制被更新**；在 2025-11 至 2026-04 之间用过该 API 的
Cloud 项目继续沿用旧配额，2026-05-01 及之后新建的项目适用新配额。因此不同项目在控制台看到的数值可能与本表不同，
以控制台「配额与系统限制」页为准。

## 1. 速率限制

| 限制类型 | 上限 |
| --- | --- |
| Per minute per project（每个 Cloud 项目每分钟） | 1,200,000 quota units |
| Per minute per user per project（每个项目下每个用户每分钟） | **6,000 quota units** |

绑定的瓶颈是第二条：用户自带的 OAuth 客户端就是本项目，**一个账号每分钟只有 6,000 单位**；第一条只约束整个项目
下所有账号加起来不超过 1,200,000。yauzin 每个账号使用各自的令牌，同一客户端下的多个账号共享项目额度、各自独立
计 6,000。

## 2. 每日阈值

| 阈值类型 | 上限 |
| --- | --- |
| Per day per project（每个项目每 24 小时） | 80,000,000 quota units |

这是**计费阈值**而不是硬性限额：24 小时内用满之前不产生额外费用，也不会被计费；该阈值无法申请提高。Google 表示
2026 年稍晚会公布计费细节，生效前至少提前 90 天通知。超速率限制本身目前仍是报错（见第 6 节），2026 年稍晚起计划
开始收费。

## 3. 每个方法的配额单位

| 方法 | 单位 |
| --- | --- |
| drafts.create | 10 |
| drafts.delete | 10 |
| drafts.get | 20（format 为 full / raw / html 时 60） |
| drafts.list | 5 |
| drafts.send | 100 |
| drafts.update | 15 |
| getProfile | 1 |
| history.list | 2 |
| labels.create | 5 |
| labels.delete | 5 |
| labels.get | 1 |
| labels.list | 1 |
| labels.update | 5 |
| messages.attachments.get | 20 |
| messages.batchDelete | 50 |
| messages.batchModify | 50 |
| messages.delete | 10 |
| messages.get | 20（format 为 full / raw / html 时 60） |
| messages.import | 25 |
| messages.insert | 25 |
| messages.list | 5 |
| messages.modify | 5 |
| messages.send | 100 |
| messages.trash | 20 |
| messages.untrash | 5 |
| settings.delegates.create | 100 |
| settings.delegates.delete | 5 |
| settings.delegates.get | 1 |
| settings.delegates.list | 1 |
| settings.filters.create | 5 |
| settings.filters.delete | 5 |
| settings.filters.get | 1 |
| settings.filters.list | 1 |
| settings.forwardingAddresses.create | 100 |
| settings.forwardingAddresses.delete | 5 |
| settings.forwardingAddresses.get | 1 |
| settings.forwardingAddresses.list | 1 |
| settings.getAutoForwarding | 1 |
| settings.getImap | 1 |
| settings.getPop | 1 |
| settings.getVacation | 1 |
| settings.sendAs.create | 100 |
| settings.sendAs.delete | 5 |
| settings.sendAs.get | 1 |
| settings.sendAs.list | 1 |
| settings.sendAs.update | 100 |
| settings.sendAs.verify | 100 |
| settings.updateAutoForwarding | 5 |
| settings.updateImap | 5 |
| settings.updatePop | 100 |
| settings.updateVacation | 5 |
| stop | 50 |
| threads.delete | 20 |
| threads.get | **40**（format 为 full 时 **120**） |
| threads.list | **10** |
| threads.modify | 10 |
| threads.trash | 20 |
| threads.untrash | 10 |
| watch | 100 |

单位是「每次方法调用」的量，与响应体大小无关（`format` 例外，见上表的括号）。`batch` 请求**不打折**：每个内嵌调用
仍按它自己的方法计费，batch 只省 HTTP 往返。上表未列出的方法（如 `users.*` 下的其它方法）以官方页面为准。

## 4. 其它限制

- 一个邮件消息最多 **500 个收件人**（To + Cc + Bcc）。
- 服务账号发出的调用在配额统计上视为**同一个用户**。
- 速率类错误建议用截断指数退避重试：第 n 次等待
  `min(2^n + random(0..1000ms), max_backoff)`，`max_backoff` 常见取 32 或 64 秒；退避上限之后可保持固定间隔重试，
  但要有总次数上限。
- 速率限制可以在 Cloud 控制台「配额与系统限制」页申请提高，不保证批准；`Per day per project` 的阈值不能提高。
- 页面上另有一节 Gmail MCP server 的 query cost（`gmailmcp.googleapis.com`，`get_thread` 40、`search_threads` 10
  等），本项目不使用 MCP，与上面的 REST 配额无关。

## 5. 错误表现

超限返回 `403`（`status: RESOURCE_EXHAUSTED`，`errors[].reason: rateLimitExceeded`，消息形如
`User-rate limit exceeded`）或 `429`。yauzin 的 `client.apiError` 把 `403 insufficientPermissions` 归为
`ErrUnauthorized`、把速率类 403 / 429 归为 `ErrRateLimited`（界面显示「请求过于频繁，请稍后重试」），其余 403
（账号被停用等）保持未分类并把服务端消息带进 `detail`。

错误里的 `window_start_time` 说明限额是按**固定 60 秒窗口**计的（两次错误的该字段正好相差 60 秒），窗口内累计
6,000 单位即拒。因此客户端必须按「最近 60 秒已花」计数，不能用「容量 + 按秒补充」的令牌桶：后者在窗口开头花光容量、
中途又补满再花一次，一个固定窗口内最多能花近两倍额度。这正是早期版本在日志显示 3,728/5,400 时就收到 403 的原因。

`batch` 内嵌调用的错误同样是完整响应，yauzin 用 `apiErrorFor` 按同一张表分类（并带上会话/标签 ID 便于定位），
所以批次里某个 `threads.get` 被限流时也能识别、退避重试，而不是把整页判为失败。

## 6. yauzin 的成本

按 `internal/mail/gmail` 的实现换算（`perPage = 25`，`batchLimit = 25`，`modifyBatchSize = 1000`）：

| 用户动作 | 实际请求 | 单位 |
| --- | --- | --- |
| 加载侧栏 | `labels.list` + 每个标签一次 `labels.get` | 1 + L |
| 打开文件夹 / 翻页 / 搜索一页（N 行全部未缓存） | `threads.list` + N 次 `threads.get?format=metadata` | 10 + 40N |
| 同上，一页 25 行冷加载 | | **1,010** |
| 再次打开同一页（页与行都命中） | 无 | **0** |
| 页序失效但行还在（收到新邮件后刷新） | `threads.list` + 变化会话数 × `threads.get` | 10 + 40k |
| 变更检查（每分钟轮询、启动、切账号） | `users.getProfile?fields=historyId` | 1 |
| 检查发现变化 | 追加 `users.history.list`（2/页，最多 5 页） | 2–10 |
| 打开一个会话（多封） | `threads.get?format=full` | **120** |
| 打开一个单封会话 | `messages.get?format=full` | 60 |
| 再次打开同一会话 | 无（正文缓存最近 8 个） | **0** |
| 下载 `.eml` 原文 | `messages.get?format=raw` | 60 |
| 下载一个附件 | `messages.attachments.get` | 20 |
| 已读 / 星标单封 | `messages.modify` | 5 |
| 已读 / 星标单会话多封 | `threads.modify` | 10 |
| 已读 / 星标整页 | 一次 `messages.batchModify` | 50 |
| 归档 / 垃圾邮件整页 | `messages.batchModify` + 页重建 | 50 + 10 |
| 删除单会话多封 | `threads.trash` | 20 |
| 删除整页（逐封 trash） | `messages.trash` 子请求 | 20 × 邮件数 |
| 发信 | `messages.send`（含 resumable 上传） | 100 |

调度：`client` 在每次请求前按上表单价向本账号的**60 秒滑动窗口**（6,000/分钟 × 0.95 = 5,700，按秒分槽记录）预留额度。
窗口算的是「最近 60 秒已经花了多少」：只要这笔花费加进去不超过 5,700 就立即放行，否则等到最老的花费滑出窗口再发。
这对应服务端的固定 60 秒窗口——任意 60 秒内不超过 5,700，任何一个 6,000 的固定窗口都不会被撑破；令牌桶（容量 5,400、按秒补充）
做不到这一点，它允许在一个固定窗口内先花光容量、中途补满后再花一次，这正是 403 的来源。前台请求额度不足时等待（尊重 ctx），
`getProfile` / `history.list` 这类后台请求最多等 2 秒便放弃本次、由下一轮补上。
等待本身也要装得进请求自己的时限：累计已等待加上本次预计等待一旦达到 ctx 剩余时间（列表与会话是 30 秒、文件夹 15 秒），
就不再空等，立即返回 `mail.ErrQuotaWait`（`QuotaWaitError.Wait` 是窗口还需滑动的时长），界面提示「配额不足，请 N 秒后重试。」。
睡满已没有时间发请求，早失败反而省掉一次必然超时的往返。

rate limit（403 `rateLimitExceeded` / 429）与 5xx 按 1s / 2s / 4s 加抖动退避重试，最多 3 次。batch 内嵌调用按**单个子请求**重试：
成功的不重发，失败的下一轮单独组批（重新计价与预留额度），因此一次限流只补那几行，不会把整页作废、也不会重复支付已成功的 40 单位。

每次请求的 DEBUG 日志行（`gmail request` / `gmail batch`）都带 `units`（本次调用或整批的花费）、`quota`（预留完成后最近 60 秒
已花/窗口额度，满值 5,700）、`wait`（为等额度花的时间，0 也记），一条日志即可回答「这次请求贵不贵、这一分钟还剩多少」；
额度不足、请求真要等待时，在开始等待前先记一条 INFO `gmail quota wait`（`account`、`op`、`units`、等待前的 `quota`、
预计 `wait`），默认 info 级别即可看到「这次为什么慢」，一次等待只记一条，不改额度算法本身；
后台被额度丢弃另记 `gmail quota skip`，批次内子调用重试另记 `gmail batch retry`；
因等不起而放弃的请求没有等待发生，因此不记 `gmail quota wait`，失败由调用方记一条 ERROR。

换算结论（6,000 单位/分钟/用户）：

- 冷启动首屏（侧栏 + 一页 25 行）约 1,020 单位，占一分钟额度的 17%，仍是最贵的一次操作。
- 一页之后，同一批会话不再重复付 `threads.get`：翻回上一页、切到星标、搜索命中同一批会话，只付 10 单位的 `threads.list`，页命中则是 0。
- 收到 1 封新邮件后刷新 = 1（检查）+ 2（history）+ 10（list）+ 40（那一行）= **53**，而不是 1,010。
- 打开会话是最贵的单次读（多封 120、单封 60），所以正文按会话缓存最近 8 个。
- 删除仍是写路径里最贵的一项（20/邮件），归档一类整页只有 50。

## 7. 未采纳的替代路径

只记录数据与结论，供以后重估：

- 消息级读取（`messages.list` 5 + 每封 `messages.get?format=metadata` 20）对平均 1.3 封/会话的收件箱只比 25×40 省约 35%，
  但 `ThreadSummary.MessageIDs` 是写操作的必要输入（必须拿全整段会话），且分页语义会从「会话」退化成「消息」、跨页会话被拆开，
  与 `mail.ThreadPage` 的契约冲突。**未采纳**。
- 会话级写操作已按量使用：`threads.modify` 10/会话用于单会话多封，`messages.batchModify` 50/次用于整页；
  删除单会话多封用 `threads.trash` 20/会话，逐封 `messages.trash` 20/封。
- `threads.get?format=full`（120）与 `messages.get?format=full`（60）相差一倍：阅读器对单封会话改用后者，多封仍用前者
  （2 封时等价，3 封起后者更贵）。
- `watch`（100）+ Pub/Sub 推送可以让检查变成事件驱动，但桌面端没有公网回调地址。**未采纳**。
