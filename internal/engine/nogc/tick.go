package nogc

import "antcolony/internal/config"

// tick, chooseMove, step, passable, cheb: identical to flatgrid. sense and
// decay now fan out to the pool; commit is untouched and stays serial.
func (w *World) tick() {
	w.sense()
	w.commit()
	w.decay()
}

// --- Phase A: sense (read-only) ---------------------------------------------

func (w *World) sense() {
	w.pool.run(phaseSense)
}

// senseRange is flatgrid's sense loop over one worker's ants. chooseMove
// advances a.Rng, which is safe only because ant i belongs to one worker.
func (w *World) senseRange(lo, hi int) {
	for i := lo; i < hi; i++ {
		w.intents[i] = w.chooseMove(w.Ants[i])
	}
}

func (w *World) chooseMove(a *Ant) intent {
	trail := w.PheroFood
	if a.HasFood {
		trail = w.PheroHome
	}

	dir := a.Dir

	// EXACTLY TWO PRNG draws per ant per tick, unconditionally — unchanged
	// from naive (CLAUDE.md §4 rule 2).
	roll := a.Rng.Intn(w.randomDen)
	wanderPick := a.Rng.Intn(3)

	cand := [3]int64{dir, (dir + 7) & 7, (dir + 1) & 7}

	if roll < w.randomNum {
		return w.step(a, cand[wanderPick])
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
		if best < 0 || score > bestScore {
			best, bestScore = k, score
		}
	}
	if best < 0 {
		return intent{NX: a.X, NY: a.Y, Dir: (dir + 4) & 7, Blocked: true}
	}
	return w.step(a, cand[best])
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

		if !a.HasFood {
			if n := w.Food[cell]; n > 0 {
				w.Food[cell] = n - 1
				w.FoodLeft--
				a.HasFood = true
				a.Dir = (a.Dir + 4) & 7
			}
		} else if a.X == w.Nest.X && a.Y == w.Nest.Y {
			w.Collected++
			a.HasFood = false
			a.Dir = (a.Dir + 4) & 7
		}

		if a.HasFood {
			w.depFood[cell] = addSat(w.depFood[cell], p.DepositFood, p.Max)
		} else {
			w.depHome[cell] = addSat(w.depHome[cell], p.DepositHome, p.Max)
		}
	}
}

func addSat(a, b, max uint32) uint32 {
	v := uint64(a) + uint64(b)
	if v > uint64(max) {
		return max
	}
	return uint32(v)
}

// --- Phase C: decay and diffuse ---------------------------------------------

func (w *World) decay() {
	w.pool.run(phaseDecay)

	w.PheroFood, w.nextFood = w.nextFood, w.PheroFood
	w.PheroHome, w.nextHome = w.nextHome, w.PheroHome

	// Still serial: the clears are memclr (~1 MB/tick on large) and cannot
	// run inside phaseDecay, since a neighbouring band may still be reading
	// dep. Parallelizing them would need a third barrier per tick — a
	// separate lever, to be justified by its own profile.
	clear(w.depFood)
	clear(w.depHome)
	clear(w.nextFood)
	clear(w.nextHome)
}

// decayRows is one worker's share of phase C: rows [y0, y1) of both grids.
func (w *World) decayRows(y0, y1 int64) {
	p := w.cfg.Pheromone
	w.decayGrid(w.PheroFood, w.depFood, w.nextFood, p, y0, y1)
	w.decayGrid(w.PheroHome, w.depHome, w.nextHome, p, y0, y1)
}

func (w *World) decayGrid(cur, dep, next []uint32, p config.PheromoneParams, y0, y1 int64) {
	dn, dd := uint64(p.DiffNum), uint64(p.DiffDen)
	en, ed := uint64(p.EvapNum), uint64(p.EvapDen)
	max := uint64(p.Max)

	// Each cell depends only on the PREVIOUS tick's cur/dep, never on a
	// cell written this tick (double buffer), so bands are independent and
	// their completion order is irrelevant.
	for y := y0; y < y1; y++ {
		for x := int64(0); x < w.W; x++ {
			i := w.idx(x, y)
			v := uint64(cur[i]) + uint64(dep[i])

			in := w.level(cur, dep, x, y-1) +
				w.level(cur, dep, x, y+1) +
				w.level(cur, dep, x-1, y) +
				w.level(cur, dep, x+1, y)

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

// level reads a cell's pre-decay level, treating out-of-bounds as empty —
// same contract as naive, indexed instead of hashed.
func (w *World) level(cur, dep []uint32, x, y int64) uint64 {
	if x < 0 || y < 0 || x >= w.W || y >= w.H {
		return 0
	}
	i := w.idx(x, y)
	return uint64(cur[i]) + uint64(dep[i])
}
