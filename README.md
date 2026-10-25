# Yauzin

**English** | [简体中文](README.zh-CN.md)

A desktop mail client built with Go + [Gio](https://gioui.org), its UI modelled on the Gmail web client. It reads real Gmail mailboxes through the Gmail API and always ships a built-in demo account, so the app opens with something to show before any credentials are entered.

## Features

- Accounts: in Settings → Accounts, add several Gmail accounts with your own Google Cloud OAuth client (desktop app with the Gmail API enabled; authorization runs through PKCE with a one-off 127.0.0.1 callback), sign in again, delete an account, switch the active account from the top-bar menu and let the app reopen the last one next time. Each account can connect through the configured proxy or directly. The demo mailbox is always present.
- Two-pane layout: a 256dp sidebar (Inbox, Starred, Sent, Trash and labels with Gmail-style hierarchy, colours and folding) beside one content card that switches between the list and the conversation view; the top bar holds the sidebar toggle, search, Settings and the account menu.
- Search: the top bar searches every folder at once (spam and trash excluded) with the query passed to the backend, so Gmail syntax such as `from:`, `subject:` or `has:attachment` works; results keep the paging toolbar.
- Conversation list: one row per conversation (participants, subject, snippet, time, star, unread bolding, label chips), 25 per page with cursor-based paging; hover actions for Archive / Delete / Mark read or unread, a tri-state select-all box, and bulk archive / delete / mark read or unread plus "mark all as read" in the toolbar menu.
- Reading: the thread ordered by time with collapsible messages; bodies are plain text, and a per-message More menu opens a safe HTML preview (strict CSP), an unrestricted preview or the original RFC 822 source — all three in the system browser. Star / Mark read / Archive / Delete act on the whole conversation.
- Compose: new message, Reply and Forward with Cc, attachments and status feedback; minimisable to the title bar, Ctrl+Enter sends. Quotes follow Gmail and are always English (`On <time> <sender> wrote:` then `> ` lines, `---------- Forwarded message ----------` for forwards); replying to a selection quotes only the selected text.
- Tray and notifications: closing the window keeps the process in the notification area with an unread dot; the mailbox is checked once a minute and new unread conversations raise Linux desktop notifications (up to three subjects plus the unread count). A second launch asks the running instance over D-Bus to open its window instead of starting another one.
- Settings: interface language (Chinese or English, following the system by default), client-side title bar (Wayland), performance monitor, proxy, log level and account management (add, sign in again, delete, per-account proxy).
- Keyboard: /, C, Esc, E, Delete, S, U, J/K, Enter, X, R, F, Ctrl+W to close the window and Ctrl+Q to quit; the full table lives in Settings → Shortcuts.

## Configuration files

`os.UserConfigDir()/yauzin/` holds `settings.json` (preferences) and `accounts.json` (OAuth client credentials, tokens and per-account options). Both are written with mode 0600 through temp file + rename; set `YAUZIN_CONFIG_DIR` to use another directory for both.

## Known limitations

- Gmail accounts are read-only (`gmail.readonly`): starring, archiving, deleting, marking read/unread, sending and saving attachments are rejected with "This account does not support that action." Only the demo account performs them, in memory.
- The demo mailbox and anything sent from it are not persisted, so a restart returns to the demo data; drafts are not saved either.
- Inbox polling only maintains the tray badge and notifications: the open window is refreshed manually (toolbar refresh), by reopening it or by clicking a notification.
- Desktop notifications are Linux/D-Bus only. Without a notification area the tray is unavailable and closing the window quits the app.
- Bodies render as `text/plain` only; HTML is handed to the system browser, inline images are not shown and links inside plain text are not clickable.
- System labels other than Inbox / Starred / Sent / Trash (drafts, spam, important, categories) are not shown; there is no unified inbox, label management, IMAP/SMTP support or offline cache.
- The client-side title bar works on Wayland only (X11 always uses server-side decorations); on KDE a "No titlebar and frame" window rule has to be added first, and while it is in use the window cannot be resized by dragging its edges.

Architecture and build steps live in [docs/DESIGN.md](docs/DESIGN.md); pending work is tracked in [docs/PLAN.md](docs/PLAN.md).
