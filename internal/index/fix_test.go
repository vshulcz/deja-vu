package index

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A failing run prints several error lines, and friction counts each of them.
// The pair was keyed on the first one only, so `deja fix` with the line that
// actually explains the failure — the refused connection under the test name —
// said no session ran anything after it, while friction listed it and the
// post-tool hook, handed the whole output, answered.
func TestAFixIsFoundByAnyErrorLineOfTheOutput(t *testing.T) {
	at := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	out := "--- FAIL: TestCheckout (0.02s)\n" +
		"    pool_test.go:41: dial tcp 127.0.0.1:5432: connect: connection refused\n" +
		"FAIL\nexit status 1"
	ms := []model.Message{
		{Role: roleCommand, Text: "go test ./internal/pool/...", Time: at},
		{Role: roleToolOutput, Text: out, Time: at.Add(time.Second)},
		{Role: roleCommand, Text: "docker compose up -d postgres", Time: at.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "Container postgres  Started", Time: at.Add(3 * time.Second)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "checkout")
	if len(pairs) == 0 {
		t.Fatal("the run mined no pair at all")
	}
	dir := t.TempDir()
	writeFixesForTest(t, dir, pairs)
	for _, asked := range []string{
		"dial tcp 127.0.0.1:5432: connect: connection refused",
		"--- FAIL: TestCheckout (0.02s)",
	} {
		got := FixesFor(dir, asked, 3, nil)
		if len(got) == 0 || got[0].Command != "docker compose up -d postgres" {
			t.Errorf("FixesFor(%q) = %+v", asked, got)
		}
	}
}

// Held back, not thrown away. A pair two sessions agree on is the answer
// whenever there is one; a single sighting is what the store has the rest of
// the time, which on a real machine is most of it.
func TestAConfirmedPairIsNeverDisplacedByASighting(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "one session ran this", Candidate: true, Project: "p"},
		{Sig: sig, Command: "two sessions ran this", Project: "p"},
	})

	got := FixesFor(dir, "zsh:1: command not found: timeout", 4, nil)
	if len(got) == 0 {
		t.Fatal("nothing came back")
	}
	for _, p := range got {
		if p.Candidate {
			t.Errorf("a single sighting was served beside a confirmed pair: %+v", p)
		}
	}
}

// With nothing confirmed, the sighting is what there is to say.
func TestASightingIsServedWhenNothingIsConfirmed(t *testing.T) {
	dir := t.TempDir()
	sig := frictionHash(mustFriction(t, "zsh:1: command not found: timeout"))
	writeFixesForTest(t, dir, []FixPair{
		{Sig: sig, Command: "one session ran this", Candidate: true, Project: "p"},
	})

	got := FixesFor(dir, "zsh:1: command not found: timeout", 4, nil)
	if len(got) != 1 || !got[0].Candidate {
		t.Errorf("the store held a sighting and answered with silence: %+v", got)
	}
}

func mustFriction(t *testing.T, line string) string { //nolint:unparam // the call names the line it classifies
	t.Helper()
	l, ok := FrictionLine(line)
	if !ok {
		t.Fatalf("not read as friction: %q", line)
	}
	return l
}

func writeFixesForTest(t *testing.T, dir string, pairs []FixPair) {
	t.Helper()
	invalidateFixesCache()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeGob(fixesPath(dir), pairs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, fixesFile)); err != nil {
		t.Fatal(err)
	}
}

