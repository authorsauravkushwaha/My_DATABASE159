package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	defaultMemTableThreshold   = 2 << 20  // 2 MiB default write buffer before flushing
	defaultBlockCacheBytes     = 16 << 20 // 16 MiB decoded block cache
	defaultMaxSSTableBytes     = 64 << 20 // 64 MiB target max SSTable file size
	defaultAutoCompactRuns     = 12       // compact automatically once run count reaches 12
	deletePrefixBatchChunkSize = 2048
)

// errNotFound is returned by Get when the key does not exist (or was deleted).
var errNotFound = errors.New("key not found")

// Options tunes memory, block caching, compaction thresholds, and SSTable sizing.
type Options struct {
	MemTableBytes        int
	BlockCacheBytes      int
	MaxSSTableBytes      uint64
	AutoCompactThreshold int
}

func normalizeOptions(opts Options) Options {
	if opts.MemTableBytes <= 0 {
		opts.MemTableBytes = defaultMemTableThreshold
	}
	if opts.BlockCacheBytes <= 0 {
		opts.BlockCacheBytes = defaultBlockCacheBytes
	}
	if opts.MaxSSTableBytes == 0 {
		opts.MaxSSTableBytes = defaultMaxSSTableBytes
	}
	if opts.AutoCompactThreshold <= 0 {
		opts.AutoCompactThreshold = defaultAutoCompactRuns
	}
	return opts
}

// KVPair is a key-value pair returned by scans.
type KVPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// BatchOp is one Set or Delete mutation inside an atomic WriteBatch.
type BatchOp struct {
	Delete bool   `json:"delete,omitempty"`
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
}

// StoreStats reports real measured storage, compression, Bloom filter, and cache metrics.
type StoreStats struct {
	Shards           int     `json:"shards"`
	SSTables         int     `json:"sstables"`
	MemTableBytes    int64   `json:"memtable_bytes"`
	MemTableKeys     int64   `json:"memtable_keys"`
	WALBytes         int64   `json:"wal_bytes"`
	LogicalBytes     uint64  `json:"logical_bytes"`
	DiskBytes        uint64  `json:"disk_bytes"`
	CompressionRatio float64 `json:"compression_ratio"`
	EstimatedKeys    uint64  `json:"estimated_keys"`
	BloomChecks      uint64  `json:"bloom_checks"`
	BloomSkips       uint64  `json:"bloom_skips"`
	CacheHits        uint64  `json:"cache_hits"`
	CacheMisses      uint64  `json:"cache_misses"`
	Compactions      uint64  `json:"compactions"`
}

// KeyStore is implemented by both single-directory LSMStore and multi-shard ShardedStore.
type KeyStore interface {
	Set(key, value []byte) error
	Delete(key []byte) error
	WriteBatch(ops []BatchOp) error
	DeletePrefix(prefix string) (int, error)
	Get(key string) ([]byte, bool, error)
	Scan(prefix string, limit int) ([]KVPair, error)
	ScanAfter(prefix, after string, limit int) ([]KVPair, error)
	Compact() error
	Stats() StoreStats
	Serve(addr string) error
	Close() error
}

// LSMStore is a persistent, block-compressed LSM-tree key-value store:
// writes go to a binary CRC-protected WAL and in-memory memtable, flush to
// DEFLATE-compressed KSS2 SSTables with Bloom filters and sparse block indexes,
// and scan/compact in constant memory using streaming k-way merge iterators.
type LSMStore struct {
	mu          sync.RWMutex
	dir         string
	opts        Options
	lock        *dirLock
	mem         *MemTable
	wal         *WAL
	cache       *blockCache
	runs        []*SSTable // runs[0] is the newest run
	nextID      uint64
	closed      bool
	bloomChecks atomic.Uint64
	bloomSkips  atomic.Uint64
	compactions atomic.Uint64
}

// Open creates or recovers a store in dir with default high-capacity options.
func Open(dir string) (*LSMStore, error) {
	return OpenWithOptions(dir, Options{})
}

