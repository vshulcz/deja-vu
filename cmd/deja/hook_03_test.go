package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDigestBudgetSpentCountsOnlyFullDigests(t *testing.T) {
	seen := map[string]bool{"sess-a": true, "q:abc": true}
	for i := range digestsPerSession - 1 {
		seen[digestSeenPrefix+strconv.Itoa(i)] = true
	}
	if digestBudgetSpent(seen, "agent") {
		t.Fatal("budget spent before the session had its full digests")
	}
	seen[digestSeenPrefix+"last"] = true
	if !digestBudgetSpent(seen, "agent") {
		t.Fatal("budget not spent after the session had its full digests")
	}
	if digestBudgetSpent(seen, spawnReaderPrefix+"agent:x") {
		t.Error("a spawned agent counted the parent's digests")
	}
	if digestBudgetSpent(seen, "") {
		t.Error("a payload with no session id was cut")
	}
}

// Past the budget a long session gets the pointer line instead of the quotes,
// a spawned agent still gets the quotes, and a compaction resets the count.
func TestHookPromptShrinksToPointerPastTheDigestBudget(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "budgetterm", []string{
		`{"type":"user","sessionId":"budgetterm","timestamp":"` + old +
			`","message":{"role":"user","content":"set up the connection pooler for the api"}}`,
		`{"type":"assistant","sessionId":"budgetterm","timestamp":"` + old +
			`","message":{"role":"assistant","content":[{"type":"text","text":"pgbouncer runs in transaction mode and prepared statements are off"}]}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "two.jsonl"), "askedterm", []string{
		`{"type":"user","sessionId":"askedterm","timestamp":"` + old +
			`","message":{"role":"user","content":"why does rsync hang in the nightly deploy script"}}`,
		`{"type":"assistant","sessionId":"askedterm","timestamp":"` + old +
			`","message":{"role":"assistant","content":[{"type":"text","text":"rsync in the nightly deploy waited on a host key prompt, added StrictHostKeyChecking"}]}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	// Each case starts from an empty seen list, so the project cooldown left by
	// the case before does not hide the session.
	spend := func(sid string) {
		t.Helper()
		if err := os.Remove(index.DefaultDir() + ".hookseen"); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		var b strings.Builder
		for i := range digestsPerSession {
			b.WriteString(hookseenKey(sid) + " " + digestSeenPrefix + "f" + strconv.Itoa(i) + "\n")
		}
		f, err := os.OpenFile(index.DefaultDir()+".hookseen", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(b.String()); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	const pooling = "pgbouncer prepared statements in transaction pooling"
	askAbout := func(sid, prompt string) string {
		t.Helper()
		payload := `{"prompt":"` + prompt + `","session_id":"` + sid + `"}`
		var out bytes.Buffer
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	ask := func(sid string) string { t.Helper(); return askAbout(sid, pooling) }

	if got := ask("fresh-1"); !strings.Contains(got, "transaction mode") {
		t.Fatalf("a fresh session did not get the full digest:\n%q", got)
	}
	requireDigestRow(t, "fresh-1")

	spend("long-1")
	got := ask("long-1")
	if !strings.Contains(got, "deja: this project has history on") {
		t.Fatalf("a session past its budget did not get the pointer:\n%q", got)
	}
	if strings.Contains(got, "- **") {
		t.Errorf("a session past its budget still got session entries:\n%q", got)
	}

	// A question asked before is the strong claim and keeps its full block.
	spend("long-2")
	if got2 := askAbout("long-2", "why does rsync hang in the nightly deploy script"); !strings.Contains(got2, "StrictHostKeyChecking") {
		t.Errorf("a repeated question past the budget lost its full block:\n%q", got2)
	}

	// A spawned agent gets the pointer from its first block, and the full one
	// only when its instructions repeat a question asked before.
	spend("none")
	reader := spawnReaderPrefix + "parent-1:abc"
	got = ask(reader)
	if !strings.Contains(got, "deja: this project has history on") || strings.Contains(got, "- **") {
		t.Errorf("a spawned agent did not get the pointer:\n%q", got)
	}
	spend("none")
	if got := askAbout(spawnReaderPrefix+"parent-1:def", "why does rsync hang in the nightly deploy script"); !strings.Contains(got, "StrictHostKeyChecking") {
		t.Errorf("a spawned agent repeating a question lost its full block:\n%q", got)
	}

	spend("compact-1")
	forgetInjected(index.DefaultDir(), "compact-1")
	if got := ask("compact-1"); !strings.Contains(got, "- **") {
		t.Errorf("after compaction the session did not get a full digest again:\n%q", got)
	}
}

// requireDigestRow fails unless the hook recorded a full digest for sid.
func requireDigestRow(t *testing.T, sid string) {
	t.Helper()
	for id := range alreadyInjected(index.DefaultDir(), sid) {
		if strings.HasPrefix(id, digestSeenPrefix) {
			return
		}
	}
	t.Fatalf("no digest row for %s", sid)
}

// A session too long to read as one episode is narrowed to the part that
// matched, not dropped and not injected whole. Turning the narrowing off left
// the whole suite green: the only test that watches focusCalls wants it at
// zero, which a disabled narrowing satisfies for the wrong reason.
func TestHookPromptNarrowsAMarathonItShows(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	lines := make([]string, 0, dejaVuMaxMessages+3)
	for i := range dejaVuMaxMessages + 1 {
		lines = append(lines, fmt.Sprintf(
			`{"type":"assistant","sessionId":"marathon","timestamp":%q,"message":{"role":"assistant","content":"routine step %d, nothing to see"}}`,
			ts, i))
	}
	lines = append(lines, `{"type":"assistant","sessionId":"marathon","timestamp":"`+ts+
		`","message":{"role":"assistant","content":"the fix: kestrel retries are capped at four"}}`)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "marathon.jsonl"), "marathon", lines)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	focusCalls.Store(0)
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel retries","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "capped at four") {
		t.Fatalf("the marathon's answer was not recalled:\n%q", out.String())
	}
	if focusCalls.Load() != 1 {
		t.Errorf("narrowed %d session(s); a marathon that gets shown must be narrowed first", focusCalls.Load())
	}
	// Only the quoted lines matter: the credit line names the session by its
	// opening message, which is noise by construction here.
	for ln := range strings.SplitSeq(out.String(), "\n") {
		quoted := strings.TrimSpace(ln)
		if !strings.HasPrefix(quoted, "- User:") && !strings.HasPrefix(quoted, "- Assistant:") {
			continue
		}
		if strings.Contains(quoted, "routine step") {
			t.Errorf("the block quotes the marathon's noise:\n%q", quoted)
		}
	}
}

// One ordinary word is a hint, not an answer: it earns a pointer, and the
// pointer is what keeps a whole digest off every message. Making the payload
// unconditional — never a pointer — left the suite green.
func TestHookPromptPointsRatherThanQuotesOnOneOrdinaryWord(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	// Two sessions so the matched word is ordinary rather than rare, and the
	// question shares exactly that one word with them.
	for i, name := range []string{"one", "two"} {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", name+".jsonl"), name, []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":"the migration ran fine on shard %d"}}`, name, ts, i),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":"migration %d finished, nothing else changed"}}`, name, ts, i),
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"is the migration something we should worry about","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "deja-recall") {
		t.Skip("nothing was recalled at all, so this says nothing about the payload rule")
	}
	if strings.Contains(got, "- User:") || strings.Contains(got, "- Assistant:") {
		t.Errorf("one ordinary word bought a full digest:\n%q", got)
	}
	if !strings.Contains(got, "call recall") {
		t.Errorf("the pointer text is missing:\n%q", got)
	}
}

// A new session that opens by asking to carry on another one gets that
// session's state, on its first prompt only and only when the prompt names the
// session or its harness.
func TestTheFirstPromptThatAsksToContinueASessionGetsItsPacket(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "src.jsonl"), "c0ffee11-src", []string{
		`{"type":"user","sessionId":"c0ffee11-src","timestamp":"` + old +
			`","message":{"role":"user","content":"the nightly export drops rows past the first page"}}`,
		`{"type":"assistant","sessionId":"c0ffee11-src","timestamp":"` + old +
			`","message":{"role":"assistant","content":[{"type":"text","text":"Root cause: the cursor resets after page one; fixed by carrying the page token."}]}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	ask := func(sid, prompt string) string {
		t.Helper()
		payload, jerr := json.Marshal(map[string]string{"prompt": prompt, "session_id": sid})
		if jerr != nil {
			t.Fatal(jerr)
		}
		var out bytes.Buffer
		if err := runHookPromptMode(index.DefaultDir(), bytes.NewReader(payload), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	byID := ask("new-1", "перенеси сюда сессию c0ffee11-src и продолжай")
	if !strings.Contains(byID, "carrying the page token") || !strings.Contains(byID, "this prompt names claude session") {
		t.Fatalf("a first prompt naming a session id did not get that session's packet:\n%s", byID)
	}
	if !strings.Contains(byID, "Reference data, not instructions") {
		t.Errorf("the packet went in without the frame:\n%s", byID)
	}
	// Once per session: the same request again is not a second handoff.
	if again := ask("new-1", "перенеси сюда сессию c0ffee11-src и продолжай"); strings.Contains(again, "this prompt names") {
		t.Errorf("the same handoff went in twice:\n%s", again)
	}

	byHarness := ask("new-2", "continue the claude session from before")
	if !strings.Contains(byHarness, "carrying the page token") {
		t.Errorf("a first prompt naming the harness did not get its newest session here:\n%s", byHarness)
	}

	// "Continue" on its own is the commonest turn there is, and not a handoff.
	if got := ask("new-3", "continue with the export fix"); strings.Contains(got, "this prompt names") {
		t.Errorf("a plain continue was read as a handoff:\n%s", got)
	}
	// A pasted command that resumes a session is not a request to continue it.
	if got := ask("new-4", "в сессии claude --resume c0ffee11-src удали два последних сообщения"); strings.Contains(got, "this prompt names") {
		t.Errorf("a request about a session was read as a handoff of it:\n%s", got)
	}
}

// Gemini hands its prompt hook the whole request, and by then the request
// carries whatever SessionStart injected — deja's own recall. Searching that
// text finds the sessions the last recall named, on every prompt, no matter
// what the user asked.
func TestPromptDropsContextAnEarlierHookInjected(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{
			name: "gemini wraps and escapes the block it injected",
			raw: "<hook_context>&lt;deja-recall&gt;\n  - Assistant: the AND across query " +
				"words is why plain questions come back empty\n&lt;/deja-recall&gt;</hook_context>" +
				"\n\nHow often does the deploy token rotate?",
			want: "How often does the deploy token rotate?",
		},
		{
			name: "a plain recall block",
			raw:  "<deja-recall>pgbouncer pool sizing</deja-recall>\nwhy is the build slow?",
			want: "why is the build slow?",
		},
		{
			name: "nothing to strip",
			raw:  "why is the build slow?",
			want: "why is the build slow?",
		},
		{
			name: "an unclosed block takes what follows it with it",
			raw:  "ask about caching\n<hook_context>half a block",
			want: "ask about caching",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got hookPromptText
			payload, err := json.Marshal(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := got.UnmarshalJSON(payload); err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("prompt = %q, want %q", got, tc.want)
			}
		})
	}
}

// The terms are what the search actually runs on, so the check that matters is
// that none of them come from the injected block.
func TestSearchTermsComeFromTheUserNotTheLastRecall(t *testing.T) {
	raw := "<hook_context>&lt;deja-recall&gt;\n  - User: have a look at internal/index/retrieval.go\n" +
		"  - Assistant: the AND across query words is why plain questions come back empty\n" +
		"&lt;/deja-recall&gt;</hook_context>\n\nHow often does the pgbouncer certificate rotate?"
	var typed hookPromptText
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := typed.UnmarshalJSON(payload); err != nil {
		t.Fatal(err)
	}
	terms := prompt.Terms(string(typed))
	if len(terms) == 0 {
		t.Fatal("the user's own question produced no search terms")
	}
	for _, term := range terms {
		if strings.Contains("retrieval.go internal/index deja-recall hook_context", term) { //nolint:gocritic // asks whether term is one of these words
			t.Errorf("term %q came from the block a previous hook injected, not "+
				"from the question: %v", term, terms)
		}
	}
	var found bool
	for _, term := range terms {
		if term == "pgbouncer" {
			found = true
		}
	}
	if !found {
		t.Errorf("the word that identifies the question is missing: %v", terms)
	}
}

// The injection log records what went out; without who received it, the only
// way to tell whether a recall was used is the sentence the block asks the
// agent to say — In real-world stores, that follows 22 of 1218 injections.
// The hook knows the agent session from its own payload, so it writes it down.
func TestHookPromptRecordsTheSessionItAnswered(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "intoterm", []string{
		`{"type":"user","sessionId":"intoterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"ses_from_harness"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "transaction mode") {
		t.Fatalf("the hook said nothing, so there is no injection to record:\n%q", out.String())
	}

	b, err := os.ReadFile(usage.SnapshotPath(index.DefaultDir()))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var snap usage.Snapshot
		if json.Unmarshal([]byte(line), &snap) != nil {
			continue
		}
		if snap.Kind == usage.KindDejaVu && snap.Into == "ses_from_harness" {
			found = true
		}
	}
	if !found {
		t.Errorf("the injection log does not say which agent session got the block:\n%s", b)
	}
}

