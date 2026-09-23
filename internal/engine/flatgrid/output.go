package flatgrid

import "antcolony/internal/simcore"

// result: identical traversal and hashing order to naive (ants by ascending
// ID, grid row-major) — only the cell address changed.
func (w *World) result() simcore.Result {
	var carrying int32
	ah := simcore.NewHash()
	for _, a := range w.Ants {
		if a.HasFood {
			carrying++
		}
		ah = simcore.HashI64(ah, a.ID)
		ah = simcore.HashI64(ah, a.X)
		ah = simcore.HashI64(ah, a.Y)
		ah = simcore.HashI64(ah, a.Dir)
		var f byte
		if a.HasFood {
			f = 1
		}
		ah = simcore.HashByte(ah, f)
	}

	gh := simcore.NewHash()
	var total uint64
	for y := int64(0); y < w.H; y++ {
		for x := int64(0); x < w.W; x++ {
			i := w.idx(x, y)
			fv, hv := w.PheroFood[i], w.PheroHome[i]
			gh = simcore.HashU32(gh, fv)
			gh = simcore.HashU32(gh, hv)
			total += uint64(fv) + uint64(hv)
		}
	}

	return simcore.Result{
		ConfigHash:     w.cfg.CanonicalHash(),
		Seed:           w.cfg.Seed,
		Ticks:          w.cfg.Ticks,
		FoodCollected:  w.Collected,
		FoodRemaining:  w.FoodLeft,
		AntsCarrying:   carrying,
		TotalPheromone: total,
		AntPosHash:     ah,
		GridHash:       gh,
	}
}

// snapshot: same UI view as naive, same field-by-field copy, indexed instead
// of hashed.
func (w *World) snapshot(tick int, done bool) *simcore.Snapshot {
	n := len(w.Ants)
	cells := int(w.W * w.H)
	s := &simcore.Snapshot{
		Tick:      tick,
		W:         int(w.W),
		H:         int(w.H),
		Nest:      [2]int{int(w.Nest.X), int(w.Nest.Y)},
		Collected: w.Collected,
		Done:      done,
		AntX:      make([]int32, n),
		AntY:      make([]int32, n),
		AntFlags:  make([]uint8, n),
		PheroFood: make([]uint8, cells),
		PheroHome: make([]uint8, cells),
	}
	for i, a := range w.Ants {
		s.AntX[i] = int32(a.X)
		s.AntY[i] = int32(a.Y)
		if a.HasFood {
			s.AntFlags[i] = 1
		}
	}

	max := uint64(w.cfg.Pheromone.Max)
	for y := int64(0); y < w.H; y++ {
		for x := int64(0); x < w.W; x++ {
			i := w.idx(x, y)
			out := int(y*w.W + x)
			s.PheroFood[out] = scale(uint64(w.PheroFood[i]), max)
			s.PheroHome[out] = scale(uint64(w.PheroHome[i]), max)
			if n := w.Food[i]; n > 0 {
				s.Food = append(s.Food, int32(x), int32(y), int32(n))
			}
			if tick == 0 && w.Walls[i] {
				s.Walls = append(s.Walls, int32(x), int32(y))
			}
		}
	}
	return s
}

func scale(v, max uint64) uint8 {
	if max == 0 || v == 0 {
		return 0
	}
	if v >= max {
		return 255
	}
	return uint8(v * 255 / max)
}
