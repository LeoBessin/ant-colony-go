package simcore

// Rng is a xoshiro256** generator stored as a plain value type.
//
// Why not math/rand: rand.Rand wraps a Source *interface*, so every draw costs
// a dynamic dispatch, and the package-level functions are mutex-guarded. Both
// are unacceptable on a hot path that draws once per ant per tick. A plain
// struct with pointer receivers inlines, and the bit stream is fixed forever,
// which is what makes golden-file determinism possible across Go versions.
type Rng struct {
	s [4]uint64
}

// SplitMix64 is the standard seeding mixer. It is also used to derive
// per-ant seeds from the global config seed.
func SplitMix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	z := x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// SeedRng expands a single 64-bit seed into a full xoshiro state.
func SeedRng(seed uint64) Rng {
	var r Rng
	x := seed
	for i := 0; i < 4; i++ {
		x += 0x9E3779B97F4A7C15
		z := x
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		r.s[i] = z ^ (z >> 31)
	}
	// The all-zero state is a fixed point of xoshiro; avoid it.
	if r.s[0]|r.s[1]|r.s[2]|r.s[3] == 0 {
		r.s[0] = 0x9E3779B97F4A7C15
	}
	return r
}

func rotl(x uint64, k uint) uint64 { return (x << k) | (x >> (64 - k)) }

// Next returns the next 64 raw bits.
func (r *Rng) Next() uint64 {
	result := rotl(r.s[1]*5, 7) * 9
	t := r.s[1] << 17
	r.s[2] ^= r.s[0]
	r.s[3] ^= r.s[1]
	r.s[1] ^= r.s[2]
	r.s[0] ^= r.s[3]
	r.s[2] ^= t
	r.s[3] = rotl(r.s[3], 45)
	return result
}

// Intn returns a uniform value in [0, n) using Lemire's bounded-rejection
// method: one multiply in the common case, and no modulo bias. n must be > 0.
func (r *Rng) Intn(n uint32) uint32 {
	x := uint32(r.Next() >> 32)
	m := uint64(x) * uint64(n)
	lo := uint32(m)
	if lo < n {
		// Rejection threshold: 2^32 mod n.
		thresh := (-n) % n
		for lo < thresh {
			x = uint32(r.Next() >> 32)
			m = uint64(x) * uint64(n)
			lo = uint32(m)
		}
	}
	return uint32(m >> 32)
}
