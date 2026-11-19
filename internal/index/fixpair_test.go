package index

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"weak"
)

func TestMineEditContextDeclarationsAndCalls(t *testing.T) {
	t.Parallel()

	// Out-of-bounds editIdx returns empty cleanly.
	if sym, why := mineEditContext(nil, 0, -1, "pkg/store.go", "panic: bad"); sym != "" || why != "" {
		t.Fatalf("expected empty for negative editIdx, got %q %q", sym, why)
	}

	// Go method declaration + adjacent edit body + causal sentence with inline code and code fence.
	ms := []model.Message{
		{Role: roleToolOutput, Text: "FAIL: lease quorum overflow in store"},
		{Role: "assistant", Text: "```go\n// ignored code block because of fence\n```\nLet me inspect the lease check first?"},
		{Role: roleEdit, Text: "pkg/lease_store.go\nfunc (s *LeaseStore) RenewQuorum(ctx context.Context) error {"},
		{Role: roleEdit, Text: "pkg/lease_store.go\n\treturn s.commitEpoch(ctx)"},
		{Role: "assistant", Text: "- Because `RenewQuorum` skipped monotonic epoch validation. Moving on!"},
		{Role: "user", Text: "now check another thing because RenewQuorum was mentioned"},
	}
	sym, why := mineEditContext(ms, 0, 2, "pkg/lease_store.go", "FAIL: lease quorum overflow")
	if sym != "LeaseStore.RenewQuorum" {
		t.Fatalf("expected LeaseStore.RenewQuorum, got %q", sym)
	}
	if !strings.Contains(why, "RenewQuorum") || !strings.Contains(why, "monotonic epoch") {
		t.Fatalf("expected causal why mentioning RenewQuorum, got %q", why)
	}

	// Python def, Rust pub fn, TypeScript export async function.
	langCases := []struct {
		path string
		body string
		want string
	}{
		{"worker/sync.py", "async def reconcile_shard(cursor):\n    return cursor", "reconcile_shard"},
		{"src/fencing.rs", "pub async fn verify_epoch_token(tok: u64) -> bool {", "verify_epoch_token"},
		{"src/router.ts", "export async function dispatchWebhookBatch(items: Item[]) {", "dispatchWebhookBatch"},
		{"pkg/types.go", "type SnapshotCursor struct {\n\tOffset int64\n}", "SnapshotCursor"},
	}
	for _, tc := range langCases {
		editMsgs := []model.Message{
			{Role: roleToolOutput, Text: "error: assertion failed"},
			{Role: roleEdit, Text: tc.path + "\n" + tc.body},
		}
		gotSym, _ := mineEditContext(editMsgs, 0, 1, tc.path, "error: assertion failed")
		if gotSym != tc.want {
			t.Errorf("%s: got symbol %q, want %q", tc.path, gotSym, tc.want)
		}
	}

	// Backtick symbol fallback and direct match in body, skipping extensions/stdlib/test symbols.
	btMsgs := []model.Message{
		{Role: roleToolOutput, Text: "panic: nil cursor"},
		{Role: "assistant", Text: "Checked `config.json`, `strings.Split`, `TestCursor`, and `CursorTracker.AdvanceOffset` before editing."},
		{Role: roleEdit, Text: "pkg/cursor.go\n\tif ok := tracker.AdvanceOffset(next); !ok {"},
	}
	if got := extractEditedSymbol(btMsgs, 0, 2, "pkg/cursor.go"); got != "CursorTracker.AdvanceOffset" {
		t.Fatalf("expected backtick match CursorTracker.AdvanceOffset, got %q", got)
	}

	// Qualified call fallback when no declaration or backtick symbol is present.
	callMsgs := []model.Message{
		{Role: roleToolOutput, Text: "error: bad frame"},
		{Role: roleEdit, Text: "pkg/codec.go\n\tfmt.Println(x)\n\tcodec. Close()\n\tdecoder.ValidateFrameHeader(buf)"},
	}
	if got := extractEditedSymbol(callMsgs, 0, 1, "pkg/codec.go"); got != "ValidateFrameHeader" {
		t.Fatalf("expected qualified call ValidateFrameHeader, got %q", got)
	}
}

func TestCausalHelpersAndMultiWordIdentifiers(t *testing.T) {
	t.Parallel()

	for _, okIdent := range []string{"snake_case", "camelCase", "TwoWords", "LeaseStore"} {
		if !isMultiWordIdent(okIdent) {
			t.Errorf("expected isMultiWordIdent(%q) = true", okIdent)
		}
	}
	for _, badIdent := range []string{"_leading", "trailing_", "UPPERONLY", "Close", "split"} {
		if isMultiWordIdent(badIdent) {
			t.Errorf("expected isMultiWordIdent(%q) = false", badIdent)
		}
	}

	for _, badSym := range []string{"short", "file.go", "data.jsonl", "fmt.Sprintf", "TestHelper", "pkg.TestHelper", "Close"} {
		if isPlausibleBacktickSymbol(badSym) {
			t.Errorf("expected isPlausibleBacktickSymbol(%q) = false", badSym)
		}
	}
	for _, goodSym := range []string{"store.FlushWalBatch", "reconcile_offsets", " AdvanceCursor"[1:]} {
		if !isPlausibleBacktickSymbol(goodSym) {
			t.Errorf("expected isPlausibleBacktickSymbol(%q) = true", goodSym)
		}
	}

	// Russian causal marker + long sentence clipping on word boundary.
	longWhy := "Проблема возникала потому что в worker_lease происходило повторное чтение устаревшего курсора очереди при высокой конкурентной нагрузке воркеров кластера и отсутствии блокировки чтения."
	ms := []model.Message{
		{Role: roleToolOutput, Text: "FAIL: worker_lease duplicate delivery"},
		{Role: roleEdit, Text: "pkg/worker_lease.go\nreturn renew()"},
		{Role: "assistant", Text: "See https://example.com/pr/1 (merged).\n" + longWhy},
	}
	_, why := mineEditContext(ms, 0, 1, "pkg/worker_lease.go", "FAIL: worker_lease duplicate delivery")
	if !strings.HasSuffix(why, "…") || len([]rune(why)) > 164 {
		t.Fatalf("expected clipped Russian causal why ending with '…', got %q (len=%d)", why, len([]rune(why)))
	}

	// Anchor via error term only (short file stem "io.go" and no symbol).
	errTermMsgs := []model.Message{
		{Role: roleToolOutput, Text: "panic: deadlock detected in coordinator"},
		{Role: roleEdit, Text: "io.go\nx = 1"},
		{Role: "assistant", Text: "* **Root cause**: The panic occurred because deadlock avoidance skipped lock ordering."},
	}
	if _, errWhy := mineEditContext(errTermMsgs, 0, 1, "io.go", "panic: deadlock detected in coordinator"); !strings.Contains(errWhy, "deadlock") {
		t.Fatalf("expected error-term anchored why, got %q", errWhy)
	}
}

func TestAssistantTurnsEdgeCases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Empty / unindexed directory and empty key sets return nil cleanly.
	recs, err := RecentSpokenAssistantTurnsForTerms(dir, []string{"ab", "quorum"}, map[string]bool{}, 5)
	if err != nil || len(recs) != 0 {
		t.Fatalf("expected empty records on empty allowedKeys, got %d, err=%v", len(recs), err)
	}
	recs, err = RecentSpokenAssistantTurnsForTerms(dir, []string{"short"}, nil, 0)
	if err != nil || len(recs) != 0 {
		t.Fatalf("expected empty records when all terms < 5 chars, got %d, err=%v", len(recs), err)
	}
	giveUps, err := GiveUpRecordsForKeys(dir, map[string]bool{})
	if err != nil || len(giveUps) != 0 {
		t.Fatalf("expected empty give-up records for empty keys, got %d, err=%v", len(giveUps), err)
	}
	if _, err := GiveUpRecordsForKeys(dir, map[string]bool{"missing": true}); err == nil {
		t.Fatalf("expected error on missing manifest")
	}
	if !isTestFileEditPath("pkg/relay_test.go") || !isTestFileEditPath("tests/test_worker.py") || isTestFileEditPath("pkg/relay.go") {
		t.Errorf("unexpected isTestFileEditPath behavior")
	}
	if !looksLikeEditedPath(`C:\Users\runneradmin\work\project\retry.go`) || !looksLikeEditedPath(`c:/Users/runneradmin/work/project/retry.go`) {
		t.Errorf("expected looksLikeEditedPath to accept Windows paths with drive letter")
	}
	if looksLikeEditedPath(`C:\Users\runneradmin\.claude\projects\s1.jsonl`) || looksLikeEditedPath("bad:path:with:colons") {
		t.Errorf("expected looksLikeEditedPath to reject claude project paths and non-drive colons")
	}
	if !preferEditRemedyCandidate("pkg/relay_test.go", "pkg/relay.go", 1, 2, false) || preferEditRemedyCandidate("pkg/relay.go", "pkg/relay_test.go", 1, 2, false) {
		t.Errorf("unexpected preferEditRemedyCandidate behavior")
	}
	priorMsgs := []model.Message{
		{Role: roleEdit, Text: "./pkg/relay.go\nold"},
		{Role: roleToolOutput, Text: "FAIL: TestRelay"},
	}
	if !hadPriorSameFileEdit(priorMsgs, 1, "pkg/relay.go") || hadPriorSameFileEdit(priorMsgs, 1, "pkg/other.go") {
		t.Errorf("unexpected hadPriorSameFileEdit behavior")
	}
}