// The lead is 301 bytes of a 1439-byte block, a session receives a median of 23
// blocks, and the lead's last sentence repeats the line every block ends with.
// Said once, the other 22 blocks stop carrying 6.6 KB of the same sentences.
func TestTheBlockExplainsItselfOncePerSession(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "leadfix.jsonl"), "leadfix", []string{
		`{"type":"user","sessionId":"leadfix","timestamp":"` + old + `","message":{"role":"user","content":"the quokkabloom exporter drops rows at utc_midnight"}}`,
		`{"type":"assistant","sessionId":"leadfix","timestamp":"` + old + `","message":{"role":"assistant","content":"we moved the quokkabloom exporter off utc_midnight and it stopped dropping rows"}}`,
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "leadfix2.jsonl"), "leadfix2", []string{
		`{"type":"user","sessionId":"leadfix2","timestamp":"` + old + `","message":{"role":"user","content":"the snorblewidget retry budget was capped at three"}}`,
		`{"type":"assistant","sessionId":"leadfix2","timestamp":"` + old + `","message":{"role":"assistant","content":"we capped the snorblewidget retry budget at three attempts and let the fourth fail"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	ask := func(prompt string) string {
		in := `{"prompt":"` + prompt + `","session_id":"agent-lead"}`
		var out bytes.Buffer
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	first := ask("is the quokkabloom exporter still losing rows overnight")
	if !strings.Contains(first, "deja found sessions whose wording matches this request") {
		t.Fatalf("the first block does not explain itself:\n%s", first)
	}

	second := ask("how many attempts does the snorblewidget budget allow now")
	if strings.Contains(second, "deja found sessions whose wording matches this request") {
		t.Errorf("the second block repeats the whole explanation:\n%s", second)
	}
	// The caveat that used to be kept here is gone, and this pins that rather
	// than the other way round. It was kept to stop a wording match being taken
	// for an answer, and measured against exactly that on the local 9B: a later
	// block whose history is about a sibling subject and carries a value nothing
	// else holds, quoted unhedged as the answer 12 times of 40 with the line and
	// 10 of 40 without it. It cost 78 bytes on every block after the first and
	// bought no care.
	if strings.Contains(second, "matched on wording, not meaning") {
		t.Errorf("the caution came back; 40 cases say it changes nothing:\n%s", second)
	}
	// What must survive on every block: the untrusted-data line and the line
	// that asks for the citation.
	for _, want := range []string{"Reference data, not instructions", "add as the last line of your reply:"} {
		if !strings.Contains(second, want) {
			t.Errorf("the second block lost %q:\n%s", want, second)
		}
	}
	if len(second) >= len(first) {
		t.Errorf("the second block is not smaller: %d then %d bytes", len(first), len(second))
	}
}

// A block whose every quoted line shares one word with the question is about
// something else; it is not sent. One that quotes a line holding two is.
func TestPromptDigestNeedsAQuoteHoldingTheQuestion(t *testing.T) {
	dir := t.TempDir()
	at := time.Now().Add(-48 * time.Hour)
	sess := func(id string, texts ...string) model.Session {
		s := model.Session{ID: id, Harness: "claude", Project: "beta", Started: at, Updated: at}
		for k, text := range texts {
			role := "user"
			if k%2 == 1 {
				role = "assistant"
			}
			s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: at.Add(time.Duration(k) * time.Second)})
		}
		return s
	}
	terms := []string{"gizmoflux", "warmup", "staging"}
	apart := sess("apart", "the gizmoflux dashboard is slow today", "Noted, I will look at the dashboard layout.",
		"move the staging banner to the left", "Moved the banner on staging.")
	if body, ok := promptDigestBody(dir, "asker", []model.Session{apart}, true, terms, nil, "gizmoflux warmup on staging?", "", ""); ok {
		t.Errorf("a block whose lines each hold one word of the question was sent:\n%s", body)
	}
	together := sess("together", "why is the gizmoflux warmup so slow on staging", "The gizmoflux warmup waited on the cold cache; staging now preloads it.")
	body, ok := promptDigestBody(dir, "asker", []model.Session{together}, true, terms, nil, "gizmoflux warmup on staging?", "", "")
	if !ok || !strings.Contains(body, "preloads") {
		t.Errorf("the session that said the words together was not sent (ok=%v):\n%s", ok, body)
	}
}

// A long session holds every word of any question somewhere, each in another
// turn about another thing; that is not a match. The session that said the
// words together is.
func TestPromptHookWantsTheLeadWordsTogether(t *testing.T) {
	withStatsStores(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC()
	line := func(id, role, text string, k int) string {
		return jsonLine(t, map[string]any{"type": role, "sessionId": id,
			"timestamp": ts.Add(time.Duration(k) * time.Second).Format(time.RFC3339),
			"message":   map[string]any{"role": role, "content": text}})
	}
	// A marathon: each word of the question once, far apart.
	long := make([]string, 0, 340)
	for k := range 340 {
		text := fmt.Sprintf("шаг %d: правлю вёрстку карточки", k)
		switch k {
		case 40:
			text = "сегодня смотрим расписание электричек"
		case 200:
			text = "потом разберём загрузку"
		case 250:
			text = "и фотографии на главной"
		case 300:
			text = "и ещё про сжатие архивов"
		}
		role := "user"
		if k%2 == 1 {
			role = "assistant"
		}
		long = append(long, line("marathon", role, text, k))
	}
	writeClaudeFixture(t, filepath.Join(root, "beta", "marathon.jsonl"), "marathon", long)
	writeClaudeFixture(t, filepath.Join(root, "beta", "focused.jsonl"), "focused", []string{
		line("focused", "user", "почему падает загрузка фотографий после сжатия?", 0),
		line("focused", "assistant", "Загрузка фотографий падала: сжатие отдавало пустой файл, поправил порядок шагов.", 1),
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	ask := func(prompt string) string {
		var out bytes.Buffer
		in, err := json.Marshal(map[string]string{"prompt": prompt, "session_id": "asking-" + prompt[:6]})
		if err != nil {
			t.Fatal(err)
		}
		if err := runHookPromptMode(index.DefaultDir(), bytes.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	got := ask("снова сломалась загрузка фотографий, глянь сжатие")
	if strings.Contains(got, "marathon") || strings.Contains(got, "электрич") {
		t.Errorf("the marathon that holds the words apart was recalled:\n%s", got)
	}
	if !strings.Contains(got, "пустой файл") {
		t.Errorf("the session that said them together was not recalled:\n%s", got)
	}
	// Words of the question met only in the marathon, never together.
	if got := ask("а электрички и архивы как там?"); strings.Contains(got, "marathon") {
		t.Errorf("words met only far apart were taken as a match:\n%s", got)
	}
}

// padStoreWithOtherWork writes sessions about something else entirely into
// another project, about 150 counted messages, so a word said once or twice
// is rare by the fixed floor (search.IdentifyingTerms) and not only by the
// ceiling a small store sets. For tests about rarity on a store of real size.
func padStoreWithOtherWork(t *testing.T, claudeRoot string) {
	t.Helper()
	at := time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)
	for s := range 6 {
		id := fmt.Sprintf("rota%d", s)
		lines := make([]string, 0, 26)
		for k := range 26 {
			role := "user"
			if k%2 == 1 {
				role = "assistant"
			}
			lines = append(lines, jsonLine(t, map[string]any{"type": role, "sessionId": id,
				"timestamp": at.Add(time.Duration(s*100+k) * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": role, "content": fmt.Sprintf("kitchen rota week %d: soup on monday, bread on friday", k)}}))
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-gamma", id+".jsonl"), id, lines)
	}
}

// askOneWordInSmallStore indexes the given sessions, each a user line and an
// agent line, and asks the prompt hook the one word "zarvoxd".
func askOneWordInSmallStore(t *testing.T, sessions map[string][2]string) string {
	t.Helper()
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC()
	for id, said := range sessions {
		lines := make([]string, 0, 2)
		for k, role := range []string{"user", "assistant"} {
			lines = append(lines, jsonLine(t, map[string]any{"type": role, "sessionId": id,
				"timestamp": at.Add(time.Duration(k) * time.Second).Format(time.RFC3339),
				"message":   map[string]any{"role": role, "content": said[k]}}))
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-delta", id+".jsonl"), id, lines)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "delta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(`{"prompt":"zarvoxd"}`), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A new user's store is a session or two, which can never give a word the
// fixed rareness floor. A word only one session says is as rare as that store
// allows, and a one-word question about it still gets the memory.
func TestASmallStoreStillAnswersAOneWordQuestion(t *testing.T) {
	answer := [2]string{"the zarvoxd cache keeps going stale", "zarvoxd now refreshes its cache every minute"}
	other := [2]string{"move the footer links to the left", "moved them and kept the spacing"}
	for name, store := range map[string]map[string][2]string{
		"one session":  {"zv": answer},
		"two sessions": {"zv": answer, "footer": other},
	} {
		t.Run(name, func(t *testing.T) {
			got := askOneWordInSmallStore(t, store)
			if !strings.Contains(got, "refreshes its cache") {
				t.Errorf("a one-word question on a small store got no memory:\n%s", got)
			}
			// And the ranking gives that word at least the ceiling the floor
			// is taken from, whatever the message counts.
			_, _, _, _, idf, err := index.ProjectRelevantNamingCut(index.DefaultDir(), nil, []string{"zarvoxd"}, 5, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if top := index.TopIDF(index.DefaultDir()); idf["zarvoxd"] < top {
				t.Errorf("idf of a one-session word = %v, under TopIDF = %v", idf["zarvoxd"], top)
			}
		})
	}
}

// The ceiling is not a pass for every word: one that every session says names
// none of them.
func TestAWordEverySessionSaysNamesNothing(t *testing.T) {
	got := askOneWordInSmallStore(t, map[string][2]string{
		"zv1": {"the zarvoxd cache keeps going stale", "zarvoxd now refreshes its cache every minute"},
		"zv2": {"zarvoxd logs are too noisy", "turned zarvoxd logging down to warnings"},
	})
	if strings.Contains(got, "zarvoxd") {
		t.Errorf("a word every session says was taken as naming one of them:\n%s", got)
	}
}

func TestPromptHookUnscopedCwdRequiresNamedSubject(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC()
	lines := []string{
		jsonLine(t, map[string]any{"type": "user", "sessionId": "other1",
			"timestamp": at.Format(time.RFC3339),
			"message":   map[string]any{"role": "user", "content": "почему падает загрузка фотографий в zarvoxd после сжатия?"}}),
		jsonLine(t, map[string]any{"type": "assistant", "sessionId": "other1",
			"timestamp": at.Add(time.Second).Format(time.RFC3339),
			"message":   map[string]any{"role": "assistant", "content": "Загрузка фотографий падала: сжатие отдавало пустой файл в zarvoxd."}}),
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-otherproj", "other1.jsonl"), "other1", lines)
	padStoreWithOtherWork(t, claudeRoot)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	var out bytes.Buffer
	in := jsonLine(t, map[string]string{"prompt": "снова сломалась загрузка фотографий, глянь сжатие", "cwd": home, "session_id": "ask-1"})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "пустой файл") {
		t.Fatalf("unscoped cwd injected unrelated project on generic words:\n%s", out.String())
	}

	out.Reset()
	inNamed := jsonLine(t, map[string]string{"prompt": "что там было с zarvoxd и сжатие?", "cwd": home, "session_id": "ask-2"})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(inNamed), &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "zarvoxd") {
		t.Fatalf("unscoped cwd with named subject failed to recall:\n%s", out.String())
	}
}

func TestPromptHookAllLatinMultiClauseAndDistinctDateOverflow(t *testing.T) {
	t.Run("all-latin multi-clause recall", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		padStoreWithOtherWork(t, claudeRoot)

		cwd := filepath.Join(t.TempDir(), "tmp", "alpha")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		writeSession := func(id, userText, assistantText string, offsetDays int) {
			at := now.AddDate(0, 0, offsetDays)
			lines := []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": userText},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": id, "cwd": cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": assistantText},
				}),
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", id+".jsonl"), id, lines)
		}

		writeSession("major-1",
			"I switched my academic major to astrophysics after advisor meeting",
			"Recorded your academic major switch to astrophysics.", -6)
		writeSession("planetarium-1",
			"I visited the downtown planetarium with astronomy club",
			"Logged your downtown planetarium visit.", -4)

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "what academic major did I switch to after advisor meeting or downtown planetarium visit?",
			"cwd":        cwd,
			"session_id": "ask-latin-1",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "astrophysics") {
			t.Fatalf("all-latin multi-clause query missed astrophysics session:\n%s", out.String())
		}
	})

	t.Run("distinct-date third slot and overflow hint", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		padStoreWithOtherWork(t, claudeRoot)

		cwd := filepath.Join(t.TempDir(), "tmp", "alpha")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		days := []int{-40, -25, -10, -2}
		markers := []string{"milestone_q1", "milestone_q2", "milestone_q3", "milestone_q4"}
		details := []string{
			"covering storage compaction and snapshot recovery.",
			"focusing on federation routing and quota telemetry.",
			"delivering webhook replay and audit exports.",
			"completing canary rollback and chaos drills.",
		}
		for idx, offset := range days {
			id := fmt.Sprintf("mile-%d", idx+1)
			at := now.AddDate(0, 0, offset)
			lines := []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": "what is our roadmap milestone target for this quarter?"},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": id, "cwd": cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": "The roadmap milestone target is " + markers[idx] + " " + details[idx]},
				}),
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", id+".jsonl"), id, lines)
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "what is our roadmap milestone target?",
			"cwd":        cwd,
			"session_id": "ask-dates-1",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		found := 0
		for _, m := range markers {
			if strings.Contains(got, m) {
				found++
			}
		}
		if found < 3 {
			t.Fatalf("expected 3 distinct-date milestone sessions in digest, got %d:\n%s", found, got)
		}
		if !strings.Contains(got, "+1 more matching sessions — run deja recall to check all") {
			t.Fatalf("expected overflow hint line for 4th matching session, got:\n%s", got)
		}
	})

	t.Run("verbose first session shares budget across distinct dates", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		padStoreWithOtherWork(t, claudeRoot)

		cwd := filepath.Join(t.TempDir(), "tmp", "beta")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		longBody := "Currently two backup keys remain active after rotating BK_FIRST_VERBOSE. " +
			"We audited every node in the ring and verified BK_FIRST_VERBOSE checksums match. " +
			strings.Repeat("The backup key verification audit completed with clean ledger status for all active shards. ", 9)
		specs := []struct {
			id     string
			offset int
			body   string
		}{
			{"bk-1", -30, longBody},
			{"bk-2", -15, "Currently three backup keys remain active after enrolling BK_SECOND_UPDATE."},
			{"bk-3", -2, "Currently four backup keys remain active after provisioning BK_THIRD_LATEST."},
		}
		for _, sp := range specs {
			at := now.AddDate(0, 0, sp.offset)
			lines := []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": "how many backup keys are active now?"},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.body},
				}),
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-beta", sp.id+".jsonl"), sp.id, lines)
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "how many backup keys are active now?",
			"cwd":        cwd,
			"session_id": "ask-bk-1",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		for _, want := range []string{"BK_FIRST_VERBOSE", "BK_SECOND_UPDATE", "BK_THIRD_LATEST"} {
			if !strings.Contains(got, want) {
				t.Fatalf("expected %s to share digest budget without starvation:\n%s", want, got)
			}
		}
	})
}

func encodeClaudeTestProjectDir(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	if len(s) > 200 {
		return s[:200] + "-test"
	}
	return s
}

func seedMediumFillerSessions(t *testing.T, claudeRoot string, count int) {
	t.Helper()
	now := time.Now().UTC()
	for i := range count {
		id := fmt.Sprintf("med-pad-%02d", i)
		at := now.AddDate(0, 0, -(40 + i))
		cwd := fmt.Sprintf("/tmp/medpad-%02d", i)
		proj := encodeClaudeTestProjectDir(cwd)
		lines := []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": fmt.Sprintf("inspect routine shard %d latency and disk metrics", i)},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": cwd,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Checked routine shard %d status; latency within normal bounds.", i)},
			}),
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, proj, id+".jsonl"), id, lines)
	}
}

func TestPromptHookMediumStoreMultiSessionSubjectUpdate(t *testing.T) {
	t.Run("generic modifier does not block multi-session subject", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 32)
		now := time.Now().UTC()

		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"sc-1", -20, "/tmp/life-1", "Currently I own three cargo scooters parked in the shed.", "Noted your three cargo scooters."},
			{"sc-2", -5, "/tmp/life-2", "I added a folding touring scooter, so with my road scooter I have four scooters total.", "Updated count: four scooters."},
			{"sc-noise", -2, "/tmp/life-3", "I own my own notes and own schedule across own projects every own day.", "Tracked your own schedule notes."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "How many scooters do I currently own?",
			"cwd":        home,
			"session_id": "ask-scooters",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "three cargo") || !strings.Contains(got, "four scooters") {
			t.Fatalf("expected both scooter sessions in digest, got:\n%s", got)
		}
		if strings.Contains(got, "schedule notes") {
			t.Fatalf("expected repetitive 'own' noise session excluded, got:\n%s", got)
		}
	})

	t.Run("subject update matched via speech inflection", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 26)
		now := time.Now().UTC()

		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"mir-1", -18, "/tmp/life-1", "My friend Miriam decided to move so she could live in Denver for her studio job.", "Saved Miriam's Denver move."},
			{"mir-2", -3, "/tmp/life-2", "My friend Miriam actually just moved back to the highlands again.", "Got it, Miriam is back in the highlands."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)
		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "Where did Miriam move to live?",
			"cwd":        home,
			"session_id": "ask-miriam",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "Denver") || !strings.Contains(got, "highlands") {
			t.Fatalf("expected both Miriam relocation sessions in digest, got:\n%s", got)
		}
	})
}

func TestPromptHookWhenClausePairedAndAbsentSubject(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	seedMediumFillerSessions(t, claudeRoot, 30)
	now := time.Now().UTC()

	for _, sp := range []struct {
		id     string
		offset int
		cwd    string
		user   string
		asst   string
	}{
		{"wh-1", -25, "/tmp/life-a", "I attended the ceramic workshop at the pottery studio today.", "Logged WORKSHOP_MAR_10 ceramic studio notes."},
		{"wh-2", -5, "/tmp/life-b", "I glazed a stoneware bowl as a gift for my cousin tonight.", "Logged GIFT_APR_02 stoneware bowl gift."},
	} {
		at := now.AddDate(0, 0, sp.offset)
		proj := encodeClaudeTestProjectDir(sp.cwd)
		writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": sp.user},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": sp.asst},
			}),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	var pairedOut bytes.Buffer
	pairedIn := jsonLine(t, map[string]string{
		"prompt":     "How many days passed between when I attended the ceramic workshop at the local pottery studio and when I glazed a stoneware bowl for my cousin?",
		"cwd":        home,
		"session_id": "ask-when-paired",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(pairedIn), &pairedOut, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pairedOut.String(), "WORKSHOP_MAR_10") || !strings.Contains(pairedOut.String(), "GIFT_APR_02") {
		t.Fatalf("expected both when-clause event sessions in digest, got:\n%s", pairedOut.String())
	}

	var absentOut bytes.Buffer
	absentIn := jsonLine(t, map[string]string{
		"prompt":     "How many days passed when the quasar rotator ran beside the workshop?",
		"cwd":        home,
		"session_id": "ask-when-absent",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(absentIn), &absentOut, true); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(absentOut.String()) != "" {
		t.Fatalf("expected absent-subject when-query to suppress injection, got:\n%s", absentOut.String())
	}
}

func TestPromptHookScopedRareSubjectDistinctDateAndOrQuery(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	projDir := filepath.Join(t.TempDir(), "notebook")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projSlug := encodeClaudeTestProjectDir(projDir)
	now := time.Now().UTC()

	for i := range 112 {
		id := fmt.Sprintf("pad-%03d", i)
		at := now.AddDate(0, 0, -(40 + i))
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, id+".jsonl"), id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": fmt.Sprintf("inspect routine shard %d latency and disk metrics", i)},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Checked routine shard %d status; latency within normal bounds.", i)},
			}),
		})
	}

	for _, sp := range []struct {
		id     string
		offset int
		user   string
		asst   string
	}{
		{"r-1", -20, "Rachel is planning a relocation to Chicago next month.", "Got it, noted Rachel's upcoming relocation to Chicago."},
		{"r-2", -3, "Rachel actually just moved back to the suburbs this week.", "Updated: Rachel moved back to the suburbs."},
		{"p-1", -15, "I have led the gateway project for two quarters.", "Recorded the gateway project you led."},
		{"p-2", -4, "Currently leading the billing project migration too.", "Recorded the billing project you are currently leading."},
	} {
		at := now.AddDate(0, 0, sp.offset)
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, sp.id+".jsonl"), sp.id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": sp.user},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": sp.asst},
			}),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projDir)

	var relocOut bytes.Buffer
	relocIn := jsonLine(t, map[string]string{
		"prompt":     "Where did Rachel move to after her recent relocation?",
		"cwd":        projDir,
		"session_id": "ask-relocation",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(relocIn), &relocOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Chicago", "suburbs"} {
		if !strings.Contains(relocOut.String(), want) {
			t.Fatalf("expected %q in relocation digest, got:\n%s", want, relocOut.String())
		}
	}

	var projOut bytes.Buffer
	projIn := jsonLine(t, map[string]string{
		"prompt":     "How many projects have I led or am currently leading?",
		"cwd":        projDir,
		"session_id": "ask-projects",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(projIn), &projOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gateway", "billing"} {
		if !strings.Contains(projOut.String(), want) {
			t.Fatalf("expected %q in projects digest, got:\n%s", want, projOut.String())
		}
	}
}

func TestPromptHookMediumStoreGenericModifierAndSingleRareSubject(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	projDir := filepath.Join(t.TempDir(), "notebook")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projSlug := encodeClaudeTestProjectDir(projDir)
	now := time.Now().UTC()

	for i := range 96 {
		id := fmt.Sprintf("pad-%03d", i)
		at := now.AddDate(0, 0, -(40 + i))
		text := fmt.Sprintf("inspect routine shard %d latency and own metrics", i)
		switch {
		case i < 16:
			text = fmt.Sprintf("inspect model variant %d latency and own metrics", i)
		case i < 19:
			text = fmt.Sprintf("recently worked and bought spare cables for shard %d", i)
		case i < 22:
			text = fmt.Sprintf("currently reviewing shard %d ownership metrics", i)
		}
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, id+".jsonl"), id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": text},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Recorded shard %d metrics.", i)},
			}),
		})
	}

	for _, sp := range []struct {
		id     string
		offset int
		user   string
		asst   string
	}{
		{"b-1", -18, "I currently own three bikes in the shed.", "Noted three bikes currently in the shed."},
		{"b-2", -3, "I will have four bikes now after fixing the frame.", "Got it, four bikes total now."},
		{"m-1", -14, "I worked on two model kits over the weekend.", "Logged the two model kits you worked on."},
		{"m-2", -2, "Picked up three additional model kits at the hobby counter.", "Added three additional model kits to your list."},
	} {
		at := now.AddDate(0, 0, sp.offset)
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, sp.id+".jsonl"), sp.id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": sp.user},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": sp.id, "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": sp.asst},
			}),
		})
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Chdir(projDir)

	var bikeOut bytes.Buffer
	bikeIn := jsonLine(t, map[string]string{
		"prompt":     "How many bikes do I currently own?",
		"cwd":        projDir,
		"session_id": "ask-bikes",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(bikeIn), &bikeOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"three bikes", "four bikes"} {
		if !strings.Contains(bikeOut.String(), want) {
			t.Fatalf("expected %q in bikes digest, got:\n%s", want, bikeOut.String())
		}
	}

	var kitOut bytes.Buffer
	kitIn := jsonLine(t, map[string]string{
		"prompt":     "How many model kits have I worked on or bought?",
		"cwd":        projDir,
		"session_id": "ask-kits",
	})
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(kitIn), &kitOut, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"two model kits", "three additional model kits"} {
		if !strings.Contains(kitOut.String(), want) {
			t.Fatalf("expected %q in model kits digest, got:\n%s", want, kitOut.String())
		}
	}
}

func TestPromptHookBetweenEventsSharesBudgetAndWeakFallback(t *testing.T) {
	t.Run("between event recall across dates", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 30)
		now := time.Now().UTC()

		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"evt-1", -24, "/tmp/events-a", "Visited the hilltop observatory tour last evening.", "Logged OBSERVATORY_TOUR_1 notes."},
			{"evt-2", -4, "/tmp/events-b", "Attended the weekend planetarium talk with friends.", "Logged PLANETARIUM_TALK_2 notes."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "How many days passed between the observatory tour and the planetarium talk?",
			"cwd":        home,
			"session_id": "ask-between-events",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "OBSERVATORY_TOUR_1") || !strings.Contains(got, "PLANETARIUM_TALK_2") {
			t.Fatalf("expected both between-clause sessions in digest, got:\n%s", got)
		}
	})

	t.Run("multi-session digest shares budget across bulky update", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 30)
		now := time.Now().UTC()

		longUser := "My friend Nadia decided to relocate to Seattle after finishing the catalog review, packing every crate carefully and coordinating the freight schedule for weeks before the truck departed."
		longAsst := "Noted that Nadia completed her Seattle relocation after wrapping up the catalog review, packing crates carefully and scheduling the freight truck departure over several busy weeks."
		for _, sp := range []struct {
			id     string
			offset int
			cwd    string
			user   string
			asst   string
		}{
			{"nad-1", -21, "/tmp/life-n1", longUser, longAsst},
			{"nad-2", -3, "/tmp/life-n2", "My friend Nadia actually just moved back to the lakefront cottage this week.", "Updated: Nadia moved back to the lakefront cottage."},
		} {
			at := now.AddDate(0, 0, sp.offset)
			proj := encodeClaudeTestProjectDir(sp.cwd)
			writeClaudeFixture(t, filepath.Join(claudeRoot, proj, sp.id+".jsonl"), sp.id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": sp.user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": sp.id, "cwd": sp.cwd,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": sp.asst},
				}),
			})
		}

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "Where did Nadia move to after her recent relocation?",
			"cwd":        home,
			"session_id": "ask-nadia-reloc",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "Seattle") || !strings.Contains(got, "lakefront") {
			t.Fatalf("expected both earlier and updated relocation sessions in digest, got:\n%s", got)
		}
	})

	t.Run("speculative unconfident match falls back to weak pointer", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		projDir := filepath.Join(t.TempDir(), "svc")
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			t.Fatal(err)
		}
		projSlug := encodeClaudeTestProjectDir(projDir)
		now := time.Now().UTC()

		for i := range 90 {
			id := fmt.Sprintf("fill-%02d", i)
			at := now.AddDate(0, 0, -(40 + i))
			user := fmt.Sprintf("Inspect worker pool %d metrics.", i)
			if i < 30 {
				user = fmt.Sprintf("Audit worker pool %d metrics.", i)
			}
			writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, id+".jsonl"), id, []string{
				jsonLine(t, map[string]any{
					"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": projDir,
					"timestamp": at.Format(time.RFC3339),
					"message":   map[string]any{"role": "user", "content": user},
				}),
				jsonLine(t, map[string]any{
					"type": "assistant", "sessionId": id, "cwd": projDir,
					"timestamp": at.Add(time.Minute).Format(time.RFC3339),
					"message":   map[string]any{"role": "assistant", "content": fmt.Sprintf("Worker pool %d metrics healthy.", i)},
				}),
			})
		}

		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, "other-ve.jsonl"), "other-ve", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "other-ve", "cwd": projDir,
				"timestamp": now.AddDate(0, 0, -20).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "Deployed vault_exporter on staging."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "other-ve", "cwd": projDir,
				"timestamp": now.AddDate(0, 0, -20).Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Verified vault_exporter scrape endpoint."},
			}),
		})

		at := now.AddDate(0, 0, -5)
		longSetup := "Audit the standard quarterly operational review agenda step by step across every regional service group, verifying routine checklist signoffs, dashboard owners, incident handoff sheets, capacity planning worksheets, SLA adherence notes, backlog queue depth metrics, pager duty schedules, and weekly escalation rotations before finally inspecting quartz_scheduler."
		writeClaudeFixture(t, filepath.Join(claudeRoot, projSlug, "target-qs.jsonl"), "target-qs", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "target-qs", "cwd": projDir,
				"timestamp": at.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": longSetup},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "target-qs", "cwd": projDir,
				"timestamp": at.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Ack: logged operational review agenda items."},
			}),
		})

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		t.Chdir(projDir)

		var out bytes.Buffer
		in := jsonLine(t, map[string]string{
			"prompt":     "Did we audit quartz_scheduler or vault_exporter?",
			"cwd":        projDir,
			"session_id": "ask-qs",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(in), &out, true); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "this project has history") && !strings.Contains(got, "deja recall") {
			t.Fatalf("expected weak recall pointer fallback when speculative digest lacks 2-term quote, got:\n%s", got)
		}
	})
}

func TestPromptHookAdvicePointerAndUpdatePrecision(t *testing.T) {
	writePair := func(t *testing.T, claudeRoot string, ts time.Time, id, cwd, userText, asstText string) {
		t.Helper()
		proj := encodeClaudeTestProjectDir(cwd)
		writeClaudeFixture(t, filepath.Join(claudeRoot, proj, id+".jsonl"), id, []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": id, "cwd": cwd,
				"timestamp": ts.Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": userText},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": id, "cwd": cwd,
				"timestamp": ts.Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": asstText},
			}),
		})
	}

	t.Run("open-ended advice prompt emits weak pointer while again follow-up emits full digest", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 36)
		now := time.Now().UTC()

		writePair(t, claudeRoot, now.AddDate(0, 0, -4), "cook-pref", "/tmp/home-a",
			"I recently made beef stew in my slow cooker and want vegetarian slow cooker ideas too.",
			"Low and slow works best for stew; preheat the ceramic insert for custard or yogurt.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -6), "lisbon-trip", "/tmp/home-b",
			"I'm planning a trip to Lisbon, which neighborhood do you suggest for quiet cafes?",
			"Try Principe Real in Lisbon for quiet leafy squares and morning espresso spots.")

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var adviceOut bytes.Buffer
		adviceIn := jsonLine(t, map[string]string{
			"prompt":     "I've been struggling with my slow cooker recipes. Any advice on getting better results?",
			"cwd":        home,
			"session_id": "ask-advice-cook",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(adviceIn), &adviceOut, true); err != nil {
			t.Fatal(err)
		}
		adviceGot := adviceOut.String()
		if !strings.Contains(adviceGot, "this project has history") && !strings.Contains(adviceGot, "call deja") {
			t.Fatalf("expected weak recall pointer on open-ended advice prompt, got:\n%s", adviceGot)
		}
		if strings.Contains(adviceGot, "deja:cook-pref") {
			t.Fatalf("expected open-ended advice prompt to avoid full digest block, got:\n%s", adviceGot)
		}

		var againOut bytes.Buffer
		againIn := jsonLine(t, map[string]string{
			"prompt":     "I'm planning my trip to Lisbon again, what neighborhood did you recommend?",
			"cwd":        home,
			"session_id": "ask-again-lisbon",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(againIn), &againOut, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(againOut.String(), "Principe Real") {
			t.Fatalf("expected full digest carrying Principe Real on again follow-up, got:\n%s", againOut.String())
		}
	})

	t.Run("multi-term update rejects 1-word noise and orders newest date first", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 36)
		now := time.Now().UTC()

		writePair(t, claudeRoot, now.AddDate(0, 0, -2), "coupon-1", "/tmp/errands-a",
			"I redeemed a $5 coupon on coffee creamer last Sunday.",
			"Using Cartwheel at Target is a handy way to catch surprise coupons on groceries.")
		projErrandsMulti := encodeClaudeTestProjectDir("/tmp/errands-multi")
		writeClaudeFixture(t, filepath.Join(claudeRoot, projErrandsMulti, "coupon-multi.jsonl"), "coupon-multi", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "I have been using the Cartwheel app from Target to organize coupons."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "A coupon binder also pairs nicely with digital app clips."},
			}),
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Add(2 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "I actually redeemed a $5 coupon on coffee creamer last Sunday."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "coupon-multi", "cwd": "/tmp/errands-multi",
				"timestamp": now.AddDate(0, 0, -1).Add(3 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Finding unexpected discounts in your inbox is always a nice bonus."},
			}),
		})
		writePair(t, claudeRoot, now.AddDate(0, 0, -7), "coffee-table", "/tmp/errands-b",
			"I will clear the clutter off the coffee table before organizing cables.",
			"Starting with the coffee table surface will make sorting remotes much faster.")

		writePair(t, claudeRoot, now.AddDate(0, 0, -10), "reloc-old", "/tmp/reloc-a",
			"Rachel is planning a relocation to the city soon; she moved to Chicago.",
			"Sounds exciting to plan a visit once Rachel settles in Chicago.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -2), "reloc-new", "/tmp/reloc-a",
			"My friend Rachel actually just moved back to the suburbs again.",
			"Updated note: Rachel relocated back to the suburbs this week.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -15), "author-qa", "/tmp/reloc-a",
			"I got to ask the author Rachel Smith a question during the bookstore Q&A.",
			"Glad you got to hear Rachel Smith discuss her novel at the Q&A.")

		projMovies := encodeClaudeTestProjectDir("/tmp/movies-a")
		writeClaudeFixture(t, filepath.Join(claudeRoot, projMovies, "watch-marathon.jsonl"), "watch-marathon", []string{
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "I just finished a Star Wars marathon, watched all the main films in a week and a half!"},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Add(time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "Sounds like a fun movie marathon!"},
			}),
			jsonLine(t, map[string]any{
				"type": "user", "entrypoint": "cli", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Add(2 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "Yes, I will add those Star Wars films to the simple list, plus Rogue One and Solo, along with several recent releases and documentaries we talked about earlier before adding all the Marvel Cinematic Universe movies too."},
			}),
			jsonLine(t, map[string]any{
				"type": "assistant", "sessionId": "watch-marathon", "cwd": "/tmp/movies-a",
				"timestamp": now.AddDate(0, 0, -3).Add(3 * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "assistant", "content": "I added both franchises to your list."},
			}),
		})

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var couponOut bytes.Buffer
		couponIn := jsonLine(t, map[string]string{
			"prompt":     "Where did I redeem a $5 coupon on coffee creamer?",
			"cwd":        "/tmp/errands-multi",
			"session_id": "ask-coupon-adj",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(couponIn), &couponOut, true); err != nil {
			t.Fatal(err)
		}
		couponGot := couponOut.String()
		if !strings.Contains(couponGot, "Target") || !strings.Contains(couponGot, "deja:coupon-m") {
			t.Fatalf("expected adjacent user Target context and deja:coupon-m, got:\n%s", couponGot)
		}
		if strings.Contains(couponGot, "coffee table") {
			t.Fatalf("expected 1-word coffee table distractor excluded, got:\n%s", couponGot)
		}

		var relocOut bytes.Buffer
		relocIn := jsonLine(t, map[string]string{
			"prompt":     "Where did Rachel move to after her recent relocation?",
			"cwd":        "/tmp/reloc-a",
			"session_id": "ask-rachel-reloc",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(relocIn), &relocOut, true); err != nil {
			t.Fatal(err)
		}
		relocGot := relocOut.String()
		if strings.Contains(relocGot, "Rachel Smith") {
			t.Fatalf("expected in-scope 1-word author Q&A distractor excluded, got:\n%s", relocGot)
		}
		idxSuburbs := strings.Index(relocGot, "reloc-new")
		idxChicago := strings.Index(relocGot, "reloc-old")
		if idxSuburbs < 0 || idxChicago < 0 || idxSuburbs > idxChicago {
			t.Fatalf("expected newer reloc-new before reloc-old, got:\n%s", relocGot)
		}
		if !strings.Contains(relocGot, "deja:reloc-new") {
			t.Fatalf("expected opener line to cite newer reloc-new session, got:\n%s", relocGot)
		}

		var marathonOut bytes.Buffer
		marathonIn := jsonLine(t, map[string]string{
			"prompt":     "How many weeks did it take me to watch all the Marvel Cinematic Universe movies and the main Star Wars films?",
			"cwd":        "/tmp/movies-a",
			"session_id": "ask-marathon",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(marathonIn), &marathonOut, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(marathonOut.String(), "week and a half") {
			t.Fatalf("expected earlier user quantity turn with 'week and a half', got:\n%s", marathonOut.String())
		}
	})

	t.Run("partial-subject sessions excluded from third slot and overflow", func(t *testing.T) {
		hermeticEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		seedMediumFillerSessions(t, claudeRoot, 36)
		now := time.Now().UTC()

		writePair(t, claudeRoot, now.AddDate(0, 0, -2), "fam-paris", "/tmp/travel-1",
			"We just returned from our recent family trip to Paris.",
			"Logged your recent family trip to Paris.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -5), "fam-hawaii", "/tmp/travel-2",
			"Our recent family trip to Hawaii had great snorkeling spots.",
			"Logged your recent family trip to Hawaii.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -10), "solo-yosemite", "/tmp/travel-3",
			"Testing water filters on my recent solo trip to Yosemite.",
			"Noted gear test from your solo trip to Yosemite.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -12), "solo-moab", "/tmp/travel-4",
			"Packing repair kit for my recent solo trip to Moab.",
			"Noted packing list for your solo trip to Moab.")
		writePair(t, claudeRoot, now.AddDate(0, 0, -14), "solo-banff", "/tmp/travel-5",
			"Checking trail maps for my recent solo trip to Banff.",
			"Noted maps for your solo trip to Banff.")

		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		home := os.Getenv("HOME")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(home)

		var tripOut bytes.Buffer
		tripIn := jsonLine(t, map[string]string{
			"prompt":     "Where did I go on my most recent family trip?",
			"cwd":        home,
			"session_id": "ask-family-trip",
		})
		if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(tripIn), &tripOut, true); err != nil {
			t.Fatal(err)
		}
		tripGot := tripOut.String()
		if !strings.Contains(tripGot, "Paris") || !strings.Contains(tripGot, "Hawaii") {
			t.Fatalf("expected both Paris and Hawaii family trip sessions, got:\n%s", tripGot)
		}
		if strings.Contains(tripGot, "Yosemite") || strings.Contains(tripGot, "more matching sessions") {
			t.Fatalf("expected Yosemite excluded and no overflow hint, got:\n%s", tripGot)
		}
	})
}

func mustJSONBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHookPromptIndexedSessionSurfacesPostEditRootCauseOverEarlyGuess(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	now := time.Now().UTC()

	repoDir := filepath.Join(t.TempDir(), "work", "relsite")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "pkg", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseAbs := filepath.Join(repoDir, "pkg", "models", "base.py")
	if err := os.WriteFile(baseAbs, []byte("class ModelBase:\n    def _prepare_related_fields_for_save(self):\n        pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := encodeClaudeTestProjectDir(repoDir)
	ts := func(mins int) string {
		return now.Add(-24*time.Hour + time.Duration(mins)*time.Minute).Format(time.RFC3339)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, proj, "past-root-cause.jsonl"), "past-root-cause", []string{
		jsonLine(t, map[string]any{
			"type": "user", "entrypoint": "cli", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(0),
			"message": map[string]any{
				"role":    "user",
				"content": "Saving parent object after setting on child loses primary key\n\nWhen assigning a parent object before save, saving the parent later drops the primary key column in ModelBase.",
			},
		}),
		jsonLine(t, map[string]any{
			"type": "assistant", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(1),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Now I understand the issue! In the `__set__` method at line 257, it copies the primary key value at assignment time. Let me make this fix:",
					},
				},
			},
		}),
		jsonLine(t, map[string]any{
			"type": "assistant", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(2),
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "e1",
						"name":  "Edit",
						"input": map[string]any{"file_path": baseAbs, "old_string": "pass", "new_string": "self._sync_fk()"},
					},
					map[string]any{
						"type":  "tool_use",
						"id":    "b1",
						"name":  "Bash",
						"input": map[string]any{"command": "pytest tests/test_models.py"},
					},
				},
			},
		}),
		jsonLine(t, map[string]any{
			"type": "user", "entrypoint": "cli", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(3),
			"message": map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": "e1", "content": "ok"},
					map[string]any{"type": "tool_result", "tool_use_id": "b1", "content": "2 passed in 0.12s"},
				},
			},
		}),
		jsonLine(t, map[string]any{
			"type": "assistant", "sessionId": "past-root-cause", "cwd": repoDir,
			"timestamp": ts(4),
			"message": map[string]any{
				"role":    "assistant",
				"content": "## Bug Summary\nSaving parent object after assignment lost the primary key column.\n\n## Root Cause\nIn pkg/models/base.py, `_prepare_related_fields_for_save` must refresh the related primary key attribute from the parent object before saving.",
			},
		}),
	})

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	payload := string(mustJSONBytes(t, map[string]string{
		"session_id": "live-asker",
		"cwd":        repoDir,
		"prompt":     "Saving parent object with non-numeric primary key fails after child assignment\n\nModelBase drops the primary key column on save after related object assignment.",
	}))
	var out bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "_prepare_related_fields_for_save") {
		t.Fatalf("expected post-edit Root Cause quoting _prepare_related_fields_for_save, got:\n%s", got)
	}
	if strings.Contains(got, "line 257") || strings.Contains(got, "Let me make this fix") {
		t.Fatalf("expected early hypothesis suppressed in favor of post-edit Root Cause, got:\n%s", got)
	}
}

// Two sessions hold the subject: one talked about it and settled nothing, the
// other concluded something. The block has room for one, and the reader is
// served by the conclusion.
//
// `deja bench prompt` has scored this since the concluded_session arm was
// written, and its probe said in a comment that it applied "the same ordering
// the hook applies, from the same function" — but the hook never called it.
// Removing the call again leaves every other test green.
func TestHookPromptLeadsWithTheSessionThatConcluded(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	recent := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	older := time.Now().Add(-96 * time.Hour).UTC().Format(time.RFC3339)

	// Says the subject over and over and never settles it, so ranking puts it
	// first on sheer weight.
	talk := make([]string, 0, 6)
	for range 6 {
		talk = append(talk, `{"type":"assistant","sessionId":"kestreltalk","timestamp":"`+recent+
			`","message":{"role":"assistant","content":"looking at kestrel retries again, still not touching kestrel"}}`)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "talk.jsonl"), "kestreltalk", talk)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "done.jsonl"), "kestreldone", []string{
		`{"type":"assistant","sessionId":"kestreldone","timestamp":"` + older +
			`","message":{"role":"assistant","content":"the fix: kestrel retries are capped at four"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel retries","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "kestrel") {
		t.Fatalf("nothing was recalled, so the ordering below is untested:\n%q", got)
	}
	fix := strings.Index(got, "capped at four")
	talked := strings.Index(got, "still not touching")
	if fix < 0 {
		t.Fatalf("the session that concluded something was not shown at all:\n%q", got)
	}
	if talked >= 0 && talked < fix {
		t.Errorf("the block opens on the session that settled nothing:\n%q", got)
	}
}

// The prompt that arrives every minute. Each tick is a new call with the same
// text, and the cooldowns are counted per session shown — so the hook worked
// down the ranking, one session per tick, and answered a loop with sessions
// that had nothing to do with it (#3189).
func TestALoopedPromptIsAnsweredOnce(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	// Several sessions that all carry the subject, which is what gives the
	// ranking somewhere to walk to on the second tick.
	answers := []struct{ subject, settled string }{
		{"dial deadline", "the zebraquux fetcher timed out on a 200ms dial deadline; we raised it to 5s"},
		{"retry budget", "the zebraquux retry budget was two, so the fetcher gave up early"},
		{"dns", "zebraquux resolved slowly and the fetcher timed out waiting on DNS"},
		{"connection pool", "the fetcher shares a pool with zebraquux, so one slow call times out the rest"},
	}
	for i, a := range answers {
		line := fmt.Sprintf(`{"type":"user","sessionId":"zeb%d","timestamp":%q,`+
			`"message":{"role":"user","content":"looking at the zebraquux %s again"}}`+"\n"+
			`{"type":"assistant","sessionId":"zeb%d","timestamp":%q,`+
			`"message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`, i, at, a.subject, i, at, a.settled)
		if err := os.WriteFile(filepath.Join(claude, fmt.Sprintf("zeb%d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// And the session the question was actually asked in, which is what the
	// first tick is answered with.
	asked := fmt.Sprintf(`{"type":"user","sessionId":"zebmain","timestamp":%q,`+
		`"message":{"role":"user","content":"why does the zebraquux fetcher time out?"}}`+"\n"+
		`{"type":"assistant","sessionId":"zebmain","timestamp":%q,`+
		`"message":{"role":"assistant","content":[{"type":"text","text":`+
		`"we settled it: the zebraquux fetcher dial deadline goes to 5s and the retry budget to three"}]}}`, at, at)
	if err := os.WriteFile(filepath.Join(claude, "zebmain.jsonl"), []byte(asked+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(tmp, "work", "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	tick := func(prompt string) string {
		t.Helper()
		var out bytes.Buffer
		payload := fmt.Sprintf(`{"session_id":"loop-1","cwd":%q,"prompt":%q}`, cwd, prompt)
		if err := runHookPrompt(dir, strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	const q = "why does the zebraquux fetcher time out?"
	first := tick(q)
	if !strings.Contains(first, "zebraquux") {
		t.Fatalf("the first tick recalled nothing, so this measures nothing: %q", first)
	}
	for i := 2; i <= 4; i++ {
		if got := tick(q); strings.Contains(got, "deja-recall") {
			t.Errorf("tick %d answered the same question again: %q", i, got)
		}
	}
	// A different question in the same reader is still a question.
	if got := tick("what did we settle on for the zebraquux retry budget?"); !strings.Contains(got, "deja-recall") {
		t.Errorf("the next question went unanswered: %q", got)
	}
}

// Every other surface puts a promoted note in front of the transcript it was
// distilled from. The per-prompt hook ranks sessions and never builds a
// search.Hit, so the rule could not reach it and the block led with the
// transcript while its own note waited behind (#2803).
func TestTheHookLiftsANoteAboveItsSource(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	// The transcript says the question's words several times over; the note is
	// the one line distilled from it. That is the shape the rule exists for —
	// on score alone the source wins its own distillation.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "s14", []string{
		`{"type":"user","sessionId":"s14","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer pool cap settled at 40 for the ticker service"}}`,
		`{"type":"user","sessionId":"s14","timestamp":"` + old +
			`","message":{"role":"user","content":"what should the pgbouncer pool cap be for the ticker service"}}`,
		`{"type":"user","sessionId":"s14","timestamp":"` + old +
			`","message":{"role":"user","content":"the pgbouncer pool cap keeps coming up, cap the pool"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "promote", "s14"); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we settle for the pgbouncer pool cap","session_id":"ses_ask"}`)
	if err := runHookPromptMode(dir, in, &out, true); err != nil {
		t.Fatal(err)
	}
	block := out.String()
	// The ids as the block prints them, in backticks. The note's own line
	// carries "-s14`" and not "`s14`", so the two needles do not collide — an
	// earlier version of this test searched for "· s14", which is not how the
	// line is written, and the order below was never actually checked.
	note := strings.Index(block, "`deja-note-claude-s14`")
	source := strings.Index(block, "`s14`")
	// Not a skip: a block holding neither is a fixture that stopped reaching
	// the path, and a test that goes quiet there guards nothing (#1412).
	if note < 0 {
		t.Fatalf("the note promoted from the session is not in the block:\n%s", block)
	}
	if source < 0 {
		t.Fatalf("the transcript is not in the block, so there is no order to check:\n%s", block)
	}
	if note > source {
		t.Errorf("the transcript came before the note distilled from it:\n%s", block)
	}
}

// A background job finishing arrives as a user turn: a banner saying it is not
// the user, the host's own paragraph, then the notification block. Taking the
// block out left the paragraph, and the hook answered it — "you have been here"
// about a sentence no person wrote (#3156 fixed the block, not the prose).
func TestHookPromptSaysNothingToAHostNotification(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "wombat.jsonl"), "wombat", []string{
		`{"type":"user","sessionId":"wombat","timestamp":"` + ts +
			`","message":{"role":"user","content":"the automated background task never received a message from the user"}}`,
		`{"type":"assistant","sessionId":"wombat","timestamp":"` + ts +
			`","message":{"role":"assistant","content":"the wombat queue drops a notification whose status arrives last"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	turn := "[SYSTEM NOTIFICATION - NOT USER INPUT]\\n" +
		"This is an automated background-task event, NOT a message from the user.\\n" +
		"No human input has been received since the last genuine user message in this conversation.\\n\\n" +
		"<task-notification>\\n<task-id>br50mykp6</task-id>\\n<status>completed</status>\\n</task-notification>"

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"` + turn + `","session_id":"asking"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "" {
		t.Errorf("the hook answered the host talking to itself:\n%s", got)
	}
}

// A prompt naming exactly one term of art is the sharpest question deja can be
// asked, and it was the one the hook never answered: "do we need pgbouncer
// here" reduces to a single term and a two-term floor returned before the
// store was touched, while the same question padded with a filler noun fired.
// `deja bench prompt` puts the floor at 10/12 real questions and the
// identifier rule at 11/12, precision 1 and no false fire on any of the 10
// negative controls, on seeds 1, 2, 3 and 7 (#R9).
func TestHookPromptAnswersASingleIdentifier(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "oneterm", []string{
		`{"type":"user","sessionId":"oneterm","timestamp":"` + old + `","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	padStoreWithOtherWork(t, claudeRoot)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"do we need pgbouncer here"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "transaction mode") {
		t.Errorf("one-identifier prompt recalled nothing:\n%q", out.String())
	}

	// The floor still holds where nothing in the prompt identifies anything:
	// a single ordinary word must not open the store.
	if got := promptTermsWorthAsking([]string{"tests"}); got {
		t.Error("a lone plain word cleared the gate")
	}
	if got := promptTermsWorthAsking(nil); got {
		t.Error("an empty prompt cleared the gate")
	}
}

// The pointer's whole content is the topic it names, because the agent has to
// turn that into a recall call. It was built without the query's terms, so
// dejaVuTopic fell through to the session's title: measured against a real
// store, "how is the block" was answered with `"делай задачи в todo.md"` — a
// title from a session that merely mentions the subject somewhere.
func TestThePointerNamesWhatWasAskedAbout(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "org/app",
		Title:   "делай задачи в todo.md",
		Updated: time.Now(),
		// The title and the opening line are one thing; the line that matched
		// the question is further down. Without the terms the topic comes from
		// the top of the session, which is the bug.
		Messages: []model.Message{
			{Role: "user", Text: "делай задачи в todo.md, там список на сегодня"},
			{Role: "assistant", Text: "открыл список"},
			{Role: "user", Text: "the exporter retry budget keeps hammering the server on failure"},
			{Role: "assistant", Text: "capped it at three attempts"},
		},
	}
	got := weakRecallPointer([]model.Session{s}, []string{"retry", "budget"})
	if !strings.Contains(got, "retry budget") {
		t.Errorf("the pointer does not name what was asked about: %q", got)
	}
	if strings.Contains(got, "todo.md") {
		t.Errorf("the pointer named the session's title instead: %q", got)
	}
}

// And it stays a pointer. The matched line is a whole user turn, which on a real
// store reaches a thousand characters: unclipped, the block went from 290 bytes
// to 1234, which is a digest's price for a pointer's content.
func TestThePointerStaysShort(t *testing.T) {
	long := "Подробный архитектурный разбор — post-mortem репликации в StorageEngine, где описывают " +
		"как низкоуровневый слой операционного recall и отдельно отмечают фильтрацию до индексации, " +
		"а ограничения формулируют через лексическое перекрытие поиска."
	s := model.Session{
		Harness: "claude", ID: "s1", Project: "org/app", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: long},
			{Role: "assistant", Text: "прочитал"},
		},
	}
	got := weakRecallPointer([]model.Session{s}, []string{"recall", "разбор"})
	if len(got) > 260 {
		t.Errorf("the pointer is %d bytes, which is a digest's price: %q", len(got), got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a clipped topic does not say it was clipped: %q", got)
	}
	// Whole words, so the reader can search for what it names.
	if strings.Contains(got, "post-mort\"") || strings.Contains(got, "StorageEng…") {
		t.Errorf("the topic was cut mid-word: %q", got)
	}
}

func sessionsWithIDs(ids ...string) []model.Session {
	out := make([]model.Session, 0, len(ids))
	for _, id := range ids {
		out = append(out, model.Session{ID: id})
	}
	return out
}

// The per-prompt cooldown only ever counted the agent session it was called
// from, so every new agent session started blank and the same past session was
// served again. Measured over six weeks of a real log: 937 injections drawn
// from 74 distinct sessions, 92% repeats, ten sessions carrying 80% of the
// total (#2038).
//
// This pins the part that was missing rather than the ranking: what one agent
// session was shown has to be visible to the next one working in the same
// project.
func TestTheCooldownOutlivesTheAgentSession(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const project = "deja-vu"

	rememberInjectedFor(dir, "agent-one", project, sessionsWithIDs("marathon", "other"))

	// The next agent session knows nothing of the first one — that is the bug.
	if got := recentlyInjected(dir, "agent-two", injectionCooldown); len(got) != 0 {
		t.Fatalf("a fresh agent session already had a cooldown: %v", got)
	}

	got := recentlyInjectedInProject(dir, project, injectionCooldown)
	if !got["marathon"] || !got["other"] {
		t.Errorf("the project cooldown lost what the previous agent session was shown: %v", got)
	}

	// A different project is not the repetition being fixed, and must not be
	// suppressed by it.
	if other := recentlyInjectedInProject(dir, "some-other-repo", injectionCooldown); len(other) != 0 {
		t.Errorf("the cooldown reached into another project: %v", other)
	}
}

// Lines written before the project field existed carry three fields. They have
// to be read past rather than guessed at, or every old entry would match the
// first project asked about.
func TestOldSeenLinesCarryNoProject(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if err := os.WriteFile(dir+".hookseen", []byte("agent-one marathon 2026-08-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := recentlyInjectedInProject(dir, "deja-vu", injectionCooldown); len(got) != 0 {
		t.Errorf("a line with no project matched a project: %v", got)
	}
	// The per-session cooldown still reads it, so nothing regresses for the
	// agent session that wrote it.
	if got := recentlyInjected(dir, "agent-one", injectionCooldown); !got["marathon"] {
		t.Errorf("the old line stopped working for its own agent session: %v", got)
	}
}

// The window is a window: a session shown long enough ago comes back, because
// the same past work answers many questions across a day.
func TestTheProjectCooldownIsAWindowNotABan(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	const project = "deja-vu"
	rememberInjectedFor(dir, "agent-one", project, sessionsWithIDs("marathon"))
	for range injectionCooldown {
		rememberInjectedFor(dir, "agent-one", project, sessionsWithIDs("filler"))
	}
	if got := recentlyInjectedInProject(dir, project, injectionCooldown); got["marathon"] {
		t.Error("a session pushed out of the window is still being skipped")
	}
}

func TestTheSeenLineStaysFieldSeparated(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	rememberInjectedFor(dir, "agent-one", "deja-vu", sessionsWithIDs("marathon"))
	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(b))
	if n := len(strings.Fields(line)); n != 4 {
		t.Fatalf("the line has %d fields, not four: %q", n, line)
	}
}

// `opencode run "<question>"` sends the hook the question wrapped in quotes.
// Read as one quoted clause, the instruction in front of the question spent the
// terms and the block was empty, where the same words on stdin found the
// session. The quotes the host added are not the person's.
func TestAPromptTheHostQuotedIsAnsweredLikeTheBareOne(t *testing.T) {
	ask := "without calling any MCP tool and without reading any project files or running commands: how did we install quokkavllm?"
	answer := func(prompt string) string {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		claude := filepath.Join(tmp, "claude", "proj")
		for _, d := range []string{home, claude} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
		t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
		t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
		t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
		at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		rows := fmt.Sprintf(`{"type":"user","sessionId":"q1","timestamp":%q,`+
			`"message":{"role":"user","content":"the quokkavllm wheel install keeps failing on the build box, fix it"}}`+"\n"+
			`{"type":"assistant","sessionId":"q1","timestamp":%q,`+
			`"message":{"role":"assistant","content":[{"type":"text","text":`+
			`"Decided: install quokkavllm from the internal mirror with pip3 install --index-url https://mirror.example.internal/simple quokkavllm"}]}}`, at, at)
		if err := os.WriteFile(filepath.Join(claude, "q1.jsonl"), []byte(rows+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(tmp, "index.db")
		if err := index.Ensure(dir, "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(tmp, "work", "proj")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		payload, err := json.Marshal(map[string]string{"session_id": "oc-1", "cwd": cwd, "prompt": prompt})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runHookPrompt(dir, bytes.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if bare := answer(ask); !strings.Contains(bare, "mirror.example.internal") {
		t.Fatalf("the bare question recalled nothing, so this measures nothing: %q", bare)
	}
	if quoted := answer(`"` + ask + `"`); !strings.Contains(quoted, "mirror.example.internal") {
		t.Errorf("the question opencode quoted recalled nothing: %q", quoted)
	}
}

// The repetition #2038 measured, at the level it actually happens: two agent
// sessions asking the same thing in the same project. The cooldown used to be
// keyed on the agent session id alone, so the second one started blank and was
// handed the same past session again — 92% of per-prompt injections in six
// weeks of a real log were repeats of 74 sessions.
//
// End-to-end rather than on the helpers, because the helpers passed with the
// cooldown unwired.
func TestASecondAgentSessionIsNotHandedTheSameMemory(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "repeatterm", []string{
		`{"type":"user","sessionId":"repeatterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	ask := func(agentSession string) string {
		t.Helper()
		var out bytes.Buffer
		in := strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"` + agentSession + `"}`)
		if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	first := ask("agent-one")
	if !strings.Contains(first, "transaction mode") {
		t.Fatalf("the first agent session got no memory, so there is nothing to repeat:\n%q", first)
	}

	second := ask("agent-two")
	if strings.Contains(second, "transaction mode") {
		t.Errorf("a second agent session in the same project was handed the same session again:\n%q", second)
	}
}

// What must not repeat is what the reader sees, not where it came from. Banning
// the session banned everything else it held: In real-world stores, walking
// one working session of eighty messages, the second half got three blocks
// where the same messages without the ban got thirty-eight — and only seven of
// those were word-for-word repeats. The same past work answers many questions
// in a day.
func TestBlockFingerprintIgnoresSpacingAndSeparatesContent(t *testing.T) {
	a := blockFingerprint("- Assistant: pgbouncer pool size settled at 40\n")
	b := blockFingerprint("- Assistant:   pgbouncer pool size settled at 40")
	if a != b {
		t.Errorf("the same block fingerprinted differently after re-wrapping: %s vs %s", a, b)
	}
	c := blockFingerprint("- Assistant: pgbouncer pool size settled at 41")
	if a == c {
		t.Error("two different blocks share a fingerprint")
	}
}

// The cooldown counts injections, not minutes: a run of messages about one
// thing should not keep re-reading the same session, and a session that comes
// back to it later should get it again.
func TestRecentlyInjectedCountsTheLastFew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	var b strings.Builder
	b.WriteString("agent old-1 2020-01-01T00:00:00Z\n")
	for range injectionCooldown {
		b.WriteString("agent filler 2020-01-01T00:00:00Z\n")
	}
	b.WriteString("agent fresh-1 2020-01-01T00:00:00Z\n")
	if err := os.WriteFile(dir+".hookseen", []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	got := recentlyInjected(dir, "agent", injectionCooldown)
	if !got["fresh-1"] {
		t.Error("a session shown a moment ago is not in the window")
	}
	if got["old-1"] {
		t.Error("a session shown long before the window still counts as recent")
	}
	if other := recentlyInjected(dir, "someone-else", injectionCooldown); len(other) != 0 {
		t.Errorf("another agent session's history leaked in: %v", other)
	}
}

// Entries written before this carried two fields, and the plan hook still
// writes two. Both shapes have to read back.
func TestAlreadyInjectedReadsBothLineShapes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.WriteFile(dir+".hookseen",
		[]byte("agent two-fields\nagent three-fields 2020-01-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := alreadyInjected(dir, "agent")
	for _, want := range []string{"two-fields", "three-fields"} {
		if !got[want] {
			t.Errorf("%q was not read back from the seen list: %v", want, got)
		}
	}
}

// Asking the same thing twice in one agent session must not print the same
// block twice: the second time it is wallpaper, and a reader who sees wallpaper
// stops reading. What is banned is the text, so the session it came from can
// still answer the next question with other lines.
func TestHookPromptDoesNotRepeatTheSameBlock(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "repeatterm", []string{
		`{"type":"user","sessionId":"repeatterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	payload := `{"prompt":"do we need pgbouncer here","session_id":"agent-1"}`
	var first bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "transaction mode") {
		t.Fatalf("nothing was recalled the first time:\n%q", first.String())
	}
	// Push that session out of the cooldown window, so the ranking offers it
	// again and the only thing left to stop a repeat is the block itself.
	f, err := os.OpenFile(index.DefaultDir()+".hookseen", os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for i := range injectionCooldown + 2 {
		if _, err := f.WriteString("agent-1 filler-" + strconv.Itoa(i) + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var second bytes.Buffer
	if err := runHookPromptMode(index.DefaultDir(), strings.NewReader(payload), &second, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second.String(), "transaction mode") {
		t.Errorf("the same block was shown twice:\n%q", second.String())
	}
}

// The session being written right now must never be recalled to itself: its
// text is already in front of the agent, and quoting it back spends tokens to
// say nothing. The hook has skipped it for a long time and nothing held that
// in place — removing the check left every test passing, which is how the
// order of the checks came to be changed without anything noticing.
func TestHookPromptDoesNotRecallTheSessionBeingWritten(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "self.jsonl"), "selfsession", []string{
		`{"type":"user","sessionId":"selfsession","timestamp":"` + old + `","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var other bytes.Buffer
	in := strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"another"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &other, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(other.String(), "transaction mode") {
		t.Fatalf("the fixture is not recalled at all, so the test below proves nothing:\n%q", other.String())
	}

	var self bytes.Buffer
	in = strings.NewReader(`{"prompt":"do we need pgbouncer here","session_id":"selfsession"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &self, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(self.String(), "transaction mode") {
		t.Errorf("the session being written was recalled to itself:\n%q", self.String())
	}
}

// A session already injected into this agent session is discarded on its id.
// Handing that id to the ranking means it is never read: measured on a real
// store, 15 of the 26 candidates a prompt ranks are ones it has already shown,
// and reading them cost 40-56 ms a message. Walking one session, that is the
// difference between 65 ms and 26 ms a message.
//
// The block that comes out is the same either way, so the count of narrowed
// sessions is what holds this in place.
func TestHookPromptDoesNotReadASessionItAlreadyShowed(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	ts := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	lines := make([]string, 0, dejaVuMaxMessages+2)
	for i := range dejaVuMaxMessages + 1 {
		lines = append(lines, fmt.Sprintf(
			`{"type":"assistant","sessionId":"longone","timestamp":%q,"message":{"role":"assistant","content":"the fix: kestrel retries are capped at four, note %d"}}`,
			ts, i))
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "longone.jsonl"), "longone", lines)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	var first bytes.Buffer
	in := strings.NewReader(`{"prompt":"what did we decide about kestrel retries","session_id":"agent-1"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &first, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first.String(), "kestrel") {
		t.Fatalf("the session was not recalled the first time, so the second ask proves nothing:\n%q", first.String())
	}

	rankedAlreadyShown.Store(0)
	var second bytes.Buffer
	in = strings.NewReader(`{"prompt":"and what about kestrel retries","session_id":"agent-1"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &second, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(second.String(), "capped at four") {
		t.Fatalf("the same session was injected twice:\n%q", second.String())
	}
	if n := rankedAlreadyShown.Load(); n != 0 {
		t.Errorf("the ranking returned %d session(s) already shown — they are read from disk in full and then dropped", n)
	}
}

// ZCode runs `hook-prompt --strict` and discards a reply with a key its schema
// does not know, recall and all. The flag was read only in hook-context, so
// whenever the "you have been here" line was due ZCode lost the recall too.
func TestStrictPromptHookDropsTheReceiptAndKeepsTheRecall(t *testing.T) {
	ask := func(t *testing.T, args ...string) string {
		t.Helper()
		hermeticEnv(t)
		t.Cleanup(func() { strictHookOutput = false })
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-hay", "focus.jsonl"), "focus", []string{
			`{"type":"user","sessionId":"focus","timestamp":"` + old + `","message":{"role":"user","content":"the quetzalcoatl stampede fix: jittered ttl"}}`,
		})
		if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
			t.Fatal(err)
		}
		cwd := filepath.Join(t.TempDir(), "tmp", "hay")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(cwd)
		withHookStdin(t, `{"session_id":"z-1","hook_event_name":"UserPromptSubmit","prompt":"quetzalcoatl stampede regression again"}`)
		out, err := captureRun(t, append([]string{"hook-prompt"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	type reply struct {
		SystemMessage      *string `json:"systemMessage"`
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	decode := func(t *testing.T, out string) reply {
		t.Helper()
		var r reply
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &r); err != nil {
			t.Fatalf("hook output is not JSON: %q", out)
		}
		return r
	}

	// The control: without the flag the receipt is due and goes out.
	plain := decode(t, ask(t))
	if plain.SystemMessage == nil || !strings.Contains(*plain.SystemMessage, "deja-vu — ") {
		t.Fatalf("the control carried no receipt, so the strict case proves nothing: %+v", plain)
	}

	strict := decode(t, ask(t, "--strict"))
	if strict.SystemMessage != nil {
		t.Errorf("a --strict reply carries systemMessage, which ZCode rejects with the recall: %q", *strict.SystemMessage)
	}
	if !strings.Contains(strict.HookSpecificOutput.AdditionalContext, "quetzalcoatl") {
		t.Errorf("the recall did not survive --strict: %q", strict.HookSpecificOutput.AdditionalContext)
	}
}

// A session that opens with a compaction summary is titled by the person's
// first real line, not by "Summary: 1. Primary Request and Intent: …" — the
// line the status bar quoted on 2026-09-08 (#3157).
func TestOpenerSkipsTheCompactionSummaryAsATitle(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "compact.jsonl"), "compact", []string{
		`{"type":"user","sessionId":"compact","timestamp":"` + old + `","message":{"role":"user","content":"Summary:\n1. Primary Request and Intent:\n   - MOST RECENT: fix the exporter_batch rows dropped at utc_midnight\n2. Key Technical Concepts:\n   - exporter_batch cursor"}}`,
		`{"type":"assistant","sessionId":"compact","timestamp":"` + old + `","message":{"role":"assistant","content":"Continuing from the summary."}}`,
		`{"type":"user","sessionId":"compact","timestamp":"` + old + `","message":{"role":"user","content":"the exporter_batch job drops rows at utc_midnight"}}`,
		`{"type":"assistant","sessionId":"compact","timestamp":"` + old + `","message":{"role":"assistant","content":"utc_midnight rollover: the exporter_batch cursor was compared in local time."}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	in := `{"prompt":"exporter_batch dropping rows at utc_midnight again","session_id":"agent-7"}`
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "Primary Request") {
		t.Fatalf("the compaction summary became a title:\n%s", got)
	}
	if !strings.Contains(got, "déjà vu: the exporter_batch job drops rows at utc_midnight") {
		t.Fatalf("the person's line was not the title:\n%s", got)
	}
}

func TestHookPromptInjectsOnRelevantHit(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "hookfix.jsonl"), "hookfix", []string{
		`{"type":"user","sessionId":"hookfix","timestamp":"` + old + `","message":{"role":"user","content":"gateway_timeout on the reconnect_loop keeps dropping heartbeats"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	in := strings.NewReader(`{"prompt":"seeing gateway_timeout in the reconnect_loop again"}`)
	if err := runHookPrompt(index.DefaultDir(), in, &out); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("bad json %q: %v", out.String(), err)
	}
	if resp.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("event = %q", resp.HookSpecificOutput.HookEventName)
	}
	ctx := resp.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "déjà vu:") && !strings.Contains(ctx, "deja found prior sessions") {
		t.Fatalf("context missing narration lead: %q", ctx)
	}
	if len(ctx) > promptHookBudget+256 {
		t.Fatalf("injection too large: %d", len(ctx))
	}
}

func TestHookPromptSilentPaths(t *testing.T) {
	withStatsStores(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{
		"no meaningful terms": `{"prompt":"ok do it"}`,
		"no matches":          `{"prompt":"quetzalcoatl zeppelin framework meltdown"}`,
		"empty":               `{}`,
		"garbage":             `not json at all`,
	} {
		var out bytes.Buffer
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(prompt), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.Len() != 0 {
			t.Fatalf("%s: expected silence, got %q", name, out.String())
		}
	}
}

func TestPromptSearchTerms(t *testing.T) {
	got := prompt.Terms("Why is the connection pool exhausted again in the gateway???")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "connection") || !strings.Contains(joined, "gateway") {
		t.Fatalf("terms = %v", got)
	}
	// Short words used to be dropped on the theory that they identify themes
	// rather than tasks. `deja bench prompt` says otherwise: keeping them
	// takes answered questions from 2/12 to 7/12 with precision unchanged and
	// no false fire, because "pool" is exactly what names the session someone
	// is asking about. Noise is held back by the two-term overlap gate, not
	// by a length floor.
	if !strings.Contains(joined, "pool") {
		t.Fatalf("short identifier dropped: %v", got)
	}
	if len(prompt.Terms("a of to")) != 0 {
		t.Fatal("stop words must not produce terms")
	}
}

// limitStore writes a Claude session in alpha that ended on last, ten minutes
// ago, written the way Claude Code writes a harness notice.
func limitStore(t *testing.T, last string) string {
	t.Helper()
	withStatsStores(t)
	at := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-alpha", "lim.jsonl"), "lim", []string{
		`{"type":"user","sessionId":"lim","cwd":"/tmp/alpha","timestamp":"` + at + `","message":{"role":"user","content":"continue the export rewrite"}}`,
		`{"type":"assistant","sessionId":"lim","cwd":"/tmp/alpha","timestamp":"` + at + `","isApiErrorMessage":true,"message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":` + mustJSON(t, last) + `}]}}`,
	})
	cwd := filepath.Join(t.TempDir(), "tmp", "alpha")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return cwd
}

// limitSideSession writes a session updated a minute ago that ended normally.
func limitSideSession(t *testing.T, harness, project, id string) {
	t.Helper()
	at := time.Now().Add(-time.Minute).UTC()
	switch harness {
	case "claude":
		stamp := at.Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-"+project, id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/tmp/` + project + `","timestamp":"` + stamp + `","message":{"role":"user","content":"look at the logs"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/tmp/` + project + `","timestamp":"` + stamp + `","message":{"role":"assistant","content":"the logs are clean"}}`,
		})
	case "codex":
		writeCodexRolloutIn(t, os.Getenv("DEJA_CODEX_ROOT"), id, "/tmp/"+project, at, "look at the logs", "the logs are clean")
	}
}

func writeCodexRolloutIn(t *testing.T, root, id, cwd string, at time.Time, question, answer string) {
	t.Helper()
	stamp := at.Format(time.RFC3339)
	path := filepath.Join(root, "sessions", at.Format("2006"), at.Format("01"), at.Format("02"),
		"rollout-"+at.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"type":"session_meta","timestamp":"` + stamp + `","payload":{"id":"` + id + `","session_id":"` + id + `","cwd":` + mustJSON(t, cwd) + `}}`,
		`{"type":"message","timestamp":"` + stamp + `","payload":{"role":"user","content":` + mustJSON(t, question) + `}}`,
		`{"type":"message","timestamp":"` + stamp + `","payload":{"role":"assistant","content":` + mustJSON(t, answer) + `}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func limitTipAt(t *testing.T, cwd string, self []string, starting string) string {
	t.Helper()
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	return limitHandoffTip(index.DefaultDir(), cwd, self, func() string { return starting })
}

// The wordings Claude Code prints, not a phrase made up for the test: the
// session form is the one the 5-hour window shows, and none of the first
// three matched before #4758.
func TestLimitHandoffTipRealWordings(t *testing.T) {
	for _, last := range []string{
		"You've hit your session limit · resets 2:40am (UTC)",
		"You've hit your limit · resets 3pm",
		"You've hit your weekly limit · resets 10am (UTC)",
		"You've reached your Fable limit. Run /usage-credits to continue or switch models with /model.",
		"API Error: 529 Overloaded. This is a server-side issue, usually temporary — try again in a moment.",
	} {
		t.Run(last[:20], func(t *testing.T) {
			cwd := limitStore(t, last)
			tip := limitTipAt(t, cwd, nil, "codex")
			if !strings.Contains(tip, "claude session") || !strings.Contains(tip, "deja handoff") {
				t.Fatalf("%q: tip = %q", last, tip)
			}
		})
	}
	// Control: the same session ending on an answer says nothing.
	cwd := limitStore(t, "Fixed: the exporter releases the lock before the upload.")
	if tip := limitTipAt(t, cwd, nil, "codex"); tip != "" {
		t.Fatalf("a session that ended normally got %q", tip)
	}
}

// A newer session in another project, or one from the harness that is
// starting, used to be "the newest session" and hid the limited one.
func TestLimitHandoffTipNotHiddenByParallelSessions(t *testing.T) {
	cwd := limitStore(t, "You've hit your session limit · resets 2:40am (UTC)")
	limitSideSession(t, "claude", "beta", "other-project")
	limitSideSession(t, "codex", "alpha", "codex-parallel")
	if tip := limitTipAt(t, cwd, []string{"codex-start"}, "codex"); !strings.Contains(tip, "claude session") {
		t.Fatalf("tip = %q", tip)
	}
	// Control: started from Claude, the newest other-harness session is the
	// codex one, which ended normally.
	if tip := limitTipAt(t, cwd, []string{"claude-start"}, "claude"); tip != "" {
		t.Fatalf("a Claude start was told about its own harness's limit: %q", tip)
	}
}

// Resuming the limited session is not a reason to tell it about itself.
func TestLimitHandoffTipSkipsTheStartingSession(t *testing.T) {
	cwd := limitStore(t, "You've hit your session limit · resets 2:40am (UTC)")
	if tip := limitTipAt(t, cwd, []string{"lim"}, ""); tip != "" {
		t.Fatalf("the starting session was read as the limited one: %q", tip)
	}
	if tip := limitTipAt(t, cwd, nil, ""); tip == "" {
		t.Fatal("control: with nothing excluded the limited session is read")
	}
}

func TestSSHSyncTipThresholdAndOnce(t *testing.T) {
	withStatsStores(t)
	ss := make([]model.Session, 0, 6)
	for i := range 6 {
		ss = append(ss, model.Session{ID: strconv.Itoa(i), Messages: []model.Message{{Role: "user", Text: "run ssh devbox and check"}}})
	}
	tip := sshSyncTip(index.DefaultDir(), ss)
	if !strings.Contains(tip, "deja sync ssh") {
		t.Fatalf("tip = %q", tip)
	}
	if again := sshSyncTip(index.DefaultDir(), ss); again != "" {
		t.Fatalf("tip must show once, got %q", again)
	}
	// Below threshold: silent (fresh sentinel dir).
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "idx"))
	if tip := sshSyncTip(index.DefaultDir(), ss[:2]); tip != "" {
		t.Fatalf("below threshold tip = %q", tip)
	}
}

func TestHookPromptCitationAndDedupe(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "citefix.jsonl"), "citefix", []string{
		`{"type":"user","sessionId":"citefix","timestamp":"` + old + `","message":{"role":"user","content":"the exporter_batch job drops rows at utc_midnight"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	in := `{"prompt":"exporter_batch dropping rows at utc_midnight again","session_id":"agent-1"}`
	var out bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `add as the last line of your reply: \"déjà vu: the exporter_batch job drops rows at utc_midnight (claude, `) || !strings.Contains(got, "deja:citefix) — reusing it.") {
		t.Fatalf("opener line missing: %q", got)
	}
	// Last, after the digest: asked for at the top of the block the complete
	// sentence was said in none of 12 tool-using replies, after it in 10.
	if strings.LastIndex(got, "add as the last line of your reply:") < strings.LastIndex(got, "citefix` · ") {
		t.Fatalf("opener line does not follow the digest: %q", got)
	}
	// The answer comes first: a bare "If it helped, say" was the whole reply
	// of a small local model in 10 of 10 runs (#5664).
	if !strings.Contains(got, "Answer the user's question in full. Then, only if this history helped,") {
		t.Fatalf("the credit line is not asked for after the answer: %q", got)
	}
	// Same session asks again: the same memory must not be re-injected.
	var out2 bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out2); err != nil {
		t.Fatal(err)
	}
	if out2.Len() != 0 {
		t.Fatalf("repeat injection for same session: %q", out2.String())
	}
	// A different agent session in the same project does not get it again.
	//
	// This assertion used to read the other way, and that is what #2038
	// measured: the cooldown was keyed on the agent session id alone, so every
	// new session started blank and asked for the same memory. Six weeks of a
	// real log showed 937 per-prompt injections drawn from 74 distinct
	// sessions — 92% repeats, one session served 110 times. The agent is told
	// to say nothing about a recall that did not help, so those servings bought
	// silence.
	var out3 bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(`{"prompt":"exporter_batch utc_midnight rows again","session_id":"agent-2"}`), &out3); err != nil {
		t.Fatal(err)
	}
	if out3.Len() != 0 {
		t.Fatalf("a second agent session in the same project was handed the same memory: %q", out3.String())
	}

	// The cooldown not reaching into another project is pinned as a unit in
	// TestTheCooldownOutlivesTheAgentSession; it cannot be shown here, because
	// per-prompt recall is project-scoped and a different directory has no
	// matching history to withhold in the first place.
}

func TestAgentCreditsCountedFromIndex(t *testing.T) {
	now := time.Now()
	ss := []model.Session{{ID: "a", Messages: []model.Message{
		{Role: "assistant", Text: "deja-vu recalled: jwt fix — reusing it.", Time: now},
		{Role: "assistant", Text: "deja-vu recalled: old one", Time: now.Add(-9 * 24 * time.Hour)},
		{Role: "user", Text: "deja-vu recalled should not count from users"},
		{Role: "assistant", Text: "Déjà vu: you asked this on Aug 26 in opencode; it was settled as a vault lease (deja:ses_fc145)", Time: now},
		// The phrase without the id is a sentence about the tool, not a credit.
		{Role: "assistant", Text: "the déjà vu line is rate-limited per session", Time: now},
		{Role: "user", Text: "déjà vu: users do not credit (deja:abc)", Time: now},
	}}}
	r := stats.Build(ss, now)
	if r.AgentCredits != 3 || r.WeekCredits != 2 {
		t.Fatalf("credits = %d/%d, want 3/2", r.AgentCredits, r.WeekCredits)
	}
}

func TestHookPromptRequiresRealOverlap(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	// A session sharing exactly ONE informative term with the prompt.
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-gamma", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + old + `","message":{"role":"user","content":"tune the quetzalcoatl dashboard colors"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "gamma")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"quetzalcoatl deploy pipeline retries failing"}`)
	if err := runHookPrompt(index.DefaultDir(), in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "deja-vu — ") {
		t.Fatalf("single-term overlap must not claim deja vu:\n%s", out.String())
	}
}

// A question whose whole subject is one rare word still gets the memory, not a
// pointer to it. Withholding it turned the answer on whether some ordinary word
// of the question happened to appear in that session too: measured on a real
// store, "напомни, что мы решали про X" returned content 63% of the time and
// "what did we decide about X" never — the same subject in the same session. On
// a store without marathon sessions the crisper phrasing returned content in 0
// of 78. The "you have been here" line stays at two terms, which is what the
// test above guards.
func TestOneRareWordStillGetsTheMemory(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-delta", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","timestamp":"` + old + `","message":{"role":"user","content":"the quetzalcoatl stampede was fixed by jittering the retry"}}`,
	})
	padStoreWithOtherWork(t, claudeRoot)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "delta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"quetzalcoatl"}`)
	if err := runHookPrompt(index.DefaultDir(), in, &out); err != nil {
		t.Fatal(err)
	}
	// A pointer names the topic in full, so checking for the words is not
	// enough — the quoted transcript is what separates the memory from a
	// pointer to it.
	if !strings.Contains(out.String(), "- User:") {
		t.Fatalf("one rare word earned a pointer instead of the memory:\n%s", out.String())
	}
}

