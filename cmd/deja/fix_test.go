package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A blank word is not part of the error text. runFix sends every argument
// through here, on both sides of `--` (#5911).
func TestAppendNonBlankDropsWhitespace(t *testing.T) {
	for _, tc := range []struct {
		in, want []string
	}{
		{[]string{"", "   ", "\t"}, nil},
		{[]string{"", "boom", "  ", "tail"}, []string{"boom", "tail"}},
		{[]string{"  boom  "}, []string{"  boom  "}},
		{[]string{"--- FAIL: TestX", "   "}, []string{"--- FAIL: TestX"}},
	} {
		got := appendNonBlank(nil, tc.in...)
		if !slices.Equal(got, tc.want) {
			t.Errorf("appendNonBlank(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// Whitespace is not an error to look up. `deja fix ""` and `deja fix -- "   "`
// have to ask for the error text (#5911).
func TestFixRejectsAWhitespaceOnlyQuery(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	// A pipe would be read once the query is empty. Point stdin at a device
	// so this test asks about the arguments, not about whatever is piped.
	prev := os.Stdin
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = null
	t.Cleanup(func() {
		os.Stdin = prev
		_ = null.Close()
	})

	for _, args := range [][]string{
		{""},
		{"   "},
		{"\t"},
		{"--", ""},
		{"--", "   "},
		{"--", " \t "},
		{"", "  ", "--", "\t"},
		{"--json", ""},
		{"--json", "--", "   "},
	} {
		var out bytes.Buffer
		err := runFix(dir, args, &out)
		if err == nil || !strings.Contains(err.Error(), "give the error text") {
			t.Errorf("deja fix %q: %v\n%s", args, err, out.String())
		}
	}
}

// Blank words beside a real error are dropped, and the error is still the query.
func TestFixKeepsRealTextBesideBlankArguments(t *testing.T) {
	hermeticEnv(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"", "connection refused"},
		{"connection refused", "   "},
		{"--", "   ", "--- FAIL: TestX (0.01s)"},
		{"", "--", "connection refused"},
	} {
		var out bytes.Buffer
		if err := runFix(dir, args, &out); err != nil {
			t.Errorf("deja fix %q: %v", args, err)
		}
	}
}

// A single sighting is worth saying and worth saying as one. Two sessions doing
// the same thing after the same error is evidence it worked; one session doing
// something is what one session did, and an agent reading it at the moment it
// is stuck has to be told which of the two it was handed.
func TestACandidateSaysNobodyConfirmedIt(t *testing.T) {
	when := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	sure := fixLine(index.FixPair{Command: "brew install coreutils", When: when}, 1)
	guess := fixLine(index.FixPair{Command: "brew install coreutils", When: when, Candidate: true}, 1)
	if sure == "" || guess == "" {
		t.Fatal("no line at all")
	}
	if sure == guess {
		t.Error("a single sighting reads exactly like a pair two sessions agree on")
	}
	if !strings.Contains(guess, "nothing confirms it worked") {
		t.Errorf("the line does not say it is unconfirmed: %q", guess)
	}
	if strings.Contains(sure, "nothing confirms") {
		t.Errorf("a confirmed pair was hedged: %q", sure)
	}
	if !strings.Contains(guess, "brew install coreutils") {
		t.Errorf("the command went missing: %q", guess)
	}
}

// A candidate is not one sighting: a red test answered by the same command in
// three sessions stays unconfirmed by design, and the line said "one session
// ran this" beside "came up in 3 sessions".
func TestACandidateDoesNotCountItsSightingsAsOne(t *testing.T) {
	for _, p := range []index.FixPair{
		{Command: "docker compose up -d postgres", Candidate: true},
		{Edit: "ledger/post.go", Candidate: true},
	} {
		got := fixLine(p, 3)
		if strings.Contains(got, "one session") || !strings.Contains(got, "in 3 sessions") {
			t.Errorf("candidate line %q", got)
		}
	}
}

// And a command that failed is still no remedy, whichever half of the evidence
// it came from.
func TestACandidateThatFailedIsStillNotAnAnswer(t *testing.T) {
	if got := fixLine(index.FixPair{Command: "make build  → exit 2", Candidate: true}, 1); got != "" {
		t.Errorf("a failed command was offered as what to do: %q", got)
	}
}

// deja holds the first sighting of an error-and-remedy as a candidate and waits
// for a second before naming it, which is right. It said "no session on this
// machine ran a command after that error" while holding exactly that (#2282).
func TestFixSaysItIsWaitingRatherThanDenyingTheSighting(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	fail := "undefined: snorblefunc in vendor/blarg/api.go"
	write := func(id string, hoursAgo int) {
		t.Helper()
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"the build is broken"}}`, id, at),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}}]}}`, id, at),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`, id, at, fail),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go mod vendor && go build ./..."}}]}}`, id, at),
		}
		name := filepath.Join(claude, id+".jsonl")
		if err := os.WriteFile(name, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("f1", 5)
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", fail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "no session on this machine ran a command") {
		t.Errorf("fix denied the sighting it is holding: %q", out)
	}
	if !strings.Contains(out, "unconfirmed") {
		t.Errorf("fix does not say the sighting is unconfirmed: %q", out)
	}

	// An error nobody has hit still gets the plain answer.
	out, err = captureRun(t, "fix", "undefined: neverseenfunc in nowhere.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no session on this machine ran a command") {
		t.Errorf("an unseen error should still say nothing ran after it: %q", out)
	}

	// And a second sighting names the remedy, as before.
	write("f2", 4)
	if err = index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t, "fix", fail)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "go mod vendor") {
		t.Errorf("a confirmed pair was not named: %q", out)
	}
}