// What fixes a failing test is an edit, not a command. The pair miner only
// ever looked for the next command, so the most common failure an agent sees —
// 2,318 of 6,744 error lines on a real store — could never have a remedy
// recorded, and the next session hit it with nothing to show (#2163).
func TestATestFailureIsPairedWithTheEditThatFollowed(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
		{Role: "edit", Text: "internal/pool/pool.go\n-\tclose(c)\n+\tc.drain()", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok  \tinternal/pool\t0.4s", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want the edit: %+v", len(pairs), pairs)
	}
	if pairs[0].Edit != "internal/pool/pool.go" {
		t.Errorf("pair records %q as the edit", pairs[0].Edit)
	}
	if pairs[0].Command != "" {
		t.Errorf("an edit remedy must not be stored as a command to run: %q", pairs[0].Command)
	}
}

// A command remedy still wins where there is one: a missing binary is fixed by
// installing it, and the edit that happens to follow is not the answer.
func TestACommandRemedyStillBeatsAnEdit(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh: command not found: rg", Time: now},
		// The edit comes first and is not the answer: the session tried a
		// change, then installed the thing that was missing.
		{Role: "edit", Text: "Makefile\n-rg\n+grep", Time: now.Add(time.Minute)},
		{Role: "command", Text: "brew install ripgrep", Time: now.Add(2 * time.Minute)},
		{Role: "tool-output", Text: "installed", Time: now.Add(3 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Command != "brew install ripgrep" || pairs[0].Edit != "" {
		t.Fatalf("the command remedy was not kept: %+v", pairs)
	}
}

// An edit is one session's doing until a second session does the same after
// the same failure — the same bar a command remedy that names nothing of the
// error has to clear.
func TestAnEditRemedyIsACandidateUntilASecondSessionRepeatsIt(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
			{Role: "edit", Text: "internal/pool/pool.go\n-\tclose(c)\n+\tc.drain()", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	first := judgeFixes(session("s1"))
	if len(first) != 1 || !first[0].Candidate {
		t.Fatalf("one sighting of an edit must be a candidate: %+v", first)
	}
	second := judgeFixes(append(first, session("s2")...))
	if len(second) != 1 || second[0].Candidate {
		t.Fatalf("a second session doing the same edit must confirm it: %+v", second)
	}
}

// The first line of an edit record is the file it changed — unless the harness
// recorded no file, in which case it is a line of source, and a line of source
// is not a remedy.
func TestALineOfSourceIsNotAnEditRemedy(t *testing.T) {
	now := time.Now()
	pairs := fixPairsIn([]model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
		{Role: "edit", Text: "\tif err != nil {\n\t\treturn err", Time: now.Add(time.Minute)},
	}, "claude:s1", "p")
	if len(pairs) != 0 {
		t.Errorf("a line of source was stored as the file that was changed: %+v", pairs)
	}
}

func TestEditRemedyMinesSymbolAndCausalWhy(t *testing.T) {
	now := time.Now()
	s1 := fixPairsIn([]model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now},
		{Role: "assistant", Text: "**Root cause:** `Pool.drain` closed the work channel because workers were still reading in-flight items.", Time: now.Add(10 * time.Second)},
		{Role: "edit", Text: "internal/pool/pool.go\nfunc (p *Pool) drain() {\n-\tclose(p.work)\n+\tp.wg.Wait()\n+\tclose(p.work)", Time: now.Add(20 * time.Second)},
		{Role: "tool-output", Text: "ok  \tinternal/pool\t0.2s", Time: now.Add(30 * time.Second)},
	}, "claude:s1", "app")
	if len(s1) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(s1), s1)
	}
	if s1[0].Symbol != "Pool.drain" {
		t.Errorf("Symbol = %q, want %q", s1[0].Symbol, "Pool.drain")
	}
	if s1[0].Why != "`Pool.drain` closed the work channel because workers were still reading in-flight items." {
		t.Errorf("Why = %q", s1[0].Why)
	}

	// Second session confirms the edit with only stdlib calls in the hunk and no
	// explanation; judgeFixes must keep the first sighting's Symbol and Why and
	// must not mistake strings.TrimSpace for a domain symbol.
	s2 := fixPairsIn([]model.Message{
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now.Add(time.Hour)},
		{Role: "edit", Text: "internal/pool/pool.go\n\tname := strings.TrimSpace(raw)\n-\tclose(p.work)\n+\tp.wg.Wait()", Time: now.Add(time.Hour + time.Minute)},
	}, "claude:s2", "app")
	if len(s2) != 1 {
		t.Fatalf("s2 got %d pairs", len(s2))
	}
	if s2[0].Symbol != "" {
		t.Errorf("stdlib call should not be mined as Symbol, got %q", s2[0].Symbol)
	}
	judged := judgeFixes(append(s1, s2...))
	if len(judged) != 1 || judged[0].Candidate {
		t.Fatalf("expected 1 confirmed pair, got %+v", judged)
	}
	if judged[0].Symbol != "Pool.drain" || judged[0].Why == "" {
		t.Errorf("judgeFixes lost causal metadata from earlier sighting: %+v", judged[0])
	}
}

func TestPassingVerifyCommandKeepsEditRemedyInsteadOfOverwritingWithTestRunner(t *testing.T) {
	now := time.Now()
	pairs := fixPairsIn([]model.Message{
		{Role: "command", Text: "go test -run TestPoolDrainsOnClose ./internal/pool", Time: now},
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)", Time: now.Add(5 * time.Second)},
		{Role: "edit", Text: "internal/pool/pool.go\nfunc (p *Pool) drain() {\n-\tclose(p.work)\n+\tp.wg.Wait()\n+\tclose(p.work)", Time: now.Add(15 * time.Second)},
		{Role: "command", Text: "go test -run TestPoolDrainsOnClose ./internal/pool", Time: now.Add(25 * time.Second)},
		{Role: "tool-output", Text: "ok  \tinternal/pool\t0.18s", Time: now.Add(30 * time.Second)},
	}, "claude:s1", "app")
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(pairs), pairs)
	}
	if pairs[0].Edit != "internal/pool/pool.go" || pairs[0].Command != "" {
		t.Fatalf("expected Edit remedy instead of VerifyCommand runner, got %+v", pairs[0])
	}
	// Without a prior edit to pool.go before the failure, a single sighting remains a candidate.
	judged := judgeFixes(pairs)
	if len(judged) != 1 || !judged[0].Candidate {
		t.Fatalf("single unintroduced edit should stay Candidate until repeated, got %+v", judged)
	}
}

func TestSelfCorrectedEditLoopConfirmsInSingleSessionAndSplitsMultiSentenceWhy(t *testing.T) {
	now := time.Now()
	pairs := fixPairsIn([]model.Message{
		{Role: "edit", Text: "./internal/pool/pool.go\nfunc (p *Pool) drain() {\n+\tclose(p.work)", Time: now},
		{Role: "command", Text: "go test ./internal/pool", Time: now.Add(10 * time.Second)},
		{Role: "tool-output", Text: "--- FAIL: TestPoolDrainsOnClose (0.09s)\n    pool_test.go:42: send on closed channel", Time: now.Add(15 * time.Second)},
		{Role: "edit", Text: "internal/pool/pool_test.go\n+// regression guard", Time: now.Add(20 * time.Second)},
		{Role: "edit", Text: "internal/pool/pool.go\nfunc (p *Pool) drain() {\n-\tclose(p.work)\n+\tp.wg.Wait()\n+\tclose(p.work)", Time: now.Add(25 * time.Second)},
		{Role: "command", Text: "go test ./internal/pool", Time: now.Add(35 * time.Second)},
		{Role: "tool-output", Text: "ok  \tinternal/pool\t0.14s", Time: now.Add(40 * time.Second)},
		{Role: "assistant", Text: "Found it. `Pool.drain` panicked because closing the work channel before `wg.Wait()` raced with active senders. All package tests now pass and the build is clean.", Time: now.Add(45 * time.Second)},
	}, "claude:s1", "app")
	if len(pairs) != 1 {
		t.Fatalf("got %d pairs, want 1: %+v", len(pairs), pairs)
	}
	if pairs[0].Edit != "internal/pool/pool.go" {
		t.Fatalf("expected implementation file pool.go to win over _test.go edit, got %+v", pairs[0])
	}
	if !pairs[0].SelfCorrected {
		t.Fatalf("expected SelfCorrected=true on Edit1 -> FAIL -> Edit2 -> VerifyCommand PASS loop, got %+v", pairs[0])
	}
	if pairs[0].Symbol != "Pool.drain" {
		t.Errorf("Symbol = %q, want Pool.drain", pairs[0].Symbol)
	}
	if pairs[0].Why != "`Pool.drain` panicked because closing the work channel before `wg.Wait()` raced with active senders." {
		t.Errorf("Why = %q", pairs[0].Why)
	}
	judged := judgeFixes(pairs)
	if len(judged) != 1 || judged[0].Candidate {
		t.Fatalf("expected SelfCorrected edit pair to be confirmed in single session, got %+v", judged)
	}
}

