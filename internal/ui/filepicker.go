package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// maxNameAttempts bounds the "name (1).ext" search when a download name is
// already taken.
const maxNameAttempts = 1000

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

// saveAttachment writes one attachment through the desktop portal. When the
// portal is unavailable it falls back to ~/Downloads and reports the path.
func (a *App) saveAttachment(msgID string, att mail.Attachment) {
	svc := a.service()
	if svc == nil {
		a.notify(i18n.T("toast.accountUnavailable"), true)
		return
	}
	dir, err := a.tempDir()
	if err != nil {
		a.notify(i18n.T("toast.saveFailed", map[string]string{"Error": err.Error()}), true)
		return
	}
	exp := a.windowExplorer()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeoutAttach)
		defer cancel()
		path, err := svc.SaveAttachment(ctx, msgID, att.ID, dir)
		if err != nil {
			logErr("save attachment", err)
			a.post(func() {
				a.notify(i18n.T("toast.saveFailed", map[string]string{"Error": errorText(err)}), true)
			})
			return
		}
		// The staged copy belongs to this attempt. The fallback below still needs
		// it, so it is only removed once the whole attempt is over.
		defer func() { _ = os.Remove(path) }()

		if exp != nil {
			if w, err := exp.CreateFile(att.Filename); err == nil {
				if src, openErr := os.Open(path); openErr == nil {
					_, copyErr := io.Copy(w, src)
					_ = src.Close()
					// Closing commits the portal write; ignoring it would report a
					// save the user never got.
					closeErr := w.Close()
					if copyErr == nil && closeErr == nil {
						a.post(func() {
							a.notify(i18n.T("toast.savedTo", map[string]string{"Path": att.Filename}), false)
						})
						return
					}
					slog.Warn("portal save failed, falling back to Downloads",
						"copy", copyErr, "close", closeErr)
				} else {
					_ = w.Close()
					slog.Warn("staged attachment unreadable, falling back to Downloads", "err", openErr)
				}
			}
		}

		// Fallback: copy straight into Downloads and tell the user where it went.
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			a.post(func() {
				a.notify(i18n.T("toast.saveFailed", map[string]string{"Error": homeErr.Error()}), true)
			})
			return
		}
		downloads := filepath.Join(home, "Downloads")
		if err := os.MkdirAll(downloads, 0o755); err != nil {
			a.post(func() {
				a.notify(i18n.T("toast.saveFailed", map[string]string{"Error": err.Error()}), true)
			})
			return
		}
		dst, err := copyIntoDir(path, downloads, att.Filename)
		if err != nil {
			a.post(func() {
				a.notify(i18n.T("toast.saveFailed", map[string]string{"Error": err.Error()}), true)
			})
			return
		}
		a.post(func() {
			a.notify(i18n.T("toast.savedTo", map[string]string{"Path": dst}), false)
		})
	}()
}

// copyIntoDir copies src into dir under name, appending a counter when the name
// is taken, and returns the path it used. The destination is created with
// O_EXCL so the name is reserved atomically: two concurrent saves of the same
// attachment cannot truncate each other.
func copyIntoDir(src, dir, name string) (string, error) {
	name = filepath.Base(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = i18n.T("attachment.name")
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; i < maxNameAttempts; i++ {
		dst := filepath.Join(dir, candidateName(base, ext, i))
		err := copyFileExclusive(src, dst)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", fmt.Errorf("no free filename for %s", name)
}

// candidateName is the i-th file name tried for a download.
func candidateName(base, ext string, i int) string {
	if i == 0 {
		return base + ext
	}
	return fmt.Sprintf("%s (%d)%s", base, i, ext)
}

// copyFileExclusive copies src to dst, failing with fs.ErrExist instead of
// truncating an existing file. A partial copy is removed again.
func copyFileExclusive(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}
