package gmail

import (
	"testing"
)

func TestLRUCacheGetSetAndHit(t *testing.T) {
	c := newLRUCache[int](3)
	if _, ok := c.get("a"); ok {
		t.Fatal("get on an empty cache reported a hit")
	}
	c.set("a", 1)
	c.set("b", 2)
	c.set("c", 3)
	for key, want := range map[string]int{"a": 1, "b": 2, "c": 3} {
		got, ok := c.get(key)
		if !ok || got != want {
			t.Fatalf("get(%q) = %d, %v; want %d, true", key, got, ok, want)
		}
	}
}

func TestLRUCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newLRUCache[int](2)
	c.set("a", 1)
	c.set("b", 2)
	c.get("a")    // a becomes the most recent, b the least
	c.set("c", 3) // evicts b
	if _, ok := c.get("b"); ok {
		t.Fatal("b should have been evicted")
	}
	if got, ok := c.get("a"); !ok || got != 1 {
		t.Fatalf("get(a) = %d, %v; want 1, true", got, ok)
	}
	if got, ok := c.get("c"); !ok || got != 3 {
		t.Fatalf("get(c) = %d, %v; want 3, true", got, ok)
	}
}

func TestLRUCacheSetReplacesWithoutGrowing(t *testing.T) {
	c := newLRUCache[int](2)
	c.set("a", 1)
	c.set("b", 2)
	c.set("a", 10) // overwrite, not an insert: b must survive
	if got, ok := c.get("a"); !ok || got != 10 {
		t.Fatalf("get(a) = %d, %v; want 10, true", got, ok)
	}
	if got, ok := c.get("b"); !ok || got != 2 {
		t.Fatalf("get(b) = %d, %v; want 2, true", got, ok)
	}
}

func TestLRUCacheClear(t *testing.T) {
	c := newLRUCache[int](2)
	c.set("a", 1)
	c.set("b", 2)
	c.clear()
	if _, ok := c.get("a"); ok {
		t.Fatal("get(a) hit after clear")
	}
	if _, ok := c.get("b"); ok {
		t.Fatal("get(b) hit after clear")
	}
}

// TestSetAtDropsAnEntryFromBeforeAClear pins the epoch contract of the LRU: a
// response whose request started before a clear must not be written back, while
// a read started after it writes normally. The account has a single request
// slot, so CheckChanges cannot produce this race against an in-flight read in a
// test; the cache is asserted directly.
func TestSetAtDropsAnEntryFromBeforeAClear(t *testing.T) {
	c := newLRUCache[string](4)
	c.set("old", "page")

	gen := c.epoch()
	c.clear()

	c.setAt(gen, "stale", "page")
	if _, ok := c.get("stale"); ok {
		t.Fatal("a response fetched before the clear landed in the cache")
	}
	if _, ok := c.get("old"); ok {
		t.Fatal("clear kept the previous entry")
	}

	c.setAt(c.epoch(), "fresh", "page")
	if got, ok := c.get("fresh"); !ok || got != "page" {
		t.Fatalf("fresh entry = %q/%v, want page/true", got, ok)
	}
}
