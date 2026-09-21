package bench

import (
	"math/rand"
	"testing"

	"antcolony/internal/simcore"
)

// The PRNG is drawn twice per ant per tick, so it sits squarely on the hot
// path. These two benchmarks justify not using math/rand: rand.Rand dispatches
// through a Source interface on every call, and the package-level functions
// take a mutex. Quote the delta in the report's memory/CPU axis.

var sinkU64 uint64

func BenchmarkRngXoshiro(b *testing.B) {
	r := simcore.SeedRng(1)
	b.ReportAllocs()
	var s uint64
	for i := 0; i < b.N; i++ {
		s += r.Next()
	}
	sinkU64 = s
}

func BenchmarkRngXoshiroIntn(b *testing.B) {
	r := simcore.SeedRng(1)
	b.ReportAllocs()
	var s uint64
	for i := 0; i < b.N; i++ {
		s += uint64(r.Intn(100))
	}
	sinkU64 = s
}

func BenchmarkRngMathRandLocal(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	b.ReportAllocs()
	var s uint64
	for i := 0; i < b.N; i++ {
		s += uint64(r.Intn(100))
	}
	sinkU64 = s
}

func BenchmarkRngMathRandGlobal(b *testing.B) {
	b.ReportAllocs()
	var s uint64
	for i := 0; i < b.N; i++ {
		s += uint64(rand.Intn(100))
	}
	sinkU64 = s
}
