package uiserver

import (
	"fmt"
	"time"

	"antcolony/internal/config"
)

// Resource caps for a harness exposed to the network.
//
// config.Validate only rejects configurations that make no sense; it has no
// upper bounds, because cmd/antsim must stay free to run anything on the
// test bench. A public server is a different matter: without these caps one
// request asking for a 100000x100000 grid gets the container OOM-killed, and
// one asking for a billion ticks pins a core forever.
//
// Every cap sits comfortably above the largest embedded scenario (large:
// 256x256, 2000 ants, 300 ticks), so the UI's own scenarios are never refused.
const (
	// maxCells bounds memory: the engines keep two grids, and every buffered
	// snapshot carries two bytes per cell (256 frames of 256x256 ≈ 34 MB).
	maxCells = 256 * 256
	maxAnts  = 5000
	maxTicks = 5000
	// maxItems bounds walls + food, which the naive engine scans per tick.
	maxItems = 4096

	maxRepeat    = 5
	maxBodyBytes = 1 << 20

	// Wall-clock budgets. The caps above bound one tick; these bound a whole
	// request, so a slow engine on a large grid cannot hold the CPU for long.
	runTimeout   = 5 * time.Minute
	benchTimeout = 60 * time.Second
)

// checkLimits rejects a configuration exceeding the server's caps.
func checkLimits(cfg config.Config) error {
	switch {
	// Each side is checked first: Width*Height can overflow and wrap to a
	// small number for sides large enough.
	case cfg.Width > maxCells || cfg.Height > maxCells || cfg.Cells() > maxCells:
		return fmt.Errorf("grid %dx%d exceeds the server limit of %d cells", cfg.Width, cfg.Height, maxCells)
	case cfg.AntCount > maxAnts:
		return fmt.Errorf("ant_count %d exceeds the server limit of %d", cfg.AntCount, maxAnts)
	case cfg.Ticks > maxTicks:
		return fmt.Errorf("ticks %d exceeds the server limit of %d", cfg.Ticks, maxTicks)
	case len(cfg.Walls)+len(cfg.Food) > maxItems:
		return fmt.Errorf("%d walls + food piles exceeds the server limit of %d", len(cfg.Walls)+len(cfg.Food), maxItems)
	}
	return nil
}
