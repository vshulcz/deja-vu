package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestPiToolRecordsSurviveIncrementalIndexing(t *testing.T) {
	root := t.TempDir()
	setHome(t, root)
	sessions := filepath.Join(root, "pi")
	t.Setenv("DEJA_PI_ROOT", sessions)
	for _, flag := range []string{"PATHS", "EDITS", "WRITES", "COMMANDS"} {
		t.Setenv("DEJA_INDEX_"+flag, "1")
	}
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, "tools.jsonl")
	header := `{"type":"session","id":"pi-tools","timestamp":"2026-01-02T03:04:05Z"}` + "\n"
	turn := func(minute int) string {
		return fmt.Sprintf(`{"type":"message","timestamp":"2026-01-02T03:%02d:06Z","message":{"role":"assistant","content":[{"type":"toolCall","name":"edit","arguments":{"path":"src/pool.go","edits":[{"oldText":"replaced span %d","newText":"pool capacity increased for request %d"}]}},{"type":"toolCall","name":"bash","arguments":{"command":"go test ./..."}}]}}`+"\n", minute, minute, minute)
	}
	if err := os.WriteFile(path, []byte(header+turn(4)), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "index")
	for _, minute := range []int{4, 5} {
		if minute == 5 {
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(turn(minute))
			closeErr := f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		if err := Ensure(dir, "pi", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	found, err := FindManyByIdentity(dir, []Identity{{Harness: "pi", ID: "pi-tools"}})
	if err != nil || len(found) != 1 {
		t.Fatalf("indexed sessions = %v, %v", found, err)
	}
	edits := map[string]bool{}
	wrote := map[int]bool{}
	paths := 0
	commands := 0
	for _, m := range found[0].Messages {
		switch m.Role {
		case sources.RoleCommand:
			if m.Text == "$ go test ./..." {
				commands++
			}
		case sources.RoleFiles:
			if strings.Contains(m.Text, "src/pool.go") {
				paths++
			}
		case sources.RoleEdit:
			edits[m.Text] = true
		case sources.RoleWrote:
			for _, minute := range []int{4, 5} {
				hash, _ := sources.HashWrittenLine(fmt.Sprintf("pool capacity increased for request %d", minute))
				if p, ok := sources.WroteRecordHas(m.Text, hash); ok && p == "src/pool.go" {
					wrote[minute] = true
				}
			}
		}
	}
	if paths != 2 {
		t.Errorf("file-path records = %d, want 2", paths)
	}
	for _, minute := range []int{4, 5} {
		if !edits[fmt.Sprintf("src/pool.go\nreplaced span %d", minute)] {
			t.Errorf("restore span missing for turn %d", minute)
		}
		if !wrote[minute] {
			t.Errorf("written-line evidence missing for turn %d", minute)
		}
	}
	if commands != 2 {
		t.Errorf("command records = %d, want 2", commands)
	}
}
