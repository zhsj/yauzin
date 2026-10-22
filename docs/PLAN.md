# 未实施事项计划

本文件只记录**尚未实施**的改进项：已完成的内容见 git 历史与 [DESIGN](DESIGN.md)，被用户暂缓的条目单列在第 1 节。每完成一项，把仍然成立的结论并入 `DESIGN.md`，并从本文件删除该条目（约定见 DESIGN §16）。

## 0. 状态基线

已完成：

- 后端调用统一走 `App.async`，列表/文件夹/会话请求可取消，回调按账号与会话快照校验。
- 文件选择器按值传给 worker；单实例激活回调改为原子指针。
- 键盘导航（列表光标、快捷键、单一按键路由）与焦点移交。
- 变更后保持列表滚动位置；搜索框清除按钮；文档与错误映射测试。
- 状态条通知只保留最后一条（`show`/`clear` 停掉旧消退定时器）；`post` 的补帧重试共用单个槽位。
- 后端装配点收敛到 `appOptions`，`newApp()` 的零配置默认仍是 demo + mock。
- `mail.Service` 去掉未使用的单封 `Get`，正文只按会话读取。
- Gmail 只读接入：`internal/accounts` 保存 OAuth 凭据与令牌，`internal/mail/gmail` 实现文件夹/会话列表/会话正文（PKCE 回环授权），其余方法返回 `ErrUnsupported`。
- 通用代理设置：`settings.json` 的 `proxy`，账号条目只记 `useProxy`；保存后按新代理重建对应后端。

当前验证基线：`gofmt` 与 `goimports -local github.com/zhsj/yauzin` 均干净、纯 Go 包 `go vet`/`go test` 通过、容器内 `go build ./cmd/yauzin` 与 `go test ./...` 通过。

## 1. 暂缓项（用户指定，暂不实施）

| 项目 | 原主张 | 恢复条件 | 影响面 |
| --- | --- | --- | --- |
| HTML-only 正文纯文本降级 | 用现有 `internal/htmltext.ToText` 在阅读器内联显示可选中文本，保留「在浏览器打开」 | 重新确认阅读器呈现范围，并把结论写回 DESIGN §8 | `internal/ui/reader.go:396`、`internal/htmltext/` |
| 附件大小上限 | 复制到暂存目录前按体积拦截（对齐主流邮箱上限），chip 显示大小 | 真实后端接入、明确单封上限后一并设计 | `internal/ui/filepicker.go:61`、`internal/mail` |
| CI 与 `-race` | `.github/workflows` + Makefile + 容器内 `go test -race` | 需要稳定的 CI 运行环境时恢复 | 仓库根（当前无 `.github/`、无 Makefile）、`Dockerfile.build` |

暂缓项不设优先级；恢复时按各自“影响面”重新评估，不要直接照搬旧结论。

## 2. 完成定义

每个条目收尾都要满足：

1. `gofmt -l internal/ cmd/` 与 `~/go/bin/goimports -local github.com/zhsj/yauzin -l internal/ cmd/` 均无输出；纯 Go 包 `go vet`/`go test` 通过；改到 Gio 目标时在容器内跑 `go vet ./... && go test ./...` 与 `go build -o /tmp/yauzin ./cmd/yauzin`。
2. 行为变化有测试或明确的手工验证步骤（GUI 相关需在宿主桌面会话验证，不在容器里启动）。
3. 按 Conventional Commits 提交，一个条目一个提交。
4. 完成后把仍然成立的结论并入 `DESIGN.md`，删除本文件中的对应条目；暂缓项恢复或否决时同样处理。
