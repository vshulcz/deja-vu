package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A skill deja writes for a harness must not tell that harness deja cannot read
// it. Copilot's opened with "deja does not index Copilot history" — true when it
// was written, false since the parser landed, and the same binary was indexing
// Copilot sessions while saying so (#3222).
//
// Derived from the registry rather than a list beside it: a harness deja reads
// and writes guidance for fails here if its own text denies the reading.
func TestNoSkillDeniesTheHarnessItIsWrittenFor(t *testing.T) {
	hermeticEnv(t)
	for _, h := range registryHarnessesForGuidance(t) {
		id := h
		if v, ok := map[string]string{"claude": "claude-code", "copilot-chat": "vscode"}[h]; ok {
			id = v
		}
		if guidancePath(id) == "" {
			continue
		}
		text := guidanceText(id)
		low := strings.ToLower(text)
		for _, deny := range []string{
			"does not index",
			"deja cannot read",
			"is not indexed",
		} {
			if strings.Contains(low, deny) {
				t.Errorf("the %s skill says %q about a harness deja indexes:\n%s", h, deny, text)
			}
		}
	}
}

// registryHarnessesForGuidance is the harnesses the format registry lists,
// which is the same source the capability matrix is built from.
func registryHarnessesForGuidance(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg capRegistry
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, h := range reg.Harnesses {
		if h.ID != "deja" {
			out = append(out, h.ID)
		}
	}
	if len(out) == 0 {
		t.Fatal("no harnesses in the registry, so this test checks nothing")
	}
	return out
}
