package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

const maxFrameSize = 16 << 20 // 16 MiB

type wireReq struct {
	Op     string `json:"op"`
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Prefix string `json:"prefix,omitempty"`
	After  string `json:"after,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type wireResp struct {
	OK    bool     `json:"ok,omitempty"`
	Value string   `json:"value,omitempty"`
	KVs   []wireKV `json:"kvs,omitempty"`
	Error string   `json:"error,omitempty"`
}

type wireKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// engineClient talks to the Kestrel storage engine over its length-prefixed
// JSON TCP protocol, one request per connection.
type engineClient struct {
	addr string
}

func (c *engineClient) call(req wireReq) (wireResp, error) {
	conn, err := net.DialTimeout("tcp", c.addr, 3*time.Second)
	if err != nil {
		return wireResp{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	payload, err := json.Marshal(req)
	if err != nil {
		return wireResp{}, err
	}
	frame := make([]byte, 0, 4+len(payload))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	if _, err := conn.Write(frame); err != nil {
		return wireResp{}, err
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return wireResp{}, err
	}
	length := binary.BigEndian.Uint32(header)
	if length > maxFrameSize {
		return wireResp{}, errors.New("response frame too large")
	}
	respBuf := make([]byte, length)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		return wireResp{}, err
	}
	var resp wireResp
	if err := json.Unmarshal(respBuf, &resp); err != nil {
		return wireResp{}, err
	}
	return resp, nil
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
