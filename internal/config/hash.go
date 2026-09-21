package config

// CanonicalHash folds every field that can influence the simulation into a
// single FNV-1a 64 value.
//
// It is written out by hand rather than hashing marshalled JSON so that the
// hash depends on the *meaning* of the config, not on its formatting: adding
// whitespace, reordering JSON keys, or renaming the scenario must not change
// it. Name and Version are deliberately excluded — they are labels, not inputs.
func (c Config) CanonicalHash() uint64 {
	h := uint64(14695981039346656037)
	u64 := func(v uint64) {
		for i := 0; i < 8; i++ {
			h ^= uint64(byte(v))
			h *= 1099511628211
			v >>= 8
		}
	}
	i := func(v int) { u64(uint64(int64(v))) }
	u32 := func(v uint32) { u64(uint64(v)) }

	u64(c.Seed)
	i(c.Width)
	i(c.Height)
	i(c.Ticks)
	i(c.AntCount)
	i(c.Nest.X)
	i(c.Nest.Y)

	i(len(c.Walls))
	for _, r := range c.Walls {
		i(r.X)
		i(r.Y)
		i(r.W)
		i(r.H)
	}
	i(len(c.Food))
	for _, f := range c.Food {
		i(f.At.X)
		i(f.At.Y)
		i(f.Amount)
	}

	p := c.Pheromone
	u32(p.DepositFood)
	u32(p.DepositHome)
	u32(p.EvapNum)
	u32(p.EvapDen)
	u32(p.DiffNum)
	u32(p.DiffDen)
	u32(p.Max)

	m := c.Movement
	u32(m.RandomNum)
	u32(m.RandomDen)

	return h
}
