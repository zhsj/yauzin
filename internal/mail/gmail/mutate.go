package gmail

import (
	"context"
	"errors"
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

// labelRequest is the body of messages.modify and threads.modify: the target is
// named in the path, so the body carries the labels only.
type labelRequest struct {
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
	var addLabels, removeLabels []string
	if add {
		addLabels = []string{labelID}
	} else {
		removeLabels = []string{labelID}
	}

	threadIDs, whole, err := s.modifyMessages(ctx, ids, addLabels, removeLabels)
	if err != nil || !whole {
		// A chunked write may have applied part of the change, and a
		// conversation the row cache cannot describe cannot be patched.
		s.cache.clear()
		return err
	}
	// The write just told us the exact state of the conversation, so the rows
	// are patched instead of read back: reading them again would cost 40 units
	// each. The page order stays, but a view that filters on the flag (the
	// starred folder, an is:unread search) may no longer contain the row, so
	// the listings are dropped and rebuilt from the patched rows for 10 units.
	s.cache.applyFlag(threadIDs, flag, on)
	s.cache.dropPages()
	return nil
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
	return s.modifyAndInvalidate(ctx, ids, nil, []string{labelInbox})
}

// MarkSpam files the messages under the spam label and takes them out of the
// inbox, or does the reverse when spam is false. Both directions are one label
// swap, so the conversation is reachable from exactly one of the two folders
// afterwards.
func (s *Service) MarkSpam(ctx context.Context, ids []string, spam bool) error {
	if spam {
		return s.modifyAndInvalidate(ctx, ids, []string{labelSpam}, []string{labelInbox})
	}
	return s.modifyAndInvalidate(ctx, ids, []string{labelInbox}, []string{labelSpam})
}

// Delete moves the messages to the trash. Gmail has no batch trash call, so a
// page of messages rides one multipart batch; a message that is already gone
// counts as done, because the trash is where the caller wanted it.
func (s *Service) Delete(ctx context.Context, ids []string) error {
	ids = usableIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	threadIDs, whole := s.cache.threadsForMessages(ids)
	var err error
	if whole && len(threadIDs) == 1 {
		// Trashing the conversation costs 20 units for all of its messages,
		// while trashing the messages one by one costs 20 each.
		err = s.trashThread(ctx, threadIDs[0])
	} else {
		err = s.trashMessages(ctx, ids)
	}
	if err != nil || !whole {
		// A batch that failed part way through still trashed some of the
		// messages, so the cache is stale either way.
		s.cache.clear()
		return err
	}
	s.cache.invalidateThreads(threadIDs)
	return nil
}

// trashThread moves one whole conversation to the trash.
func (s *Service) trashThread(ctx context.Context, threadID string) error {
	err := s.c.postJSON(ctx, "threads.trash", "/users/me/threads/"+url.PathEscape(threadID)+"/trash", nil, nil)
	if err == nil || errors.Is(err, mail.ErrNotFound) {
		// A conversation that is already gone is in the trash, which is where
		// the caller wanted it.
		return nil
	}
	return err
}

// trashMessages moves the listed messages to the trash in one batch per
// batchLimit messages.
func (s *Service) trashMessages(ctx context.Context, ids []string) error {
	targets := make([]batchTarget, len(ids))
	for i, id := range ids {
		targets[i] = batchTarget{
			path:   "/users/me/messages/" + url.PathEscape(id) + "/trash",
			method: http.MethodPost,
		}
	}
	return s.c.batchFetch(ctx, "messages.trash", "/users/me/messages", targets, func(_ int, resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
			return nil
		}
		return apiError("messages.trash", resp)
	})
}

// modifyAndInvalidate applies a label change that moves conversations between
// folders and drops what the cache says about them: the row no longer belongs
// to the folder it was listed in, and its labels changed.
func (s *Service) modifyAndInvalidate(ctx context.Context, ids []string, add, remove []string) error {
	threadIDs, whole, err := s.modifyMessages(ctx, ids, add, remove)
	if err != nil || !whole {
		s.cache.clear()
		return err
	}
	s.cache.invalidateThreads(threadIDs)
	return nil
}

// modifyMessages applies one label change to whole conversations, choosing the
// cheapest call that covers them, and reports the conversations it touched:
//
//   - a single message: messages.modify, 5 quota units;
//   - a single conversation of several messages: threads.modify, 10 units for
//     the whole conversation instead of 50 for a batch;
//   - several conversations: one messages.batchModify per modifyBatchSize IDs,
//     50 units each, which beats one thread call per conversation.
//
// whole is false when the row cache cannot describe the IDs as complete
// conversations. The caller then has to drop the cache rather than patch it:
// showing a stale row is worse than paying to read it again.
func (s *Service) modifyMessages(ctx context.Context, ids []string, add, remove []string) (threadIDs []string, whole bool, err error) {
	ids = usableIDs(ids)
	if len(ids) == 0 {
		return nil, true, nil
	}
	threadIDs, whole = s.cache.threadsForMessages(ids)
	body := labelRequest{AddLabelIDs: add, RemoveLabelIDs: remove}
	switch {
	case len(ids) == 1:
		return threadIDs, whole, s.c.postJSON(ctx, "messages.modify",
			"/users/me/messages/"+url.PathEscape(ids[0])+"/modify", body, nil)
	case whole && len(threadIDs) == 1:
		return threadIDs, whole, s.c.postJSON(ctx, "threads.modify",
			"/users/me/threads/"+url.PathEscape(threadIDs[0])+"/modify", body, nil)
	}
	for start := 0; start < len(ids); start += modifyBatchSize {
		end := min(start+modifyBatchSize, len(ids))
		req := modifyRequest{IDs: ids[start:end], AddLabelIDs: add, RemoveLabelIDs: remove}
		if err := s.c.postJSON(ctx, "messages.batchModify", "/users/me/messages/batchModify", req, nil); err != nil {
			return threadIDs, whole, err
		}
	}
	return threadIDs, whole, nil
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
