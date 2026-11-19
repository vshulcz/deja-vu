package main

import (
	"bytes"
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `deja how` counts its own rows over the record log, so it split a command by
// the outcome codex and opencode append to it: "$ make test" and
// "$ make test  → exit 2" as two lines of one screen (#2590).
func TestHowCountsACommandOnceWhateverItExitedWith(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	line := func(text string, mins int) string {
		return `{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"` + text +
			`\"]}","timestamp":"` + at.Add(time.Duration(mins)*time.Minute).Format(time.RFC3339) + `"}`
	}
	_ = line
	// The reader-visible shape is what matters here, so write the records the
	// way the opencode reader produces them: the command line carries the code.
	body := ""
	var bodySb30 strings.Builder
	for i, text := range []string{"$ make test", "$ make test  → exit 2", "$ make test  → exit 2"} {
		bodySb30.WriteString(`{"type":"user","sessionId":"s1","cwd":"/w/app","timestamp":"` +
			at.Add(time.Duration(i)*time.Minute).Format(time.RFC3339) +
			`","message":{"role":"user","content":[{"type":"tool_use","name":"Bash","input":{"command":"` +
			strings.TrimPrefix(text, "$ ") + `"}}]}}` + "\n")
	}
	body += bodySb30.String()
	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "how", "make test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "make test") {
		t.Fatalf("how found nothing, so this measures nothing:\n%s", out)
	}
	if strings.Contains(out, "exit") {
		t.Errorf("the outcome is on the screen as part of the command:\n%s", out)
	}
	if n := strings.Count(out, "$ make test"); n != 1 {
		t.Errorf("one command on %d rows:\n%s", n, out)
	}
}

// writeHowRuns writes one session per command text, so each run counts as a
// separate session the way `how` weighs them.
func writeHowRuns(t *testing.T, texts []string) {
	t.Helper()
	at := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	dir := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, text := range texts {
		id := "s" + string(rune('1'+i))
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` +
			at.Add(time.Duration(i)*time.Hour).Format(time.RFC3339) +
			`","message":{"role":"user","content":[{"type":"tool_use","name":"Bash","input":{"command":"` +
			text + `"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// `deja how` strips the outcome codex and opencode append so one command is
// one row (#2590), and then looked at it no further: a command whose every
// recorded run on this machine failed was offered as the way it is done here,
// in the same shape as one that has always worked (#2624).
func TestHowSaysWhenEveryRecordedRunFailed(t *testing.T) {
	hermeticEnv(t)
	writeHowRuns(t, []string{"npm run build  → exit 127", "npm run build  → exit 127"})
	out, err := captureRun(t, "how", "npm run build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Fatalf("the two runs are no longer one command:\n%s", out)
	}
	if !strings.Contains(out, "every run recorded here failed (exit 127)") {
		t.Fatalf("nothing on the line says the command never once worked:\n%s", out)
	}
}

// A command that sometimes fails is ordinary, and a command deja has no
// outcome for is most of the store: neither earns the line.
func TestHowStaysQuietWhenTheHistoryIsNotAllFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		texts []string
	}{
		{"mixed outcomes", []string{"npm run build  → exit 127", "npm run build  → exit 0"}},
		{"one run has no outcome", []string{"npm run build  → exit 127", "npm run build"}},
		{"no outcome at all", []string{"npm run build", "npm run build"}},
		{"every run worked", []string{"npm run build  → exit 0", "npm run build  → exit 0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			writeHowRuns(t, tc.texts)
			out, err := captureRun(t, "how", "npm run build")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "failed") {
				t.Fatalf("the line claims a failing history it does not have:\n%s", out)
			}
		})
	}
}

// The same note over MCP: the agent reading that answer is the one most likely
// to run the command straight back (#2624).
func TestMCPHowSaysWhenEveryRecordedRunFailed(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-h")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for _, id := range []string{"h1", "h2"} {
		body := `{"type":"assistant","sessionId":"` + id + `","cwd":"/w/h","timestamp":"2026-07-20T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"npm run build  \u2192 exit 127"}}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"npm run build"}`))
	if err != nil {
		t.Fatalf("how: %v", err)
	}
	if !strings.Contains(got, "every run recorded here failed (exit 127)") {
		t.Fatalf("the agent is not told the command never once worked:\n%s", got)
	}
}

