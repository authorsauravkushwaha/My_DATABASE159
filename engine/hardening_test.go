package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClosedStoreRejectsMutations(t *testing.T) {
	s := tmpStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("x"), []byte("y")); err == nil {
		t.Fatal("accepted write after close")
	}
	if err := s.Delete([]byte("x")); err == nil {
		t.Fatal("accepted delete after close")
	}
	if err := s.Compact(); err == nil {
		t.Fatal("accepted compaction after close")
	}
}

func TestCorruptWALFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("x"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := s.wal.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wal.log")
	data, _ := os.ReadFile(path)
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if recovered, err := Open(dir); err == nil {
		recovered.Close()
		t.Fatal("silently accepted corrupt WAL")
	}
}

func TestTornWALTailRecoversThenAppends(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]byte("x"), []byte("safe")); err != nil {
		t.Fatal(err)
	}
	if err := s.wal.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wal.log")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, 0, 0, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Set([]byte("y"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if value, _, err := s.Get("x"); err != nil || string(value) != "safe" {
		t.Fatalf("lost committed record: %q %v", value, err)
	}
}

func TestMissingRunIsAnErrorNotAMiss(t *testing.T) {
	s := tmpStore(t)
	if err := s.Set([]byte("x"), []byte("y")); err != nil {
		t.Fatal(err)
	}
	if err := s.flushLocked(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.runs[0].path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get("x"); err == nil || err == errNotFound {
		t.Fatalf("expected I/O error, got %v", err)
	}
}

func TestScanAfterUsesFullKeyWithoutChangingPrefix(t *testing.T) {
	s := tmpStore(t)
	for _, key := range []string{"p:a:r:1", "p:a:r:2", "p:a:r:3", "p:b:r:1"} {
		if err := s.Set([]byte(key), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ScanAfter("p:a:r:", "p:a:r:1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Key != "p:a:r:2" || rows[1].Key != "p:a:r:3" {
		t.Fatalf("unexpected cursor result: %+v", rows)
	}
}

func TestBatchFlushPersistsEntireBatch(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenWithOptions(dir, Options{MemTableBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	ops := []BatchOp{{Key: "first", Value: "one"}, {Key: "second", Value: "two"}}
	if err := s.WriteBatch(ops); err != nil {
		t.Fatal(err)
	}
	// A threshold-triggered flush must cover every entry before truncating WAL.
	if s.mem.Len() != 0 {
		t.Fatal("batch left entries outside the flushed snapshot")
	}
	if len(s.runs) != 1 {
		t.Fatalf("expected one complete batch flush, got %d", len(s.runs))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenWithOptions(dir, Options{MemTableBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, op := range ops {
		value, ok, err := s.Get(op.Key)
		if err != nil || !ok || string(value) != op.Value {
			t.Fatalf("%s: got %q, %v, %v", op.Key, value, ok, err)
		}
	}
}

func TestShardedBatchValidatesBeforeWriting(t *testing.T) {
	s, err := OpenSharded(t.TempDir(), 4, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteBatch([]BatchOp{{Key: "valid", Value: "value"}, {Key: "", Value: "invalid"}}); err == nil {
		t.Fatal("expected validation error")
	}
	if _, ok, _ := s.Get("valid"); ok {
		t.Fatal("invalid batch wrote a valid entry")
	}
}
