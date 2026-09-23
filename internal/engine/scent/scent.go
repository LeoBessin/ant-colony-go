// Package scent is a SEMANTIC variant of the simulation, built on `gradient`,
// not an optimization stage. It carries its own golden files.
//
// `gradient` fixed how a loaded ant finds its way HOME. It left the other half
// of foraging untouched: how a searching ant finds FOOD. In both engines the
// food field is written only by ants already carrying food, so a pile nobody
// has reached yet is invisible, and a pile is a single cell that an ant has to
// land on exactly. Walking past one cell away counts for nothing. Measured on
// `medium` (`flatgrid`, identical to `naive`, 1600 ticks): one pile out of
// eight is eaten, three are never touched, and summed over the whole run the
// untouched piles see 7 to 23 searcher-ticks within three cells.
//
// The single change: a searching ant smells the nearest pile that still holds
// food within scentRadius, and a step that brings it closer earns scentBias.
// It is the exact mirror of the nest bias a carrying ant already uses, same
// magnitude, same Chebyshev distance, and it stops the instant the pile is
// empty, so ants are not drawn back to a depleted source.
//
// The radius is the whole design. Like the nest bias, the pull is a straight
// line and cannot route around a concave wall; too wide and it reaches through
// the enclosure and pins searchers against its inner face. Swept on `medium`
// (5 seeds): 24 is the best radius, and 48 collapses the late game (see
// docs/journal/05-moteur-scent.md).
//
// Everything else — the three-phase tick, the per-ant PRNG, the two
// unconditional draws, the fixed scan order, fixed-point levels, the double
// buffer, gradient's distance-scaled deposits, the deliberately slow data
// structures — is `gradient` verbatim, so this package is still a valid
// baseline for the same optimization ladder.
package scent

import (
	"context"
	"runtime"
	"strconv"
	"time"

	"antcolony/internal/config"
	"antcolony/internal/simcore"
)

func init() {
	simcore.Register("scent", func() simcore.Engine { return &Engine{} })
}

// Engine holds no state; all state lives in World.
type Engine struct{}

func (*Engine) Name() string { return "scent" }

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

	Walls []bool
	Food  []int

	PheroFood []uint32 // current level, read by phase A
	PheroHome []uint32
	depFood   []uint32 // deposits accumulated during phase B
	depHome   []uint32
	nextFood  []uint32 // write buffer for phase C
	nextHome  []uint32

	intents []intent

	Collected int64
	FoodLeft  int64

	randomNum uint32
	randomDen uint32
	nestBias  uint64

	piles     []Point // food pile cells, in cfg.Food order
	scentBias uint64
}

// idx is the flat cell address: one multiply-add, no allocation, no hash.
// Replaces the string key(x,y) this package used before — profiling showed
// it at 61.86% of CPU and 99.60% of allocations (docs/journal/06-scentgrid.md),
// the exact same shape as naive's pre-v1 profile, because this package was
// branched from gradient/naive before flatgrid existed.
func (w *World) idx(x, y int64) int64 { return y*w.W + x }

// trailKey builds the same "x,y" string the old key(x,y) produced, but with
// strconv instead of fmt.Sprintf. Exists only because Ant.Trail (still a
// []string — nobody reads it, v3-equivalent cleanup would remove it) needs
// *a* string; this keeps the package free of fmt.Sprintf without touching
// Trail's behavior.
func trailKey(x, y int64) string {
	return strconv.FormatInt(x, 10) + "," + strconv.FormatInt(y, 10)
}

// Fixed scan order: N, NE, E, SE, S, SW, W, NW.
//
// "Fixed" is load-bearing. Phase A breaks pheromone ties by taking the lowest
// index in this order, so the order is part of the simulation's definition,
// not an implementation detail. Any engine that changes it produces different
// output and fails the golden test.
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
		// A carrying ant with no home trail to follow still needs to find its
		// way back, so closing in on the nest is worth a quarter of a
		// saturated trail. Integer, so it reorders safely.
		nestBias: uint64(cfg.Pheromone.Max / 4),
		// The searching counterpart of nestBias, same magnitude. Swept from
		// Max/16 to Max at radius 24: all within noise, so the symmetric
		// choice wins.
		scentBias: uint64(cfg.Pheromone.Max / 4),
	}

	// Walls and food are built from ORDERED config slices, never by iterating
	// a map. Go randomizes map iteration order; building world state from one
	// would make the run irreproducible from the very first tick.
	for _, r := range cfg.Walls {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				w.Walls[w.idx(int64(x), int64(y))] = true
			}
		}
	}
	for _, f := range cfg.Food {
		k := w.idx(int64(f.At.X), int64(f.At.Y))
		w.Food[k] += f.Amount
		w.FoodLeft += int64(f.Amount)
		w.piles = append(w.piles, Point{int64(f.At.X), int64(f.At.Y)})
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
// `naive` baseline (and from `gradient`), so the golden gate holds it to its
// own reference file and the benchmark harness never reports it as a speedup
// of the baseline.
func (*Engine) Variant() string { return "scent" }

// scentRadius is how far (Chebyshev) a live pile can be smelled. Swept on
// `medium`, mean over 5 seeds, food collected at tick 400 / 1600: 0 (i.e.
// gradient) 549 / 1061, 8 660 / 1196, 16 706 / 1227, 24 786 / 1388, 32
// 749 / 1409, 48 691 / 973. Past 24 the straight-line pull starts reaching
// through walls; at 48 it traps searchers against the enclosure.
const scentRadius = 24