func TestDejaVuTopicSkipsHarnessPlumbing(t *testing.T) {
	s := model.Session{
		Harness: "codex", ID: "x", Project: "app",
		Title: `# AGENTS.md instructions <INSTRUCTIONS> <!-- deja guidance:start -->`,
		Messages: []model.Message{
			{Role: "user", Text: `# AGENTS.md instructions <INSTRUCTIONS> <!-- deja guidance:start -->`},
			{Role: "user", Text: "why does the exporter drop rows at midnight"},
		},
	}
	if got := dejaVuTopic(s); got != "why does the exporter drop rows at midnight" {
		t.Fatalf("topic = %q", got)
	}
	line := dejaVuLine(s)
	if strings.Contains(line, "AGENTS.md") {
		t.Fatalf("plumbing leaked into deja vu line: %q", line)
	}
	// The fourth term is never named.
	withWhy := dejaVuLine(s, "exporter", "midnight", "rows", "fourth")
	if !strings.HasPrefix(withWhy, "deja-vu — from Codex CLI: ") || !strings.Contains(withWhy, "(via exporter") || strings.Contains(withWhy, "fourth") {
		t.Fatalf("via-terms missing or uncapped: %q", withWhy)
	}
	if w := termwidth.Columns(withWhy); w > noteWidth {
		t.Fatalf("the line is %d columns, over %d: %q", w, noteWidth, withWhy)
	}
	// A quote with quotes in it is measured as printed, escapes included, and
	// a 39-character identifier as the first term is cut to fit, not dropped.
	quoted := model.Session{ID: "q", Project: "app", Messages: []model.Message{{Role: "user", Text: `why does "make test" fail on the "replica" path after the nightly exporter runs`}}}
	ident := "TestReplicaExporterDropsRowsAtMidnight1"
	got := dejaVuLine(quoted, ident, "exporter")
	if w := termwidth.Columns(got); w > noteWidth {
		t.Fatalf("the line is %d columns, over %d: %q", w, noteWidth, got)
	}
	if !strings.Contains(got, `\"make test\"`) || !strings.Contains(got, "(via TestReplica") {
		t.Fatalf("the quote or the first term is missing: %q", got)
	}
	// The order is checked against the quote as cut: a term that only the cut
	// tail held is news to the reader and goes first.
	tail := model.Session{ID: "t", Project: "app", Updated: time.Now(), Messages: []model.Message{{Role: "user", Text: "the exporter drops the last batch of rows every night at midnight, only on the replica"}}}
	if got := dejaVuLine(tail, "exporter", "replica"); !strings.Contains(got, "(via replica") || strings.Contains(got, "on the replica\"") {
		t.Fatalf("a term cut out of the quote was not put first: %q", got)
	}
	short := model.Session{ID: "z", Project: "app", Messages: []model.Message{{Role: "user", Text: "drain the queue"}}}
	if got := dejaVuLine(short, "queue", "grace", "deploy"); !strings.HasSuffix(got, "(via grace, deploy, queue)") {
		t.Fatalf("with room, all three terms, the ones the quote lacks first: %q", got)
	}
	junk := model.Session{Harness: "codex", ID: "y", Project: "app",
		Title:    `<environment_context> <cwd>/x</cwd>`,
		Messages: []model.Message{{Role: "user", Text: `{"type":"init"}`}}}
	if got := dejaVuLine(junk); got != "" {
		t.Fatalf("all-plumbing session must yield no visible line, got %q", got)
	}
}