func msgs(roleText ...string) []model.Message {
	now := time.Now()
	var ms []model.Message
	for i := 0; i+1 < len(roleText); i += 2 {
		ms = append(ms, model.Message{Role: roleText[i], Text: roleText[i+1], Time: now.Add(time.Duration(i) * time.Second)})
	}
	return ms
}

// zsh refusing `echo ===` says nothing about the code; the next file the
// session edited and the next unrelated command it ran are not answers to it.
// On one machine this wall was answered "changed next: <unrelated file>" for
// 57 repeat failures.
func TestALineTheShellRefusedIsNotAnsweredByTheNextEdit(t *testing.T) {
	ms := msgs(
		"command", "sed -n 1,20p a.go; echo ===; sed -n 1,20p b.go",
		"tool-output", "(eval):1: == not found",
		"edit", "internal/index/retrieval.go\nfunc x() {}",
		"command", "go build ./...",
		"tool-output", "",
	)
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Fatalf("a refused line paired with what came after it: %+v", pairs)
	}
}

// The same line in a form that runs is the answer.
func TestALineTheShellRefusedIsAnsweredByItselfCorrected(t *testing.T) {
	ms := msgs(
		"command", "sed -n 1,20p a.go; echo ===; sed -n 1,20p b.go",
		"tool-output", "(eval):1: == not found",
		"command", "sed -n 1,20p a.go; echo ---; sed -n 1,20p b.go",
		"tool-output", "package a",
	)
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || !pairs[0].Repaired {
		t.Fatalf("want the corrected line as a repaired pair, got %+v", pairs)
	}
}

// A shared `cd` in front made both sides a navigation, which the repair rule
// turns away, so `timeout` dropped from `cd wt && timeout 30 go test` was
// never recognised as the fix it is.
func TestADroppedWrapperBehindACdIsARepair(t *testing.T) {
	ms := msgs(
		"command", "cd /work/wt && timeout 30 go test ./cmd/deja -run TestX",
		"tool-output", "zsh:1: command not found: timeout",
		"command", "cd /work/wt && go test ./cmd/deja -run TestX",
		"tool-output", "ok  \texample.com/cmd/deja\t0.4s",
	)
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || !pairs[0].Repaired {
		t.Fatalf("want a repaired pair, got %+v", pairs)
	}
	if !pairs[0].MachineFact() {
		t.Error("a missing program answered by the command without it is a fact about the machine")
	}
}

// gh missing is answered by another program doing its job on the same
// repository: what 34 of 49 Hermes sessions on one machine did.
func TestAMissingProgramIsAnsweredByOneDoingItsJob(t *testing.T) {
	ms := msgs(
		"command", "gh pr view 1463 --repo acme/widgets --json title,body",
		"tool-output", "/bin/bash: line 2: gh: command not found",
		"command", "git status --short",
		"tool-output", "",
		"command", "git ls-remote https://github.com/acme/widgets.git refs/pull/1463/head",
		"tool-output", "abc123\trefs/pull/1463/head",
	)
	pairs := fixPairsIn(ms, "hermes:s1", "p")
	if len(pairs) != 1 || !pairs[0].Substitute || pairs[0].Command != "git ls-remote https://github.com/acme/widgets.git refs/pull/1463/head" {
		t.Fatalf("want the ls-remote as the substitute, got %+v", pairs)
	}
	if pairs[0].Candidate || !selfEvidentPair(pairs[0]) || !pairs[0].MachineFact() {
		t.Errorf("a substitute stands on its own evidence: %+v", pairs[0])
	}
}

// One shared word is a coincidence: `gh auth status` and `git status`.
func TestOneSharedWordIsNotASubstitute(t *testing.T) {
	if substitutedProgram("gh: command not found", "gh auth status", "git status --short --branch") {
		t.Error("git status read as doing gh auth status's job")
	}
}

// Hermes runs a turn's calls together and stores the outputs after every
// command, so the record before the error is someone else's output. The error
// names the program, and that names the command.
func TestTheFailingLineIsFoundByTheProgramTheErrorNames(t *testing.T) {
	ms := msgs(
		"command", "gh api repos/acme/widgets/issues/1625 --jq .title",
		"command", "cat notes.md",
		"tool-output", "# notes",
		"tool-output", "/bin/bash: line 2: gh: command not found",
		"command", "git ls-remote git@github.com:acme/widgets.git refs/pull/1625/head",
		"tool-output", "abc\trefs/pull/1625/head",
	)
	pairs := fixPairsIn(ms, "hermes:s1", "p")
	if len(pairs) != 1 || !pairs[0].Substitute {
		t.Fatalf("want a substitute found through the named program, got %+v", pairs)
	}
}

// An install that names what was missing is still the answer.
func TestAnInstallStillAnswersAMissingModule(t *testing.T) {
	ms := msgs(
		"command", "python3 scan.py",
		"tool-output", "ModuleNotFoundError: No module named 'yaml'",
		"command", "python3 -m pip install pyyaml",
		"tool-output", "Successfully installed pyyaml",
	)
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 1 || pairs[0].Command != "python3 -m pip install pyyaml" {
		t.Fatalf("want the install, got %+v", pairs)
	}
}

// Only the shell's refusals change. A failing test is still answered by the
// file the session edited after it.
func TestAFailureThatRanStillPairsWithTheEdit(t *testing.T) {
	ms := msgs(
		"tool-output", "--- FAIL: TestPoolDrainsOnClose (0.09s)",
		"edit", "calc/calc.go\nreturn a + b",
		"tool-output", "ok  \tcalc\t0.4s",
	)
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Edit != "calc/calc.go" {
		t.Fatalf("want the edit pair, got %+v", pairs)
	}
}

func TestMachineFactIsOnlyAMissingProgramAnswered(t *testing.T) {
	cases := []struct {
		p    FixPair
		want bool
	}{
		{FixPair{Error: "command not found: timeout", Command: "go test ./...", Repaired: true}, true},
		{FixPair{Error: "No module named 'yaml'", Command: "pip install pyyaml"}, false},
		{FixPair{Error: "command not found: timeout", Command: "go test ./...", Repaired: true, Candidate: true}, false},
		{FixPair{Error: "--- FAIL: TestAdd", Command: "go test ./x -run TestAdd", Repaired: true}, false},
	}
	for _, c := range cases {
		if got := c.p.MachineFact(); got != c.want {
			t.Errorf("%+v: MachineFact = %v, want %v", c.p, got, c.want)
		}
	}
}

// A repaired remedy is the failing command corrected, so what makes it the
// answer is the command before it. Without that command stored the remedy is a
// long line naming nothing of the error: 107 of the 360 pairs served on a real
// store are that shape.
func TestARepairedPairCarriesTheCommandThatFailed(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: `grep -rn "deja" --include=*.go .`, Time: now},
		{Role: "tool-output", Text: "zsh:1: no matches found: --include=*.go", Time: now.Add(time.Second)},
		{Role: "command", Text: `grep -rn "deja" --include=\*.go .`, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "3 matches", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || !pairs[0].Repaired {
		t.Fatalf("want one repaired pair, got %+v", pairs)
	}
	if pairs[0].Failed != `grep -rn "deja" --include=*.go .` {
		t.Errorf("the pair does not say what it repaired: %q", pairs[0].Failed)
	}
}

// Every other remedy is a command someone ran after the error, not that
// command corrected, and saying "after this failed" about it would be a claim
// the store cannot make.
func TestAnOrdinaryPairCarriesNoFailingCommand(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "make build", Time: now},
		{Role: "tool-output", Text: "zsh: command not found: shellcheck", Time: now.Add(time.Second)},
		{Role: "command", Text: "brew install shellcheck", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "installed", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Repaired {
		t.Fatalf("want one ordinary pair, got %+v", pairs)
	}
	if pairs[0].Failed != "" {
		t.Errorf("an ordinary remedy claims to repair %q", pairs[0].Failed)
	}
}

// pairStore writes one session per call into the same project and re-indexes,
// so sessions arrive the way they really do: one at a time.
func pairStore(t *testing.T) (dir, proj string) {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj = filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(tmp, "index.db"), proj
}

