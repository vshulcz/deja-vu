package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The README says the harness count in words, twice. A number spelled out in
// prose is the kind that goes stale quietly: adding a harness touches the
// registry and the generated table, and neither of those is the sentence. This
// pins the word to the registry so the release that adds the eighteenth fails
// here instead of shipping a README that undercounts the product.
func TestReadmeSpellsTheHarnessCountTheRegistryHas(t *testing.T) {
	root := filepath.Join("..", "..")

	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	var reg struct {
		Harnesses []struct {
			ID string `json:"id"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatalf("registry: %v", err)
	}
	n := 0
	for _, h := range reg.Harnesses {
		// deja is in the registry as the reader, not as a harness it reads.
		if h.ID != "deja" {
			n++
		}
	}

	words := map[int]string{
		15: "fifteen", 16: "sixteen", 17: "seventeen", 18: "eighteen",
		19: "nineteen", 20: "twenty",
	}
	want, ok := words[n]
	if !ok {
		t.Fatalf("registry has %d harnesses and this test has no word for it; add one", n)
	}

	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("README: %v", err)
	}
	text := string(readme)

	for _, stale := range words {
		if stale == want {
			continue
		}
		if strings.Contains(strings.ToLower(text), stale+" coding agents") ||
			strings.Contains(strings.ToLower(text), stale+" harnesses") {
			t.Errorf("README counts %q harnesses; the registry has %d (%s)", stale, n, want)
		}
	}
	if !strings.Contains(strings.ToLower(text), want+" coding agents") {
		t.Errorf("README does not say %q coding agents; the registry has %d", want, n)
	}
}
