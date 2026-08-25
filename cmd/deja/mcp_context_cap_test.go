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
)

// seedContextDigestSession writes a session long enough that the digest hits
// its budget, in a project and under an id the caller names — the header
// carries both, and neither was bounded.
func seedContextDigestSession(t *testing.T, project, id string) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-"+project)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	var b strings.Builder
	for i := range 400 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		text := strings.Repeat("резервирование шардов ", 12)
		if i%3 == 0 {
			text = strings.Repeat(fmt.Sprintf("wobbleshard budget note %d ", i), 12)
		}
		line, err := json.Marshal(map[string]any{
			"type":      role,
			"timestamp": base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			"sessionId": id,
			"cwd":       "/" + project,
			"message":   map[string]any{"role": role, "content": text},
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// recall_context documents ~8KB and had no cap: the body budget lives inside
// PrintContext, and the header, the tier prefix and the frame were all added on
// top of it. recall at the same layer is exact because it trims before framing
// (#1797).
func TestRecallContextFitsTheBudgetItDocuments(t *testing.T) {
	for _, tc := range []struct{ name, project, id string }{
		{"ordinary", "proj", "wobble"},
		{"long names", strings.Repeat("p", 180), strings.Repeat("w", 120)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := seedContextDigestSession(t, tc.project, tc.id)
			framed, err := callMCPTool(dir, "recall_context", []byte(`{"query":"резервирование шардов"}`))
			if err != nil {
				t.Fatal(err)
			}
			if len(framed) > contextMCPBudget {
				t.Errorf("recall_context returned %d bytes, budget is %d", len(framed), contextMCPBudget)
			}
			if len(framed) < contextMCPBudget/2 {
				t.Errorf("%d bytes is too little to prove a cap was applied rather than the session being short", len(framed))
			}
			if !strings.Contains(framed, "шард") {
				t.Errorf("the digest lost the words it matched on")
			}
		})
	}
}

// A cut digest says it was cut. Without the line the reply ends mid-word and
// reads as the whole session — which is what an agent then tells the user it
// saw.
func TestATrimmedDigestSaysItWasTrimmed(t *testing.T) {
	dir := seedContextDigestSession(t, "proj", "wobble")
	framed, err := callMCPTool(dir, "recall_context", []byte(`{"query":"резервирование шардов"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(framed, "digest trimmed") {
		t.Errorf("a digest at the budget does not say it was cut:\n%s", framed[len(framed)-200:])
	}
	if strings.Count(framed, "digest trimmed") != 1 {
		t.Errorf("the marker appears %d times", strings.Count(framed, "digest trimmed"))
	}

	// The control: a session that fits gets no marker.
	short := shortContextSession(t)
	framed, err = callMCPTool(short, "recall_context", []byte(`{"query":"pool exhausted"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(framed, "digest trimmed") {
		t.Errorf("a digest well inside the budget claims it was cut:\n%s", framed)
	}
}

// shortContextSession seeds one session small enough to fit whole.
func shortContextSession(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"the pool exhausted again"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"short","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "short.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The backup to a line break is tidiness; the matched turn is the answer. A
// digest whose only match sits in the last line must not lose it to the cut.
func TestTheBackupNeverDropsTheMatch(t *testing.T) {
	filler := strings.Repeat("filler line that says nothing\n", 300)
	// One long last line whose match sits near its start: the trim keeps the
	// match, and the backup to the previous line break would throw it away.
	tail := "the wobbleshard verdict is that it holds " + strings.Repeat("and then some more words about it ", 14) + "\n"
	full := filler + tail
	budget := len(full) - 200

	out := fitContextDigest(full, "wobbleshard verdict", budget)
	if !strings.Contains(out, "wobbleshard") {
		t.Errorf("the backup dropped the line carrying the query:\n%s", out[max(0, len(out)-160):])
	}
	if len(out) > budget {
		t.Errorf("the guard broke the budget: %d > %d", len(out), budget)
	}

	// The control: nothing at stake in that last line, so it is still tidied
	// away at the line break rather than left ragged.
	plain := fitContextDigest(filler+strings.Repeat("another filler line ", 25)+"\n", "wobbleshard", budget)
	if !strings.HasSuffix(plain, contextDigestCut) || strings.Contains(strings.TrimSuffix(plain, contextDigestCut), "another filler") {
		t.Errorf("the line-break backup stopped happening when nothing was at stake:\n%q", plain[max(0, len(plain)-200):])
	}
}