// OpenWithOptions creates or recovers a store in dir with custom tuning options.
func OpenWithOptions(dir string, opts Options) (*LSMStore, error) {
	opts = normalizeOptions(opts)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dlock, err := acquireDirLock(dir)
	if err != nil {
		return nil, err
	}
	cleanTempFiles(dir)

	s := &LSMStore{
		dir:   dir,
		opts:  opts,
		lock:  dlock,
		mem:   newMemTable(),
		cache: newBlockCache(opts.BlockCacheBytes),
	}
	wal, err := openWAL(filepath.Join(dir, "wal.log"), func() {
		_ = dlock.release()
	})
	if err != nil {
		_ = dlock.release()
		return nil, err
	}
	s.wal = wal

	entries, err := wal.Replay()
	if err != nil {
		_ = wal.Close()
		return nil, err
	}
	runs, nextID, err := loadRuns(dir, s.cache, &s.bloomChecks, &s.bloomSkips)
	if err != nil {
		_ = wal.Close()
		return nil, err
	}
	s.runs = runs
	s.nextID = nextID

	for _, e := range entries {
		if e.Op == walDelOp {
			s.mem.Put(e.Key, nil, true)
		} else {
			s.mem.Put(e.Key, e.Value, false)
		}
	}
	return s, nil
}

func cleanTempFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func loadRuns(dir string, cache *blockCache, bloomChecks, bloomSkips *atomic.Uint64) ([]*SSTable, uint64, error) {
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
		run, err := openSSTableWithCache(p, cache, bloomChecks, bloomSkips)
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

func validateKV(key, value []byte, isDelete bool) error {
	if len(key) == 0 || len(key) > 4096 {
		return errors.New("key must be 1–4096 bytes")
	}
	if !isDelete && len(value) > 16<<20 {
		return errors.New("value exceeds 16 MiB")
	}
	return nil
}

// Set durably stores key -> value.
func (s *LSMStore) Set(key, value []byte) error {
	if err := validateKV(key, value, false); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}
	if err := s.wal.Append(walSetOp, string(key), value); err != nil {
		return err
	}
	s.mem.Put(string(key), value, false)
	return s.maybeFlushAndCompactLocked()
}

// Delete marks key as deleted with a durable tombstone.
func (s *LSMStore) Delete(key []byte) error {
	if err := validateKV(key, nil, true); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}
	if err := s.wal.Append(walDelOp, string(key), nil); err != nil {
		return err
	}
	s.mem.Put(string(key), nil, true)
	return s.maybeFlushAndCompactLocked()
}

