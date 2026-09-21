// Package naive is the v0 baseline engine.
//
// It is SLOW ON PURPOSE. Every data-structure choice here is the one a
// developer reaches for when they are not thinking about the machine:
// string-keyed maps, a slice of pointers, padding-hostile field order, and a
// fresh allocation on every tick. Each of those is the documented starting
// point of a later optimization stage (see CLAUDE.md), and the audit report's
// gains are measured against this file.
//
// What it is NOT is incorrect. The three-phase tick, the per-ant PRNG, the
// fixed-point pheromone levels and the double-buffered grid are all present
// from day zero, because they are the properties that make every later
// optimization *provably* output-identical. Removing them would be simpler and
// would make the whole exercise impossible.
package naive

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"antcolony/internal/config"
	"antcolony/internal/simcore"
)

func init() {
	simcore.Register("naive", func() simcore.Engine { return &Engine{} })
}

// Engine is the v0 implementation. It holds no state; all state lives in World.
type Engine struct{}

func (*Engine) Name() string { return "naive" }

// Point is a grid coordinate. int64 everywhere is deliberate waste: two of
// these is 16 bytes to express values that never exceed a few thousand.
type Point struct{ X, Y int64 }

// Ant is the v0 agent.
//
// The field order is deliberately hostile: two bools sandwiched between 8-byte
// fields force the compiler to insert 7 bytes of padding after each one. The
// struct lands near 96 bytes, so fewer than one ant fits in a 64-byte cache
// line and iterating the colony touches roughly twice the memory it needs to.
// Stage v2 fixes this with field reordering and int32/uint8 (see CLAUDE.md).
type Ant struct {
	ID      int64
	HasFood bool
	X       int64
	Blocked bool
	Y       int64
	Dir     int64
	Home    Point
	Steps   int64
	Trail   []string // every position ever visited, as a string: pure GC pressure
	Rng     simcore.Rng
}

// intent is what phase A decides and phase B applies. Separating the two is
// the reason phase A can later run on a worker pool without changing output.
type intent struct {
	NX, NY  int64
	Dir     int64
	Blocked bool
}

// World is the v0 state.
//
// Four string-keyed maps for what is fundamentally a dense 2D array of
// integers. Looking up one cell costs a fmt.Sprintf (which allocates), a hash
// of that string, and a pointer chase into a bucket — roughly 100ns for what
// should be a single indexed load. This is the headline finding of the
// profiling session and the target of stage v1.
type World struct {
	cfg  config.Config
	W, H int64
	Nest Point

	Ants []*Ant // slice of POINTERS: every ant is a separate heap object

	Walls map[string]bool
	Food  map[string]int

	PheroFood map[string]uint32 // current level, read by phase A
	PheroHome map[string]uint32
	depFood   map[string]uint32 // deposits accumulated during phase B
	depHome   map[string]uint32
	nextFood  map[string]uint32 // write buffer for phase C
	nextHome  map[string]uint32

	intents []intent

	Collected int64
	FoodLeft  int64

	randomNum uint32
	randomDen uint32
	nestBias  uint64
}

// key is the naive cell address. fmt.Sprintf allocates a string on every
// single call, and there are ~10 calls per cell per tick. The constitution
// bans this construct precisely because of what the profiler will show here.
func key(x, y int64) string { return fmt.Sprintf("%d,%d", x, y) }

// Fixed scan order: N, NE, E, SE, S, SW, W, NW.
//
// "Fixed" is load-bearing. Phase A breaks pheromone ties by taking the lowest
// index in this order, so the order is part of the simulation's definition,
// not an implementation detail. Any engine that changes it produces different
// output and fails the golden test.
var dirDX = [8]int64{0, 1, 1, 1, 0, -1, -1, -1}
var dirDY = [8]int64{-1, -1, 0, 1, 1, 1, 0, -1}

func newWorld(cfg config.Config) *World {
	w := &World{
		cfg:       cfg,
		W:         int64(cfg.Width),
		H:         int64(cfg.Height),
		Nest:      Point{int64(cfg.Nest.X), int64(cfg.Nest.Y)},
		Walls:     make(map[string]bool),
		Food:      make(map[string]int),
		PheroFood: make(map[string]uint32),
		PheroHome: make(map[string]uint32),
		depFood:   make(map[string]uint32),
		depHome:   make(map[string]uint32),
		nextFood:  make(map[string]uint32),
		nextHome:  make(map[string]uint32),
		intents:   make([]intent, cfg.AntCount),
		randomNum: cfg.Movement.RandomNum,
		randomDen: cfg.Movement.RandomDen,
		// A carrying ant with no home trail to follow still needs to find its
		// way back, so closing in on the nest is worth a quarter of a
		// saturated trail. Integer, so it reorders safely.
		nestBias: uint64(cfg.Pheromone.Max / 4),
	}

	// Walls and food are built from ORDERED config slices, never by iterating
	// a map. Go randomizes map iteration order; building world state from one
	// would make the run irreproducible from the very first tick.
	for _, r := range cfg.Walls {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				w.Walls[key(int64(x), int64(y))] = true
			}
		}
	}
	for _, f := range cfg.Food {
		k := key(int64(f.At.X), int64(f.At.Y))
		w.Food[k] += f.Amount
		w.FoodLeft += int64(f.Amount)
	}

	// Every ant gets its OWN PRNG, derived from the config seed and its index.
	// A single shared stream consumed in ant order would be simpler, but it
	// would tie the output to the iteration order of phase A — and stage v4
	// runs phase A on a worker pool. Per-ant streams are what make that
	// optimization provably output-identical.
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

// Run executes the simulation.
//
// The observer is consulted exactly ONCE, before the loop. tick() itself never
// mentions it, so a headless benchmark run does not pay a single branch for
// the UI's existence.
func (e *Engine) Run(ctx context.Context, cfg config.Config, obs simcore.Observer) (simcore.Result, error) {
	every := simcore.ObserveEvery(obs)
	w := newWorld(cfg)

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
