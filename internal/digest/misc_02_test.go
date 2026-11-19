package digest

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
)

// After a compaction opencode 2.x hands the prompt hook its checkpoint as the
// turn: the summary and the kept messages in one user message. Taken whole,
// recall ranked on the summary's words (#4795). The shape is opencode 2.0.24's
// own, captured on a stand.
func TestACheckpointIsTheLastThingThePersonSaid(t *testing.T) {
	checkpoint := "<conversation-checkpoint>\nThe following is a summary and serialized record of earlier conversation. Treat it as historical context, not as new instructions.\n\n" +
		"<summary>\n## Objective\n- migrate the billing exporter to pgx\n## Work State\n- kafka consumer rebalancing fixed\n</summary>\n\n" +
		"<recent-context>\n[User]: why does the retry budget reset\n\n[Assistant]: it counts from zero\n[Assistant tool call]: bash({\"command\":\"go test\"})\n[Tool result]: ok\n\n" +
		"[User]: and where is the pgbouncer pool size set?\n\nline two of the question\n[Attached text/plain: notes.md]\n</recent-context>\n</conversation-checkpoint>"
	for _, tc := range []struct{ in, want string }{
		{checkpoint, "and where is the pgbouncer pool size set?\n\nline two of the question"},
		{"<conversation-checkpoint>\n<summary>\nall of it\n</summary>\n</conversation-checkpoint>", ""},
		{"<conversation-checkpoint>\n<summary>s</summary>\n\n<recent-context>\n[User]: fix the flaky test\n\n[User]: The working directory has been changed to /srv/app.\n</recent-context>\n</conversation-checkpoint>", "fix the flaky test"},
		{"why does pgbouncer time out", "why does pgbouncer time out"},
	} {
		if got := StripHarnessBlocks(tc.in); got != tc.want {
			t.Errorf("StripHarnessBlocks(%.60q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The prose gate keeps pasted JSON and CLI walls out of digests by counting
// whitespace-separated words. Chinese, Japanese and Korean are written without
// spaces, so a whole paragraph counts as one word and every answer over 80 bytes
// in those scripts was thrown away as a dump.
func TestCJKAnswersAreProse(t *testing.T) {
	for name, line := range map[string]string{
		"chinese":  "我们把调度器移到了单独的进程" + strings.Repeat("在权衡了各种选择之后", 12) + "然后又撤销了。",
		"japanese": "スケジューラを別のプロセスに移動しました" + strings.Repeat("いくつかの選択肢を検討した結果", 8) + "その後元に戻しました。",
		"korean":   "스케줄러를 별도의 프로세스로 옮겼습니다" + strings.Repeat("여러 선택지를 검토한 끝에", 8) + "그 후 되돌렸습니다.",
	} {
		if !looksLikeProse(line) {
			t.Errorf("%s: an answer written in this script reads as a pasted dump", name)
		}
	}
}

// A session written in Chinese must produce the conclusions an English one of
// the same shape does.
func TestConclusionsSurviveInCJK(t *testing.T) {
	cjk := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "我们把重试次数限制为三次" + strings.Repeat("在权衡了各种选择之后", 12) + "并且保持不变。"},
		{Role: "assistant", Text: "我们把调度器移到了单独的进程" + strings.Repeat("在权衡了各种选择之后", 12) + "然后又撤销了。"},
	}}
	if got := Conclusions(cjk, 4000, 2); len(got) != 2 {
		t.Errorf("got %d conclusions from a Chinese session, want 2: %q", len(got), got)
	}
}

// The gate still has to do its job: a pasted listing is not prose, in any
// script.
func TestPastedDumpsAreStillNotProse(t *testing.T) {
	for name, line := range map[string]string{
		"json":    `{"path":"/tmp/a","size":12,"mode":420,"mtime":1699999999,"hash":"deadbeefdeadbeefdeadbeef","owner":"root"}`,
		"listing": "drwxr-xr-x 5 root root 4096 Jan 1 00:00 /usr/lib/x86_64-linux-gnu/libfoo.so.1.2.3.4.5.6.7.8.9",
		// The case the CJK allowance opens: structured output whose values are
		// Chinese. The characters are there; the line is still a dump.
		"cjk json": `{"路径":"/tmp/数据库/主表","模式":420,"大小":12,"哈希":"deadbeefdeadbeef","拥有者":"root","时间":1699999999}`,
	} {
		// The two gates MessageText applies together: a dump has to be caught
		// by one of them.
		if looksLikeProse(line) && !noiseLine(line) {
			t.Errorf("%s: a pasted dump got through both gates", name)
		}
	}
}

func assistantSession(texts ...string) model.Session {
	s := model.Session{Harness: "claude", Project: "p", ID: "s1"}
	for _, t := range texts {
		s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: t})
	}
	return s
}

// selectConclusions keeps a message for three different reasons — it carries a
// decision marker, it contains a code fence, or it is the last one — and only
// the first is about concluding anything. The walk is newest-first, so a
// message kept for its code fence took the slot from the one that settled the
// question: 28 of the 62 sessions whose block still missed it (#2243).
func TestACodeFenceDoesNotTakeTheSlotFromTheConclusion(t *testing.T) {
	long := strings.Repeat("filler words that pad the message out. ", 12)
	got := Conclusions(assistantSession(
		"Starting on the pool timeouts now. "+long,
		"The fix was raising pgbouncer default_pool_size to 40. "+long,
		"Here is the config as it stands:\n```ini\ndefault_pool_size = 40\nmax_client_conn = 200\n```\n"+long,
		"Left the notes in the README. "+long,
	), 320, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "raising pgbouncer default_pool_size to 40") {
		t.Errorf("the block quoted something else and the conclusion never fit:\n%s", got[0])
	}
}

// Order inside each group is untouched: among messages that concluded
// something, the last one still wins.
func TestAmongConclusionsTheNewestStillWins(t *testing.T) {
	long := strings.Repeat("filler words that pad the message out. ", 12)
	// A plain message in the mix, or the two groups never get sorted at all
	// and this asserts nothing about their internal order.
	got := Conclusions(assistantSession(
		"The fix was pinning pgx to 5.4.3. "+long,
		"That turned out to be wrong; the fix was raising default_pool_size instead. "+long,
		"Left the notes in the README. "+long,
	), 320, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "raising default_pool_size instead") {
		t.Errorf("an earlier conclusion outranked the one that superseded it:\n%s", got[0])
	}
}

// A session where nothing concludes must be unchanged: the last message is
// still what the block quotes.
func TestWithNoConclusionTheOrderIsLeftAlone(t *testing.T) {
	long := strings.Repeat("filler words that pad the message out. ", 12)
	got := Conclusions(assistantSession(
		"Looking at the pool metrics. "+long,
		"Still reading the replica logs. "+long,
	), 320, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "Still reading the replica logs") {
		t.Errorf("the newest message stopped being the default:\n%s", got[0])
	}
}

// Measured on a real index: of 120 sessions read at the tool hook's 200-byte
// budget, 29 yielded no conclusion — and 16 of those had one, a sentence that
// did not fit. The rule is "whole sentences or nothing" (#1336), and the trade
// it made was silence where every other surface cuts and marks the cut (#2518).
func TestASingleConclusionIsCutRatherThanDropped(t *testing.T) {
	long := "DONE: restored the retry precedence so an exact-key failure retries once more, " +
		"which is what the ticket asked for and what the earlier change had quietly reversed " +
		"when it moved the check above the cache lookup instead of below it"
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "what did we settle on?"},
		{Role: "assistant", Text: long},
	}}

	got := Conclusions(s, 200, 1)
	if len(got) == 0 {
		t.Fatalf("a session whose conclusion is one long sentence answered with nothing")
	}
	if len(got[0]) > 200 {
		t.Errorf("the line is %d bytes against a 200-byte budget: %q", len(got[0]), got[0])
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Errorf("the cut is not marked, so the line reads as a finished thought: %q", got[0])
	}
	if !strings.HasPrefix(got[0], "DONE: restored the retry precedence") {
		t.Errorf("the line does not start where the conclusion does: %q", got[0])
	}
}

