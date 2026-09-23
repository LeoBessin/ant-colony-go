package golden_test

import (
	"fmt"
	"runtime"
	"testing"
)

// TestParallelIndependentOfWorkerCount pins the claim the v4 package comment
// makes: the partition changes with GOMAXPROCS, the result must not. TestGolden
// only exercises the default GOMAXPROCS; odd counts (3, 7) put band edges on
// different rows and give workers unequal shares, which is where an
// off-by-one in span() or a cross-band write would show up.
//
// The reference is flatgrid, which TestGolden already holds to naive bit for
// bit; medium is included because it is cheap for flatgrid and has more rows
// per band than small.
func TestParallelIndependentOfWorkerCount(t *testing.T) {
	sc := []string{"tiny", "small", "medium"}
	if testing.Short() {
		sc = sc[:1]
	}
	prev := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prev)

	for _, name := range sc {
		cfg := loadScenario(t, name)
		want := runEngine(t, "flatgrid", cfg)
		for _, procs := range []int{1, 2, 3, 7, 16} {
			t.Run(fmt.Sprintf("%s/procs=%d", name, procs), func(t *testing.T) {
				runtime.GOMAXPROCS(procs)
				got := runEngine(t, "parallel", cfg)
				if !got.DeterministicEqual(want) {
					t.Errorf("parallel with %d workers diverged from flatgrid: %#x != %#x",
						procs, got.StateChecksum, want.StateChecksum)
				}
			})
		}
	}
}