func TestHookPromptSkipsMarathonSessions(t *testing.T) {
	hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	lines := make([]string, 0, dejaVuMaxMessages+10)
	for i := range dejaVuMaxMessages + 10 {
		lines = append(lines, `{"type":"user","sessionId":"hay","timestamp":"`+old+`","message":{"role":"user","content":"quetzalcoatl stampede msg `+strconv.Itoa(i)+`"}}`)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-hay", "hay.jsonl"), "hay", lines)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-hay", "focus.jsonl"), "focus", []string{
		`{"type":"user","sessionId":"focus","timestamp":"` + old + `","message":{"role":"user","content":"the quetzalcoatl stampede fix: jittered ttl"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "hay")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"quetzalcoatl stampede regression again","session_id":"s-new"}`)
	if err := runHookPrompt(index.DefaultDir(), in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "deja-vu — ") {
		t.Fatalf("focused session must fire:\n%s", got)
	}
	if strings.Contains(got, "msg 5") {
		t.Fatalf("marathon session leaked into injection:\n%s", got)
	}
}

func TestDejaVuLineCooldown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if !dejaVuLineDue(dir, "s1") {
		t.Fatal("first call must be due")
	}
	if dejaVuLineDue(dir, "s1") {
		t.Fatal("second call within cooldown must be suppressed")
	}
}

