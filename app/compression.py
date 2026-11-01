"""High-density record compression, shard key hashing, and optional Go engine bridge."""
from __future__ import annotations

import json
import os
import socket
import struct
import zlib
from typing import Any

KZ1_MAGIC = b"KZ1\x00"
COMPRESSION_MIN_BYTES = 96
MAX_RECORD_BYTES = int(os.getenv("MAX_RECORD_BYTES", str(512 * 1024)))  # 512 KiB default
SHARD_BUCKETS = 64


def compute_shard_key(record_id: str) -> int:
    """Deterministic 0..63 virtual partition bucket for horizontal shard distribution."""
    return zlib.crc32(record_id.encode("utf-8")) & (SHARD_BUCKETS - 1)


def serialize_record_json(data: dict[str, Any]) -> bytes:
    return json.dumps(data, separators=(",", ":"), ensure_ascii=False, allow_nan=False).encode("utf-8")


def encode_record_payload(data: dict[str, Any]) -> tuple[dict[str, Any] | None, bytes | None, int, int]:
    """Encode a validated JSON object, compressing with Zlib/DEFLATE when space is saved."""
    raw = serialize_record_json(data)
    raw_len = len(raw)
    if raw_len >= COMPRESSION_MIN_BYTES:
        compressed = KZ1_MAGIC + zlib.compress(raw, level=6)
        if len(compressed) + 8 < raw_len:
            return None, compressed, raw_len, len(compressed)
    return data, None, raw_len, raw_len


def decode_record_payload(inline_data: dict[str, Any] | None, compressed_data: bytes | None) -> dict[str, Any]:
    """Decode a record from either compressed KZ1 storage or inline JSON."""
    if compressed_data is not None:
        if not compressed_data.startswith(KZ1_MAGIC):
            raise ValueError("Unsupported compressed record header")
        decompressor = zlib.decompressobj()
        max_out = MAX_RECORD_BYTES * 2 + 4096
        raw = decompressor.decompress(compressed_data[len(KZ1_MAGIC) :], max_length=max_out)
        if decompressor.unconsumed_tail:
            raise ValueError("Compressed record exceeds maximum decompressed size")
        raw += decompressor.flush()
        parsed = json.loads(raw.decode("utf-8"))
        if not isinstance(parsed, dict):
            raise ValueError("Decoded record payload is not an object")
        return parsed
    if inline_data is not None:
        return inline_data
    return {}


def _engine_call(req: dict[str, Any], timeout: float = 2.0) -> dict[str, Any] | None:
    """Send one frame to the optional Go LSM engine if STORAGE_ENGINE_ADDR is configured."""
    addr = os.getenv("STORAGE_ENGINE_ADDR", "").strip()
    if not addr or ":" not in addr:
        return None
    host, port_str = addr.rsplit(":", 1)
    try:
        port = int(port_str)
    except ValueError:
        return None
    payload = json.dumps(req, separators=(",", ":")).encode("utf-8")
    frame = struct.pack(">I", len(payload)) + payload
    try:
        with socket.create_connection((host, port), timeout=timeout) as sock:
            sock.settimeout(timeout)
            sock.sendall(frame)
            hdr = _recv_exact(sock, 4)
            length = struct.unpack(">I", hdr)[0]
            if length == 0 or length > (32 << 20):
                return None
            body = _recv_exact(sock, length)
            return json.loads(body.decode("utf-8"))
    except OSError:
        return None


def _recv_exact(sock: socket.socket, count: int) -> bytes:
    chunks = bytearray()
    while len(chunks) < count:
        chunk = sock.recv(count - len(chunks))
        if not chunk:
            raise OSError("unexpected EOF from storage engine")
        chunks.extend(chunk)
    return bytes(chunks)


def purge_engine_namespace(project_id: str, collection_id: str | None = None) -> int:
    """Best-effort orphan cleanup on the Go storage engine when configured."""
    p_hex = project_id.encode("utf-8").hex()
    if collection_id is not None:
        c_hex = collection_id.encode("utf-8").hex()
        prefix = f"p:{p_hex}:c:{c_hex}:r:"
    else:
        prefix = f"p:{p_hex}:"
    resp = _engine_call({"op": "delete_prefix", "prefix": prefix})
    if resp and resp.get("ok"):
        return int(resp.get("deleted", 0))
    return 0


def get_engine_stats() -> dict[str, Any] | None:
    """Fetch live LSM engine stats if STORAGE_ENGINE_ADDR is configured and reachable."""
    resp = _engine_call({"op": "stats"})
    if resp and resp.get("ok") and isinstance(resp.get("stats"), dict):
        return resp["stats"]
    return None
