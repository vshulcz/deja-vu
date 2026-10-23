package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// commonWordStore writes n one-line Claude sessions that all say "deploy",
// session i worked in cwdOf(i), and indexes them.
func commonWordStore(t *testing.T, n int, cwdOf func(int) string) string {
	t.Helper()
	return storeOf(t, n, cwdOf, func(i int) string {
		return fmt.Sprintf("the deploy of service %d is slow", i) + strings.Repeat(" and the deploy again", i%4)
	})
}

// storeOf is commonWordStore with session i saying textOf(i).
func storeOf(t *testing.T, n int, cwdOf func(int) string, textOf func(int) string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := range n {
		cwd := cwdOf(i)
		store := filepath.Join(root, "-work-"+filepath.Base(cwd))
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("%08d-2222-4000-8000-a1b2c3d4e5f6", i)
		text := textOf(i)
		line, err := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": cwd,
			"timestamp": at.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": text}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), append(line, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir
}

// countRecallSearches counts the index searches recall runs from here on.
func countRecallSearches(t *testing.T) *int {
	t.Helper()
	n := new(int)
	prev := recallSearch
	recallSearch = func(dir string, o search.Options, progress io.Writer) (index.SearchResult, error) {
		*n++
		return prev(dir, o, progress)
	}
	t.Cleanup(func() { recallSearch = prev })
	return n
}

// recall ranks the project the agent works in first and the machine after.
// That was two searches, and on a word most of a 40k-session store holds the
// second cost as much as the first (#5486). The project's answer is cut from
// the machine's search now, and the page is the one the project's own search
// gives.
func TestRecallCutsTheProjectAnswerFromOneSearch(t *testing.T) {
	base := t.TempDir()
	project, tiny, other, none := filepath.Join(base, "work", "shopfront"), filepath.Join(base, "work", "tinyapp"),
		filepath.Join(base, "work", "billing"), filepath.Join(base, "work", "nowhere")
	for _, d := range []string{project, tiny, other, none} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	n := search.PoolSize(5) + 300
	dir := commonWordStore(t, n, func(i int) string {
		switch {
		case i < 40:
			return project
		case i < 43:
			return tiny
		}
		return other
	})
	t.Chdir(project)

	scope := howScope(howCwd(), "", false)
	po := search.Options{Query: "deploy", All: true, Window: 5, Projects: scope}
	_, _, want, _, err := recallRank(dir, "deploy", po)
	if err != nil {
		t.Fatal(err)
	}
	want = inProjectHits(want, scope)
	if len(want) < 5 {
		t.Fatalf("the project's own search found %d hits", len(want))
	}

	searches := countRecallSearches(t)
	text, _, _, ids, _, err := recallTextResultFrom(dir, "deploy", "", 5, 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if *searches != 1 {
		t.Errorf("recall ran %d searches, want 1", *searches)
	}
	if len(ids) != 5 {
		t.Fatalf("recall served %d sessions:\n%s", len(ids), text)
	}
	for i, id := range ids {
		if id != want[i].Session.ID {
			t.Errorf("hit %d is %s, the project's own search ranks %s there", i+1, id, want[i].Session.ID)
		}
	}
	if w := fmt.Sprintf("(5 of %d matched)", n); !strings.Contains(text, w) {
		t.Errorf("recall does not say %q:\n%.400s", w, text)
	}

	// Outside every indexed project there is nothing to rank first, and no
	// second search either.
	*searches = 0
	t.Chdir(none)
	if _, _, _, _, _, err := recallTextResultFrom(dir, "deploy", "", 5, 0, 8000); err != nil {
		t.Fatal(err)
	}
	if *searches != 1 {
		t.Errorf("outside every indexed project recall ran %d searches, want 1", *searches)
	}

	// The control: three matches are a thin answer, whose tail only the
	// project's own search can draw, so that one still runs.
	*searches = 0
	t.Chdir(tiny)
	if _, _, _, _, _, err := recallTextResultFrom(dir, "deploy", "", 5, 0, 8000); err != nil {
		t.Fatal(err)
	}
	if *searches != 2 {
		t.Errorf("in a project with three matches recall ran %d searches, want 2", *searches)
	}
}
