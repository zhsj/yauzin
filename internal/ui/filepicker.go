package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// pickAttachment opens the desktop file chooser and stages the chosen file in
// the app temp directory so the mail backend receives a stable path.
func (a *App) pickAttachment(c *composeView) {
	// Capture the picker on the UI goroutine: the worker must not read the
	// field, which detach clears when the window goes away.
	exp := a.windowExplorer()
	if exp == nil {
		c.errMsg = i18n.T("compose.attachUnavailable")
		return
	}
	dir, err := a.tempDir()
	if err != nil {
		c.errMsg = err.Error()
		return
	}

	go func() {
		rc, err := exp.ChooseFile()
		if err != nil {
			a.post(func() {
				if errors.Is(err, explorer.ErrNotAvailable) {
					c.errMsg = i18n.T("compose.attachUnavailable")
					return
				}
				c.errMsg = err.Error()
			})
			return
		}
		defer func() { _ = rc.Close() }()

		name := i18n.T("attachment.name")
		if f, ok := rc.(*os.File); ok && f.Name() != "" {
			name = filepath.Base(f.Name())
		} else if statter, ok := rc.(interface{ Stat() (os.FileInfo, error) }); ok {
			if fi, statErr := statter.Stat(); statErr == nil && fi.Name() != "" {
				name = fi.Name()
			}
		}

		tmp, err := os.CreateTemp(dir, "attach-*")
		if err != nil {
			a.post(func() { c.errMsg = err.Error() })
			return
		}
		tmpName := tmp.Name()
		// Every failure below has to drop the staged file: nothing else owns it
		// until the draft receives the path.
		discard := func(err error) {
			_ = os.Remove(tmpName)
			a.post(func() { c.errMsg = err.Error() })
		}
		if _, err := io.Copy(tmp, rc); err != nil {
			_ = tmp.Close()
			discard(err)
			return
		}
		if err := tmp.Close(); err != nil {
			discard(err)
			return
		}
		a.post(func() {
			c.addAttachment(mail.DraftAttachment{Name: name, Path: tmpName})
		})
	}()
}

// saveAttachment asks for a destination first, then writes the attachment
// straight into it. The download task in the sidebar reports the transfer and
// stays after it finishes.
func (a *App) saveAttachment(msgID string, att mail.Attachment) {
	svc := a.service()
	if svc == nil {
		a.notify(i18n.T("toast.accountUnavailable"), true)
		return
	}
	exp := a.windowExplorer()
	if exp == nil {
		a.notify(i18n.T("compose.attachUnavailable"), true)
		return
	}

	task := a.tasks.begin(taskDownload, msgID+"\x00"+att.ID, att.Filename)
	if task == nil {
		// The same attachment is already downloading: one transfer is enough.
		return
	}
	id := task.id
	// The parent context is what the row's dismiss control cancels. The
	// transfer timeout is applied only after the dialog returns, so a slow
	// dialog does not eat into the download budget.
	ctx, cancel := context.WithCancel(context.Background())
	a.tasks.setCancel(id, cancel)
	report := func(pr mail.Progress) {
		a.post(func() { a.tasks.progress(id, pr) })
	}
	fail := func(err error, partial bool) {
		logErr("save attachment", err)
		a.post(func() {
			// The row was dismissed meanwhile: the user asked for the
			// transfer to stop, so its failure is not news.
			if a.tasks.find(id) == nil {
				return
			}
			a.tasks.fail(id, err, partial)
			a.notify(i18n.T("toast.saveFailed", map[string]string{"Error": errorText(err)}), true)
		})
	}

	go func() {
		defer cancel()
		// The dialog comes first: the user picks the destination before any
		// byte is downloaded, and the download timeout starts only afterwards.
		w, err := exp.CreateFile(att.Filename)
		if err != nil {
			if errors.Is(err, explorer.ErrUserDecline) {
				// A cancelled dialog is not a failure: nothing was downloaded
				// and no task is left behind.
				a.post(func() { a.tasks.drop(id) })
				return
			}
			logErr("open save dialog", err)
			msg := i18n.T("compose.attachUnavailable")
			a.post(func() {
				if a.tasks.find(id) == nil {
					return
				}
				a.tasks.fail(id, errors.New(msg), false)
				a.notify(msg, true)
			})
			return
		}
		if ctx.Err() != nil {
			// Dismissed while the dialog was open: the portal already created
			// the file, but there is no task left to report a transfer into it.
			_ = w.Close()
			return
		}

		dl, stop := context.WithTimeout(ctx, timeoutAttach)
		defer stop()
		saveErr := svc.SaveAttachment(dl, msgID, att.ID, w, report)
		// Closing commits the portal write; a close error is the only sign the
		// file never made it, so it counts as a failed save.
		closeErr := w.Close()
		if saveErr != nil {
			// The target was created when the dialog was confirmed, and the
			// dialog offers no safe way to remove it again, so the task says it
			// may be incomplete instead of guessing.
			fail(saveErr, true)
			return
		}
		if closeErr != nil {
			fail(closeErr, true)
			return
		}
		a.post(func() { a.tasks.finish(id, savedName(w, att.Filename)) })
	}()
}

// savedName is what the task row shows after a save: the real name of the
// chosen file when the portal handed back an *os.File, the attachment's own
// name otherwise.
func savedName(w io.WriteCloser, fallback string) string {
	if namer, ok := w.(interface{ Name() string }); ok {
		if name := filepath.Base(namer.Name()); name != "" && name != "." {
			return name
		}
	}
	return fallback
}