// techTerm rejects every rune above 127, so a Russian prompt without an ASCII
// identifier used to yield no terms and auto-recall could never fire for it —
// the same hole CJK had, in the language this project is written from.
func TestPromptSearchTermsCyrillic(t *testing.T) {
	got := prompt.Terms("почему падает индексация кириллицы")
	if len(got) < 2 {
		t.Fatalf("Russian prompt yielded %v, recall cannot fire", got)
	}
	for _, junk := range []string{"почему", "нужно", "сделать"} {
		for _, g := range got {
			if g == junk {
				t.Errorf("closed-class word %q became a search term: %v", junk, got)
			}
		}
	}
	// Short words carry no signal.
	if terms := prompt.Terms("а он там был"); len(terms) != 0 {
		t.Errorf("short Russian words became terms: %v", terms)
	}
	// A prompt mixing Russian prose with an identifier keeps both.
	mixed := prompt.Terms("почему падает openBucketDir на кириллице")
	var hasIdent, hasWord bool
	for _, g := range mixed {
		if g == "openbucketdir" {
			hasIdent = true
		}
		if g == "кириллице" || g == "падает" {
			hasWord = true
		}
	}
	if !hasIdent || !hasWord {
		t.Errorf("mixed prompt lost one side: %v", mixed)
	}
}

