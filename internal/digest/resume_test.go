package digest

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"strings"
	"testing"
	"time"
)

// What the marker rules let through, read across 32 projects on a real store:
// deja's own marker on a synced session, a one-word acknowledgement, and a
// pasted design document whose first line is a heading. Each carries a decision
// marker and none of them is a decision.
func TestWhatIsNotADecisionEvenWithTheMarkers(t *testing.T) {
	junk := []string{
		"Imported from Claude session `dccbfbb0-6b5a-466b-a180-ca1d9f658794`.",
		"Затащил.",
		"Done.",
		"## Goal - довести автоскейлер до 100/100: строгий аудит, чистая архитектура",
		"- fixed the retry budget and merged it",
	}
	for _, j := range junk {
		s := model.Session{Messages: []model.Message{{Role: "assistant", Text: j}}}
		if r := ResumeFrom(s, nil); r.Decision != "" {
			t.Errorf("kept as a decision: %q", r.Decision)
		}
	}
}

// And a sentence that settles something is still kept, heading-free and long
// enough to read cold.
func TestARealDecisionSurvivesTheseGuards(t *testing.T) {
	keep := []string{
		"решили: бюджет повторов для quokkabloom остаётся четыре",
		"we settled on warming the cold path before the first read",
	}
	for _, k := range keep {
		s := model.Session{Messages: []model.Message{{Role: "assistant", Text: k}}}
		if r := ResumeFrom(s, nil); r.Decision == "" {
			t.Errorf("a real decision was dropped: %q", k)
		}
	}
}

// `deja wip` printed `was.`, `it`, `as`, `exactly` beside the real paths: every
// whitespace-separated token of a files record counted as a file, so a record
// carrying a sentence contributed one per word — and crowded the real paths out
// of the handful that get named (#3706).
func TestFilesInFlightKeepsPathsAndDropsProse(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: sources.RoleFiles, Text: "/Users/x/work/cmd/deja/how.go"},
		{Role: sources.RoleFiles, Text: "the state as it was. exactly it"},
		{Role: sources.RoleEdit, Text: "cmd/deja/mcp.go internal/digest/resume.go"},
		{Role: sources.RoleFiles, Text: "~/.config/deja/wiring.json README.md"},
	}}
	got := ResumeFrom(s, nil).Files
	joined := strings.Join(got, " ")
	for _, want := range []string{"how.go", "mcp.go", "resume.go", "wiring.json", "README.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("a real path was dropped: %q missing from %v", want, got)
		}
	}
	for _, prose := range []string{"the", "state", "as", "it", "was.", "exactly"} {
		for _, f := range got {
			if f == prose {
				t.Errorf("prose was named as a file: %q in %v", prose, got)
			}
		}
	}
}

// The five slots went to whatever was touched last, and on a real store that
// was a probe script under /tmp in 238 of 400 handovers — with a repository
// file that had lost its slot to one in 29 of them (#3716).
func TestFilesInFlightNamesTheWorkNotTheScratch(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: sources.RoleEdit, Text: "/Users/x/work/internal/index/retrieval.go"},
		{Role: sources.RoleEdit, Text: "/Users/x/work/.claude/worktrees/agent-a1/internal/index/search_rungs.go"},
		{Role: sources.RoleFiles, Text: "/private/tmp/deja-probe/run.sh /tmp/scan.py"},
		{Role: sources.RoleFiles, Text: "/Users/x/.claude/projects/proj/scratchpad/notes.md"},
		{Role: sources.RoleFiles, Text: "/Users/x/work/node_modules/left-pad/index.js"},
		{Role: sources.RoleFiles, Text: "/Users/x/work/out/build.log /var/folders/t/T/probe.go"},
		// Recorded relative to a directory above it, which is how a harness
		// that stores paths as typed writes a probe script.
		{Role: sources.RoleFiles, Text: "tmp/deja-probe/shot.txt"},
	}}
	got := ResumeFrom(s, nil).Files
	if len(got) != 2 || !strings.HasSuffix(got[0], "internal/index/search_rungs.go") || !strings.HasSuffix(got[1], "internal/index/retrieval.go") {
		t.Fatalf("the handover should name the work (including .claude/worktrees) and nothing else, got %v", got)
	}
	// And a session that touched only throwaway files says nothing about files
	// rather than sending the next turn to a probe script.
	only := model.Session{Messages: []model.Message{
		{Role: sources.RoleFiles, Text: "/private/tmp/deja-probe/run.sh"},
	}}
	if got := ResumeFrom(only, nil).Files; len(got) != 0 {
		t.Fatalf("got %v, want nothing to name", got)
	}
}