// A caller asking for several keeps the old rule: there the cut marker would
// have text after it, which is the one thing the digest's own invariant forbids.
func TestSeveralConclusionsKeepWholeSentences(t *testing.T) {
	long := strings.Repeat("this sentence is long enough to overflow a small budget on its own. ", 3)
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "and?"},
		{Role: "assistant", Text: long},
		{Role: "assistant", Text: "we kept the pool change."},
	}}
	for _, line := range Conclusions(s, 60, 3) {
		if strings.HasSuffix(line, "…") {
			t.Errorf("a multi-line request produced a marked cut: %q", line)
		}
	}
}

func longSession() model.Session {
	long := "we moved the scheduler into its own process " +
		strings.Repeat("after weighing the options ", 30) + "and then reverted that."
	return model.Session{
		Harness: "claude", Project: "app", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle on for the scheduler?"},
			{Role: "assistant", Text: long},
		},
	}
}

// A block that stops mid-sentence reads as a finished thought, and the end of a
// message is where a session says it reverted something. Both surfaces that hand
// a session on — `deja share` and the handover tail — cut to fit and said
// nothing about it.
func TestTruncatedBlocksSayTheyWereCut(t *testing.T) {
	s := longSession()
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"share", Share(s, 400)},
		{"tail", tailSection(s, 300)},
	} {
		got := strings.TrimSpace(tc.out)
		if !strings.HasSuffix(got, "…") {
			t.Errorf("%s ends on an unmarked cut: %q", tc.name, got[max(0, len(got)-50):])
		}
	}
}

// The marker comes out of the budget rather than being added to it: these blocks
// are sized to fit somewhere.
func TestTruncatedBlocksStayWithinBudget(t *testing.T) {
	s := longSession()
	for _, budget := range []int{120, 300, 400, 900} {
		if n := len(Share(s, budget)); n > budget {
			t.Errorf("share at budget %d produced %d bytes", budget, n)
		}
		if n := len(tailSection(s, budget)); n > budget {
			t.Errorf("tail at budget %d produced %d bytes", budget, n)
		}
	}
}

// A session that fits is left alone — no marker, nothing dropped. Otherwise a
// fix could pass the tests above by marking everything.
func TestBlocksThatFitAreUnmarked(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "app", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle on for the scheduler?"},
			{Role: "assistant", Text: "we moved it into its own process."},
		},
	}
	got := Share(s, 4000)
	if strings.Contains(got, "…") {
		t.Errorf("a session that fits was marked as cut: %q", got)
	}
	if !strings.Contains(got, "we moved it into its own process.") {
		t.Errorf("a session that fits lost its content: %q", got)
	}
}

// A marker in the middle of a block is worse than none: it says the passage it
// follows was cut and lets the next one read as continuous. Once a chunk is cut,
// the block ends.
func TestNothingFollowsAMarkedCut(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "app", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "what did we settle on for the scheduler?"},
			{Role: "assistant", Text: "we moved the scheduler out " + strings.Repeat("after weighing the options ", 30) + strings.Repeat(" ", 60) + "and then reverted that."},
			{Role: "assistant", Text: "the retry budget stays where it is for now."},
		},
	}
	// Sweep the budget: whether the cut lands in a run of spaces, mid-word or on
	// a rune boundary changes how much room is left after it.
	for budget := 150; budget <= 900; budget++ {
		for _, tc := range []struct {
			name string
			out  string
		}{
			{"share", Share(s, budget)},
			{"tail", tailSection(s, budget)},
		} {
			i := strings.Index(tc.out, "…")
			if i < 0 {
				continue
			}
			if rest := strings.TrimSpace(tc.out[i+len("…"):]); rest != "" {
				t.Fatalf("%s at budget %d continues after a marked cut with %q", tc.name, budget, rest)
			}
		}
	}
}

func TestLearnDiscourseMarkersSeparatesOutcomeVerbsFromDomainNouns(t *testing.T) {
	defer SetLearnedDecisionMarkers(nil)

	// Build a synthetic corpus where:
	// - "quicksilver" and "scheduler" appear in both exploration preambles (C-)
	//   and verified outcomes (C+), so their log-odds z-score is ~0.
	// - "модернизировал" (RU) and "stabilized" (EN) appear specifically in
	//   trajectory-verified outcome turns (RoleEdit + RoleCommand -> exit 0),
	//   while neither is in the static decisionMarkers list.
	ss := make([]model.Session, 0, 8)
	for i := range 8 {
		ss = append(ss, model.Session{
			ID: "s" + string(rune('a'+i)), Harness: "claude", Project: "app",
			Messages: []model.Message{
				{Role: "user", Text: "check quicksilver scheduler latency"},
				{Role: "assistant", Text: "проверяю логи quicksilver scheduler перед правкой"},
				{Role: sources.RoleFiles, Text: "internal/quicksilver/scheduler.go"},
				{Role: "assistant", Text: "читаю конфигурацию quicksilver scheduler дальше"},
				{Role: sources.RoleFiles, Text: "internal/quicksilver/config.go"},
				{Role: sources.RoleEdit, Text: "internal/quicksilver/scheduler.go"},
				{Role: sources.RoleCommand, Text: "go test ./internal/quicksilver/... → exit 0"},
				{Role: "assistant", Text: "Модернизировал и stabilized `internal/quicksilver/scheduler.go` для батчинга."},
			},
		})
	}

	markers := LearnDiscourseMarkers(ss)
	if !slices.Contains(markers, "модернизировал") {
		t.Fatalf("expected Monroe log-odds to learn 'модернизировал' from verified outcomes, got %v", markers)
	}
	if !slices.Contains(markers, "stabilized") {
		t.Fatalf("expected Monroe log-odds to learn 'stabilized' from verified outcomes, got %v", markers)
	}
	if slices.Contains(markers, "quicksilver") || slices.Contains(markers, "scheduler") {
		t.Fatalf("expected shared domain nouns to cancel out in log-odds, but got %v", markers)
	}

	SetLearnedDecisionMarkers(markers)
	line := "Модернизировал пайплайн в `internal/quicksilver/scheduler.go`."
	if !CarriesDecision(line) {
		t.Fatalf("expected CarriesDecision(%q) to succeed via learned Monroe log-odds marker", line)
	}
	if carriesLearnedDecision(strings.ToLower(line), line, []string{"модернизировал"}) {
		t.Fatalf("expected carriesLearnedDecision to skip marker echoed in asked terms")
	}
	if LearnDiscourseMarkers(ss[:1]) != nil {
		t.Fatal("expected nil markers when corpus has fewer than logOddsMinPosCount sessions")
	}
}

func TestMultilingualRulesAndHandoffUnwrapHelpers(t *testing.T) {
	if !trivialContinuation("はい 続けて") {
		t.Error("expected Japanese continuation nudge to be recognized as trivialContinuation")
	}
	if trivialContinuation("fix internal/index/cooccur.go") {
		t.Error("expected file path prompt not to be trivialContinuation")
	}
	rules := turnPairRules("1. Enable redis cache\n2. Drop legacy migration table in `schema.sql`?", "2. не надо, таблица еще нужна")
	if len(rules) == 0 {
		t.Fatal("expected numbered refusal rule from turnPairRules")
	}
	rules2 := turnPairRules("1. Rewrite the entire billing pipeline from scratch — this will take three weeks and break existing webhooks in production if we are not careful", "1 не буду, слишком долго")
	if len(rules2) == 0 {
		t.Fatal("expected summary refusal rule from turnPairRules")
	}
	rules3 := turnPairRules("Should we run `docker compose down -v`?", "2 нет, удалит базу")
	if len(rules3) == 0 {
		t.Fatal("expected fallback single-entity numbered refusal rule from turnPairRules")
	}
	if !onlyStandingRules("Never use force push on main. Always run tests before merge.") {
		t.Error("expected onlyStandingRules to return true for pure standing rules")
	}
	if onlyStandingRules("Look at the logs first.") {
		t.Error("expected onlyStandingRules to return false for non-rule")
	}
	if got := stripTagBlocks("<system_reminder>ignore</system_reminder>keep this <bad>unclosed</other> text"); !strings.Contains(got, "keep this") {
		t.Fatalf("stripTagBlocks = %q", got)
	}
	cmds := proposedSlashCommands("Check /Users/alex/app and /tmp/test or run /compact after /deploy/prod")
	if !slices.Contains(cmds, "/compact") || slices.Contains(cmds, "/Users") {
		t.Fatalf("unexpected proposedSlashCommands: %v", cmds)
	}
	handoff := "You are picking up work handed off from a previous session.\n\nObjective\n- Session started with: build zonal BM25F scorer\n- Latest request before compaction: fix negative control benchmark\n" + handoffQuoteClose + "\ncontinue from the last failing test"
	if got := unwrapHandoffPrompt(handoff, true); got != "build zonal BM25F scorer" {
		t.Fatalf("unwrapHandoffPrompt(wantGoal=true) = %q", got)
	}
	if got := unwrapHandoffPrompt(handoff, false); got != "continue from the last failing test" {
		t.Fatalf("unwrapHandoffPrompt(wantGoal=false) = %q", got)
	}
	handoffNoTail := "You are picking up work handed off from a previous session.\n\nObjective\n- Session started with: build zonal BM25F scorer\n- Latest request: verify CI shards"
	if got := unwrapHandoffPrompt(handoffNoTail, false); got != "verify CI shards" {
		t.Fatalf("unwrapHandoffPrompt(no tail, wantGoal=false) = %q", got)
	}
}

