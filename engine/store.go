package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	memTableFlushThreshold = 64 * 1024 // approximate bytes before flushing a run
)

// errNotFound is returned by Get when the key does not exist (or was deleted).
var errNotFound = errors.New("key not found")

// KVPair is a key-value pair returned by scans.
type KVPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// LSMStore is a persistent LSM-tree key-value store: writes go to a
// write-ahead log and an in-memory memtable, and are periodically flushed to
// sorted SSTable runs on disk. Reads check the memtable first, then runs from
// newest to oldest. Compaction merges runs and drops obsolete tombstones.
type LSMStore struct {
	mu     sync.RWMutex
	dir    string
	mem    *MemTable
	wal    *WAL
	runs   []*SSTable // runs[0] is the newest run
	nextID uint64
	closed bool
}

// Open creates or recovers a store in dir. The WAL is replayed into the
// memtable and every existing SSTable run is loaded.
func Open(dir string) (*LSMStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	wal, err := openWAL(filepath.Join(dir, "wal.log"))
	if err != nil {
		return nil, err
	}
	entries, err := wal.Replay()
	if err != nil {
		wal.Close()
		return nil, err
	}
	runs, nextID, err := loadRuns(dir)
	if err != nil {
		wal.Close()
		return nil, err
	}
	s := &LSMStore{
		dir:    dir,
		mem:    newMemTable(),
		wal:    wal,
		runs:   runs,
		nextID: nextID,
	}
	for _, e := range entries {
		if e.Op == walDelOp {
			s.mem.Put(e.Key, nil, true)
		} else {
			s.mem.Put(e.Key, []byte(e.Value), false)
		}
	}
	return s, nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func loadRuns(dir string) ([]*SSTable, uint64, error) {
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var paths []string
	var nextID uint64
	for _, e := range dirEntries {
		name := e.Name()
		if len(name) < 12 || name[:4] != "sst-" || name[len(name)-4:] != ".sst" {
			continue
		}
		var id uint64
		if _, err := fmt.Sscanf(name[4:len(name)-4], "%016d", &id); err != nil {
			continue
		}
		if id >= nextID {
			nextID = id + 1
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths) // oldest first
	runs := make([]*SSTable, 0, len(paths))
	for _, p := range paths {
		run, err := openSSTable(p)
		if err != nil {
			return nil, 0, err
		}
		runs = append(runs, run)
	}
	// runs[0] must be newest: reverse so newest runs lead the slice.
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	return runs, nextID, nil
}

// Set durably stores key -> value. The write is fsync'd to the WAL before it
// becomes visible.
func (s *LSMStore) Set(key, value []byte) error {
	if len(key) == 0 || len(key) > 4096 {
		return errors.New("key must be 1–4096 bytes")
	}
	if len(value) > 16<<20 {
		return errors.New("value exceeds 16 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}
	if err := s.wal.Append(walSetOp, string(key), string(value)); err != nil {
		return err
	}
	s.mem.Put(string(key), value, false)
	if s.mem.ApproxSize() >= memTableFlushThreshold {
		return s.flushLocked()
	}
	return nil
}

// Delete marks key as deleted. The tombstone is durable once this returns.
func (s *LSMStore) Delete(key []byte) error {
	if len(key) == 0 || len(key) > 4096 {
		return errors.New("key must be 1–4096 bytes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}
	if err := s.wal.Append(walDelOp, string(key), ""); err != nil {
		return err
	}
	s.mem.Put(string(key), nil, true)
	if s.mem.ApproxSize() >= memTableFlushThreshold {
		return s.flushLocked()
	}
	return nil
}

// Get returns the value for key, or errNotFound when the key is absent or
// deleted. The memtable is consulted first, then runs from newest to oldest.
func (s *LSMStore) Get(key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, false, errors.New("store is closed")
	}
	if value, deleted, ok := s.mem.Get(key); ok {
		if deleted {
			return nil, false, errNotFound
		}
		return value, true, nil
	}
	for i := 0; i < len(s.runs); i++ {
		value, deleted, ok, err := s.runs[i].get(key)
		if err != nil {
			return nil, false, err
		}
		if ok {
			if deleted {
				return nil, false, errNotFound
			}
			return value, true, nil
		}
	}
	return nil, false, errNotFound
}

// Scan returns up to limit keys with the given prefix, in key order, with
// deletions applied. Older runs are applied first so newer writes shadow
// older ones across runs and the memtable; a set always resurrects a key
// that an older run tombstoned. A limit of 0 means no limit.
func (s *LSMStore) Scan(prefix string, limit int) ([]KVPair, error) {
	return s.ScanAfter(prefix, "", limit)
}

// ScanAfter excludes keys <= after, while retaining the namespace prefix.
func (s *LSMStore) ScanAfter(prefix, after string, limit int) ([]KVPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errors.New("store is closed")
	}
	merged := make(map[string][]byte)
	for i := len(s.runs) - 1; i >= 0; i-- {
		entries, err := s.runs[i].scanPrefix(prefix)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.key <= after {
				continue
			}
			if e.op == walDelOp {
				delete(merged, e.key)
			} else {
				merged[e.key] = e.value
			}
		}
	}
	for k, v := range s.mem.Snapshot() {
		if !strings.HasPrefix(k, prefix) || k <= after {
			continue
		}
		if v.deleted {
			delete(merged, k)
		} else {
			merged[k] = v.value
		}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]KVPair, 0, len(keys))
	for _, k := range keys {
		out = append(out, KVPair{Key: k, Value: string(merged[k])})
	}
	return out, nil
}

// flushLocked writes the memtable to a new SSTable run, then truncates the
// WAL. Callers must hold the write lock.
func (s *LSMStore) flushLocked() error {
	snapshot := s.mem.Snapshot()
	if len(snapshot) == 0 {
		return nil
	}
	id := s.nextID
	s.nextID++
	path := filepath.Join(s.dir, fmt.Sprintf("sst-%016d.sst", id))

	entries := make([]sstableEntry, 0, len(snapshot))
	for k, v := range snapshot {
		if v.deleted {
			entries = append(entries, sstableEntry{op: walDelOp, key: k})
		} else {
			entries = append(entries, sstableEntry{op: walSetOp, key: k, value: v.value})
		}
	}
	run, err := buildSSTable(path, entries)
	if err != nil {
		return err
	}
	s.runs = append([]*SSTable{run}, s.runs...)
	if err := s.wal.Truncate(); err != nil {
		return err
	}
	s.mem.Reset()
	return nil
}

// Compact merges all runs and the memtable into a single run, dropping
// tombstones for keys that no longer exist anywhere, then removes the old
// run files.
func (s *LSMStore) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}
	merged := make(map[string]memEntry)
	for i := len(s.runs) - 1; i >= 0; i-- {
		entries, err := s.runs[i].allEntries()
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.op == walDelOp {
				delete(merged, e.key)
			} else {
				merged[e.key] = memEntry{value: e.value, deleted: false}
			}
		}
	}
	for k, v := range s.mem.Snapshot() {
		merged[k] = v
	}

	entries := make([]sstableEntry, 0, len(merged))
	for k, v := range merged {
		if v.deleted {
			entries = append(entries, sstableEntry{op: walDelOp, key: k})
		} else {
			entries = append(entries, sstableEntry{op: walSetOp, key: k, value: v.value})
		}
	}
	id := s.nextID
	s.nextID++
	path := filepath.Join(s.dir, fmt.Sprintf("sst-%016d.sst", id))
	run, err := buildSSTable(path, entries)
	if err != nil {
		return err
	}
	for _, old := range s.runs {
		if err := os.Remove(old.path); err != nil {
			return err
		}
	}
	s.runs = []*SSTable{run}
	if err := syncDir(s.dir); err != nil {
		return err
	}
	if err := s.wal.Truncate(); err != nil {
		return err
	}
	s.mem.Reset()
	return nil
}

// Close flushes the memtable to disk and closes the WAL.
func (s *LSMStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if err := s.flushLocked(); err != nil {
		s.closed = true
		return s.wal.Close()
	}
	s.closed = true
	return s.wal.Close()
}