// writePairSession puts an error and the command run after it into one session.
func writePairSession(t *testing.T, proj, id, errText, cmd string) { //nolint:unparam // the call names the error the session holds
	t.Helper()
	line := func(role, text string) string {
		switch role {
		case "tool":
			return `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + text + `"}]}}` + "\n"
		default:
			return `{"type":"assistant","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + text + `"}}]}}` + "\n"
		}
	}
	body := line("tool", errText) + line("cmd", cmd)
	if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A pair whose only evidence is that another session did the same thing after
// the same error was lost when the sessions arrived one at a time: the first
// candidate was rejected and forgotten, so the second had nothing to count
// against (#1301).
func TestACrossSessionPairSurvivesOneAtATimeArrival(t *testing.T) {
	dir, proj := pairStore(t)
	const errText = "undefined: renderInvoice in vendor/billing/api.go"
	const cmd = "go mod vendor \\u0026\\u0026 go build ./..."

	writePairSession(t, proj, "one", errText, cmd)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// One session is a sighting, and it is served as one.
	if got := FixesFor(dir, errText, 3, nil); len(got) != 1 || !got[0].Candidate {
		t.Fatalf("one session should come back marked unconfirmed, got %+v", got)
	}

	writePairSession(t, proj, "two", errText, cmd)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got := FixesFor(dir, errText, 3, nil)
	if len(got) == 0 {
		t.Fatalf("the second session confirmed the remedy and the pair is still missing")
	}
	// And the confirmed pair replaces the sighting rather than sitting beside it.
	for _, g := range got {
		if g.Candidate {
			t.Errorf("a sighting was still served after the pair was confirmed: %+v", g)
		}
	}
}

// The whole-corpus build and the one-at-a-time build have to agree, which is
// the measurement the issue reported failing.
func TestPairsAgreeWhicheverWayTheIndexGotThere(t *testing.T) {
	const errText = "undefined: renderInvoice in vendor/billing/api.go"
	const cmd = "go mod vendor \\u0026\\u0026 go build ./..."

	grownDir, grownProj := pairStore(t)
	writePairSession(t, grownProj, "one", errText, cmd)
	if err := Ensure(grownDir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	writePairSession(t, grownProj, "two", errText, cmd)
	if err := Ensure(grownDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	wholeDir, wholeProj := pairStore(t)
	writePairSession(t, wholeProj, "one", errText, cmd)
	writePairSession(t, wholeProj, "two", errText, cmd)
	if err := Ensure(wholeDir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	whole := len(FixesFor(wholeDir, errText, 3, nil))
	grown := len(FixesFor(grownDir, errText, 3, nil))
	if whole != grown {
		t.Errorf("built whole gives %d pairs, grown one session at a time gives %d", whole, grown)
	}
}

// A candidate is not an answer. Until a second session confirms it, nothing
// serves it — one session doing something after an error is half the evidence.
func TestASightingIsServedAndKeptForConfirmation(t *testing.T) {
	dir, proj := pairStore(t)
	const errText = "undefined: renderInvoice in vendor/billing/api.go"
	writePairSession(t, proj, "one", errText, "go mod vendor \\u0026\\u0026 go build ./...")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Served, and served as what it is: the caller marks it, because the store
	// holds a sighting for four of every five real failures and silence was
	// costing all of them.
	got := FixesFor(dir, errText, 3, nil)
	if len(got) != 1 || !got[0].Candidate {
		t.Errorf("a sighting was withheld and the failure answered with silence: %+v", got)
	}
	// It is on file, though — that is the point.
	held := 0
	for _, p := range ReadFixes(dir) {
		if p.Candidate {
			held++
		}
	}
	if held == 0 {
		t.Error("the sighting was thrown away, so a later session has nothing to confirm")
	}
}

// The incremental path has its own merge, and it is the one a live machine
// actually runs: sessions arrive one at a time, and the carried table is where
// the earlier sighting has to be waiting.
func TestTheIncrementalMergePromotesACarriedCandidate(t *testing.T) {
	dir := t.TempDir()
	const errText = "psql: connection refused on port 5432"
	const cmd = "brew services start postgresql"
	carried := []FixPair{{
		Sig: frictionHash(errText), Error: errText, Command: cmd,
		Key: "claude:one", When: time.Now().Add(-time.Hour), Candidate: true,
	}}
	if err := writeGob(fixesPath(dir), carried); err != nil {
		t.Fatal(err)
	}
	// A second session does the same thing after the same error.
	second := model.Session{Harness: "claude", ID: "two", Messages: []model.Message{
		{Role: roleToolOutput, Text: errText, Time: time.Now()},
		{Role: roleCommand, Text: cmd, Time: time.Now()},
	}}
	mergeFixes(dir, dir, nil, []model.Session{second}, map[string]bool{})
	served := 0
	for _, p := range ReadFixes(dir) {
		if !p.Candidate {
			served++
		}
	}
	if served == 0 {
		t.Errorf("a carried sighting plus a fresh one did not make a pair: %+v", ReadFixes(dir))
	}
	// And the candidate copy of the same thing is gone, so the table does not
	// carry both readings of one fact.
	for _, p := range ReadFixes(dir) {
		if p.Candidate && p.Command == cmd {
			t.Errorf("the promoted pair is still marked a candidate: %+v", p)
		}
	}
}

// And the merge keeps a sighting it sees for the first time, so the session
// that arrives tomorrow has something to confirm. Two updates, one session
// each, which is what a live machine does all day.
func TestTheIncrementalMergeKeepsAFirstSighting(t *testing.T) {
	dir := t.TempDir()
	const errText = "psql: connection refused on port 5432"
	const cmd = "brew services start postgresql"
	session := func(id string, when time.Time) model.Session {
		return model.Session{Harness: "claude", ID: id, Messages: []model.Message{
			{Role: roleToolOutput, Text: errText, Time: when},
			{Role: roleCommand, Text: cmd, Time: when.Add(time.Second)},
		}}
	}
	mergeFixes(dir, dir, nil, []model.Session{session("one", time.Now().Add(-time.Hour))}, map[string]bool{})
	if got := FixesFor(dir, errText, 3, nil); len(got) != 1 || !got[0].Candidate {
		t.Fatalf("one sighting should come back marked unconfirmed: %+v", got)
	}
	mergeFixes(dir, dir, nil, []model.Session{session("two", time.Now())}, map[string]bool{})
	if got := FixesFor(dir, errText, 3, nil); len(got) == 0 {
		t.Errorf("the second update confirmed the remedy and nothing was served: %+v", ReadFixes(dir))
	}
}

func TestAnIrreversibleCommandIsNeverARemedy(t *testing.T) {
	for cmd, want := range map[string]bool{
		"gh pr merge 41 --squash --delete-branch":                  true,
		"git push origin --delete feat/old":                        true,
		"git push -f origin main":                                  true,
		"git branch -D scratch":                                    true,
		"git reset --hard origin/main":                             true,
		"git stash drop stash@{0} 2>&1 | tail -1":                  true,
		"cd repo && git clean -fdx":                                true,
		"rm -rf build && make":                                     true,
		"kubectl -n shop-prod exec db-0 -- env":                    true,
		"kubectl delete pod web-1":                                 true,
		"GOFLAGS=-count=1 gh api -X DELETE repos/o/r/git/refs/x":   true,
		"$ git push -q -u origin fix/x  → exit 0":                  false,
		"git checkout -- internal/store/store.go":                  false,
		"brew install coreutils":                                   false,
		"kubectl -n staging port-forward svc/api 8080:80":          false,
		"rm internal/zz_probe_test.go && go test ./internal/index": false,
		"gh pr checks 41 | grep -v pass":                           false,
		"go test ./... -run TestPushDelete":                        false,
	} {
		if got := remedyIsIrreversible(cmd); got != want {
			t.Errorf("remedyIsIrreversible(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// What the session did next was merge its PR; the agent that hits the same
// error is handed the merge. The sighting behind it still answers.
func TestFixesForSkipsAnIrreversibleRemedy(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "gh pr merge 41 --squash --delete-branch", Project: "p"},
		{Sig: sig, Command: "brew install coreutils", Candidate: true, Project: "p"},
	})
	got := FixesFor(dir, "zsh:1: command not found: timeout", 4, nil)
	if len(got) != 1 || got[0].Command != "brew install coreutils" {
		t.Errorf("want only the install, got %+v", got)
	}
}

// When every remedy on file is one FixesFor withholds, the store still holds
// the pair: the empty answer must not say nothing ran after the error.
func TestAWithheldRemedyIsNotReportedAsNothingRan(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "gh pr merge 41 --squash --delete-branch", Project: "p"},
		{Sig: sig, Command: "git push -f origin main", Candidate: true, Project: "p"},
	})
	const e = "zsh:1: command not found: timeout"
	if got := FixesFor(dir, e, 4, nil); len(got) != 0 {
		t.Fatalf("want nothing served, got %+v", got)
	}
	if !FixWithheldAsIrreversible(dir, e, nil) {
		t.Error("the withheld merge is not reported")
	}
	if FixCandidateSeen(dir, e, nil) {
		t.Error("an irreversible sighting is reported as waiting for confirmation")
	}
	if FixWithheldAsIrreversible(dir, e, func(string) bool { return false }) {
		t.Error("a pair the policy hides is still reported")
	}
	if FixWithheldAsIrreversible(dir, "some other error nobody hit", nil) {
		t.Error("an unseen error reports a withheld remedy")
	}
}

// A guard rather than a fix: #2051 expected the marker to be lost with the
// rest of the record, because a pair stores only the first line. It is not —
// commandFailed reads the whole record — and this is what says so, so the
// next change to either end has to keep it true.
func TestAFailedMultiLineCommandIsNotAFix(t *testing.T) {
	now := time.Now()
	script := "python - <<'EOF'\nimport socket\nsocket.create_connection(('db', 5432))\nEOF  → exit 1"
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: script, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "done", Time: now.Add(2 * time.Minute)},
	}
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Errorf("a command that exited 1 was stored as the fix: %+v", pairs)
	}
}