// A specification the agent wrote during the session is not a statement about
// the session. One of its bullets was being recalled to a later agent as a past
// decision.
func TestDocumentItemIsNotWhatTheSessionConcluded(t *testing.T) {
	var b strings.Builder
	b.WriteString("## Goal\n")
	b.WriteString("- Consolidate scattered AI work into one workspace\n")
	b.WriteString("## Constraints & Preferences\n")
	for i := range 30 {
		fmt.Fprintf(&b, "- rule %d: keep responses concise by default\n", i)
	}
	spec := b.String()

	if !IsDocumentItem(spec, "- rule 7: keep responses concise by default") {
		t.Error("a bullet of a thirty-line specification was taken for a conclusion")
	}
	// The prose in the same message still describes the work.
	if IsDocumentItem(spec, "Ниже — черновик правил, это будущий источник правды") {
		t.Error("the prose introducing the document was dropped with it")
	}

	// An ordinary reply that happens to list three things is not a document.
	reply := "Починил. Причина была в трёх местах:\n- gate\n- cache\n- hook\nПроверил на реальном сторе."
	if IsDocumentItem(reply, "- gate") {
		t.Error("a short list inside a normal answer was treated as a document")
	}
	// And a table row in a long table is structure like any other.
	var tb strings.Builder
	for i := range 25 {
		fmt.Fprintf(&tb, "| bench%d | six fixed queries against a real index | median |\n", i)
	}
	if !IsDocumentItem(tb.String(), "| bench3 | six fixed queries against a real index | median |") {
		t.Error("a row of a long table was taken for a conclusion")
	}
}

// A marker with no space after it is not a marker. Found by reading what the
// document rule still skipped: a bold sentence opens with an asterisk and a
// pointer opens with a dash, and counting either as structure lets a real
// conclusion be dropped as a list item.
func TestBoldAndArrowsAreNotStructure(t *testing.T) {
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "- rule %d: something the document says\n", i)
	}
	doc := b.String()

	for _, line := range []string{
		"**1. caffeinate — in a separate terminal** so it survives the session",
		"-> the retry fires only after the second failure",
		"#2740 is the issue this came from",
	} {
		if IsDocumentItem(doc, line) {
			t.Errorf("a sentence was taken for a list item: %q", line)
		}
	}
	for _, line := range []string{
		"- rule 3: something the document says",
		"## Constraints",
		"| bench | queries | median |",
	} {
		if !IsDocumentItem(doc, line) {
			t.Errorf("real structure went unrecognised: %q", line)
		}
	}
}

// A harness that introduces an envelope before quoting it put truncated
// inter-agent JSON into the session-start block — the first thing an agent
// reads. The tags are not prose: wherever one appears, the message is plumbing.
func TestEnvelopeIsNoiseWhereverItAppears(t *testing.T) {
	wrapped := []string{
		`Another Claude session sent a message: <teammate-message teammate_id="triage2" color="cyan"> {"type":"idle_notification"}`,
		`heads up — <task-notification id="7"> finished`,
		`context follows <system-reminder>do the thing</system-reminder>`,
	}
	for _, m := range wrapped {
		if !noisyMessage(m) {
			t.Errorf("an introduced envelope was kept: %q", m)
		}
	}
	// Prose that merely reads like a caveat is only skipped when it opens the
	// message, so a sentence about one is still the user talking.
	if noisyMessage("the caveat here is that the index lags by a session") {
		t.Error("a sentence mentioning a caveat was dropped as plumbing")
	}
	if !noisyMessage("Caveat: the messages below were generated by the user") {
		t.Error("the harness caveat header was kept")
	}
}

func TestStripHarnessBlocksLeavesThePersonsWords(t *testing.T) {
	cases := map[string]string{ //nolint:gosec // harness tags, not a credential
		// The host alone: nothing is left.
		"<task-notification>\n<task-id>br50mykp6</task-id>\n<status>failed</status>\n</task-notification>": "",
		"<system-reminder>\nTERSE MODE ACTIVE\n</system-reminder>":                                         "",
		// A person, with the host's reminder appended: the reminder goes, the words stay.
		"why does the pool use the simple protocol?\n<system-reminder>\nthe file changed on disk\n</system-reminder>": "why does the pool use the simple protocol?",
		// Attributes on the tag, and a block that is never closed.
		`<teammate-message teammate_id="triage2" color="cyan">{"type":"idle"}</teammate-message> fix the flaky test`: "fix the flaky test",
		"what did we decide about token refresh?\n<task-notification>\n<task-id>x</task-id>":                         "what did we decide about token refresh?",
		// deja's own block echoed back is not the person either.
		"<deja-recall>\nRecalled history…\n</deja-recall>\nand the second one?": "and the second one?",
		// The `!` shell: what ran and what it printed is not a question.
		"<bash-input>git status</bash-input>\n<bash-stdout>On branch main</bash-stdout>": "",
		// A tag name that is only a prefix of a word in prose is not a tag.
		"the <metadata> table has no meta column": "the <metadata> table has no meta column",
		// Amp's attachment block ahead of the question, and Cursor's wrapper:
		// the file bodies go, the question stays (#3182).
		"<attached_files>\n<file path=\"zebraquux/fetch.go\">package zebraquux\nfunc Fetch() {}\n</file>\n</attached_files>\nwhy does the zebraquux fetcher time out?":                                                                                         "why does the zebraquux fetcher time out?",
		"<additional_data>\nBelow are some potentially helpful/relevant pieces of information\n<attached_files>\n<file_contents>x</file_contents>\n</attached_files>\n</additional_data>\n\n<user_query>why does the pager quokkabloom on scroll</user_query>": "why does the pager quokkabloom on scroll",
		// A closing tag inside a sentence is the sentence's.
		"I removed the </attached_files> line, is that right?": "I removed the </attached_files> line, is that right?",
		// No tags at all: untouched.
		"plain question about <T> generics": "plain question about <T> generics",
	}
	for in, want := range cases {
		if got := StripHarnessBlocks(in); got != want {
			t.Errorf("StripHarnessBlocks(%q) = %q, want %q", in, got, want)
		}
	}
}

// Claude Code's paste wrapper is the one envelope whose body is the person's:
// the prompt hook searched on `pasted_content` and the paste's id. The tags
// go, the pasted text stays.
func TestPasteWrapperIsUnwrappedNotStripped(t *testing.T) {
	in := "<pasted_content id=\"dd73\">\nthe zorblax hook never fires\n</pasted_content id=\"dd73\">\n\nwhy?\n<system-reminder>\nnoise\n</system-reminder>"
	const want = "the zorblax hook never fires why?"
	if got := strings.Join(strings.Fields(StripHarnessBlocks(in)), " "); got != want {
		t.Errorf("StripHarnessBlocks = %q, want %q", got, want)
	}
}