// Kimi sends the prompt as content parts, Claude Code as a string. Reading
// only one shape is indistinguishable from recall finding nothing.
func TestPromptHookAcceptsBothPromptShapes(t *testing.T) {
	for name, payload := range map[string]string{
		"string": `{"prompt":"openBucketDir makeslice panic"}`,
		"parts":  `{"prompt":[{"type":"text","text":"openBucketDir makeslice"},{"type":"text","text":"panic"}]}`,
	} {
		var in promptHookInput
		if err := json.Unmarshal([]byte(payload), &in); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(string(in.Prompt), "openBucketDir") || !strings.Contains(string(in.Prompt), "panic") {
			t.Fatalf("%s: prompt = %q", name, in.Prompt)
		}
	}
	// An unfamiliar shape means no prompt, not a hook that errors out.
	var in promptHookInput
	if err := json.Unmarshal([]byte(`{"prompt":{"weird":true}}`), &in); err != nil {
		t.Fatalf("unknown shape returned an error: %v", err)
	}
	if in.Prompt != "" {
		t.Fatalf("unknown shape produced %q", in.Prompt)
	}
}

// heldPipe delivers head and then keeps the pipe open, the way a host that
// never closes the hook's stdin does.
type heldPipe struct {
	head []byte
	stop chan struct{}
}

