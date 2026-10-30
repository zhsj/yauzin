package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

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