func TestForkBoilerplateStripsEnvelopeAndKeepsDirective(t *testing.T) {
	forked := "Fork started — processing in background\n<fork-boilerplate>\nYou are a worker fork in an isolated workspace. Do not touch parent branch.\n</fork-boilerplate>\n\nYour directive: fix snapshot manifest checksum mismatch in archive_writer.go\n<system-reminder>\nhost reminder\n</system-reminder>"
	const want = "fix snapshot manifest checksum mismatch in archive_writer.go"
	if got := StripHarnessBlocks(forked); got != want {
		t.Errorf("StripHarnessBlocks(forked) = %q, want %q", got, want)
	}
	if got := BeforeHarnessBlock(forked); got != want {
		t.Errorf("BeforeHarnessBlock(forked) = %q, want %q", got, want)
	}
	// Mentioning the tag name in prose without a closing tag stays untouched.
	prose := "why did <fork-boilerplate> appear in raw jsonl?"
	if got := StripHarnessBlocks(prose); got != prose {
		t.Errorf("StripHarnessBlocks(prose) = %q, want %q", got, prose)
	}
}

func hostileSession() model.Session {
	return model.Session{
		Harness: "claude",
		Project: "app\x1b[31mred\x1b[0m",
		ID:      "x1\nfake",
		Messages: []model.Message{
			{Role: "user", Text: "the retry queue stalls on staging and every worker wakes at once"},
			{Role: "assistant", Text: "We spread the wakeups over a second. The jitter is bounded by the poll interval."},
		},
	}
}

// The share document's header is markdown: "# deja share: <id>" and
// "- Project: <name>". Both fields are text deja did not author — a directory
// name and a harness-assigned id — and a break in either split the header in
// two, leaving the stray half standing as a line of the document.
func TestShareHeaderFieldsStayOnTheirLines(t *testing.T) {
	out := Share(hostileSession(), 4000)
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "# deja share: ") {
		t.Fatalf("no header: %q", lines[0])
	}
	if strings.Contains(lines[0], "\n") || lines[1] != "" {
		t.Errorf("the header spilled: %q / %q", lines[0], lines[1])
	}
	if !strings.Contains(lines[0], "fake") {
		t.Errorf("the id lost its tail rather than joining it: %q", lines[0])
	}
	var project string
	for _, l := range lines {
		if strings.HasPrefix(l, "- Project: ") {
			project = l
		}
	}
	if project == "" {
		t.Fatalf("no project line: %q", out)
	}
	for _, r := range project {
		if unicode.IsControl(r) {
			t.Errorf("a control character reached the project line: %q in %q", r, project)
			break
		}
	}
}