// Skipping a heredoc is not abandoning the error: the window keeps being
// scanned, and the one-liner two records on is the pair worth having.
func TestTheCommandAfterASkippedHeredocIsStillThePair(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: "python - <<'EOF'\nimport socket\nEOF", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
		{Role: "command", Text: "docker compose up -d db", Time: now.Add(3 * time.Minute)},
		{Role: "tool-output", Text: "db started", Time: now.Add(4 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d: %+v", len(pairs), pairs)
	}
	if pairs[0].Command != "docker compose up -d db" {
		t.Errorf("wrong command stored: %q", pairs[0].Command)
	}
}

// The stored command is what a reader is told to run. A heredoc's first line
// is not a command — it opens one — so storing it hands the agent something
// that hangs waiting for input.
func TestAHeredocFirstLineIsNotOfferedAsTheFix(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: "python - <<'EOF'\nimport socket\nsocket.create_connection(('db', 5432))\nEOF", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
	}
	for _, p := range fixPairsIn(ms, "claude:s1", "p") {
		if strings.Contains(p.Command, "<<") {
			t.Errorf("a heredoc opener was stored as the fix: %q", p.Command)
		}
	}
}

// A herestring is a whole command, not the opening of one.
func TestAHerestringIsStillAFix(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "connection refused on port 5432", Time: now},
		{Role: "command", Text: `psql <<< "select 1"`, Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "1", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 || pairs[0].Command != `psql <<< "select 1"` {
		t.Errorf("a herestring was read as an opener: %+v", pairs)
	}
}

// A herestring followed by a heredoc: the shape the carve-out has to read
// correctly, and the one a scan that advances by a relative offset never
// finishes reading at all.
func TestAHerestringBeforeAHeredocStillOpensMoreInput(t *testing.T) {
	for cmd, want := range map[string]bool{
		"tr a b <<<x; cat <<EOF":      true,
		`cmp <<<"$a" <<<"$b"`:         false,
		"a<<<a<<":                     true,
		"psql -h db <<'SQL'":          true,
		`psql <<< "select 1"`:         false,
		"docker run --rm \\\\":        true,
		`cat <<<"$h" && psql <<'SQL'`: true,
	} {
		if got := opensMoreInput(cmd); got != want {
			t.Errorf("opensMoreInput(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// Mining runs per session on all cores now — it was 9.4 s of a 51 s build, the
// longest of the four sidecars. What lands on file must not depend on which
// worker finished first, so the same corpus is mined on one core and on eight
// and the tables are compared row by row.
func TestMinedPairsAreTheSameOnOneCoreAndMany(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ss := make([]model.Session, 0, 40)
	for i := range 40 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%02d", i), Project: "app", Updated: now,
			Messages: []model.Message{
				{Role: "command", Text: fmt.Sprintf("grep -rn \"quokkabloom%d\" --include=*.go internal", i), Time: now},
				{Role: "tool-output", Text: "zsh:1: no matches found: --include=*.go", Time: now.Add(time.Second)},
				{Role: "command", Text: fmt.Sprintf("grep -rn \"quokkabloom%d\" --include=\"*.go\" internal", i), Time: now.Add(2 * time.Second)},
				{Role: "tool-output", Text: "3 matches", Time: now.Add(3 * time.Second)},
				{Role: "command", Text: "make check", Time: now.Add(4 * time.Second)},
				{Role: "tool-output", Text: fmt.Sprintf("--- FAIL: TestSnorbleWidget%d", i), Time: now.Add(5 * time.Second)},
				{Role: "edit", Text: fmt.Sprintf("internal/widget/snorble%d.go\n-\tone\n+\ttwo", i), Time: now.Add(6 * time.Second)},
			},
		})
	}
	keyOf := func(s model.Session) string { return s.Harness + ":" + s.ID }

	build := func(workers int) []FixPair {
		rerankWorkers = func() int { return workers }
		dir := t.TempDir()
		buildFixes(dir, ss, keyOf)
		return ReadFixes(dir)
	}
	one := build(1)
	many := build(8)
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	if len(one) == 0 {
		t.Fatal("nothing was mined, so the comparison proves nothing")
	}
	if len(one) != len(many) {
		t.Fatalf("one core mined %d pairs, eight mined %d", len(one), len(many))
	}
	for i := range one {
		if !reflect.DeepEqual(one[i], many[i]) {
			t.Fatalf("row %d differs:\n one core: %+v\n eight:    %+v", i, one[i], many[i])
		}
	}
}

func claudePairLines(id, cwd, ts1, ts2, errText, cmd string) string {
	return `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts1 + `","cwd":"` + cwd + `","message":{"role":"user","content":[{"type":"tool_result","content":"` + errText + `"}]}}` + "\n" +
		`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + ts2 + `","cwd":"` + cwd + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}` + "\n"
}

func normalizeFixPairsUTC(ps []FixPair) []FixPair {
	out := slices.Clone(ps)
	for i := range out {
		out[i].When = out[i].When.UTC()
	}
	return out
}

func sortRawFixPairs(ps []FixPair) []FixPair {
	out := normalizeFixPairsUTC(ps)
	slices.SortFunc(out, func(a, b FixPair) int {
		if c := cmp.Compare(a.Key, b.Key); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Error, b.Error); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Command, b.Command); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Edit, b.Edit); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return a.When.Compare(b.When)
	})
	return out
}

