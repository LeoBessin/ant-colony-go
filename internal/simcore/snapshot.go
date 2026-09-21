package simcore

// Snapshot is a UI-facing view of the world. It exists ONLY so the web UI can
// draw; nothing inside tick() may read or write it. Pheromone levels are
// pre-scaled to 0..255 here because the browser cannot use more precision than
// that, and shipping uint32 would quadruple the frame size for no visible gain.
//
// Sparse fields (Food, Walls) are flat coordinate triples/pairs rather than
// full grids, so an empty map costs a few bytes instead of W*H entries.
type Snapshot struct {
	Tick      int     `json:"tick"`
	W         int     `json:"w"`
	H         int     `json:"h"`
	AntX      []int32 `json:"ax"`
	AntY      []int32 `json:"ay"`
	AntFlags  []uint8 `json:"af"`              // bit0 = carrying food
	PheroFood []uint8 `json:"pf"`              // row-major, scaled 0..255
	PheroHome []uint8 `json:"ph"`              // row-major, scaled 0..255
	Food      []int32 `json:"food"`            // flat x,y,n triples
	Walls     []int32 `json:"walls,omitempty"` // flat x,y pairs, sent on frame 0 only
	Nest      [2]int  `json:"nest"`
	Collected int64   `json:"collected"`
	Done      bool    `json:"done"`
}

// Observer receives snapshots while a run is in flight.
//
// Run() consults Interval() exactly once, before the tick loop starts, so an
// unobserved run pays nothing at all: tick() never mentions the observer, and
// snapshot() — the only code that copies world state — is never reached.
type Observer interface {
	// Interval returns the tick period between snapshots. 0 disables observation.
	Interval() int
	// OnTick delivers a snapshot. Implementations MUST NOT block: a slow
	// consumer that stalls here would skew the very timings we are measuring.
	OnTick(tick int, s *Snapshot)
}

// NopObserver observes nothing. Headless runs pass nil, but this is available
// for call sites that would rather not nil-check.
type NopObserver struct{}

func (NopObserver) Interval() int         { return 0 }
func (NopObserver) OnTick(int, *Snapshot) {}

// ObserveEvery reports the effective snapshot interval for an observer,
// treating a nil observer as "never".
func ObserveEvery(obs Observer) int {
	if obs == nil {
		return 0
	}
	n := obs.Interval()
	if n < 0 {
		return 0
	}
	return n
}
