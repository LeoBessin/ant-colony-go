package simcore

// Result is the deterministic output of one simulation run.
//
// Every field EXCEPT Engine, WallNS, Allocs and HeapBytes takes part in
// StateChecksum. Timing and allocation counters are observational: folding
// them into the checksum would make the golden test flaky by construction.
type Result struct {
	// --- identity (not part of the checksum) ---
	Engine string `json:"engine"`

	// --- deterministic state (all part of the checksum) ---
	ConfigHash     uint64 `json:"config_hash"`
	Seed           uint64 `json:"seed"`
	Ticks          int    `json:"ticks"`
	TicksRun       int    `json:"ticks_run"`
	FoodCollected  int64  `json:"food_collected"`
	FoodRemaining  int64  `json:"food_remaining"`
	AntsCarrying   int32  `json:"ants_carrying"`
	TotalPheromone uint64 `json:"total_pheromone"`
	AntPosHash     uint64 `json:"ant_pos_hash"`
	GridHash       uint64 `json:"grid_hash"`
	StateChecksum  uint64 `json:"state_checksum"`

	// --- observational (never part of the checksum) ---
	WallNS    int64  `json:"wall_ns"`
	Allocs    uint64 `json:"allocs"`
	HeapBytes uint64 `json:"heap_bytes"`
}

// ComputeChecksum folds every deterministic field into StateChecksum and
// returns the updated Result. Engines call this once, at the end of Run.
func (r Result) ComputeChecksum() Result {
	h := NewHash()
	h = HashU64(h, r.ConfigHash)
	h = HashU64(h, r.Seed)
	h = HashI64(h, int64(r.Ticks))
	h = HashI64(h, int64(r.TicksRun))
	h = HashI64(h, r.FoodCollected)
	h = HashI64(h, r.FoodRemaining)
	h = HashI64(h, int64(r.AntsCarrying))
	h = HashU64(h, r.TotalPheromone)
	h = HashU64(h, r.AntPosHash)
	h = HashU64(h, r.GridHash)
	r.StateChecksum = h
	return r
}

// DeterministicEqual reports whether two results describe the same simulated
// state, ignoring which engine produced them and how long it took. This is the
// invariant every optimization must preserve.
func (r Result) DeterministicEqual(o Result) bool {
	return r.ConfigHash == o.ConfigHash &&
		r.Seed == o.Seed &&
		r.Ticks == o.Ticks &&
		r.TicksRun == o.TicksRun &&
		r.FoodCollected == o.FoodCollected &&
		r.FoodRemaining == o.FoodRemaining &&
		r.AntsCarrying == o.AntsCarrying &&
		r.TotalPheromone == o.TotalPheromone &&
		r.AntPosHash == o.AntPosHash &&
		r.GridHash == o.GridHash &&
		r.StateChecksum == o.StateChecksum
}