// The handoff framing strips the share header by cutting at the first newline.
// An id carrying one left the rest of that id standing at the top of what
// another agent is handed.
func TestHandoffDropsTheWholeShareHeader(t *testing.T) {
	out := Handoff(hostileSession(), 4000)
	if strings.Contains(out, "deja share") {
		t.Errorf("the share header survived into the handoff: %q", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "fake" {
			t.Errorf("the tail of the id is standing on its own line:\n%s", out)
		}
	}
	first, _, _ := strings.Cut(out, "\n")
	for _, r := range first {
		if unicode.IsControl(r) {
			t.Errorf("a control character reached the framing line: %q in %q", r, first)
			break
		}
	}
}

// A plain session is unchanged.
func TestShareHeaderUnchangedForPlainFields(t *testing.T) {
	s := hostileSession()
	s.Project, s.ID = "app", "x1"
	out := Share(s, 4000)
	if !strings.HasPrefix(out, "# deja share: x1\n\n- Project: app\n") {
		t.Errorf("plain header changed: %q", strings.SplitN(out, "\n\n", 2)[0])
	}
}

// The id in the handoff's closing advice is meant to be typed back, and the
// lookup matches on a prefix: the head of an id with a break in it finds the
// session, the joined form matches nothing.
func TestHandoffAdviceIDIsTypeable(t *testing.T) {
	s := hostileSession()
	s.ID = "x1abcdef\nfake"
	out := Handoff(s, 4000)
	if !strings.Contains(out, "deja show x1abcdef`") {
		t.Errorf("the advice does not name a usable id:\n%s", out)
	}
	if strings.Contains(out, "x1abcdef fake`") {
		t.Errorf("the advice names the joined form, which matches nothing:\n%s", out)
	}
}

// Claude Code writes what a hook returned into the transcript under the user
// role, so deja's own status line comes back as something the person said —
// and gets quoted as the session's title. Seen in the status bar as
// `you have been here: "UserPromptSubmit says: deja-vu — you have been h…"`,
// which is recall quoting itself (#3168).
func TestHookEchoesAreNotWhatThePersonSaid(t *testing.T) {
	echoes := []string{
		`UserPromptSubmit says: deja-vu — you have been here: "the retry cap" (today)`,
		`SessionStart:compact says: deja recalled 3 prior sessions for this project`,
		`⎿ SessionStart:startup says: deja: recalled 2 sessions`,
		`PreToolUse says: deja: this command failed here before`,
		`PreCompact:manual says: deja saved the session digest`,
		"<deja-recall>\nRecalled history from prior sessions.\n</deja-recall>",
		"the assistant answered, and the transcript kept\n<deja-recall>\nan echoed block\n</deja-recall>",
	}
	for _, e := range echoes {
		if !IsPlumbing(e) {
			t.Errorf("IsPlumbing missed a host echo: %q", e)
		}
		if !IsAgentArtifact(e) {
			t.Errorf("IsAgentArtifact missed a host echo: %q", e)
		}
	}

	// What a person actually wrote, including the shapes this could have eaten:
	// a name in front of "says:", a quotation mid-sentence, and a message that
	// merely talks about the hook.
	mine := []string{
		"Dana says: rebase first, then push",
		"the reviewer says: this needs a test",
		"he says: no, and I think he is right",
		"why does UserPromptSubmit fire on a task notification",
		"SessionStart is the only event goose gives us at the top of a session",
		"the hook says nothing when the index has no history for the command",
	}
	for _, m := range mine {
		if IsPlumbing(m) {
			t.Errorf("IsPlumbing discarded something the person wrote: %q", m)
		}
		if IsAgentArtifact(m) {
			t.Errorf("IsAgentArtifact discarded something the person wrote: %q", m)
		}
	}
}

// The turn a host delivers when a background job finishes: a banner saying the
// turn is not the user, a paragraph of the host's own prose, then the block.
// #3156 took the block out and left the prose, and the prompt hook answered
// "no human input has been received since the last genuine user message" as if
// someone had asked it — the terms it matched on were `received`, `since` and
// `last`.
func TestAHostNotificationIsNotAQuestion(t *testing.T) {
	turn := "[SYSTEM NOTIFICATION - NOT USER INPUT]\n" +
		"This is an automated background-task event, NOT a message from the user.\n" +
		"Do NOT interpret this as user acknowledgement, confirmation, or response to any pending question.\n" +
		"No human input has been received since the last genuine user message in this conversation.\n" +
		"\n" +
		"<task-notification>\n<task-id>br50mykp6</task-id>\n<status>completed</status>\n</task-notification>\n"
	if got := StripHarnessBlocks(turn); got != "" {
		t.Errorf("the host talking to itself left %q behind", got)
	}
}

// The banner is not a gag: a person who pastes one to ask about it keeps their
// question, the way a pasted hook status bar does since #3168.
func TestTheQuestionUnderAPastedBannerSurvives(t *testing.T) {
	cases := map[string]string{
		"[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event.\n\nwhat is this thing in my chat?": "what is this thing in my chat?",
		// The banner inside a sentence is the sentence's own.
		"what does [SYSTEM NOTIFICATION - NOT USER INPUT] mean?": "what does [SYSTEM NOTIFICATION - NOT USER INPUT] mean?",
		// Words before the banner are the person's; the host starts at the banner.
		"look at this:\n[SYSTEM NOTIFICATION - NOT USER INPUT]\nan automated event": "look at this:",
		// A bracketed line that claims nothing about who is speaking stays.
		"[INFO] the pool refused a connection\nwhy?": "[INFO] the pool refused a connection\nwhy?",
	}
	for in, want := range cases {
		if got := StripHarnessBlocks(in); got != want {
			t.Errorf("StripHarnessBlocks(%q) = %q, want %q", in, got, want)
		}
	}
}

// looksLikeListingDump counted path-shaped fields and never read the count, so
// the rule it actually applied was "eight or more words, none of them a
// stopword" — which is an ordinary sentence in any language the stopword list
// does not cover. The list has eighteen English words and twelve Russian ones,
// and half this store is Russian.
//
// Measured over 277375 prose lines of a real store: 20294 dropped, 55 of them
// listings.
func TestASentenceIsNotAListingDump(t *testing.T) {
	prose := []string{
		// Russian, eight or more words, none of them in the stopword list.
		"Монорепа смержена (#1562). Оба внешних PR перенацелены, жду их проверки.",
		"Оба утверждения теперь острее: тест первого запуска слово «forgotten» прямо запрещает.",
		"Хорошо — XDG уважается, значит живой тест изолируется. Пишу защиту.",
		// English written without any of the eighteen: still a sentence.
		"Raised pgbouncer default_pool_size from twenty up through forty, timeouts stopped afterwards.",
	}
	for _, l := range prose {
		if looksLikeListingDump(l) {
			t.Errorf("a sentence was dropped as a listing: %q", l)
		}
	}
}

// The rule still has to do its job. A listing is what it was written for, and
// `ls` inside a directory prints no slashes at all — so bare filenames count.
func TestAListingIsStillARejected(t *testing.T) {
	listings := []string{
		"internal/index/ingest.go internal/index/manifest.go internal/search/recall.go cmd/deja/mcp.go cmd/deja/fix.go cmd/deja/how.go internal/digest/digest.go internal/model/model.go",
		"main.go util.go parser.go writer.go reader.go index.go search.go digest.go",
		"- Repositories (`order_repository.go`, `payment_repository.go`, `profile_repository.go`, `dftype_repository.go`, `wallet_repository.go`, `audit_repository.go`, `session_repository.go`)",
	}
	for _, l := range listings {
		if !looksLikeListingDump(l) {
			t.Errorf("a listing dump got through: %q", l)
		}
	}
}

// The short-line and stopword exits are unchanged, and are what keeps an
// ordinary sentence about files from being read as a list of them.
func TestTheListingRuleKeepsItsEarlierExits(t *testing.T) {
	if looksLikeListingDump("main.go util.go parser.go") {
		t.Error("a three-field line is not a listing")
	}
	if looksLikeListingDump("the files are main.go util.go parser.go writer.go reader.go index.go search.go") {
		t.Error("a sentence with a stopword in it was read as a listing")
	}
}

// A word is letters once the punctuation a sentence puts around one is off.
// Everything an `ls` line is made of is not.
func TestPlainWordFieldNamesOnlyWords(t *testing.T) {
	for _, f := range []string{"pgbouncer", "перенацелены", "(смержена)", "root", "**Домены**"} {
		if !plainWordField(f) {
			t.Errorf("%q is a word", f)
		}
	}
	for _, f := range []string{"internal/index/ingest.go", "main.go", "v5.4.3", "4096", "drwxr-xr-x", "00:00", "a"} {
		if plainWordField(f) {
			t.Errorf("%q is not a word", f)
		}
	}
}

// The shape this regressed on first: `ls -l` output is three words and six
// fields that are not, and the old rule caught it only by accident.
func TestAnLsLineIsStillAListing(t *testing.T) {
	if !looksLikeListingDump("drwxr-xr-x 5 root root 4096 Jan 1 00:00 /usr/lib/x86_64-linux-gnu/libfoo.so.1.2.3") {
		t.Error("an ls -l line got through")
	}
}

// A session whose only conclusion is one sentence longer than the budget
// answered a caller asking for three lines with silence, and a caller asking
// for one with the sentence, marked. The rule behind the marked cut is that
// nothing follows the marker — and an empty result is that whatever max was
// asked for, because the loop breaks straight after.
//
// 9 of 452 sessions on a real store settled something and got no block at all.
func TestOneOverlongConclusionIsMarkedRatherThanDropped(t *testing.T) {
	// Two sentences, the first of them alone longer than the budget. Both
	// parts matter: with one sentence firstSentences never counts two and
	// falls into its own 240-byte cap, and with a short first sentence the
	// one-sentence retry fits and no cut is needed. Either way the marked cut
	// is never reached and the test would be measuring something else.
	long := "The fix was raising pgbouncer default_pool_size to 40 " +
		strings.Repeat("after weighing the options against the replica load ", 8) +
		". Revisit when the replica catches up."

	for _, limit := range []int{1, 3} {
		got := Conclusions(conclusionSession(long), 300, limit)
		if len(got) == 0 {
			t.Fatalf("max=%d: a session that settled something got no block", limit)
		}
		if !strings.HasSuffix(got[0], "…") {
			t.Errorf("max=%d: the line was not marked as cut:\n%s", limit, got[0])
		}
		if len(got[0]) > 300 {
			t.Errorf("max=%d: the cut line is %d bytes, over the 300 budget", limit, len(got[0]))
		}
	}
}

// The marked cut is a last resort, not the normal path: a conclusion that fits
// arrives whole and unmarked.
func TestAConclusionThatFitsIsNotMarked(t *testing.T) {
	got := Conclusions(conclusionSession("The fix was raising pgbouncer default_pool_size to 40."), 300, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if strings.HasSuffix(got[0], "…") {
		t.Errorf("a conclusion that fits was marked as cut:\n%s", got[0])
	}
}

// The marked cut is only for an empty result. Once a whole line is in, a cut
// must not follow it: the marker would read as "and there is more of this
// line" while the text after it is a different conclusion entirely (#1336).
func TestACutDoesNotFollowALineThatFitted(t *testing.T) {
	long := "The earlier attempt pinned pgx to 5.4.3 " +
		strings.Repeat("after weighing the options against the replica load ", 8) +
		". Revisit when the replica catches up."
	got := Conclusions(assistantSession(
		long,
		"The fix was raising pgbouncer default_pool_size to 40.",
	), 300, 3)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "default_pool_size to 40") {
		t.Fatalf("the newest conclusion is not first, so this measures nothing:\n%s", got[0])
	}
	for i, l := range got[1:] {
		if strings.HasSuffix(l, "…") {
			t.Errorf("line %d is a marked cut following a line that fitted:\n%s", i+1, l)
		}
	}
}

// A marker fires where it begins a word. As a plain substring it fired inside
// longer words meaning something else, or the opposite (#2734).
func TestMarkersDoNotFireInsideAnotherWord(t *testing.T) {
	inside := []string{
		"Заметки живут в теле релиза, в CHANGELOG.md пустой [Unreleased] блок.",
		"`reviewDecision: REVIEW_REQUIRED` — сам себя одобрить не могу.",
		"Path resolves correctly, so the pollution is in the writer, not the path.",
		"The one genuinely-unfixed item is the NFD form of the accented name.",
		// Both halves of the list, not just the ASCII one: "оказалось" sits
		// inside "показалось", which is the opposite claim.
		"мне показалось, что тест флэйковый, но я не проверял",
	}
	for _, line := range inside {
		if CarriesDecision(line) {
			t.Errorf("a marker fired inside another word: %q", line)
		}
	}

	// The tail stays open: half the markers are Russian verbs, and the
	// inflections are how they are written.
	inflected := []string{
		"решили оставить один индекс на все харнессы",
		"исправили в том же коммите, тест краснеет на откате",
		"вторая нода заработала после смены keepalive",
	}
	for _, line := range inflected {
		if !CarriesDecision(line) {
			t.Errorf("an inflected marker stopped firing: %q", line)
		}
	}
}

// The rule was the number alone — `^\s*\d{1,6}\s` — so every sentence that
// opens with a count was dropped from every digest. In real-world stores, // 916 of the assistant lines it matched are sentences.
func TestASentenceThatStartsWithANumberIsKept(t *testing.T) {
	prose := []string{
		"12 зелёных. Жду ревьюера перед мержем.",
		"505 отказов записи шаблонов за 16 часов — это то, что я вчера «починил» ретраем. Проверяю в первую очередь.",
		"21 стора, 1.26 мс и 0.39 мс. Локально дёшево — но проверяю утверждение про сетевой стор.",
		"3 sessions read it before the fix. None of them saw the pair.",
		"已修复 3 个测试。现在全部通过。",
	}
	for _, l := range prose {
		if numberedDumpLine(l) {
			t.Errorf("a sentence was dropped as a numbered dump: %q", l)
		}
	}
}

// And the dumps it exists for are still dumps. `cat -n` and the file readers
// put a tab after the number; the space-separated ones are fragments with no
// sentence in them.
func TestANumberedDumpLineIsStillDropped(t *testing.T) {
	dumps := []string{
		"1\tdiff --git a/internal/index/ingest.go b/internal/index/ingest.go",
		// A numbered source line whose content is a sentence: the tab is what
		// says it is a listing, and without it the sentence test would keep it.
		"   42\t// Raised the pool to 40. It held.",
		"3 files changed, 9 insertions(+)",
		"1 file changed, 1 insertion(+), 1 deletion(-)",
		"300 index.js",
		"1564 nonpass=0",
		// A rendered table row that happens to contain a sentence end. The
		// pipe is what says it is a row.
		"61 |            [claude] goprojects/catalog-svc · today · Summary: 1. done",
	}
	for _, l := range dumps {
		if !numberedDumpLine(l) {
			t.Errorf("a numbered dump got through: %q", l)
		}
	}
}

// A line that does not open with a number is not this rule's business.
func TestTheNumberedRuleOnlyLooksAtNumberedLines(t *testing.T) {
	for _, l := range []string{"raised the pool to 40", "\tindented but no number", ""} {
		if numberedDumpLine(l) {
			t.Errorf("matched a line with no leading number: %q", l)
		}
	}
}

// hasSentenceEnd follows firstSentences: a stop needs a space after it, so a
// version number does not end a sentence, and the CJK stops take none.
func TestHasSentenceEndAgreesWithTheSentenceSplitter(t *testing.T) {
	for _, s := range []string{"pinned pgx. it held", "done.", "готово! дальше", "已修复。"} {
		if !hasSentenceEnd(s) {
			t.Errorf("%q ends a sentence", s)
		}
	}
	for _, s := range []string{"v5.4.3 held", "300 index.js", "files changed, 9 insertions(+)", ""} {
		if hasSentenceEnd(s) {
			t.Errorf("%q does not end a sentence", s)
		}
	}
}

// "теперь" opening a clause starts a plan; in the middle of one it reports a
// state. Which it is was decided by the byte before it, and every byte >= 0x80
// counted as a letter — so Russian punctuation, which is also multi-byte, hid
// the opening and the plan was promoted as an outcome (#2734).
func TestPunctuationDoesNotHideAnOpeningNow(t *testing.T) {
	plans := []string{
		"— Теперь пишу SQL и проверяю выборку.",
		"«Теперь пишу SQL и проверяю выборку».",
		"…Теперь пишу SQL и проверяю выборку.",
	}
	for _, line := range plans {
		if CarriesDecision(line) {
			t.Errorf("a plan promoted as a conclusion: %q", line)
		}
	}

	// Mid-sentence it still reports where something ended up.
	states := []string{
		"прод-пины теперь ложатся на релиз-ветку, а не на общий кластер",
		"дамп теперь на двух нодах, обе отвечают",
	}
	for _, line := range states {
		if !CarriesDecision(line) {
			t.Errorf("an outcome read as a plan: %q", line)
		}
	}
}

// The state markers ("теперь", "стало") were added because a decision is as
// often reported as the state something ended up in. The same words open the
// next task, and then the line is a plan. Measured by reading ten lines the
// rule counted as conclusions on a real store, four were plans: "Go-структуры
// готовы. Теперь SQL — 2 SELECT, INSERT, UPDATE" and "PR открыт. Теперь
// главное — сделать так, чтобы деплой не мог соврать".
func TestAPlanIsNotADecision(t *testing.T) {
	for _, line := range []string{
		"Go-структуры готовы. Теперь SQL — 2 SELECT, INSERT, UPDATE + apply-блок",
		"PR открыт. Теперь главное — сделать так, чтобы деплой не мог соврать",
		"теперь нужно прогнать бенч ещё раз",
		"now let's wire the plugin",
		// Mid-sentence plan wording: the clause rule cannot see this one, the
		// phrase list has to.
		"ладно, а теперь давай прогоним бенч",
	} {
		if CarriesDecision(line) {
			t.Errorf("a plan counted as a decision: %q", line)
		}
	}
}

// And a state that really is an outcome still counts, including in the same
// sentence shape.
func TestAStateStillCountsAsADecision(t *testing.T) {
	for _, line := range []string{
		"позиция восстановления теперь точная, дампы 389 МБ ежедневно",
		"PR #810 теперь закрывает #813 и #814",
		"прод-пины теперь ложатся на релиз-ветку",
		"the retry budget now lives in config",
	} {
		if !CarriesDecision(line) {
			t.Errorf("an outcome stopped counting as a decision: %q", line)
		}
	}
}

// A line that plans and reports in one breath keeps its decision: the plan
// wording only disqualifies the state marker it sits on.
func TestAPlanBesideAnOutcomeKeepsTheOutcome(t *testing.T) {
	line := "в итоге остановились на 40. теперь давай посмотрим логи"
	if !CarriesDecision(line) {
		t.Errorf("an outcome was lost because a plan followed it: %q", line)
	}
}

// A review agent's report reads as a decision to every rule here — it is full of
// "approved", "fixed", "findings" — and it is about one diff on one day, which
// makes it the worst thing to carry forward. Read from the lines deja would have
// served before an edit on a real store: `index.tsx — No findings.` and, worst,
// `config.go — prior decision: Deploy: NO-DEPLOY until at least findings 1 and 2
// are fixed.` — a hold from a review of something else, offered months later as
// the standing position on that file.
func TestAReviewVerdictIsNotSomethingConcluded(t *testing.T) {
	reports := []string{
		"VERDICT: APPROVED ISSUES: - None. REQUIRED_FIXES: - None.",
		"VERDICT: NEEDS_CHANGES ISSUES: - the billing index re-exports a type twice",
		"Findings: none high/medium. Deploy: DEPLOY.",
		"No findings.",
		"Deploy: **NO-DEPLOY** until at least findings 1 and 2 are fixed.",
	}
	for _, r := range reports {
		if !IsAgentArtifact(r) {
			t.Errorf("a review report is treated as the session's own words:\n  %s", r)
		}
	}
}

// A sentence a person or an agent writes about the work is not a report, even
// where it uses the same vocabulary.
func TestARealConclusionAboutFindingsSurvives(t *testing.T) {
	keep := []string{
		"the retry budget stays at four; the review findings about backoff were already covered",
		"решили не деплоить по пятницам — откатывать некому",
		"we approved the migration plan and it is the one we keep",
	}
	for _, k := range keep {
		if IsAgentArtifact(k) {
			t.Errorf("a real sentence was dropped as a report:\n  %s", k)
		}
	}
}

// deja asks an agent to credit the recall it used, and that sentence reports
// what an earlier session decided — so every decision marker fires on it and
// the line came back as a decision of its own. In real-world stores, at the
// file line: `doctor_auto.go has been worked on in 2 sessions — prior decision:
// deja-vu recalled: …`, which is deja quoting itself quoting a session.
func TestDejaQuotingItselfIsNotADecision(t *testing.T) {
	quotes := []string{
		"déjà vu: the retry budget stays at four (claude, Aug 11, deja:2fc1d1ef) — reusing it.",
		"deja-vu recalled: earlier experiments already fixed the ranking, so that is settled",
		"Recalled from this machine's history: the deploy key was rotated in March",
		"deja found sessions whose wording matches this request",
	}
	for _, q := range quotes {
		if CarriesDecision(q) {
			t.Errorf("deja's own line read as a decision:\n  %s", q)
		}
	}
}

// And a person's own decision about deja still is one — the guard is about who
// is speaking, not about the word.
func TestADecisionAboutDejaIsStillADecision(t *testing.T) {
	decisions := []string{
		"решили: deja больше не трогает живой индекс, только копию",
		"we settled on keeping the deja hook out of the release build",
	}
	for _, d := range decisions {
		if !CarriesDecision(d) {
			t.Errorf("a real decision was dropped:\n  %s", d)
		}
	}
}

func toolHeavySession() model.Session {
	base := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	msg := func(role, text string, minute int) model.Message {
		return model.Message{Role: role, Text: text, Time: base.Add(time.Duration(minute) * time.Minute)}
	}
	return model.Session{
		ID: "agent-run", Harness: "claude", Project: "app", Updated: base.Add(time.Hour),
		Messages: []model.Message{
			msg("user", "the exporter drops rows when the batch crosses midnight", 0),
			msg("assistant", "Running the test suite to reproduce the report now.", 1),
			msg("assistant", "Running the test suite to reproduce the report now.", 2),
			msg("assistant", "Running the test suite to reproduce the report now.", 3),
			msg("assistant", "Checking the batch window helpers for boundary handling.", 4),
			msg("assistant", "Root cause: the window used local midnight but rows are stamped UTC, so the fix pins the cutoff to UTC.", 5),
			msg("assistant", "All 42 tests pass, closing out.", 6),
		},
	}
}

func TestShareToolHeavyPrefersConclusions(t *testing.T) {
	out := Share(toolHeavySession(), 0)
	if !strings.Contains(out, "Root cause") || !strings.Contains(out, "closing out") {
		t.Fatalf("conclusion and outcome must survive:\n%s", out)
	}
	if strings.Contains(out, "Checking the batch window helpers") {
		t.Fatalf("unmarked status chatter must be dropped when conclusions exist:\n%s", out)
	}
	if strings.Count(out, "Running the test suite") > 0 {
		t.Fatalf("repeated status lines must not survive selection:\n%s", out)
	}
}

func TestShareConversationalUnchanged(t *testing.T) {
	s := model.Session{
		ID: "chat", Harness: "claude", Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "what port does the dev server use"},
			{Role: "assistant", Text: "It listens on 5173 by default."},
		},
	}
	out := Share(s, 0)
	if !strings.Contains(out, "5173") {
		t.Fatalf("short conversational sessions must keep everything:\n%s", out)
	}
}