// The last two surfaces from #2862's list. "No command on this machine mentions
// X" and "No session on this machine ran a command after that error" are honest
// about the machine and silent about the store: on a first run they read as a
// real absence, and an agent concludes the work was never done — the mistake
// #680 named and #2862 and #2863 fixed for recall and blame.
func TestHowAndFixSayTheStoreIsEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ tool, args string }{
		{"how", `{"what":"deploy"}`},
		{"fix", `{"error":"ld: symbol(s) not found for architecture arm64"}`},
	} {
		text, err := callMCPTool(dir, tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "no indexed history") {
			t.Errorf("%s: an empty store answers as though the thing was never done:\n%s", tc.tool, text)
		}
	}
}

// A store with sessions keeps its own answer: it is a real absence there.
func TestHowAndFixOnAStoreWithHistoryDoNotCallItEmpty(t *testing.T) {
	dir := manySessionStore(t, 3)
	for _, tc := range []struct{ tool, args string }{
		{"how", `{"what":"nothing-like-this-was-ever-run"}`},
		{"fix", `{"error":"ld: symbol(s) not found for architecture arm64"}`},
	} {
		text, err := callMCPTool(dir, tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "no indexed history") {
			t.Errorf("%s: a store with sessions in it was called empty:\n%s", tc.tool, text)
		}
	}
}

// `deja log` is the record of what an agent was handed, and the two tools that
// serve commands were the two it never mentioned (#2858). The kind list already
// argues the case twice — blame and the resource read were added because
// leaving them out "understated what the agent was given" (#682), and remember
// because a write "showed up nowhere in the journal the user reads".
//
// Recorded, and deliberately not counted as memory served: `servedKind` decides
// the ratios `deja stats` publishes, and a one-line command is not the store's
// text handed over. That half is asserted below too, because the last time
// these drifted a blame was a recall on one screen and nothing on the other
// (#1907).
func TestHowAndFixReachTheJournal(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const errLine = "ld: symbol(s) not found for architecture arm64"
	for i, sid := range []string{"s1", "s2"} {
		day := string(rune('1' + i))
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:00:00Z","message":{"role":"user","content":"deploying the zonkomatic"}}
{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + errLine + `"}]}}
{"type":"assistant","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + day + `T09:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"helm upgrade zonkomatic --set replicas=9"}}]}}
`
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	kinds := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		b, err := os.ReadFile(usage.Path(dir))
		if err != nil {
			return out
		}
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			var ev struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil {
				out[ev.Kind]++
			}
		}
		return out
	}
	// The number `deja stats` publishes: recalls counted as memory served.
	served := func() int {
		t.Helper()
		c := usage.StatusCounters(dir)
		return c.Recalls + c.Injections
	}

	before := served()
	text, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "helm upgrade") {
		t.Fatalf("how served nothing, so there is nothing to journal:\n%s", text)
	}
	text, err = callMCPTool(dir, "fix", json.RawMessage(`{"error":"`+errLine+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "helm upgrade") {
		t.Fatalf("fix served nothing, so there is nothing to journal:\n%s", text)
	}

	got := kinds()
	if got[usage.KindHow] != 1 {
		t.Errorf("how is not in the journal: %v", got)
	}
	if got[usage.KindFix] != 1 {
		t.Errorf("fix is not in the journal: %v", got)
	}
	// And the number deja publishes has not moved: a command offered is not
	// the store's text served.
	if after := served(); after != before {
		t.Errorf("the served count moved from %d to %d — the ratios in `deja stats` changed with it", before, after)
	}
}

