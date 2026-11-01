package engine

import (
	"encoding/binary"
	"errors"
	"os"
	"sort"
)

const sstableMagic = "KSS1"

// indexEntry points at one entry inside an SSTable file.
type indexEntry struct {
	key    string
	offset uint64
}

// sstableEntry is one in-memory representation of a stored entry.
type sstableEntry struct {
	op    byte // walSetOp or walDelOp
	key   string
	value []byte
}

// SSTable is an immutable, sorted run of key-value entries persisted to one
// file. Reads open the file per operation so concurrent goroutines never
// share a file descriptor.
type SSTable struct {
	path  string
	index []indexEntry // sorted by key
}

// buildSSTable writes entries (deduped, last write wins) to path as a sorted
// SSTable and returns a handle over it. The file is written to a temporary
// name and renamed atomically, so a crash mid-write never leaves a partially
// written run behind.
func buildSSTable(path string, entries []sstableEntry) (*SSTable, error) {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].key < entries[j].key
	})
	deduped := entries[:0]
	for _, e := range entries {
		if len(deduped) > 0 && deduped[len(deduped)-1].key == e.key {
			deduped[len(deduped)-1] = e
			continue
		}
		deduped = append(deduped, e)
	}
	entries = deduped

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	offset := uint64(4) // magic prefix
	if _, err := f.WriteString(sstableMagic); err != nil {
		return nil, err
	}
	index := make([]indexEntry, 0, len(entries))
	for _, e := range entries {
		index = append(index, indexEntry{key: e.key, offset: offset})
		var entry []byte
		entry = append(entry, e.op)
		klen := make([]byte, 4)
		binary.BigEndian.PutUint32(klen, uint32(len(e.key)))
		entry = append(entry, klen...)
		entry = append(entry, e.key...)
		vlen := make([]byte, 4)
		if e.op == walDelOp {
			binary.BigEndian.PutUint32(vlen, 0xFFFFFFFF) // tombstone marker
		} else {
			binary.BigEndian.PutUint32(vlen, uint32(len(e.value)))
		}
		entry = append(entry, vlen...)
		if e.op == walSetOp {
			entry = append(entry, e.value...)
		}
		if _, err := f.Write(entry); err != nil {
			return nil, err
		}
		offset += uint64(len(entry))
	}

	indexOffset := offset
	for _, ie := range index {
		klen := make([]byte, 4)
		binary.BigEndian.PutUint32(klen, uint32(len(ie.key)))
		if _, err := f.Write(klen); err != nil {
			return nil, err
		}
		if _, err := f.WriteString(ie.key); err != nil {
			return nil, err
		}
		offb := make([]byte, 8)
		binary.BigEndian.PutUint64(offb, ie.offset)
		if _, err := f.Write(offb); err != nil {
			return nil, err
		}
		offset += 4 + uint64(len(ie.key)) + 8
	}
	indexLen := offset - indexOffset

	footer := make([]byte, 20)
	binary.BigEndian.PutUint64(footer[0:8], indexOffset)
	binary.BigEndian.PutUint64(footer[8:16], indexLen)
	copy(footer[16:20], sstableMagic)
	if _, err := f.Write(footer); err != nil {
		return nil, err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return &SSTable{path: path, index: index}, nil
}

func readAt(f *os.File, buf []byte, off int64) error {
	read := 0
	for read < len(buf) {
		n, err := f.ReadAt(buf[read:], off+int64(read))
		if err != nil {
			return err
		}
		read += n
	}
	return nil
}

