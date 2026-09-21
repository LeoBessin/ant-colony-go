package simcore

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"antcolony/internal/config"
)

// Engine is one implementation of the ant colony simulation.
//
// The interface is deliberately coarse: exactly ONE dynamic dispatch per run,
// never per tick and never per ant. Optimization stages are new Engine
// implementations registered under new names, which is what lets the harness
// benchmark v0 against v5 side by side inside a single binary. Build tags
// would make that impossible.
type Engine interface {
	Name() string
	Run(ctx context.Context, cfg config.Config, obs Observer) (Result, error)
}

var (
	mu       sync.RWMutex
	registry = make(map[string]func() Engine)
)

// Register adds an engine constructor. Engines call this from init().
// Duplicate names panic: a silent overwrite would make benchmark results lie.
func Register(name string, ctor func() Engine) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic("simcore: duplicate engine registration: " + name)
	}
	registry[name] = ctor
}

// New constructs the named engine.
func New(name string) (Engine, error) {
	mu.RLock()
	ctor, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("simcore: unknown engine %q (have %v)", name, Names())
	}
	return ctor(), nil
}

// Names returns every registered engine name, sorted.
//
// Sorted, not map order: benchmark suites iterate this, and Go randomizes map
// iteration. Unsorted here would mean runs execute in a different order every
// time, which changes cache state and therefore the numbers.
func Names() []string {
	mu.RLock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	mu.RUnlock()
	sort.Strings(out)
	return out
}
