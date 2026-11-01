package engine

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"time"
)

const maxFrameSize = 32 << 20 // 32 MiB max TCP wire frame

// frameReq is one client request over the length-prefixed JSON protocol.
type frameReq struct {
	Op     string    `json:"op"`
	Key    string    `json:"key,omitempty"`
	Value  string    `json:"value,omitempty"`
	Prefix string    `json:"prefix,omitempty"`
	After  string    `json:"after,omitempty"`
	Limit  int       `json:"limit,omitempty"`
	Ops    []BatchOp `json:"ops,omitempty"`
}

// frameResp is one server response.
type frameResp struct {
	OK      bool        `json:"ok,omitempty"`
	Value   string      `json:"value,omitempty"`
	KVs     []KVPair    `json:"kvs,omitempty"`
	Deleted int         `json:"deleted,omitempty"`
	Stats   *StoreStats `json:"stats,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func serveConn(ks KeyStore, conn net.Conn) {
	defer conn.Close()
	header := make([]byte, 4)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header)
		if length == 0 || length > maxFrameSize {
			log.Printf("engine: invalid frame size: %d bytes", length)
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		var req frameReq
		if err := json.Unmarshal(payload, &req); err != nil {
			_ = writeFrame(conn, frameResp{Error: "invalid request: " + err.Error()})
			continue
		}
		resp := handleReq(ks, req)
		_ = conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if err := writeFrame(conn, resp); err != nil {
			return
		}
	}
}

func handleReq(ks KeyStore, req frameReq) frameResp {
	switch req.Op {
	case "set":
		if err := ks.Set([]byte(req.Key), []byte(req.Value)); err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true}
	case "delete":
		if err := ks.Delete([]byte(req.Key)); err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true}
	case "batch":
		if err := ks.WriteBatch(req.Ops); err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true}
	case "delete_prefix":
		deleted, err := ks.DeletePrefix(req.Prefix)
		if err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true, Deleted: deleted}
	case "compact":
		if err := ks.Compact(); err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true}
	case "stats":
		st := ks.Stats()
		return frameResp{OK: true, Stats: &st}
	case "get":
		value, _, err := ks.Get(req.Key)
		if err != nil {
			if errors.Is(err, errNotFound) {
				return frameResp{Error: "not found"}
			}
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true, Value: string(value)}
	case "scan":
		kvs, err := ks.ScanAfter(req.Prefix, req.After, req.Limit)
		if err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true, KVs: kvs}
	default:
		return frameResp{Error: "unknown op: " + req.Op}
	}
}

func writeFrame(conn net.Conn, resp frameResp) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	frame := make([]byte, 0, 4+len(payload))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	_, err = conn.Write(frame)
	return err
}

func serveKeyStore(ks KeyStore, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go serveConn(ks, conn)
	}
}

// Serve listens on addr and serves store connections until closed.
func (s *LSMStore) Serve(addr string) error {
	return serveKeyStore(s, addr)
}

// Serve listens on addr and serves sharded store connections until closed.
func (ss *ShardedStore) Serve(addr string) error {
	return serveKeyStore(ss, addr)
}
