package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Load reads and validates a scenario file.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return Parse(b)
}

// Parse decodes and validates a scenario from JSON bytes.
func Parse(b []byte) (Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields() // a typo in a scenario must fail loudly, not silently change the workload
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate rejects any config that could produce a meaningless or
// non-reproducible run.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if c.Version != 1 {
		add("config: version must be 1, got %d", c.Version)
	}
	if c.Width <= 0 || c.Height <= 0 {
		add("config: width and height must be > 0, got %dx%d", c.Width, c.Height)
	}
	if c.Ticks < 0 {
		add("config: ticks must be >= 0, got %d", c.Ticks)
	}
	if c.AntCount <= 0 {
		add("config: ant_count must be > 0, got %d", c.AntCount)
	}
	if c.Width > 0 && c.Height > 0 && !c.inBounds(c.Nest) {
		add("config: nest %v is outside the %dx%d grid", c.Nest, c.Width, c.Height)
	}
	for i, r := range c.Walls {
		if r.W <= 0 || r.H <= 0 {
			add("config: walls[%d] has non-positive size %dx%d", i, r.W, r.H)
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > c.Width || r.Y+r.H > c.Height {
			add("config: walls[%d] %v escapes the grid", i, r)
		}
		if r.contains(c.Nest) {
			add("config: walls[%d] %v covers the nest", i, r)
		}
	}
	for i, f := range c.Food {
		if f.Amount <= 0 {
			add("config: food[%d] amount must be > 0, got %d", i, f.Amount)
		}
		if !c.inBounds(f.At) {
			add("config: food[%d] at %v is outside the grid", i, f.At)
		}
		for j, r := range c.Walls {
			if r.contains(f.At) {
				add("config: food[%d] at %v is inside walls[%d]", i, f.At, j)
			}
		}
	}

	p := c.Pheromone
	if p.EvapDen == 0 {
		add("config: pheromone.evap_den must be > 0")
	} else if p.EvapNum > p.EvapDen {
		add("config: pheromone.evap_num (%d) must be <= evap_den (%d)", p.EvapNum, p.EvapDen)
	}
	if p.DiffDen == 0 {
		add("config: pheromone.diff_den must be > 0")
	} else if p.DiffNum > p.DiffDen {
		add("config: pheromone.diff_num (%d) must be <= diff_den (%d)", p.DiffNum, p.DiffDen)
	}
	if p.Max == 0 {
		add("config: pheromone.max must be > 0")
	}

	m := c.Movement
	if m.RandomDen == 0 {
		add("config: movement.random_den must be > 0")
	} else if m.RandomNum > m.RandomDen {
		add("config: movement.random_num (%d) must be <= random_den (%d)", m.RandomNum, m.RandomDen)
	}

	return errors.Join(errs...)
}

func (c Config) inBounds(p Point) bool {
	return p.X >= 0 && p.Y >= 0 && p.X < c.Width && p.Y < c.Height
}

func (r Rect) contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}