func TestRemineFixesStreamsInterleavedAppendsForeignCopiesAndDeadRecords(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	projA := filepath.Join(claudeRoot, "-Users-alex-app")
	projB := filepath.Join(claudeRoot, "-Users-alex-infra")

	filler := strings.Repeat("context ", 400)
	userLine := func(id, cwd, ts, text string) string {
		return `{"type":"user","sessionId":"` + id + `","timestamp":"` + ts + `","cwd":"` + cwd + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}

	s1Path := filepath.Join(projA, "s1.jsonl")
	s2Path := filepath.Join(projA, "s2.jsonl")
	s3Path := filepath.Join(projA, "s3.jsonl")
	dupAPath := filepath.Join(projA, "dup.jsonl")
	dupBPath := filepath.Join(projB, "dup.jsonl")

	write(t, s1Path, userLine("s1", "/Users/alex/app", "2026-01-02T03:00:00Z", filler)+
		claudePairLines("s1", "/Users/alex/app", "2026-01-02T03:01:00Z", "2026-01-02T03:01:05Z", "undefined: alphaSymbol in pkg/a.go", "go build ./pkg/a"))
	write(t, s2Path, userLine("s2", "/Users/alex/app", "2026-01-02T03:00:00Z", filler)+
		claudePairLines("s2", "/Users/alex/app", "2026-01-02T03:02:00Z", "2026-01-02T03:02:05Z", "undefined: betaSymbol in pkg/b.go", "go build ./pkg/b"))
	write(t, s3Path, claudePairLines("s3", "/Users/alex/app", "2026-01-02T03:03:00Z", "2026-01-02T03:03:05Z", "undefined: staleSymbol in old/pkg.go", "make stale"))
	write(t, dupAPath, claudePairLines("dup", "/Users/alex/app", "2026-01-02T03:04:00Z", "2026-01-02T03:04:05Z", "undefined: ownConfig in cmd/app/main.go", "go build ./cmd/app"))
	write(t, dupBPath, claudePairLines("dup", "/Users/alex/infra", "2026-01-02T03:05:00Z", "2026-01-02T03:05:05Z", "undefined: foreignSecret in infra/tf.go", "terraform init"))
	indexPass(t, dir)

	// Interleave appends across s1 and s2 across two more incremental passes, and
	// rewrite s3 to introduce dead records while staying below compaction threshold.
	appendTo(t, s1Path, claudePairLines("s1", "/Users/alex/app", "2026-01-02T03:06:00Z", "2026-01-02T03:06:05Z", "undefined: gammaSymbol in pkg/c.go", "go test ./pkg/c"))
	appendTo(t, s2Path, claudePairLines("s2", "/Users/alex/app", "2026-01-02T03:07:00Z", "2026-01-02T03:07:05Z", "undefined: alphaSymbol in pkg/a.go", "go build ./pkg/a"))
	indexPass(t, dir)

	appendTo(t, s1Path, claudePairLines("s1", "/Users/alex/app", "2026-01-02T03:08:00Z", "2026-01-02T03:08:05Z", "undefined: deltaSymbol in pkg/d.go", "go test ./pkg/d"))
	writeAt(t, s3Path, claudePairLines("s3", "/Users/alex/app", "2026-01-02T03:09:00Z", "2026-01-02T03:09:05Z", "undefined: freshSymbol in new/pkg.go", "make fresh"), time.Now().Add(2*time.Minute))
	indexPass(t, dir)

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Sessions["claude:dup"].Shared {
		t.Fatalf("expected claude:dup to be marked shared, got %+v", m.Sessions["claude:dup"])
	}
	if len(m.DeadRecords) == 0 {
		t.Fatalf("expected DeadRecords after rewriting s3, got %d", len(m.DeadRecords))
	}

	rebuildDir := filepath.Join(tmp, "rebuild")
	if err = Ensure(rebuildDir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	wantFixes := normalizeFixPairsUTC(ReadFixes(rebuildDir))
	wantRaw, ok := readFixRaw(rebuildDir)
	if !ok || len(wantFixes) == 0 {
		t.Fatalf("rebuild produced empty fix tables: fixes=%d ok=%v", len(wantFixes), ok)
	}
	wantRawSorted := sortRawFixPairs(wantRaw)

	assertParity := func(label string) {
		t.Helper()
		gotFixes := ReadFixes(dir)
		if len(gotFixes) == 0 {
			t.Fatalf("%s: ReadFixes returned empty slice", label)
		}
		if !reflect.DeepEqual(normalizeFixPairsUTC(gotFixes), wantFixes) {
			t.Fatalf("%s: ReadFixes mismatch\n got: %+v\nwant: %+v", label, gotFixes, wantFixes)
		}
		for _, p := range gotFixes {
			if strings.Contains(p.Error, "staleSymbol") {
				t.Fatalf("%s: dead staleSymbol leaked into ReadFixes: %+v", label, p)
			}
		}
		gotRaw, ok := readFixRaw(dir)
		if !ok {
			t.Fatalf("%s: readFixRaw returned ok=false", label)
		}
		if gotRawSorted := sortRawFixPairs(gotRaw); !reflect.DeepEqual(gotRawSorted, wantRawSorted) {
			t.Fatalf("%s: sorted readFixRaw mismatch\n got: %+v\nwant: %+v", label, gotRawSorted, wantRawSorted)
		}
	}

	// Case A: recoffsets.bin present.
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	invalidateFixesCache()
	remineFixes(dir)
	assertParity("with recoffsets.bin")

	// Case B: recoffsets.bin absent, forcing scanRecordOffsets across the log.
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	_ = os.Remove(filepath.Join(dir, recordOffsetsFile))
	invalidateFixesCache()
	remineFixes(dir)
	assertParity("without recoffsets.bin")

	// Case C: mid-log corrupt record preserves existing fixes.gob.
	before := ReadFixes(dir)
	if len(before) == 0 {
		t.Fatal("expected non-empty fixes before corruption test")
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	off := 4
	for range 3 {
		_, k := binary.Uvarint(b[off:])
		if k <= 0 {
			t.Fatalf("bad uvarint at off=%d", off)
		}
		off += k
	}
	b[off] = 0xFF
	if err := os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}
	remineFixes(dir)
	invalidateFixesCache()
	if after := ReadFixes(dir); !reflect.DeepEqual(after, before) {
		t.Fatalf("corrupt remine modified existing fixes.gob\n got: %+v\nwant: %+v", after, before)
	}
}

func TestRemineFixesReleasesSessionMessagesBetweenSessions(t *testing.T) {
	origWorkers := rerankWorkers
	rerankWorkers = func() int { return 1 }
	defer func() { rerankWorkers = origWorkers }()
	defer func() { remineSessionHook = nil }()

	const totalSessions = 24
	base := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	ss := make([]model.Session, 0, totalSessions)
	pad := strings.Repeat("x", 32<<10)
	for i := range totalSessions {
		t0 := base.Add(time.Duration(i) * time.Minute)
		ss = append(ss, model.Session{
			ID:      fmt.Sprintf("s%02d", i),
			Harness: "claude",
			Project: "app",
			Updated: t0,
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("ctx %d a %s", i, pad), Time: t0},
				{Role: "user", Text: fmt.Sprintf("ctx %d b %s", i, pad), Time: t0.Add(time.Second)},
				{Role: "user", Text: fmt.Sprintf("ctx %d c %s", i, pad), Time: t0.Add(2 * time.Second)},
				{Role: "user", Text: fmt.Sprintf("ctx %d d %s", i, pad), Time: t0.Add(3 * time.Second)},
				{
					Role: "tool-output",
					Text: "zsh:1: command not found: bigtool" + strconv.Itoa(i),
					Time: t0.Add(4 * time.Second),
				},
				{
					Role: "command",
					Text: "brew install bigtool" + strconv.Itoa(i),
					Time: t0.Add(5 * time.Second),
				},
				{
					Role: "tool-output",
					Text: "ok",
					Time: t0.Add(6 * time.Second),
				},
			},
		})
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	invalidateFixesCache()

	var (
		ptrs               []weak.Pointer[model.Message]
		seen               int
		reclaimedDuringRun int
	)
	remineSessionHook = func(msgs []model.Message) {
		seen++
		if len(msgs) > 0 {
			ptrs = append(ptrs, weak.Make(&msgs[0]))
		}
		if seen == totalSessions {
			runtime.GC()
			runtime.GC()
			for _, p := range ptrs[:len(ptrs)-1] {
				if p.Value() == nil {
					reclaimedDuringRun++
				}
			}
		}
	}

	remineFixes(dir)

	if seen != totalSessions {
		t.Fatalf("seen=%d, want %d", seen, totalSessions)
	}
	if reclaimedDuringRun < totalSessions-4 {
		t.Fatalf("reclaimedDuringRun=%d/%d, want >= %d", reclaimedDuringRun, totalSessions-1, totalSessions-4)
	}
	if got := ReadFixes(dir); len(got) != totalSessions {
		t.Fatalf("got %d mined pairs, want %d", len(got), totalSessions)
	}
}

func TestRemineFixesDefensiveBranches(t *testing.T) {
	// Missing manifest.json returns cleanly without writing fix tables.
	missingDir := t.TempDir()
	remineFixes(missingDir)
	if len(ReadFixes(missingDir)) != 0 {
		t.Fatalf("unexpected fixes in empty dir")
	}

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	makeSession := func(id string) model.Session {
		return model.Session{
			ID:      id,
			Harness: "claude",
			Project: "app",
			Updated: now,
			Messages: []model.Message{
				{Role: "tool-output", Text: "zsh:1: command not found: edgebin" + id, Time: now},
				{Role: "command", Text: "brew install edgebin" + id, Time: now.Add(time.Second)},
				{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Second)},
			},
		}
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, []model.Session{makeSession("s1"), makeSession("s2")}, nil, ""); err != nil {
		t.Fatal(err)
	}

	// Orphan record key removed from m.Sessions is skipped cleanly.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.Sessions, "claude:s2")
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(fixesPath(dir))
	_ = os.Remove(fixRawPath(dir))
	invalidateFixesCache()
	remineFixes(dir)
	got := ReadFixes(dir)
	if len(got) != 1 || got[0].Key != "claude:s1" {
		t.Fatalf("expected only claude:s1 after deleting claude:s2 from manifest, got %+v", got)
	}

	// Oversized record header triggers scanRecordOffsets corruption error and preserves fixes.gob.
	_ = os.Remove(filepath.Join(dir, recordOffsetsFile))
	rp := filepath.Join(dir, "records.bin")
	origLog, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := slices.Clone(origLog)
	binary.LittleEndian.PutUint32(corrupt[:4], 0x7fffffff)
	if err = os.WriteFile(rp, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	remineFixes(dir)
	invalidateFixesCache()
	if after := ReadFixes(dir); !reflect.DeepEqual(after, got) {
		t.Fatalf("oversized log modified existing fixes.gob\n got: %+v\nwant: %+v", after, got)
	}

	// Missing records.bin preserves existing fixes.gob.
	if err = os.Remove(rp); err != nil {
		t.Fatal(err)
	}
	remineFixes(dir)
	invalidateFixesCache()
	if after := ReadFixes(dir); !reflect.DeepEqual(after, got) {
		t.Fatalf("missing records.bin modified existing fixes.gob\n got: %+v\nwant: %+v", after, got)
	}
}

func TestFixPairsKeepTheCommandThatSettledTheError(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "timeout 5 curl example.internal", Time: now},
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "curl --max-time 5 example.internal", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "200 OK", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d", len(pairs))
	}
	if pairs[0].Command != "curl --max-time 5 example.internal" {
		t.Errorf("wrong command stored: %q", pairs[0].Command)
	}
}

// The command that did not settle it is not a fix: the same error right after
// it means the session was still failing.
func TestFixPairsDropACommandTheErrorSurvived(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "timeout 5 curl example.internal", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now.Add(2 * time.Minute)},
	}
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Errorf("a command the error outlived was stored as a fix: %+v", pairs)
	}
}

