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

// A golden file is keyed by an engine's VARIANT (see simcore.VariantOf), not
// by a list maintained here. Optimization stages declare nothing and therefore
// land in the default family, where they are held to `naive` bit for bit --
// which is the premise of the whole audit report. An engine that deliberately
// changes what the simulation computes declares a variant in its own package
// and is held to its own reference file instead.

// familyReference names the engine whose output DEFINES each family's golden.
// This one IS deliberate and hand-written: "which engine is the truth for this
// simulation" is a decision, not something to infer. TestGoldenFamilies checks
// it stays consistent with what the engines declare.
var familyReference = map[string]string{
	"":         "naive",
	"gradient": "gradient",
}

func goldenPath(family, name string) string {
	if family == "" {
		return filepath.Join("testdata", "golden", name+".json")
	}
	return filepath.Join("testdata", "golden", family, name+".json")
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
				for family, ref := range familyReference {
					got := canonical(runEngine(t, ref, cfg))
					b, err := json.MarshalIndent(got, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					path := goldenPath(family, sc.name)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
						t.Fatal(err)
					}
					t.Logf("updated %s (from engine %q)", path, ref)
				}
				return
			}

			want := map[string]simcore.Result{}
			load := func(family string) simcore.Result {
				if r, ok := want[family]; ok {
					return r
				}
				path := goldenPath(family, sc.name)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("missing golden file %s (run: UPDATE_GOLDEN=1 go test ./test/...): %v", path, err)
				}
				var r simcore.Result
				if err := json.Unmarshal(raw, &r); err != nil {
					t.Fatal(err)
				}
				want[family] = r
				return r
			}

			// Every registered engine, present and future, must match the
			// golden of ITS family -- and the default family is `naive`, so a
			// new optimization stage is gated automatically.
			for _, name := range simcore.Names() {
				eng, err := simcore.New(name)
				if err != nil {
					t.Fatalf("engine %s: %v", name, err)
				}
				family := simcore.VariantOf(eng)
				got := canonical(runEngine(t, name, cfg))
				if !got.DeterministicEqual(load(family)) {
					t.Errorf("engine %q diverged from golden %s (family %q):\n got %+v\nwant %+v",
						name, sc.name, family, got, load(family))
				}
			}

			// A family whose reference engine produces the same checksum as
			// the baseline is not a separate simulation at all -- it is an
			// optimization stage that has been let off the invariant by
			// mistake. Catch that, because it is a silent hole in the gate.
			for family, ref := range familyReference {
				if family == "" {
					continue
				}
				if load(family).DeterministicEqual(load("")) {
					t.Errorf("engine %q has its own golden family but reproduces the baseline exactly; "+
						"remove it from engineFamily so it is gated against naive", ref)
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

// TestGoldenFamilies keeps familyReference honest.
//
// Two ways this can rot: an engine declares a variant that has no reference
// file to be checked against, or a reference engine is listed under a family
// it does not actually belong to. Either one silently drops an engine out of
// the correctness gate, which is the one thing the gate must not do.
func TestGoldenFamilies(t *testing.T) {
	for family, ref := range familyReference {
		eng, err := simcore.New(ref)
		if err != nil {
			t.Errorf("familyReference[%q] = %q, which is not a registered engine: %v", family, ref, err)
			continue
		}
		if got := simcore.VariantOf(eng); got != family {
			t.Errorf("familyReference says %q defines family %q, but that engine declares variant %q",
				ref, family, got)
		}
	}
	for _, name := range simcore.Names() {
		eng, err := simcore.New(name)
		if err != nil {
			t.Fatal(err)
		}
		v := simcore.VariantOf(eng)
		if _, ok := familyReference[v]; !ok {
			t.Errorf("engine %q declares variant %q, but no golden reference engine is defined for it; "+
				"add it to familyReference and run make golden", name, v)
		}
	}
}
