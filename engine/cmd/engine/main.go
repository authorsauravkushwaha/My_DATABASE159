// Command engine is the Kestrel storage engine: a persistent LSM-tree key
// value store served over a length-prefixed JSON TCP protocol.
package main

import (
	"flag"
	"log"

	"kestrel.io/engine"
)

func main() {
	dir := flag.String("dir", "./data", "directory used to persist WAL and SSTable runs")
	addr := flag.String("addr", ":9100", "TCP listen address")
	flag.Parse()

	store, err := engine.Open(*dir)
	if err != nil {
		log.Fatalf("engine: open store: %v", err)
	}
	defer store.Close()

	log.Printf("kestrel engine listening on %s (dir=%s)", *addr, *dir)
	if err := store.Serve(*addr); err != nil {
		log.Fatalf("engine: serve: %v", err)
	}
}
