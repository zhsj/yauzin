# 未实施事项计划

本文件只记录**尚未实施**的改进项：已完成的内容见 git 历史与 [DESIGN](DESIGN.md)，被用户暂缓的条目单列在第 1 节。每完成一项，把仍然成立的结论并入 `DESIGN.md`，并从本文件删除该条目（约定见 DESIGN §16）。

## 0. 状态基线

已完成：

- 后端调用统一走 `App.async`，列表/文件夹/会话请求可取消，回调按账号与会话快照校验。
- 文件选择器按值传给 worker；单实例激活回调改为原子指针。
- 键盘导航（列表光标、快捷键、单一按键路由）与焦点移交。
- 变更后保持列表滚动位置；搜索框清除按钮；文档与错误映射测试。
- 状态条通知只保留最后一条（`show`/`clear` 停掉旧消退定时器）；`post` 的补帧重试共用单个槽位。
- 后端装配点收敛到 `appOptions`；演示账号由 `YAUZIN_DEV` 控制，发布构建默认没有账号。
- `mail.Service` 去掉未使用的单封 `Get`，正文只按会话读取。
- Gmail 接入：`internal/accounts` 保存 OAuth 凭据与令牌，`internal/mail/gmail` 实现文件夹/会话列表/会话全局搜索（独立缓存）/会话正文（PKCE 回环授权）、写操作（标记/归档/删除/发信）与附件下载（附件只有 blob 一种来源：附件接口流式解码、边读边写入调用方给定的目标 writer，带进度与失败原因的 DEBUG 日志），`mail.Service` 全量实现。
- 侧栏底部固定「任务列表」分栏：附件保存先选位置再下载，进度、完成路径、取消与失败原因常驻内存（重启清空），无任务时不占位，最多显示 3 行；邮件发送同样进入该分栏（主题为标题，成功/失败结果常驻），每行可删除，运行中的任务删除即取消。
- 通用代理设置：`settings.json` 的 `proxy`，账号条目只记 `useProxy`；保存后按新代理重建对应后端。
- Gmail API 类型改由 Discovery 文档生成（`internal/cmd/gmailgen` + `//go:generate`），`api.go` 只剩领域映射辅助方法。
- 托盘与单实例：`internal/tray` 的未读红点、关窗驻留，`internal/singleinstance` 经会话总线唤起已有窗口。
- 进程级 1 分钟变更检查（`internal/ui/app_poll.go`）：`internal/inboxwatch` 维护未读快照，`internal/notify` 发送 D-Bus 桌面通知；只更新托盘与通知，不刷新窗口。
- 顶栏跨全部文件夹搜索，与文件夹浏览共用游标分页、代际校验与同一个列表请求槽位。
- Gmail 读缓存（LRU）与 historyId 基线，全量加载统一「先检查再加载」。
- 阅读器「更多」菜单：安全 / 完整 HTML 预览在系统浏览器打开，原文可在同一菜单下载为 `.eml`。
- 设置扩充：性能监视器、日志级别、代理（全局与按账号）、账号手风琴（重新登录 / 删除）与 `activeAccount` 记忆。
- 系统字体 + emoji 回退、CSD 判定与系统光标主题解析（`internal/cursor`）。
- 发信：Gmail `messages.send`（media 上传）与 SMTP 发信账号（`kind=smtp`，三种加密、代理、默认密送回 Gmail），写信浮层的发件人可在邮箱与各发信账号之间切换。

当前验证基线：`gofmt` 与 `goimports -local github.com/zhsj/yauzin` 均干净、纯 Go 包 `go test -vet=all` 通过、容器内 `go build ./cmd/yauzin` 与 `go test -vet=all ./...` 通过。

## 1. 暂缓项（用户指定，暂不实施）

| 项目 | 原主张 | 恢复条件 | 影响面 |
| --- | --- | --- | --- |
| 附件大小上限 | 不设客户端限制；Gmail 发送走 resumable 分块流式上传，超出服务端上限时由 Gmail 报错，附件直接读原文件、不复制暂存 | 需要在发送前快速失败或按账号展示上限时再评估 | `internal/mail/gmail/upload.go`、`internal/mail/rfc822`、`internal/ui/filepicker.go` |
| CI 与 `-race` | `.github/workflows` + Makefile + 容器内 `go test -race` | 需要稳定的 CI 运行环境时恢复 | 仓库根（当前无 `.github/`、无 Makefile）、`Dockerfile.build` |

暂缓项不设优先级；恢复时按各自“影响面”重新评估，不要直接照搬旧结论。

## 2. 完成定义

每个条目收尾都要满足：

1. `gofmt -l internal/ cmd/` 与 `~/go/bin/goimports -local github.com/zhsj/yauzin -l internal/ cmd/` 均无输出；纯 Go 包 `go test -vet=all` 通过；改到 Gio 目标时在容器内跑 `go test -vet=all ./...` 与 `go build -o /tmp/yauzin ./cmd/yauzin`。
2. 行为变化有测试或明确的手工验证步骤（GUI 相关需在宿主桌面会话验证，不在容器里启动）。
3. 按 Conventional Commits 提交，一个条目一个提交。
4. 完成后把仍然成立的结论并入 `DESIGN.md`，删除本文件中的对应条目；暂缓项恢复或否决时同样处理。
