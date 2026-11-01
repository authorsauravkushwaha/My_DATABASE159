package engine

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// bloomFilter is a compact probabilistic set membership filter stored in KSS2
// SSTables so point lookups skip runs that definitely do not contain a key
// without touching disk data blocks.
type bloomFilter struct {
	bits []byte
	k    uint8
}

const defaultBitsPerKey = 10

func newBloomFilter(numKeys int, bitsPerKey int) *bloomFilter {
	if bitsPerKey <= 0 {
		bitsPerKey = defaultBitsPerKey
	}
	nbits := numKeys * bitsPerKey
	if nbits < 64 {
		nbits = 64
	}
	nbytes := (nbits + 7) / 8
	nbits = nbytes * 8
	// k = ln(2) * bitsPerKey ~= 0.69 * bitsPerKey
	k := int(float64(bitsPerKey) * 0.69)
	if k < 1 {
		k = 1
	}
	if k > 16 {
		k = 16
	}
	return &bloomFilter{
		bits: make([]byte, nbytes),
		k:    uint8(k),
	}
}

// bloomHash computes two independent 32-bit hashes using FNV-1a 64-bit split
// with Murmur-style finalizer mixing for Kirsch-Mitzenmacher double hashing.
func bloomHash(key string) (uint32, uint32) {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= 1099511628211
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	h1 := uint32(h)
	h2 := uint32(h >> 32)
	if h2 == 0 {
		h2 = 0x9e3779b9
	}
	return h1, h2
}

func (bf *bloomFilter) add(key string) {
	if bf == nil || len(bf.bits) == 0 {
		return
	}
	nbits := uint32(len(bf.bits) * 8)
	h1, h2 := bloomHash(key)
	for i := uint8(0); i < bf.k; i++ {
		bit := (h1 + uint32(i)*h2) % nbits
		bf.bits[bit/8] |= 1 << (bit % 8)
	}
}

func (bf *bloomFilter) mayContain(key string) bool {
	if bf == nil || len(bf.bits) == 0 {
		return true
	}
	nbits := uint32(len(bf.bits) * 8)
	h1, h2 := bloomHash(key)
	for i := uint8(0); i < bf.k; i++ {
		bit := (h1 + uint32(i)*h2) % nbits
		if (bf.bits[bit/8] & (1 << (bit % 8))) == 0 {
			return false
		}
	}
	return true
}

func (bf *bloomFilter) encode() []byte {
	if bf == nil || len(bf.bits) == 0 {
		return nil
	}
	buf := make([]byte, 1+len(bf.bits)+4)
	buf[0] = bf.k
	copy(buf[1:1+len(bf.bits)], bf.bits)
	sum := crc32.ChecksumIEEE(buf[:1+len(bf.bits)])
	binary.BigEndian.PutUint32(buf[1+len(bf.bits):], sum)
	return buf
}

func decodeBloomFilter(buf []byte) (*bloomFilter, error) {
	if len(buf) == 0 {
		return nil, nil
	}
	if len(buf) < 6 {
		return nil, errors.New("corrupt sstable bloom filter: too short")
	}
	payloadLen := len(buf) - 4
	wantCRC := binary.BigEndian.Uint32(buf[payloadLen:])
	if crc32.ChecksumIEEE(buf[:payloadLen]) != wantCRC {
		return nil, errors.New("corrupt sstable bloom filter: checksum mismatch")
	}
	k := buf[0]
	if k == 0 || k > 32 {
		return nil, errors.New("corrupt sstable bloom filter: invalid probe count")
	}
	bits := make([]byte, payloadLen-1)
	copy(bits, buf[1:payloadLen])
	return &bloomFilter{bits: bits, k: k}, nil
}
