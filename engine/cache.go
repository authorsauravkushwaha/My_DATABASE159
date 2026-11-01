package engine

import (
	"container/list"
	"sync"
	"sync/atomic"
)

type blockCacheKey struct {
	path        string
	blockOffset uint64
}

type cachedBlock struct {
	key     blockCacheKey
	entries []sstableEntry
	bytes   int
}

// blockCache is a byte-bounded LRU cache of decoded SSTable blocks so hot
// point lookups and scans avoid repeated disk reads and decompression.
type blockCache struct {
	mu       sync.Mutex
	maxBytes int
	used     int
	ll       *list.List
	items    map[blockCacheKey]*list.Element
	hits     atomic.Uint64
	misses   atomic.Uint64
}

func newBlockCache(maxBytes int) *blockCache {
	if maxBytes <= 0 {
		maxBytes = 16 << 20 // 16 MiB default
	}
	return &blockCache{
		maxBytes: maxBytes,
		ll:       list.New(),
		items:    make(map[blockCacheKey]*list.Element),
	}
}

func (c *blockCache) get(path string, blockOffset uint64) ([]sstableEntry, bool) {
	if c == nil {
		return nil, false
	}
	key := blockCacheKey{path: path, blockOffset: blockOffset}
	c.mu.Lock()
	elem, ok := c.items[key]
	if ok {
		c.ll.MoveToFront(elem)
		entries := elem.Value.(*cachedBlock).entries
		c.mu.Unlock()
		c.hits.Add(1)
		return entries, true
	}
	c.mu.Unlock()
	c.misses.Add(1)
	return nil, false
}

func (c *blockCache) put(path string, blockOffset uint64, entries []sstableEntry, approxBytes int) {
	if c == nil || approxBytes <= 0 || approxBytes > c.maxBytes/2 {
		return
	}
	key := blockCacheKey{path: path, blockOffset: blockOffset}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		c.ll.MoveToFront(elem)
		old := elem.Value.(*cachedBlock)
		c.used += approxBytes - old.bytes
		old.entries = entries
		old.bytes = approxBytes
	} else {
		cb := &cachedBlock{key: key, entries: entries, bytes: approxBytes}
		elem := c.ll.PushFront(cb)
		c.items[key] = elem
		c.used += approxBytes
	}
	for c.used > c.maxBytes && c.ll.Len() > 0 {
		back := c.ll.Back()
		if back == nil {
			break
		}
		cb := back.Value.(*cachedBlock)
		delete(c.items, cb.key)
		c.ll.Remove(back)
		c.used -= cb.bytes
	}
}

func (c *blockCache) evictFile(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, elem := range c.items {
		if k.path == path {
			cb := elem.Value.(*cachedBlock)
			c.used -= cb.bytes
			delete(c.items, k)
			c.ll.Remove(elem)
		}
	}
}
