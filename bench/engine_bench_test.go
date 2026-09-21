// Package bench holds the in-process benchmarks.
//
// Division of labour with hyperfine, which the audit report must state
// explicitly:
//
//   - testing.B answers "which instruction or allocation got cheaper?". It
//     reports ns/op, B/op and allocs/op, and -cpu sweeps give the scalability
//     curve. Its numbers exclude process startup and config parsing.
//   - hyperfine answers "what does the operator actually wait for?". It times
//     the whole binary, startup and JSON output included, with warmup runs and
//     full statistics.
//
// The order-of-magnitude table in the report comes from hyperfine; the
// axis-by-axis evidence comes from benchstat over these.
package bench

import (
	"context"
	"path/filepath"
	"testing"

	"antcolony/internal/config"
	_ "antcolony/internal/engine"
	"antcolony/internal/simcore"
)

func load(b *testing.B, name string) config.Config {
	b.Helper()
	cfg, err := config.Load(filepath.Join("..", "internal", "config", "scenarios", name+".json"))
	if err != nil {
		b.Fatalf("load %s: %v", name, err)
	}
	return cfg
}

// benchScenarios are chosen against this machine's cache hierarchy
// (Ryzen 5 5600X: 512 KB L2 per core, 32 MB shared L3). Once stage v1 turns
// the grid into a flat []uint32, one grid costs Width*Height*4 bytes:
//
//	small   64x64   =  16 KB per grid  -> comfortably inside L1/L2
//	medium  128x128 =  64 KB per grid  -> inside L2
//	large   256x256 = 256 KB per grid  -> four grids exceed L2, still in L3
//
// The jump between medium and large is where cache-locality work should start
// paying off visibly, and the report should say so.
var benchScenarios = []string{"small", "medium"}

// BenchmarkEngine is the primary comparison: every registered engine against
// every scenario, so v0 and every later stage appear in one benchstat table.
func BenchmarkEngine(b *testing.B) {
	for _, sc := range benchScenarios {
		cfg := load(b, sc)
		for _, name := range simcore.Names() {
			eng, err := simcore.New(name)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(sc+"/"+name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := eng.Run(context.Background(), cfg, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkTickRate reports ticks/sec rather than ns/op, which is the unit the
// report talks in and the one the UI displays.
func BenchmarkTickRate(b *testing.B) {
	cfg := load(b, "medium")
	for _, name := range simcore.Names() {
		eng, err := simcore.New(name)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			var ticks int64
			for i := 0; i < b.N; i++ {
				res, err := eng.Run(context.Background(), cfg, nil)
				if err != nil {
					b.Fatal(err)
				}
				ticks += int64(res.TicksRun)
			}
			b.ReportMetric(float64(ticks)/b.Elapsed().Seconds(), "ticks/s")
		})
	}
}

// BenchmarkScaling exists to be run with -cpu 1,2,6,12. Until stage v4 adds a
// worker pool it should show a FLAT line: that flatness is the evidence that
// v0 is single-threaded, and it is the baseline the parallel stage is measured
// against.
func BenchmarkScaling(b *testing.B) {
	cfg := load(b, "small")
	for _, name := range simcore.Names() {
		eng, err := simcore.New(name)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := eng.Run(context.Background(), cfg, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
