// Package gradient is a SEMANTIC variant of the simulation, not an
// optimization stage.
//
// Every other engine in this repo is required to reproduce `naive` bit for
// bit; this one deliberately does not, and carries its own golden files. It
// exists because `naive` has a navigation defect that no amount of tuning
// fixes: its pheromone fields record *where ants have been*, not *how far
// they are from anything*.
//
// In `naive` every searching ant deposits the same DepositHome wherever it
// stands. The home field therefore measures searcher DENSITY, so its maxima
// sit wherever ants crowd — corridors, jams, dead ends — and the nest is only
// one of ~57 local maxima on a 128x128 map. A carrying ant climbing that field
// is captured by the nearest crowd. Measured on `medium`: greedy ascent of the
// home field reaches the nest from 3% of the map; adding the straight-line
// nest bias lifts that to 9%, and an INFINITELY strong bias still only reaches
// 46%, because "reduce the distance to the nest" points straight into the
// outer face of a wall and cannot route around a concave obstacle. Outside the
// scenario's wall enclosure the figure is zero: not one cell can navigate home.
//
// The single change here: a deposit is scaled down by how far the ant has
// travelled since the event that anchors it — leaving the nest for the home
// field, picking up food for the food field. Because Steps counts ACTUAL PATH
// LENGTH and not Euclidean distance, an ant that walked around a wall carries
// a correspondingly larger number, so the field encodes geodesic distance and
// routes around obstacles for free. That is the property the straight-line
// bias can never have.
//
// Everything else — the three-phase tick, the per-ant PRNG, the two
// unconditional draws, the fixed scan order, fixed-point levels, the double
// buffer, the deliberately slow data structures — is `naive` verbatim, so this
// package is still a valid baseline for the same optimization ladder.
package gradient

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"antcolony/internal/config"
	"antcolony/internal/simcore"
)

func init() {
	simcore.Register("gradient", func() simcore.Engine { return &Engine{} })
}

// Engine holds no state; all state lives in World.
type Engine struct{}

func (*Engine) Name() string { return "gradient" }

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
	// Steps is the number of ticks since this ant's last ANCHOR event: leaving
	// the nest (a drop-off, or the start of the run) while searching, or
	// picking up food while carrying. It is what scales the deposit, and it
	// measures path length walked, not distance as the crow flies.
	Steps int64
	Trail []string // every position ever visited, as a string: pure GC pressure
	Rng   simcore.Rng
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

// Variant marks this engine as implementing a DIFFERENT simulation from the
// `naive` baseline, so the golden gate holds it to its own reference file and
// the benchmark harness never reports it as a speedup of the baseline.
func (*Engine) Variant() string { return "gradient" }
