package engine

import (
	"os"
	"strings"
	"testing"
)

func tmpStore(t *testing.T) *LSMStore {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSetGetDelete(t *testing.T) {
	s := tmpStore(t)

	if err := s.Set([]byte("user:alice"), []byte(`{"name":"Alice"}`)); err != nil {
		t.Fatalf("set: %v", err)
	}
	value, _, err := s.Get("user:alice")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(value) != `{"name":"Alice"}` {
		t.Fatalf("unexpected value: %s", value)
	}

	if _, _, err := s.Get("user:bob"); err != errNotFound {
		t.Fatalf("expected errNotFound for missing key, got: %v", err)
	}

	if err := s.Delete([]byte("user:alice")); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := s.Get("user:alice"); err != errNotFound {
		t.Fatalf("expected errNotFound after delete, got: %v", err)
	}
}

func TestUpdateShadowsPrevious(t *testing.T) {
	s := tmpStore(t)

	if err := s.Set([]byte("k"), []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("k"), []byte("second")); err != nil {
		t.Fatal(err)
	}
	value, _, err := s.Get("k")
	if err != nil || string(value) != "second" {
		t.Fatalf("expected shadowed value, got %q err=%v", value, err)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set([]byte("durable"), []byte("survives restart")); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	value, _, err := s.Get("durable")
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if string(value) != "survives restart" {
		t.Fatalf("value not persisted: %q", value)
	}
}

func TestWALRecoveryWithoutFlush(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set([]byte("wal"), []byte("recover-me")); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Simulate a crash: close the WAL directly without flushing or truncating
	// it, then reopen and confirm the WAL replay recovers the write.
	if err := s.wal.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	value, _, err := s2.Get("wal")
	if err != nil {
		t.Fatalf("wal recovery: %v", err)
	}
	if string(value) != "recover-me" {
		t.Fatalf("waled value not recovered: %q", value)
	}
}

func TestScanPrefixAndLimit(t *testing.T) {
	s := tmpStore(t)

	keys := []string{"app:user:alice", "app:user:bob", "app:config:theme", "other:thing"}
	for _, k := range keys {
		if err := s.Set([]byte(k), []byte("value-of-"+k)); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	kvs, err := s.Scan("app:user:", 0)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(kvs) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(kvs))
	}
	if kvs[0].Key != "app:user:alice" || kvs[1].Key != "app:user:bob" {
		t.Fatalf("unexpected scan result: %+v", kvs)
	}

	kvs, err = s.Scan("app:", 1)
	if err != nil {
		t.Fatalf("scan limit: %v", err)
	}
	if len(kvs) != 1 {
		t.Fatalf("expected limit 1, got %d", len(kvs))
	}

	kvs, err = s.Scan("nope:", 0)
	if err != nil {
		t.Fatalf("scan empty: %v", err)
	}
	if len(kvs) != 0 {
		t.Fatalf("expected empty scan, got %d", len(kvs))
	}
}

func TestScanDeletesAreFiltered(t *testing.T) {
	s := tmpStore(t)

	if err := s.Set([]byte("gone"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete([]byte("gone")); err != nil {
		t.Fatal(err)
	}
	kvs, err := s.Scan("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(kvs) != 0 {
		t.Fatalf("tombstoned key should not scan: %+v", kvs)
	}
}

func TestPersistenceFlushesToRuns(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	value, _, err := s.Get("a")
	if err != nil || string(value) != "1" {
		t.Fatalf("flushed value missing: %q err=%v", value, err)
	}
	if len(s.runs) == 0 {
		t.Fatal("expected at least one SSTable run after flush+reopen")
	}
}

func TestCompactDropsTombstones(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"c:1", "c:2", "c:3"} {
		if err := s.Set([]byte(k), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete([]byte("c:2")); err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("c:2"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if value, _, err := s.Get("c:2"); err != nil || string(value) != "v2" {
		t.Fatalf("compact lost updated key: %q err=%v", value, err)
	}
	if value, _, err := s.Get("c:1"); err != nil || string(value) != "v" {
		t.Fatalf("compact lost untouched key: %q err=%v", value, err)
	}

	// Reopen: the compacted single run must persist.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after compact: %v", err)
	}
	defer s2.Close()
	if value, _, err := s2.Get("c:2"); err != nil || string(value) != "v2" {
		t.Fatalf("compacted run not persisted: %q err=%v", value, err)
	}

	// Old run files must have been removed.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var runFiles int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sst-") && strings.HasSuffix(e.Name(), ".sst") {
			runFiles++
		}
	}
	if runFiles != 1 {
		t.Fatalf("expected exactly one run file after compaction, got %d", runFiles)
	}
}
