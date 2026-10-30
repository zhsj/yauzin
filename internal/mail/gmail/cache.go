package gmail

import (
	"container/list"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/zhsj/yauzin/internal/mail"
)

// The cached reads have no expiry: a response stays valid until something
// reports that the mailbox moved, which is CheckChanges (another client changed
// it) or a write of this account, which invalidates what it touched as soon as
// it returns. The capacities only bound how much memory the cache may hold.
//
// The sizes follow the quota price of each entry: a row (40 units) is worth
// far more than a page (10 units), and a whole conversation (60 or 120 units)
// more than both.
const (
	foldersCacheSize = 4
	detailsCacheSize = 4
	pagesCacheSize   = 64
	// searchCacheSize is separate from the browsing cache: a query list and a
	// folder page describe different views, and neither should evict the other.
	searchCacheSize = 32
	// rowsCacheSize bounds the conversation rows, the entries a refresh can
	// reuse across folders, labels and searches.
	rowsCacheSize = 2000
	// bodiesCacheSize bounds the conversations the reader opened.
	bodiesCacheSize = 8
	// goneCacheSize bounds the remembered missing conversations.
	goneCacheSize = 4096
)

// cacheKeySeparator joins the fields of a cache key. It never occurs in a
// label ID, a query or a page token.
const cacheKeySeparator = "\x00"

// cacheKeyFolders names the single Folders entry.
const cacheKeyFolders = "folders"

// detailsCacheKey names the label detail batch of one folder set.
func detailsCacheKey(ids []string) string {
	return "details:" + strings.Join(ids, cacheKeySeparator)
}

// threadsCacheKey names one page of folder browsing.
func threadsCacheKey(labelID, pageToken string, limit int) string {
	return "threads:" + labelID + cacheKeySeparator + pageToken +
		cacheKeySeparator + strconv.Itoa(limit)
}

// searchCacheKey names one page of search results. Search pages carry no label
// id, which is what keeps them apart from the browsing entries.
func searchCacheKey(query, pageToken string, limit int) string {
	return "search:" + query + cacheKeySeparator + pageToken +
		cacheKeySeparator + strconv.Itoa(limit)
}

// pageEntry is one page of a listing: the conversation IDs in page order plus
// the paging information. The rows themselves are cached separately, keyed by
// conversation, because a change to one conversation must not throw the whole
// page away.
type pageEntry struct {
	IDs           []string
	Total         int
	NextPageToken string
}

// clone copies the ID slice, so a caller cannot reorder the cached page.
func (p pageEntry) clone() pageEntry {
	p.IDs = slices.Clone(p.IDs)
	return p
}

// lruCache is a small concurrent LRU cache keyed by string. It is a map plus a
// doubly linked list of recency order; both are guarded by one mutex so get,
// set and clear are each a single atomic step.
type lruCache[V any] struct {
	mu  sync.Mutex
	cap int
	ll  *list.List
	m   map[string]*list.Element
	// gen counts the invalidations. A read captures it before its request and
	// hands it to setAt, so a response fetched before a clear can never be
	// written back: the entry would describe the mailbox state the clear
	// already replaced.
	gen uint64
}

// lruEntry is one element of the recency list.
type lruEntry[V any] struct {
	key   string
	value V
}

func newLRUCache[V any](cap int) *lruCache[V] {
	return &lruCache[V]{cap: cap, ll: list.New(), m: make(map[string]*list.Element)}
}

// get returns the cached value for key and reports whether it was present. A
// hit refreshes the entry's recency.
func (c *lruCache[V]) get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*lruEntry[V]).value, true
}

// set stores value under key, replacing an existing entry and evicting the
// least recently used one when the cache is full.
func (c *lruCache[V]) set(key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setLocked(key, value)
}

// setAt stores value under key unless the cache has been invalidated since gen
// was read: the response then describes a mailbox state a newer entry replaced.
func (c *lruCache[V]) setAt(gen uint64, key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	c.setLocked(key, value)
}

// epoch returns the generation a read has to present to setAt. Reading it
// before the request and comparing it again inside setAt is what keeps a slow
// response from landing in a cache that was invalidated while it was in flight.
func (c *lruCache[V]) epoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// setLocked is set and setAt without the lock.
func (c *lruCache[V]) setLocked(key string, value V) {
	if el, ok := c.m[key]; ok {
		el.Value.(*lruEntry[V]).value = value
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry[V]{key: key, value: value})
	c.m[key] = el
	if c.ll.Len() > c.cap {
		if last := c.ll.Back(); last != nil {
			c.ll.Remove(last)
			delete(c.m, last.Value.(*lruEntry[V]).key)
		}
	}
}