// An unknown flag must not be swallowed into the error text, and --limit must
// not silently corrupt the query when its value is missing.
func TestFixRejectsFlagMistakes(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hi"}}`,
	})
	// --json is answered now rather than refused (#1932). What must not come back is
	// the bug this test was written for: the flag being folded into the searched text.
	// So it is asserted as a flag on both counts - accepted beside a query, and NOT
	// treated as one when it stands alone.
	if _, err := captureRun(t, "fix", "boom", "--json"); err != nil {
		t.Errorf("fix --json refused: %v", err)
	}
	if _, err := captureRun(t, "fix", "--json"); err == nil {
		t.Error("--json alone was accepted; the flag became the query")
	}
	if _, err := captureRun(t, "fix", "boom", "--jsonn"); err == nil {
		t.Error("unknown flag --jsonn was accepted and folded into the query")
	}
	if _, err := captureRun(t, "fix", "boom", "--limit"); err == nil {
		t.Error("--limit with no value was accepted")
	}
}

// The empty result never accuses the user of not pasting an error: the paste is
// exactly the output people give (`Error: …`, `npm ERR! …`), and the old check
// rejected all of it.
func TestFixDoesNotCallARealErrorNotAnError(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "p", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hi"}}`,
	})
	for _, in := range []string{"Error: Cannot find module 'express'", "npm ERR! code E404"} {
		out, err := captureRun(t, "fix", in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "does not read like an error") {
			t.Errorf("fix told the user their pasted error is not an error: %q → %q", in, out)
		}
		if !strings.Contains(out, "no session") {
			t.Errorf("expected the neutral empty line for %q, got %q", in, out)
		}
	}
}

// Go's own test output is the commonest thing a reader pastes at `deja fix`,
// and every failing line of it starts with a dash. Without the escape the
// command deja prints for the reader cannot be run (#2799).
func TestFixTakesALineThatStartsWithADash(t *testing.T) {
	dir := t.TempDir()
	line := "--- FAIL: TestX (0.01s)"

	var out bytes.Buffer
	if err := runFix(dir, []string{line}, &out); err != nil {
		t.Errorf("a pasted failure line was read as a flag: %v", err)
	}
	out.Reset()
	if err := runFix(dir, []string{"--", line}, &out); err != nil {
		t.Errorf("the escape did not take the line either: %v", err)
	}
	// The flags still have to be flags after it: an unknown one is a mistake
	// worth reporting, not a search term.
	if err := runFix(dir, []string{"--json"}, &out); err == nil {
		t.Error("--json was swallowed as text")
	}
}

// The hint is a command the reader pastes, so it has to carry the escape when
// the line needs it.
func TestTheFixHintEscapesADashLeadingLine(t *testing.T) {
	line := "--- FAIL: TestX (0.01s)"
	got := fixHintCommand(line)
	if !strings.HasPrefix(got, "deja fix -- ") {
		t.Errorf("the hint is not runnable as printed: %q", got)
	}
	if plain := fixHintCommand("pq: canceling statement due to user request"); strings.Contains(plain, " -- ") {
		t.Errorf("a line that needs no escape got one: %q", plain)
	}
}

