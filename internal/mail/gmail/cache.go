package gmail

import (
	"container/list"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/zhsj/yauzin/internal/mail"
)

// The cached read methods have no expiry: the account is read-only, so a
// response stays valid until CheckChanges drops it because the mailbox moved.
// The LRU capacities only bound how much memory the cache may hold.
const (
	foldersCacheSize = 4
	detailsCacheSize = 4
	threadsCacheSize = 64
	// searchCacheSize is separate from the browsing cache: a query list and a
	// folder page describe different views, and neither should evict the other.
	searchCacheSize = 32
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

// lruCache is a small concurrent LRU cache keyed by string. It is a map plus a
// doubly linked list of recency order; both are guarded by one mutex so get,
// set and clear are each a single atomic step.
type lruCache[V any] struct {
	mu  sync.Mutex
	cap int
	ll  *list.List
	m   map[string]*list.Element
	// gen counts the clears. A read captures it before its request and hands it
	// to setAt, so a response fetched before a clear can never be written back:
	// the entry would describe the mailbox state the clear already replaced.
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

// setAt stores value under key unless the cache has been cleared since gen was
// read: the response then describes a mailbox state a newer entry replaced.
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
// response from landing in a cache that was cleared while it was in flight.
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

// clear drops every entry and advances the generation, which invalidates the
// responses still being fetched.
func (c *lruCache[V]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.m = make(map[string]*list.Element)
	c.gen++
}

// serviceCache holds the memoized reads of one account.
type serviceCache struct {
	folders *lruCache[[]mail.Folder]
	details *lruCache[map[string]*Label]
	threads *lruCache[mail.ThreadPage]
	search  *lruCache[mail.ThreadPage]
}

func newServiceCache() *serviceCache {
	return &serviceCache{
		folders: newLRUCache[[]mail.Folder](foldersCacheSize),
		details: newLRUCache[map[string]*Label](detailsCacheSize),
		threads: newLRUCache[mail.ThreadPage](threadsCacheSize),
		search:  newLRUCache[mail.ThreadPage](searchCacheSize),
	}
}

// clear drops every cached response of the account.
func (sc *serviceCache) clear() {
	sc.folders.clear()
	sc.details.clear()
	sc.threads.clear()
	sc.search.clear()
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
