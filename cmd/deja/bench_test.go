package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/usage"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The corpus has to be able to say no. A bench whose baseline scores full
// marks measures nothing, which is how bench context's coverage column ended
// up unable to move (#2931) — so the shape of this corpus is asserted, not
// assumed.
func TestTheBlockCorpusSettlesOnceAndNotLast(t *testing.T) {
	corpus := bench.GenerateBlock(1)
	if len(corpus.Chains) != bench.BlockChainCount {
		t.Fatalf("chains = %d, want %d", len(corpus.Chains), bench.BlockChainCount)
	}
	chain := corpus.Chains[0]
	if len(chain.Sessions) != bench.BlockPriorCount {
		t.Fatalf("sessions = %d, want %d", len(chain.Sessions), bench.BlockPriorCount)
	}
	settled := 0
	for i, s := range chain.Sessions {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, chain.SettledMarker()) {
				settled++
				if i != bench.BlockSettledAt {
					t.Errorf("session %d carries the answer; only %d should", i, bench.BlockSettledAt)
				}
			}
		}
	}
	if settled != 1 {
		t.Errorf("the answer appears %d times in the chain, want once", settled)
	}
	// Not the last thing said, or "take the newest turn" scores full marks
	// without choosing anything.
	last := chain.Sessions[bench.BlockSettledAt].Messages
	if strings.Contains(last[len(last)-1].Text, chain.SettledMarker()) {
		t.Error("the answer is the last message of its session; recency alone would win")
	}
}

// The baseline is what says the metric is real: the newest turns of the right
// session do not carry the answer, so an arm that scores above zero had to
// find it.
func TestTheNewestTurnBaselineMissesTheAnswer(t *testing.T) {
	corpus := bench.GenerateBlock(1)
	chain := corpus.Chains[0]
	got := scoreBlock(blockArmNewest([]model.Session{chain.Sessions[bench.BlockSettledAt]}), chain)
	if got.carries {
		t.Error("the newest two turns carry the answer, so the baseline cannot fail")
	}
}

// And scoring itself: the settled sentence counts, the question's words do not.
func TestScoringCountsTheAnswerAndNotTheSubject(t *testing.T) {
	chain := bench.GenerateBlock(1).Chains[0]
	if got := scoreBlock(chain.Settled, chain); !got.carries || !got.settled {
		t.Errorf("the settled sentence scored %+v", got)
	}
	subjectOnly := strings.Repeat(chain.Terms[0]+" ", 40)
	if got := scoreBlock(subjectOnly, chain); got.carries {
		t.Error("saying the subject over and over counted as carrying the answer")
	}
	if got := scoreBlock("", chain); got.carries || got.settled || !got.armEmpty {
		t.Errorf("an empty arm scored %+v", got)
	}
}

func TestBlockReportPrintsEveryArm(t *testing.T) {
	var b bytes.Buffer
	printBlockReport(&b, blockReport{
		Chains: 30, Priors: 8,
		Arms: map[string]blockArmReport{
			"deja-block":  {Carries: 1, ReadsSettled: 1, MedianTokens: 665},
			"deja-digest": {Carries: 1, ReadsSettled: 1, MedianTokens: 1656},
			"newest-turn": {},
			"cold":        {},
		},
	})
	for _, want := range []string{"deja-block", "deja-digest", "newest-turn", "cold", "carries the answer"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the report does not mention %q:\n%s", want, b.String())
		}
	}
}

// End to end, the same shape the context bench is guarded with: the report
// parses, every arm is present, the baseline is beaten, and nothing is written
// outside the scratch directory.
func TestBlockJSONAndIsolation(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "block", "--json", "--seed", "7")
	if err != nil {
		t.Fatal(err)
	}
	var report blockReport
	if err = json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid block JSON %q: %v", out, err)
	}
	if report.Chains != bench.BlockChainCount || report.Priors != bench.BlockPriorCount {
		t.Fatalf("unexpected block report: %#v", report)
	}
	for _, arm := range []string{"deja-block", "deja-digest", "newest-turn", "cold"} {
		if _, ok := report.Arms[arm]; !ok {
			t.Fatalf("missing arm %q", arm)
		}
	}
	// The whole point: deja's surfaces carry the answer and the baseline does
	// not. A run where the baseline scores is a corpus that stopped asking.
	if report.Arms["deja-block"].Carries <= report.Arms["newest-turn"].Carries {
		t.Errorf("the block did not beat the baseline: %+v", report.Arms)
	}
	if report.Arms["cold"].Carries != 0 {
		t.Errorf("the empty arm carried something: %+v", report.Arms["cold"])
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside scratch: %v", entries)
	}
}

func TestBlockArgs(t *testing.T) {
	if _, err := captureRun(t, "bench", "block", "--bad"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal("invalid block flag did not fail")
	}
	if _, err := captureRun(t, "bench", "block", "--seed"); err == nil {
		t.Fatal("missing seed did not fail")
	}
}

// Every corpus in this benchmark files history the way no real store does:
// many short sessions, one durable fact each. On this machine's own store a
// recall page's deepest hit matched between 24 and 23,278 times, and a hit may
// quote three of its matches — so the whole class of "the answer is inside one
// long session" was outside what `deja bench` could see. The marathon corpus
// is the same chains with the same facts, folded into one session each, and it
// shows what that costs the session-start block.
func TestTheContextBenchMeasuresOneLongSessionToo(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "context", "--json", "--seed", "7")
	if err != nil {
		t.Fatal(err)
	}
	var report contextReport
	if err = json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid context JSON %q: %v", out, err)
	}
	if report.MarathonChains != bench.ContextMarathonCount {
		t.Errorf("marathon chains: %d, want %d", report.MarathonChains, bench.ContextMarathonCount)
	}
	if report.MarathonHash == "" || report.MarathonHash == report.CorpusHash {
		t.Errorf("the folded corpus is not a corpus of its own: %q against %q", report.MarathonHash, report.CorpusHash)
	}
	for _, arm := range []string{"deja-recall", "deja-digest", "deja-block", "full-history", "naive-grep", "cold"} {
		if _, ok := report.MarathonArms[arm]; !ok {
			t.Fatalf("missing marathon arm %q", arm)
		}
	}
	// The property the corpus exists to show: the same facts in one session
	// reach the agent less often than the same facts in fourteen. If this
	// stops holding, either the block got better at long sessions — worth
	// knowing — or the fold stopped folding.
	normal := report.Arms["deja-block"].MedianCoverage
	folded := report.MarathonArms["deja-block"].MedianCoverage
	if folded >= normal {
		t.Errorf("the block reaches %.2f of the facts in one long session against %.2f in fourteen short ones — the corpora no longer differ", folded, normal)
	}
	if folded <= 0 {
		t.Errorf("the block reaches nothing at all on the folded corpus, so the column cannot move: %.2f", folded)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside scratch: %v", entries)
	}
}

// The fold keeps the content and changes only the filing.
func TestFoldingAChainKeepsEveryMessage(t *testing.T) {
	normal := bench.GenerateContext(7)
	folded := bench.GenerateContextMarathon(7)
	if len(folded.Chains) != bench.ContextMarathonCount {
		t.Fatalf("folded chains: %d", len(folded.Chains))
	}
	for i, fc := range folded.Chains {
		nc := normal.Chains[i]
		if fc.ID != nc.ID {
			t.Fatalf("chain %d is not the same chain: %q against %q", i, fc.ID, nc.ID)
		}
		if len(fc.Sessions) != 2 {
			t.Fatalf("%s folded into %d sessions, want the marathon and the task", fc.ID, len(fc.Sessions))
		}
		want, got := 0, len(fc.Sessions[0].Messages)
		for _, s := range nc.Sessions[:len(nc.Sessions)-1] {
			want += len(s.Messages)
		}
		if got != want {
			t.Errorf("%s: folded session holds %d messages, the chain had %d", fc.ID, got, want)
		}
		if fc.Sessions[1].ID != nc.ID+"-task" {
			t.Errorf("%s: the task session was folded in too: %q", fc.ID, fc.Sessions[1].ID)
		}
	}
}