// remove drops the named entries and advances the generation, so a response
// still in flight cannot write a row back that the mailbox already changed.
// Keys that are not cached are ignored.
func (c *lruCache[V]) remove(keys ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for _, key := range keys {
		el, ok := c.m[key]
		if !ok {
			continue
		}
		c.ll.Remove(el)
		delete(c.m, key)
	}
}

// clear drops every entry and advances the generation, which invalidates the
// responses still being fetched.
func (c *lruCache[V]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.m = make(map[string]*list.Element)
	c.gen++
}

// goneSet remembers the conversation IDs that answered 404, so a page that
// still lists a deleted conversation does not ask for it again. It is cleared
// whenever the mailbox moves: a thread can be restored from the trash.
type goneSet struct {
	mu sync.Mutex
	m  map[string]struct{}
}

func newGoneSet() *goneSet {
	return &goneSet{m: make(map[string]struct{})}
}

func (g *goneSet) add(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.m) >= goneCacheSize {
		g.m = make(map[string]struct{}, goneCacheSize)
	}
	g.m[id] = struct{}{}
}

func (g *goneSet) has(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.m[id]
	return ok
}

func (g *goneSet) clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.m = make(map[string]struct{})
}

// serviceCache holds the memoized reads of one account.
type serviceCache struct {
	folders *lruCache[[]mail.Folder]
	details *lruCache[map[string]*Label]
	// pages and search hold the listing order; rows hold what the rows show.
	pages  *lruCache[pageEntry]
	search *lruCache[pageEntry]
	rows   *lruCache[mail.ThreadSummary]
	bodies *lruCache[[]*mail.Message]
	gone   *goneSet
}

func newServiceCache() *serviceCache {
	return &serviceCache{
		folders: newLRUCache[[]mail.Folder](foldersCacheSize),
		details: newLRUCache[map[string]*Label](detailsCacheSize),
		pages:   newLRUCache[pageEntry](pagesCacheSize),
		search:  newLRUCache[pageEntry](searchCacheSize),
		rows:    newLRUCache[mail.ThreadSummary](rowsCacheSize),
		bodies:  newLRUCache[[]*mail.Message](bodiesCacheSize),
		gone:    newGoneSet(),
	}
}

// clear drops every cached response of the account. It is the fallback for a
// change the history cannot describe precisely.
func (sc *serviceCache) clear() {
	sc.folders.clear()
	sc.details.clear()
	sc.dropPages()
	sc.rows.clear()
	sc.bodies.clear()
	sc.gone.clear()
}

// dropPages drops the listing order of every view, keeping the rows. The order
// and the totals of a page stop being true as soon as the mailbox moves, while
// a row stays true until its own conversation changes.
func (sc *serviceCache) dropPages() {
	sc.pages.clear()
	sc.search.clear()
	sc.folders.clear()
	sc.details.clear()
}

// invalidateThreads drops the rows and bodies of the named conversations and
// the order of every page. The folder counters go with them: a new message
// changes the numbers the sidebar shows.
func (sc *serviceCache) invalidateThreads(ids []string) {
	sc.rows.remove(ids...)
	sc.bodies.remove(ids...)
	sc.dropPages()
	sc.gone.clear()
}

// cloneThreadPage deep-copies a page so a caller mutating the returned rows
// cannot change the cached copy. The summary scalars are copied by the slice
// clone; the three inner slices still share their backing arrays, so they are
// cloned too.
func cloneThreadPage(p mail.ThreadPage) mail.ThreadPage {
	p.Threads = slices.Clone(p.Threads)
	for i := range p.Threads {
		p.Threads[i].Participants = slices.Clone(p.Threads[i].Participants)
		p.Threads[i].Labels = slices.Clone(p.Threads[i].Labels)
		p.Threads[i].MessageIDs = slices.Clone(p.Threads[i].MessageIDs)
	}
	return p
}

// cloneSummary deep-copies one row for a caller.
func cloneSummary(row mail.ThreadSummary) mail.ThreadSummary {
	row.Participants = slices.Clone(row.Participants)
	row.Labels = slices.Clone(row.Labels)
	row.MessageIDs = slices.Clone(row.MessageIDs)
	return row
}
