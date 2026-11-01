package engine

import (
	"sort"
	"strings"
	"sync"
)

// memEntry holds one recent mutation in the memtable.
type memEntry struct {
	value   []byte
	deleted bool
}

// MemTable is the write-optimized in-memory layer of the LSM store.
type MemTable struct {
	mu    sync.RWMutex
	items map[string]memEntry
	size  int // accurate live byte size of all entries
}

func newMemTable() *MemTable {
	return &MemTable{items: make(map[string]memEntry)}
}

func (m *MemTable) Put(key string, value []byte, deleted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, exists := m.items[key]; exists {
		m.size -= len(key) + len(prev.value)
	}
	var copied []byte
	if !deleted && value != nil {
		copied = make([]byte, len(value))
		copy(copied, value)
	}
	m.items[key] = memEntry{value: copied, deleted: deleted}
	m.size += len(key) + len(copied)
}

func (m *MemTable) Get(key string) (value []byte, deleted bool, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.items[key]
	if !ok {
		return nil, false, false
	}
	return e.value, e.deleted, true
}

// Snapshot returns a copy of the current entries, safe to iterate without
// holding the memtable lock.
func (m *MemTable) Snapshot() map[string]memEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]memEntry, len(m.items))
	for k, v := range m.items {
		out[k] = v
	}
	return out
}

// SortedRange returns sorted memtable entries matching prefix and strictly > after
// (or >= startKey if inclusive is true).
func (m *MemTable) SortedRange(prefix, after string, inclusive bool) []sstableEntry {
	m.mu.RLock()
	out := make([]sstableEntry, 0, 64)
	for k, v := range m.items {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		if after != "" {
			if inclusive {
				if k < after {
					continue
				}
			} else if k <= after {
				continue
			}
		}
		if v.deleted {
			out = append(out, sstableEntry{op: walDelOp, key: k})
		} else {
			out = append(out, sstableEntry{op: walSetOp, key: k, value: v.value})
		}
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].key < out[j].key
	})
	return out
}

func (m *MemTable) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

func (m *MemTable) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = make(map[string]memEntry)
	m.size = 0
}

func (m *MemTable) ApproxSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.size
}
