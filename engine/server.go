package engine

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
)

const maxFrameSize = 16 << 20 // 16 MiB

// frameReq is one client request over the length-prefixed JSON protocol.
type frameReq struct {
	Op     string `json:"op"`
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// frameResp is one server response.
type frameResp struct {
	OK   bool     `json:"ok,omitempty"`
	Value string  `json:"value,omitempty"`
	KVs   []KVPair `json:"kvs,omitempty"`
	Error string  `json:"error,omitempty"`
}

// serveConn handles one client connection: read frames, execute store ops,
// write response frames, until the connection closes.
func (s *LSMStore) serveConn(conn net.Conn) {
	defer conn.Close()
	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header)
		if length > maxFrameSize {
			log.Printf("engine: frame too large: %d bytes", length)
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		var req frameReq
		if err := json.Unmarshal(payload, &req); err != nil {
			_ = s.writeFrame(conn, frameResp{Error: "invalid request: " + err.Error()})
			continue
		}
		resp := s.handleReq(req)
		if err := s.writeFrame(conn, resp); err != nil {
			return
		}
	}
}

func (s *LSMStore) handleReq(req frameReq) frameResp {
	switch req.Op {
	case "set":
		if err := s.Set([]byte(req.Key), []byte(req.Value)); err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true}
	case "delete":
		if err := s.Delete([]byte(req.Key)); err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true}
	case "get":
		value, _, err := s.Get(req.Key)
		if err != nil {
			return frameResp{Error: "not found"}
		}
		return frameResp{OK: true, Value: string(value)}
	case "scan":
		kvs, err := s.Scan(req.Prefix, req.Limit)
		if err != nil {
			return frameResp{Error: err.Error()}
		}
		return frameResp{OK: true, KVs: kvs}
	default:
		return frameResp{Error: "unknown op: " + req.Op}
	}
}

func (s *LSMStore) writeFrame(conn net.Conn, resp frameResp) error {
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

// Serve listens on addr and serves store connections until the listener
// fails or is closed.
func (s *LSMStore) Serve(addr string) error {
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
		go s.serveConn(conn)
	}
}
