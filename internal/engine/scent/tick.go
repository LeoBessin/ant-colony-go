package scent

import "antcolony/internal/config"

// tick advances the world by one step, in three strictly separated phases.
//
//	A. SENSE   reads the pheromone grid, writes nothing but intents.
//	B. COMMIT  applies intents in ascending ant index, strictly serial.
//	C. DECAY   evaporates and diffuses, then swaps the double buffer.
//
// The split exists from v0 even though v0 is single-threaded, because phases A
// and C are cell/ant independent and can later be striped across cores
// WITHOUT changing the output. Phase B stays serial forever: it resolves
// conflicts (two ants reaching the last food unit) and its outcome genuinely
// depends on ant order.
func (w *World) tick() {
	w.sense()
	w.commit()
	w.decay()
}

// --- Phase A: sense (read-only) ---------------------------------------------

func (w *World) sense() {
	for i := 0; i < len(w.Ants); i++ {
		w.intents[i] = w.chooseMove(w.Ants[i])
	}
}

func (w *World) chooseMove(a *Ant) intent {
	// A searching ant follows the FOOD trail; a carrying ant follows the HOME
	// trail. The two grids are what turn a random walk into a colony.
	trail := w.PheroFood
	if a.HasFood {
		trail = w.PheroHome
	}

	// a.Dir ALREADY holds the reversed heading when the previous tick failed to
	// move: both failure paths below store (d+4)&7. Reversing a second time
	// here would restore the original heading, so a boxed-in ant would re-test
	// the exact same three impassable cells every tick and wedge itself against
	// the obstacle until a wander roll happened to shake it loose. Trust the
	// stored heading; the turn-around has already been made.
	dir := a.Dir

	// EXACTLY TWO PRNG draws per ant per tick, unconditionally.
	//
	// Both values are drawn before any branch, even when the second is
	// unused. That keeps each ant's stream position a pure function of the
	// tick number, independent of the world around it. If draws were
	// conditional, an ant's random sequence would depend on what its
	// neighbours did, and the parallel engine could not reproduce it.
	roll := a.Rng.Intn(w.randomDen)
	wanderPick := a.Rng.Intn(3)

	// Candidate headings: the three-cell cone ahead, listed STRAIGHT FIRST,
	// then left, then right.
	//
	// The order is both a determinism contract and a behavioural one. Ties are
	// broken by lowest index, and on an untouched grid every candidate scores
	// zero — so whichever heading sits at index 0 is the one an ant with no
	// information takes. Putting a turn there makes searching ants spiral in
	// place and never leave the nest; putting "straight" there makes them
	// explore ballistically and actually find food.
	cand := [3]int64{dir, (dir + 7) & 7, (dir + 1) & 7}

	if roll < w.randomNum {
		return w.step(a, cand[wanderPick])
	}

	var scent Point
	hasScent := false
	if !a.HasFood {
		scent, hasScent = w.nearestScent(a.X, a.Y)
	}

	best := -1
	var bestScore uint64
	for k := 0; k < 3; k++ {
		d := cand[k]
		nx, ny := a.X+dirDX[d], a.Y+dirDY[d]
		if !w.passable(nx, ny) {
			continue
		}
		score := uint64(trail[w.idx(nx, ny)])
		if a.HasFood && cheb(nx, ny, w.Nest) < cheb(a.X, a.Y, w.Nest) {
			score += w.nestBias
		}
		if !a.HasFood && hasScent && cheb(nx, ny, scent) < cheb(a.X, a.Y, scent) {
			score += w.scentBias
		}
		// Strict >, ascending k: ties go to the lowest index in the cone.
		if best < 0 || score > bestScore {
			best, bestScore = k, score
		}
	}
	if best < 0 {
		// Boxed in on all three headings: stay put and reverse.
		return intent{NX: a.X, NY: a.Y, Dir: (dir + 4) & 7, Blocked: true}
	}
	return w.step(a, cand[best])
}

// nearestScent returns the closest pile that still holds food and lies within
// scentRadius (Chebyshev) of (x, y).
//
// Scans w.piles, an ORDERED slice built from cfg.Food, never the Food map; on
// equal distance the earlier pile wins. Phase A only READS w.Food — phase B is
// its sole writer — so every ant in a tick sees the same set of live piles
// whatever order phase A runs in.
func (w *World) nearestScent(x, y int64) (Point, bool) {
	var best Point
	bestD := int64(-1)
	for _, p := range w.piles {
		if w.Food[w.idx(p.X, p.Y)] <= 0 {
			continue
		}
		d := cheb(x, y, p)
		if d > scentRadius {
			continue
		}
		if bestD < 0 || d < bestD {
			best, bestD = p, d
		}
	}
	return best, bestD >= 0
}

func (w *World) step(a *Ant, d int64) intent {
	nx, ny := a.X+dirDX[d], a.Y+dirDY[d]
	if !w.passable(nx, ny) {
		return intent{NX: a.X, NY: a.Y, Dir: (d + 4) & 7, Blocked: true}
	}
	return intent{NX: nx, NY: ny, Dir: d}
}

func (w *World) passable(x, y int64) bool {
	if x < 0 || y < 0 || x >= w.W || y >= w.H {
		return false
	}
	return !w.Walls[w.idx(x, y)]
}