func TestDedupeStatusKeepsFirstOccurrence(t *testing.T) {
	ms := []model.Message{
		{Role: "assistant", Text: "retrying the deploy step"},
		{Role: "assistant", Text: "retrying   the deploy step"},
		{Role: "assistant", Text: "deploy finished"},
	}
	got := dedupeStatus(ms)
	if len(got) != 2 || got[1].Text != "deploy finished" {
		t.Fatalf("dedupe wrong: %#v", got)
	}
}

// A flat cut printed "deja-2026-08" for a note whose id is
// "deja-2026-08-01-ops/db" — an id no session has, in the error telling the
// reader which session was refused (#741).
func TestShortKeepsBothEnds(t *testing.T) {
	long := "deja-2026-08-01-ops/db"
	got := Short(long)
	if !strings.HasPrefix(got, "deja-2026") || !strings.HasSuffix(got, "ops/db") {
		t.Errorf("Short(%q) = %q — an end is missing", long, got)
	}
	if len([]rune(got)) > 20 {
		t.Errorf("Short returned %d runes: %q", len([]rune(got)), got)
	}
	// Two ids that differ only in the middle must not collapse together.
	if Short("deja-2026-08-01-ops/db") == Short("deja-2026-09-14-ops/db") {
		t.Error("two ids shortened to the same string")
	}
	// Short ids are printed whole.
	for _, id := range []string{"", "a1", "deja-note-claude-a1"} {
		if got := Short(id); got != id {
			t.Errorf("Short(%q) = %q, want it unchanged", id, got)
		}
	}
	// Runes, not bytes: cutting mid-character would print a replacement glyph.
	cyr := strings.Repeat("сессия", 6)
	if got := Short(cyr); strings.ContainsRune(got, '�') {
		t.Errorf("Short(%q) = %q", cyr, got)
	}
}