// The union arm cannot fall until both surfaces under it do — each carries the
// chain's facts alone, which is how four PR bodies came to cite "coverage 1.00
// unchanged" as evidence about a digest that was emitting nothing (#2931). The
// report says which rows to read instead.
func TestTheContextReportSaysTheUnionRowIsAFloor(t *testing.T) {
	report := contextReport{
		Chains: 3, Negatives: 1,
		Arms: map[string]contextArmReport{
			"deja-recall": {MedianTokens: 563, MedianCoverage: 1},
			"deja-digest": {MedianTokens: 294, MedianCoverage: 1},
			"deja-block":  {MedianTokens: 269, MedianCoverage: 0.67},
		},
	}
	var out bytes.Buffer
	printContextReport(&out, report)
	got := out.String()
	for _, arm := range []string{"deja-recall", "deja-digest", "deja-block"} {
		if !strings.Contains(got, arm) {
			t.Fatalf("the %s row is missing:\n%s", arm, got)
		}
	}
	if !strings.Contains(got, "union of the two rows under it") {
		t.Fatalf("the report does not say the union row is a floor:\n%s", got)
	}
}

func TestContextCoverageAndColdArm(t *testing.T) {
	chain := bench.GenerateContext(bench.Seed).Chains[0]
	text := strings.Join(chain.Facts, "\n")
	if contextCoverage(text, chain.Facts) != 1 {
		t.Fatal("ground-truth facts were not fully covered")
	}
	if contextCoverage("", chain.Facts) != 0 {
		t.Fatal("empty context was not zero coverage")
	}
	report := summarizeContext([]contextMeasurement{{tokens: 10, coverage: 1}, {tokens: 20, coverage: 0}, {negative: true, tokens: 5}})
	if report.MedianTokens != 10 || report.NegativeMedian != 5 {
		t.Fatalf("summary = %#v", report)
	}
}

func TestContextJSONAndIsolation(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "context", "--json", "--seed", "7")
	if err != nil {
		t.Fatal(err)
	}
	var report contextReport
	if err = json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid context JSON %q: %v", out, err)
	}
	if report.Chains != bench.ContextChainCount || report.Negatives != bench.ContextNegativeCount || len(report.CorpusHash) != 64 {
		t.Fatalf("unexpected context report: %#v", report)
	}
	for _, arm := range []string{"deja-recall", "deja-digest", "deja-block", "full-history", "naive-grep", "cold"} {
		if _, ok := report.Arms[arm]; !ok {
			t.Fatalf("missing arm %q", arm)
		}
	}
	// The two halves of what deja injects are scored apart, and each has to
	// carry its own text: scored only as a union the coverage column could not
	// move — deleting either surface left it at 1.00 because the other one
	// still held the chain's facts (#2931).
	// The coverage column has to be able to move. Every prior session names
	// the chain and only the first three settle anything, so the block's
	// budget has to choose — at three prior sessions per chain every arm
	// scored 1.00 whatever the digest did (#2931, #2933).
	if c := report.Arms["deja-block"].MedianCoverage; c <= 0 || c >= 1 {
		t.Errorf("the block's coverage is %.2f — saturated again, so a regression in it cannot show", c)
	}
	digest, block := report.Arms["deja-digest"], report.Arms["deja-block"]
	if digest.MedianTokens == 0 || block.MedianTokens == 0 {
		t.Errorf("a half of the injection is scored empty: digest %.0f tokens, block %.0f", digest.MedianTokens, block.MedianTokens)
	}
	if union := report.Arms["deja-recall"]; union.MedianTokens < digest.MedianTokens || union.MedianTokens < block.MedianTokens {
		t.Errorf("the union is smaller than one of its halves: %.0f against %.0f and %.0f",
			union.MedianTokens, digest.MedianTokens, block.MedianTokens)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside scratch: %v", entries)
	}
}

func TestContextArgs(t *testing.T) {
	if _, err := captureRun(t, "bench", "context", "--bad"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal("invalid context flag did not fail")
	}
	if _, err := captureRun(t, "bench", "context", "--seed"); err == nil {
		t.Fatal("missing seed did not fail")
	}
}

func TestWriteBenchCorpusRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	sessions := bench.Generate(bench.Seed).Sessions
	if err := writeBenchCorpus(tmp, sessions[:5]); err != nil {
		t.Fatal(err)
	}
	found := 0
	if err := filepath.Walk(tmp, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			found++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found != 5 {
		t.Fatalf("wrote %d files, want 5", found)
	}
	// Error path: root squatted by a file.
	squat := filepath.Join(tmp, "sq")
	if err := os.WriteFile(squat, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBenchCorpus(filepath.Join(squat, "sub"), []model.Session{{ID: "a", Project: "p", Messages: []model.Message{{Role: "user", Text: "t"}}}}); err == nil {
		t.Fatal("expected mkdir error")
	}
}

// The bench writes its corpus under the working directory, and the ignore rule
// covers paths rather than configuration — so a run from a covered tree indexes
// its corpus and then finds none of it. Every arm reads zero, which is what a
// dead surface looks like: `deja bench prompt` printed 0 of 13 real questions
// from a directory under `~/.claude/jobs/`, and 13 of 13 from one beside it.
func TestBenchRefusesToMeasureACorpusItCannotSee(t *testing.T) {
	work := filepath.Join(t.TempDir(), "home", ".claude", "jobs", "job-1", "tmp")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	// t.Chdir rather than a cleanup of our own: cleanups run last-registered
	// first, so a manual restore would race t.TempDir's removal.
	t.Chdir(work)

	got, err := benchmarkTempDir()
	if err == nil {
		t.Fatalf("the bench prepared %q inside a tree the ignore rule hides", got)
	}
	for _, want := range []string{"ignore rule", "read zero"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if entries, rerr := os.ReadDir(work); rerr == nil && len(entries) > 0 {
		t.Errorf("a refused run still left %d entries behind", len(entries))
	}
}

// Anywhere else it prepares the run tree as before.
func TestBenchPreparesItsRunTreeElsewhere(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	got, err := benchmarkTempDir()
	if err != nil {
		t.Fatalf("bench refused an ordinary directory: %v", err)
	}
	if !strings.HasPrefix(got, work) {
		t.Errorf("run tree %q is not under the working directory %q", got, work)
	}
}

// The gate this benchmark exists to be: an update must not rewrite the records
// already on file. That is what the new-transcript path got wrong for two
// months (#3500), and what a rewritten transcript cost every store until
// #5387; wall time on a shared runner is too noisy to catch it — the bytes are
// not.
func TestBenchIngestNoUpdateRewritesTheStore(t *testing.T) {
	hermeticEnv(t)
	report, err := measureIngest(bench.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Classes) != 5 {
		t.Fatalf("measured %d update classes, want the five a store sees: %#v", len(report.Classes), report.Classes)
	}
	for _, c := range report.Classes {
		if c.Rewritten {
			t.Errorf("%q rewrote the store", c.Name)
		}
		if c.RecordsMB <= 0 {
			t.Errorf("%q left no records behind, so the pass measured nothing", c.Name)
		}
	}
	if added := report.Classes[2].AddedKB; added <= 0 {
		t.Errorf("a new transcript added %.2f KB of records, so it was not indexed", added)
	}
	// The records a rewrite replaced are cleared where they stand, and only
	// those: no other class clears anything (#5429).
	for i, c := range report.Classes {
		if cleared := c.ClearedKB > 0; cleared != (c.Name == "rewritten transcript") {
			t.Errorf("class %d %q cleared %.2f KB of records", i, c.Name, c.ClearedKB)
		}
	}
	// A rename adds nothing: the bytes are already on file under the old name,
	// and re-reading them was what #3546 fixed.
	renamed := report.Classes[3]
	if renamed.Name != "renamed transcript" {
		t.Fatalf("class 3 is %q, want the rename", renamed.Name)
	}
	if renamed.AddedKB != 0 {
		t.Errorf("a rename added %.2f KB of records, so the file was read again", renamed.AddedKB)
	}
}

// `deja bench` builds its corpus under .deja-bench/run-* in the working
// directory — the user's repo — removes the run tree on success and never
// touches the parent. So a ^C leaves megabytes there that nothing ever looks at
// again, and a clean run still leaves an empty directory behind (#2560).
func TestBenchSweepsWhatEarlierRunsLeft(t *testing.T) {
	// t.Chdir rather than a cleanup of our own: cleanups run last-registered
	// first, so a chdir registered before the temp directory is restored
	// after it is removed — and on Windows a process's working directory
	// cannot be removed while it stands there (#2808).
	work := t.TempDir()
	t.Chdir(work)
	parent := filepath.Join(work, ".deja-bench")
	stale := filepath.Join(parent, "run-interrupted")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "corpus.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	// A run that started a minute ago is somebody else's, still going.
	fresh := filepath.Join(parent, "run-live")
	if err := os.MkdirAll(fresh, 0o700); err != nil {
		t.Fatal(err)
	}

	dir, err := benchmarkTempDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the interrupted run's tree is still there: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a live run's tree was swept: %v", err)
	}

	// And the parent goes when this run's own tree is the last thing in it.
	if err := os.RemoveAll(fresh); err != nil {
		t.Fatal(err)
	}
	releaseBenchTempDir(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the run tree survived: %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf(".deja-bench was left in the working directory: %v", err)
	}
}

// A parent still holding another run's tree stays where it is.
func TestBenchKeepsTheParentWhileAnotherRunIsInIt(t *testing.T) {
	// t.Chdir rather than a cleanup of our own: cleanups run last-registered
	// first, so a chdir registered before the temp directory is restored
	// after it is removed — and on Windows a process's working directory
	// cannot be removed while it stands there (#2808).
	work := t.TempDir()
	t.Chdir(work)
	dir, err := benchmarkTempDir()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(dir)
	other := filepath.Join(parent, "run-other")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	releaseBenchTempDir(dir)
	if _, err := os.Stat(parent); err != nil {
		t.Errorf("the parent was removed while another run was in it: %v", err)
	}
}

func TestBenchLiveEvaluatesRepoReadOnlyWithoutMutatingHookSeenOrInjections(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/payments\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relayFile := filepath.Join(proj, "relay.go")
	if err := os.WriteFile(relayFile, []byte("package payments\n\nfunc DispatchSettlementBatch() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	retryFile := filepath.Join(proj, "retry.go")
	if err := os.WriteFile(retryFile, []byte("package payments\n\nfunc BackoffSchedule() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"init files"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e2","name":"Edit","input":{"file_path":%q,"old_string":"1","new_string":"1"}}]}}`, proj, retryFile),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s2.jsonl"), "s2", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:00:00Z","message":{"role":"user","content":"harden relay and retry"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e3","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:02:00Z","message":{"role":"assistant","content":"We decided to use advisory locks in relay.go so concurrent workers never double-settle a batch."}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./..."}}]}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:03:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"--- FAIL: TestBackoffScheduleJitterBounds (0.01s)\nFAIL\texample.com/payments\t0.02s"}]}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s2","cwd":%q,"timestamp":"2026-03-02T10:04:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e4","name":"Edit","input":{"file_path":%q,"old_string":"1","new_string":"1"}}]}}`, proj, retryFile),
	})
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s3.jsonl"), "s3", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:00:00Z","message":{"role":"user","content":"prevent advisory locks double-settle batch contention"}}`, proj),
		fmt.Sprintf(`{"type":"assistant","sessionId":"s3","cwd":%q,"timestamp":"2026-03-03T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e5","name":"Edit","input":{"file_path":%q,"old_string":"nil","new_string":"nil"}}]}}`, proj, relayFile),
	})

	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	t.Chdir(proj)

	out, err := captureRun(t, "bench", "live", "--json")
	if err != nil {
		t.Fatalf("bench live --json: %v", err)
	}
	var rep liveBenchReport
	if unmarshalErr := json.Unmarshal([]byte(out), &rep); unmarshalErr != nil {
		t.Fatalf("unmarshal liveBenchReport: %v\n%s", unmarshalErr, out)
	}
	if rep.SourceFiles != 2 || rep.WorkedFiles != 2 {
		t.Fatalf("expected 2 source files and 2 worked files, got %+v", rep)
	}
	if rep.PriorDecision != 1 || rep.PastBreakage != 1 || rep.ActionableFiles != 2 {
		t.Fatalf("expected 1 prior decision + 1 past breakage (2 actionable), got %+v", rep)
	}
	if rep.EphemeralNoise != 0 {
		t.Fatalf("expected 0 ephemeral noise, got %+v", rep)
	}

	// Read-only invariant: bench live must not create .hookseen or .injections.jsonl.
	for _, sideEffect := range []string{".hookseen", ".injections.jsonl", ".usage.jsonl"} {
		if _, statErr := os.Stat(filepath.Join(idxDir, sideEffect)); !os.IsNotExist(statErr) {
			t.Errorf("bench live must be read-only, found %s in index dir", sideEffect)
		}
	}

	textOut, err := captureRun(t, "bench", "live")
	if err != nil {
		t.Fatalf("bench live text: %v", err)
	}
	if !strings.Contains(textOut, "actionable recall: 2 files") {
		t.Errorf("unexpected text output:\n%s", textOut)
	}
	if _, err := captureRun(t, "bench", "live", "--seed", "1"); err == nil {
		t.Errorf("expected bench live --seed 1 to be rejected")
	}
}

func TestBenchLiveClassificationAndAuditHelpers(t *testing.T) {
	t.Parallel()

	var rep liveBenchReport
	classifyLiveHookLine(&rep, "")
	classifyLiveHookLine(&rep, "relay.go"+standingLabel+"never push directly")
	classifyLiveHookLine(&rep, "relay.go"+repoDecisionLabel+"keep monotonic cursor")
	classifyLiveHookLine(&rep, "relay.go"+revertedLabel+"abandoned mutex retry because it deadlocked under storm — usually edited alongside store.go")
	classifyLiveHookLine(&rep, "relay.go"+breakageLabel+"TestRelay: nil cursor — verified here: go test ./...")
	classifyLiveHookLine(&rep, "relay.go"+decisionLabel+"I fixed all 14 tests and CI checks pass.")
	classifyLiveHookLine(&rep, "relay.go — test target: go test ./pkg/relay")
	classifyLiveHookLine(&rep, "relay.go — usually edited alongside store.go")
	if rep.Silent != 1 || rep.Standing != 1 || rep.RepoMemory != 1 || rep.RevertedApproach != 1 || rep.PastBreakage != 1 || rep.VerifiedCommand != 1 || rep.EphemeralNoise != 1 || rep.TestTargetOnly != 1 {
		t.Fatalf("unexpected classification counts: %+v", rep)
	}
	if !liveOutputNamesAnyTarget("likely target: pkg/relay_worker.go", []string{"pkg/relay_worker.go"}) || !liveOutputNamesAnyTarget("see relay_worker.go", []string{"pkg/relay_worker.go"}) || liveOutputNamesAnyTarget("", []string{"pkg/relay_worker.go"}) {
		t.Errorf("unexpected liveOutputNamesAnyTarget behavior")
	}
	if emptyRep, err := measureLiveRepo(t.TempDir(), t.TempDir()); err != nil || emptyRep.SourceFiles != 0 {
		t.Errorf("expected empty repo report, got %+v (%v)", emptyRep, err)
	}

	if !liveSkippedDir("node_modules") || liveSkippedDir("internal") {
		t.Errorf("unexpected liveSkippedDir behavior")
	}
	if isLiveBenchSourceFile("api/service.pb.go", "service.pb.go") || !isLiveBenchSourceFile("api/service.rs", "service.rs") || isLiveBenchSourceFile("README.md", "README.md") {
		t.Errorf("unexpected isLiveBenchSourceFile behavior")
	}
	if !liveOutputHasGuardrail("x"+revertedLabel+"y") || liveOutputHasGuardrail("plain line") {
		t.Errorf("unexpected liveOutputHasGuardrail behavior")
	}
	if !liveOutputHasTestRecipe("run pytest tests/unit") || liveOutputHasTestRecipe("no command") {
		t.Errorf("unexpected liveOutputHasTestRecipe behavior")
	}
	if !symbolBriefAlreadyCoversTarget("Prior work in store_codec.go (Decode)", "likely target: store_codec.go — test target: go test ./...") {
		t.Errorf("expected symbolBriefAlreadyCoversTarget = true")
	}
	if symbolBriefAlreadyCoversTarget("Prior work in other.go", "no target prefix") {
		t.Errorf("expected symbolBriefAlreadyCoversTarget = false")
	}
	if got := percentileMS(nil, 0.5); got != 0 {
		t.Errorf("expected percentileMS(nil) = 0, got %v", got)
	}

	tmp := t.TempDir()
	idxDir := filepath.Join(tmp, "index.db")
	snapLines := strings.Join([]string{
		`{"t":"2026-04-01T10:00:00Z","kind":"tool","bytes":120,"digest":"relay.go — prior decision: I fixed all 14 tests and CI checks pass."}`,
		`{"t":"2026-04-01T10:01:00Z","kind":"dejavu","bytes":240,"digest":"deja vu"}`,
		`{"t":"2026-04-01T10:02:00Z","kind":"context","bytes":360,"digest":"digest"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(idxDir+".injections.jsonl", []byte(snapLines), 0o644); err != nil {
		t.Fatal(err)
	}
	audit := auditLiveInjections(idxDir)
	if audit.Total != 3 || audit.Tool != 1 || audit.DejaVu != 1 || audit.Context != 1 || audit.ToolNoise != 1 || audit.MedianBytes != 240 {
		t.Fatalf("unexpected injection audit: %+v", audit)
	}
}