// The predicate itself, since it decides what an agent is told to open.
func TestLooksLikeAPath(t *testing.T) {
	for _, s := range []string{
		"/abs/path/file.go", "cmd/deja/how.go", `windows\path\file.txt`,
		"~/.config/deja/wiring.json", ".env", "README.md", "main.go",
	} {
		if !looksLikeAPath(s) {
			t.Errorf("%q is a path and was not taken", s)
		}
	}
	for _, s := range []string{
		"", "the", "state", "was.", "exactly", "it", "sentence,", "deja-vu",
		"word.longerthanextension", `\,`, "actions/checkout@3d3c42e5d204f500a0fd1c2fe4ac7ede2d22138c",
		"https://github.com/vshulcz/deja-vu/issues/new", "file.</p>", "~600",
		"[internal/embed]=93", "o.r", "*os.File", "strings.HasPrefix(m.Text",
	} {
		if looksLikeAPath(s) {
			t.Errorf("%q is prose and was taken as a path", s)
		}
	}
}

func TestResumeFromEditSpanWroteAndMidTurnWait(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "Исправил сохранение префиксов в normalizeFriction."},
		{Role: "assistant", Text: "Исправил сохранение префиксов в normalizeFriction, жду завершения тестов cmd/deja и golangci-lint."},
		{Role: sources.RoleEdit, Text: "/Users/x/work/.claude/worktrees/fix-wt/cmd/deja/files.go\nimport \"internal/bogus/span.go\""},
		{Role: sources.RoleWrote, Text: "/Users/x/work/.claude/worktrees/fix-wt/cmd/deja/blame.go\n0123456789abcdef"},
	}}
	r := ResumeFrom(s, nil)
	if strings.Contains(r.Decision, "жду завершения") {
		t.Errorf("mid-turn waiting update selected as decision: %q", r.Decision)
	}
	if !strings.Contains(r.Decision, "normalizeFriction") {
		t.Errorf("expected real decision, got %q", r.Decision)
	}
	wantFiles := []string{"/Users/x/work/cmd/deja/blame.go", "/Users/x/work/cmd/deja/files.go"}
	if len(r.Files) != len(wantFiles) || r.Files[0] != wantFiles[0] || r.Files[1] != wantFiles[1] {
		t.Errorf("Files = %v, want %v (collapsed worktree paths, no code span tokens)", r.Files, wantFiles)
	}
}

// The last thing said before a compaction is as often a nudge as it is the
// task. Read over 96 real compactions: taking the newest user turn reported
// `working on: да`, `working on: /compact`, and once a pasted HAR file.
func TestANudgeIsNotTheTask(t *testing.T) {
	nudges := []string{
		"да", "давай дальше", "продолжай", "ok", "go on", "спасибо",
		"/compact", "/clear",
		`{"log": {"version": "1.2", "entries": [{"request": {"url": "https://example.invalid"}}]}}`,
	}
	for _, n := range nudges {
		s := model.Session{Messages: []model.Message{
			{Role: "user", Text: "cap the quokkabloom retries at four and warm the cold path"},
			{Role: "user", Text: n},
		}}
		r := ResumeFrom(s, nil)
		if !strings.Contains(r.Asked, "quokkabloom") {
			t.Errorf("%q displaced the task: %q", n, r.Asked)
		}
	}
}

