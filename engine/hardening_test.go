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
