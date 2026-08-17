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

	// Both READMEs. The npm one is a separate, shorter file that nobody
	// re-reads when the main one changes — it was still leading with the
	// previous tagline months later, on a page that gets five hundred installs
	// a week. Third-party write-ups were quoting a count from before July.
	// Seven files spell the count and only two were checked, so when Zed made it
	// eighteen the other five stayed on seventeen — including the two manifest
	// descriptions, which is the line the MCP registry shows next to the name,
	// and the harnesses page, which says it in the lede and in three meta tags.
	for _, name := range []string{
		"README.md",
		"npm/README.md",
		"llms-install.md",
		"docs/ARCHITECTURE.md",
		"server.json",
		"packaging/mcpb/manifest.json",
		"docs/guide/harnesses.html",
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := strings.ToLower(string(b))

		// The word, not a phrase around it. These four documents each have their
		// own voice — "eighteen coding agents", "the eighteen harnesses deja
		// installs into", "the eighteen the loader registers" — and pinning one
		// phrasing meant the other two were never checked at all.
		for _, stale := range words {
			if stale != want && strings.Contains(text, stale) {
				t.Errorf("%s still counts %q; the registry has %d (%s)", name, stale, n, want)
			}
		}
		if !strings.Contains(text, want) {
			t.Errorf("%s never says %q; the registry has %d harnesses", name, want, n)
		}
	}
}

// The npm page is the one a lot of people meet the project on, and it had
// drifted a whole rewrite behind: still the old tagline, no numbers, no list of
// what it reads. This keeps the two pitches saying the same thing.
func TestNpmReadmeLeadsWithWhatTheMainOneLeadsWith(t *testing.T) {
	root := filepath.Join("..", "..")
	main, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	npm, err := os.ReadFile(filepath.Join(root, "npm", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"Your agent is about to re-debug something you fixed in March.",
		"deja starts full",
	} {
		if !strings.Contains(string(main), line) {
			t.Fatalf("the main README no longer says %q — update this test with it", line)
		}
		if !strings.Contains(string(npm), line) {
			t.Errorf("npm/README.md does not carry %q", line)
		}
	}
}
