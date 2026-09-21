package golden_test

import (
	"os/exec"
	"strings"
	"testing"
)

// forbidden lists packages that must never reach the measured binary.
//
// net/http alone registers handlers, spins up background goroutines and drags
// in crypto/tls and reflect at init time. If any of that ended up inside
// cmd/antsim, every hyperfine number in the audit report would silently
// include HTTP stack initialisation — a measurement error the report could
// not detect by inspection. So we check it mechanically instead.
var forbidden = []string{
	"net/http",
	"antcolony/internal/uiserver",
	"html/template",
}

func TestHeadlessBinaryStaysClean(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "antcolony/cmd/antsim").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	deps := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		deps[strings.TrimSpace(line)] = true
	}
	for _, f := range forbidden {
		if deps[f] {
			t.Errorf("cmd/antsim imports %q — the measurement target must stay free of UI dependencies", f)
		}
	}
}