// The markers exist to see past status chatter at the few lines that say what a
// session concluded. Two of them were chatter: measured on 85k assistant lines
// from a real store, "зелёный" was the only marker on 543 of 4901 promoted
// lines and "готово" on 221, and reading them they are CI runs and checklist
// ticks (#2734).
func TestGreenCIIsNotAConclusion(t *testing.T) {
	chatter := []string{
		"CI 12/12 зелёный, жду ревьюера.",
		"CI зелёный на #1380 — все проверки прошли.",
		"- ✓ упоминания в вебе — готово",
		"первую попытку я построил копированием готового HOME",
		"«полуготовое задание хуже повторного клика»",
	}
	for _, line := range chatter {
		if CarriesDecision(line) {
			t.Errorf("status chatter promoted as a conclusion: %q", line)
		}
	}

	// What ended up working still counts. "заработал" went in beside those two
	// and stays: of the lines it alone marks, most report an outcome.
	settled := []string{
		"Механизм наконец заработал полностью: настоящие HTTP-запросы, правильные ключи.",
		"Direct SSH к se заработал 3/3 с keepalive-опциями.",
	}
	for _, line := range settled {
		if !CarriesDecision(line) {
			t.Errorf("an outcome read as a passing mention: %q", line)
		}
	}
}

// A reply is the last thing the agent said before the next real ask; a nudge
// does not cut it, and a probe file under /tmp is not an edit.
func TestStoryOfKeepsAsksRepliesRunsAndEdits(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "the scheduler times out under load"},
		{Role: "assistant", Text: "Let me look at the pool first."},
		{Role: sources.RoleCommand, Text: "$ go test ./...  → exit 1"},
		{Role: sources.RoleToolOutput, Text: "--- FAIL: TestLoad"},
		{Role: sources.RoleEdit, Text: "/work/app/pool.go\nold span"},
		{Role: sources.RoleEdit, Text: "/tmp/probe/x.go\nold span"},
		{Role: "user", Text: "go on"},
		{Role: "assistant", Text: "The proxy closes idle connections at five minutes, so the pool must retire them sooner."},
		{Role: "user", Text: "now add a gauge for it on the dashboard"},
	}}
	st := StoryOf(s)
	want := []Turn{
		{User: true, Text: "the scheduler times out under load"},
		{Text: "The proxy closes idle connections at five minutes, so the pool must retire them sooner."},
		{User: true, Text: "now add a gauge for it on the dashboard"},
	}
	if !reflect.DeepEqual(st.Turns, want) {
		t.Errorf("turns = %+v", st.Turns)
	}
	if len(st.Runs) != 1 || st.Runs[0].Output != "--- FAIL: TestLoad" {
		t.Errorf("runs = %+v", st.Runs)
	}
	if !reflect.DeepEqual(st.Edited, []string{"/work/app/pool.go"}) {
		t.Errorf("edited = %q", st.Edited)
	}
}

// An agent started in a package directory is standing in the project all the
// same, and the project is what recall is scoped by. The worktree roots are
// what carry that — except a repository with one worktree dropped its root,
// which is the only name a subdirectory does not already have (#2037).
func TestProjectNameCandidatesNameTheRepositoryFromASubdirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "work", "repo")
	sub := filepath.Join(repo, "cmd", "deja")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}

	// The lookup runs under a budget so a session-start hook cannot hang, and
	// this test is about what it answers, not about how fast the machine is.
	// The windows runner missed the budget on a cold `git.exe` and the failure
	// read as a wrong answer (#3624), so the speed is measured first and named.
	start := time.Now()
	if out, err := exec.CommandContext(t.Context(), "git", "-C", sub, "worktree", "list", "--porcelain").CombinedOutput(); err != nil {
		t.Skipf("git cannot list worktrees here: %v %s", err, out)
	}
	if took := time.Since(start); took > worktreeListBudget {
		t.Skipf("git worktree list took %v on this machine, past the %v budget the lookup runs under",
			took.Round(time.Millisecond), worktreeListBudget)
	}

	// The premise: standing at the root, the project's own names are there.
	atRoot := ProjectNameCandidates(repo)
	if !has(atRoot, "work/repo") {
		t.Fatalf("the root does not name its own project: %v", atRoot)
	}

	got := ProjectNameCandidates(sub)
	if !has(got, "work/repo") {
		t.Errorf("a subdirectory of the repository does not name the project it is in: %v", got)
	}
	// And its own names stay: a session recorded against the subdirectory
	// before it was re-projected is still that session's.
	if !has(got, "cmd/deja") {
		t.Errorf("the subdirectory lost its own name: %v", got)
	}
}

