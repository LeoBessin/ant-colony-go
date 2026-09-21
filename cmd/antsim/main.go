// Command antsim runs one simulation headlessly and prints its deterministic
// result as JSON.
//
// This binary is the measurement target: hyperfine times it, pprof profiles
// it, and the golden test diffs its output. It therefore MUST NOT import
// net/http or anything the web UI drags in — a test in test/ enforces that
// mechanically, because a stray import would put HTTP initialisation inside
// every number in the audit report.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"

	"antcolony/internal/config"
	_ "antcolony/internal/engine"
	"antcolony/internal/simcore"
)

func main() {
	var (
		cfgPath    = flag.String("config", "internal/config/scenarios/small.json", "scenario JSON file")
		engineName = flag.String("engine", "naive", "engine name")
		outPath    = flag.String("out", "", "write result JSON here (default: stdout)")
		seed       = flag.Int64("seed", -1, "override the scenario seed (-1 keeps it)")
		ticks      = flag.Int("ticks", -1, "override the tick count (-1 keeps it)")
		repeat     = flag.Int("repeat", 1, "run N times; all runs must produce the same checksum")
		maxProcs   = flag.Int("gomaxprocs", 0, "override GOMAXPROCS (0 keeps the default)")
		cpuProf    = flag.String("cpuprofile", "", "write a CPU profile here")
		memProf    = flag.String("memprofile", "", "write a heap profile here")
		list       = flag.Bool("list", false, "list registered engines and exit")
		quiet      = flag.Bool("quiet", false, "suppress the human-readable summary on stderr")
	)
	flag.Parse()

	if *list {
		for _, n := range simcore.Names() {
			fmt.Println(n)
		}
		return
	}

	if err := run(*cfgPath, *engineName, *outPath, *seed, *ticks, *repeat, *maxProcs, *cpuProf, *memProf, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, "antsim:", err)
		os.Exit(1)
	}
}

func run(cfgPath, engineName, outPath string, seed int64, ticks, repeat, maxProcs int, cpuProf, memProf string, quiet bool) error {
	if maxProcs > 0 {
		runtime.GOMAXPROCS(maxProcs)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if seed >= 0 {
		cfg.Seed = uint64(seed)
	}
	if ticks >= 0 {
		cfg.Ticks = ticks
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	eng, err := simcore.New(engineName)
	if err != nil {
		return err
	}

	if cpuProf != "" {
		f, err := os.Create(cpuProf)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			return err
		}
		defer pprof.StopCPUProfile()
	}

	if repeat < 1 {
		repeat = 1
	}

	var first simcore.Result
	for i := 0; i < repeat; i++ {
		// nil observer: no snapshots, no copying, no UI cost in the timing.
		res, err := eng.Run(context.Background(), cfg, nil)
		if err != nil {
			return err
		}
		if i == 0 {
			first = res
			continue
		}
		// Self-check: the same binary, same config and same seed must produce
		// the same state every time. If this ever fires, something
		// non-deterministic crept in and no benchmark number is trustworthy.
		if !res.DeterministicEqual(first) {
			return fmt.Errorf("non-deterministic: run %d checksum %#x != run 0 checksum %#x",
				i, res.StateChecksum, first.StateChecksum)
		}
		if res.WallNS < first.WallNS {
			first.WallNS = res.WallNS // report the fastest run of the repeat set
		}
	}

	if memProf != "" {
		f, err := os.Create(memProf)
		if err != nil {
			return err
		}
		defer f.Close()
		runtime.GC() // materialise up-to-date heap statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			return err
		}
	}

	b, err := json.MarshalIndent(first, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	if outPath != "" {
		if err := os.WriteFile(outPath, b, 0o644); err != nil {
			return err
		}
	} else {
		os.Stdout.Write(b)
	}

	if !quiet {
		fmt.Fprintf(os.Stderr,
			"engine=%s scenario=%s %dx%d ants=%d ticks=%d -> collected=%d remaining=%d checksum=%#016x wall=%.3fms allocs=%d\n",
			first.Engine, cfg.Name, cfg.Width, cfg.Height, cfg.AntCount, first.TicksRun,
			first.FoodCollected, first.FoodRemaining, first.StateChecksum,
			float64(first.WallNS)/1e6, first.Allocs)
	}
	return nil
}
