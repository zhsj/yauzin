# Yauzin（邮笺）

[English](README.md) | **简体中文**

用 Go + [Gio](https://gioui.org) 编写的 Linux 桌面邮件客户端。

![Yauzin](artwork/screenshot.png)

## 亮点

- **原生 Gmail**：标签、归档和搜索走 Gmail API，不靠 IMAP。
- **替代 Send As**：Gmail 将于 2027 年 1 月[下线 Send As](https://support.google.com/mail/answer/17101213)。

## 安装

```bash
flatpak remote-add --if-not-exists --user --no-gpg-verify \
  yauzin oci+https://zhsj.github.io/yauzin
flatpak install --user yauzin me.zhsj.yauzin
```

## 注意

- 需自备启用 Gmail API 的 Google Cloud OAuth 客户端。

## 协议

[MIT](LICENSE)
