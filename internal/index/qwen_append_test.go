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

// qwenLine writes one transcript line in the shape qwen stores: the role is on
// the envelope and the text sits in message.parts.
func qwenLine(i int, text string) string {
	role, inner := "user", "user"
	if i%2 == 1 {
		role, inner = "assistant", "model"
	}
	b, _ := json.Marshal(map[string]any{
		"type": role, "sessionId": "s1", "timestamp": "2026-08-30T09:00:00.000Z",
		"message": map[string]any{"role": inner, "parts": []any{map[string]any{"text": text}}},
	})
	return string(b)
}

// qwen declares an offset parser and was not on the list of harnesses the
// append path will trust, so every pass re-read the whole file — the shape
// #2075 found on the database side (#2870).
//
// The list is per harness on purpose: appending onto a file that was rewritten
// would leave the old text in the index, so a harness joins it only with a
// fixture that shows both halves.
func TestAGrowingQwenSessionIsAppendedNotReread(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "none-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "none-codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "none-grok.db"))
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(tmp, "none-hermes"))
	t.Setenv("DEJA_ZED_DB", filepath.Join(tmp, "none-zed.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	chats := filepath.Join(root, "projects", "-work-app", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(chats, "s1.jsonl")
	write := func(lines []string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var seed []string
	for i := 0; i < 6; i++ {
		seed = append(seed, qwenLine(i, fmt.Sprintf("seed %d zonkomatic", i)))
	}
	write(seed)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
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
	if hits("seed 0 ") != 1 {
		t.Fatalf("the fixture did not index, so this measures nothing")
	}

	// A turn is appended: it has to arrive, and the earlier ones must not be
	// counted twice — a cursor that hands back what it already gave grows the
	// session on every pass (#2025).
	write(append(append([]string{}, seed...), qwenLine(6, "appended vantorquell")))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("appended vantorquell"); n != 1 {
		t.Errorf("the appended turn is not indexed: %d", n)
	}
	if n := hits("seed 0 "); n != 1 {
		t.Errorf("an earlier turn is held %d times", n)
	}

	// And a rewind: the file is rewritten with different text and regrows past
	// its old length, which looks exactly like an append until the prefix is
	// compared. The old text must not survive.
	var rewritten []string
	for i := 0; i < 9; i++ {
		rewritten = append(rewritten, qwenLine(i, fmt.Sprintf("rewritten %d zonkomatic", i)))
	}
	write(rewritten)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("seed 0 "); n != 0 {
		t.Errorf("text from before the rewrite survived in the index: %d", n)
	}
	if n := hits("rewritten 8 "); n != 1 {
		t.Errorf("the rewritten tail is not indexed: %d", n)
	}
}
