package engine

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
)

const (
	sstableMagicV1   = "KSS1"
	sstableMagicV2   = "KSS2"
	defaultBlockSize = 32 * 1024 // 32 KiB target uncompressed block size
	blockCodecRaw    = 0
	blockCodecFlate  = 1
	kss2FooterSize   = 44
)

var errCorruptSSTable = errors.New("corrupt sstable")

// indexEntry is retained for legacy KSS1 compatibility.
type indexEntry struct {
	key    string
	offset uint64
}

// blockIndexEntry indexes one compressed multi-record data block in KSS2.
type blockIndexEntry struct {
	firstKey string
	lastKey  string
	offset   uint64
	length   uint32
}

// sstableEntry is one key-value or tombstone entry.
type sstableEntry struct {
	op    byte // walSetOp or walDelOp
	key   string
	value []byte
}

// SSTable is an immutable, sorted run persisted on disk.
// KSS2 tables keep only a sparse block index + Bloom filter in RAM (~200x less
// memory than per-key indexing), compress blocks with DEFLATE + key prefix
// delta encoding, and verify CRC32 checksums on every block read.
type SSTable struct {
	path         string
	version      int
	level        int
	entryCount   uint64
	logicalBytes uint64
	diskBytes    uint64
	minKey       string
	maxKey       string
	blocks       []blockIndexEntry // KSS2 sparse block index
	bloom        *bloomFilter      // KSS2 Bloom filter
	cache        *blockCache       // optional shared decoded-block cache
	legacyIndex  []indexEntry      // KSS1 fallback only

	bloomChecks *atomic.Uint64
	bloomSkips  *atomic.Uint64
}

func commonPrefixLen(a, b string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	if limit > 65535 {
		limit = 65535
	}
	i := 0
	for i < limit && a[i] == b[i] {
		i++
	}
	return i
}

// sstableWriter streams sorted, deduplicated entries into a KSS2 SSTable file
// block-by-block without holding the full run in memory.
type sstableWriter struct {
	path          string
	tmpPath       string
	file          *os.File
	level         int
	offset        uint64
	blockEntries  []sstableEntry
	blockRawBytes int
	blocks        []blockIndexEntry
	keyHashes     []uint64
	entryCount    uint64
	logicalBytes  uint64
	minKey        string
	maxKey        string
	cache         *blockCache
	bloomChecks   *atomic.Uint64
	bloomSkips    *atomic.Uint64
}

func newSSTableWriter(path string, level int, cache *blockCache, bloomChecks, bloomSkips *atomic.Uint64) (*sstableWriter, error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(sstableMagicV2); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	return &sstableWriter{
		path:        path,
		tmpPath:     tmp,
		file:        f,
		level:       level,
		offset:      4,
		cache:       cache,
		bloomChecks: bloomChecks,
		bloomSkips:  bloomSkips,
	}, nil
}

func (w *sstableWriter) Add(e sstableEntry) error {
	if w.entryCount == 0 {
		w.minKey = e.key
	}
	w.maxKey = e.key
	w.entryCount++
	w.logicalBytes += uint64(len(e.key) + len(e.value))

	h1, h2 := bloomHash(e.key)
	w.keyHashes = append(w.keyHashes, (uint64(h1)<<32)|uint64(h2))

	var valCopy []byte
	if e.op == walSetOp && len(e.value) > 0 {
		valCopy = make([]byte, len(e.value))
		copy(valCopy, e.value)
	}
	w.blockEntries = append(w.blockEntries, sstableEntry{op: e.op, key: e.key, value: valCopy})
	w.blockRawBytes += 9 + len(e.key) + len(valCopy)
	if w.blockRawBytes >= defaultBlockSize {
		return w.flushBlock()
	}
	return nil
}