func TestBenchLiveHelpersAndFormatting(t *testing.T) {
	t.Parallel()

	short := "fix backoff jitter"
	if got := clipLiveOpeningPrompt(short); got != short {
		t.Errorf("clipLiveOpeningPrompt(short) = %q, want %q", got, short)
	}
	long := strings.Repeat("a", 1700) + strings.Repeat("b", 900)
	clipped := clipLiveOpeningPrompt(long)
	if len([]rune(clipped)) != 2401 || !strings.HasPrefix(clipped, "aaaa") || !strings.HasSuffix(clipped, "bbbb") {
		t.Errorf("unexpected clipped length=%d", len([]rune(clipped)))
	}

	root := t.TempDir()
	idxDir := filepath.Join(t.TempDir(), "index.db")
	fallback := liveOpeningPrompt(idxDir, index.SessionMeta{Harness: "claude", ID: "missing", Title: "fallback title"})
	if fallback != "fallback title" {
		t.Errorf("liveOpeningPrompt fallback = %q", fallback)
	}
	if emptyLoc := measureLiveLocalization(idxDir, root, []string{"work/app"}, policy.Policy{}); emptyLoc.Tasks != 0 {
		t.Errorf("expected empty localization audit, got %+v", emptyLoc)
	}
	if emptyInj := auditLiveInjections(idxDir); emptyInj.Total != 0 {
		t.Errorf("expected empty injection audit, got %+v", emptyInj)
	}

	srcDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainSrc := filepath.Join(srcDir, "worker.go")
	if err := os.WriteFile(mainSrc, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "worker_test.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "worker.pb.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := liveExistingEditedTargets(root, []string{
		mainSrc,
		filepath.Join(srcDir, "worker.go"),
		filepath.Join(srcDir, "worker_test.go"),
		filepath.Join(srcDir, "worker.pb.go"),
		filepath.Join(srcDir, "missing.go"),
		"/outside/other.go",
	})
	if len(targets) != 1 || targets[0] != "pkg/worker.go" {
		t.Errorf("liveExistingEditedTargets = %v, want [pkg/worker.go]", targets)
	}
	if !livePromptNamesAnyTarget("please inspect Worker.go logic", targets) || livePromptNamesAnyTarget("unrelated prompt", targets) {
		t.Errorf("unexpected livePromptNamesAnyTarget behavior")
	}

	if got := roundMS(1234 * time.Microsecond); got != 1.23 {
		t.Errorf("roundMS = %v, want 1.23", got)
	}
	if got := percentileMS([]float64{1.2, 4.5, 9.8}, 0.95); got != 9.8 {
		t.Errorf("percentileMS = %v, want 9.8", got)
	}

	var buf bytes.Buffer
	printLiveBenchReport(&buf, liveBenchReport{
		Repo:        "work/app",
		SourceFiles: 10,
		WorkedFiles: 4,
		Localization: liveLocalizationAudit{
			Tasks: 2, Localized: 1, LocalizedRate: 0.5,
			ImplicitTasks: 1, ImplicitLocalized: 1, ImplicitRate: 1.0,
			WithGuardrail: 1, WithTestCmd: 1, MedianBytes: 180, P50MS: 2.5,
		},
		Injections: liveInjectionAudit{
			Total: 5, Tool: 2, DejaVu: 2, Context: 1, MedianBytes: 210, ToolNoise: 1,
		},
	})
	out := buf.String()
	if !strings.Contains(out, "turn-0 localization:") || !strings.Contains(out, "injections log:") {
		t.Errorf("printLiveBenchReport missing optional sections:\n%s", out)
	}
}

func TestBenchLiveOpeningPromptFromIndex(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(tmp, "work", "payments")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeFixture(t, filepath.Join(claudeRoot, "payments", "s1.jsonl"), "s1", []string{
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:00Z","message":{"role":"user","content":"hi"}}`, proj),
		fmt.Sprintf(`{"type":"user","sessionId":"s1","cwd":%q,"timestamp":"2026-03-01T10:00:05Z","message":{"role":"user","content":"investigate connection pool starvation under burst traffic"}}`, proj),
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	got := liveOpeningPrompt(idxDir, index.SessionMeta{Harness: "claude", ID: "s1", Title: "ignored"})
	if !strings.Contains(got, "connection pool starvation") {
		t.Errorf("liveOpeningPrompt from index = %q", got)
	}
}

func TestBenchLiveDeliveryReportIsReadOnly(t *testing.T) {
	opts, err := parseBenchLiveArgs([]string{"--delivery", "--json"})
	if err != nil || !opts.delivery || !opts.jsonOutput {
		t.Fatalf("parseBenchLiveArgs(--delivery --json) = (%+v, %v)", opts, err)
	}
	if _, argErr := parseBenchLiveArgs([]string{"--bogus"}); argErr == nil {
		t.Fatal("parseBenchLiveArgs(--bogus) succeeded; want error")
	}

	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idxDir)
	work := filepath.Join(tmp, "work", "payments")
	if mkdirErr := os.MkdirAll(filepath.Join(work, ".git"), 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	t.Chdir(work)

	if _, runErr := captureRun(t, "bench", "live", "--delivery"); runErr != nil {
		t.Fatalf("bench live --delivery on empty dir failed: %v", runErr)
	}
	for _, sideEffect := range []string{".hookseen", ".injections.jsonl", ".usage.jsonl", ".hook.lock"} {
		if _, statErr := os.Stat(idxDir + sideEffect); !os.IsNotExist(statErr) {
			t.Errorf("bench live --delivery created side-effect file %s", sideEffect)
		}
		if _, statErr := os.Stat(filepath.Join(idxDir, sideEffect)); !os.IsNotExist(statErr) {
			t.Errorf("bench live --delivery created side-effect file in indexDir: %s", sideEffect)
		}
	}

	usage.RecordResult(idxDir, usage.KindRecall, 400, 1, false)
	usage.RecordResult(idxDir, usage.KindRecall, 0, 0, true)
	usage.RecordResult(idxDir, usage.KindHook, 320, 1, false)
	usage.Record(idxDir, usage.KindTool, 90)
	usage.RecordCompactionCapture(idxDir, usage.CompactionCapture{
		Key:       usage.CompactionKey{Session: "ses-1", Workspace: work, Revision: "rev1"},
		ToolCalls: 5,
	})

	snapLines := strings.Join([]string{
		`{"t":"2026-04-01T10:00:00Z","kind":"dejavu","bytes":220,"digest":"repeated identical recall line here\nrepeated identical recall line here"}`,
		`{"t":"2026-04-01T10:01:00Z","kind":"context","bytes":180,"digest":"unique recall line that appears once"}`,
	}, "\n") + "\n"
	if writeErr := os.WriteFile(idxDir+".injections.jsonl", []byte(snapLines), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	jsonOut, err := captureRun(t, "bench", "live", "--delivery", "--json")
	if err != nil {
		t.Fatalf("bench live --delivery --json: %v", err)
	}
	var rep liveDeliveryReport
	if unmarshalErr := json.Unmarshal([]byte(jsonOut), &rep); unmarshalErr != nil {
		t.Fatalf("unmarshal liveDeliveryReport: %v\n%s", unmarshalErr, jsonOut)
	}
	if rep.Repo != "payments" || rep.Recalls != 2 || rep.EmptyResultRate != 0.5 ||
		rep.Injections != 2 || rep.ToolLines != 1 || rep.PushToPullRatio != 1.0 ||
		rep.DuplicateDigestSnapshots != 1 || rep.Snapshots.Total != 2 {
		t.Fatalf("unexpected delivery JSON report: %+v", rep)
	}
	if rep.Compaction == nil || rep.Compaction.Captures != 1 {
		t.Fatalf("expected compaction captures=1, got %+v", rep.Compaction)
	}
	if rep.Harnesses.Catalog < 50 {
		t.Fatalf("expected harness catalog >= 50, got %+v", rep.Harnesses)
	}
	if rep.Harnesses.Detected != 0 || len(rep.Wirings) != 0 {
		t.Fatalf("expected 0 detected harnesses on clean env, got %+v %+v", rep.Harnesses, rep.Wirings)
	}

	textOut, err := captureRun(t, "bench", "live", "--delivery")
	if err != nil {
		t.Fatalf("bench live --delivery text: %v", err)
	}
	for _, want := range []string{"repo: payments", "harnesses:", "wirings: none", "pull: 2 recalls", "push/pull 1.00", "duplicate lines 1"} {
		if !strings.Contains(textOut, want) {
			t.Errorf("text report missing %q:\n%s", want, textOut)
		}
	}

	home := homeDir()
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	if claudeRoot == "" {
		claudeRoot = filepath.Join(home, ".claude", "projects")
	}
	seedFiles := map[string]string{
		filepath.Join(home, ".codex", "config.toml"):          "model = \"gpt-5\"\n",
		filepath.Join(home, ".gemini", "settings.json"):       "{}\n",
		filepath.Join(gooseConfigDir(), "config.yaml"):        "{}\n",
		filepath.Join(claudeRoot, "payments", "sess-1.jsonl"): "{\"type\":\"user\",\"timestamp\":\"2026-11-20T10:00:00Z\",\"sessionId\":\"sess-1\",\"cwd\":\"" + filepath.ToSlash(work) + "\",\"message\":{\"role\":\"user\",\"content\":\"fix payments webhook signature\"}}\n{\"type\":\"assistant\",\"timestamp\":\"2026-11-20T10:00:02Z\",\"sessionId\":\"sess-1\",\"cwd\":\"" + filepath.ToSlash(work) + "\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"checked hmac verification\"}]}}\n",
	}
	for p, body := range seedFiles {
		if mkErr := os.MkdirAll(filepath.Dir(p), 0o755); mkErr != nil {
			t.Fatal(mkErr)
		}
		if wErr := os.WriteFile(p, []byte(body), 0o644); wErr != nil {
			t.Fatal(wErr)
		}
	}
	if _, idxErr := captureRun(t, "index"); idxErr != nil {
		t.Fatalf("deja index seeded session: %v", idxErr)
	}

	jsonMulti, err := captureRun(t, "bench", "live", "--delivery", "--json")
	if err != nil {
		t.Fatalf("bench live --delivery --json multi: %v", err)
	}
	var repMulti liveDeliveryReport
	if unmarshalErr := json.Unmarshal([]byte(jsonMulti), &repMulti); unmarshalErr != nil {
		t.Fatalf("unmarshal multi liveDeliveryReport: %v\n%s", unmarshalErr, jsonMulti)
	}
	if repMulti.Harnesses.Detected < 4 || repMulti.Harnesses.WithSessions < 1 {
		t.Fatalf("expected >=4 detected harnesses and >=1 with sessions, got %+v", repMulti.Harnesses)
	}
	byName := map[string]liveDeliveryWiring{}
	for _, hw := range repMulti.Wirings {
		byName[hw.Harness] = hw
	}
	for _, wantName := range []string{"codex", "gemini", "goose", "claude-code"} {
		if _, ok := byName[wantName]; !ok {
			t.Fatalf("missing seeded harness %q in %+v", wantName, repMulti.Wirings)
		}
	}
	if byName["claude-code"].Sessions < 1 {
		t.Fatalf("expected indexed claude-code sessions >= 1, got %+v", byName["claude-code"])
	}

	textMulti, err := captureRun(t, "bench", "live", "--delivery")
	if err != nil {
		t.Fatalf("bench live --delivery multi text: %v", err)
	}
	if strings.Contains(textMulti, "wirings: none") {
		t.Fatalf("expected populated wirings line, got:\n%s", textMulti)
	}
	for _, want := range []string{"claude-code=", "codex=", "gemini=", "goose="} {
		if !strings.Contains(textMulti, want) {
			t.Errorf("multi text report missing %q:\n%s", want, textMulti)
		}
	}
}

// A marathon that brushes the question across hundreds of turns and the short
// session that settled it scored alike, so on ten questions phrased from real
// PR titles the giant took the top place twice and the session that did the
// work came second (#3214). The bench arm is where that shape is reproducible:
// the ranking's own unit fixtures are too small to build a marathon out of.
func TestTheMarathonDoesNotTakeTheSpecificQuestion(t *testing.T) {
	if testing.Short() {
		t.Skip("the prompt bench builds a corpus")
	}
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "prompt", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report promptReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid prompt JSON: %v", err)
	}
	arm := report.SpecificVsMarathon
	if arm.Cases == 0 {
		t.Fatal("the arm has no cases, so this measures nothing")
	}
	if arm.FalseFires != 0 {
		t.Errorf("the marathon took %d of %d specific questions", arm.FalseFires, arm.Cases)
	}
}

// The benchmark is what makes changes to per-prompt recall arguable instead of
// opinionated, so it needs to be right itself. These pin the parts that were
// wrong when it was written: a corpus dated in the future, gates the probe did
// not apply, and arms that scored the wrong chains.
func TestPromptBenchCorpusIsMeasurable(t *testing.T) {
	corpus := bench.GeneratePrompt(bench.Seed)
	kinds := map[string]int{}
	topics := map[string]bool{}
	for _, c := range corpus.Chains {
		kinds[c.Kind]++
		if c.Negative {
			kinds["negative"]++
			continue
		}
		// One topic per chain: the context corpus gives them all the same
		// vocabulary, which scores zero no matter what the extractor does.
		// Filler for the catch-all scope carries no question of its own: it is
		// asked about through the bucket-answer chain, whose question is the
		// one that must go unanswered. A chain nobody asks about still has to
		// earn its place, and this one earns it by being the wrong answer.
		if c.Kind == "bucket" || c.Kind == "haystack-noise" || c.Kind == "concluded-noise" ||
			c.Kind == "marathon-vs-noise" || c.Kind == "background" {
			continue
		}
		if topics[c.Topic] {
			t.Fatalf("topic %q appears in more than one chain", c.Topic)
		}
		topics[c.Topic] = true
		if c.Question == "" {
			t.Fatalf("chain %s has no question to ask", c.ID)
		}
		for _, s := range c.Sessions {
			// A corpus dated in the future reads as newer than now and the
			// freshness gate withholds all of it.
			if s.Updated.After(time.Now()) {
				t.Fatalf("chain %s is dated in the future: %v", c.ID, s.Updated)
			}
		}
	}
	if kinds["marathon"] == 0 || kinds["fresh"] == 0 {
		t.Fatalf("corpus has no gated shapes to measure: %v", kinds)
	}
	if kinds["negative"] == 0 {
		t.Fatal("no negative controls: a run could buy coverage with noise unnoticed")
	}
}

func TestPromptBenchScoresAndReports(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an index")
	}
	report, err := measurePrompt(bench.Seed)
	if err != nil {
		t.Fatal(err)
	}
	if report.Real.Cases == 0 || report.Marathon.Cases == 0 || report.Fresh.Cases == 0 {
		t.Fatalf("arms not populated: %+v", report)
	}
	// The numbers this run reports are the ones the PR quoted; a drop below
	// them means recall got worse without anyone saying so.
	if report.Real.Correct < report.Real.Cases {
		t.Fatalf("real questions answered dropped to %d/%d", report.Real.Correct, report.Real.Cases)
	}
	// Back to zero (#1534). The five this recorded were the fixture answering
	// its own controls: every content chain's filler quoted three of them word
	// for word, and the background sessions are built from the same working
	// vocabulary on purpose, so asking one inside its own project matched its
	// own text. Neither said anything about the bar.
	if report.Negative.FalseFires != 0 {
		t.Fatalf("%d false fires on negative controls", report.Negative.FalseFires)
	}
	if report.Real.Precision < 1 {
		t.Fatalf("precision fell to %.2f", report.Real.Precision)
	}
	if report.CorpusHash == "" {
		t.Fatal("report carries no corpus hash, so results cannot be compared across runs")
	}
	// Every other arm was reported and nothing read it. Over one night three
	// separate changes moved shown_line, haystack and russian_questions and the
	// suite stayed green — the numbers only appeared in a PR description, where
	// they are worth exactly as much as whoever remembered to look.
	//
	// Floors, not equalities: an arm that improves must not need this file
	// edited, and the three arms that still read false fires are held at their
	// current count so the known defects cannot quietly grow.
	if report.Shown.Correct < report.Shown.Cases {
		t.Fatalf("shown_line %d/%d: a block opened on a line carrying none of the question",
			report.Shown.Correct, report.Shown.Cases)
	}
	if report.Haystack.Correct < 3 {
		t.Fatalf("haystack %d/%d: a long session that mentions everything won again",
			report.Haystack.Correct, report.Haystack.Cases)
	}
	if report.Russian.Correct < report.Russian.Cases {
		t.Fatalf("russian_questions %d/%d", report.Russian.Correct, report.Russian.Cases)
	}
	if report.Paraphrase.Correct < 15 {
		t.Fatalf("paraphrase %d/%d, want >= 15", report.Paraphrase.Correct, report.Paraphrase.Cases)
	}
	if report.Tied.Correct < report.Tied.Cases {
		t.Fatalf("paraphrase_tied %d/%d, want 100%%", report.Tied.Correct, report.Tied.Cases)
	}
	if report.Marathon.Fired < 1 || report.Fresh.Fired < 1 {
		t.Fatalf("marathon %d/%d, fresh %d/%d: a shape the gates turn away",
			report.Marathon.Fired, report.Marathon.Cases, report.Fresh.Fired, report.Fresh.Cases)
	}
	// Known defects, held where they are. Each has its own entry in the loop
	// journal and none has a fix that survived measurement on a real store.
	for _, k := range []struct {
		name string
		arm  promptArmReport
		max  int
	}{
		{"absent_subject", report.AbsentSubject, 0},
		{"off_topic", report.OffTopic, 0},
		{"cross_paired", report.CrossPaired, 0},
		{"bucket_scope", report.Bucket, 1},
	} {
		if k.arm.FalseFires > k.max {
			t.Fatalf("%s false fires rose to %d of %d, was %d", k.name, k.arm.FalseFires, k.arm.Cases, k.max)
		}
	}
	if report.Pasted.Correct < report.Pasted.Cases {
		t.Fatalf("pasted_preamble %d/%d, want 100%%", report.Pasted.Correct, report.Pasted.Cases)
	}
	if report.LeadIn.Correct < report.LeadIn.Cases {
		t.Fatalf("inline_lead_in %d/%d, want 100%%", report.LeadIn.Correct, report.LeadIn.Cases)
	}
}

func TestRunBenchPromptWritesJSONAndText(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an index")
	}
	out := captureStdout(t, func() {
		if err := runBenchPrompt([]string{"--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var got promptReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json is not json: %v\n%s", err, out)
	}
	if got.Real.Cases == 0 {
		t.Fatalf("json report is empty: %s", out)
	}
	text := captureStdout(t, func() {
		if err := runBenchPrompt(nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"real questions", "long sessions", "recent sessions", "negative controls"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text report missing %q:\n%s", want, text)
		}
	}
}

func TestFinishPromptArmHandlesEmptyArms(t *testing.T) {
	var arm promptArmReport
	finishPromptArm(&arm, nil)
	if arm.FireRate != 0 || arm.Precision != 0 {
		t.Fatalf("empty arm reported rates: %+v", arm)
	}
	arm = promptArmReport{Cases: 4, Fired: 2, Correct: 1}
	finishPromptArm(&arm, []int{2, 3})
	if arm.FireRate != 0.5 || arm.Precision != 0.5 {
		t.Fatalf("rates wrong: %+v", arm)
	}
	if arm.MedianTerm == 0 {
		t.Fatal("median term count not computed")
	}
}

// The bucket arm measures a scope that cannot hold the answer, so nothing it
// returns is ever right. Until it existed the benchmark handed the probe the
// correct project on every case and could not see a scope failure at all —
// which is how `bench prompt` reported precision 1.00 while the hook was
// injecting a stranger's session on a real machine (2026-08-19).
func TestBucketArmCannotBeSatisfied(t *testing.T) {
	report, err := measurePrompt(1)
	if err != nil {
		t.Fatal(err)
	}
	b := report.Bucket
	if b.Cases == 0 {
		t.Fatal("the bucket case is not in the corpus, so a wrong scope goes unmeasured")
	}
	if b.Correct != 0 {
		t.Errorf("the bucket arm scored %d correct — nothing in a catch-all scope is the answer, so the question is being asked against the right project instead", b.Correct)
	}
	if b.Fired != b.FalseFires {
		t.Errorf("fired %d but counted %d false — every fire out of the bucket is a false one", b.Fired, b.FalseFires)
	}
}

// The gate this benchmark exists to be: one long escape-heavy value must not
// cost more than the rest of the store put together by orders of magnitude.
// That is what `sqlite3 -json` did for two months while every other benchmark
// stayed flat — 412s on a value this size against 0.04s, because no benchmark
// read a store at all (#3553, #3552).
func TestBenchReadHoldsTheLongValueToTheRestOfTheStore(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	hermeticEnv(t)
	// On a deadline, because the failure this guards against does not make the
	// benchmark slower by a little: a reader that is quadratic in what it
	// escapes takes about seven minutes on this store, which would read as a
	// package timeout rather than as a failed gate.
	type result struct {
		report readReport
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := measureRead(bench.Seed)
		done <- result{r, err}
	}()
	var got result
	select {
	case got = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("reading a store of half a megabyte and one long value took over a minute")
	}
	report, err := got.report, got.err
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != "" {
		t.Skipf("benchmark skipped itself: %s", report.Skipped)
	}
	if len(report.Classes) != 2 {
		t.Fatalf("measured %d stores, want the ordinary one and the one with a long value: %#v", len(report.Classes), report.Classes)
	}
	plain, long := report.Classes[0], report.Classes[1]
	if plain.Sessions == 0 || plain.Messages == 0 {
		t.Fatalf("the ordinary store read nothing: %#v", plain)
	}
	// Every message of the corpus has to come back, or the benchmark is timing
	// a store the reader mostly skips. The reader gates on `"type":"text"` in
	// the head of a part, so a fixture that writes its fields in another order
	// halves what it measures without saying so.
	corpus := bench.Generate(bench.Seed)
	want := 0
	for _, s := range corpus.Sessions {
		want += len(s.Messages)
	}
	if plain.Messages != want {
		t.Fatalf("read %d messages of the corpus's %d — the reader skipped what the fixture wrote", plain.Messages, want)
	}
	// The long value is one more message, and it is the only difference
	// between the two stores — otherwise the ratio below compares two
	// different corpora.
	if long.Sessions != plain.Sessions || long.Messages != plain.Messages+2 {
		t.Errorf("stores differ by more than the long value: %#v vs %#v", plain, long)
	}
	if long.StoreMB < 4 {
		t.Errorf("the long value did not land: the store is %.2f MB", long.StoreMB)
	}
	// Generous on purpose: this is a shared runner, and what it has to catch is
	// a reader that is quadratic in what it escapes, which lands four orders of
	// magnitude away rather than near any threshold.
	if report.Ratio > 100 {
		t.Errorf("one %d KB value cost %.0fx the rest of the store (%.1fms against %.1fms) — the read is not linear in what it escapes",
			readBenchLongValueKB, report.Ratio, long.WallMS, plain.WallMS)
	}
}

func TestBenchReadReportNamesBothStores(t *testing.T) {
	var b strings.Builder
	printReadReport(&b, readReport{
		CorpusHash: "abc",
		Seed:       1,
		Classes: []readClass{
			{Name: "ordinary store", WallMS: 18.9, StoreMB: 0.28, Sessions: 500, Messages: 1000},
			{Name: "one long tool output", WallMS: 53.6, StoreMB: 4.55, Sessions: 500, Messages: 1002},
		},
		Ratio: 2.8,
	})
	out := b.String()
	for _, want := range []string{"ordinary store", "one long tool output", "2.8x"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not say %q:\n%s", want, out)
		}
	}
}

func TestBenchReadSaysWhenItCannotRun(t *testing.T) {
	var b strings.Builder
	printReadReport(&b, readReport{Skipped: "sqlite3 is not installed, and the database-backed stores are read through it"})
	if !strings.Contains(b.String(), "sqlite3 is not installed") {
		t.Fatalf("a skipped run has to say why:\n%s", b.String())
	}
}

// benchCorpusHash runs one bench subcommand and returns the corpus it measured.
func benchCorpusHash(t *testing.T, sub string, args ...string) string {
	t.Helper()
	out, err := captureRun(t, append([]string{"bench", sub}, args...)...)
	if err != nil {
		t.Fatalf("bench %s %v: %v", sub, args, err)
	}
	var report struct {
		CorpusHash string `json:"corpus_hash"`
		Seed       int64  `json:"seed"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("bench %s output is not JSON: %v\n%s", sub, err, out)
	}
	return report.CorpusHash
}

// `deja help` documents `bench recall|context|prompt|block [--json] [--seed n]`,
// and two of the first three did not honor it: recall exited 1 on --seed,
// prompt took the flag and measured seed 1 anyway. A benchmark whose seed does
// nothing cannot be re-run on another corpus, which is the point of publishing
// one. Every subcommand with a seeded corpus belongs in this loop.
func TestBenchSeedChangesTheCorpusOnEverySubcommand(t *testing.T) {
	hermeticEnv(t)
	for _, sub := range []string{"recall", "context", "prompt", "block"} {
		one := benchCorpusHash(t, sub, "--seed", "1", "--json")
		other := benchCorpusHash(t, sub, "--seed", "7", "--json")
		if one == "" || other == "" {
			t.Fatalf("bench %s reported no corpus hash", sub)
		}
		if one == other {
			t.Errorf("bench %s measured the same corpus %s for seed 1 and seed 7 — --seed is ignored", sub, one)
		}
		// Same seed, same corpus: the flag has to be the only thing deciding.
		if again := benchCorpusHash(t, sub, "--seed", "7", "--json"); again != other {
			t.Errorf("bench %s is not reproducible at seed 7: %s then %s", sub, other, again)
		}
	}
}

// The usage line names the flags, so it has to name --seed too.
func TestBenchUsageNamesTheSeedFlag(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "bench", "recall", "--nope")
	if err == nil {
		t.Fatal("an unknown bench flag was accepted")
	}
	if !strings.Contains(err.Error(), "--seed") {
		t.Errorf("usage does not mention --seed: %v", err)
	}
}

// A seed that is not a number is named as such, not as a strconv error.
func TestBenchSeedNotANumberSaysSo(t *testing.T) {
	hermeticEnv(t)
	_, err := captureRun(t, "bench", "recall", "--seed", "abc")
	if err == nil || !strings.Contains(err.Error(), `--seed needs a whole number, got "abc"`) || strings.Contains(err.Error(), "strconv") {
		t.Errorf("bench --seed abc: %v", err)
	}
}

func TestBenchRecallJSONAndIsolation(t *testing.T) {
	outside := t.TempDir()
	t.Setenv("HOME", outside)
	t.Setenv("USERPROFILE", outside)
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "recall", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		CorpusHash string `json:"corpus_hash"`
		Sessions   int    `json:"sessions"`
		Queries    int    `json:"queries"`
		Lexical    struct {
			RecallAt1  float64 `json:"recall_at_1"`
			MRR        float64 `json:"mrr"`
			RecallAt5  float64 `json:"recall_at_5"`
			RecallAt10 float64 `json:"recall_at_10"`
			MedianMS   float64 `json:"median_latency_ms"`
		} `json:"lexical"`
		HybridStatus string `json:"hybrid_status"`
	}
	if err = json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid benchmark JSON %q: %v", out, err)
	}
	if len(report.CorpusHash) != 64 || report.Sessions != bench.SessionCount || report.Queries != bench.QueryCount || report.Lexical.RecallAt5 < 0.85 || report.Lexical.RecallAt10 < report.Lexical.RecallAt5 || report.Lexical.MedianMS < 0 || report.HybridStatus == "" {
		t.Fatalf("unexpected benchmark report: %#v", report)
	}
	// The columns that see the order, pinned at the top rather than at a
	// fraction. Each of the fifty queries names one session and that session is
	// the only one carrying its words (#3481 and the occasions after it), so the
	// ranker putting every answer first is the pass mark: a drop here is a
	// ranking regression and nothing else.
	//
	// These used to be asserted below recall@5, which was right while they were
	// capped by ties — recall@1 could not exceed 0.20 and MRR 0.457 whatever the
	// ranker did, and the bench printed exactly those two numbers. The cap was
	// the thing that made them look like measurements.
	if report.Lexical.RecallAt1 < 1 {
		t.Errorf("recall@1 %.2f: a query whose one answer is the only session that says it was not ranked first", report.Lexical.RecallAt1)
	}
	if report.Lexical.MRR < 1 {
		t.Errorf("MRR %.3f: the answer was not first for every query", report.Lexical.MRR)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark wrote outside its temp directory: %v", entries)
	}
}

func TestBenchRecallHumanAndInvalidArgs(t *testing.T) {
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:1")
	out, err := captureRun(t, "bench", "recall")
	if err != nil || !strings.Contains(out, "recall@5") || !strings.Contains(out, "hybrid: endpoint unavailable, skipped") {
		t.Fatalf("human benchmark output=%q err=%v", out, err)
	}
	if _, err := captureRun(t, "bench", "other"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal("invalid benchmark command did not fail")
	}
	if _, err := captureRun(t, "bench", "recall", "--bad"); err == nil {
		t.Fatal("invalid benchmark flag did not fail")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "index.db")); !os.IsNotExist(err) {
		t.Fatalf("unexpected real index path state: %v", err)
	}
}

func TestBenchHelpers(t *testing.T) {
	hits := []search.Hit{{Session: model.Session{ID: "one"}}, {Session: model.Session{ID: "two"}}}
	if !containsRelevant(hits, []string{"one"}, 1) || containsRelevant(hits, []string{"two"}, 1) {
		t.Fatal("relevance cutoff is wrong")
	}
	var out bytes.Buffer
	printBenchReport(&out, benchReport{Sessions: 1, Queries: 1, Lexical: benchMetric{RecallAt5: 1}, Hybrid: &benchMetric{RecallAt10: 1}, HybridStatus: "available"})
	if !strings.Contains(out.String(), "hybrid") {
		t.Fatalf("hybrid report missing: %q", out.String())
	}
	if _, err := measureRecall(filepath.Join(t.TempDir(), "missing"), []bench.Query{{Text: "missing"}}, nil); err == nil {
		t.Fatal("missing benchmark index did not fail")
	}
	root := t.TempDir()
	when := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	session := model.Session{ID: "helper", Project: "project", Messages: []model.Message{{Role: "user", Text: "helper", Time: when}}}
	if err := writeBenchCorpus(root, []model.Session{session}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "project", "helper.jsonl")); err != nil {
		t.Fatal(err)
	}
	badRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBenchCorpus(badRoot, []model.Session{session}); err == nil {
		t.Fatal("writing under a file did not fail")
	}
}

func TestBenchmarkTempDirStaysInWorkspace(t *testing.T) {
	dir, err := benchmarkTempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(workingDir, dir)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("benchmark temp dir escaped workspace: %q", dir)
	}
}

func TestBenchmarkTempDirRejectsSquattedParent(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, ".deja-bench"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := benchmarkTempDir(); err == nil {
		t.Fatal("benchmark accepted a squatted scratch path")
	}
	if err := runBenchRecall(false, bench.Seed); err == nil {
		t.Fatal("benchmark ignored a squatted scratch path")
	}
}

func TestMeasureRecallUsesFuzzyVariants(t *testing.T) {
	root := t.TempDir()
	corpus := bench.Generate(bench.Seed)
	claudeRoot := filepath.Join(root, "claude")
	if err := writeBenchCorpus(claudeRoot, corpus.Sessions); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(root, "index.db")
	restore := isolateBenchEnv(root, claudeRoot, indexDir)
	defer restore()
	if err := index.EnsureForSearch(indexDir, search.Options{Query: "", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := measureRecall(indexDir, []bench.Query{{Text: "invaldtion", Relevant: []string{"session-000"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := measureRecall(indexDir, []bench.Query{{Text: "cache", Relevant: []string{"session-000"}}}, &embed.Client{}); err == nil {
		t.Fatal("missing hybrid sidecar did not fail")
	}
}

func TestBenchHybridWhenEndpointIsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("embedding request: %v", err)
			return
		}
		vectors := make([][]float32, len(request.Input))
		for i := range vectors {
			vectors[i] = []float32{1}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	t.Setenv("DEJA_EMBED_URL", server.URL)
	if _, err := captureRun(t, "bench", "recall", "--json"); err != nil {
		t.Fatal(err)
	}
}

// A corpus chain that says "a tool printed this" has to reach the index as tool
// output. Written as a transcript line of type "tool-output" it reached nothing
// at all: the Claude reader knows "user" and "assistant", so the message was
// dropped and a benchmark arm built on it passed because the corpus was empty.
func TestBenchCorpusCarriesToolOutput(t *testing.T) {
	when := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	session := model.Session{ID: "toolout", Project: "toolproject", Harness: "claude",
		Started: when, Updated: when.Add(time.Minute), Messages: []model.Message{
			{Role: "user", Text: "прогони пайплайн", Time: when},
			{Role: "tool-output", Text: "[toil.leader] Job 'WDLStartJob' v41 is completely failed",
				Time: when.Add(30 * time.Second)},
		}}

	root := t.TempDir()
	if err := writeBenchCorpus(root, []model.Session{session}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "toolproject", "toolout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var sawToolResult bool
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err = json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(entry.Message.Content), `"tool_result"`) {
			sawToolResult = true
			if entry.Type != "user" {
				t.Errorf("tool output written under type %q, which the reader skips", entry.Type)
			}
		}
	}
	if !sawToolResult {
		t.Fatal("no tool_result block written, so the corpus cannot hold tool output")
	}

	dir := filepath.Join(t.TempDir(), "index.db")
	restore := isolateBenchEnv(t.TempDir(), root, dir)
	defer restore()
	if err = index.EnsureForSearch(dir, query.Options{All: true}, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	ss, err := indexSearch(dir, query.Options{Query: "v41", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "v41") && m.Role == "tool-output" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the indexed session holds no tool-output message carrying v41")
	}
}