// A remedy that names a file under a temp directory cannot be run by anyone,
// including the session that ran it: the file is gone by the time deja serves
// the pair. On this machine 38 of 186 confirmed pairs stored one — an agent
// scripts an edit into a scratch file, runs it, then re-runs the suite, and
// the whole line is mined as the fix for the failing test.
//
// Handing that back at the moment an agent is stuck is worse than silence,
// which is the bar the miner already holds itself to for a command that
// failed (#2163).
func TestARemedyUnderATempDirectoryIsNotMined(t *testing.T) {
	gone := []string{
		"$ python3 /Users/x/.claude/jobs/1a2b3c4d/tmp/edit38.py; go test ./internal/index/",
		"$ sh /tmp/patch.sh",
		"$ bash /var/folders/2b/T/scratch/apply.sh && go build ./...",
		"$ python3 $TMPDIR/fixture.py",
	}
	for _, cmd := range gone {
		if !namesAnEphemeralPath(cmd) {
			t.Errorf("kept as a remedy, and the file it names is gone: %s", cmd)
		}
	}

	kept := []string{
		"$ go mod tidy",
		"$ python3 scripts/gen.py",
		"$ brew install pgbouncer",
		// The word appears, the path does not: a flag or a package name that
		// merely spells it is not a scratch file.
		"$ go test ./internal/tmp/",
		"$ kubectl get pods --request-timeout=20s",
		"$ git commit -m 'drop the /tmp fallback'",
	}
	for _, cmd := range kept {
		if namesAnEphemeralPath(cmd) {
			t.Errorf("dropped a runnable remedy: %s", cmd)
		}
	}
}

// End to end through the miner, so the rule is wired and not merely present.
func TestTheMinerSkipsAScratchScriptAndTakesTheRealRemedy(t *testing.T) {
	ms := []model.Message{
		{Role: roleToolOutput, Text: "--- FAIL: TestPoolTimeout"},
		{Role: roleCommand, Text: "$ python3 /tmp/edit.py; go test ./..."},
		{Role: roleToolOutput, Text: "ok"},
		{Role: roleCommand, Text: "$ go mod tidy"},
		{Role: roleToolOutput, Text: "ok"},
	}
	pairs := fixPairsIn(ms, "k", "proj")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d: %#v", len(pairs), pairs)
	}
	if pairs[0].Command != "$ go mod tidy" {
		t.Errorf("remedy = %q, want the command that can still be run", pairs[0].Command)
	}
}

// A line deja stored is a line deja can look up. The shell-position rule reads
// `zsh:1: no matches found: …` and stores the line without the marker, and the
// stored line is not friction on its own — so `deja fix` was handed a line out
// of its own table, answered "nothing recorded for that line", and offered as
// the closest thing it held the line it had just been given. 106 of the 1,310
// error lines on a real store are that shape.
func TestAStoredErrorLineIsFoundByItsOwnText(t *testing.T) {
	now := time.Now().UTC()
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: "command", Text: `grep -rn "quokkabloom" --include=*.go internal`, Time: now},
			{Role: "tool-output", Text: "zsh:1: no matches found: --include=*.go", Time: now.Add(time.Second)},
			{Role: "command", Text: `grep -rn "quokkabloom" --include="*.go" internal`, Time: now.Add(2 * time.Second)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{s}, func(m model.Session) string { return m.Harness + ":" + m.ID })

	stored := ReadFixes(dir)
	if len(stored) != 1 {
		t.Fatalf("want one pair, got %+v", stored)
	}
	if got := FixesFor(dir, stored[0].Error, 3, nil); len(got) != 1 {
		t.Fatalf("the stored line %q found nothing: %+v", stored[0].Error, got)
	}
	// The marker the shell printed still works, and so does the whole line with
	// other output around it.
	if got := FixesFor(dir, "make check\nzsh:1: no matches found: --include=*.go\nexit 1", 3, nil); len(got) != 1 {
		t.Errorf("the pasted output stopped matching: %+v", got)
	}
	// The fallback is deja recognising its own wording, not a search: a line
	// that only contains the stored one is still a miss, which is what the
	// "closest it holds" hint is for.
	if got := FixesFor(dir, "no matches found", 3, nil); len(got) != 0 {
		t.Errorf("a partial line matched: %+v", got)
	}
}