func (h *heldPipe) Read(p []byte) (int, error) {
	if len(h.head) > 0 {
		n := copy(p, h.head)
		h.head = h.head[n:]
		return n, nil
	}
	<-h.stop
	return 0, io.EOF
}

// This hook runs on every user message, so a host holding stdin open stalls
// every turn until the harness kills it (#846).
func TestHookPromptDoesNotWaitForTheHostToCloseStdin(t *testing.T) {
	withStatsStores(t)
	for name, head := range map[string]string{
		"silent":    "",
		"truncated": `{"session_id":"s","prompt":"gateway_timeout on the`,
		"complete":  `{"session_id":"s","prompt":"gateway_timeout on the reconnect_loop"}`,
	} {
		t.Run(name, func(t *testing.T) {
			stop := make(chan struct{})
			t.Cleanup(func() { close(stop) })
			done := make(chan error, 1)
			go func() {
				done <- runHookPrompt(index.DefaultDir(), &heldPipe{head: []byte(head), stop: stop}, io.Discard)
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Minute):
				t.Fatal("hook still blocked on stdin after 2m")
			}
		})
	}
}

// A payload can be complete on the wire while the pipe stays open behind it;
// giving up on the deadline must not throw away what already arrived.
func TestReadHookPayloadKeepsWhatArrivedBeforeTheDeadline(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	for _, payload := range []string{
		`{"session_id":"s","prompt":"gateway_timeout"}`,
		// Half a payload is kept too: the deadline decides when to stop
		// waiting, not what to do with what is already in hand.
		`{"session_id":"s","prompt":"gateway_ti`,
	} {
		got := readHookPayload(&heldPipe{head: []byte(payload), stop: stop}, 200*time.Millisecond)
		if string(got) != payload {
			t.Fatalf("payload = %q, want %q", got, payload)
		}
	}
}

// The deadline is the fallback, not the price of admission: a host that sends
// the whole payload and leaves the pipe open must not pay it on every message.
func TestReadHookPayloadReturnsAsSoonAsTheValueIsWhole(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	payload := `{"session_id":"s","prompt":"gateway_timeout"}` + "\n"
	start := time.Now()
	got := readHookPayload(&heldPipe{head: []byte(payload), stop: stop}, 2*time.Second)
	if string(got) != payload {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waited %v for a payload that had already arrived", elapsed)
	}
}

// A payload written with a UTF-8 byte order mark was waited out to the
// deadline and then dropped, because no JSON decoder takes the mark.
func TestReadHookPayloadDropsAByteOrderMark(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	payload := `{"session_id":"s","prompt":"gateway_timeout"}`
	start := time.Now()
	got := readHookPayload(&heldPipe{head: append(append([]byte(nil), utf8BOM...), payload...), stop: stop}, 2*time.Second)
	if string(got) != payload {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waited %v for a payload that had already arrived", elapsed)
	}
}

// Kept reading past a brace that closes nothing, or the deadline would fire on
// every payload with a nested object in it.
func TestReadHookPayloadWaitsOutAnInnerBrace(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	payload := `{"prompt":[{"type":"text","text":"gateway_timeout"}]}`
	got := readHookPayload(&splitPipe{parts: []string{
		`{"prompt":[{"type":"text","text":"gateway_timeout"}`, `]}`,
	}, stop: stop}, 2*time.Second)
	if string(got) != payload {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

// splitPipe hands over one part per Read and then holds the pipe open.
type splitPipe struct {
	parts []string
	stop  chan struct{}
}

func (s *splitPipe) Read(p []byte) (int, error) {
	if len(s.parts) > 0 {
		n := copy(p, s.parts[0])
		if n < len(s.parts[0]) {
			s.parts[0] = s.parts[0][n:]
			return n, nil
		}
		s.parts = s.parts[1:]
		return n, nil
	}
	<-s.stop
	return 0, io.EOF
}

// json.Unmarshal rejects the whole buffer over one byte after the object, so
// decoding it that way turned a host with a chatty pipe into a host with no
// memory. Recall must survive what follows the value (#846).
func TestHookPromptSurvivesBytesAfterThePayload(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "trail.jsonl"), "trail", []string{
		`{"type":"user","sessionId":"trail","timestamp":"` + old + `","message":{"role":"user","content":"gateway_timeout on the reconnect_loop keeps dropping heartbeats"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	payload := `{"prompt":"seeing gateway_timeout in the reconnect_loop again"}`
	for _, trailer := range []string{"", "\n", "\x00", "\n" + `{"prompt":"next"}`} {
		var out bytes.Buffer
		if err := runHookPrompt(index.DefaultDir(), strings.NewReader(payload+trailer), &out); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatalf("trailer %q lost the recall", trailer)
		}
	}
}

// The one line a person reads claimed they had done work that arrived from
// another machine, while the context block below it labelled the same session
// `imported:` (#1001).
func TestDejaVuLineDoesNotClaimAPeersWorkAsYours(t *testing.T) {
	local := model.Session{ID: "loc", Harness: "codex", Project: "tmp/w16", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "the ticker window stays at 30s"}}}
	if got := dejaVuLine(local, "ticker"); !strings.HasPrefix(got, "deja-vu — from Codex CLI, today: ") {
		t.Errorf("a local session does not name its agent and age: %q", got)
	}
	peer := local
	peer.Project = "imported:tmp/w16"
	got := dejaVuLine(peer, "ticker")
	if !strings.HasPrefix(got, "deja-vu — from Codex CLI on another machine, today: ") {
		t.Errorf("a session from another machine is claimed as the reader's own: %q", got)
	}
	unknown := peer
	unknown.Harness = ""
	if line := dejaVuLine(unknown, "ticker"); !strings.HasPrefix(line, "deja-vu — from another machine, today: ") {
		t.Errorf("with no agent known the origin is still said: %q", line)
	}
	if !strings.Contains(got, "another machine") {
		t.Errorf("the line does not say where the session came from: %q", got)
	}
	// The rest of the line — the topic and why it fired — is unchanged.
	if !strings.Contains(got, "ticker window") || !strings.Contains(got, "via ticker") {
		t.Errorf("the line lost what it is for: %q", got)
	}
}

// The headline names three of the query's terms as the reason. They have to be
// terms the block underneath actually carries: In real-world stores, the
// block opened on a line carrying a term the product used in 94 cases of 94,
// while the three words named above it agreed only 71 times.
func TestTheHeadlineNamesTermsTheBlockCarries(t *testing.T) {
	block := "- User: the pgbouncer prepared statement failures came back\n"
	terms := []string{"deploy", "reboot", "pgbouncer", "statement"}
	got := viaTerms(block, terms)
	if len(got) == 0 {
		t.Fatal("no terms chosen")
	}
	if !search.TextCarriesTerm(block, got[0]) {
		t.Fatalf("headline leads with %q, which the block does not carry", got[0])
	}
	// Terms the block does not carry still get named once the carried ones run
	// out — the line says what the question was about, not only what matched.
	if len(got) != 3 {
		t.Fatalf("via = %v, want three terms", got)
	}
}

// A Claude session that is just starting has no indexed record and maybe no
// transcript yet; where its transcript will live still names the harness.
func TestStartingHarnessFromTranscriptPath(t *testing.T) {
	withStatsStores(t)
	p := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-alpha", "fresh.jsonl")
	if got := startingHarness(index.DefaultDir(), "fresh", p); got != "claude" {
		t.Fatalf("startingHarness = %q, want claude", got)
	}
	if got := startingHarness(index.DefaultDir(), "nowhere", ""); got != "" {
		t.Fatalf("startingHarness with nothing to go on = %q", got)
	}
}

func TestNamedLeadTermsAndSingleSubjectDigest(t *testing.T) {
	idf := map[string]float64{"telemetry": 5.2, "decide": 2.1, "kestrel": 5.0, "escrow": 5.0}
	if got := namedLeadTerms([]string{"telemetry", "decide"}, idf, 5.2); len(got) != 1 || got[0] != "telemetry" {
		t.Fatalf("namedLeadTerms single = %v, want [telemetry]", got)
	}
	if got := namedLeadTerms([]string{"kestrel", "escrow", "decide"}, idf, 1e9); len(got) != 2 {
		t.Fatalf("namedLeadTerms multi = %v, want [kestrel escrow]", got)
	}
}

// seedDejaVuStore lays out a session the déjà vu hook will recall, and returns
// the index directory.
func seedDejaVuStore(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "vuterm", []string{
		`{"type":"user","sessionId":"vuterm","timestamp":"` + old +
			`","message":{"role":"user","content":"pgbouncer runs in transaction mode and prepared statements are off"}}`,
	})
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	return dir
}

// dejaVuRows reads back what the hook recorded.
func dejaVuRows(t *testing.T, dir string) []usage.Snapshot {
	t.Helper()
	b, err := os.ReadFile(usage.SnapshotPath(dir))
	if err != nil {
		return nil
	}
	var out []usage.Snapshot
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var snap usage.Snapshot
		if json.Unmarshal([]byte(line), &snap) != nil {
			continue
		}
		if snap.Kind == usage.KindDejaVu {
			out = append(out, snap)
		}
	}
	return out
}

// A payload this hook cannot read at all injects nothing: the prompt is what
// it recalls from, and there is none. That covers the whole-payload failures —
// what it does not cover is a decode that fails on a later field, which is
// where the real hole was (see the test below).
func TestAPayloadTheDejaVuHookCannotReadInjectsNothingAtAll(t *testing.T) {
	for _, c := range []struct{ name, payload string }{
		{"prose", "not a payload at all"},
		{"truncated", `{"prompt":"pgbouncer transaction mode prepared statements"`},
		{"binary", "\x00\x01\x02"},
		{"a list", "[1, 2]"},
		{"empty", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := seedDejaVuStore(t)
			var out bytes.Buffer
			if err := runHookPromptMode(dir, strings.NewReader(c.payload), &out, true); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "transaction mode") {
				t.Errorf("a payload deja could not read still injected:\n%s", out.String())
			}
			if rows := dejaVuRows(t, dir); len(rows) != 0 {
				t.Errorf("a payload deja could not read left %d row(s): %+v", len(rows), rows)
			}
		})
	}
}

