package gmail

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

// Stages a failed save is reported under. The value is part of the debug log
// line, which is the only way to tell an attachment that has no downloadable
// content from a broken download or a target that refused the bytes.
const (
	attachmentStageNoBlob   = "no blob"
	attachmentStageEmpty    = "empty"
	attachmentStageDownload = "download"
	attachmentStageWrite    = "write"
)

// SaveAttachment writes one attachment of one message to w.
//
// Gmail keeps a file part in a blob of its own, named by body.attachmentId, so
// saving is one streamed request: the response is scanned for its base64 field
// and decoded as it arrives, and neither the encoded text nor the decoded file
// ever sits in memory as a whole. A file part without an attachmentId carries its
// content in the payload instead, where there is nothing to download; the reader
// lists it like any other attachment, and saving it reports that it is missing.
func (s *Service) SaveAttachment(ctx context.Context, messageID, attachmentID string, w io.Writer, progress func(mail.Progress)) error {
	if strings.TrimSpace(messageID) == "" || w == nil {
		return mail.ErrInvalidArgument
	}
	if strings.TrimSpace(attachmentID) == "" {
		err := attachmentFailure{stage: attachmentStageNoBlob, err: mail.ErrNotFound}
		s.logSaveFailure(messageID, attachmentID, err)
		return err
	}
	written, err := s.saveBlob(ctx, messageID, attachmentID, w, progress)
	if err != nil {
		s.logSaveFailure(messageID, attachmentID, err)
		return err
	}
	slog.Debug("gmail attachment saved",
		"account", s.acc.Email,
		"message", messageID,
		"attachment", shortID(attachmentID),
		"bytes", written)
	return nil
}

// logSaveFailure reports one failed save at DEBUG. The stage says whether the
// attachment was missing, the download broke, or the target refused the bytes;
// the content of the attachment is never logged.
func (s *Service) logSaveFailure(messageID, attachmentID string, err error) {
	var failure attachmentFailure
	if !errors.As(err, &failure) {
		failure = attachmentFailure{stage: attachmentStageDownload, err: err}
	}
	slog.Debug("gmail attachment save failed",
		"account", s.acc.Email,
		"message", messageID,
		"attachment", shortID(attachmentID),
		"stage", failure.stage,
		"err", failure.err)
}

// saveBlob fetches the blob of one attachment and streams its decoded content
// into w. progress reports how much of the encoded response body has arrived, so
// the bar moves with the file instead of jumping to the end.
func (s *Service) saveBlob(ctx context.Context, messageID, attachmentID string, w io.Writer, progress func(mail.Progress)) (int64, error) {
	q := url.Values{}
	path := "/users/me/messages/" + url.PathEscape(messageID) +
		"/attachments/" + url.PathEscape(attachmentID)
	var written int64
	err := s.c.doStream(ctx, "messages.attachments.get", path, q, progress, func(r io.Reader) error {
		tail, err := scanDataField(r)
		if err != nil {
			if errors.Is(err, mail.ErrNotFound) {
				return attachmentFailure{stage: attachmentStageEmpty, err: err}
			}
			return err
		}
		n, err := copyToTarget(w, base64.NewDecoder(base64.URLEncoding, newPaddingReader(tail)))
		written = n
		if err != nil {
			return err
		}
		if n == 0 {
			// The field is there but holds nothing, so the attachment has no
			// content to download.
			return attachmentFailure{stage: attachmentStageEmpty, err: mail.ErrNotFound}
		}
		return nil
	})
	return written, err
}

// copyToTarget streams a decoded payload into the target with a fixed buffer,
// so a save costs the same memory however large the payload is. io.Copy would
// do, but the error has to say whether reading or writing broke: only the
// latter is a write failure.
func copyToTarget(w io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32<<10)
	var written int64
	for {
		nr, err := src.Read(buf)
		if nr > 0 {
			nw, writeErr := w.Write(buf[:nr])
			written += int64(nw)
			if writeErr != nil {
				return written, attachmentFailure{stage: attachmentStageWrite, err: writeErr}
			}
			if nw != nr {
				return written, attachmentFailure{stage: attachmentStageWrite, err: io.ErrShortWrite}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return written, nil
			}
			return written, err
		}
	}
}

// attachmentFailure is a failed save together with the stage it failed in, so one
// log line can say whether the attachment or the transfer was at fault. The
// wrapped error is what the caller and the UI see.
type attachmentFailure struct {
	stage string
	err   error
}

func (e attachmentFailure) Error() string { return e.err.Error() }

func (e attachmentFailure) Unwrap() error { return e.err }

// shortID keeps a log line readable: attachment IDs run to hundreds of bytes, and
// a part with no downloadable ID has none to show.
func shortID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}