// The same error a dozen records later says the same thing as the same error
// immediately after: the command did not settle it. Six records was the whole
// of the check, so a session that ran a command, worked on something else and
// hit the error again stored the command as the fix. Measured over this
// machine's transcripts, 104 of the 831 pairs the miner kept were contradicted
// that way.
func TestFixPairsDropACommandTheErrorOutlivedLater(t *testing.T) {
	now := time.Now()
	ms := []model.Message{ //nolint:prealloc // a fixture literal the loop below grows
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "brew install coreutils for timeout", Time: now.Add(time.Minute)},
	}
	// Far enough that the old six-record window never reached it.
	for i := range 12 {
		ms = append(ms, model.Message{Role: "tool-output", Text: "ok", Time: now.Add(time.Duration(i) * time.Minute)})
	}
	ms = append(ms, model.Message{
		Role: "tool-output", Text: "zsh:1: command not found: timeout",
		Time: now.Add(time.Hour),
	})
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Errorf("a command the error outlived was stored as a fix: %+v", pairs)
	}
}

// And the check stays a check on the same error: another error later in the
// session says nothing about this one, and withholding on it would empty the
// table for any session that hit more than one thing.
func TestFixPairsKeepACommandADifferentErrorFollowed(t *testing.T) {
	now := time.Now()
	ms := []model.Message{ //nolint:prealloc // a fixture literal the loop below grows
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "curl --max-time 5 example.internal", Time: now.Add(time.Minute)},
	}
	for i := range 12 {
		ms = append(ms, model.Message{Role: "tool-output", Text: "ok", Time: now.Add(time.Duration(i) * time.Minute)})
	}
	ms = append(ms, model.Message{
		Role: "tool-output", Text: "zsh:1: command not found: gsed",
		Time: now.Add(time.Hour),
	})
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 1 {
		t.Errorf("a different error later withheld the pair: %+v", pairs)
	}
}

// A command that failed is not a remedy. Reading 116 confirmed pairs off a real
// store, ten stored a command whose own record said it exited non-zero — and
// this line is handed to an agent at the moment it is stuck, where being wrong
// costs most.
func TestFixPairsDropACommandThatFailedItself(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "python3 -m pytest  → exit 1", Time: now.Add(time.Minute)},
	}
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Errorf("a command that exited non-zero was stored as a fix: %+v", pairs)
	}
}

// Where the harness stores no exit status — Claude does not — the output right
// after the command says the same thing.
func TestFixPairsDropACommandWhoseOutputFailed(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "brew install coreutils", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "Error: Permission denied @ dir_s_mkdir - /opt/homebrew/lib", Time: now.Add(2 * time.Minute)},
	}
	if pairs := fixPairsIn(ms, "claude:s1", "p"); len(pairs) != 0 {
		t.Errorf("a command whose output failed was stored as a fix: %+v", pairs)
	}
}

// And the retry is what the session is for: a failed attempt must not take the
// error's whole look-ahead window with it.
func TestFixPairsKeepTheRetryAfterAFailedAttempt(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "brew install coreutils", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "Error: Permission denied @ dir_s_mkdir - /opt/homebrew/lib", Time: now.Add(2 * time.Minute)},
		{Role: "command", Text: "curl --max-time 5 example.internal", Time: now.Add(3 * time.Minute)},
		{Role: "tool-output", Text: "200 OK", Time: now.Add(4 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	// Both errors in the sequence are walls — the missing command and the
	// permission denied the first attempt hit — and the retry is what followed
	// each of them. Counted rather than pinned at one: the second used to be
	// invisible, because a line opening with "Error: " never reached the
	// phrase list (#2432).
	if len(pairs) == 0 {
		t.Fatalf("the retry was not stored for either error")
	}
	byError := map[string]string{}
	for _, p := range pairs {
		byError[p.Error] = p.Command
	}
	if got := byError["command not found: timeout"]; got != "curl --max-time 5 example.internal" {
		t.Errorf("the retry after the missing command is %q", got)
	}
	for _, p := range pairs {
		if p.Command == "brew install coreutils" {
			t.Errorf("the attempt whose own output failed was stored as a fix: %+v", p)
		}
	}
}

// Sequence alone is 13% precise on a real store — the next command is usually
// the session moving on. A pair survives the build only with a second reason:
// the command names what the error named, or the same remedy recurs.
func TestBuildFixesDropsTheUnrelatedNextCommand(t *testing.T) {
	now := time.Now()
	unrelated := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "git status --short", Time: now.Add(time.Minute)},
		},
	}
	related := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "brew services start postgresql && psql -c 'select 1'", Time: now.Add(time.Minute)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{unrelated, related}, func(s model.Session) string { return s.Harness + ":" + s.ID })
	// The unrelated one is kept as a candidate — a second session doing the
	// same thing after the same error is the other half of the evidence, and
	// sessions arrive one at a time (#1301). It is never served; that is what
	// the lookup below checks.
	var got []FixPair
	for _, p := range ReadFixes(dir) {
		if !p.Candidate {
			got = append(got, p)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want only the related pair served, got %d: %+v", len(got), ReadFixes(dir))
	}
	if got[0].Key != "claude:s2" {
		t.Errorf("kept the wrong pair: %+v", got[0])
	}
	// And a lookup finds it from the error text alone, wherever in a paste the
	// line sits.
	found := FixesFor(dir, "traceback follows\npsql: connection refused on port 5432\n", 3, nil)
	if len(found) != 1 {
		t.Errorf("the pair is not findable from the pasted error: %+v", found)
	}
}

// A command run after an error can carry a live secret. It is redacted in the
// record log; the mined pairs must be scrubbed the same way, or `deja fix`
// hands the secret straight back to an agent. This drives a real index build
// through the same path almost everyone builds their first index on.
func TestFixPairsAreRedactedLikeTheRecordLog(t *testing.T) {
	dir := t.TempDir()
	secret := "AKIAIOSFODNN7EXAMPLE" //nolint:gosec // a fake secret the redaction test plants
	// Two sessions with the same error and remedy, so the pair survives on
	// corroboration alone — the point here is redaction, not the match rule.
	session := func(id string) model.Session {
		return model.Session{
			Harness: "claude", ID: id, Project: "p",
			Messages: []model.Message{
				{Role: "tool-output", Text: "Unable to locate credentials. command not found: awscli"},
				{Role: "command", Text: "AWS_ACCESS_KEY_ID=" + secret + " aws s3 ls"},
				{Role: "tool-output", Text: "ok"},
			},
		}
	}
	ss := []model.Session{session("leak1"), session("leak2")}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range ReadFixes(dir) {
		if strings.Contains(p.Command, secret) {
			t.Fatalf("mined fix pair carries a live secret: %q", p.Command)
		}
	}
	// And the pair is still there — redaction must not drop it.
	if len(ReadFixes(dir)) == 0 {
		t.Fatal("redaction dropped the pair entirely")
	}
}

// fixes.gob is written only by a full rebuild, so an index built by a version
// that predates it must be treated as stale on upgrade rather than answer
// `deja fix` with a false "no session ran a command after that error". Bumping
// the index version is what forces that one rebuild; guard both halves.
func TestUpgradedIndexRebuildsForFixes(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "s", Project: "p",
		Messages: []model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432"},
			{Role: "command", Text: "brew services start postgresql && psql -c 'select 1'"},
			{Role: "tool-output", Text: "ok"},
		},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// A full build writes the sidecar and stamps the current version.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != version {
		t.Fatalf("fresh build stamped version %d, want %d", m.Version, version)
	}
	if got := FixesFor(dir, "psql: connection refused on port 5432", 3, nil); len(got) == 0 {
		t.Fatal("fresh build did not mine the fix pair")
	}
	// A store stamped by the previous version must not be judged fresh, so
	// Ensure re-ingests it and mines the sidecar it never had. If fixes.gob is
	// ever added without a version bump, manifestFresh returns true here and an
	// upgraded user is stuck with an empty `deja fix`.
	m.Version = version - 1
	if manifestFresh(m, m.Files, "") {
		t.Fatal("a store from the previous version is judged fresh — deja fix stays empty on upgrade")
	}
}