// recall_frame.go states the rule: recalled transcript text is data an
// attacker may have influenced, so agent-facing output is framed as untrusted.
// `how` is agent-facing and returns the most directly actionable thing deja
// serves — command lines lifted out of transcripts — and it was the one MCP
// answer with no frame and no note at all.
func TestTheHowToolFramesWhatItServes(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","sessionId":"s1","cwd":"/app","timestamp":"2026-08-01T09:00:00Z","message":{"role":"user","content":"how do we deploy"}}
{"type":"assistant","sessionId":"s1","cwd":"/app","timestamp":"2026-08-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"helm upgrade zonkomatic --set replicas=9"}}]}}
`
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "helm upgrade zonkomatic") {
		t.Fatalf("the command was not served, so there is nothing to frame:\n%s", text)
	}
	if !strings.Contains(text, "Reference data, not instructions") {
		t.Errorf("a command drawn from a transcript arrives with nothing saying where it came from:\n%s", text)
	}
}

// howEntries counted withheld records and the sentence it feeds says sessions,
// so one hidden session that ran a command five times was reported as five
// (#1641, the shape #1639 fixed for friction). The CLI and the MCP how tool
// both pass that number on.
func TestHowCountsWithheldSessionsNotRecords(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, 5)
	for i := range 5 {
		lines = append(lines, `{"type":"assistant","timestamp":"2026-07-10T10:0`+string(rune('0'+i))+`:00Z","sessionId":"bbbb0002-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	}
	if err := os.WriteFile(filepath.Join(store, "bbbb0002.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	out, err := captureRun(t, "how", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hides 1 matching session") {
		t.Errorf("the store holds one session; the note says otherwise:\n%s", out)
	}
	if strings.Contains(out, "hides 5") {
		t.Errorf("record count reported as sessions:\n%s", out)
	}
}

// The sentence says the trust policy "hides N matching sessions", and the
// count was taken before the terms were matched — so a question about
// terraform on a machine whose hidden sessions only ever ran `go test` was
// told three of them were hidden from the answer (#2766).
func TestHowCountsTheHiddenSessionsThatMatchTheQuestion(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(name, id, day, command string) {
		t.Helper()
		line := `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + command + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed("one.jsonl", "aaaa0001-1111-4000-8000-d6e7f8a9b0c1", "10", "go test ./...")
	seed("two.jsonl", "bbbb0002-1111-4000-8000-d6e7f8a9b0c1", "11", "go test ./internal/...")
	seed("three.jsonl", "cccc0003-1111-4000-8000-d6e7f8a9b0c1", "12", "terraform apply -auto-approve")

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(pol, []byte(`{"activations":{"search":{"*":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	out, err := captureRun(t, "how", "terraform")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hides 1 matching session") {
		t.Errorf("one hidden session ran terraform; the note says otherwise:\n%s", out)
	}
	if strings.Contains(out, "hides 3") {
		t.Errorf("sessions that ran something else were counted as matching:\n%s", out)
	}

	// And a question nothing hidden answers is not told anything was hidden
	// from it.
	out, err = captureRun(t, "how", "kubectl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hides") {
		t.Errorf("a question no hidden session answers was told the policy withheld something:\n%s", out)
	}
}

// The ignore rule moved with the trust policy and says the same kind of thing
// — "keeps N sessions out of this answer" — so it is counted the same way:
// against what was asked, not against the store.
func TestHowCountsTheIgnoredSessionsThatMatchTheQuestion(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := func(name, id, day, command string) {
		t.Helper()
		line := `{"type":"assistant","timestamp":"2026-07-` + day + `T10:00:00Z","sessionId":"` + id +
			`","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + command + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, name), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed("one.jsonl", "aaaa0001-1111-4000-8000-d6e7f8a9b0c1", "10", "go test ./...")
	seed("two.jsonl", "cccc0003-1111-4000-8000-d6e7f8a9b0c1", "12", "terraform apply -auto-approve")

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(tmp, "policy.json")
	body := `{"ignore":["` + strings.ReplaceAll(store, `\`, `\\`) + `/*"]}`
	if err := os.WriteFile(pol, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	// The ignore note goes to stderr, beside the answer rather than in it.
	out, err := captureRunStderr(t, "how", "terraform")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "keeps 1 session") {
		t.Errorf("one ignored session ran terraform; the note says otherwise:\n%s", out)
	}
	out, err = captureRunStderr(t, "how", "kubectl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ignore rule keeps") {
		t.Errorf("a question no ignored session answers was told the rule withheld something:\n%s", out)
	}
}

// "the trust policy hides N matching sessions" counted every withheld command
// record in the store — before the terms were checked and before --project.
// So asking about terraform on a machine whose hidden sessions only ever ran
// `go test` was told three matching sessions were being kept from it, and an
// agent reading that has been handed a reason to distrust an honest answer
// (#2766).
func TestHowCountsOnlyWhatTheQuestionAsked(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// One hidden session, and what it ran has nothing to do with the question
	// asked below.
	body := `{"type":"user","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"run the suite"}}
{"type":"assistant","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"assistant","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go vet ./..."}}]}}
`
	if err := os.WriteFile(filepath.Join(proj, "sess-one.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	cfg := filepath.Join(tmp, "config", "deja")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "policy.json"),
		[]byte(`{"activations":{"search":{"local":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	if policy.Load().Allows(policy.ActivationSearch, "app") {
		t.Fatal("the policy did not take effect, so this test would pass for the wrong reason")
	}

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// Asked about something the hidden session never ran.
	_, hidden, _, err := howEntries(dir, []string{"terraform"}, nil, policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if hidden != 0 {
		t.Errorf("said %d sessions matching \"terraform\" are hidden; the hidden session ran go test", hidden)
	}

	// Asked about what it did run, the count has to survive — a scoped number
	// that is always zero would pass the check above and hide a real leak.
	_, hidden, _, err = howEntries(dir, []string{"vet"}, nil, policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if hidden == 0 {
		t.Error("a command the question does match was withheld with nothing said")
	}
}

// `deja how` answers "how do I run this here", and its answer is the single
// output most worth piping somewhere else — yet it was the one command in this
// family without `--json` (#1931).

func TestHowJSONIsAVersionedEnvelope(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json", "go"}, &buf); err != nil {
		t.Fatalf("how --json refused: %v", err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if got.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version = %d, want %d", got.SchemaVersion, jsonout.Version)
	}
	if got.Commands == nil {
		t.Fatal("commands is null; an empty result must still be an array")
	}
}

// The empty case keeps the shape. The prose path answers with a sentence naming
// the terms, and separately with a policy note when a rule withheld everything;
// a script can branch on neither.
func TestHowJSONKeepsItsShapeWhenNothingMatches(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json", "nothing-matches-this-xyzzy"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) != 0 || got.Found != 0 || got.Truncated {
		t.Fatalf("want an empty result, got %+v", got)
	}
}

// `found` and `truncated` exist because the cap note goes to STDERR. A caller
// reading stdout alone could not otherwise tell eight ways to run the tests
// from thirteen.
func TestHowJSONReportsWhatTheLimitHid(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json", "--limit", "1", "go"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) > 1 {
		t.Fatalf("--limit 1 returned %d commands", len(got.Commands))
	}
	if got.Found < len(got.Commands) {
		t.Fatalf("found %d is below the rows returned %d", got.Found, len(got.Commands))
	}
	if got.Truncated != (got.Found > len(got.Commands)) {
		t.Fatalf("truncated=%v disagrees with found=%d rows=%d", got.Truncated, got.Found, len(got.Commands))
	}
}

// A command with no search terms is still a usage error, and the flag must not
// become the term — the shape `fix` was refused over.
func TestHowJSONIsNotSearchedFor(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--json"}, &buf); err == nil {
		t.Fatal("how --json with no terms was accepted; the flag became the query")
	}
}

// Answering one flag must not reopen the dropped-on-the-floor behaviour the
// unknown-flag guard closed.
func TestHowStillRefusesAnUnknownFlag(t *testing.T) {
	root := frictionEnv(t)
	writeFrictionCorpus(t, root, index.FrictionMinSessions)
	_ = root
	var buf bytes.Buffer
	if err := runHow(index.DefaultDir(), []string{"--jsonn", "go"}, &buf); err == nil {
		t.Fatal("how accepted --jsonn")
	}
}

// howEntries carries the withheld count for a stated reason: filtering on its
// own turns a leak into a confident "no command mentions that" over records a
// rule hid. The prose path says so in a sentence, and a script cannot read a
// sentence — so `commands: []` alone would mean both "nothing matched" and
// "everything that matched was withheld".
func TestHowJSONSaysWhatARuleWithheld(t *testing.T) {
	dir := importedCommandStore(t)

	// Control: with no rule in force the command is answered, or the arm below
	// is measuring an empty store rather than a policy.
	var open bytes.Buffer
	if err := runHow(dir, []string{"--json", "kafkactl"}, &open); err != nil {
		t.Fatal(err)
	}
	var before howJSON
	if err := json.Unmarshal(open.Bytes(), &before); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, open.String())
	}
	if len(before.Commands) == 0 {
		t.Fatalf("nothing matched without a policy, so this test measures nothing:\n%s", open.String())
	}

	writePolicy(t, `{"activations":{"search":{"imported":false}}}`)

	var buf bytes.Buffer
	if err := runHow(dir, []string{"--json", "kafkactl"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) != 0 {
		t.Fatalf("the policy should have withheld everything, got %d rows", len(got.Commands))
	}
	if got.Withheld < 1 {
		t.Errorf("withheld=%d — an empty result reads as \"nothing matched\":\n%s", got.Withheld, buf.String())
	}
}

// The ignore rule is the other half, and it is a separate number: the trust
// policy is about where a session came from, the ignore rule about a path the
// reader named. A consumer that can see only one of them still cannot tell an
// empty answer from a hidden one.
func TestHowJSONSaysWhatTheIgnoreRuleHid(t *testing.T) {
	dir := importedCommandStore(t)
	writePolicy(t, `{"ignore":["*projx*"]}`)

	var buf bytes.Buffer
	if err := runHow(dir, []string{"--json", "kafkactl"}, &buf); err != nil {
		t.Fatal(err)
	}
	var got howJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(got.Commands) != 0 {
		t.Fatalf("the ignore rule should have taken the only match, got %d rows", len(got.Commands))
	}
	if got.Ignored < 1 {
		t.Errorf("ignored=%d — the rule hid the answer and the envelope says nothing:\n%s", got.Ignored, buf.String())
	}
}

// importedCommandStore holds one imported session that ran a command, so a
// local-only policy has something to withhold.
func importedCommandStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for i, rec := range []index.SyncRecord{
		{Harness: "claude", SessionID: "peer1", Project: "tmp/projx", Role: "user",
			Text: "how do we drive kafka here"},
		{Harness: "claude", SessionID: "peer1", Project: "tmp/projx", Role: "command",
			Text: "kafkactl consume orders --from-beginning"},
	} {
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
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
	if out, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatalf("import: %v (%s)", err, strings.TrimSpace(out))
	}
	return dir
}

func howFixture(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(cmd, ts string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","sessionId":"ffff0001-1111-4000-8000-d6e7f8a9b0c1","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}`
	}
	lines := []string{
		rec("go test ./internal/search/ -run Retry", "2026-07-10T10:01:00Z"),
		rec("go test ./internal/search/ -run Retry -v", "2026-07-10T10:02:00Z"),
		rec("golangci-lint run ./...", "2026-07-10T10:03:00Z"),
	}
	if err := os.WriteFile(filepath.Join(store, "ffff0001.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `how go` asks how this machine runs go. Matching was a raw substring test, so
// golangci-lint answered — and answered first (#1630).
func TestHowDoesNotMatchInsideAnotherWord(t *testing.T) {
	dir := howFixture(t)
	var b bytes.Buffer
	if err := runHow(dir, []string{"go"}, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "golangci-lint") {
		t.Errorf("`how go` answered with golangci-lint:\n%s", out)
	}
	if !strings.Contains(out, "go test ./internal/search/") {
		t.Errorf("`how go` lost the go commands:\n%s", out)
	}
	// The controls: the full name still finds the linter, and a multi-word
	// query still narrows.
	b.Reset()
	if err := runHow(dir, []string{"golangci-lint"}, &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "golangci-lint run") {
		t.Errorf("`how golangci-lint` lost it:\n%s", b.String())
	}
	b.Reset()
	if err := runHow(dir, []string{"go", "test"}, &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "go test") || strings.Contains(b.String(), "golangci") {
		t.Errorf("`how go test`:\n%s", b.String())
	}
}

// The same flag loop files had: what you typed has to do something (#1630).
func TestHowRefusesWhatDoesNothing(t *testing.T) {
	dir := howFixture(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"go", "--limit"}, "--limit needs value"},
		{[]string{"go", "--project"}, "--project needs value"},
		{[]string{"go", "--project", ""}, "--project needs value"},
		{[]string{"go", "--unknown"}, "unknown flag"},
		{[]string{""}, "usage"},
	} {
		if err := runHow(dir, tc.args, io.Discard); err == nil {
			t.Errorf("how %v was accepted", tc.args)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("how %v: %v, want it to mention %q", tc.args, err, tc.want)
		}
	}
	for _, args := range [][]string{{"go"}, {"go", "--limit", "3"}, {"go", "--project", "api"}, {"--", "-run"}} {
		if err := runHow(dir, args, io.Discard); err != nil {
			t.Errorf("how %v: %v", args, err)
		}
	}
}

// A dash or an underscore is a boundary, not part of the word: people ask for
// the half of a hyphenated tool they remember. Review named this as the cost of
// the first attempt, where both counted as word characters (#1630).
func TestHowFindsTheHalfOfAHyphenatedCommand(t *testing.T) {
	dir := howFixture(t)
	for _, term := range []string{"lint", "golangci", "golangci-lint"} {
		var b bytes.Buffer
		if err := runHow(dir, []string{term}, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "golangci-lint run") {
			t.Errorf("`how %s` lost golangci-lint:\n%s", term, b.String())
		}
	}
	// The control that started all this: `go` must still not match golangci.
	var b bytes.Buffer
	if err := runHow(dir, []string{"go"}, &b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "golangci") {
		t.Errorf("`how go` matched inside golangci again:\n%s", b.String())
	}
}

// The MCP how tool cut the list at its limit and said nothing, so an agent
// asking how the tests are run here got eight of thirteen with no way to know
// there were more — and no way to ask a follow-up. The CLI has said so since
// #1632; the tool reimplemented the answer instead of sharing it (#1634).
func TestTheMCPHowToolSaysWhenItCutTheList(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 13 {
		id := "0000000" + string(rune('a'+i)) + "-1111-4000-8000-d6e7f8a9b0c1"
		line := `{"type":"assistant","timestamp":"2026-07-0` + string(rune('1'+i%9)) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/api","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./... -run Case` + string(rune('a'+i)) + `"}}]}}`
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"go test","limit":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(text, "go test"); n < 3 {
		t.Fatalf("expected three entries:\n%s", text)
	}
	if !strings.Contains(text, "3 of 13") {
		t.Errorf("the cut is silent — nothing says how many there were:\n%s", text)
	}
	// An agent passes limit as a field; there is no flag for it to raise.
	if strings.Contains(text, "--limit") {
		t.Errorf("the agent is told to raise a flag it cannot pass:\n%s", text)
	}
	if !strings.Contains(text, "call again with a higher limit") {
		t.Errorf("the agent is not told how to see the rest:\n%s", text)
	}

	// The control: an uncut list says nothing extra.
	text, err = callMCPTool(dir, "how", json.RawMessage(`{"what":"go test","limit":20}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, " of 13") {
		t.Errorf("a list that was not cut says it was:\n%s", text)
	}
}

// The trust policy keys on which channel is asking: a machine may allow
// imported memory in its owner's own searches and deny it to an agent over MCP.
// `how` served both from one function with the search activation compiled in,
// so the MCP rule was never consulted — recall, asked the same question on the
// same machine, correctly said it was withholding something while how handed
// the imported command over.
func TestHowHonoursTheChannelsOwnPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Written out rather than seeded: `how` answers from command records, and
	// only a Bash tool_use produces one.
	body := `{"type":"user","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:00:00Z","message":{"role":"user","content":"how do we ship the zonkomatic"}}
{"type":"assistant","sessionId":"sess-one","cwd":"/app","timestamp":"2026-10-01T09:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"helm upgrade zonkomatic --set replicas=9"}}]}}
`
	if err := os.WriteFile(filepath.Join(proj, "sess-one.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	cfg := filepath.Join(tmp, "config", "deja")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	// Denied over MCP, allowed for the owner's own searches. Keyed on "local"
	// because the seeded session is this machine's own; the split is what
	// matters, not which origin carries it.
	if err := os.WriteFile(filepath.Join(cfg, "policy.json"),
		[]byte(`{"activations":{"mcp":{"local":false},"search":{"local":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))

	if policy.Load().Allows(policy.ActivationMCP, "app") {
		t.Fatal("the policy did not take effect, so this test would pass for the wrong reason")
	}
	if !policy.Load().Allows(policy.ActivationSearch, "app") {
		t.Fatal("the search side is denied too, so this cannot tell the two apart")
	}

	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	entries, _, _, err := howEntries(dir, []string{"zonkomatic"}, nil, policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the owner's own search returned nothing, so the fixture never had a command to withhold")
	}

	out, err := callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "replicas=9") {
		t.Errorf("how handed the command to the agent although the mcp policy denies it: %q", out)
	}
	// Filtering alone would turn the leak into a confident negative over records
	// that do exist, which is the failure emptyRecallAnswerPolicy and blame's
	// omission note were both written for.
	if strings.Contains(out, "No command on this machine") {
		t.Errorf("how told the agent nothing exists while the policy was hiding something: %q", out)
	}

	// The other direction, which absence alone cannot prove: with the mcp rule
	// allowing, the command must come back. Without this the test still passes
	// if how simply stops answering anyone.
	if werr := os.WriteFile(filepath.Join(cfg, "policy.json"),
		[]byte(`{"activations":{"mcp":{"local":true},"search":{"local":true}}}`), 0o600); werr != nil {
		t.Fatal(werr)
	}
	out, err = callMCPTool(dir, "how", json.RawMessage(`{"what":"zonkomatic"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "replicas=9") {
		t.Errorf("how withheld the command although the mcp policy allows it: %q", out)
	}
}

// `deja how` is the surface whose answer gets run, and it answered from the
// whole machine: asked inside one repository it returned another's wrapper —
// forty runs of `rtk go test ./...` above this repository's own command
// (#3705). The scope is the project of the working directory now.
func TestHowAnswersFromTheProjectItWasAskedIn(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two projects, and the busier one is not the one being asked about.
	writeClaudeFixture(t, filepath.Join(root, "-work-alpha", "a.jsonl"), "alpha-1", []string{
		`{"type":"user","sessionId":"alpha-1","cwd":"/work/alpha","timestamp":"2026-09-10T10:00:00Z",` +
			`"message":{"role":"user","content":"how do we run the tests"}}`,
		`{"type":"assistant","sessionId":"alpha-1","cwd":"/work/alpha","timestamp":"2026-09-10T10:00:01Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./cmd/... -count=1"}}]}}`,
	})
	for i, id := range []string{"beta-1", "beta-2", "beta-3"} {
		writeClaudeFixture(t, filepath.Join(root, "-work-beta", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/work/beta","timestamp":"2026-09-1` + string(rune('1'+i)) + `T10:00:00Z",` +
				`"message":{"role":"user","content":"run the tests"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/work/beta","timestamp":"2026-09-1` + string(rune('1'+i)) + `T10:00:01Z",` +
				`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"rtk go test ./..."}}]}}`,
		})
	}

	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	// Asked in alpha: alpha's command, though beta ran three times as many
	// sessions.
	entries, _, _, err := howEntries(dir, []string{"test"}, howScope("/work/alpha", "", false), policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no command matched in the project that ran one")
	}
	if !strings.Contains(entries[0].Command, "go test ./cmd/...") {
		t.Errorf("asked in alpha, answered %q", entries[0].Command)
	}
	for _, e := range entries {
		if strings.Contains(e.Command, "rtk") {
			t.Errorf("the other project's command reached an answer scoped to alpha: %q", e.Command)
		}
	}

	// The machine still answers when asked for it, and there the busier
	// project wins as before.
	wide, _, _, err := howEntries(dir, []string{"test"}, howScope("/work/alpha", "", true), policy.ActivationSearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(wide) == 0 || !strings.Contains(wide[0].Command, "rtk") {
		t.Errorf("--all-projects did not answer from the machine: %+v", wide)
	}
}

// A directory that belongs to no project deja knows still gets an answer: the
// scope is a default, not a filter the caller cannot see past.
func TestHowWidensWhenTheProjectHasNothing(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-work-beta", "b.jsonl"), "beta-1", []string{
		`{"type":"user","sessionId":"beta-1","cwd":"/work/beta","timestamp":"2026-09-11T10:00:00Z",` +
			`"message":{"role":"user","content":"run the tests"}}`,
		`{"type":"assistant","sessionId":"beta-1","cwd":"/work/beta","timestamp":"2026-09-11T10:00:01Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"rtk go test ./..."}}]}}`,
	})
	// A directory of its own, belonging to no project in the store.
	elsewhere := filepath.Join(os.Getenv("HOME"), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)
	out, err := captureRun(t, "how", "test")
	if err != nil {
		t.Fatalf("how: %v", err)
	}
	if !strings.Contains(out, "rtk go test") {
		t.Errorf("a directory with no project of its own got no answer:\n%s", out)
	}
	if !strings.Contains(out, "these are from other projects") {
		t.Errorf("the widening was not said:\n%s", out)
	}
}

// The scope line names the project the way the rows do. Claude's encoding
// reads a hyphen as a slash, so for ~/proj/my-app the footer said "in my/app
// only", a name no row shows and --project does not take (#5321).
func TestTheScopeLineNamesAHyphenatedProjectLikeTheRows(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	cwd := filepath.Join(tmp, "proj", "my-app")
	if err := os.MkdirAll(filepath.Join(cwd, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwdJSON, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := json.Marshal(filepath.Join(cwd, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2"} {
		writeClaudeFixture(t, filepath.Join(root, claudeEncodedDirForTest(cwd), id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":` + string(cwdJSON) + `,"timestamp":"2026-10-17T10:00:00Z","message":{"role":"user","content":"write the release notes"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":` + string(cwdJSON) + `,"timestamp":"2026-10-17T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Write","input":{"file_path":` + string(notes) + `,"content":"release"}}]}}`,
		})
	}
	if _, err = captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	if got := howScopeName(howScope(cwd, "", false)); got != "proj/my-app" {
		t.Errorf("scope name = %q, want proj/my-app", got)
	}
	out, err := captureRun(t, "files", "release")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "notes.txt") {
		t.Fatalf("the fixture no longer answers, so this pins nothing:\n%s", out)
	}
	if !strings.Contains(out, "in proj/my-app only") {
		t.Errorf("the footer does not name the project the rows do:\n%s", out)
	}
}

// claudeEncodedDirForTest is the directory name Claude Code keeps a
// project's transcripts under: the path with every separator, dot and colon
// turned into a dash.
func claudeEncodedDirForTest(dir string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ".", "-", ":", "-").Replace(dir)
}

// `deja how` exists to hand back a command to run, so the command it prints has
// to be the command that ran. SafeLine collapsed runs of whitespace, and
// `-run "Pool  Size"` came back as `-run "Pool Size"` — a different test filter
// (#2052).
func TestHowPrintsTheCommandThatRan(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	dir := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_INDEX_COMMANDS", "1")

	cmd := `go test ./... -run "Pool  Size"`
	rec := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := rec(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:05Z",
		"message":   map[string]any{"role": "user", "content": "the pgbouncer pool times out"},
	}) + "\n" + rec(map[string]any{
		"type": "assistant", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": "2026-01-02T03:04:06Z",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": cmd}},
		}},
	}) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := captureRun(t, "index"); err != nil {
		t.Fatalf("index: %v %s", err, out)
	}

	out, err := captureRun(t, "how", "test")
	if err != nil {
		t.Fatal(err)
	}
	// The premise: how found the command at all. A `grep` would not have been
	// indexed, and an empty answer would pass the check below for free.
	if !strings.Contains(out, "go test") {
		t.Fatalf("how found no command to print:\n%s", out)
	}
	if !strings.Contains(out, cmd) {
		t.Errorf("how printed a command that is not the one that ran:\n%s\nwanted: %s", out, cmd)
	}

	// A quoted phrase still finds it: the record keeps the spacing that ran, and
	// matching folds its own copy rather than the record's (#2052).
	quoted, err := captureRun(t, "how", "pool size")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(quoted, cmd) {
		t.Errorf("a quoted phrase no longer finds the command it names:\n%s", quoted)
	}

	// The same answer through MCP, which is the half an agent runs without a
	// person reading it first.
	got, err := callMCPTool(os.Getenv("DEJA_INDEX_DIR"), "how", json.RawMessage(`{"what":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "go test") {
		t.Fatalf("the MCP tool found no command to print:\n%s", got)
	}
	if !strings.Contains(got, cmd) {
		t.Errorf("the MCP tool handed back a command that is not the one that ran:\n%s\nwanted: %s", got, cmd)
	}
}

// Commands are kept out of ranking on purpose, which left the most reusable
// thing in a store — the invocation that actually works on this machine —
// unreachable. `how` is that channel, and it weighs a command by how many
// separate sessions ran it rather than by how often one session repeated it.
func TestHowRanksByHowManySessionsRanIt(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One session hammering the same command.
	writeClaudeFixture(t, filepath.Join(root, "p", "loop.jsonl"), "loop", []string{
		`{"type":"user","sessionId":"loop","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"run the suite"}}`,
		`{"type":"assistant","sessionId":"loop","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./internal/loop -run TestOne"}}]}}`,
		`{"type":"assistant","sessionId":"loop","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./internal/loop -run TestOne"}}]}}`,
		`{"type":"assistant","sessionId":"loop","timestamp":"2026-01-02T03:04:08Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"go test ./internal/loop -run TestOne"}}]}}`,
	})
	// Two separate sessions running the project's real invocation once each.
	for i, id := range []string{"a", "b"} {
		writeClaudeFixture(t, filepath.Join(root, "p", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-0` + string(rune('3'+i)) + `T03:04:05Z","message":{"role":"user","content":"run the suite"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-0` + string(rune('3'+i)) + `T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./... -count=1"}}]}}`,
		})
	}
	out, err := captureRun(t, "how", "go test")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if !strings.Contains(first, "-count=1") {
		t.Errorf("the command two sessions ran is not first:\n%s", out)
	}
	if !strings.Contains(out, "2 sessions") {
		t.Errorf("the session count is not reported:\n%s", out)
	}
}

func TestHowSaysNothingRatherThanGuessing(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello"}}`,
	})
	out, err := captureRun(t, "how", "terraform apply")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no command on this machine mentions") {
		t.Errorf("a store with no such command must say so, got:\n%s", out)
	}
}

func TestHowPrefersDirectWorkingInvocationsOverCompoundOrFailedOnes(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i, tc := range []struct {
		id  string
		cmd string
	}{
		{"s1", "git checkout -b lint/branch && golangci-lint run"},
		{"s2", "git checkout -b lint/branch && golangci-lint run"},
		{"s3", "golangci-lint run --broken-flag  \\u2192 exit 1"},
		{"s4", "golangci-lint run --broken-flag  \\u2192 exit 1"},
		{"s5", "golangci-lint run ./..."},
	} {
		writeClaudeFixture(t, filepath.Join(root, "p", tc.id+".jsonl"), tc.id, []string{
			`{"type":"user","sessionId":"` + tc.id + `","timestamp":"2026-01-0` + string(rune('2'+i)) + `T03:04:05Z","message":{"role":"user","content":"lint"}}`,
			`{"type":"assistant","sessionId":"` + tc.id + `","timestamp":"2026-01-0` + string(rune('2'+i)) + `T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"` + tc.cmd + `"}}]}}`,
		})
	}
	out, err := captureRun(t, "how", "golangci-lint")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if !strings.Contains(first, "golangci-lint run ./...") {
		t.Fatalf("direct working invocation should rank first, got:\n%s", out)
	}
}