func has(names []string, want string) bool {
	return slices.Contains(names, want)
}

// Inside a submodule git names the gitdir rather than the working tree, so the
// root it reports is ".git/modules/<name>" — a path nobody worked in. Dropping
// the single root used to hide that; keeping it made the junk a candidate.
func TestProjectNameCandidatesSkipTheSubmoduleGitdir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "work", "parent")
	child := filepath.Join(tmp, "work", "child")
	for _, d := range []string{parent, child} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
			if out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", d}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(child, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "f.txt"}, {"commit", "-q", "-m", "seed"}} {
		if out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", child}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	add := exec.CommandContext(t.Context(), "git", "-C", parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", child, "sub")
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("submodules unavailable here: %v %s", err, out)
	}

	got := ProjectNameCandidates(filepath.Join(parent, "sub"))
	for _, n := range got {
		if strings.Contains(n, "modules/") {
			t.Errorf("the gitdir reached the candidates as a project: %v", got)
		}
	}
	// The submodule is still a place someone works in, under its own name.
	if !has(got, "parent/sub") {
		t.Errorf("the submodule lost its own name: %v", got)
	}
}

// The parsers now file Claude's compaction summary under the summary role. The
// packet's objective and a resumed session's earlier half still read it there,
// as they read it under the user role from an index built before.
func TestSummaryRoleStillCarriesTheCompactedIntent(t *testing.T) {
	summary := "This session is being continued from a previous conversation that ran out of context.\n\nSummary:\n1. Primary Request and Intent:\n  Repair the decoder and add a fixture.\n2. Key Technical Concepts:\n  - irrelevant\n"
	for _, role := range []string{sources.RoleSummary, "user"} {
		s := model.Session{ID: "s", Harness: "claude", Messages: []model.Message{
			{Role: role, Text: summary},
			{Role: "user", Text: "go on"},
		}}
		c := ExtractCompactionContext(s, ExtractOptions{})
		if !strings.Contains(c.Objective.Text, "Repair the decoder") {
			t.Errorf("role %q: summary intent was not retained: %#v", role, c.Objective)
		}
		if got := compactedHalf(s); !strings.Contains(got, "Repair the decoder") {
			t.Errorf("role %q: compactedHalf = %q", role, got)
		}
	}
	// A session that opens with the person's own words has no compacted half.
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "fix the decoder"},
		{Role: sources.RoleSummary, Text: summary},
	}}
	if got := compactedHalf(s); got != "" {
		t.Errorf("compactedHalf read a summary that came after the person spoke: %q", got)
	}
}

// Lines the session-start digest quoted on real session starts, judged by
// hand: none of them tells a later session anything.
func TestALineThatSaysNothingIsNotQuotedAsAConclusion(t *testing.T) {
	for _, filler := range []string{
		"Fixed the same way.",
		"работает",
		"Hi. What do you want to work on?",
		"It printed: `hi`",
		"Waiting for CI.",
		"Let me check what formatters or other tests might also depend on the tab layout:",
		"I'll run `echo hi` and report the output.",
		"Merged. Ledger + notes.",
	} {
		if got := SubstantialConclusions(conclusionSession(filler), 400, 2); len(got) != 0 {
			t.Errorf("%q was quoted as a conclusion: %q", filler, got)
		}
	}
}

// Under a command the agent is about to run, a short outcome is the answer:
// the surfaces that pair a line with a command or a file keep it.
func TestAShortOutcomeStaysWhereTheReaderHasTheContext(t *testing.T) {
	if got := Conclusions(conclusionSession("tests pass"), 200, 1); len(got) == 0 || got[0] != "tests pass" {
		t.Errorf("the command-paired surface lost a short outcome: %q", got)
	}
}

// Short is not the same as empty. A line that names what it is about stays,
// however few words it takes.
func TestAShortLineThatNamesSomethingIsKept(t *testing.T) {
	for _, line := range []string{
		"vendoring fixed it",
		"`toolHookMaxBytes` (480 bytes).",
		"We chose a 32-byte nonce.",
		"Verdict: the fix is sound.",
		"Сейчас Hermes — это «читалка»: 15 кронов, почти все производят тексты.",
	} {
		got := SubstantialConclusions(conclusionSession(line), 400, 2)
		if len(got) == 0 || !strings.Contains(got[0], strings.TrimSuffix(line, ".")) {
			t.Errorf("%q was dropped or cut: %q", line, got)
		}
	}
}

// A stub that opens a longer message is not quoted alone: the content is in
// the sentences after it.
func TestAStubOpeningIsMovedPast(t *testing.T) {
	got := SubstantialConclusions(conclusionSession(
		"Готово. Полный отчёт ниже. Ранжирование по дате теперь насыщается, а не заворачивается: "+
			"`dayBoost` ограничен 0.3, тест на 31 декабря зелёный."), 400, 2)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if strings.HasPrefix(got[0], "Готово.") || !strings.Contains(got[0], "dayBoost") {
		t.Errorf("the quote is the stub, not what followed it: %q", got[0])
	}
}

// The budget cut takes the first sentence that carries something, not merely
// the first one.
func TestTheBudgetCutSkipsAStubOpening(t *testing.T) {
	got := SubstantialConclusions(conclusionSession(
		"Готово. The fix was pinning pgx to 5.4.3 in go.mod so the pool stops leaking. "+
			"Both replicas held under the load test for an hour afterwards."), 90, 1)
	if len(got) == 0 {
		t.Fatal("nothing was quoted")
	}
	if !strings.Contains(got[0], "pinning pgx to 5.4.3") {
		t.Errorf("the cut kept the stub: %q", got[0])
	}
}

// A message with nothing in it gives way to an older one that has something,
// rather than leaving the block on a greeting.
func TestAnEmptyNewestMessageGivesWayToAnOlderConclusion(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "why did deploys hang?"},
		{Role: "assistant", Text: "The fix was enforcing one lock order in `scheduler.go`: queue lock before worker lock."},
		{Role: "user", Text: "thanks"},
		{Role: "assistant", Text: "Hi. What do you want to work on?"},
	}}
	got := SubstantialConclusions(s, 400, 2)
	if len(got) == 0 || !strings.Contains(got[0], "scheduler.go") {
		t.Errorf("the older conclusion did not take the slot: %q", got)
	}
	for _, l := range got {
		if strings.Contains(l, "What do you want") {
			t.Errorf("the greeting was quoted: %q", got)
		}
	}
}

// The number that opens a list item is not a sentence. Counted as one, the
// two-sentence lead ended at "1." and the item it introduced was cut off.
func TestAListNumberDoesNotEndTheLead(t *testing.T) {
	got := SubstantialConclusions(conclusionSession(
		"Found 3 production bugs. 1. **High — order is marked DONE before notification**, so a failed delivery is never retried."), 400, 1)
	if len(got) == 0 || !strings.Contains(got[0], "marked DONE before notification") {
		t.Errorf("the list item was cut off at its number: %q", got)
	}
}

// A line that records a call being made is not a candidate for the slot that
// answers a question — it matches the question because it *is* the question,
// asked earlier by an agent whose stdout was captured (#2067).
func TestToolCallRecordIsNotSomethingSaid(t *testing.T) {
	records := []string{
		`claude  goprojects/deja-vu  > builder · gpt-5.6-luna ⚙ deja_recall {"query":"deja-vu repository description wording"}`,
		`⏺ Bash {"command":"go test ./..."}`,
		`▶ read_file { "path": "internal/index/index.go" }`,
	}
	for _, line := range records {
		if !IsToolCallRecord(line) {
			t.Errorf("a captured tool call read as something said: %q", line)
		}
	}

	// Prose about the same subjects stays a candidate. The marker alone is not
	// the shape — a status line uses one too.
	said := []string{
		"⚙ настройки переехали в конфиг, ключ читается один раз на старте",
		"we picked the second wording option for the repository description",
		"deja_recall returns the ranked sessions, not the raw store",
		"⏺ Bash закончился ошибкой, разбираю",
	}
	for _, line := range said {
		if IsToolCallRecord(line) {
			t.Errorf("prose barred as a tool call: %q", line)
		}
	}
}