func openSSTable(path string) (*SSTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size < 20 {
		return nil, errors.New("sstable too small")
	}
	footer := make([]byte, 20)
	if err := readAt(f, footer, size-20); err != nil {
		return nil, err
	}
	if string(footer[16:20]) != sstableMagic {
		return nil, errors.New("bad sstable magic")
	}
	indexOffset := binary.BigEndian.Uint64(footer[0:8])
	indexLen := binary.BigEndian.Uint64(footer[8:16])
	indexBuf := make([]byte, indexLen)
	if err := readAt(f, indexBuf, int64(indexOffset)); err != nil {
		return nil, err
	}

	index, pos := make([]indexEntry, 0, 16), 0
	for pos < len(indexBuf) {
		if pos+4 > len(indexBuf) {
			return nil, errors.New("truncated sstable index")
		}
		klen := binary.BigEndian.Uint32(indexBuf[pos : pos+4])
		pos += 4
		if pos+int(klen)+8 > len(indexBuf) {
			return nil, errors.New("truncated sstable index")
		}
		index = append(index, indexEntry{
			key:    string(indexBuf[pos : pos+int(klen)]),
			offset: binary.BigEndian.Uint64(indexBuf[pos+int(klen) : pos+int(klen)+8]),
		})
		pos += int(klen) + 8
	}
	sort.Slice(index, func(i, j int) bool { return index[i].key < index[j].key })
	return &SSTable{path: path, index: index}, nil
}

// get returns the entry for key, or (nil, false, false) when absent. A
// tombstone is reported as deleted=true.
func (s *SSTable) get(key string) (value []byte, deleted bool, ok bool) {
	i := sort.Search(len(s.index), func(i int) bool {
		return s.index[i].key >= key
	})
	if i == len(s.index) || s.index[i].key != key {
		return nil, false, false
	}
	offset := int64(s.index[i].offset)

	f, err := os.Open(s.path)
	if err != nil {
		return nil, false, false
	}
	defer f.Close()

	header := make([]byte, 5)
	if err := readAt(f, header, offset); err != nil {
		return nil, false, false
	}
	op := header[0]
	klen := binary.BigEndian.Uint32(header[1:5])
	keyBuf := make([]byte, klen)
	if err := readAt(f, keyBuf, offset+5); err != nil {
		return nil, false, false
	}
	if string(keyBuf) != key {
		return nil, false, false
	}
	vlenBuf := make([]byte, 4)
	if err := readAt(f, vlenBuf, offset+5+int64(klen)); err != nil {
		return nil, false, false
	}
	vlen := binary.BigEndian.Uint32(vlenBuf)
	if op == walDelOp || vlen == 0xFFFFFFFF {
		return nil, true, true
	}
	value = make([]byte, vlen)
	if err := readAt(f, value, offset+5+int64(klen)+4); err != nil {
		return nil, false, false
	}
	return value, false, true
}

// scanPrefix returns every entry whose key has the given prefix, in key order.
// Deleted entries are included; callers filter tombstones.
func (s *SSTable) scanPrefix(prefix string) ([]sstableEntry, error) {
	i := sort.Search(len(s.index), func(i int) bool {
		return s.index[i].key >= prefix
	})
	out := make([]sstableEntry, 0)
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	for i < len(s.index) && hasPrefix(s.index[i].key, prefix) {
		offset := int64(s.index[i].offset)
		header := make([]byte, 5)
		if err := readAt(f, header, offset); err != nil {
			return out, err
		}
		op := header[0]
		klen := binary.BigEndian.Uint32(header[1:5])
		keyBuf := make([]byte, klen)
		if err := readAt(f, keyBuf, offset+5); err != nil {
			return out, err
		}
		vlenBuf := make([]byte, 4)
		if err := readAt(f, vlenBuf, offset+5+int64(klen)); err != nil {
			return out, err
		}
		vlen := binary.BigEndian.Uint32(vlenBuf)
		var value []byte
		if op == walSetOp && vlen != 0xFFFFFFFF {
			value = make([]byte, vlen)
			if err := readAt(f, value, offset+5+int64(klen)+4); err != nil {
				return out, err
			}
		}
		out = append(out, sstableEntry{op: op, key: string(keyBuf), value: value})
		i++
	}
	return out, nil
}

// allEntries returns every entry in the table in key order, including
// tombstones.
func (s *SSTable) allEntries() ([]sstableEntry, error) {
	return s.scanPrefix("")
}

func hasPrefix(s, prefix string) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}
