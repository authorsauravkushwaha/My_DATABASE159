package engine

import (
	"container/heap"
	"encoding/binary"
	"os"
	"sort"
)

type kvIterator interface {
	Next() bool
	Entry() sstableEntry
	Err() error
	Close() error
}

type sliceIterator struct {
	entries []sstableEntry
	idx     int
}

func newSliceIterator(entries []sstableEntry) *sliceIterator {
	return &sliceIterator{entries: entries, idx: -1}
}

func (it *sliceIterator) Next() bool {
	it.idx++
	return it.idx < len(it.entries)
}

func (it *sliceIterator) Entry() sstableEntry {
	return it.entries[it.idx]
}

func (it *sliceIterator) Err() error   { return nil }
func (it *sliceIterator) Close() error { return nil }

type sstableIterator struct {
	sst       *SSTable
	prefix    string
	after     string
	inclusive bool
	file      *os.File

	// KSS2 state
	blockIdx      int
	blockData     []sstableEntry
	entryIdx      int
	searchedFirst bool

	// KSS1 state
	v1Idx int

	curr sstableEntry
	err  error
	done bool
}

func newSSTableIterator(sst *SSTable, prefix, after string, inclusive bool) *sstableIterator {
	it := &sstableIterator{
		sst:       sst,
		prefix:    prefix,
		after:     after,
		inclusive: inclusive,
	}
	seekKey := prefix
	if after != "" && after > seekKey {
		seekKey = after
	}

	if sst.entryCount == 0 {
		it.done = true
		return it
	}
	if after != "" {
		if inclusive && sst.maxKey < after {
			it.done = true
			return it
		}
		if !inclusive && sst.maxKey <= after {
			it.done = true
			return it
		}
	}
	if prefix != "" {
		if sst.maxKey < prefix || (sst.minKey > prefix && !hasPrefix(sst.minKey, prefix)) {
			it.done = true
			return it
		}
	}

	if sst.version == 1 {
		it.v1Idx = sort.Search(len(sst.legacyIndex), func(i int) bool {
			if after != "" && !inclusive && seekKey == after {
				return sst.legacyIndex[i].key > seekKey
			}
			return sst.legacyIndex[i].key >= seekKey
		}) - 1
		return it
	}

	it.blockIdx = sort.Search(len(sst.blocks), func(i int) bool {
		if after != "" && !inclusive && seekKey == after {
			return sst.blocks[i].lastKey > seekKey
		}
		return sst.blocks[i].lastKey >= seekKey
	})
	it.entryIdx = -1
	return it
}

func (it *sstableIterator) passesLowerBound(key string) bool {
	if it.after == "" {
		return true
	}
	if it.inclusive {
		return key >= it.after
	}
	return key > it.after
}

func (it *sstableIterator) Next() bool {
	if it.done || it.err != nil {
		return false
	}
	if it.sst.version == 1 {
		return it.nextV1()
	}
	for {
		it.entryIdx++
		for it.blockData == nil || it.entryIdx >= len(it.blockData) {
			if it.blockIdx >= len(it.sst.blocks) {
				it.done = true
				return false
			}
			blk := it.sst.blocks[it.blockIdx]
			if it.prefix != "" && blk.firstKey > it.prefix && !hasPrefix(blk.firstKey, it.prefix) {
				it.done = true
				return false
			}
			entries, err := it.sst.getBlock(&it.file, blk)
			if err != nil {
				it.err = err
				it.done = true
				return false
			}
			it.blockData = entries
			it.blockIdx++

			if !it.searchedFirst {
				it.searchedFirst = true
				seekKey := it.prefix
				if it.after != "" && it.after > seekKey {
					seekKey = it.after
				}
				if seekKey != "" {
					it.entryIdx = sort.Search(len(entries), func(i int) bool {
						if it.after != "" && !it.inclusive && seekKey == it.after {
							return entries[i].key > seekKey
						}
						return entries[i].key >= seekKey
					})
				} else {
					it.entryIdx = 0
				}
			} else {
				it.entryIdx = 0
			}
		}

		e := it.blockData[it.entryIdx]
		if !it.passesLowerBound(e.key) {
			continue
		}
		if it.prefix != "" && !hasPrefix(e.key, it.prefix) {
			if e.key > it.prefix {
				it.done = true
				return false
			}
			continue
		}
		it.curr = e
		return true
	}
}

