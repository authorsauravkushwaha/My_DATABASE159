package engine

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

const (
	walSetOp = 0
	walDelOp = 1
)

// walEntry is one durable mutation recorded ahead of the memtable.
type walEntry struct {
	Op    byte   `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// WAL is an append-only, fsync'd log. Every Set/Delete is appended here
// before it becomes visible in the memtable, so a crash can be recovered by
// replaying the log. A torn final record (crash mid-append) is ignored on
// replay.
type WAL struct {
	mu   sync.Mutex
	file *os.File
}

func openWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &WAL{file: f}, nil
}

// Append encodes one record as a length-prefixed, CRC32-protected frame and
// durably writes it before returning.
func (w *WAL) Append(op byte, key, value string) error {
	e := walEntry{Op: op, Key: key, Value: value}
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	frame := make([]byte, 0, len(payload)+8)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	hash := crc32.NewIEEE()
	hash.Write(frame)
	frame = binary.BigEndian.AppendUint32(frame, hash.Sum32())

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Write(frame); err != nil {
		return err
	}
	return w.file.Sync()
}

// Replay returns every complete, CRC-valid record in the log, in order.
func (w *WAL) Replay() ([]walEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var out []walEntry
	head := make([]byte, 4)
	for {
		if _, err := io.ReadFull(w.file, head); err != nil {
			break // end of log or torn final record: stop at last complete record
		}
		length := binary.BigEndian.Uint32(head)
		if length > 16<<20 {
			break // corrupt length
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
			break
		}
		var e walEntry
		if err := json.Unmarshal(payload, &e); err != nil {
			break
		}
		out = append(out, e)
	}
	return out, nil
}

// Truncate discards the log contents after a successful flush.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	_, err := w.file.Seek(0, io.SeekStart)
	return err
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
