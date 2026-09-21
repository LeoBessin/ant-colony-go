package config

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

// Scenarios are embedded so the web UI works from any working directory.
// The headless binary still loads them from disk by path, because the audit
// report needs the exact file a measurement used to be visible in the command
// line that produced it.
//
//go:embed scenarios/*.json
var scenarioFS embed.FS

// ScenarioNames lists the built-in scenarios, sorted.
func ScenarioNames() []string {
	entries, err := scenarioFS.ReadDir("scenarios")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out
}

// ScenarioBytes returns a built-in scenario's raw JSON.
func ScenarioBytes(name string) ([]byte, error) {
	if strings.ContainsAny(name, `/\.`) {
		return nil, fmt.Errorf("config: invalid scenario name %q", name)
	}
	b, err := scenarioFS.ReadFile("scenarios/" + name + ".json")
	if err != nil {
		return nil, fmt.Errorf("config: unknown scenario %q", name)
	}
	return b, nil
}

// LoadScenario parses a built-in scenario by name.
func LoadScenario(name string) (Config, error) {
	b, err := ScenarioBytes(name)
	if err != nil {
		return Config{}, err
	}
	return Parse(b)
}