func (it *sstableIterator) nextV1() bool {
	for {
		it.v1Idx++
		if it.v1Idx >= len(it.sst.legacyIndex) {
			it.done = true
			return false
		}
		ie := it.sst.legacyIndex[it.v1Idx]
		if !it.passesLowerBound(ie.key) {
			continue
		}
		if it.prefix != "" && !hasPrefix(ie.key, it.prefix) {
			if ie.key > it.prefix {
				it.done = true
				return false
			}
			continue
		}
		if it.file == nil {
			f, err := os.Open(it.sst.path)
			if err != nil {
				it.err = err
				it.done = true
				return false
			}
			it.file = f
		}
		offset := int64(ie.offset)
		var header [5]byte
		if err := readAt(it.file, header[:], offset); err != nil {
			it.err = err
			it.done = true
			return false
		}
		op := header[0]
		klen := binary.BigEndian.Uint32(header[1:5])
		keyBuf := make([]byte, klen)
		if err := readAt(it.file, keyBuf, offset+5); err != nil {
			it.err = err
			it.done = true
			return false
		}
		var vlenBuf [4]byte
		if err := readAt(it.file, vlenBuf[:], offset+5+int64(klen)); err != nil {
			it.err = err
			it.done = true
			return false
		}
		vlen := binary.BigEndian.Uint32(vlenBuf[:])
		var value []byte
		if op == walSetOp && vlen != 0xFFFFFFFF {
			value = make([]byte, vlen)
			if err := readAt(it.file, value, offset+5+int64(klen)+4); err != nil {
				it.err = err
				it.done = true
				return false
			}
		}
		it.curr = sstableEntry{op: op, key: string(keyBuf), value: value}
		return true
	}
}

func (it *sstableIterator) Entry() sstableEntry { return it.curr }
func (it *sstableIterator) Err() error          { return it.err }
func (it *sstableIterator) Close() error {
	if it.file != nil {
		err := it.file.Close()
		it.file = nil
		return err
	}
	return nil
}

type mergeHeapItem struct {
	entry sstableEntry
	rank  int // smaller rank = newer run
	iter  kvIterator
}

type mergeHeap []mergeHeapItem

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	if h[i].entry.key != h[j].entry.key {
		return h[i].entry.key < h[j].entry.key
	}
	return h[i].rank < h[j].rank
}
func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x any)   { *h = append(*h, x.(mergeHeapItem)) }
func (h *mergeHeap) Pop() any     { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

// mergeIterator performs a streaming O(R) memory k-way merge across ordered
// runs, shadowing older versions of duplicate keys automatically.
type mergeIterator struct {
	iters       []kvIterator
	h           mergeHeap
	skipDeletes bool
	curr        sstableEntry
	err         error
}

func newMergeIterator(iters []kvIterator, skipDeletes bool) (*mergeIterator, error) {
	m := &mergeIterator{
		iters:       iters,
		h:           make(mergeHeap, 0, len(iters)),
		skipDeletes: skipDeletes,
	}
	for rank, it := range iters {
		if it.Next() {
			m.h = append(m.h, mergeHeapItem{entry: it.Entry(), rank: rank, iter: it})
		} else if err := it.Err(); err != nil {
			_ = m.Close()
			return nil, err
		}
	}
	heap.Init(&m.h)
	return m, nil
}

func (m *mergeIterator) advanceTop() bool {
	top := heap.Pop(&m.h).(mergeHeapItem)
	if top.iter.Next() {
		top.entry = top.iter.Entry()
		heap.Push(&m.h, top)
	} else if err := top.iter.Err(); err != nil {
		m.err = err
		return false
	}
	return true
}

func (m *mergeIterator) Next() bool {
	if m.err != nil {
		return false
	}
	for len(m.h) > 0 {
		winner := m.h[0].entry
		if !m.advanceTop() {
			return false
		}
		// Advance and discard older shadowed versions of the same key.
		for len(m.h) > 0 && m.h[0].entry.key == winner.key {
			if !m.advanceTop() {
				return false
			}
		}
		if m.skipDeletes && winner.op == walDelOp {
			continue
		}
		m.curr = winner
		return true
	}
	return false
}

func (m *mergeIterator) Entry() sstableEntry { return m.curr }
func (m *mergeIterator) Err() error          { return m.err }
func (m *mergeIterator) Close() error {
	var firstErr error
	for _, it := range m.iters {
		if err := it.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
