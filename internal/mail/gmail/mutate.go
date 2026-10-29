package gmail

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

// Gmail system label IDs the app names. The mailbox states the sidebar shows
// are these labels: INBOX is what archiving removes, UNREAD and STARRED are the
// two flags the list and the reader turn on and off, SENT holds what this
// account sent, and SPAM and TRASH are the two folders Gmail keeps out of a
// listing unless it is asked for them.
const (
	labelInbox   = "INBOX"
	labelUnread  = "UNREAD"
	labelStarred = "STARRED"
	labelSent    = "SENT"
	labelSpam    = "SPAM"
	labelTrash   = "TRASH"
)

// modifyBatchSize is the largest messages.batchModify the API accepts. One call
// therefore covers a page of conversations, whose message count is a small
// multiple of the page size.
const modifyBatchSize = 1000

// modifyRequest is the body of messages.batchModify. The generated
// BatchModifyMessagesRequest carries the classification-label fields as well;
// this app never sets them, and a local shape keeps them off the wire.
type modifyRequest struct {
	IDs            []string `json:"ids"`
	AddLabelIDs    []string `json:"addLabelIds,omitempty"`
	RemoveLabelIDs []string `json:"removeLabelIds,omitempty"`
}

// SetFlag adds or removes one of the flags the list and the reader toggle. The
// IDs are message IDs: the caller passes every message of a conversation, so
// the conversation changes state as a whole.
func (s *Service) SetFlag(ctx context.Context, ids []string, flag mail.Flag, on bool) error {
	labelID, err := flagLabel(flag)
	if err != nil {
		return err
	}
	// UNREAD is present while a message is unread, STARRED while it is starred.
	// on is the state the caller wants, so the read flag adds the label when it
	// is turned off and the star flag when it is turned on.
	add := on
	if flag == mail.FlagRead {
		add = !on
	}
	if add {
		return s.modifyMessages(ctx, ids, []string{labelID}, nil)
	}
	return s.modifyMessages(ctx, ids, nil, []string{labelID})
}

// flagLabel maps a flag onto the label that carries its state.
func flagLabel(flag mail.Flag) (string, error) {
	switch flag {
	case mail.FlagRead:
		return labelUnread, nil
	case mail.FlagStarred:
		return labelStarred, nil
	}
	return "", fmt.Errorf("%w: flag %d", mail.ErrInvalidArgument, flag)
}

// Archive takes the conversations out of the inbox. Gmail has no archive call:
// removing the INBOX label is archiving, and the messages stay reachable from
// every other label and from search.
func (s *Service) Archive(ctx context.Context, ids []string) error {
	return s.modifyMessages(ctx, ids, nil, []string{labelInbox})
}

// MarkSpam files the messages under the spam label and takes them out of the
// inbox, or does the reverse when spam is false. Both directions are one label
// swap, so the conversation is reachable from exactly one of the two folders
// afterwards.
func (s *Service) MarkSpam(ctx context.Context, ids []string, spam bool) error {
	if spam {
		return s.modifyMessages(ctx, ids, []string{labelSpam}, []string{labelInbox})
	}
	return s.modifyMessages(ctx, ids, []string{labelInbox}, []string{labelSpam})
}

// Delete moves the messages to the trash. Gmail has no batch trash call, so the
// per-message calls ride one multipart batch per batchLimit messages; a message
// that is already gone counts as done, because the trash is where the caller
// wanted it.
func (s *Service) Delete(ctx context.Context, ids []string) error {
	ids = usableIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	targets := make([]batchTarget, len(ids))
	for i, id := range ids {
		targets[i] = batchTarget{
			path:   "/users/me/messages/" + url.PathEscape(id) + "/trash",
			method: http.MethodPost,
		}
	}
	// The whole write rides the batch envelope, so a page of conversations is
	// one round trip per batchLimit messages instead of one per message.
	err := s.c.batchFetch(ctx, "messages.trash", "/users/me/messages", targets, func(_ int, resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
			return nil
		}
		return apiError("messages.trash", resp)
	})
	// A batch that failed part way through still trashed some of the messages,
	// so the cached pages are stale either way.
	s.cache.clear()
	return err
}

// modifyMessages adds and removes labels on every listed message. One call per
// modifyBatchSize IDs keeps a bulk action on a page to a single request.
func (s *Service) modifyMessages(ctx context.Context, ids []string, add, remove []string) error {
	ids = usableIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	// The write invalidates whatever the cached reads describe, and it does so
	// even when a later chunk fails: the earlier ones already changed the
	// mailbox, and the caller refreshes right after the answer.
	defer s.cache.clear()
	for start := 0; start < len(ids); start += modifyBatchSize {
		end := min(start+modifyBatchSize, len(ids))
		req := modifyRequest{IDs: ids[start:end], AddLabelIDs: add, RemoveLabelIDs: remove}
		if err := s.c.postJSON(ctx, "messages.batchModify", "/users/me/messages/batchModify", req, nil); err != nil {
			return err
		}
	}
	return nil
}

// usableIDs drops the IDs a request cannot address. The list only hands out IDs
// a backend produced, so this guards a future caller from turning an empty
// string into a malformed path.
func usableIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
