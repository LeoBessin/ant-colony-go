// Package config defines the simulation's input data.
//
// Everything that can influence the outcome of a run lives here and nowhere
// else. There is no clock, no environment variable and no command-line flag
// that reaches the simulation: Config plus Seed fully determine the output.
package config

// Point is a grid cell coordinate.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Rect is an axis-aligned wall block, [X, X+W) x [Y, Y+H).
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// FoodPile places Amount units of food on a single cell.
type FoodPile struct {
	At     Point `json:"at"`
	Amount int   `json:"amount"`
}

// PheromoneParams are expressed as integer numerator/denominator pairs on
// purpose. Floating point is banned inside the tick: float addition is not
// associative, so any reordering (SoA layout, row striping, parallel decay)
// would change the result and destroy the cross-engine golden invariant.
// Levels are fixed-point "milli-units" in a uint32.
type PheromoneParams struct {
	DepositFood uint32 `json:"deposit_food"` // laid by ants carrying food
	DepositHome uint32 `json:"deposit_home"` // laid by ants searching for food
	EvapNum     uint32 `json:"evap_num"`     // level *= EvapNum/EvapDen each tick
	EvapDen     uint32 `json:"evap_den"`
	DiffNum     uint32 `json:"diff_num"` // fraction spread to the 4 neighbours
	DiffDen     uint32 `json:"diff_den"`
	Max         uint32 `json:"max"` // saturation ceiling
}

// MovementParams control ant steering.
type MovementParams struct {
	RandomNum uint32 `json:"random_num"` // P(ignore pheromone and wander) = Num/Den
	RandomDen uint32 `json:"random_den"`
}

// Config is the complete input to a run.
type Config struct {
	Version   int             `json:"version"`
	Name      string          `json:"name"`
	Seed      uint64          `json:"seed"`
	Width     int             `json:"width"`
	Height    int             `json:"height"`
	Ticks     int             `json:"ticks"`
	AntCount  int             `json:"ant_count"`
	Nest      Point           `json:"nest"`
	Walls     []Rect          `json:"walls"` // ordered; never iterated from a map
	Food      []FoodPile      `json:"food"`  // ordered
	Pheromone PheromoneParams `json:"pheromone"`
	Movement  MovementParams  `json:"movement"`
}

// Cells is the number of grid cells.
func (c Config) Cells() int { return c.Width * c.Height }