// And an agent announcing an answer is not the answer.
func TestAPreambleIsNotWhatWasSettled(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "assistant", Text: "Decision: the retry budget stays at four."},
		{Role: "assistant", Text: "Хорошо, отвечаю по существу — как работает pgbouncer."},
	}}
	r := ResumeFrom(s, nil)
	if !strings.Contains(r.Decision, "stays at four") {
		t.Errorf("a preamble displaced the decision: %q", r.Decision)
	}
}

// A long request is still a request: the bound is there for pasted documents,
// not for someone explaining themselves.
func TestALongRequestIsStillTheTask(t *testing.T) {
	long := "the orders worker keeps exhausting its database connections under load, " +
		"and I want the first change to be the one that bounds every query with a " +
		"context so a stalled call cannot hold a connection open"
	s := model.Session{Messages: []model.Message{{Role: "user", Text: long}}}
	if r := ResumeFrom(s, nil); !strings.Contains(r.Asked, "orders worker") {
		t.Errorf("a real request was dropped as a paste: %q", r.Asked)
	}
}

// A compaction costs a median of 28 actions before the next edit against 10 from
// a cold start, over 86 of them on a real machine. Nothing is missing from the
// store when it happens — the shape is. This is the shape, and every part of it
// is derived, because the two places an agent can write hold 9 writes against
// 3793 injections.
func TestTheHandoverIsWhatTheSessionWasDoing(t *testing.T) {
	now := time.Now().UTC()
	at := func(n int) time.Time { return now.Add(time.Duration(n) * time.Minute) }
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			// What a harness wrote is not what was asked.
			{Role: "user", Text: "Summary:\n1. **Primary Request and Intent:** the quokkabloom fetcher", Time: at(0)},
			{Role: "user", Text: "cap the quokkabloom retries and make the cold path warm the index", Time: at(1)},
			{Role: sources.RoleEdit, Text: "internal/fetch/quokkabloom.go", Time: at(2)},
			{Role: sources.RoleCommand, Text: "go test ./internal/fetch -run Quokkabloom", Time: at(3)},
			{Role: sources.RoleToolOutput, Text: "--- FAIL: TestQuokkabloomRetries\npanic: too many retries", Time: at(4)},
			{Role: "assistant", Text: "Decision: the retry budget stays at four and the cold path warms the index first.", Time: at(5)},
			{Role: sources.RoleEdit, Text: "internal/fetch/cold.go", Time: at(6)},
			{Role: sources.RoleCommand, Text: "go test ./internal/fetch -count=1", Time: at(7)},
			{Role: sources.RoleToolOutput, Text: "ok  \tinternal/fetch\t1.2s", Time: at(8)},
		},
	}
	r := ResumeFrom(s, func(out string) bool { return strings.Contains(out, "FAIL") })
	if strings.Contains(r.Asked, "Primary Request") {
		t.Errorf("a compaction summary is reported as the task: %q", r.Asked)
	}
	if !strings.Contains(r.Asked, "cap the quokkabloom retries") {
		t.Errorf("the task is missing: %q", r.Asked)
	}
	if !strings.Contains(r.Decision, "retry budget stays at four") {
		t.Errorf("what the session settled is missing: %q", r.Decision)
	}
	if len(r.Files) == 0 || r.Files[0] != "internal/fetch/cold.go" {
		t.Errorf("the files in flight are not newest first: %v", r.Files)
	}
	if r.Command != "go test ./internal/fetch -count=1" {
		t.Errorf("the last command is wrong: %q", r.Command)
	}
	if r.Failed {
		t.Error("the last command passed, and the handover says it failed")
	}
	lines := strings.Join(r.Lines(), "\n")
	for _, want := range []string{"working on:", "settled:", "files in flight:", "last command:"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the rendered handover is missing %q:\n%s", want, lines)
		}
	}
}

// A failure is the part that changes what the next turn does first, so it is
// said as one.
func TestAHandoverSaysTheLastCommandFailed(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: sources.RoleCommand, Text: "go build ./..."},
			{Role: sources.RoleToolOutput, Text: "internal/fetch/cold.go:12: undefined: warmIndex"},
		},
	}
	r := ResumeFrom(s, func(out string) bool { return strings.Contains(out, "undefined") })
	if !r.Failed {
		t.Fatal("the failure did not reach the handover")
	}
	if !strings.Contains(strings.Join(r.Lines(), "\n"), "last command failed: go build") {
		t.Errorf("the handover does not say it failed:\n%s", strings.Join(r.Lines(), "\n"))
	}
}

