package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/stats"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

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
	if !strings.Contains(ctx, "deja-vu recalled") && !strings.Contains(ctx, "deja found prior sessions") {
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

func TestLimitHandoffTip(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-alpha", "lim.jsonl"), "lim", []string{
		`{"type":"user","sessionId":"lim","timestamp":"` + now + `","message":{"role":"user","content":"continue please"}}`,
		`{"type":"assistant","sessionId":"lim","timestamp":"` + now + `","message":{"role":"assistant","content":"You have reached your usage limit reached for today"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	recent, err := index.Recent(index.DefaultDir(), 1)
	if err != nil || len(recent) == 0 {
		t.Fatalf("recent: %v %v", recent, err)
	}
	t.Logf("newest: id=%s updated=%v msgs=%d", recent[0].ID, recent[0].Updated, len(recent[0].Messages))
	tip := limitHandoffTip(index.DefaultDir())
	if !strings.Contains(tip, "usage limit") || !strings.Contains(tip, "deja handoff") {
		t.Fatalf("tip = %q", tip)
	}
}

func TestSSHSyncTipThresholdAndOnce(t *testing.T) {
	withStatsStores(t)
	var ss []model.Session
	for i := 0; i < 6; i++ {
		ss = append(ss, model.Session{ID: strconv.Itoa(i), Messages: []model.Message{{Role: "user", Text: "run ssh mini and check"}}})
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
	if !strings.Contains(out.String(), `If it helped, say: \"deja-vu recalled:`) {
		t.Fatalf("citation line missing: %q", out.String())
	}
	// Same session asks again: the same memory must not be re-injected.
	var out2 bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(in), &out2); err != nil {
		t.Fatal(err)
	}
	if out2.Len() != 0 {
		t.Fatalf("repeat injection for same session: %q", out2.String())
	}
	// A different agent session still gets it.
	var out3 bytes.Buffer
	if err := runHookPrompt(index.DefaultDir(), strings.NewReader(`{"prompt":"exporter_batch utc_midnight rows again","session_id":"agent-2"}`), &out3); err != nil {
		t.Fatal(err)
	}
	if out3.Len() == 0 {
		t.Fatal("fresh session should still receive the memory")
	}
}

func TestAgentCreditsCountedFromIndex(t *testing.T) {
	now := time.Now()
	ss := []model.Session{{ID: "a", Messages: []model.Message{
		{Role: "assistant", Text: "deja-vu recalled: jwt fix — reusing it.", Time: now},
		{Role: "assistant", Text: "deja-vu recalled: old one", Time: now.Add(-9 * 24 * time.Hour)},
		{Role: "user", Text: "deja-vu recalled should not count from users"},
	}}}
	r := stats.Build(ss, now)
	if r.AgentCredits != 2 || r.WeekCredits != 1 {
		t.Fatalf("credits = %d/%d, want 2/1", r.AgentCredits, r.WeekCredits)
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
	if strings.Contains(out.String(), "you have been here") {
		t.Fatalf("single-term overlap must not claim deja vu:\n%s", out.String())
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
	withWhy := dejaVuLine(s, "exporter", "midnight", "rows", "fourth")
	if !strings.Contains(withWhy, "via: exporter, midnight, rows") || strings.Contains(withWhy, "fourth") {
		t.Fatalf("via-terms missing or uncapped: %q", withWhy)
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
	var lines []string
	for i := 0; i < dejaVuMaxMessages+10; i++ {
		lines = append(lines, `{"type":"user","sessionId":"hay","timestamp":"`+old+`","message":{"role":"user","content":"quetzalcoatl stampede msg `+fmt.Sprint(i)+`"}}`)
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
	if !strings.Contains(got, "you have been here") {
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
			case <-time.After(5 * time.Second):
				t.Fatal("hook still blocked on stdin after 5s")
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
	local := model.Session{ID: "loc", Project: "tmp/w16", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "the ticker window stays at 30s"}}}
	if got := dejaVuLine(local, "ticker"); !strings.Contains(got, "you have been here") {
		t.Errorf("a local session lost the wording it has always had: %q", got)
	}
	peer := local
	peer.Project = "imported:tmp/w16"
	got := dejaVuLine(peer, "ticker")
	if strings.Contains(got, "you have been here") {
		t.Errorf("a session from another machine is claimed as the reader's own: %q", got)
	}
	if !strings.Contains(got, "another machine") {
		t.Errorf("the line does not say where the session came from: %q", got)
	}
	// The rest of the line — the topic and why it fired — is unchanged.
	if !strings.Contains(got, "ticker window") || !strings.Contains(got, "via: ticker") {
		t.Errorf("the line lost what it is for: %q", got)
	}
}

// The headline names three of the query's terms as the reason. They have to be
// terms the block underneath actually carries: measured on a real store, the
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
