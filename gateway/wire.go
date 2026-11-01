package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const maxFrameSize = 32 << 20 // 32 MiB

type wireBatchOp struct {
	Delete bool   `json:"delete,omitempty"`
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
}

type wireStoreStats struct {
	Shards           int     `json:"shards"`
	SSTables         int     `json:"sstables"`
	MemTableBytes    int64   `json:"memtable_bytes"`
	MemTableKeys     int64   `json:"memtable_keys"`
	WALBytes         int64   `json:"wal_bytes"`
	LogicalBytes     uint64  `json:"logical_bytes"`
	DiskBytes        uint64  `json:"disk_bytes"`
	CompressionRatio float64 `json:"compression_ratio"`
	EstimatedKeys    uint64  `json:"estimated_keys"`
	BloomChecks      uint64  `json:"bloom_checks"`
	BloomSkips       uint64  `json:"bloom_skips"`
	CacheHits        uint64  `json:"cache_hits"`
	CacheMisses      uint64  `json:"cache_misses"`
	Compactions      uint64  `json:"compactions"`
}

type wireReq struct {
	Op     string        `json:"op"`
	Key    string        `json:"key,omitempty"`
	Value  string        `json:"value,omitempty"`
	Prefix string        `json:"prefix,omitempty"`
	After  string        `json:"after,omitempty"`
	Limit  int           `json:"limit,omitempty"`
	Ops    []wireBatchOp `json:"ops,omitempty"`
}

type wireResp struct {
	OK      bool            `json:"ok,omitempty"`
	Value   string          `json:"value,omitempty"`
	KVs     []wireKV        `json:"kvs,omitempty"`
	Deleted int             `json:"deleted,omitempty"`
	Stats   *wireStoreStats `json:"stats,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type wireKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// engineClient talks to the Kestrel storage engine over its length-prefixed
// TCP protocol using a reusable connection pool with transparent reconnect on
// stale idle sockets.
type engineClient struct {
	addr string
	once sync.Once
	pool chan net.Conn
}

func (c *engineClient) initPool() {
	c.once.Do(func() {
		c.pool = make(chan net.Conn, 16)
	})
}

func (c *engineClient) acquire() (net.Conn, bool, error) {
	c.initPool()
	select {
	case conn := <-c.pool:
		return conn, true, nil
	default:
		conn, err := net.DialTimeout("tcp", c.addr, 3*time.Second)
		return conn, false, err
	}
}

func (c *engineClient) release(conn net.Conn) {
	c.initPool()
	select {
	case c.pool <- conn:
	default:
		_ = conn.Close()
	}
}

func (c *engineClient) callOnce(conn net.Conn, frame []byte) (wireResp, error) {
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := conn.Write(frame); err != nil {
		_ = conn.Close()
		return wireResp{}, err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		_ = conn.Close()
		return wireResp{}, err
	}
	length := binary.BigEndian.Uint32(header)
	if length == 0 || length > maxFrameSize {
		_ = conn.Close()
		return wireResp{}, errors.New("response frame out of bounds")
	}
	respBuf := make([]byte, length)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		_ = conn.Close()
		return wireResp{}, err
	}
	var resp wireResp
	if err := json.Unmarshal(respBuf, &resp); err != nil {
		_ = conn.Close()
		return wireResp{}, err
	}
	c.release(conn)
	return resp, nil
}

func (c *engineClient) call(req wireReq) (wireResp, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return wireResp{}, err
	}
	frame := make([]byte, 0, 4+len(payload))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)

	conn, reused, err := c.acquire()
	if err != nil {
		return wireResp{}, err
	}
	resp, err := c.callOnce(conn, frame)
	if err != nil && reused {
		// Stale pooled connection (e.g. engine restarted): dial fresh once.
		fresh, dialErr := net.DialTimeout("tcp", c.addr, 3*time.Second)
		if dialErr != nil {
			return wireResp{}, dialErr
		}
		return c.callOnce(fresh, frame)
	}
	return resp, err
}

func (c *engineClient) Set(key, value string) error {
	resp, err := c.call(wireReq{Op: "set", Key: key, Value: value})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

func (c *engineClient) Batch(ops []wireBatchOp) error {
	resp, err := c.call(wireReq{Op: "batch", Ops: ops})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

func (c *engineClient) DeletePrefix(prefix string) (int, error) {
	resp, err := c.call(wireReq{Op: "delete_prefix", Prefix: prefix})
	if err != nil {
		return 0, err
	}
	if resp.Error != "" {
		return 0, errors.New(resp.Error)
	}
	return resp.Deleted, nil
}

func (c *engineClient) Compact() error {
	resp, err := c.call(wireReq{Op: "compact"})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

func (c *engineClient) Stats() (*wireStoreStats, error) {
	resp, err := c.call(wireReq{Op: "stats"})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.Stats == nil {
		return &wireStoreStats{}, nil
	}
	return resp.Stats, nil
}

func (c *engineClient) Get(key string) (string, bool, error) {
	resp, err := c.call(wireReq{Op: "get", Key: key})
	if err != nil {
		return "", false, err
	}
	if resp.Error != "" {
		if resp.Error == "not found" {
			return "", false, nil
		}
		return "", false, errors.New(resp.Error)
	}
	return resp.Value, true, nil
}

func (c *engineClient) Delete(key string) error {
	resp, err := c.call(wireReq{Op: "delete", Key: key})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

func (c *engineClient) Scan(prefix, after string, limit int) ([]wireKV, error) {
	resp, err := c.call(wireReq{Op: "scan", Prefix: prefix, After: after, Limit: limit})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return resp.KVs, nil
}