func encodeRawBlock(entries []sstableEntry) []byte {
	var buf bytes.Buffer
	var countBuf [4]byte
	binary.BigEndian.PutUint32(countBuf[:], uint32(len(entries)))
	buf.Write(countBuf[:])

	prevKey := ""
	var hdr [9]byte
	for i, e := range entries {
		shared := 0
		if i > 0 {
			shared = commonPrefixLen(prevKey, e.key)
		}
		unshared := len(e.key) - shared
		hdr[0] = e.op
		binary.BigEndian.PutUint16(hdr[1:3], uint16(shared))
		binary.BigEndian.PutUint16(hdr[3:5], uint16(unshared))
		if e.op == walDelOp {
			binary.BigEndian.PutUint32(hdr[5:9], 0xFFFFFFFF)
		} else {
			binary.BigEndian.PutUint32(hdr[5:9], uint32(len(e.value)))
		}
		buf.Write(hdr[:])
		buf.WriteString(e.key[shared:])
		if e.op == walSetOp && len(e.value) > 0 {
			buf.Write(e.value)
		}
		prevKey = e.key
	}
	return buf.Bytes()
}

func decodeRawBlock(raw []byte) ([]sstableEntry, int, error) {
	if len(raw) < 4 {
		return nil, 0, errCorruptSSTable
	}
	count := binary.BigEndian.Uint32(raw[0:4])
	if count > 1_000_000 {
		return nil, 0, errCorruptSSTable
	}
	entries := make([]sstableEntry, 0, count)
	pos := 4
	prevKey := ""
	approxBytes := 0
	for i := uint32(0); i < count; i++ {
		if pos+9 > len(raw) {
			return nil, 0, errCorruptSSTable
		}
		op := raw[pos]
		shared := int(binary.BigEndian.Uint16(raw[pos+1 : pos+3]))
		unshared := int(binary.BigEndian.Uint16(raw[pos+3 : pos+5]))
		vlen := binary.BigEndian.Uint32(raw[pos+5 : pos+9])
		pos += 9
		if shared > len(prevKey) || pos+unshared > len(raw) {
			return nil, 0, errCorruptSSTable
		}
		key := prevKey[:shared] + string(raw[pos:pos+unshared])
		pos += unshared
		var val []byte
		if op == walSetOp && vlen != 0xFFFFFFFF {
			if pos+int(vlen) > len(raw) {
				return nil, 0, errCorruptSSTable
			}
			val = make([]byte, vlen)
			copy(val, raw[pos:pos+int(vlen)])
			pos += int(vlen)
		} else if op != walDelOp && vlen != 0xFFFFFFFF {
			return nil, 0, errCorruptSSTable
		}
		entries = append(entries, sstableEntry{op: op, key: key, value: val})
		approxBytes += 32 + len(key) + len(val)
		prevKey = key
	}
	if pos != len(raw) {
		return nil, 0, errCorruptSSTable
	}
	return entries, approxBytes, nil
}

func (w *sstableWriter) flushBlock() error {
	if len(w.blockEntries) == 0 {
		return nil
	}
	raw := encodeRawBlock(w.blockEntries)
	codec := byte(blockCodecRaw)
	payload := raw

	if len(raw) >= 128 {
		var compBuf bytes.Buffer
		fw, err := flate.NewWriter(&compBuf, flate.BestSpeed)
		if err == nil {
			_, werr := fw.Write(raw)
			cerr := fw.Close()
			if werr == nil && cerr == nil && compBuf.Len()+8 < len(raw) {
				codec = blockCodecFlate
				payload = compBuf.Bytes()
			}
		}
	}

	frame := make([]byte, 9+len(payload)+4)
	frame[0] = codec
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(raw)))
	binary.BigEndian.PutUint32(frame[5:9], uint32(len(payload)))
	copy(frame[9:9+len(payload)], payload)
	crc := crc32.ChecksumIEEE(frame[:9+len(payload)])
	binary.BigEndian.PutUint32(frame[9+len(payload):], crc)

	if _, err := w.file.Write(frame); err != nil {
		return err
	}
	w.blocks = append(w.blocks, blockIndexEntry{
		firstKey: w.blockEntries[0].key,
		lastKey:  w.blockEntries[len(w.blockEntries)-1].key,
		offset:   w.offset,
		length:   uint32(len(frame)),
	})
	w.offset += uint64(len(frame))
	w.blockEntries = w.blockEntries[:0]
	w.blockRawBytes = 0
	return nil
}