// Nothing to hand over is its own answer: a session with only plumbing in it
// must not produce a line.
func TestAHandoverFromPlumbingIsEmpty(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s3",
		Messages: []model.Message{
			{Role: "user", Text: "<system-reminder>\nTERSE MODE ACTIVE\n</system-reminder>"},
			{Role: "assistant", Text: "File created successfully at: /work/app/notes.txt"},
		},
	}
	if r := ResumeFrom(s, nil); !r.Empty() {
		t.Errorf("plumbing produced a handover: %+v", r)
	}
}

func TestHandoverSkipsContinuationNudgesAndClipsChainedCommands(t *testing.T) {
	longCmd := "git push -u origin fix/branch && gh pr create --title 'fix: trim handover commands' --body '" +
		strings.Repeat("long body word ", 30) + "'"
	s := model.Session{
		Harness: "claude", ID: "s4",
		Messages: []model.Message{
			{Role: "user", Text: "audit the live store surfaces and fix the six regressions"},
			{Role: "user", Text: "мержи и давай думать"},
			{Role: "user", Text: "давай еще думать дальше"},
			{Role: sources.RoleCommand, Text: longCmd + "\n| row | val |"},
		},
	}
	r := ResumeFrom(s, nil)
	if !strings.Contains(r.Asked, "audit the live store surfaces") {
		t.Errorf("continuation nudge overwrote the task: %q", r.Asked)
	}
	if len([]rune(r.Command)) > resumeCommandMax+1 || !strings.HasSuffix(r.Command, "…") {
		t.Errorf("chained command was not clipped to %d runes: %d (%q)", resumeCommandMax, len([]rune(r.Command)), r.Command)
	}
	if got := MessageText("Result summary:\n| col1 | col2 |\n|---|---|\nAll checks passed."); strings.Contains(got, "|") {
		t.Errorf("MessageText kept markdown table rows: %q", got)
	}
}

func TestResumeTrajectorySynthesisAndEntityAwareAsks(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s-traj",
		Messages: []model.Message{
			// Short ask (< 20 runes) that names a concrete code file: must be kept as Asked.
			{Role: "user", Text: "почини `auth.go`"},
			// Mid-turn assistant preamble before exploration tool call (no word from aboutToExplain,
			// but structurally followed by RoleFiles before any edit): must NOT become Decision.
			{Role: "assistant", Text: "В итоге посмотрим содержимое пакета авторизации."},
			{Role: sources.RoleFiles, Text: "internal/auth/auth.go"},
			{Role: sources.RoleEdit, Text: "internal/auth/auth.go"},
			{Role: sources.RoleCommand, Text: "go test ./internal/auth/... → exit 0"},
			// Turn-final assistant synthesis after Edit + passing command, naming code entities
			// without using any cliché phrase from decisionMarkers: must become Decision.
			{Role: "assistant", Text: "Заменил `\\b` в `verifyTokenRE` на `(?:$|[^\\p{L}])` для корректной границы кириллических токенов."},
			// Multilingual compositional continuation nudges: must NOT overwrite Asked.
			{Role: "user", Text: "супер, погнали дальше"},
			{Role: "user", Text: "sounds good, keep going"},
			{Role: "user", Text: "太好了，继续往下走"},
			{Role: "user", Text: "いいね、そのまま進めて"},
		},
	}
	r := ResumeFrom(s, nil)
	if r.Asked != "почини `auth.go`" {
		t.Errorf("Asked = %q, want %q", r.Asked, "почини `auth.go`")
	}
	wantDec := "Заменил `\\b` в `verifyTokenRE` на `(?:$|[^\\p{L}])` для корректной границы кириллических токенов."
	if r.Decision != wantDec {
		t.Errorf("Decision = %q, want %q", r.Decision, wantDec)
	}
}
