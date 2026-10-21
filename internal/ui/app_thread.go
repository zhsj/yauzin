package ui

import (
	"context"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

func (a *App) resetSelection() {
	a.selectedThread = ""
	a.thread = mail.ThreadSummary{}
	a.msgs = nil
	a.expanded = map[string]bool{}
	a.msgGen++
	cancelRequest(&a.msgCancel)
	a.loadingThread = false
}

func (a *App) openThread(sum mail.ThreadSummary) {
	if sum.ID == "" || a.selectedThread == sum.ID {
		return
	}
	// Opening a conversation moves the keyboard focus out of the search
	// field, so Esc and the list shortcuts work on the way back.
	a.blurRequested = true
	a.selectedThread = sum.ID
	a.thread = sum
	a.msgs = nil
	a.expanded = map[string]bool{}
	a.loadingThread = true
	a.reader.reset()

	svc := a.service()
	if svc == nil {
		a.closeThread()
		return
	}

	cancelRequest(&a.msgCancel)
	a.msgGen++
	gen, threadID := a.msgGen, sum.ID
	var msgs []*mail.Message
	a.msgCancel = a.async("get thread", timeoutThread,
		func() bool { return gen == a.msgGen && a.selectedThread == threadID },
		func(ctx context.Context) error {
			var err error
			msgs, err = svc.GetThread(ctx, threadID)
			return err
		},
		func() {
			a.loadingThread = false
			a.msgs = msgs
			a.expanded = make(map[string]bool, len(msgs))
			if len(msgs) > 0 {
				a.expanded[msgs[len(msgs)-1].ID] = true
			}
		},
		func(err error) {
			a.loadingThread = false
			a.closeThread()
			a.notify(errorText(err), true)
		})

	if sum.UnreadCount > 0 {
		a.markThreadRead(sum, true)
	}
}

func (a *App) closeThread() {
	if a.selectedThread == "" {
		return
	}
	a.selectedThread = ""
	a.thread = mail.ThreadSummary{}
	a.msgs = nil
	a.expanded = map[string]bool{}
	a.loadingThread = false
	a.msgGen++
	cancelRequest(&a.msgCancel)
	a.reader.reset()
}

func (a *App) markThreadRead(sum mail.ThreadSummary, read bool) {
	if len(sum.MessageIDs) == 0 {
		return
	}
	unread := 0
	if !read {
		unread = len(sum.MessageIDs)
	}
	// The list updates before the backend answers; a failure has to put the
	// previous count back, or the unread badge would stay wrong until the next
	// reload.
	prev := sum.UnreadCount
	setUnread := func(n int) {
		a.applyThread(sum.ID, func(s *mail.ThreadSummary) { s.UnreadCount = n })
	}
	setUnread(unread)

	svc := a.service()
	if svc == nil {
		setUnread(prev)
		a.notify(i18n.T("toast.accountUnavailable"), true)
		return
	}
	ids := append([]string{}, sum.MessageIDs...)
	a.async("set read", timeoutMutation, nil,
		func(ctx context.Context) error { return svc.SetFlag(ctx, ids, mail.FlagRead, read) },
		func() { a.reloadFolders() },
		func(err error) {
			setUnread(prev)
			a.notify(i18n.T("toast.markReadFailed", map[string]string{"Error": errorText(err)}), true)
		})
}

func (a *App) toggleThreadRead() {
	if a.thread.ID == "" {
		return
	}
	a.markThreadRead(a.thread, a.thread.UnreadCount > 0)
}

func (a *App) toggleThreadStar(id string) {
	sum, ok := a.list.thread(id)
	if !ok {
		sum = a.thread
	}
	if sum.ID == "" {
		return
	}
	starred := !sum.Starred
	setStar := func(on bool) {
		a.applyThread(sum.ID, func(s *mail.ThreadSummary) { s.Starred = on })
	}
	setStar(starred)

	svc := a.service()
	if svc == nil {
		return
	}
	ids := append([]string{}, sum.MessageIDs...)
	a.async("set star", timeoutMutation, nil,
		func(ctx context.Context) error { return svc.SetFlag(ctx, ids, mail.FlagStarred, starred) },
		func() {
			a.reloadFolders()
			if a.activeFolder == "starred" {
				a.refreshThreads()
			}
		},
		func(err error) {
			setStar(!starred)
			a.notify(i18n.T("toast.starFailed"), true)
		})
}

// applyThread writes an optimistic summary change to both the list row and the
// open conversation, so an update and its rollback cannot diverge.
func (a *App) applyThread(id string, set func(*mail.ThreadSummary)) {
	a.list.mutateThread(id, set)
	if a.thread.ID == id {
		set(&a.thread)
	}
}

// idsOp is a backend operation on message IDs. The service is resolved on the
// UI goroutine and handed in: reading a.service() inside the worker would race
// with switchAccount and closeServices.
type idsOp func(context.Context, mail.Service, []string) error

func (a *App) archiveThread() {
	a.mutateThread("archive", i18n.T("toast.archived"),
		func(ctx context.Context, svc mail.Service, ids []string) error { return svc.Archive(ctx, ids) })
}

func (a *App) deleteThread() {
	a.mutateThread("delete", i18n.T("toast.trashed"),
		func(ctx context.Context, svc mail.Service, ids []string) error { return svc.Delete(ctx, ids) })
}

func (a *App) mutateThread(op, success string, fn idsOp) {
	svc := a.service()
	if svc == nil {
		a.notify(i18n.T("toast.accountUnavailable"), true)
		return
	}
	if a.thread.ID == "" || len(a.thread.MessageIDs) == 0 {
		a.notify(i18n.T("toast.noThread"), true)
		return
	}
	ids := append([]string{}, a.thread.MessageIDs...)
	// Snapshot the conversation: it may change while the backend works, and
	// the answer must not touch the new one.
	threadID := a.thread.ID
	a.submitIDs(op, svc, ids, fn, func() {
		a.notify(success, false)
		// Close only the conversation the action started from; the user may
		// have opened another one meanwhile.
		if a.thread.ID == threadID {
			a.closeThread()
		}
		a.refreshThreads()
		a.reloadFolders()
	})
}

func (a *App) mutateIDs(op, success string, ids []string, fn idsOp) {
	svc := a.service()
	if svc == nil {
		a.notify(i18n.T("toast.accountUnavailable"), true)
		return
	}
	if len(ids) == 0 {
		a.notify(i18n.T("toast.nothingToDo"), true)
		return
	}
	a.submitIDs(op, svc, ids, fn, func() {
		a.notify(success, false)
		a.list.clearSelection()
		a.refreshThreads()
		a.reloadFolders()
	})
}

// submitIDs runs fn on the worker and drops its answer when the account
// changed meanwhile: the summary it would refresh belongs to another mailbox.
// svc is passed in because the caller resolves it on the UI goroutine.
func (a *App) submitIDs(op string, svc mail.Service, ids []string, fn idsOp, done func()) {
	accID := a.activeAcc
	a.async(op, timeoutMutation,
		func() bool { return accID == a.activeAcc },
		func(ctx context.Context) error { return fn(ctx, svc, ids) },
		done, nil)
}

func (a *App) archiveThreadByID(id string) {
	a.mutateIDs("archive", i18n.T("toast.archived"), a.list.messageIDs([]string{id}),
		func(ctx context.Context, svc mail.Service, ids []string) error { return svc.Archive(ctx, ids) })
}

func (a *App) deleteThreadByID(id string) {
	a.mutateIDs("delete", i18n.T("toast.trashed"), a.list.messageIDs([]string{id}),
		func(ctx context.Context, svc mail.Service, ids []string) error { return svc.Delete(ctx, ids) })
}

// toggleThreadReadByID flips the read state of one row. markThreadRead takes
// the target state, so an unread conversation turns read and a read one turns
// unread, matching toggleThreadRead in the reader.
func (a *App) toggleThreadReadByID(id string) {
	sum, ok := a.list.thread(id)
	if !ok {
		return
	}
	a.markThreadRead(sum, sum.UnreadCount > 0)
}

func (a *App) bulkIDs() []string {
	return a.list.messageIDs(a.list.selectedIDs())
}

func (a *App) bulkArchive() {
	a.mutateIDs("archive", i18n.T("toast.archived"), a.bulkIDs(),
		func(ctx context.Context, svc mail.Service, ids []string) error { return svc.Archive(ctx, ids) })
}

func (a *App) bulkDelete() {
	a.mutateIDs("delete", i18n.T("toast.trashed"), a.bulkIDs(),
		func(ctx context.Context, svc mail.Service, ids []string) error { return svc.Delete(ctx, ids) })
}

func (a *App) bulkSetRead(read bool) {
	success := i18n.T("toast.markedRead")
	if !read {
		success = i18n.T("toast.markedUnread")
	}
	a.mutateIDs("set read", success, a.bulkIDs(),
		func(ctx context.Context, svc mail.Service, ids []string) error {
			return svc.SetFlag(ctx, ids, mail.FlagRead, read)
		})
}

func (a *App) markPageRead(read bool) {
	success := i18n.T("toast.pageMarkedRead")
	if !read {
		success = i18n.T("toast.pageMarkedUnread")
	}
	a.mutateIDs("mark page read", success, a.list.pageMessageIDs(),
		func(ctx context.Context, svc mail.Service, ids []string) error {
			return svc.SetFlag(ctx, ids, mail.FlagRead, read)
		})
}