func (w *sstableWriter) Abort() {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	_ = os.Remove(w.tmpPath)
}

func (w *sstableWriter) Finish() (*SSTable, error) {
	if err := w.flushBlock(); err != nil {
		w.Abort()
		return nil, err
	}

	// Build and write Bloom filter block.
	bf := newBloomFilter(len(w.keyHashes), defaultBitsPerKey)
	nbits := uint32(len(bf.bits) * 8)
	for _, packed := range w.keyHashes {
		h1 := uint32(packed >> 32)
		h2 := uint32(packed)
		for i := uint8(0); i < bf.k; i++ {
			bit := (h1 + uint32(i)*h2) % nbits
			bf.bits[bit/8] |= 1 << (bit % 8)
		}
	}
	w.keyHashes = nil
	bloomBytes := bf.encode()
	bloomOffset := w.offset
	if len(bloomBytes) > 0 {
		if _, err := w.file.Write(bloomBytes); err != nil {
			w.Abort()
			return nil, err
		}
		w.offset += uint64(len(bloomBytes))
	}
	bloomLen := w.offset - bloomOffset

	// Encode sparse block index + table metadata with CRC32 protection.
	var idxBuf bytes.Buffer
	var metaHdr [24]byte
	binary.BigEndian.PutUint64(metaHdr[0:8], w.entryCount)
	binary.BigEndian.PutUint64(metaHdr[8:16], w.logicalBytes)
	binary.BigEndian.PutUint32(metaHdr[16:20], uint32(w.level))
	binary.BigEndian.PutUint32(metaHdr[20:24], uint32(len(w.blocks)))
	idxBuf.Write(metaHdr[:])
	writeLenPrefixedString(&idxBuf, w.minKey)
	writeLenPrefixedString(&idxBuf, w.maxKey)
	for _, b := range w.blocks {
		writeLenPrefixedString(&idxBuf, b.firstKey)
		writeLenPrefixedString(&idxBuf, b.lastKey)
		var offLen [12]byte
		binary.BigEndian.PutUint64(offLen[0:8], b.offset)
		binary.BigEndian.PutUint32(offLen[8:12], b.length)
		idxBuf.Write(offLen[:])
	}
	idxCRC := crc32.ChecksumIEEE(idxBuf.Bytes())
	var crcBytes [4]byte
	binary.BigEndian.PutUint32(crcBytes[:], idxCRC)
	idxBuf.Write(crcBytes[:])

	indexOffset := w.offset
	if _, err := w.file.Write(idxBuf.Bytes()); err != nil {
		w.Abort()
		return nil, err
	}
	w.offset += uint64(idxBuf.Len())
	indexLen := w.offset - indexOffset

	footer := make([]byte, kss2FooterSize)
	binary.BigEndian.PutUint64(footer[0:8], bloomOffset)
	binary.BigEndian.PutUint64(footer[8:16], bloomLen)
	binary.BigEndian.PutUint64(footer[16:24], indexOffset)
	binary.BigEndian.PutUint64(footer[24:32], indexLen)
	binary.BigEndian.PutUint32(footer[32:36], 2) // format version 2
	footerCRC := crc32.ChecksumIEEE(footer[0:36])
	binary.BigEndian.PutUint32(footer[36:40], footerCRC)
	copy(footer[40:44], sstableMagicV2)

	if _, err := w.file.Write(footer); err != nil {
		w.Abort()
		return nil, err
	}
	diskBytes := w.offset + uint64(len(footer))
	if err := w.file.Sync(); err != nil {
		w.Abort()
		return nil, err
	}
	if err := w.file.Close(); err != nil {
		w.file = nil
		w.Abort()
		return nil, err
	}
	w.file = nil
	if err := os.Rename(w.tmpPath, w.path); err != nil {
		_ = os.Remove(w.tmpPath)
		return nil, err
	}
	if err := syncDir(filepath.Dir(w.path)); err != nil {
		return nil, err
	}
	return &SSTable{
		path:         w.path,
		version:      2,
		level:        w.level,
		entryCount:   w.entryCount,
		logicalBytes: w.logicalBytes,
		diskBytes:    diskBytes,
		minKey:       w.minKey,
		maxKey:       w.maxKey,
		blocks:       w.blocks,
		bloom:        bf,
		cache:        w.cache,
		bloomChecks:  w.bloomChecks,
		bloomSkips:   w.bloomSkips,
	}, nil
}

