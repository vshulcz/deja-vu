package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A search for a word most sessions hold reads only the sessions that can
// reach its page (#5467). What it did not read still matched, and both
// surfaces that print a count have to say so: the JSON total and recall's
// "5 of N matched" read the pool's size otherwise.
func TestCommonWordCountsTheSessionsItDidNotRead(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(15) + 300
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%08d-2222-4000-8000-a1b2c3d4e5f6", i)
		line := `{"type":"user","message":{"role":"user","content":` +
			quoteJSON(fmt.Sprintf("the deploy of service %d is slow", i)) +
			`},"timestamp":"` + at.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) +
			`","sessionId":"` + id + `","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runSearch(dir, []string{"--json", "deploy"}, ""); err != nil {
			t.Error(err)
		}
	})
	var env struct {
		Total  int               `json:"total"`
		Capped bool              `json:"capped"`
		Hits   []json.RawMessage `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v\n%.400s", err, out)
	}
	if env.Total != n || !env.Capped || len(env.Hits) != 15 {
		t.Errorf("search --json: total %d capped %v hits %d, want %d capped 15", env.Total, env.Capped, len(env.Hits), n)
	}

	text, _, _, _, _, err := recallTextResultFrom(dir, "deploy", "", 5, 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("(5 of %d matched)", n); !strings.Contains(text, want) {
		t.Errorf("recall does not say %q:\n%.600s", want, text)
	}
}
