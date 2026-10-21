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

// A quoted phrase over two common words: the postings name every session
// holding both, and the paged total counted the unread ones among them, so
// search and recall said 106 matched where 5 say it (#5495).
func TestQuotedPhraseTotalCountsTheSessionsThatSayIt(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	n := search.PoolSize(15) + 600
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%08d-2222-4000-8000-a1b2c3d4e5f6", i)
		text := fmt.Sprintf("alpha went out first and beta followed in run %d", i)
		if i%(n/5) == n/10 {
			text = fmt.Sprintf("the alpha beta rollout of run %d", i)
		}
		line := `{"type":"user","message":{"role":"user","content":` + quoteJSON(text) +
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

	for _, c := range []struct {
		q    string
		want int
	}{
		// The control: the words alone match every session.
		{"alpha beta", n},
		{`"alpha beta"`, 5},
	} {
		out := captureStdout(t, func() {
			if err := runSearch(dir, []string{"--json", "--limit", "3", c.q}, ""); err != nil {
				t.Error(err)
			}
		})
		var env struct {
			Total     int  `json:"total"`
			Estimated bool `json:"total_estimated"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%v\n%.400s", err, out)
		}
		if env.Total != c.want || env.Estimated {
			t.Errorf("search --json %s: total %d estimated %v, want %d", c.q, env.Total, env.Estimated, c.want)
		}
	}

	text, _, _, _, _, err := recallTextResultFrom(dir, `"alpha beta"`, "", 3, 0, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "(3 of 5 matched)") || !strings.Contains(text, "2 more matches") {
		t.Errorf("recall does not say 3 of 5:\n%.600s", text)
	}
}

// When retrieval only estimated the unread sessions, every line that counts
// them says so.
func TestEstimatedTotalSaysAbout(t *testing.T) {
	for _, c := range []struct {
		line, want string
	}{
		{recallCountLineAbout("alpha beta", search.TierExact, 0, 3, 40, 0, false, true), "(3 of about 40 matched)"},
		{recallCountLineAbout("alpha beta", search.TierExact, 3, 3, 40, 0, false, true), "(matches 4-6 of about 40)"},
		// An estimate of none left over still says it is one, and not by
		// editing the query it echoes.
		{recallCountLineAbout("1 of 3", search.TierExact, 0, 3, 3, 0, false, true), `"1 of 3" (3 of about 3 matched)`},
		{contextOthersNote(41, true), "about 40 other sessions"},
	} {
		if !strings.Contains(c.line, c.want) {
			t.Errorf("%q does not say %q", c.line, c.want)
		}
	}
	if exact := recallCountLineAbout("alpha beta", search.TierExact, 0, 3, 40, 0, false, false); strings.Contains(exact, "about") {
		t.Errorf("an exact count line says about: %q", exact)
	}
}