// WriteBatch atomically validates and commits multiple Set/Delete mutations
// in a single fsync'd WAL frame.
func (s *LSMStore) WriteBatch(ops []BatchOp) error {
	if len(ops) == 0 {
		return nil
	}
	walEntries := make([]walEntry, 0, len(ops))
	for _, op := range ops {
		if err := validateKV([]byte(op.Key), []byte(op.Value), op.Delete); err != nil {
			return err
		}
		if op.Delete {
			walEntries = append(walEntries, walEntry{Op: walDelOp, Key: op.Key})
		} else {
			walEntries = append(walEntries, walEntry{Op: walSetOp, Key: op.Key, Value: []byte(op.Value)})
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}
	if err := s.wal.AppendBatch(walEntries); err != nil {
		return err
	}
	for _, e := range walEntries {
		if e.Op == walDelOp {
			s.mem.Put(e.Key, nil, true)
		} else {
			s.mem.Put(e.Key, e.Value, false)
		}
		if s.mem.ApproxSize() >= s.opts.MemTableBytes {
			if err := s.flushLocked(); err != nil {
				return err
			}
		}
	}
	if len(s.runs) >= s.opts.AutoCompactThreshold {
		return s.compactLocked()
	}
	return nil
}

// DeletePrefix deletes all live keys under prefix (e.g. a deleted collection
// or project namespace) in bounded streaming batches.
func (s *LSMStore) DeletePrefix(prefix string) (int, error) {
	if prefix == "" {
		return 0, errors.New("prefix must not be empty")
	}
	totalDeleted := 0
	after := ""
	for {
		kvs, err := s.ScanAfter(prefix, after, deletePrefixBatchChunkSize)
		if err != nil {
			return totalDeleted, err
		}
		if len(kvs) == 0 {
			break
		}
		ops := make([]BatchOp, len(kvs))
		for i, kv := range kvs {
			ops[i] = BatchOp{Delete: true, Key: kv.Key}
		}
		after = kvs[len(kvs)-1].Key
		if err := s.WriteBatch(ops); err != nil {
			return totalDeleted, err
		}
		totalDeleted += len(ops)
		if len(kvs) < deletePrefixBatchChunkSize {
			break
		}
	}
	return totalDeleted, nil
}

func (s *LSMStore) maybeFlushAndCompactLocked() error {
	if s.mem.ApproxSize() >= s.opts.MemTableBytes {
		if err := s.flushLocked(); err != nil {
			return err
		}
		if len(s.runs) >= s.opts.AutoCompactThreshold {
			return s.compactLocked()
		}
	}
	return nil
}

// Get returns the value for key, or errNotFound when absent or deleted.
// Point lookups check the memtable, then consult each SSTable's key bounds
// and Bloom filter before touching any compressed disk block.
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
		out := make([]byte, len(value))
		copy(out, value)
		return out, true, nil
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
// deletions applied. A limit of 0 means no limit.
func (s *LSMStore) Scan(prefix string, limit int) ([]KVPair, error) {
	return s.ScanAfter(prefix, "", limit)
}

// ScanAfter streams up to limit keys > after matching prefix using a constant
// O(R) memory k-way merge iterator across the memtable and SSTable runs.
func (s *LSMStore) ScanAfter(prefix, after string, limit int) ([]KVPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errors.New("store is closed")
	}

	iters := make([]kvIterator, 0, 1+len(s.runs))
	iters = append(iters, newSliceIterator(s.mem.SortedRange(prefix, after, false)))
	for _, run := range s.runs {
		iters = append(iters, newSSTableIterator(run, prefix, after, false))
	}

	merged, err := newMergeIterator(iters, true)
	if err != nil {
		return nil, err
	}
	defer merged.Close()

	capHint := 64
	if limit > 0 && limit < 1024 {
		capHint = limit
	}
	out := make([]KVPair, 0, capHint)
	for merged.Next() {
		e := merged.Entry()
		out = append(out, KVPair{Key: e.key, Value: string(e.value)})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if err := merged.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// flushLocked writes the memtable to a new compressed KSS2 SSTable run, then
// truncates the WAL. Callers must hold s.mu.
func (s *LSMStore) flushLocked() error {
	snapshot := s.mem.Snapshot()
	if len(snapshot) == 0 {
		return nil
	}
	id := s.nextID
	s.nextID++
	path := filepath.Join(s.dir, fmt.Sprintf("sst-%016d.sst", id))

	entries := sortedSnapshotEntries(snapshot)
	run, err := buildSSTableWithMeta(path, 0, entries, s.cache, &s.bloomChecks, &s.bloomSkips)
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

// Compact streams all SSTable runs and the memtable through a constant-memory
// k-way merge iterator into bounded compressed SSTables, dropping obsolete
// tombstones and removing superseded run files.
func (s *LSMStore) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("store is closed")
	}
	return s.compactLocked()
}

func (s *LSMStore) compactLocked() error {
	iters := make([]kvIterator, 0, 1+len(s.runs))
	iters = append(iters, newSliceIterator(s.mem.SortedRange("", "", true)))
	for _, run := range s.runs {
		iters = append(iters, newSSTableIterator(run, "", "", true))
	}

	merged, err := newMergeIterator(iters, true)
	if err != nil {
		return err
	}

	var newRuns []*SSTable
	var currentWriter *sstableWriter
	var currentBytes uint64

	cleanupNewRuns := func() {
		if currentWriter != nil {
			currentWriter.Abort()
			currentWriter = nil
		}
		for _, r := range newRuns {
			_ = os.Remove(r.path)
		}
	}

	for merged.Next() {
		e := merged.Entry()
		if currentWriter == nil {
			id := s.nextID
			s.nextID++
			path := filepath.Join(s.dir, fmt.Sprintf("sst-%016d.sst", id))
			w, err := newSSTableWriter(path, 1, s.cache, &s.bloomChecks, &s.bloomSkips)
			if err != nil {
				_ = merged.Close()
				cleanupNewRuns()
				return err
			}
			currentWriter = w
			currentBytes = 0
		}
		if err := currentWriter.Add(e); err != nil {
			_ = merged.Close()
			cleanupNewRuns()
			return err
		}
		currentBytes += uint64(len(e.key) + len(e.value))
		if currentBytes >= s.opts.MaxSSTableBytes {
			run, err := currentWriter.Finish()
			currentWriter = nil
			if err != nil {
				_ = merged.Close()
				cleanupNewRuns()
				return err
			}
			newRuns = append(newRuns, run)
		}
	}
	if err := merged.Err(); err != nil {
		_ = merged.Close()
		cleanupNewRuns()
		return err
	}
	if err := merged.Close(); err != nil {
		cleanupNewRuns()
		return err
	}
	if currentWriter != nil {
		run, err := currentWriter.Finish()
		currentWriter = nil
		if err != nil {
			cleanupNewRuns()
			return err
		}
		newRuns = append(newRuns, run)
	}

	// Reverse newRuns so runs[0] remains highest ID (newest), though compacted
	// runs have disjoint key ranges anyway.
	for i, j := 0, len(newRuns)-1; i < j; i, j = i+1, j-1 {
		newRuns[i], newRuns[j] = newRuns[j], newRuns[i]
	}

	oldRuns := s.runs
	for _, old := range oldRuns {
		if s.cache != nil {
			s.cache.evictFile(old.path)
		}
		if err := os.Remove(old.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	s.runs = newRuns
	if err := syncDir(s.dir); err != nil {
		return err
	}
	if err := s.wal.Truncate(); err != nil {
		return err
	}
	s.mem.Reset()
	s.compactions.Add(1)
	return nil
}

// Stats returns live storage capacity, compression ratio, Bloom filter, and
// block cache telemetry.
func (s *LSMStore) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var logical, disk, estKeys uint64
	for _, r := range s.runs {
		logical += r.logicalBytes
		disk += r.diskBytes
		estKeys += r.entryCount
	}
	memBytes := int64(s.mem.ApproxSize())
	memKeys := int64(s.mem.Len())
	walBytes := s.wal.Size()
	totalLogical := logical + uint64(memBytes)
	totalDisk := disk + uint64(walBytes)
	ratio := 1.0
	if disk > 0 && logical > 0 {
		ratio = float64(logical) / float64(disk)
	}
	var hits, misses uint64
	if s.cache != nil {
		hits = s.cache.hits.Load()
		misses = s.cache.misses.Load()
	}
	return StoreStats{
		Shards:           1,
		SSTables:         len(s.runs),
		MemTableBytes:    memBytes,
		MemTableKeys:     memKeys,
		WALBytes:         walBytes,
		LogicalBytes:     totalLogical,
		DiskBytes:        totalDisk,
		CompressionRatio: ratio,
		EstimatedKeys:    estKeys + uint64(memKeys),
		BloomChecks:      s.bloomChecks.Load(),
		BloomSkips:       s.bloomSkips.Load(),
		CacheHits:        hits,
		CacheMisses:      misses,
		Compactions:      s.compactions.Load(),
	}
}

// Close flushes the memtable to disk, closes the WAL, and releases the directory lock.
func (s *LSMStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	flushErr := s.flushLocked()
	s.closed = true
	walErr := s.wal.Close()
	lockErr := s.lock.release()
	if flushErr != nil {
		return flushErr
	}
	if walErr != nil {
		return walErr
	}
	return lockErr
}

// ShardedStore partitions keys across N independent LSMStore shards so writes,
// WAL fsyncs, memtable flushes, and compactions scale across multiple cores.
// Keys with a Kestrel collection namespace ("p:<proj>:c:<coll>:r:...") hash by
// collection prefix so per-collection scans hit a single shard.
type ShardedStore struct {
	shards []*LSMStore
}

// OpenSharded opens numShards independent LSM shards inside dir.
func OpenSharded(dir string, numShards int, opts Options) (*ShardedStore, error) {
	if numShards < 1 || numShards > 64 {
		return nil, errors.New("numShards must be between 1 and 64")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	perShardOpts := normalizeOptions(opts)
	// Divide memory budgets cleanly across shards with sensible minimums.
	if numShards > 1 {
		if perShardOpts.BlockCacheBytes/numShards >= 1<<20 {
			perShardOpts.BlockCacheBytes /= numShards
		}
	}
	shards := make([]*LSMStore, 0, numShards)
	for i := 0; i < numShards; i++ {
		shardDir := filepath.Join(dir, fmt.Sprintf("shard-%02d", i))
		st, err := OpenWithOptions(shardDir, perShardOpts)
		if err != nil {
			for _, openSt := range shards {
				_ = openSt.Close()
			}
			return nil, err
		}
		shards = append(shards, st)
	}
	return &ShardedStore{shards: shards}, nil
}

// routingPrefix extracts "p:<proj>:c:<coll>:" when present so all records of a
// collection live on the same shard for single-shard ordered cursor scans.
func routingPrefix(keyOrPrefix string) (string, bool) {
	if !strings.HasPrefix(keyOrPrefix, "p:") {
		return keyOrPrefix, false
	}
	idx := strings.Index(keyOrPrefix, ":r:")
	if idx > 0 {
		return keyOrPrefix[:idx+3], true
	}
	return keyOrPrefix, false
}

func (ss *ShardedStore) shardIndex(routingKey string) int {
	h1, _ := bloomHash(routingKey)
	return int(h1 % uint32(len(ss.shards)))
}

func (ss *ShardedStore) shardForKey(key string) *LSMStore {
	rk, _ := routingPrefix(key)
	return ss.shards[ss.shardIndex(rk)]
}

func (ss *ShardedStore) Set(key, value []byte) error {
	return ss.shardForKey(string(key)).Set(key, value)
}

func (ss *ShardedStore) Delete(key []byte) error {
	return ss.shardForKey(string(key)).Delete(key)
}

func (ss *ShardedStore) Get(key string) ([]byte, bool, error) {
	return ss.shardForKey(key).Get(key)
}

func (ss *ShardedStore) WriteBatch(ops []BatchOp) error {
	if len(ops) == 0 {
		return nil
	}
	if len(ss.shards) == 1 {
		return ss.shards[0].WriteBatch(ops)
	}
	grouped := make(map[int][]BatchOp)
	for _, op := range ops {
		rk, _ := routingPrefix(op.Key)
		idx := ss.shardIndex(rk)
		grouped[idx] = append(grouped[idx], op)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(grouped))
	for idx, shardOps := range grouped {
		wg.Add(1)
		go func(st *LSMStore, batch []BatchOp) {
			defer wg.Done()
			if err := st.WriteBatch(batch); err != nil {
				errCh <- err
			}
		}(ss.shards[idx], shardOps)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (ss *ShardedStore) Scan(prefix string, limit int) ([]KVPair, error) {
	return ss.ScanAfter(prefix, "", limit)
}

func (ss *ShardedStore) ScanAfter(prefix, after string, limit int) ([]KVPair, error) {
	if rk, scoped := routingPrefix(prefix); scoped {
		return ss.shards[ss.shardIndex(rk)].ScanAfter(prefix, after, limit)
	}
	var all []KVPair
	for _, st := range ss.shards {
		part, err := st.ScanAfter(prefix, after, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, part...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (ss *ShardedStore) DeletePrefix(prefix string) (int, error) {
	if rk, scoped := routingPrefix(prefix); scoped {
		return ss.shards[ss.shardIndex(rk)].DeletePrefix(prefix)
	}
	total := 0
	for _, st := range ss.shards {
		n, err := st.DeletePrefix(prefix)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (ss *ShardedStore) Compact() error {
	for _, st := range ss.shards {
		if err := st.Compact(); err != nil {
			return err
		}
	}
	return nil
}

func (ss *ShardedStore) Stats() StoreStats {
	agg := StoreStats{Shards: len(ss.shards)}
	for _, st := range ss.shards {
		part := st.Stats()
		agg.SSTables += part.SSTables
		agg.MemTableBytes += part.MemTableBytes
		agg.MemTableKeys += part.MemTableKeys
		agg.WALBytes += part.WALBytes
		agg.LogicalBytes += part.LogicalBytes
		agg.DiskBytes += part.DiskBytes
		agg.EstimatedKeys += part.EstimatedKeys
		agg.BloomChecks += part.BloomChecks
		agg.BloomSkips += part.BloomSkips
		agg.CacheHits += part.CacheHits
		agg.CacheMisses += part.CacheMisses
		agg.Compactions += part.Compactions
	}
	agg.CompressionRatio = 1.0
	if agg.DiskBytes > 0 && agg.LogicalBytes > 0 {
		agg.CompressionRatio = float64(agg.LogicalBytes) / float64(agg.DiskBytes)
	}
	return agg
}

func (ss *ShardedStore) Close() error {
	var firstErr error
	for _, st := range ss.shards {
		if err := st.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