func writeLenPrefixedString(buf *bytes.Buffer, s string) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(s)))
	buf.Write(l[:])
	buf.WriteString(s)
}

func readLenPrefixedString(data []byte, pos *int) (string, error) {
	if *pos+4 > len(data) {
		return "", errCorruptSSTable
	}
	n := int(binary.BigEndian.Uint32(data[*pos : *pos+4]))
	*pos += 4
	if n < 0 || *pos+n > len(data) {
		return "", errCorruptSSTable
	}
	s := string(data[*pos : *pos+n])
	*pos += n
	return s, nil
}

// buildSSTable writes entries (deduped, last write wins) to path as a sorted
// KSS2 SSTable and returns a handle over it.
func buildSSTable(path string, entries []sstableEntry) (*SSTable, error) {
	return buildSSTableWithMeta(path, 0, entries, nil, nil, nil)
}

func buildSSTableWithMeta(path string, level int, entries []sstableEntry, cache *blockCache, bloomChecks, bloomSkips *atomic.Uint64) (*SSTable, error) {
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
	writer, err := newSSTableWriter(path, level, cache, bloomChecks, bloomSkips)
	if err != nil {
		return nil, err
	}
	for _, e := range deduped {
		if err := writer.Add(e); err != nil {
			writer.Abort()
			return nil, err
		}
	}
	return writer.Finish()
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
	return openSSTableWithCache(path, nil, nil, nil)
}

func openSSTableWithCache(path string, cache *blockCache, bloomChecks, bloomSkips *atomic.Uint64) (*SSTable, error) {
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

	var magicTail [4]byte
	if err := readAt(f, magicTail[:], size-4); err != nil {
		return nil, err
	}
	switch string(magicTail[:]) {
	case sstableMagicV2:
		return openSSTableV2(f, path, size, cache, bloomChecks, bloomSkips)
	case sstableMagicV1:
		return openSSTableV1(f, path, size)
	default:
		return nil, errors.New("bad sstable magic")
	}
}

