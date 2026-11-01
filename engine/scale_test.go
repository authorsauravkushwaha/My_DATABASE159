package engine

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestBlockCompressionBloomCacheAndLargeDataset(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithOptions(dir, Options{
		MemTableBytes:        128 * 1024,
		BlockCacheBytes:      4 << 20,
		MaxSSTableBytes:      512 * 1024,
		AutoCompactThreshold: 6,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	const totalRecords = 5000
	batch := make([]BatchOp, 0, 500)
	for i := 0; i < totalRecords; i++ {
		key := fmt.Sprintf("p:61636d65:c:6576656e7473:r:%08d", i)
		val := fmt.Sprintf(`{"event_type":"checkout.completed","customer_tier":"enterprise","region":"ap-south-1","sequence":%d,"notes":"%s"}`,
			i, strings.Repeat("high-density-telemetry-payload-", 6))
		batch = append(batch, BatchOp{Key: key, Value: val})
		if len(batch) == 500 {
			if err := s.WriteBatch(batch); err != nil {
				t.Fatalf("write batch at %d: %v", i, err)
			}
			batch = batch[:0]
		}
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}

	stats := s.Stats()
	if stats.CompressionRatio < 2.5 {
		t.Fatalf("expected strong block compression (>2.5x), got %.2fx (logical=%d disk=%d)",
			stats.CompressionRatio, stats.LogicalBytes, stats.DiskBytes)
	}
	if stats.SSTables < 2 {
		t.Fatalf("expected multi-file split SSTables under 512KiB cap, got %d", stats.SSTables)
	}

	// Verify point lookups hit cache and Bloom filters skip non-existent keys.
	sampleKey := "p:61636d65:c:6576656e7473:r:00002500"
	val1, ok, err := s.Get(sampleKey)
	if err != nil || !ok || !strings.Contains(string(val1), `"sequence":2500`) {
		t.Fatalf("first get failed: ok=%v err=%v val=%s", ok, err, val1)
	}
	val2, ok, err := s.Get(sampleKey)
	if err != nil || !ok || string(val1) != string(val2) {
		t.Fatalf("cached get mismatch")
	}
	for i := 0; i < 200; i++ {
		missing := fmt.Sprintf("p:61636d65:c:6576656e7473:r:%08d-absent", i*20)
		if _, _, err := s.Get(missing); !errors.Is(err, errNotFound) {
			t.Fatalf("expected errNotFound for %s, got %v", missing, err)
		}
	}
	afterStats := s.Stats()
	if afterStats.CacheHits == 0 {
		t.Fatalf("expected block cache hits > 0, got %+v", afterStats)
	}
	if afterStats.BloomChecks == 0 || afterStats.BloomSkips == 0 {
		t.Fatalf("expected bloom filter skips > 0, got %+v", afterStats)
	}

	// Verify streaming cursor scan across all 5,000 records in pages of 300.
	seen := 0
	cursor := ""
	prefix := "p:61636d65:c:6576656e7473:r:"
	for {
		page, err := s.ScanAfter(prefix, cursor, 300)
		if err != nil {
			t.Fatalf("scan after %q: %v", cursor, err)
		}
		if len(page) == 0 {
			break
		}
		for idx, kv := range page {
			wantKey := fmt.Sprintf("p:61636d65:c:6576656e7473:r:%08d", seen+idx)
			if kv.Key != wantKey {
				t.Fatalf("out-of-order key at %d: got %s want %s", seen+idx, kv.Key, wantKey)
			}
		}
		seen += len(page)
		cursor = page[len(page)-1].Key
	}
	if seen != totalRecords {
		t.Fatalf("cursor scan count=%d want=%d", seen, totalRecords)
	}
}

func TestCorruptSSTableBlockChecksumFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("alpha"), []byte(strings.Repeat("payload-integrity-", 30))); err != nil {
		t.Fatal(err)
	}
	if err := s.flushLocked(); err != nil {
		t.Fatal(err)
	}
	sstPath := s.runs[0].path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Flip one byte inside the first data block payload (right after 4-byte KSS2 magic + 9-byte block header).
	raw, err := os.ReadFile(sstPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[14] ^= 0xff
	if err := os.WriteFile(sstPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, _, err := reopened.Get("alpha"); err == nil || errors.Is(err, errNotFound) {
		t.Fatalf("expected block checksum error, got %v", err)
	}
}

func TestExclusiveDirectoryLockRejectsSecondOwner(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()

	if s2, err := Open(dir); err == nil {
		_ = s2.Close()
		t.Fatal("expected second concurrent Open on locked directory to fail")
	}
}

func TestDeletePrefixAndShardedStoreIsolation(t *testing.T) {
	dir := t.TempDir()
	ss, err := OpenSharded(dir, 4, Options{MemTableBytes: 64 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	ops := make([]BatchOp, 0, 120)
	for i := 0; i < 60; i++ {
		ops = append(ops,
			BatchOp{Key: fmt.Sprintf("p:p1:c:c1:r:%04d", i), Value: `{"n":1}`},
			BatchOp{Key: fmt.Sprintf("p:p1:c:c2:r:%04d", i), Value: `{"n":2}`},
		)
	}
	if err := ss.WriteBatch(ops); err != nil {
		t.Fatalf("sharded batch: %v", err)
	}

	c1Rows, err := ss.Scan("p:p1:c:c1:r:", 0)
	if err != nil || len(c1Rows) != 60 {
		t.Fatalf("expected 60 c1 rows, got %d err=%v", len(c1Rows), err)
	}

	deleted, err := ss.DeletePrefix("p:p1:c:c1:r:")
	if err != nil || deleted != 60 {
		t.Fatalf("delete prefix deleted=%d err=%v", deleted, err)
	}
	afterC1, err := ss.Scan("p:p1:c:c1:r:", 0)
	if err != nil || len(afterC1) != 0 {
		t.Fatalf("expected c1 empty after DeletePrefix, got %d", len(afterC1))
	}
	afterC2, err := ss.Scan("p:p1:c:c2:r:", 0)
	if err != nil || len(afterC2) != 60 {
		t.Fatalf("expected c2 untouched with 60 rows, got %d", len(afterC2))
	}
	if err := ss.Compact(); err != nil {
		t.Fatalf("sharded compact: %v", err)
	}
	if ss.Stats().Shards != 4 {
		t.Fatalf("expected 4 shards in stats")
	}
}
