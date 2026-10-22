# Yauzin

**English** | [简体中文](README.zh-CN.md)

A desktop mail client built with Go + [Gio](https://gioui.org), its UI modelled on the Gmail web client. **Current stage: UI + mock backend** — every message comes from an in-memory demo account, so nothing goes over the network and no credentials are needed.

## Features

- Two-pane layout: a sidebar (Inbox, Starred, Sent, Trash and labels) beside one content card that switches between the list and the conversation view; the top bar holds the sidebar toggle, search, Settings and the account menu.
- Conversation list: one row per conversation (participants, subject, snippet, time, star, unread bolding), 50 per page; hover actions for Archive / Delete / Mark as read, tri-state selection and bulk actions in the toolbar.
- Reading: the thread ordered by time with collapsible messages; plain-text bodies, HTML opened in the browser; Star / Mark read / Archive / Delete act on the whole conversation.
- Compose: new message, Reply and Forward with Cc, attachments and status feedback; minimisable to the title bar.
- Keyboard: J/K, Enter, X, E, Delete, S, U, R, F, C, / and Ctrl+Enter, plus Ctrl+W to close the window and Ctrl+Q to quit; the full table lives in Settings → Shortcuts.
- Interface language: Chinese or English, following the system and switchable in Settings, applied instantly and saved to the settings file.

## Settings file

`os.UserConfigDir()/yauzin/settings.json` (0600, atomic writes); set `YAUZIN_CONFIG_DIR` to use another directory.

## Known limitations

- Accounts and mail are not persisted, so a restart returns to the demo data; drafts, a unified inbox, notifications and background sync are not implemented.
- Bodies render as `text/plain` only, links are not clickable, inline images are placeholders and attachments are mock placeholder files.

Architecture and build steps live in [docs/DESIGN.md](docs/DESIGN.md); pending work is tracked in [docs/PLAN.md](docs/PLAN.md).