// A session re-ran what had just failed, in the same shell, and the error went
// away for a reason of its own — so the pair is honest about what happened and
// useless as a remedy: "ran next: X / after this failed: X" tells an agent to
// repeat the command that produced the error. The two strings are not equal,
// which is why nothing caught it: a stored command carries its exit status
// (#3720).
func TestFixPairsDropARemedyThatIsTheFailure(t *testing.T) {
	now := time.Now().UTC()
	const cmd = "rtk git status --short && rtk git diff --stat"
	s := model.Session{
		Harness: "claude", ID: "s1",
		Messages: []model.Message{
			{Role: roleCommand, Text: "$ " + cmd + "  → exit 128", Time: now},
			{Role: roleToolOutput, Text: "fatal: not a repository (or any of the parent directories)", Time: now.Add(time.Second)},
			{Role: roleCommand, Text: "$ " + cmd, Time: now.Add(2 * time.Second)},
			{Role: roleToolOutput, Text: "M internal/index/fixpair.go", Time: now.Add(3 * time.Second)},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{s}, func(m model.Session) string { return m.Harness + ":" + m.ID })

	if got := FixesFor(dir, "fatal: not a repository (or any of the parent directories)", 3, nil); len(got) != 0 {
		t.Fatalf("served a remedy that is the failing command: ran %q after %q", got[0].Command, got[0].Failed)
	}
	if !remedyIsTheFailure(FixPair{Failed: "$ " + cmd + "  → exit 128", Command: "$ " + cmd}) {
		t.Fatal("remedyIsTheFailure stopped catching stored identical remedies")
	}

	// The control: the same shape with the command actually corrected is still
	// served. This is the pair `deja fix` exists for.
	s2 := model.Session{
		Harness: "claude", ID: "s2",
		Messages: []model.Message{
			{Role: roleCommand, Text: "$ timeout 90 make check  → exit 127", Time: now},
			{Role: roleToolOutput, Text: "command not found: timeout", Time: now.Add(time.Second)},
			{Role: roleCommand, Text: "$ make check", Time: now.Add(2 * time.Second)},
			{Role: roleToolOutput, Text: "ok", Time: now.Add(3 * time.Second)},
		},
	}
	dir2 := t.TempDir()
	buildFixes(dir2, []model.Session{s2}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	kept := ReadFixes(dir2)
	if len(kept) == 0 {
		t.Fatal("the corrected pair was not mined at all")
	}
	if got := FixesFor(dir2, kept[0].Error, 3, nil); len(got) == 0 {
		t.Fatal("a corrected command is the remedy this surface is for, and it was dropped")
	}

	// Explicit clean exit on the retry (now emitted across harnesses) still drops identical retries.
	sExit0 := model.Session{
		Harness: "claude", ID: "s3",
		Messages: []model.Message{
			{Role: roleCommand, Text: "$ " + cmd + "  → exit 128", Time: now},
			{Role: roleToolOutput, Text: "fatal: not a repository (or any of the parent directories)", Time: now.Add(time.Second)},
			{Role: roleCommand, Text: "$ " + cmd + "  → exit 0", Time: now.Add(2 * time.Second)},
			{Role: roleToolOutput, Text: "M internal/index/fixpair.go", Time: now.Add(3 * time.Second)},
		},
	}
	dirExit0 := t.TempDir()
	buildFixes(dirExit0, []model.Session{sExit0}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	if got := FixesFor(dirExit0, "fatal: not a repository (or any of the parent directories)", 3, nil); len(got) != 0 {
		t.Fatalf("served identical retry with exit 0: %+v", got)
	}

	// Unstamped retries (e.g. kimi) are also dropped.
	sUnstamped := model.Session{
		Harness: "kimi", ID: "s4",
		Messages: []model.Message{
			{Role: roleCommand, Text: "$ " + cmd, Time: now},
			{Role: roleToolOutput, Text: "fatal: not a repository (or any of the parent directories)", Time: now.Add(time.Second)},
			{Role: roleCommand, Text: "$ " + cmd, Time: now.Add(2 * time.Second)},
			{Role: roleToolOutput, Text: "M internal/index/fixpair.go", Time: now.Add(3 * time.Second)},
		},
	}
	dirUnstamped := t.TempDir()
	buildFixes(dirUnstamped, []model.Session{sUnstamped}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	if got := FixesFor(dirUnstamped, "fatal: not a repository (or any of the parent directories)", 3, nil); len(got) != 0 {
		t.Fatalf("served identical unstamped retry: %+v", got)
	}

	// Consecutive uncommanded env errors solved by the same generic command keep remedies for both errors.
	sEnv := model.Session{
		Harness: "claude", ID: "s5", Project: "p",
		Messages: []model.Message{
			{Role: roleToolOutput, Text: "ModuleNotFoundError: No module named 'alpha'", Time: now},
			{Role: roleCommand, Text: "$ uv sync  → exit 0", Time: now.Add(time.Minute)},
			{Role: roleToolOutput, Text: "ok", Time: now.Add(2 * time.Minute)},
			{Role: roleToolOutput, Text: "ModuleNotFoundError: No module named 'beta'", Time: now.Add(3 * time.Minute)},
			{Role: roleCommand, Text: "$ uv sync  → exit 0", Time: now.Add(4 * time.Minute)},
			{Role: roleToolOutput, Text: "ok", Time: now.Add(5 * time.Minute)},
		},
	}
	dirEnv := t.TempDir()
	buildFixes(dirEnv, []model.Session{sEnv, sEnv}, func(m model.Session) string { return m.Harness + ":" + m.ID })
	sigs := map[uint64]bool{}
	for _, p := range ReadFixes(dirEnv) {
		sigs[p.Sig] = true
	}
	if len(sigs) != 2 {
		t.Fatalf("env generic remedy kept for %d error(s), want 2", len(sigs))
	}
}

func TestShellOnlySigsAndTheSameFailingCommand(t *testing.T) {
	out := "(eval):1: == not found\nzsh:1: command not found: gtimeout\nError: widget cache is locked by pid 4242"
	sigs := fixSignaturesFor(t.TempDir(), out)
	shell := shellOnlySigs(out, "make lint-planner && echo == done", sigs)
	parse, _ := FrictionLine("(eval):1: == not found")
	missing, _ := FrictionLine("zsh:1: command not found: gtimeout")
	locked, _ := FrictionLine("Error: widget cache is locked by pid 4242")
	if !shell[frictionHash(parse)] || !shell[frictionHash(missing)] || shell[frictionHash(locked)] {
		t.Errorf("shell-only set %v", shell)
	}
	// The command runs the missing program: that is its own failure.
	if s := shellOnlySigs(out, "gtimeout 30 go vet ./...", sigs); s[frictionHash(missing)] {
		t.Error("a missing program the command runs read as the shell's complaint")
	}
	if shellOnlySigs(out, "", sigs) != nil {
		t.Error("with no command there is nothing to compare")
	}
	if !shellSpoke("bash: line 3: syntax error near unexpected token `)'") || !shellSpoke("zsh: parse error near `|'") || shellSpoke("/bin/bash: line 9: gh: command not found") {
		t.Error("shellSpoke misread a line")
	}

	p := FixPair{Failed: "cd /work/orchard && make lint-planner && echo == done", Command: "make lint-planner && echo done"}
	if !sameFailingCommand(p, "make lint-planner && echo == ok") {
		t.Error("the same command corrected was not the same failure")
	}
	if sameFailingCommand(p, "go test ./internal/rows; echo == end") || sameFailingCommand(p, "") {
		t.Error("another command was the same failure")
	}
	if !sameFailingCommand(FixPair{Command: "make lint-planner"}, "make lint-planner") {
		t.Error("a pair with no failed command is compared on its remedy")
	}
	if got := without(map[uint64]bool{1: true, 2: true}, map[uint64]bool{2: true}); len(got) != 1 || !got[1] {
		t.Errorf("without = %v", got)
	}
	globOut := "(eval):1: no matches found: --include=*.py\nError: widget cache is locked by pid 4242"
	globSigs := fixSignaturesFor(t.TempDir(), globOut)
	globLine, _ := FrictionLine("(eval):1: no matches found: --include=*.py")
	if s := shellOnlySigs(globOut, "grep -r --include=*.py foo", globSigs); s[frictionHash(globLine)] {
		t.Error("an unmatched glob the command carries read as an unanchored shell complaint")
	}
	for _, diag := range []string{
		"(eval):1: no matches found: --include=*.py",
		"zsh:1: bad pattern: [a-",
		"zsh: = not found",
		"== not found",
		"bash: syntax error near unexpected token `)'",
	} {
		if !IsShellDiagnostic(diag) {
			t.Errorf("IsShellDiagnostic(%q) = false, want true", diag)
		}
	}
	if IsShellDiagnostic("") || IsShellDiagnostic("psql: error: connection refused") {
		t.Error("IsShellDiagnostic matched non-shell output")
	}
}

func TestFixPairMiningStripsExitMarkersAcrossSessions(t *testing.T) {
	if repairedVariant("$ go test ./...  → exit 1", "$ go test ./...  → exit 0") {
		t.Fatal("repairedVariant treated identical commands with different exit markers as a repair")
	}

	now := time.Now().UTC()
	sessions := []model.Session{
		{
			Harness: "claude", ID: "s1",
			Messages: []model.Message{
				{Role: roleCommand, Text: "$ psql -c 'select 1'  → exit 2", Time: now},
				{Role: roleToolOutput, Text: "psql: error: connection refused", Time: now.Add(time.Second)},
				{Role: roleCommand, Text: "$ brew services start postgresql  → exit 0", Time: now.Add(2 * time.Second)},
				{Role: roleToolOutput, Text: "Successfully started postgresql", Time: now.Add(3 * time.Second)},
			},
		},
		{
			Harness: "kimi", ID: "s2",
			Messages: []model.Message{
				{Role: roleCommand, Text: "$ psql -c 'select 1'", Time: now.Add(time.Minute)},
				{Role: roleToolOutput, Text: "psql: error: connection refused", Time: now.Add(time.Minute + time.Second)},
				{Role: roleCommand, Text: "$ brew services start postgresql", Time: now.Add(time.Minute + 2*time.Second)},
				{Role: roleToolOutput, Text: "Successfully started postgresql", Time: now.Add(time.Minute + 3*time.Second)},
			},
		},
	}
	dir := t.TempDir()
	buildFixes(dir, sessions, func(m model.Session) string { return m.Harness + ":" + m.ID })

	stored := FixesFor(dir, "psql: error: connection refused", 5, nil)
	if len(stored) != 1 {
		t.Fatalf("fixes = %d, want 1 deduplicated confirmed pair: %+v", len(stored), stored)
	}
	if stored[0].Command != "$ brew services start postgresql" {
		t.Errorf("stored Command = %q, want stripped of exit marker", stored[0].Command)
	}
	if stored[0].Candidate {
		t.Errorf("cross-harness pair with exit 0 in first session stayed Candidate: %+v", stored[0])
	}
}

func TestRepairedPairAnswersSharedSecondaryLineOnlyWhenActionable(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	pipMsgs := []model.Message{
		{Role: roleCommand, Text: "$ pip3 install vllm==0.6.3  → exit 1", Time: now},
		{Role: roleToolOutput, Text: "ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)\nERROR: No matching distribution found for vllm==0.6.3", Time: now.Add(time.Second)},
		{Role: roleCommand, Text: "$ pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3  → exit 0", Time: now.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "Successfully installed vllm-0.6.3", Time: now.Add(3 * time.Second)},
	}
	pipDir := t.TempDir()
	buildFixes(pipDir, []model.Session{{Harness: "claude", ID: "pip-1", Messages: pipMsgs}}, func(s model.Session) string {
		return s.Harness + ":" + s.ID
	})
	pipGot := FixesFor(pipDir, "ERROR: No matching distribution found for vllm==0.6.3", 3, nil)
	if len(pipGot) == 0 || pipGot[0].Command != "$ pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3" {
		t.Fatalf("pip secondary line = %+v, want mirror install", pipGot)
	}

	testMsgs := []model.Message{
		{Role: roleCommand, Text: "$ go test -run TestAlphaFailure ./pkg/svc  → exit 1", Time: now},
		{Role: roleToolOutput, Text: "--- FAIL: TestAlphaFailure (0.01s)\npkg/svc: permission denied", Time: now.Add(time.Second)},
		{Role: roleCommand, Text: "$ go test -run TestAlphaFailure -v ./pkg/svc  → exit 0", Time: now.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "PASS", Time: now.Add(3 * time.Second)},
	}
	testDir := t.TempDir()
	buildFixes(testDir, []model.Session{{Harness: "claude", ID: "go-1", Messages: testMsgs}}, func(s model.Session) string {
		return s.Harness + ":" + s.ID
	})
	if testGot := FixesFor(testDir, "pkg/svc: permission denied", 3, nil); len(testGot) != 0 {
		t.Fatalf("test-flag tweak answered secondary error line: %+v", testGot)
	}

	shellMsgs := []model.Message{
		{Role: roleCommand, Text: "$ timeout 30 deploy-tool stage-west  → exit 127", Time: now},
		{Role: roleToolOutput, Text: "zsh:1: command not found: timeout\ndeploy-tool: permission denied: stage-west", Time: now.Add(time.Second)},
		{Role: roleCommand, Text: "$ deploy-tool stage-west  → exit 0", Time: now.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "deployed stage-west", Time: now.Add(3 * time.Second)},
	}
	shellDir := t.TempDir()
	buildFixes(shellDir, []model.Session{{Harness: "claude", ID: "sh-1", Messages: shellMsgs}}, func(s model.Session) string {
		return s.Harness + ":" + s.ID
	})
	if shellGot := FixesFor(shellDir, "deploy-tool: permission denied: stage-west", 3, nil); len(shellGot) != 0 {
		t.Fatalf("shell repair answered non-shell secondary line without matching command: %+v", shellGot)
	}
	if cmdGot := FixesForCommand(shellDir, "timeout 30 deploy-tool stage-west", "deploy-tool: permission denied: stage-west", 3, nil); len(cmdGot) == 0 || cmdGot[0].Command != "$ deploy-tool stage-west" {
		t.Fatalf("FixesForCommand sameFailingCommand = %+v, want $ deploy-tool stage-west", cmdGot)
	}
}

func TestFixesForCommandGatesTimeoutOnSameCommand(t *testing.T) {
	now := time.Now().Add(-time.Hour).UTC()
	msgs := []model.Message{
		{Role: roleCommand, Text: "$ go test ./slow/...  → exit 124", Time: now},
		{Role: roleToolOutput, Text: "Command timed out after 2m 0.0s", Time: now.Add(time.Second)},
		{Role: roleCommand, Text: "$ go test -timeout 5m ./slow/...  → exit 0", Time: now.Add(2 * time.Second)},
		{Role: roleToolOutput, Text: "ok", Time: now.Add(3 * time.Second)},
	}
	dir := t.TempDir()
	buildFixes(dir, []model.Session{{Harness: "claude", ID: "to-1", Messages: msgs}}, func(s model.Session) string {
		return s.Harness + ":" + s.ID
	})
	if got := FixesFor(dir, "Command timed out after 2m 0.0s", 3, nil); len(got) != 0 {
		t.Fatalf("generic timeout matched without failing command: %+v", got)
	}
	if got := FixesForCommand(dir, "npm run build", "Command timed out after 2m 0.0s", 3, nil); len(got) != 0 {
		t.Fatalf("generic timeout matched different command: %+v", got)
	}
	if got := FixesForCommand(dir, "go test ./slow/...", "Command timed out after 2m 0.0s", 3, nil); len(got) == 0 || got[0].Command != "$ go test -timeout 5m ./slow/..." {
		t.Fatalf("generic timeout failed to match same command: %+v", got)
	}
}
