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
`User-rate limit exceeded`）或 `429`。yauzin 的 `client.apiError` 目前只把 `403 insufficientPermissions` 归为
`ErrUnauthorized`，速率错误保持未分类并把服务端消息带进 `detail`，因此界面会原样显示这句英文。

## 6. yauzin 当前的成本

按 `internal/mail/gmail` 现有实现换算（`perPage = 25`，`batchLimit = 25`，`modifyBatchSize = 1000`）：

| 用户动作 | 实际请求 | 单位 |
| --- | --- | --- |
| 加载侧栏 | `labels.list` + 每个标签一次 `labels.get` | 1 + L |
| 打开文件夹 / 翻页 / 搜索一页（N 行） | `threads.list` + N 次 `threads.get?format=metadata` | 10 + 40N |
| 同上，一页 25 行 | | **1,010** |
| 变更检查（每分钟轮询、启动、切账号） | `users.getProfile?fields=historyId` | 1 |
| 轮询发现变化后读未读收件箱 | `threads.list` + 3 次 `threads.get` | 130 |
| 打开一个会话 | `threads.get?format=full` | **120** |
| 下载 `.eml` 原文 | `messages.get?format=raw` | 60 |
| 下载一个附件 | `messages.attachments.get` | 20 |
| 已读 / 星标 / 归档 / 垃圾邮件 | 一次 `messages.batchModify`（≤1000 个邮件 ID） | 50 |
| 删除（移入垃圾箱） | 每个邮件一次 `messages.trash`，批量信封不减免 | 20 × 邮件数 |
| 发信 | `messages.send`（含 resumable 上传） | 100 |

换算结论（6,000 单位/分钟/用户）：

- 一次 25 行的整页加载占一分钟额度的 **16.8%**，冷加载最多约 **5.9 页/分钟**；考虑 `getProfile`、侧栏标签与打开
  会话，实际可用次数更低。连续切几个文件夹或每次写操作后的刷新就会打满，这就是 `User-rate limit exceeded` 的
  直接来源。
- 账单上最贵的单个动作是打开会话（120）与整页元数据（40/行）；`threads.get` 在整页成本里占 99%。
- 写操作便宜（归档一类整页一次 50），但 `Delete` 按邮件计 20，删一页 25 封=500，是写路径里最贵的一个。

## 7. 供后续设计参考的替代路径

只记录数据，不作为已决定的方案：

- 行级元数据若改用 `messages.list?fields=messages(id,threadId)`（5）+ 每封邮件 `messages.get?format=metadata`（20），
  一页 25 行约为 5 + 20×邮件数，比 25×40 低，但分页与「整段会话」语义需要重做，且搜索页会退化成按邮件而不是按会话。
- 会话级写操作：`threads.modify` 10/次（一次只能一个会话）对比 `messages.batchModify` 50/次（一次整页），整页操作
  用后者更省；`threads.trash` 20/会话对比 `messages.trash` 20/邮件，多邮件会话用前者更省。
- `threads.get` 的 `format=full`（120）与 `messages.get` 的 `format=full`（60）之间差一倍，阅读器若要按邮件取正文
  需要先确认调用面能拿到邮件 ID。
