// Command engine is the Kestrel storage engine: a persistent, block-compressed,
// optionally hash-sharded LSM-tree key-value store served over TCP.
package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"kestrel.io/engine"
)

func envInt(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func main() {
	dir := flag.String("dir", "./data", "directory used to persist WAL and SSTable runs")
	addr := flag.String("addr", ":9100", "TCP listen address")
	shards := flag.Int("shards", envInt("ENGINE_SHARDS", 1), "number of parallel LSM shards (1–64)")
	memMB := flag.Int("memtable-mb", envInt("ENGINE_MEMTABLE_MB", 4), "memtable flush threshold in MiB")
	cacheMB := flag.Int("cache-mb", envInt("ENGINE_CACHE_MB", 32), "decoded block cache budget in MiB")
	flag.Parse()

	opts := engine.Options{
		MemTableBytes:   *memMB << 20,
		BlockCacheBytes: *cacheMB << 20,
	}

	var store engine.KeyStore
	var err error
	if *shards > 1 {
		store, err = engine.OpenSharded(*dir, *shards, opts)
	} else {
		store, err = engine.OpenWithOptions(*dir, opts)
	}
	if err != nil {
		log.Fatalf("engine: open store: %v", err)
	}
	defer store.Close()

	log.Printf("kestrel engine listening on %s (dir=%s shards=%d memtable=%dMiB cache=%dMiB)", *addr, *dir, *shards, *memMB, *cacheMB)
	if err := store.Serve(*addr); err != nil {
		log.Fatalf("engine: serve: %v", err)
	}
}