func cheb(x, y int64, p Point) int64 {
	dx := x - p.X
	if dx < 0 {
		dx = -dx
	}
	dy := y - p.Y
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

// --- Phase B: commit (serial, ascending ant index) --------------------------

func (w *World) commit() {
	p := w.cfg.Pheromone
	for i := 0; i < len(w.Ants); i++ {
		a := w.Ants[i]
		in := w.intents[i]
		a.X, a.Y, a.Dir, a.Blocked = in.NX, in.NY, in.Dir, in.Blocked
		a.Steps++

		cell := w.idx(a.X, a.Y)

		// A growing string history nobody reads. This is the clearest single
		// source of GC pressure in v0 and the first thing stage v3 deletes.
		a.Trail = append(a.Trail, trailKey(a.X, a.Y))

		if !a.HasFood {
			if n := w.Food[cell]; n > 0 {
				w.Food[cell] = n - 1
				w.FoodLeft--
				a.HasFood = true
				a.Dir = (a.Dir + 4) & 7 // head back the way it came
				a.Steps = 0             // ANCHOR: zero distance from this food
			}
		} else if a.X == w.Nest.X && a.Y == w.Nest.Y {
			w.Collected++
			a.HasFood = false
			a.Dir = (a.Dir + 4) & 7
			a.Steps = 0 // ANCHOR: zero distance from the nest
		}

		// Deposit AFTER pickup/dropoff, so an ant that just found food
		// immediately starts marking the route to it.
		//
		// THE ONE CHANGE versus naive: the amount is scaled down by how far
		// the ant has walked since its anchor event. A mark laid two steps out
		// of the nest is worth far more than one laid two hundred steps out,
		// so the summed field slopes upward toward the anchor instead of
		// merely recording that somebody passed. Steps counts path length, so
		// an ant that had to walk around a wall reports the longer distance
		// and the field encodes geodesic, not straight-line, closeness.
		if a.HasFood {
			w.depFood[cell] = addSat(w.depFood[cell], falloff(p.DepositFood, a.Steps), p.Max)
		} else {
			w.depHome[cell] = addSat(w.depHome[cell], falloff(p.DepositHome, a.Steps), p.Max)
		}
	}
}

// falloff scales a deposit by the ant's distance from its anchor, halving it
// every falloffHalfLife steps walked:
//
//	base >> (steps / halfLife)
//
// A shift, so no float ever enters the tick and the result stays reorderable.
//
// The decay has to be EXPONENTIAL, not linear or hyperbolic: the field a cell
// settles at is proportional to the traffic through it, and traffic varies by
// more than an order of magnitude between a corridor and open ground. A gentle
// falloff is simply outvoted by crowding — measured, a hyperbolic curve only
// lifted navigability from 9% to 28%. Halving gives a range of hundreds across
// one nest-to-food leg, which distance wins comfortably.
func falloff(base uint32, steps int64) uint32 {
	if steps <= 0 {
		return base
	}
	sh := uint64(steps) / falloffHalfLife
	if sh >= 32 {
		return 0
	}
	return uint32(uint64(base) >> sh)
}

// falloffHalfLife is the path length over which a deposit halves. Swept on
// `medium`: 4 and 8 give 53% navigability, 16 gives 58%, 32 and 64 fall back
// to 29% as the curve flattens into the crowding noise again. 16 also sits at
// roughly a third of a nest-to-food leg (~44 cells), so a full leg spans about
// three halvings.
const falloffHalfLife = 16

func addSat(a, b, max uint32) uint32 {
	v := uint64(a) + uint64(b)
	if v > uint64(max) {
		return max
	}
	return uint32(v)
}

// --- Phase C: decay and diffuse ---------------------------------------------

func (w *World) decay() {
	p := w.cfg.Pheromone
	w.decayGrid(w.PheroFood, w.depFood, w.nextFood, p)
	w.decayGrid(w.PheroHome, w.depHome, w.nextHome, p)

	// Swap the double buffer. Reading from one grid and writing to another is
	// what makes phase C order-independent: no cell can ever observe another
	// cell's new value. In-place update would tie the result to traversal
	// order and forbid striping it across cores.
	w.PheroFood, w.nextFood = w.nextFood, w.PheroFood
	w.PheroHome, w.nextHome = w.nextHome, w.PheroHome

	clear(w.depFood)
	clear(w.depHome)
	clear(w.nextFood)
	clear(w.nextHome)
}

func (w *World) decayGrid(cur, dep, next []uint32, p config.PheromoneParams) {
	dn, dd := uint64(p.DiffNum), uint64(p.DiffDen)
	en, ed := uint64(p.EvapNum), uint64(p.EvapDen)
	max := uint64(p.Max)

	// Row-major traversal over every cell — the definition of the simulation
	// (CLAUDE.md §4 rule 5), not a property of the map this used to read from.
	for y := int64(0); y < w.H; y++ {
		for x := int64(0); x < w.W; x++ {
			i := w.idx(x, y)
			v := uint64(cur[i]) + uint64(dep[i])

			in := w.level(cur, dep, x, y-1) +
				w.level(cur, dep, x, y+1) +
				w.level(cur, dep, x-1, y) +
				w.level(cur, dep, x+1, y)

			// Diffusion is applied to pre-evaporation values. Evaporation is
			// a uniform linear scale, so scaling after spreading equals
			// spreading after scaling — one fewer pass, same answer.
			// Everything is integer: truncation is deterministic, float
			// rounding would not be reorderable.
			out := v * dn / dd
			v = v - out + (in*dn/dd)/4
			v = v * en / ed
			if v > max {
				v = max
			}
			if v != 0 {
				next[i] = uint32(v)
			}
		}
	}
}

// level reads a cell's pre-decay level, treating out-of-bounds as empty.
func (w *World) level(cur, dep []uint32, x, y int64) uint64 {
	if x < 0 || y < 0 || x >= w.W || y >= w.H {
		return 0
	}
	i := w.idx(x, y)
	return uint64(cur[i]) + uint64(dep[i])
}
