package simcore

// FNV-1a 64-bit. Used for every checksum in this project because it is
// order-sensitive, allocation-free, and identical on every platform, which is
// exactly what a golden-file invariant needs. It is NOT a security hash.

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// NewHash returns the FNV-1a starting state.
func NewHash() uint64 { return fnvOffset64 }

// HashByte folds one byte into h.
func HashByte(h uint64, b byte) uint64 {
	h ^= uint64(b)
	h *= fnvPrime64
	return h
}

// HashU64 folds a 64-bit value into h, little-endian.
func HashU64(h uint64, v uint64) uint64 {
	for i := 0; i < 8; i++ {
		h = HashByte(h, byte(v))
		v >>= 8
	}
	return h
}

// HashU32 folds a 32-bit value into h, little-endian.
func HashU32(h uint64, v uint32) uint64 {
	for i := 0; i < 4; i++ {
		h = HashByte(h, byte(v))
		v >>= 8
	}
	return h
}

// HashI64 folds a signed value via its two's-complement bit pattern.
func HashI64(h uint64, v int64) uint64 { return HashU64(h, uint64(v)) }

// HashString folds a string's bytes into h.
func HashString(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h = HashByte(h, s[i])
	}
	return h
}
