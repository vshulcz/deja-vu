package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
		19: "nineteen", 20: "twenty", 21: "twenty-one",
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
		// The ClawHub skill pack, which the OpenClaw registry renders as the
		// skill's own page.
		"extensions/openclaw/skill/deja-history/SKILL.md",
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
		// The wanted word first: "twenty-one" contains "twenty", so scanning the
		// raw text for stale words reported every correct document as stale the
		// day the count crossed twenty.
		rest := strings.ReplaceAll(text, want, "")
		for _, stale := range words {
			if stale != want && strings.Contains(rest, stale) {
				t.Errorf("%s still counts %q; the registry has %d (%s)", name, stale, n, want)
			}
		}
		if !strings.Contains(text, want) {
			t.Errorf("%s never says %q; the registry has %d harnesses", name, want, n)
		}
	}
}

// The word form is pinned above; this is the other way the count is written.
// The landing page and the comparison page say it in digits — "17 harnesses,
// one index" sat under a matrix listing twenty of them, and the comparison
// against MemPalace argued from a number three releases old. Nobody re-reads a
// sentence when a harness lands, so the sentence has to fail here.
func TestPagesCountHarnessesInDigitsCorrectly(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	claim := regexp.MustCompile(`(\d+)\s*(?:&nbsp;)?\s*(?:coding\s+)?(?:harnesses|harness|agents)\b`)
	var files []string
	for _, dir := range []string{".", "docs", "docs/guide"} {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if ext := filepath.Ext(e.Name()); ext != ".html" && ext != ".md" {
				continue
			}
			// The changelog is a record of what was true at each release and
			// must keep saying it.
			if e.Name() == "CHANGELOG.md" {
				continue
			}
			files = append(files, filepath.ToSlash(filepath.Join(dir, e.Name())))
		}
	}
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, m := range claim.FindAllStringSubmatch(string(b), -1) {
			got, err := strconv.Atoi(m[1])
			if err != nil || got < 10 || got > 40 {
				// Not a claim about how many harnesses there are: "3 agents"
				// in a sentence about someone else's tool, a version, a count
				// of something entirely different.
				continue
			}
			if got != n {
				t.Errorf("%s says %q; the registry has %d harnesses", name, strings.TrimSpace(m[0]), n)
			}
		}
	}
}

// registryHarnessCount is how many harnesses deja reads, from the one file that
// decides it. deja itself is in the registry as the reader, not as something it
// reads.
func registryHarnessCount(t *testing.T, root string) int {
	t.Helper()
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
		if h.ID != "deja" {
			n++
		}
	}
	return n
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

// The two npm pages count what a plugin brings its host: every harness except
// the host itself. Between them they are installed a couple of thousand times a
// week — more people than the site sees — and both were describing nineteen
// agents months after there were twenty, because the count was written relative
// to whatever the sentence had just listed ("and ten more") and nothing could
// check that against anything.
func TestPluginPagesCountTheOtherHarnesses(t *testing.T) {
	root := filepath.Join("..", "..")
	want, words := harnessCountWords(t, root, -1)

	for _, name := range []string{
		"extensions/dsh/package.json",
		"extensions/dsh/README.md",
		"extensions/opencode/package.json",
		"extensions/opencode/README.md",
	} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := strings.ToLower(string(b))
		rest := strings.ReplaceAll(text, want, "")
		for _, stale := range words {
			if stale != want && strings.Contains(rest, stale) {
				t.Errorf("%s still counts %q other agents; there are %s", name, stale, want)
			}
		}
		if !strings.Contains(text, want) {
			t.Errorf("%s never says %q, which is how many other agents deja reads", name, want)
		}
	}
}

// harnessCountWords is the registry count plus an offset, as a word, together
// with the words this test knows — a page that counts the harnesses says the
// first, and a page that counts the other ones says it with offset -1.
func harnessCountWords(t *testing.T, root string, offset int) (string, map[int]string) {
	t.Helper()
	n := registryHarnessCount(t, root) + offset
	words := map[int]string{
		15: "fifteen", 16: "sixteen", 17: "seventeen", 18: "eighteen",
		19: "nineteen", 20: "twenty", 21: "twenty-one",
	}
	want, ok := words[n]
	if !ok {
		t.Fatalf("the count is %d and this test has no word for it; add one", n)
	}
	return want, words
}

// The per-harness pages say it a third way — "twenty of them today" — and ten
// of them were a harness behind, because the phrase is not a title, not a
// digit, and not the bare word the README test looks for.
func TestGuidePagesCountTheHarnessesTheyList(t *testing.T) {
	root := filepath.Join("..", "..")
	want, words := harnessCountWords(t, root, 0)
	phrase := regexp.MustCompile(`([a-z]+(?:-[a-z]+)?) of them today`)
	pages, err := filepath.Glob(filepath.Join(root, "docs", "guide", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range phrase.FindAllStringSubmatch(string(b), -1) {
			if m[1] == want {
				continue
			}
			for _, w := range words {
				if m[1] == w {
					t.Errorf("%s says %q of them; the registry has %s", filepath.Base(p), m[1], want)
				}
			}
		}
	}
}
