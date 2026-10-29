package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// pickAttachment opens the desktop file chooser and records the chosen file's
// path. Nothing is copied to the scratch directory: the send path streams the
// original file from disk, so the draft never owns a second copy of it.
func (a *App) pickAttachment(c *composeView) {
	// Capture the picker on the UI goroutine: the worker must not read the
	// field, which detach clears when the window goes away.
	exp := a.windowExplorer()
	if exp == nil {
		c.errMsg = i18n.T("compose.attachUnavailable")
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

		// A desktop picker opens the file itself, so its name is the path to
		// stream from later. A reader with no path cannot be attached: reading
		// it now would mean copying the whole file somewhere.
		f, ok := rc.(*os.File)
		if !ok || f.Name() == "" {
			a.post(func() { c.errMsg = i18n.T("compose.attachmentPathUnavailable") })
			return
		}
		path := f.Name()
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			a.post(func() { c.errMsg = i18n.T("compose.attachmentPathUnavailable") })
			return
		}
		name := filepath.Base(path)
		a.post(func() {
			c.addAttachment(mail.DraftAttachment{Name: name, Path: path})
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
	a.saveDownload("save attachment", msgID+"\x00"+att.ID, att.Filename, att.Filename,
		func(ctx context.Context, w io.Writer, report func(mail.Progress)) error {
			return svc.SaveAttachment(ctx, msgID, att.ID, w, report)
		})
}

// saveMessage downloads one message as an .eml file. It goes through the same
// task row as an attachment: the dialog picks the destination, the source
// streams into it, and the row reports progress and the saved path.
func (a *App) saveMessage(m *mail.Message) {
	if m == nil {
		return
	}
	svc := a.service()
	if svc == nil {
		a.notify(i18n.T("toast.accountUnavailable"), true)
		return
	}
	id := m.ID
	name := subjectOrPlaceholder(strings.Join(strings.Fields(m.Subject), " "))
	a.saveDownload("save message", id, name, downloadFileName(m.Subject),
		func(ctx context.Context, w io.Writer, report func(mail.Progress)) error {
			return svc.SaveMessage(ctx, id, w, report)
		})
}

// saveDownload runs one download into a file the user picks. op names the work
// in the failure log; key identifies the work for taskPanel so a second click
// while the same transfer runs is ignored; name is what the row shows and
// filename is the default the save dialog offers.
func (a *App) saveDownload(op, key, name, filename string, fetch func(context.Context, io.Writer, func(mail.Progress)) error) {
	exp := a.windowExplorer()
	if exp == nil {
		a.notify(i18n.T("compose.attachUnavailable"), true)
		return
	}

	task := a.tasks.begin(taskDownload, key, name)
	if task == nil {
		// The same download is already running: one transfer is enough.
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
		logErr(op, err)
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
		w, err := exp.CreateFile(filename)
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
		saveErr := fetch(dl, w, report)
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
		a.post(func() { a.tasks.finish(id, savedName(w, filename)) })
	}()
}

// downloadNameMaxBytes leaves room for the ".eml" extension within the usual
// 255-byte file name limit.
const downloadNameMaxBytes = 200

// downloadFileName is the default name of a downloaded message: its subject
// plus an .eml extension. The subject is user-visible, so non-ASCII letters
// stay; only path separators and control characters are replaced, and the
// leading and trailing dots and spaces a file system would hide are trimmed.
// An empty subject falls back to message.eml.
func downloadFileName(subject string) string {
	var b strings.Builder
	for _, r := range strings.Join(strings.Fields(subject), " ") {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}
	name := strings.Trim(b.String(), " .")
	if len(name) > downloadNameMaxBytes {
		// Cut on a rune boundary so the name stays valid UTF-8.
		cut := downloadNameMaxBytes
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = strings.Trim(name[:cut], " .")
	}
	if name == "" {
		return "message.eml"
	}
	return name + ".eml"
}

// savedName is what the task row shows after a save: the real name of the
// chosen file when the portal handed back an *os.File, the suggested name
// otherwise.
func savedName(w io.WriteCloser, fallback string) string {
	if namer, ok := w.(interface{ Name() string }); ok {
		if name := filepath.Base(namer.Name()); name != "" && name != "." {
			return name
		}
	}
	return fallback
}