func openSSTableV2(f *os.File, path string, size int64, cache *blockCache, bloomChecks, bloomSkips *atomic.Uint64) (*SSTable, error) {
	if size < 4+kss2FooterSize {
		return nil, errors.New("sstable v2 too small")
	}
	var hdrMagic [4]byte
	if err := readAt(f, hdrMagic[:], 0); err != nil {
		return nil, err
	}
	if string(hdrMagic[:]) != sstableMagicV2 {
		return nil, errors.New("bad sstable v2 header magic")
	}

	footer := make([]byte, kss2FooterSize)
	if err := readAt(f, footer, size-kss2FooterSize); err != nil {
		return nil, err
	}
	wantFooterCRC := binary.BigEndian.Uint32(footer[36:40])
	if crc32.ChecksumIEEE(footer[0:36]) != wantFooterCRC {
		return nil, errors.New("corrupt sstable footer checksum")
	}
	bloomOffset := binary.BigEndian.Uint64(footer[0:8])
	bloomLen := binary.BigEndian.Uint64(footer[8:16])
	indexOffset := binary.BigEndian.Uint64(footer[16:24])
	indexLen := binary.BigEndian.Uint64(footer[24:32])

	if indexOffset+indexLen > uint64(size-kss2FooterSize) || bloomOffset+bloomLen > indexOffset || indexLen < 28 {
		return nil, errors.New("corrupt sstable v2 offsets")
	}

	var bf *bloomFilter
	if bloomLen > 0 {
		bloomBuf := make([]byte, bloomLen)
		if err := readAt(f, bloomBuf, int64(bloomOffset)); err != nil {
			return nil, err
		}
		var err error
		bf, err = decodeBloomFilter(bloomBuf)
		if err != nil {
			return nil, err
		}
	}

	idxBuf := make([]byte, indexLen)
	if err := readAt(f, idxBuf, int64(indexOffset)); err != nil {
		return nil, err
	}
	wantIdxCRC := binary.BigEndian.Uint32(idxBuf[len(idxBuf)-4:])
	idxPayload := idxBuf[:len(idxBuf)-4]
	if crc32.ChecksumIEEE(idxPayload) != wantIdxCRC {
		return nil, errors.New("corrupt sstable index checksum")
	}

	entryCount := binary.BigEndian.Uint64(idxPayload[0:8])
	logicalBytes := binary.BigEndian.Uint64(idxPayload[8:16])
	level := int(binary.BigEndian.Uint32(idxPayload[16:20]))
	blockCount := int(binary.BigEndian.Uint32(idxPayload[20:24]))
	pos := 24
	minKey, err := readLenPrefixedString(idxPayload, &pos)
	if err != nil {
		return nil, err
	}
	maxKey, err := readLenPrefixedString(idxPayload, &pos)
	if err != nil {
		return nil, err
	}
	blocks := make([]blockIndexEntry, 0, blockCount)
	for i := 0; i < blockCount; i++ {
		firstKey, err := readLenPrefixedString(idxPayload, &pos)
		if err != nil {
			return nil, err
		}
		lastKey, err := readLenPrefixedString(idxPayload, &pos)
		if err != nil {
			return nil, err
		}
		if pos+12 > len(idxPayload) {
			return nil, errCorruptSSTable
		}
		off := binary.BigEndian.Uint64(idxPayload[pos : pos+8])
		length := binary.BigEndian.Uint32(idxPayload[pos+8 : pos+12])
		pos += 12
		blocks = append(blocks, blockIndexEntry{
			firstKey: firstKey,
			lastKey:  lastKey,
			offset:   off,
			length:   length,
		})
	}
	if pos != len(idxPayload) {
		return nil, errCorruptSSTable
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return &SSTable{
		path:         path,
		version:      2,
		level:        level,
		entryCount:   entryCount,
		logicalBytes: logicalBytes,
		diskBytes:    uint64(size),
		minKey:       minKey,
		maxKey:       maxKey,
		blocks:       blocks,
		bloom:        bf,
		cache:        cache,
		bloomChecks:  bloomChecks,
		bloomSkips:   bloomSkips,
	}, nil
}

func openSSTableV1(f *os.File, path string, size int64) (*SSTable, error) {
	footer := make([]byte, 20)
	if err := readAt(f, footer, size-20); err != nil {
		return nil, err
	}
	if string(footer[16:20]) != sstableMagicV1 {
		return nil, errors.New("bad sstable magic")
	}
	indexOffset := binary.BigEndian.Uint64(footer[0:8])
	indexLen := binary.BigEndian.Uint64(footer[8:16])
	if indexOffset+indexLen > uint64(size-20) {
		return nil, errors.New("corrupt sstable v1 offsets")
	}
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
	var minKey, maxKey string
	if len(index) > 0 {
		minKey = index[0].key
		maxKey = index[len(index)-1].key
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	return &SSTable{
		path:        path,
		version:     1,
		entryCount:  uint64(len(index)),
		diskBytes:   uint64(size),
		minKey:      minKey,
		maxKey:      maxKey,
		legacyIndex: index,
	}, nil
}

func (s *SSTable) getBlock(f **os.File, b blockIndexEntry) ([]sstableEntry, error) {
	if s.cache != nil {
		if entries, ok := s.cache.get(s.path, b.offset); ok {
			return entries, nil
		}
	}
	if b.length < 13 {
		return nil, errCorruptSSTable
	}
	var localFile *os.File
	var target *os.File
	if f != nil && *f != nil {
		target = *f
	} else {
		opened, err := os.Open(s.path)
		if err != nil {
			return nil, err
		}
		if f != nil {
			*f = opened
			target = opened
		} else {
			localFile = opened
			target = localFile
		}
	}
	if localFile != nil {
		defer localFile.Close()
	}

	frame := make([]byte, b.length)
	if err := readAt(target, frame, int64(b.offset)); err != nil {
		return nil, err
	}
	payloadEnd := len(frame) - 4
	wantCRC := binary.BigEndian.Uint32(frame[payloadEnd:])
	if crc32.ChecksumIEEE(frame[:payloadEnd]) != wantCRC {
		return nil, errors.New("corrupt sstable data block checksum")
	}
	codec := frame[0]
	rawLen := binary.BigEndian.Uint32(frame[1:5])
	payloadLen := binary.BigEndian.Uint32(frame[5:9])
	if int(payloadLen) != payloadEnd-9 || rawLen > 64<<20 {
		return nil, errCorruptSSTable
	}
	payload := frame[9:payloadEnd]

	var raw []byte
	switch codec {
	case blockCodecRaw:
		if uint32(len(payload)) != rawLen {
			return nil, errCorruptSSTable
		}
		raw = payload
	case blockCodecFlate:
		fr := flate.NewReader(bytes.NewReader(payload))
		raw = make([]byte, rawLen)
		_, err := io.ReadFull(fr, raw)
		_ = fr.Close()
		if err != nil {
			return nil, errors.New("corrupt sstable compressed block: " + err.Error())
		}
	default:
		return nil, errors.New("unsupported sstable block codec")
	}
	entries, approxBytes, err := decodeRawBlock(raw)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		s.cache.put(s.path, b.offset, entries, approxBytes)
	}
	return entries, nil
}

// get returns the entry for key, or (nil, false, false, nil) when absent.
func (s *SSTable) get(key string) (value []byte, deleted bool, ok bool, err error) {
	if s.version == 1 {
		return s.getV1(key)
	}
	if s.entryCount == 0 || key < s.minKey || key > s.maxKey {
		return nil, false, false, nil
	}
	if s.bloom != nil {
		if s.bloomChecks != nil {
			s.bloomChecks.Add(1)
		}
		if !s.bloom.mayContain(key) {
			if s.bloomSkips != nil {
				s.bloomSkips.Add(1)
			}
			return nil, false, false, nil
		}
	}

	bi := sort.Search(len(s.blocks), func(i int) bool {
		return s.blocks[i].lastKey >= key
	})
	if bi >= len(s.blocks) || s.blocks[bi].firstKey > key {
		return nil, false, false, nil
	}
	entries, err := s.getBlock(nil, s.blocks[bi])
	if err != nil {
		return nil, false, false, err
	}

	ei := sort.Search(len(entries), func(i int) bool {
		return entries[i].key >= key
	})
	if ei >= len(entries) || entries[ei].key != key {
		return nil, false, false, nil
	}
	e := entries[ei]
	if e.op == walDelOp {
		return nil, true, true, nil
	}
	out := make([]byte, len(e.value))
	copy(out, e.value)
	return out, false, true, nil
}

func (s *SSTable) getV1(key string) ([]byte, bool, bool, error) {
	i := sort.Search(len(s.legacyIndex), func(i int) bool {
		return s.legacyIndex[i].key >= key
	})
	if i == len(s.legacyIndex) || s.legacyIndex[i].key != key {
		return nil, false, false, nil
	}
	offset := int64(s.legacyIndex[i].offset)

	f, openErr := os.Open(s.path)
	if openErr != nil {
		return nil, false, false, openErr
	}
	defer f.Close()

	header := make([]byte, 5)
	if err := readAt(f, header, offset); err != nil {
		return nil, false, false, err
	}
	op := header[0]
	klen := binary.BigEndian.Uint32(header[1:5])
	keyBuf := make([]byte, klen)
	if err := readAt(f, keyBuf, offset+5); err != nil {
		return nil, false, false, err
	}
	if string(keyBuf) != key {
		return nil, false, false, errors.New("sstable key mismatch")
	}
	vlenBuf := make([]byte, 4)
	if err := readAt(f, vlenBuf, offset+5+int64(klen)); err != nil {
		return nil, false, false, err
	}
	vlen := binary.BigEndian.Uint32(vlenBuf)
	if op == walDelOp || vlen == 0xFFFFFFFF {
		return nil, true, true, nil
	}
	value := make([]byte, vlen)
	if err := readAt(f, value, offset+5+int64(klen)+4); err != nil {
		return nil, false, false, err
	}
	return value, false, true, nil
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
