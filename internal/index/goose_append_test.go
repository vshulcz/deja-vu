package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func gooseHeaderLine() string {
	b, _ := json.Marshal(map[string]any{
		"id": "g1", "description": "the ingest watermark work",
		"working_dir": "/work/app",
		"created_at":  "2026-08-30T09:00:00Z", "updated_at": "2026-08-30T09:00:00Z",
	})
	return string(b)
}

func gooseTurn(i int, text string) string {
	role := "user"
	if i%2 == 1 {
		role = "assistant"
	}
	b, _ := json.Marshal(map[string]any{"role": role, "content": text, "created": "2026-08-30T09:00:00Z"})
	return string(b)
}

// goose's JSONL sessions declare an offset parser the append gate never
// reached, so every pass re-read them whole (#2870). Adding them needs one
// thing first: goose writes the session's project in a header line, and an
// offset parse starts past it — measured before this, appending a turn moved
// the session from project "app" to the literal "goose".
func TestAGrowingGooseSessionKeepsItsProject(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	for k, v := range map[string]string{
		"DEJA_CLAUDE_ROOT": "none-claude", "DEJA_CODEX_ROOT": "none-codex",
		"DEJA_GOOSE_DB": "none-goose.db", "DEJA_OPENCODE_DB": "none-opencode.db",
		"DEJA_GROK_DB": "none-grok.db", "DEJA_HERMES_HOME": "none-hermes",
		"DEJA_ZED_DB": "none-zed.db", "DEJA_QWEN_ROOT": "none-qwen",
		"DEJA_NOTES_FILE": "notes.jsonl",
	} {
		t.Setenv(k, filepath.Join(tmp, v))
	}
	root := filepath.Join(tmp, "goose")
	t.Setenv("DEJA_GOOSE_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "sessions", "g1.jsonl")
	write := func(lines []string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed := []string{gooseHeaderLine()}
	for i := 0; i < 6; i++ {
		seed = append(seed, gooseTurn(i, fmt.Sprintf("seed %d zonkomatic", i)))
	}
	write(seed)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	found := func(term string) []string {
		t.Helper()
		got, err := Search(dir, search.Options{Query: term, All: true})
		if err != nil {
			t.Fatal(err)
		}
		var projects []string
		for _, s := range got {
			projects = append(projects, s.Project)
		}
		return projects
	}
	hits := func(term string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: term, All: true})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range got {
			for _, m := range s.Messages {
				if strings.Contains(m.Text, term) {
					n++
				}
			}
		}
		return n
	}
	if p := found("zonkomatic"); len(p) != 1 || p[0] != "app" {
		t.Fatalf("the fixture did not index under its working_dir: %v", p)
	}

	write(append(append([]string{}, seed...), gooseTurn(6, "appended vantorquell")))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if p := found("zonkomatic"); len(p) != 1 || p[0] != "app" {
		t.Errorf("appending a turn moved the session's project: %v", p)
	}
	if n := hits("appended vantorquell"); n != 1 {
		t.Errorf("the appended turn is not indexed: %d", n)
	}
	if n := hits("seed 0 "); n != 1 {
		t.Errorf("an earlier turn is held %d times", n)
	}

	// The rewind the prefix comparison exists for.
	rewritten := []string{gooseHeaderLine()}
	for i := 0; i < 9; i++ {
		rewritten = append(rewritten, gooseTurn(i, fmt.Sprintf("rewritten %d zonkomatic", i)))
	}
	write(rewritten)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("seed 0 "); n != 0 {
		t.Errorf("text from before the rewrite survived: %d", n)
	}
	if n := hits("rewritten 8 "); n != 1 {
		t.Errorf("the rewritten tail is not indexed: %d", n)
	}
}