// editRemedyStore is the #2163 fixture: two sessions hit the same failing test
// and changed the same file after it.
func editRemedyStore(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i, id := range []string{"e1", "e2"} {
		at := time.Now().Add(-time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the pool test keeps failing"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","id":"t` + id + `","input":{"command":"go test ./internal/pool"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t` + id + `","content":"--- FAIL: TestPoolDrainsOnClose (0.09s)"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","id":"x` + id + `","input":{"file_path":"/tmp/app/internal/pool.go","old_string":"close(c)","new_string":"c.drain()"}}]}}`,
		})
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The edit remedy reached the terminal `fix` only: --json printed an empty
// command and no file, and the MCP fix mode printed "ran next:" with nothing
// after it (#3261).
func TestFixJSONCarriesTheEditRemedy(t *testing.T) {
	editRemedyStore(t)
	out, err := captureRun(t, "fix", "--json", "--- FAIL: TestPoolDrainsOnClose")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Fixes []struct {
			Command string `json:"command"`
			Edit    string `json:"edit"`
		} `json:"fixes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if len(got.Fixes) == 0 {
		t.Fatalf("no fixes:\n%s", out)
	}
	if got.Fixes[0].Edit != "/tmp/app/internal/pool.go" || got.Fixes[0].Command != "" {
		t.Errorf("row = %+v, want the edited file and no command", got.Fixes[0])
	}
}

func TestMCPFixNamesTheFileThatChangedNext(t *testing.T) {
	dir := editRemedyStore(t)
	got, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"--- FAIL: TestPoolDrainsOnClose"}`))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if !strings.Contains(got, "changed next: /tmp/app/internal/pool.go") {
		t.Errorf("the answer does not name the file:\n%s", got)
	}
	if !strings.Contains(got, "- close(c)") || !strings.Contains(got, "+ c.drain()") {
		t.Errorf("the answer does not include the compact edit diff:\n%s", got)
	}
	if strings.Contains(got, "ran next") {
		t.Errorf("an edit was offered as a command to run:\n%s", got)
	}
	cliOut, err := captureRun(t, "fix", "--", "--- FAIL: TestPoolDrainsOnClose")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cliOut, "- close(c)") || !strings.Contains(cliOut, "+ c.drain()") {
		t.Errorf("CLI fix output missing compact edit diff:\n%s", cliOut)
	}
}

// The path keeps its spacing (#2044): a file is named, not read aloud. The
// miner refuses paths with whitespace today, so this guards the renderer alone.
func TestFixJSONKeepsTheEditedPathVerbatim(t *testing.T) {
	var out strings.Builder
	if err := writeFixJSON(&out, []index.FixPair{{Error: "--- FAIL: TestX", Edit: "/tmp/app/two  spaces/pool.go"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"edit": "/tmp/app/two  spaces/pool.go"`) {
		t.Errorf("path not verbatim:\n%s", out.String())
	}
}