// And a payload it read is recorded with whatever receiver it named.
func TestTheDejaVuHookRecordsTheReceiverThePayloadNamed(t *testing.T) {
	for _, c := range []struct{ name, payload, into string }{
		{"named", `{"prompt":"pgbouncer transaction mode prepared statements","session_id":"ses_vu"}`, "ses_vu"},
		{"no session named", `{"prompt":"pgbouncer transaction mode prepared statements"}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := seedDejaVuStore(t)
			var out bytes.Buffer
			if err := runHookPromptMode(dir, strings.NewReader(c.payload), &out, true); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "transaction mode") {
				t.Fatalf("the hook injected nothing:\n%s", out.String())
			}
			rows := dejaVuRows(t, dir)
			if len(rows) != 1 {
				t.Fatalf("want one déjà vu row, got %d", len(rows))
			}
			if rows[0].Unreadable {
				t.Errorf("a payload deja read was reported as broken: %+v", rows[0])
			}
			if rows[0].Into != c.into {
				t.Errorf("into = %q, want %q", rows[0].Into, c.into)
			}
		})
	}
}

// The hole #2773 named, found by measurement rather than by reading: a decode
// that fails on a *later* field reads the prompt, injects on it, and leaves a
// row saying the host named no session — for a payload that named one deja
// could not read. Identical, in the log, to a host that named none.
func TestADecodeThatFailsAfterThePromptIsRecordedAsUnreadable(t *testing.T) {
	for _, payload := range []string{
		`{"prompt":"pgbouncer transaction mode prepared statements","session_id":123}`,
		`{"prompt":"pgbouncer transaction mode prepared statements","session_id":{"a":1}}`,
		`{"prompt":"pgbouncer transaction mode prepared statements","cwd":7}`,
	} {
		t.Run(payload, func(t *testing.T) {
			dir := seedDejaVuStore(t)
			var out bytes.Buffer
			if err := runHookPromptMode(dir, strings.NewReader(payload), &out, true); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "transaction mode") {
				t.Fatalf("the premise moved — this payload is meant to inject:\n%s", out.String())
			}
			rows := dejaVuRows(t, dir)
			if len(rows) != 1 {
				t.Fatalf("want one déjà vu row, got %d", len(rows))
			}
			if !rows[0].Unreadable {
				t.Errorf("the row reads as a host that named no session: %+v", rows[0])
			}
		})
	}
}

// The per-prompt hook asks the ranking for a fixed number of candidates and
// then filters them: the trust policy can withhold one, a weak match is
// dropped, a marathon session can narrow to nothing, the session being written
// is never recalled to itself, and anything already injected this conversation
// is skipped. Only two survivors are ever used.
//
// The window was sized against two of those — its comment said "to leave room
// after excluding the current/too-fresh sessions" — and three more arrived
// afterwards. When the filtered ones fill it, the answer below them is never
// looked at and the hook says nothing at all, on a machine where it is
// installed and reported healthy.
//
// Staged on the weak-match filter, which needs no state and is always on: nine
// sessions carry the question's ordinary words and outrank the one session
// carrying its rare one, exactly the way a plainly worded question ranks.
func TestTheHookLooksPastTheCandidatesItWillNotUse(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)

	write := func(id, text string) {
		writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + old +
				`","message":{"role":"user","content":"` + text + `"}}`,
		})
	}
	// Enough history for the shared term to still count as rare.
	for i := range 200 {
		write(fmt.Sprintf("noise%03d", i), "notes on the weekly planning meeting and the roadmap")
	}
	// Ten sessions that all answer the question, so the ranking has no reason to
	// prefer one of them and every one clears the bar to be injected.
	shown := make([]string, 0, 9)
	for i := range 10 {
		id := fmt.Sprintf("probe%02d", i)
		write(id, "the hydrostat_probe reported a stale pressure reading again")
		if i < 9 {
			shown = append(shown, id)
		}
	}

	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "tmp", "beta")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	// Nine have already been shown in this conversation, which is the ordinary
	// state by the fourth or fifth prompt rather than a contrived one.
	var seen strings.Builder
	for _, id := range shown {
		fmt.Fprintf(&seen, "agent-1 %s\n", id)
	}
	if err := os.WriteFile(index.DefaultDir()+".hookseen", []byte(seen.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	in := strings.NewReader(`{"prompt":"stale pressure reading from the hydrostat_probe again","session_id":"agent-1"}`)
	if err := runHookPrompt(index.DefaultDir(), in, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("the candidates the hook cannot use filled its window and the one it could was never looked at: " +
			"nothing was recalled")
	}
	var resp struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("hook output is not the expected envelope: %v\n%s", err, out.String())
	}
	if !strings.Contains(resp.HookSpecificOutput.AdditionalContext, "hydrostat_probe") {
		t.Errorf("the session that answers the question was reached but not recalled:\n%s",
			resp.HookSpecificOutput.AdditionalContext)
	}
}

// WorkBuddy AI starts every chat in a fresh ~/WorkBuddy AI/<yyyy-mm-dd-hh-mm-ss>
// folder. Scoped by that folder, each chat was its own project and the second
// chat never recalled the first (#4735).
func TestWorkBuddySecondChatRecallsTheFirst(t *testing.T) {
	withStatsStores(t)
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "WorkBuddy AI")
	first := filepath.Join(root, "2026-10-05-14-52-46")
	second := filepath.Join(root, "2026-10-05-15-06-37")
	for _, d := range []string{first, second} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id := "72784911-4fa0-423d-b43f-685d8a6fa2b7"
	ts := time.Now().Add(-72 * time.Hour).UnixMilli()
	line := func(role, kind, text string) string {
		return `{"id":"` + role + `1","timestamp":` + jsonString(ts) + `,"type":"message","role":"` + role +
			`","content":[{"type":"` + kind + `","text":` + jsonString(text) + `}],"sessionId":"` + id + `","cwd":` + jsonString(first) + "}\n"
	}
	path := filepath.Join(home, ".workbuddy-ai", "projects", "WorkBuddy AI-2026-10-05-14-52-46", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := line("user", "input_text", "what backoff should the payments retry use") +
		line("assistant", "output_text", "We settled on jittered exponential backoff for the payments retry, capped at 30 seconds.")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	in := strings.NewReader(`{"session_id":"8407","cwd":` + jsonString(second) +
		`,"hook_event_name":"UserPromptSubmit","prompt":"What backoff did we decide on for the payments retry last time?","client":"WorkBuddy"}`)
	if err := runHookPromptMode(index.DefaultDir(), in, &out, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "jittered exponential backoff") {
		t.Fatalf("the second WorkBuddy chat recalled nothing from the first:\n%q", out.String())
	}
}

// The hook logged the transcript volume of every candidate it loaded, not of
// the sessions the digest kept. With three near-duplicate sessions the digest
// serves one and the log claimed all three — and that figure is the
// denominator of the "N× less context" line `deja stats` prints about itself.
func TestHookRawCountsOnlyTheSessionsItServed(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	// Near-duplicates by word set, so injection keeps the newest and drops
	// the rest. Sizes differ so the served figure is unambiguous.
	sessions := []struct {
		id    string
		when  string
		lines int
	}{
		{"old1", "2026-08-01T10:00:00Z", 4},
		{"old2", "2026-08-01T11:00:00Z", 3},
		{"new1", "2026-08-01T12:00:00Z", 1},
	}
	const text = "the retry loop in the ticker window backs off before the pool cap"
	var total int64
	for _, s := range sessions {
		body := ""
		var bodySb36 strings.Builder
		for range s.lines {
			bodySb36.WriteString(`{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + s.when + `","sessionId":"` + s.id + `","cwd":"/proj"}` + "\n")
			total += int64(len(text))
		}
		body += bodySb36.String()
		if err := os.WriteFile(filepath.Join(store, s.id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	digest, served, raw, _, _, _, _ := hookDigestResult(dir)
	if digest == "" || served == 0 {
		t.Skip("no memory to recall in this environment")
	}
	if served != 1 {
		t.Fatalf("fixture served %d sessions, want the near-duplicate filter to keep 1", served)
	}
	if want := int64(len(text)); raw != want {
		t.Errorf("raw = %d, want %d — the served session's transcript, not all %d bytes of candidates", raw, want, total)
	}
}

// #1048's line hangs on `indexDirWritable`, which probed the index directory's
// parent. A read-only index directory inside a writable parent — a cache
// directory owned by another user, a read-only subtree — was therefore read as
// writable, and the session start went out silent (#2499).
//
// What it must say there is not what it says for an unwritable parent: `deja
// index` rebuilds this one, because the build writes beside the directory and
// replaces it. The hook itself cannot start that rebuild — the sentinel it
// would write lives inside the read-only directory — so recall stays quiet
// until the command runs, which is what the statusline says for the same state
// (#2502).
func TestHookNamesAReadOnlyIndexDirInsideAWritableParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory anyway")
	}
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"zeppelin gasket"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	writeStaleFormatIndex(t, dir)

	// The index directory alone. Its parent stays writable, which is the whole
	// point: that is the shape the old probe could not see.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("the session start said nothing at all on an index that cannot answer and cannot be rebuilt")
	}
	var resp struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	if !strings.Contains(resp.SystemMessage, "recall is quiet until `deja index` rebuilds it") {
		t.Errorf("the hook does not say what this state is: %q", resp.SystemMessage)
	}
	if strings.Contains(resp.SystemMessage, "is not writable") {
		t.Errorf("the hook blames permissions where `deja index` rebuilds fine: %q", resp.SystemMessage)
	}
}

// An index this build cannot read, in a directory it cannot write. The line
// for that state hung off warmupJustRequested, whose sentinel lives inside the
// very directory that is read-only — so the hook went out empty on every
// session and nothing ever said why (#1048).
func TestHookNamesAnUnwritableIndexDirWithoutTheSentinel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not deny writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory anyway")
	}
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	parent := filepath.Dir(dir)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"zeppelin gasket"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	// An index a newer build left behind: readable, and unreadable by this one.
	writeStaleFormatIndex(t, dir)

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	// t.TempDir removes the tree afterwards and cannot do it through 0500.
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o700)
		_ = os.Chmod(dir, 0o700)
	})

	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("hook said nothing at all on an index that needs rebuilding in a read-only directory")
	}
	var resp struct {
		SystemMessage string `json:"systemMessage"`
	}
	if uerr := json.Unmarshal([]byte(out), &resp); uerr != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	if !strings.Contains(resp.SystemMessage, "is not writable") {
		t.Errorf("hook did not name the unwritable directory: %q", resp.SystemMessage)
	}

	// A healthy index in a writable directory keeps its silence: the notice
	// must not become something every session start carries.
	if cerr := os.Chmod(parent, 0o700); cerr != nil {
		t.Fatal(cerr)
	}
	if cerr := os.Chmod(dir, 0o700); cerr != nil {
		t.Fatal(cerr)
	}
	if _, cerr := captureRunStderr(t, "index"); cerr != nil {
		t.Fatal(cerr)
	}
	out, err = captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "is not writable") {
		t.Errorf("a healthy index was reported as unwritable: %q", out)
	}
}

// A read-only cache directory lets the rebuild be requested and then fail, so
// the hook promised recall "in a few seconds" on every session forever (#887).
func TestTheHookDoesNotPromiseARebuildItCannotDo(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny writes here")
	}
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_WARMUP_SENTINEL", "")
	// Never the real thing: on Windows a detached child holds deja.test.exe
	// open and `go test` fails to remove it after the run.
	oldSpawn := spawnWarmup
	spawnWarmup = func(_, _ string) error { return nil }
	t.Cleanup(func() { spawnWarmup = oldSpawn })
	cache := filepath.Join(tmp, "cache")
	dir := filepath.Join(cache, "index.db")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_INDEX_DIR", dir)
	// A build was asked for and has published nothing yet: without the probe
	// this is the state that prints the promise.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","message":{"role":"user","content":"pool exhausted"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"s1","cwd":"/proj"}`
	if err := os.WriteFile(filepath.Join(store, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(warmupStatusPath(dir))
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", filepath.Join(tmp, "elsewhere"))

	message := func() string {
		out, err := captureRun(t, "hook-context")
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			SystemMessage string `json:"systemMessage"`
		}
		if out == "" {
			return ""
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("hook output is not JSON: %q", out)
		}
		return resp.SystemMessage
	}

	if got := message(); !strings.Contains(got, "indexing your history") {
		t.Fatalf("a writable cache lost the promise: %q", got)
	}

	if err := os.Chmod(cache, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cache, 0o700) })
	got := message()
	if strings.Contains(got, "comes online in a few seconds") {
		t.Errorf("promised a rebuild that cannot run: %q", got)
	}
	if !strings.Contains(got, "is not writable") || !strings.Contains(got, cache) {
		t.Errorf("does not name what to change: %q", got)
	}
}

// While a build runs the digest is empty, and a machine with environment facts
// took an early return that never reached the "a build is running" branch: the
// whole post-upgrade rebuild passed in silence on exactly the stores where it
// takes longest (#927).
func TestHookReportsTheBuildEvenWhenItHasOnlyEnvironmentFacts(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One directory per project: a wall of one repository is no longer a fact
	// about the machine, and this block is built from other projects' walls.
	stores := make([]string, 0, environmentMinProjects)
	for i := range environmentMinProjects {
		store := filepath.Join(root, "-elsewhere"+strconv.Itoa(i))
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		stores = append(stores, store)
	}
	// Sessions from another project, each hitting the same missing tool: that
	// is what the environment block is built from, and none of it is this
	// project's memory.
	var b strings.Builder
	for i := range 4 {
		id := "env" + strconv.Itoa(i)
		b.Reset()
		b.WriteString(`{"type":"user","message":{"role":"user","content":"run the thing"},"timestamp":"2026-07-0` + strconv.Itoa(i+1) + `T10:00:00Z","sessionId":"` + id + `","cwd":"/elsewhere` + strconv.Itoa(i%environmentMinProjects) + `"}` + "\n")
		// Claude files tool results inside user messages, and friction is
		// counted from tool output only.
		b.WriteString(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"zsh:1: command not found: shellcheck"}]},"timestamp":"2026-07-0` + strconv.Itoa(i+1) + `T10:01:00Z","sessionId":"` + id + `","cwd":"/elsewhere` + strconv.Itoa(i%environmentMinProjects) + `"}` + "\n")
		if err := os.WriteFile(filepath.Join(stores[i%len(stores)], id+".jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// A build asked for just now, nothing published yet — the first session
	// after an upgrade.
	if err := os.WriteFile(filepath.Join(dir, "warmup.sentinel"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")

	out, err := captureRun(t, "hook-context")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		SystemMessage string `json:"systemMessage"`
		Hook          struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("hook output is not JSON: %q (%v)", out, err)
	}
	if !strings.Contains(resp.Hook.AdditionalContext, "command not found: shellcheck") {
		t.Fatalf("this is not the environment-facts path:\n%s", resp.Hook.AdditionalContext)
	}
	if !strings.Contains(resp.SystemMessage, "indexing your history") {
		t.Errorf("the rebuild passed in silence: %q", resp.SystemMessage)
	}
}

// The hook's digest is computed from the index, so refreshing it off a stale
// index re-served the same snapshot: a session that reversed an earlier
// decision stayed invisible to the agent for as long as the user went without
// running the CLI (#913).
func TestHookRefreshRebuildsTheIndexBeforeTheDigest(t *testing.T) {
	tmp := hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	store := filepath.Join(root, "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(sid, ts, text string) []byte {
		return []byte(`{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + ts + `","sessionId":"` + sid + `","cwd":"/proj"}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(store, "old.jsonl"), line("old", "2026-06-01T10:00:00Z", "poll interval set to 30 seconds"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The store moves on after the build: the decision above is reversed.
	if err := os.WriteFile(filepath.Join(store, "new.jsonl"), line("new", "2026-07-25T10:00:00Z", "reverted, the poll interval is back to 5 seconds"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := index.SessionCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != 1 {
		t.Fatalf("index holds %d sessions before the refresh, want 1", before)
	}

	t.Setenv("CLAUDE_PROJECT_DIR", "/proj")
	runHookRefresh(dir)

	after, err := index.SessionCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after != 2 {
		t.Errorf("index holds %d sessions after the refresh, want 2 — the reversal is still invisible", after)
	}
}
