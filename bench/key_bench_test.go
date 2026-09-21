package bench

import (
	"fmt"
	"strconv"
	"testing"
)

// The v0 engine addresses every grid cell with fmt.Sprintf("%d,%d", x, y), and
// does so roughly ten times per cell per tick. These micro-benchmarks isolate
// that one call so the report can attribute the cost precisely instead of
// hand-waving at the flamegraph:
//
//	Sprintf   -> formatting machinery, reflection over the argument slice, and
//	             a heap-allocated string
//	Strconv   -> the same string, without the formatter
//	FlatIndex -> what stage v1 replaces all of it with: one multiply-add
//
// Hypothesis to verify with -benchmem: Sprintf allocates, FlatIndex does not,
// and the gap is two orders of magnitude.

var (
	sinkStr string
	sinkInt int
)

func BenchmarkKeySprintf(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkStr = fmt.Sprintf("%d,%d", i&127, i>>7&127)
	}
}

func BenchmarkKeyStrconv(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkStr = strconv.Itoa(i&127) + "," + strconv.Itoa(i>>7&127)
	}
}

func BenchmarkKeyFlatIndex(b *testing.B) {
	const w = 128
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkInt = (i>>7&127)*w + i&127
	}
}

// BenchmarkGridLookup contrasts the two grid representations directly, with no
// simulation logic around them: the same 128x128 grid as a string-keyed map
// and as a flat slice.
func BenchmarkGridLookupMap(b *testing.B) {
	const w, h = 128, 128
	m := make(map[string]uint32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m[fmt.Sprintf("%d,%d", x, y)] = uint32(x + y)
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	var s uint32
	for i := 0; i < b.N; i++ {
		s += m[fmt.Sprintf("%d,%d", i&127, i>>7&127)]
	}
	sinkInt = int(s)
}

func BenchmarkGridLookupFlat(b *testing.B) {
	const w, h = 128, 128
	g := make([]uint32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			g[y*w+x] = uint32(x + y)
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	var s uint32
	for i := 0; i < b.N; i++ {
		s += g[(i>>7&127)*w+i&127]
	}
	sinkInt = int(s)
}