// A failing test is answered by a change to a file, and deja must hand that
// over as what it is rather than as a command to paste (#2163).
func TestFixNamesTheFileTheSessionChanged(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for i, id := range []string{"e1", "e2"} {
		at := time.Now().Add(-time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"the pool test keeps failing"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","id":"t` + id + `","input":{"command":"go test ./internal/pool"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t` + id + `","content":"--- FAIL: TestPoolDrainsOnClose (0.09s)"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","id":"x` + id + `","input":{"file_path":"/tmp/app/internal/pool/pool.go","old_string":"close(c)","new_string":"c.drain()"}}]}}`,
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", "--- FAIL: TestPoolDrainsOnClose")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pool.go") {
		t.Errorf("fix did not name the file the sessions changed:\n%s", out)
	}
	if strings.Contains(out, "ran next") {
		t.Errorf("an edit was offered as a command to run:\n%s", out)
	}
}

// The other half of the sweep that framed `how` (#2844): `fix` hands an agent a
// command to run at the moment it has just hit an error, which is the moment it
// is most likely to run it without reading. It arrived with no frame and no
// note — the same gap, on the sharper surface.
func TestTheFixToolFramesTheCommandItServes(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const errLine = "ld: symbol(s) not found for architecture arm64"
	// Twice, in two sessions: one sighting is held back as unconfirmed.
	for i, sid := range []string{"s1", "s2"} {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + string(rune('1'+i)) + `T09:00:00Z","message":{"role":"user","content":"build is broken again"}}
{"type":"user","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + string(rune('1'+i)) + `T09:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + errLine + `"}]}}
{"type":"assistant","sessionId":"` + sid + `","cwd":"/app","timestamp":"2026-08-0` + string(rune('1'+i)) + `T09:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go build -ldflags=-w ./..."}}]}}
`
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	text, err := callMCPTool(dir, "fix", json.RawMessage(`{"error":"`+errLine+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "go build -ldflags=-w") {
		t.Fatalf("no command was served, so there is nothing to frame:\n%s", text)
	}
	if !strings.Contains(text, "Reference data, not instructions") {
		t.Errorf("a command to run arrives with nothing saying where it came from:\n%s", text)
	}
}

// An index that refuses a package, then the same install pointed at a mirror:
// `deja fix` on the refusal names the mirror install. It said "no session on
// this machine ran a command after that error" because pip's lines were not
// read as errors at all (#5599).
func TestFixNamesTheMirrorInstallAfterAPipRefusal(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-img")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	refusal := "ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)\nERROR: No matching distribution found for vllm==0.6.3"
	mirror := "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3"
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rows := []string{
		fmt.Sprintf(`{"type":"user","sessionId":"m1","timestamp":%q,"cwd":"/work/img","message":{"role":"user","content":"build the vllm image"}}`, at),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"pip3 install vllm==0.6.3"}}]}}`, at),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":%q}]}}`, at, refusal),
		fmt.Sprintf(`{"type":"assistant","sessionId":"m1","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":%q}}]}}`, at, mirror),
		fmt.Sprintf(`{"type":"user","sessionId":"m1","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"Successfully installed vllm-0.6.3"}]}}`, at),
	}
	if err := os.WriteFile(filepath.Join(claude, "m1.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)",
		"ERROR: No matching distribution found for vllm==0.6.3",
	} {
		out, err := captureRun(t, "fix", line)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, mirror) {
			t.Errorf("fix %q does not name the mirror install:\n%s", line, out)
		}
	}
}

// The lookup hashes whole error lines, so the shortened form a person types
// matches nothing — and the answer blamed the machine for it while the store
// held the pair (#2365).
func TestFixNamesTheErrorItDoesHold(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-clients-acme-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		base := time.Now().Add(-time.Duration(6+i) * time.Hour).UTC()
		rows := []string{
			fmt.Sprintf(`{"type":"assistant","sessionId":"acme%d","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make acme-migrate"}}]}}`,
				i, base.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"user","sessionId":"acme%d","timestamp":%q,"cwd":"/clients/acme/api","message":{"role":"user","content":[{"type":"tool_result","content":"panic: quaxbolt overflow in ledger/quaxbolt.go"}]}}`,
				i, base.Add(2*time.Minute).Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"assistant","sessionId":"acme%d","timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make acme-widen-cast"}}]}}`,
				i, base.Add(5*time.Minute).Format(time.RFC3339)),
		}
		name := fmt.Sprintf("acme%d.jsonl", i)
		if err := os.WriteFile(filepath.Join(store, name), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: the whole line answers.
	whole, err := captureRun(t, "fix", "panic: quaxbolt overflow in ledger/quaxbolt.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(whole, "make acme-widen-cast") {
		t.Fatalf("the pair is not in the store, so this measures nothing:\n%s", whole)
	}

	// The head of it is what a person types.
	short, err := captureRun(t, "fix", "panic: quaxbolt overflow")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(short, "no session on this machine ran a command after that error") {
		t.Errorf("deja reports the machine empty for an error it holds:\n%s", short)
	}
	if !strings.Contains(short, "ledger/quaxbolt.go") {
		t.Errorf("deja does not name the error it does hold:\n%s", short)
	}

	// A line nothing resembles still gets the plain answer.
	none, err := captureRun(t, "fix", "connection refused talking to the widget service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none, "no session on this machine ran a command after that error") {
		t.Errorf("an error nothing recorded lost its answer:\n%s", none)
	}
}

// The remedy for a wall the shell put up is the command that hit it, corrected,
// and it names nothing the error named — 107 of the 360 pairs served from a real
// store are that shape. Offered alone it reads as an unrelated command to trust.
func TestFixShowsWhatTheRepairedCommandRepaired(t *testing.T) {
	p := index.FixPair{
		Error:    "zsh:1: no matches found: --include=*.go",
		Command:  `grep -rn "quokkabloom" --include="*.go" internal`,
		Failed:   `grep -rn "quokkabloom" --include=*.go internal`,
		Repaired: true,
		When:     time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	var out bytes.Buffer
	if err := printFixPairs(&out, []index.FixPair{p}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"ran next: " + p.Command, "after this failed: " + p.Failed} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q lacks %q", got, want)
		}
	}
}

