package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

const (
	defaultPageSize = 50
	maxPageSize     = 500
	// metadataFields trims the thread response to what the list row renders.
	// payload is requested whole: the metadata format carries no bodies, but
	// the part tree is what tells the row whether a file is attached.
	metadataFields = "id,messages(id,labelIds,snippet,internalDate,payload)"
)

// metadataQuery is the query of one threads.get call that fills a list row.
func metadataQuery() url.Values {
	q := url.Values{}
	q.Set("format", "metadata")
	q.Set("fields", metadataFields)
	for _, h := range []string{"From", "To", "Cc", "Subject", "Date"} {
		q.Add("metadataHeaders", h)
	}
	return q
}

// ListThreads returns one page of conversations of one folder, newest first.
//
// A page the cache still holds is assembled from the row cache and costs no
// request at all; a page that has to be listed costs the threads.list call plus
// a threads.get for every row the cache is missing.
func (s *Service) ListThreads(ctx context.Context, req mail.ListRequest) (mail.ThreadPage, error) {
	labelID, err := labelIDFor(req.FolderID)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	limit := pageLimit(req.Limit)
	key := threadsCacheKey(labelID, req.PageToken, limit)
	if page, ok := s.cache.pages.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	if err := s.waitCheck(ctx); err != nil {
		return mail.ThreadPage{}, err
	}
	if page, ok := s.cache.pages.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	gen := s.cache.pages.epoch()
	page, err := s.listThreads(ctx, labelID, "", req.PageToken, limit)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	s.cache.pages.setAt(gen, key, page)
	return s.pageFromIDs(ctx, page)
}

// SearchThreads returns one page of conversations matching the query, newest
// first. The query goes to threads.list as written and without a label filter,
// so it spans every folder; Gmail's includeSpamTrash default keeps trash and
// spam out, which is how the web search behaves.
func (s *Service) SearchThreads(ctx context.Context, req mail.SearchRequest) (mail.ThreadPage, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return mail.ThreadPage{}, mail.ErrInvalidArgument
	}
	limit := pageLimit(req.Limit)
	key := searchCacheKey(query, req.PageToken, limit)
	if page, ok := s.cache.search.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	if err := s.waitCheck(ctx); err != nil {
		return mail.ThreadPage{}, err
	}
	// A read that waited may have been beaten to the entry by another one.
	if page, ok := s.cache.search.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	gen := s.cache.search.epoch()
	page, err := s.listThreads(ctx, "", query, req.PageToken, limit)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	s.cache.search.setAt(gen, key, page)
	return s.pageFromIDs(ctx, page)
}

// pageLimit clamps a requested page size to the backend's bounds.
func pageLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	if limit > maxPageSize {
		return maxPageSize
	}
	return limit
}

// listThreads performs one threads.list call and returns the conversation IDs
// of the page. An empty labelID does not filter by folder, which is what a
// search needs; an empty query browses the label instead.
//
// includeSpamTrash is raised for the spam and trash folders: the API leaves
// both out of the results by default, so labelIds=SPAM or TRASH would come back
// empty. A search keeps the default false, which is what limits it to mail that
// is neither spam nor in the trash.
func (s *Service) listThreads(ctx context.Context, labelID, query, pageToken string, limit int) (pageEntry, error) {
	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(limit))
	q.Set("fields", "nextPageToken,resultSizeEstimate,threads/id")
	if labelID != "" {
		q.Set("labelIds", labelID)
	}
	if hidesByDefault(labelID) {
		q.Set("includeSpamTrash", "true")
	}
	if query != "" {
		q.Set("q", query)
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}

	var list ListThreadsResponse
	if err := s.c.do(ctx, "threads.list", "GET", "/users/me/threads", q, &list); err != nil {
		return pageEntry{}, err
	}

	ids := make([]string, 0, len(list.Threads))
	for _, th := range list.Threads {
		if th == nil || th.ID == "" {
			continue
		}
		ids = append(ids, th.ID)
	}
	total := int(list.ResultSizeEstimate)
	if list.ResultSizeEstimate <= 0 && len(ids) > 0 {
		// Gmail omits the estimate for some queries; the contract uses -1 to
		// say the backend cannot tell.
		total = -1
	}
	return pageEntry{IDs: ids, Total: total, NextPageToken: list.NextPageToken}, nil
}

// pageFromIDs turns a page of conversation IDs into list rows, loading only the
// rows the cache does not hold.
//
// This is where the quota is spent: threads.list costs 10 units, every
// threads.get 40, while a row that is still cached costs nothing. The rows are
// therefore cached per conversation and outlive the page that showed them.
func (s *Service) pageFromIDs(ctx context.Context, page pageEntry) (mail.ThreadPage, error) {
	page = page.clone()

	var missing []string
	for _, id := range page.IDs {
		if _, ok := s.cache.rows.get(id); ok {
			continue
		}
		if s.cache.gone.has(id) {
			continue
		}
		missing = append(missing, id)
	}

	loaded := make(map[string]mail.ThreadSummary, len(missing))
	if len(missing) > 0 {
		gen := s.cache.rows.epoch()
		threads, err := s.fetchThreads(ctx, missing)
		if err != nil {
			return mail.ThreadPage{}, err
		}
		found := make(map[string]bool, len(threads))
		for _, th := range threads {
			row := summarize(s.acc.ID, th)
			if row.ID == "" {
				continue
			}
			loaded[row.ID] = row
			found[row.ID] = true
			s.cache.rows.setAt(gen, row.ID, row)
		}
		for _, id := range missing {
			if !found[id] {
				// Deleted between the list and the batch: remember it, so the
				// next assembly of this page does not ask again.
				s.cache.gone.add(id)
			}
		}
	}

	rows := make([]mail.ThreadSummary, 0, len(page.IDs))
	for _, id := range page.IDs {
		if row, ok := s.cache.rows.get(id); ok {
			rows = append(rows, cloneSummary(row))
			continue
		}
		if row, ok := loaded[id]; ok {
			rows = append(rows, cloneSummary(row))
		}
	}
	return mail.ThreadPage{
		Threads:       rows,
		Total:         page.Total,
		NextPageToken: page.NextPageToken,
	}, nil
}

// fetchThreads loads the metadata of every conversation in ids with one batch
// request, keeping the order of ids. A conversation deleted between the list
// and the batch is dropped instead of failing the whole page.
func (s *Service) fetchThreads(ctx context.Context, ids []string) ([]*Thread, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]*Thread, len(ids))
	found := make([]bool, len(ids))
	targets := make([]batchTarget, len(ids))
	q := metadataQuery()
	for i, id := range ids {
		targets[i] = batchTarget{path: "/users/me/threads/" + url.PathEscape(id), query: q}
	}

	err := s.c.batchFetch(ctx, "threads.get", "/users/me/threads", targets, func(i int, resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			// The conversation was deleted between the list and the batch:
			// drop the row and keep the rest of the page.
			return nil
		default:
			detail, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			return fmt.Errorf("gmail threads.get %s: %s: %s",
				ids[i], http.StatusText(resp.StatusCode), strings.TrimSpace(string(detail)))
		}
		var th Thread
		if err := json.NewDecoder(resp.Body).Decode(&th); err != nil {
			return fmt.Errorf("gmail threads.get %s: decode response: %w", ids[i], err)
		}
		if th.ID == "" {
			th.ID = ids[i]
		}
		out[i] = &th
		found[i] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	live := make([]*Thread, 0, len(ids))
	for i := range out {
		if found[i] {
			live = append(live, out[i])
		}
	}
	return live, nil
}