// A peer's command must be filterable by the trust policy, so a pair carries
// its project and FixesFor gates each one through the caller's allow func.
func TestFixesForFiltersByProject(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{
		{
			Harness: "claude", ID: "local", Project: "p",
			Messages: []model.Message{
				{Role: "tool-output", Text: "psql: connection refused on port 5432"},
				{Role: "command", Text: "psql -h localhost -c 'select 1'"},
				{Role: "tool-output", Text: "ok"},
			},
		},
		{
			Harness: "claude", ID: "peer", Project: "imported:peer",
			Messages: []model.Message{
				{Role: "tool-output", Text: "psql: connection refused on port 5432"},
				{Role: "command", Text: "psql -h 192.0.2.5 -c 'select 1'"},
				{Role: "tool-output", Text: "ok"},
			},
		},
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	q := "psql: connection refused on port 5432"
	// No filter: both remedies are candidates, and each carries its project.
	all := FixesFor(dir, q, 5, nil)
	if len(all) < 2 {
		t.Fatalf("both pairs should be mined, got %d: %+v", len(all), all)
	}
	sawImported := false
	for _, p := range all {
		if p.Project == "imported:peer" {
			sawImported = true
		}
	}
	if !sawImported {
		t.Fatal("the imported pair lost its project")
	}
	// Deny imported: the peer command must not come back.
	local := FixesFor(dir, q, 5, func(project string) bool {
		return !strings.HasPrefix(project, "imported:")
	})
	for _, p := range local {
		if strings.Contains(p.Command, "192.0.2.5") {
			t.Errorf("a denied imported command surfaced: %q", p.Command)
		}
	}
	if len(local) == 0 {
		t.Fatal("the local pair was dropped too")
	}
}

// A generic remedy settles several different errors. Keying dedupe on the
// command alone deleted the pair for every signature but the newest, so the
// remedy became unfindable for the others.
func TestGenericRemedyIsKeptPerError(t *testing.T) {
	now := time.Now()
	s := model.Session{
		Harness: "claude", ID: "s", Project: "p",
		Messages: []model.Message{
			{Role: "tool-output", Text: "cannot find module 'a'", Time: now},
			{Role: "command", Text: "go mod tidy", Time: now.Add(time.Minute)},
			{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
			{Role: "tool-output", Text: "cannot find module 'b'", Time: now.Add(3 * time.Minute)},
			{Role: "command", Text: "go mod tidy", Time: now.Add(4 * time.Minute)},
			{Role: "tool-output", Text: "ok", Time: now.Add(5 * time.Minute)},
		},
	}
	dir := t.TempDir()
	// Second corroborating session so the pairs clear the precision gate.
	buildFixes(dir, []model.Session{s, s}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	sigs := map[uint64]bool{}
	for _, p := range ReadFixes(dir) {
		sigs[p.Sig] = true
	}
	if len(sigs) < 2 {
		t.Fatalf("the shared remedy was kept for only %d error(s), want 2", len(sigs))
	}
}

// A heredoc immediately after the error must not abandon the window: the real
// one-liner two records on is the remedy.
func TestFixLooksPastAnUnusableCommand(t *testing.T) {
	now := time.Now()
	long := "echo " + strings.Repeat("x", fixCommandMax+10)
	ms := []model.Message{
		{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
		{Role: "command", Text: long, Time: now.Add(time.Minute)},
		{Role: "command", Text: "brew services start postgresql", Time: now.Add(2 * time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(3 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s", "p")
	if len(pairs) != 1 || pairs[0].Command != "brew services start postgresql" {
		t.Fatalf("the real remedy after a heredoc was not found: %+v", pairs)
	}
}

// A missing binary is not fixed by a flag that happens to spell it. sharesTerm
// must reject `command not found: timeout` → `kubectl --request-timeout=20s`,
// while still accepting a command that names the thing the error named.
func TestSharesTermRejectsSubstringMatches(t *testing.T) {
	if sharesTerm("zsh: command not found: timeout", "kubectl get nodes --request-timeout=20s") {
		t.Error("a flag spelling the missing binary was accepted as its fix")
	}
	if !sharesTerm("ModuleNotFoundError: No module named 'aiokafka'", "uv pip install aiokafka") {
		t.Error("the command that installs the missing module was rejected")
	}
	// Error prose alone must not carry a match.
	if sharesTerm("command not found: xyz", "git commit -m done") {
		t.Error("the word 'command' from error prose matched an unrelated command")
	}
	// A flag that spells the missing binary is not invoking it.
	if sharesTerm("command not found: timeout", "go test ./... -timeout 60s") {
		t.Error("a -timeout flag matched a missing timeout binary")
	}
	// Invoking the binary itself does match.
	if !sharesTerm("command not found: timeout", "timeout 5 curl x") {
		t.Error("running the missing binary was not recognised as its fix")
	}
	// An install command may name the missing module inside a longer package.
	if !sharesTerm("ModuleNotFoundError: No module named 'yaml'", "pip install pyyaml") {
		t.Error("the install of the package that provides the module was rejected")
	}
	if !sharesTerm("No module named 'aiokafka'", "pip install aiokafka-python") {
		t.Error("the hyphenated install package was rejected")
	}
	// But containment without an install verb stays rejected (the flag class).
	if sharesTerm("command not found: timeout", "go run ./cmd --request-timeout=5s") {
		t.Error("a flag containing the binary name matched outside an install")
	}
}

// A failing test is repaired by an edit, so the command that followed one is
// the session moving on — and the work moves on the same way every day, which
// used to be enough to call it a fix. `deja fix` answered eleven red tests on
// this machine with `gh pr merge`, `git checkout -b` and `git status`.
func TestACommandAfterARedTestIsNotConfirmedByRepetition(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "--- FAIL: TestBillingRoundsHalfUp (0.09s)", Time: now},
			{Role: "command", Text: "gh pr merge 2532 --squash --delete-branch", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	first := judgeFixes(session("s1"))
	if len(first) != 1 || !first[0].Candidate {
		t.Fatalf("one sighting is a candidate: %+v", first)
	}
	second := judgeFixes(append(first, session("s2")...))
	if len(second) != 1 {
		t.Fatalf("the sighting is kept, not dropped: %+v", second)
	}
	if !second[0].Candidate {
		t.Errorf("a second session running the same command after the same red test confirmed nothing: %+v", second[0])
	}
}

// The rule is about that shape only. `brew services start postgresql` names
// nothing `psql: connection refused` names either, and a second session running
// it is still the answer.
func TestARepeatedCommandAfterAnOrdinaryFailureStillConfirms(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "psql: connection refused on port 5432", Time: now},
			{Role: "command", Text: "brew services start postgresql", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	second := judgeFixes(append(judgeFixes(session("s1")), session("s2")...))
	if len(second) != 1 || second[0].Candidate {
		t.Fatalf("a repeated remedy for an ordinary failure must be a pair: %+v", second)
	}
}

// And an edit after a red test is exactly what a repair looks like, so the
// second sighting confirms it.
func TestAnEditAfterARedTestIsStillConfirmedByRepetition(t *testing.T) {
	now := time.Now()
	session := func(id string) []FixPair {
		return fixPairsIn([]model.Message{
			{Role: "tool-output", Text: "--- FAIL: TestBillingRoundsHalfUp (0.09s)", Time: now},
			{Role: "edit", Text: "internal/billing/round.go\n-\tmath.Round(v)\n+\tmath.Floor(v + 0.5)", Time: now.Add(time.Minute)},
		}, "claude:"+id, "p")
	}
	second := judgeFixes(append(judgeFixes(session("s1")), session("s2")...))
	if len(second) != 1 || second[0].Candidate {
		t.Fatalf("a repeated edit after a red test must be a pair: %+v", second)
	}
}

// Five changes widened what counts as a wall, so more errors now look for a
// command to pair with — and the next command after an error is usually the
// session moving on, not a remedy. Measured over the store on this machine
// after the widening: 24 walls, 12 with a pair, every one of them confirmed and
// none of them navigation. This holds the part that keeps it that way (#2448).
func TestACommandThatCannotBeARemedyIsNotStoredAsOne(t *testing.T) {
	now := time.Now()
	wall := "psql: error: connection to server at db-a, port 5432 failed: Connection refused"
	for _, cmd := range []string{
		"cd ../service",
		"ls -la internal/handler",
		"cat internal/handler/orders.go",
		"pwd",
		"git status",
	} {
		ms := []model.Message{
			{Role: roleCommand, Text: "psql -h db-a -c 'select 1'", Time: now},
			{Role: roleToolOutput, Text: wall, Time: now.Add(time.Minute)},
			{Role: roleCommand, Text: cmd, Time: now.Add(2 * time.Minute)},
			{Role: roleToolOutput, Text: "ok", Time: now.Add(3 * time.Minute)},
		}
		for _, p := range fixPairsIn(ms, "claude:s1", "work/app") {
			if strings.Contains(p.Command, cmd) {
				t.Errorf("`%s` was stored as what fixes a refused connection", cmd)
			}
		}
	}

	// And the control: a command that changes something is still stored.
	ms := []model.Message{
		{Role: roleCommand, Text: "psql -h db-a -c 'select 1'", Time: now},
		{Role: roleToolOutput, Text: wall, Time: now.Add(time.Minute)},
		{Role: roleCommand, Text: "brew services start postgresql@16", Time: now.Add(2 * time.Minute)},
		{Role: roleToolOutput, Text: "==> Successfully started postgresql@16", Time: now.Add(3 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "work/app")
	if len(pairs) == 0 {
		t.Fatalf("the command that fixed it was not stored at all")
	}
	if !strings.Contains(pairs[0].Command, "brew services start postgresql@16") {
		t.Errorf("stored %q", pairs[0].Command)
	}
}