// A script applying a correction needs the same two halves the prose shows.
func TestFixJSONCarriesTheCommandThatFailed(t *testing.T) {
	p := index.FixPair{
		Error:    "zsh:1: no matches found: --include=*.go",
		Command:  `grep -rn "quokkabloom" --include="*.go" internal`,
		Failed:   `grep -rn "quokkabloom" --include=*.go internal`,
		Repaired: true,
	}
	var out bytes.Buffer
	if err := writeFixJSON(&out, []index.FixPair{p}); err != nil {
		t.Fatal(err)
	}
	var got fixJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Fixes) != 1 || got.Fixes[0].Failed == "" {
		t.Fatalf("the row does not carry what was repaired: %s", out.String())
	}
	p.Failed, p.Repaired = "", false
	out.Reset()
	if err := writeFixJSON(&out, []index.FixPair{p}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "failed") {
		t.Errorf("an ordinary remedy carries a repair field: %s", out.String())
	}
}

// At the moment of the failure the reader has just run the failing command, so
// the line names the relationship instead of repeating it.
func TestFixLineSaysTheSameCommandWorked(t *testing.T) {
	p := index.FixPair{
		Error:    "zsh:1: no matches found: --include=*.go",
		Command:  `grep -rn "quokkabloom" --include="*.go" internal`,
		Failed:   `grep -rn "quokkabloom" --include=*.go internal`,
		Repaired: true,
		Project:  "deja-vu",
		When:     time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
	}
	got := fixLine(p, 3)
	if !strings.Contains(got, "the same command worked as: "+p.Command) {
		t.Errorf("line %q does not say the command is the reader's own, corrected", got)
	}
	if strings.Contains(got, "what followed it") {
		t.Errorf("a repair is offered as the next thing someone happened to run: %q", got)
	}
	p.Failed, p.Repaired = "", false
	if got := fixLine(p, 3); !strings.Contains(got, "what followed it") {
		t.Errorf("an ordinary remedy lost its wording: %q", got)
	}
}

// The two doors onto the fix store answer at different stages on purpose: a
// person asking gets told what deja holds and how sure it is, while the hook
// that interrupts unasked stays quiet until a remedy is confirmed. Nothing
// pinned the ladder, and the asymmetry is easy to lose in either direction.
func TestTheFixLadderSpeaksAtTheRightStage(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	store := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	const wall = "psql: FATAL: sorry, too many clients already"
	addSession := func(id string, hoursAgo int) {
		t.Helper()
		base := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC()
		rows := []string{
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make serve"}}]}}`,
				id, base.Format(time.RFC3339)),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/api","message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`,
				id, base.Add(2*time.Minute).Format(time.RFC3339), wall),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew services restart postgresql"}}]}}`,
				id, base.Add(5*time.Minute).Format(time.RFC3339)),
		}
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	afterHook := func(session string) string {
		t.Helper()
		payload := fmt.Sprintf(`{"hook_event_name":"PostToolUse","tool_name":"Bash",`+
			`"tool_input":{"command":"make serve"},"tool_response":{"stderr":%q},`+
			`"cwd":"/work/api","session_id":%q}`, wall, session)
		var out bytes.Buffer
		if err := runHookToolAfter(dir, strings.NewReader(payload), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	// Stage one: nobody has hit it.
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	asked, err := captureRun(t, "fix", wall)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "no session on this machine ran a command after that error") {
		t.Errorf("stage one, asked: %q", asked)
	}
	if got := afterHook("a1"); strings.TrimSpace(got) != "" {
		t.Errorf("stage one, the hook spoke about an error nobody has hit:\n%s", got)
	}

	// Stage two: one session ran something after it. Held from auto-hooks
	// (Candidate requires 2+ sessions for live hook injection), while explicit
	// `deja fix` still reports it as unconfirmed.
	addSession("one", 6)
	if err = index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	asked, err = captureRun(t, "fix", wall)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "unconfirmed") {
		t.Errorf("stage two, asked: %q", asked)
	}
	for strike := 1; strike <= 2; strike++ {
		if got := afterHook("a2"); strings.TrimSpace(got) != "" {
			t.Errorf("stage two strike %d, candidate leaked into auto-hook:\n%s", strike, got)
		}
	}

	// Stage three: a second session did the same. Explicit `deja fix` answers,
	// and the auto-hook holds back on Strike 1 then delivers on Strike 2.
	addSession("two", 3)
	if err = index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	asked, err = captureRun(t, "fix", wall)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "brew services restart postgresql") {
		t.Errorf("stage three, asked: %q", asked)
	}
	if got := afterHook("a3"); strings.TrimSpace(got) != "" {
		t.Errorf("stage three strike 1, hook spoke on first non-detEnv failure:\n%s", got)
	}
	if got := afterHook("a3"); !strings.Contains(got, "brew services restart postgresql") {
		t.Errorf("stage three strike 2, the hook stayed quiet about a confirmed remedy:\n%s", got)
	}
}

