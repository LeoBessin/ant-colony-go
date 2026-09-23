// Package engine blank-imports every simulation engine so that a single
// binary can construct any of them by name.
//
// This is why optimization stages are separate PACKAGES and not build tags:
// the whole point of the harness is running v0 and v5 side by side in one
// process and comparing them. Build tags would give one engine per binary and
// make that comparison impossible.
package engine

import (
	_ "antcolony/internal/engine/flatgrid"
	_ "antcolony/internal/engine/gradient"
	_ "antcolony/internal/engine/naive"
)
