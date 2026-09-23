// Package nogc is v3: internal/engine/parallel with exactly one change.
//
// Ant.Trail — a []string of every position an ant ever visited, appended once
// per ant per tick since naive and never read by anything — is deleted. That
// is the ONLY change from parallel (CLAUDE.md §5 rule 3).
//
// Built on v4 rather than v2 as the original roadmap drew it: the stages are
// taken in the order the profiles justify them, and it was v4's profile that
// made Trail the problem. Two measurements (large, 2026-09-23):
//   - heap: 99% of alloc_space in parallel is Trail — 650 MB of slice growth
//     in commit plus 128 MB of "x,y" strings over 6000 ticks, all retained
//     until Run returns (RSS grows linearly, ~120 MB/s);
//   - CPU: commit is serial and became ~36% of wall time once phases A and C
//     were parallel; the Trail append is its only allocation.
//
// Trail is not part of the result: result() hashes ID, X, Y, Dir and HasFood,
// never Trail, and snapshot() does not read it. Deleting it cannot move the
// checksum — make test is what proves that claim rather than this comment.
package nogc

import (
	"context"
	"runtime"
	"time"

	"antcolony/internal/config"
	"antcolony/internal/simcore"
)

func init() {
	simcore.Register("nogc", func() simcore.Engine { return &Engine{} })
}

// Engine is the v3 implementation. It holds no state; all state lives in World.
type Engine struct{}

func (*Engine) Name() string { return "nogc" }

// Point is a grid coordinate. Kept int64, unchanged from naive: shrinking it
// is a SoA/alignment concern (stage v2), not an addressing concern.
type Point struct{ X, Y int64 }

// Ant is copied verbatim from naive, padding-hostile field order included.
// Fixing that is stage v2's one change, not this one.
type Ant struct {
	ID      int64
	HasFood bool
	X       int64
	Blocked bool
	Y       int64
	Dir     int64
	Home    Point
	Steps   int64
	Rng     simcore.Rng
	// Trail []string — deleted: this stage's one change.
}

// intent is what phase A decides and phase B applies, unchanged from naive.
type intent struct {
	NX, NY  int64
	Dir     int64
	Blocked bool
}

// World is flatgrid's state plus the worker pool.
//
// (Original flatgrid comment follows.)
//
// Same fields as naive.World, same value types (bool, int, uint32) — the
// only change is the container: map[string]T becomes []T, addressed by
// idx(x,y) instead of key(x,y). A cell lookup is now one multiply-add and
// one indexed load instead of a Sprintf, a hash and a bucket probe.
type World struct {
	cfg  config.Config
	W, H int64
	Nest Point

	Ants []*Ant // still a slice of pointers; v2's change, not this one

	Walls []bool
	Food  []int

	PheroFood []uint32 // current level, read by phase A
	PheroHome []uint32
	depFood   []uint32 // deposits accumulated during phase B
	depHome   []uint32
	nextFood  []uint32 // write buffer for phase C
	nextHome  []uint32

	intents []intent

	pool *pool

	Collected int64
	FoodLeft  int64

	randomNum uint32
	randomDen uint32
	nestBias  uint64
}

// idx is the v1 cell address: one multiply-add, no allocation, no hash.
// Replaces naive's key(x,y) string for every grid-shaped field.
func (w *World) idx(x, y int64) int64 { return y*w.W + x }

// Fixed scan order, identical to naive: this is part of the simulation's
// definition (CLAUDE.md §4 rule 6), not an implementation detail a stage is
// free to touch.
var dirDX = [8]int64{0, 1, 1, 1, 0, -1, -1, -1}
var dirDY = [8]int64{-1, -1, 0, 1, 1, 1, 0, -1}

func newWorld(cfg config.Config) *World {
	W, H := int64(cfg.Width), int64(cfg.Height)
	cells := W * H
	w := &World{
		cfg:       cfg,
		W:         W,
		H:         H,
		Nest:      Point{int64(cfg.Nest.X), int64(cfg.Nest.Y)},
		Walls:     make([]bool, cells),
		Food:      make([]int, cells),
		PheroFood: make([]uint32, cells),
		PheroHome: make([]uint32, cells),
		depFood:   make([]uint32, cells),
		depHome:   make([]uint32, cells),
		nextFood:  make([]uint32, cells),
		nextHome:  make([]uint32, cells),
		intents:   make([]intent, cfg.AntCount),
		randomNum: cfg.Movement.RandomNum,
		randomDen: cfg.Movement.RandomDen,
		nestBias:  uint64(cfg.Pheromone.Max / 4),
	}

	// Same ordered-slice construction as naive: never derived from map
	// iteration, so this stays reproducible from the first tick.
	for _, r := range cfg.Walls {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				w.Walls[w.idx(int64(x), int64(y))] = true
			}
		}
	}
	for _, f := range cfg.Food {
		i := w.idx(int64(f.At.X), int64(f.At.Y))
		w.Food[i] += f.Amount
		w.FoodLeft += int64(f.Amount)
	}

	// Per-ant PRNG, byte-identical derivation to naive: this is what makes
	// the two engines' outputs comparable at all.
	w.Ants = make([]*Ant, cfg.AntCount)
	for i := 0; i < cfg.AntCount; i++ {
		seed := simcore.SplitMix64(cfg.Seed ^ (uint64(i)*0x9E3779B97F4A7C15 + 0x632BE59BD9B4E019))
		a := &Ant{
			ID:   int64(i),
			X:    w.Nest.X,
			Y:    w.Nest.Y,
			Home: w.Nest,
			Rng:  simcore.SeedRng(seed),
		}
		a.Dir = int64(a.Rng.Intn(8))
		w.Ants[i] = a
	}
	return w
}

// Run is flatgrid's, plus starting and stopping the worker pool. The pool is
// sized from GOMAXPROCS so `go test -cpu` and `antsim -gomaxprocs` sweep the
// worker count without a code change; the count never reaches the result.
func (e *Engine) Run(ctx context.Context, cfg config.Config, obs simcore.Observer) (simcore.Result, error) {
	every := simcore.ObserveEvery(obs)
	w := newWorld(cfg)
	w.pool = newPool(w, runtime.GOMAXPROCS(0))
	defer w.pool.stop()

	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	start := time.Now()

	ticksRun := 0
	for t := 0; t < cfg.Ticks; t++ {
		if ctx.Err() != nil {
			break
		}
		w.tick()
		ticksRun++
		if every > 0 && t%every == 0 {
			obs.OnTick(t, w.snapshot(t, false))
		}
	}

	elapsed := time.Since(start)
	runtime.ReadMemStats(&m1)

	if every > 0 {
		obs.OnTick(ticksRun, w.snapshot(ticksRun, true))
	}

	res := w.result()
	res.Engine = e.Name()
	res.TicksRun = ticksRun
	res.WallNS = elapsed.Nanoseconds()
	res.Allocs = m1.Mallocs - m0.Mallocs
	res.HeapBytes = m1.TotalAlloc - m0.TotalAlloc
	return res.ComputeChecksum(), nil
}
