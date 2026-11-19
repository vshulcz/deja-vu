package index

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"os"
	"path/filepath"
	"strings"
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

	stored := ReadFixes(dir)
	if len(stored) == 0 {
		t.Skip("nothing mined from the fixture; the miner's own rules changed")
	}
	for _, p := range stored {
		if !strings.Contains(p.Error, "not a repository") {
			continue
		}
		if got := FixesFor(dir, p.Error, 3, nil); len(got) != 0 {
			t.Fatalf("served a remedy that is the failing command: ran %q after %q", got[0].Command, got[0].Failed)
		}
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
