package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// importedStore builds a machine on the receiving end of `deja sync import`:
// n sessions, every one of them arrived from somewhere else.
func importedStore(t *testing.T, n int) string {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for i := 0; i < n; i++ {
		b, err := json.Marshal(index.SyncRecord{
			Harness: "claude", SessionID: "peer" + string(rune('1'+i)), Project: "tmp/projx",
			Role: "user", Text: "the kafka consumer rebalance keeps flapping, take " + string(rune('1'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		batch = append(append(batch, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writePolicy(t *testing.T, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", p)
}

// Someone who sets `auto: local-only` on a machine where everything arrived by
// sync gets silence from their agent, while search and the listing still
// answer — so the rule is the last thing they suspect. The first screen counted
// the sessions as the memory and said nothing about the rule; doctor had the
// number all along, five screens later (#1067).
func TestBriefSaysWhenThePolicyWithholdsEverything(t *testing.T) {
	dir := importedStore(t, 3)
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "all 3 sessions") || !strings.Contains(out, "keeps them out of every agent") {
		t.Errorf("brief does not say the rule withholds everything:\n%s", out)
	}
	// The number has to agree with doctor's, which is where the reader goes next.
	withheld, total := policyWithheldCounts(dir)
	if withheld["auto"] != 3 || total != 3 {
		t.Fatalf("fixture wrong: withheld=%d total=%d", withheld["auto"], total)
	}
}

// The line is about the rule emptying the agent's memory, not about having a
// rule: a store the rule lets through says nothing extra, and neither does one
// with no rule at all.
func TestBriefStaysQuietWhenThePolicyWithholdsNothing(t *testing.T) {
	dir := importedStore(t, 3)

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("no policy file, yet the brief claims memory is withheld:\n%s", buf.String())
	}

	// A rule that names this import group allows it through again.
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false,"imported:tmp":true}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("rule withholds nothing, yet the brief says it does:\n%s", buf.String())
	}

	// Partial withholding stays quiet: the counters above are still broadly
	// true, and a caveat on every line is wallpaper.
	local := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"loc","cwd":"/proj","timestamp":"2026-07-11T10:00:00Z","message":{"role":"user","content":"local work on the ticker window"}}` + "\n"
	if err := os.WriteFile(filepath.Join(local, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)
	if withheld, total := policyWithheldCounts(dir); withheld["auto"] == 0 || withheld["auto"] == total {
		t.Fatalf("fixture is not partial: withheld=%d total=%d", withheld["auto"], total)
	}
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("partial withholding reported as total:\n%s", buf.String())
	}

	// A rule on the search path does not empty the agent's memory.
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "keeps them out of every agent") {
		t.Errorf("search-path rule reported as withholding from agents:\n%s", buf.String())
	}
}

// askedLine returns the "asked" row of the brief, or "" if there is none.
func askedLine(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "asked ") {
			return ln
		}
	}
	return ""
}

// A question asked locally and again by a peer whose session arrived by sync is
// still the same question asked twice — the one line the brief exists to show.
// Imported sessions used to carry no asked hashes, so the repeat crossed a sync
// boundary unseen; stats' RepeatQuestions (from records) counted it and the two
// screens disagreed. It stays gated: a machine that withholds imported memory
// from this screen must not surface an all-imported repeat. The rule is the
// reader's own — `search`, like the rest of the brief — since a rule aimed at
// agents leaves work the reader can still grep for themselves (#1312).
func TestBriefAskedTwiceCountsImportedAndHonoursPolicy(t *testing.T) {
	q := "how do I configure the retry queue for delivery?"

	// A local session asks it in March.
	tmp := hermeticEnv(t)
	local := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"locz","cwd":"/proj","timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"` + q + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(local, "l.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	// A peer asks the same thing in July; it arrives by sync.
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	peer := `{"harness":"claude","session_id":"peerz","project":"proj","role":"user","text":"` + q + `","time":"2026-07-01T10:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-p.jsonl"), []byte(peer), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	// Default policy (local+imported): the repeat surfaces.
	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := askedLine(buf.String()); !strings.Contains(ln, "retry queue") {
		t.Fatalf("asked-twice did not count the imported repeat; asked line = %q\n%s", ln, buf.String())
	}

	// A rule that keeps imported memory off the reader's own screen must drop
	// the line: the only thing keeping the count at two is a session they chose
	// to hide. The whole brief reads `search` — a rule aimed at agents alone
	// leaves this screen intact, since the reader can still grep the same
	// session (#1312).
	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := askedLine(buf.String()); ln != "" {
		t.Errorf("asked-twice surfaced an all-imported repeat under search:local-only:\n%s", ln)
	}
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := askedLine(buf.String()); ln == "" {
		t.Errorf("a rule aimed at agents emptied the reader's own screen:\n%s", buf.String())
	}
}

// hitLine returns the "hit" (friction) row of the brief, or "" if there is none.
func hitLine(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "hit ") {
			return ln
		}
	}
	return ""
}

// A wall a peer kept hitting, arriving by sync, is a wall — `deja friction` and
// stats both count it, and the brief's one friction line used to be the lone
// holdout because imported sessions carried no Hit hashes. Populate meta.Hit on
// import and gate the brief's lookup on the search rule, the same shape as the
// asked-twice test above.
func TestBriefFrictionCountsImportedAndHonoursPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for i := 1; i <= index.FrictionMinSessions; i++ {
		rec := `{"harness":"claude","session_id":"peer` + string(rune('0'+i)) + `","project":"svc","role":"tool-output","text":"npm ERR! code ELIFECYCLE","time":"2026-0` + string(rune('0'+i)) + `-01T10:00:00Z"}` + "\n"
		batch = append(batch, rec...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-p.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := hitLine(buf.String()); !strings.Contains(ln, "ELIFECYCLE") {
		t.Fatalf("friction line did not count the imported wall; hit line = %q\n%s", ln, buf.String())
	}

	writePolicy(t, `{"activations":{"search":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := hitLine(buf.String()); ln != "" {
		t.Errorf("friction surfaced an all-imported wall under search:local-only:\n%s", ln)
	}
	// And a rule that only stops agents leaves the reader's own screen alone.
	writePolicy(t, `{"activations":{"auto":{"local":true,"imported":false}}}`)
	buf.Reset()
	if err := runBrief(dir, &buf); err != nil {
		t.Fatal(err)
	}
	if ln := hitLine(buf.String()); ln == "" {
		t.Errorf("a rule aimed at agents emptied the reader's own screen:\n%s", buf.String())
	}
}
