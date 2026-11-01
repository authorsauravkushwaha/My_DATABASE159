package engine

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"sort"
	"sync"
)

const (
	walSetOp      = 0
	walDelOp      = 1
	walBatchMagic = 0xB2
	maxWALFrame   = 64 << 20 // 64 MiB max batch frame
)

// walEntry is one durable mutation recorded ahead of the memtable.
type walEntry struct {
	Op    byte
	Key   string
	Value []byte
}

// legacyWALEntry preserves backward-compatible replay of v1 JSON frames.
type legacyWALEntry struct {
	Op    byte   `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// WAL is an append-only, fsync'd write-ahead log supporting both single-key
// frames and multi-key atomic batch frames protected by CRC32 checksums.
type WAL struct {
	mu      sync.Mutex
	file    *os.File
	size    int64
	onClose func()
}

func openWAL(path string, onClose func()) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &WAL{file: f, size: info.Size(), onClose: onClose}, nil
}

// Append durably logs a single Set or Delete mutation in compact binary format.
func (w *WAL) Append(op byte, key string, value []byte) error {
	return w.AppendBatch([]walEntry{{Op: op, Key: key, Value: value}})
}

// AppendBatch encodes one or more mutations into a single CRC32-protected
// binary WAL frame and fsyncs once for the entire batch.
func (w *WAL) AppendBatch(entries []walEntry) error {
	if len(entries) == 0 {
		return nil
	}
	payloadLen := 1 + 4
	for _, e := range entries {
		payloadLen += 1 + 2 + 4 + len(e.Key)
		if e.Op == walSetOp {
			payloadLen += len(e.Value)
		}
	}
	if payloadLen > maxWALFrame {
		return errors.New("batch exceeds maximum WAL frame size (64 MiB)")
	}

	frame := make([]byte, 4+payloadLen+4)
	binary.BigEndian.PutUint32(frame[0:4], uint32(payloadLen))
	frame[4] = walBatchMagic
	binary.BigEndian.PutUint32(frame[5:9], uint32(len(entries)))
	pos := 9
	for _, e := range entries {
		frame[pos] = e.Op
		pos++
		binary.BigEndian.PutUint16(frame[pos:pos+2], uint16(len(e.Key)))
		pos += 2
		vlen := 0
		if e.Op == walSetOp {
			vlen = len(e.Value)
		}
		binary.BigEndian.PutUint32(frame[pos:pos+4], uint32(vlen))
		pos += 4
		copy(frame[pos:pos+len(e.Key)], e.Key)
		pos += len(e.Key)
		if vlen > 0 {
			copy(frame[pos:pos+vlen], e.Value)
			pos += vlen
		}
	}
	sum := crc32.ChecksumIEEE(frame[:4+payloadLen])
	binary.BigEndian.PutUint32(frame[4+payloadLen:], sum)

	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.file.Write(frame)
	if err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	w.size += int64(n)
	return nil
}

func decodeBinaryBatch(payload []byte) ([]walEntry, error) {
	if len(payload) < 5 {
		return nil, errors.New("corrupt WAL binary batch header")
	}
	count := binary.BigEndian.Uint32(payload[1:5])
	if count > 1_000_000 {
		return nil, errors.New("corrupt WAL batch count")
	}
	out := make([]walEntry, 0, count)
	pos := 5
	for i := uint32(0); i < count; i++ {
		if pos+7 > len(payload) {
			return nil, errors.New("truncated WAL binary batch entry")
		}
		op := payload[pos]
		if op != walSetOp && op != walDelOp {
			return nil, errors.New("unknown WAL operation")
		}
		klen := int(binary.BigEndian.Uint16(payload[pos+1 : pos+3]))
		vlen := int(binary.BigEndian.Uint32(payload[pos+3 : pos+7]))
		pos += 7
		if klen == 0 || klen > 4096 || vlen < 0 || pos+klen+vlen > len(payload) {
			return nil, errors.New("corrupt WAL binary entry lengths")
		}
		key := string(payload[pos : pos+klen])
		pos += klen
		var val []byte
		if op == walSetOp {
			val = make([]byte, vlen)
			copy(val, payload[pos:pos+vlen])
		}
		pos += vlen
		out = append(out, walEntry{Op: op, Key: key, Value: val})
	}
	if pos != len(payload) {
		return nil, errors.New("trailing bytes in WAL binary batch")
	}
	return out, nil
}

// Replay returns every complete, CRC-valid record in the log, in order.
// Both v1 JSON frames and v2 binary batch frames are supported.
func (w *WAL) Replay() ([]walEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var out []walEntry
	validEnd := int64(0)
	head := make([]byte, 4)
	for {
		if _, err := io.ReadFull(w.file, head); err != nil {
			break // end of log or torn final record: stop at last complete record
		}
		length := binary.BigEndian.Uint32(head)
		if length == 0 || length > maxWALFrame {
			return nil, errors.New("corrupt WAL frame length")
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(w.file, payload); err != nil {
			break
		}
		crcBuf := make([]byte, 4)
		if _, err := io.ReadFull(w.file, crcBuf); err != nil {
			break
		}
		hash := crc32.NewIEEE()
		hash.Write(head)
		hash.Write(payload)
		if hash.Sum32() != binary.BigEndian.Uint32(crcBuf) {
			return nil, errors.New("corrupt WAL checksum; refusing partial recovery")
		}

		if payload[0] == walBatchMagic {
			batch, err := decodeBinaryBatch(payload)
			if err != nil {
				return nil, err
			}
			out = append(out, batch...)
		} else {
			var legacy legacyWALEntry
			if err := json.Unmarshal(payload, &legacy); err != nil {
				return nil, err
			}
			if legacy.Op != walSetOp && legacy.Op != walDelOp {
				return nil, errors.New("unknown WAL operation")
			}
			var val []byte
			if legacy.Op == walSetOp {
				val = []byte(legacy.Value)
			}
			out = append(out, walEntry{Op: legacy.Op, Key: legacy.Key, Value: val})
		}
		validEnd += int64(4 + len(payload) + 4)
	}
	// Remove an incomplete final frame left by a crash before new appends.
	if err := w.file.Truncate(validEnd); err != nil {
		return nil, err
	}
	if _, err := w.file.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	if err := w.file.Sync(); err != nil {
		return nil, err
	}
	w.size = validEnd
	return out, nil
}

// Truncate discards the log contents after a successful flush.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	w.size = 0
	return nil
}

func (w *WAL) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

func (w *WAL) Close() error {
	w.mu.Lock()
	onClose := w.onClose
	w.onClose = nil
	err := w.file.Close()
	w.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	return err
}

// sortedSnapshotEntries converts a memtable map into sorted sstableEntries.
func sortedSnapshotEntries(snapshot map[string]memEntry) []sstableEntry {
	entries := make([]sstableEntry, 0, len(snapshot))
	for k, v := range snapshot {
		if v.deleted {
			entries = append(entries, sstableEntry{op: walDelOp, key: k})
		} else {
			entries = append(entries, sstableEntry{op: walSetOp, key: k, value: v.value})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].key < entries[j].key
	})
	return entries
}
