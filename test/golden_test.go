// Package golden_test holds the correctness gate for the whole project.
//
// The audit report's premise is that every optimization made the simulation
// faster WITHOUT changing what it computes. These tests are the evidence for
// the second half of that claim. run_benchmarks.sh refuses to publish a single
// number until they pass.
package golden_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"antcolony/internal/config"
	_ "antcolony/internal/engine"
	"antcolony/internal/simcore"
)

// scenarios exercised by the golden test. medium and large are excluded: they
// prove nothing extra about determinism and the v0 engine takes minutes on
// them. Benchmarks use those.
var scenarios = []struct {
	name  string
	short bool // include when -short is set
}{
	{"tiny", true},
	{"small", false},
}

func scenarioPath(name string) string {
	return filepath.Join("..", "internal", "config", "scenarios", name+".json")
}

func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name+".json")
}

// canonical strips everything that is allowed to vary between runs. Wall time
// and allocation counts are observations about the machine, not about the
// simulation; folding them into a golden file would make it fail at random.
func canonical(r simcore.Result) simcore.Result {
	r.Engine = ""
	r.WallNS = 0
	r.Allocs = 0
	r.HeapBytes = 0
	return r
}

func runEngine(t *testing.T, name string, cfg config.Config) simcore.Result {
	t.Helper()
	eng, err := simcore.New(name)
	if err != nil {
		t.Fatalf("engine %s: %v", name, err)
	}
	res, err := eng.Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("engine %s run: %v", name, err)
	}
	return res
}

func loadScenario(t *testing.T, name string) config.Config {
	t.Helper()
	cfg, err := config.Load(scenarioPath(name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return cfg
}

// TestGolden pins every engine's output to a checked-in file.
//
// Regenerate deliberately with UPDATE_GOLDEN=1 go test ./test/... — and only
// when the simulation's DEFINITION changed, never to make a failing
// optimization go green.
func TestGolden(t *testing.T) {
	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			if testing.Short() && !sc.short {
				t.Skip("skipping in -short mode")
			}
			cfg := loadScenario(t, sc.name)

			if update {
				got := canonical(runEngine(t, "naive", cfg))
				b, err := json.MarshalIndent(got, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(goldenPath(sc.name)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath(sc.name), append(b, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated %s", goldenPath(sc.name))
				return
			}

			raw, err := os.ReadFile(goldenPath(sc.name))
			if err != nil {
				t.Fatalf("missing golden file (run: UPDATE_GOLDEN=1 go test ./test/...): %v", err)
			}
			var want simcore.Result
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}

			// Every registered engine, present and future, must match.
			for _, name := range simcore.Names() {
				got := canonical(runEngine(t, name, cfg))
				if !got.DeterministicEqual(want) {
					t.Errorf("engine %q diverged from golden %s:\n got %+v\nwant %+v",
						name, sc.name, got, want)
				}
			}
		})
	}
}

// TestRepeatable checks that the same engine, run twice in the same process,
// produces the same state. A failure here means something reads the clock, the
// environment, or a map's iteration order.
func TestRepeatable(t *testing.T) {
	cfg := loadScenario(t, "tiny")
	for _, name := range simcore.Names() {
		a := runEngine(t, name, cfg)
		b := runEngine(t, name, cfg)
		if !a.DeterministicEqual(b) {
			t.Errorf("engine %q is not repeatable: %#x != %#x", name, a.StateChecksum, b.StateChecksum)
		}
	}
}

// TestSeedMatters guards against the opposite failure: a "deterministic"
// simulation that ignores its seed entirely would pass every test above.
func TestSeedMatters(t *testing.T) {
	cfg := loadScenario(t, "tiny")
	base := runEngine(t, "naive", cfg)

	cfg.Seed++
	other := runEngine(t, "naive", cfg)

	if base.StateChecksum == other.StateChecksum {
		t.Fatalf("changing the seed did not change the outcome (%#x) — the seed is not wired in",
			base.StateChecksum)
	}
}

// TestConfigHashIsSemantic checks that the config hash tracks meaning, not
// formatting: results are keyed by it in the benchmark tables.
func TestConfigHashIsSemantic(t *testing.T) {
	cfg := loadScenario(t, "tiny")
	h := cfg.CanonicalHash()

	renamed := cfg
	renamed.Name = "something else entirely"
	if renamed.CanonicalHash() != h {
		t.Error("renaming a scenario changed its config hash; the name is a label, not an input")
	}

	moved := cfg
	moved.Nest.X++
	if moved.CanonicalHash() == h {
		t.Error("moving the nest did not change the config hash")
	}
}

// TestObserverDoesNotChangeOutcome proves the UI is genuinely a bystander:
// attaching an observer must not perturb a single bit of the simulation.
func TestObserverDoesNotChangeOutcome(t *testing.T) {
	cfg := loadScenario(t, "tiny")
	eng, err := simcore.New("naive")
	if err != nil {
		t.Fatal(err)
	}

	headless, err := eng.Run(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	obs := &countingObserver{every: 3}
	watched, err := eng.Run(context.Background(), cfg, obs)
	if err != nil {
		t.Fatal(err)
	}

	if !headless.DeterministicEqual(watched) {
		t.Error("attaching an observer changed the simulation outcome")
	}
	if obs.calls == 0 {
		t.Error("observer was never called; this test proved nothing")
	}
}

type countingObserver struct {
	every int
	calls int
}

func (o *countingObserver) Interval() int { return o.every }
func (o *countingObserver) OnTick(_ int, s *simcore.Snapshot) {
	o.calls++
	if s == nil {
		panic("nil snapshot")
	}
}
