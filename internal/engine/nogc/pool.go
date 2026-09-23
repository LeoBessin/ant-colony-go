package nogc

import "sync"

// phase names the parallel work one tick hands to the pool.
type phase uint8

const (
	phaseSense phase = iota // A: ants [lo, hi)
	phaseDecay              // C: rows [lo, hi), both pheromone grids
)

// pool is a fixed set of long-lived workers, started once per Run.
//
// Each worker has its OWN channel, so worker k always receives range k: the
// split is by fixed index ranges, never by work stealing (CLAUDE.md §4 rule
// 8). A shared channel would let whichever goroutine wakes first take a
// range — harmless for the result here, but it would make the partition
// depend on the scheduler, which is exactly what the rule forbids.
type pool struct {
	work []chan phase
	wg   sync.WaitGroup
}

// newPool starts n workers (clamped to [1, H]: a worker with no rows would
// only add a handoff to every phase C).
func newPool(w *World, n int) *pool {
	if h := int(w.H); n > h {
		n = h
	}
	if n < 1 {
		n = 1
	}
	p := &pool{work: make([]chan phase, n)}
	ants, rows := len(w.Ants), int(w.H)
	for k := 0; k < n; k++ {
		ch := make(chan phase)
		p.work[k] = ch
		antLo, antHi := span(k, n, ants)
		rowLo, rowHi := span(k, n, rows)
		go func() {
			for ph := range ch {
				switch ph {
				case phaseSense:
					w.senseRange(antLo, antHi)
				case phaseDecay:
					w.decayRows(int64(rowLo), int64(rowHi))
				}
				p.wg.Done()
			}
		}()
	}
	return p
}

// run hands ph to every worker and blocks until all of them are done. The
// send and the Wait are the happens-before edges between phases.
func (p *pool) run(ph phase) {
	p.wg.Add(len(p.work))
	for _, ch := range p.work {
		ch <- ph
	}
	p.wg.Wait()
}

// stop ends every worker. Called once, when Run returns.
func (p *pool) stop() {
	for _, ch := range p.work {
		close(ch)
	}
}

// span returns worker k's share [lo, hi) of total items split n ways. Pure
// function of (k, n, total): the partition is fixed before the first tick.
func span(k, n, total int) (lo, hi int) {
	return total * k / n, total * (k + 1) / n
}