// A failure and its fix inside a subagent's run, deep enough that the cut keeps
// neither as part of the task or the closing turns. `deja fix` names the fix:
// the cut used to drop every command a child ran, so most failure-and-fix
// episodes on a machine that delegates its edits were never mined (#5678).
func TestFixNamesARemedyASubagentRan(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	sub := filepath.Join(tmp, "claude", "-work-img", "parent-1", "subagents")
	for _, d := range []string{home, sub} {
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
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")

	refusal := "ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)\nERROR: No matching distribution found for vllm==0.6.3"
	mirror := "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3"
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	row := func(typ, content string) string {
		return fmt.Sprintf(`{"type":%q,"sessionId":"parent-1","isSidechain":true,"agentId":"child-1","timestamp":%q,"cwd":"/work/img","message":{"role":%q,"content":%s}}`, typ, at, typ, content)
	}
	bash := func(id, cmd, out string, failed bool) []string {
		return []string{
			row("assistant", fmt.Sprintf(`[{"type":"text","text":"running it"},{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]`, id, cmd)),
			row("user", fmt.Sprintf(`[{"type":"tool_result","tool_use_id":%q,"is_error":%t,"content":%q}]`, id, failed, out)),
		}
	}
	rows := make([]string, 0, 22)
	rows = append(rows, row("user", `"build the vllm image"`))
	for i := range 4 {
		rows = append(rows, bash(fmt.Sprintf("g%d", i), fmt.Sprintf("git log --oneline -1 part%d", i), "abc123 part", false)...)
	}
	rows = append(rows, bash("t1", "pip3 install vllm==0.6.3", refusal, true)...)
	rows = append(rows, bash("t2", mirror, "Successfully installed vllm-0.6.3", false)...)
	for i := range 4 {
		rows = append(rows, bash(fmt.Sprintf("s%d", i), fmt.Sprintf("git status part%d", i), "clean", false)...)
	}
	rows = append(rows, row("assistant", `"the image builds; vllm comes from the mirror"`))
	if err := os.WriteFile(filepath.Join(sub, "agent-child-1.jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", "ERROR: No matching distribution found for vllm==0.6.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, mirror) {
		t.Errorf("fix does not name the mirror install the subagent ran:\n%s", out)
	}
}

// Two sessions answered the same error with a force push. deja never hands
// that over as a fix, and then said "no session on this machine ran a command
// after that error" — false, the store holds the pair.
func TestFixSaysTheRemedyWasWithheldRatherThanThatNothingRan(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	fail := "error: failed to push some refs to 'origin' (non-fast-forward snorbleref)"
	for i, id := range []string{"w1", "w2"} {
		at := time.Now().Add(-time.Duration(5-i) * time.Hour).UTC().Format(time.RFC3339)
		rows := []string{
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":"/work/app","message":{"role":"user","content":"push it"}}`, id, at),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"git push origin main"}}]}}`, id, at),
			fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`, id, at, fail),
			fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"git push -f origin main"}}]}}`, id, at),
		}
		if err := os.WriteFile(filepath.Join(claude, id+".jsonl"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := index.Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "fix", fail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "git push -f") {
		t.Fatalf("fix handed over the force push: %q", out)
	}
	if strings.Contains(out, "no session on this machine ran a command") {
		t.Errorf("fix denied the pair it withheld: %q", out)
	}
	if !strings.Contains(out, "does not hand those over") {
		t.Errorf("fix does not say the remedy was withheld: %q", out)
	}
}
