package engine

import "sync"

// memEntry holds one recent mutation in the memtable.
type memEntry struct {
	value   []byte
	deleted bool
}

// MemTable is the write-optimized in-memory layer of the LSM store.
type MemTable struct {
	mu    sync.RWMutex
	items map[string]memEntry
	size  int // approximate byte size of all entries
}

func newMemTable() *MemTable {
	return &MemTable{items: make(map[string]memEntry)}
}

func (m *MemTable) Put(key string, value []byte, deleted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = memEntry{value: append([]byte(nil), value...), deleted: deleted}
	m.size += len(key) + len(value)
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
