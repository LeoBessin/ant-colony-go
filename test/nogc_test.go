package golden_test

import "testing"

// TestNogcAllocationsDoNotGrowWithTicks pins v3's claim: once Ant.Trail is
// gone, the tick loop allocates nothing per tick. Result.Allocs is a
// process-wide MemStats delta, so the runtime's own background allocations
// land in it too; asserting an exact 0 would fail at random. The claim that
// matters is the SLOPE: ten times more ticks must not mean more allocations.
// Before v3 the same run allocated ~5000 objects per tick on tiny.
func TestNogcAllocationsDoNotGrowWithTicks(t *testing.T) {
	cfg := loadScenario(t, "tiny")

	cfg.Ticks = 200
	short := runEngine(t, "nogc", cfg)
	cfg.Ticks = 2000
	long := runEngine(t, "nogc", cfg)

	// Slack of 1 alloc per 10 extra ticks: loose enough for runtime noise,
	// thousands of times tighter than one Trail append per ant per tick.
	if slack := uint64(cfg.Ticks-200) / 10; long.Allocs > short.Allocs+slack {
		t.Errorf("allocations grow with ticks: %d ticks -> %d allocs, %d ticks -> %d allocs",
			200, short.Allocs, cfg.Ticks, long.Allocs)
	}
	t.Logf("allocs: %d ticks -> %d, %d ticks -> %d", 200, short.Allocs, cfg.Ticks, long.Allocs)
}
