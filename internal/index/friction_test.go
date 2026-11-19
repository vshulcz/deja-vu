package index

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A wall the manifest remembers but the store can no longer show must not
// spend a slot in the answer. `meta.Hit` is a union that never forgets — the
// comment on extendDerived says so on purpose — while the text is recovered by
// reading the records back, so a transcript that was rewritten (a compact, a
// harness that rewrites its rows) leaves hashes nothing yields. TopFriction cut
// to n first and dropped those after, so `FindFriction`, which asks for one,
// came back empty with a second wall standing right behind it (#2544).
func TestAWallWithNoTextLeftDoesNotSpendTheSlot(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-tmp-friction")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two walls in every session: the loud one is hit by four sessions, the
	// quiet one by three, so the loud one ranks first.
	write := func(i int, loud bool) {
		sid := fmt.Sprintf("fr%02d", i)
		day := fmt.Sprintf("2026-01-%02d", i+1)
		out := func(at, text string) string {
			return `{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"` + day + `T` + at +
				`Z","message":{"role":"user","content":[{"type":"tool_result","content":"` + text + `"}]}}`
		}
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"` + day +
				`T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
			out("03:06:05", "(eval):1: command not found: quokkactl"),
		}
		if loud {
			lines = append(lines, out("03:05:05", "undefined: snorblefunc in vendor/blarg/api.go"))
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 4 {
		write(i, true)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: the loud wall is what the one-wall surface shows.
	if f, ok := FindFriction(dir, nil); !ok || f.Text != "undefined: snorblefunc in vendor/blarg/api.go" {
		t.Fatalf("before the rewrite: ok=%v text=%q", ok, f.Text)
	}

	// A hash the manifest holds and the records do not: on this machine's index
	// 25 sessions carry one, and the two clusters they build are the two the
	// listing loses. How they got there is a separate question — `meta.Hit` is
	// a union that never forgets, and transcripts are rewritten under it — but
	// the state is real, and the ranking has to survive it.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	ghost := frictionHash("undefined: quenchfunc in vendor/blarg/api.go")
	for key, meta := range m.Sessions {
		meta.Hit = append(meta.Hit, ghost)
		m.Sessions[key] = meta
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	f, ok := FindFriction(dir, nil)
	if !ok {
		t.Fatalf("the wall that is still in the store was not reported at all")
	}
	if f.Text != "undefined: snorblefunc in vendor/blarg/api.go" {
		t.Fatalf("text = %q", f.Text)
	}
	// And asking for two comes back with both real walls rather than one.
	if got := TopFriction(dir, 2, nil); len(got) != 2 {
		t.Errorf("TopFriction(2) returned %d walls, want the two the store can show", len(got))
	}
}

// A host and a port inside an error make every occurrence of one failure a
// different wall: friction never reaches its floor, no fix pair is confirmed
// twice, and search's error tier matches nothing (#2369).
func TestOneErrorIsOneWallAcrossHostsAndPorts(t *testing.T) {
	first, ok := FrictionLine("dial tcp 192.0.2.7:5432: connect: connection refused")
	if !ok {
		t.Fatal("the line is not friction at all, so this measures nothing")
	}
	second, ok := FrictionLine("dial tcp 192.0.2.9:5433: connect: connection refused")
	if !ok {
		t.Fatal("the second line is not friction")
	}
	if frictionHash(first) != frictionHash(second) {
		t.Errorf("one service being down is two walls:\n  %q\n  %q", first, second)
	}
}

// The masking stops where the digits carry the meaning: an exit code says which
// failure this is, and two of them are two failures.
func TestShortCodesKeepTheirIdentity(t *testing.T) {
	a, aok := FrictionLine("make: *** [build] Error 1")
	b, bok := FrictionLine("make: *** [build] Error 2")
	if !aok || !bok {
		t.Fatal("the control lines are not friction")
	}
	if frictionHash(a) == frictionHash(b) {
		t.Errorf("two different failures collapsed into one wall:\n  %q\n  %q", a, b)
	}
}

// CodeBuddy reports a shell call as `Command: …`, `Stdout: …`, `Stderr: …`,
// `Exit Code: N`, and Qwen Code as `Command: …`, `Output: …`, `Error: …`. The
// first line of what the command printed shares a line with the label, so the
// error was stored as `Stdout: fatal: …`: a fix learned in CodeBuddy answered
// no other harness, and `deja fix` with the error as printed found nothing.
func TestAShellEnvelopeLabelIsNotPartOfTheError(t *testing.T) {
	for _, c := range []struct{ wrapped, bare string }{
		{"Stdout: fatal: unrecognized argument: --onelin", "fatal: unrecognized argument: --onelin"},
		{"Stderr: ModuleNotFoundError: No module named 'app.db'", "ModuleNotFoundError: No module named 'app.db'"},
		{"Output: fatal: your current branch 'master' does not have any commits yet", "fatal: your current branch 'master' does not have any commits yet"},
		{"Stderr: zsh:1: command not found: timeout", "zsh:1: command not found: timeout"},
	} {
		wl, wsig, wok := FrictionSignature(c.wrapped)
		bl, bsig, bok := FrictionSignature(c.bare)
		if !bok {
			t.Fatalf("control: %q is not read as friction", c.bare)
		}
		if !wok || wsig != bsig || wl != bl {
			t.Errorf("%q read as %q (%v), want %q like the bare line", c.wrapped, wl, wok, bl)
		}
	}
	// Only a label at the very start, and only these three: an error that
	// names its own output keeps its words.
	for _, keep := range []string{
		"Error: cannot find module 'express'",
		"panic: Output: buffer closed while writing",
		"Stdout:fatal: no space after the colon is not the label",
	} {
		if got := normalizeFriction(keep); got != keep {
			t.Errorf("normalizeFriction(%q) = %q, want it unchanged", keep, got)
		}
	}
}

// The bare-prefix rule drops a line for opening with "Error: ", because that
// alone says nothing. It ran before the phrase list, so it also dropped the
// lines that go on to say something the list knows — a missing module, a
// refused connection — and those are the ones an agent hits twice (#2432).
func TestAGenericPrefixWithSomethingBehindIt(t *testing.T) {
	said := []string{
		`Error: Cannot find module ./config`,
		`error: cannot find package "example.com/x" in any of the module caches`,
		`Error: connection refused while dialing the pool`,
		`error: permission denied while trying to connect to the Docker daemon socket`,
	}
	for _, l := range said {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a line naming a wall the list knows was dropped for its prefix:\n  %s", l)
		}
	}

	// A prefix with nothing behind it is still nothing: these say only that
	// something failed, which every run says differently.
	bare := []string{
		`Error: 1`,
		`error: exit status 2`,
		`Error: something went wrong, see above`,
	}
	for _, l := range bare {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line that names nothing was read as a wall:\n  %s", l)
		}
	}
}

// Three walls this machine hits that no phrase recognised: the two ways the go
// toolchain says there is no module here, and sqlite saying a table is not
// there. Counted in the real stores: the go.mod line 17 times, the pattern
// line 12, the sqlite one 4 — and none of them was ever mined, so `deja fix`
// answered nothing for the sessions that hit them (#3373).
func TestFrictionReadsTheGoModuleAndSqliteWalls(t *testing.T) {
	walls := []string{
		"go: go.mod file not found in current directory or any parent directory; see 'go help modules'",
		"pattern ./...: directory prefix . does not contain main module or its selected dependencies",
		"Error: in prepare, no such table: part",
	}
	for _, w := range walls {
		if _, ok := FrictionLine(w); !ok {
			t.Errorf("not read as friction: %q", w)
		}
	}
	// And the source that talks about them is not a wall: a test asserting on
	// the text, a comment, a line of Go quoting it.
	notWalls := []string{
		`	if !strings.Contains(got[0], "does not contain main module") {`,
		`// go.mod file not found is what the toolchain says when there is no module`,
		`echo "no such table: part"`,
	}
	for _, s := range notWalls {
		if _, ok := FrictionLine(s); ok {
			t.Errorf("source read as friction: %q", s)
		}
	}
}

// "no such table" is what sqlite says and also what a person writes about a
// specification. The server's own shape is the difference, the same guard
// "does not exist" has carried since #2431 (review of #3373).
func TestNoSuchTableNeedsTheServersOwnShape(t *testing.T) {
	// Every runtime puts its marker somewhere different, and one of them puts
	// none at all — what they share is naming the table (second review).
	for _, wall := range []string{
		"Error: in prepare, no such table: part",
		"D1_ERROR: no such table: users",
		"sqlite3.OperationalError: no such table: users",
		"Parse error: no such table: x",
		"no such table: users",
		"SQL logic error: no such table: main.sessions",
	} {
		if _, ok := FrictionLine(wall); !ok {
			t.Errorf("not read as friction: %q", wall)
		}
	}
	for _, prose := range []string{
		"there is no such table in the spec, so I improvised one",
		"we have no such table yet — add a migration",
		"Error handling for missing tables is not written yet, so no such table checks run",
		// A sentence can end a clause on a colon too, and what follows is not
		// a table name (third review).
		"I checked and there is no such table: the schema only has runs",
		"the docs say: no such table exists for this mapping",
	} {
		if _, ok := FrictionLine(prose); ok {
			t.Errorf("a sentence about tables is read as an error: %q", prose)
		}
	}
}

// frictionIgnoreStore gives the same wall to three servable sessions and two the
// ignore rule keeps out.
func frictionIgnoreStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(project, id string) {
		p := filepath.Join(root, "-w-"+project, id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling"}}` + "\n" +
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/` + project + `","timestamp":"2026-07-01T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"make: *** [widget] Error 2"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		write("keep", fmt.Sprintf("k%d", i))
	}
	for i := range 2 {
		write("scratch", fmt.Sprintf("s%d", i))
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TopFriction gates on the caller's allow(project), which is the trust policy.
// The ignore rule was not applied and cannot be applied by the callers — allow
// takes a project and the rule matches on the path — so the brief said five
// sessions where `deja friction` said three, and the session-start block
// counted the same two (#2658).
func TestTopFrictionLeavesOutTheIgnoredTree(t *testing.T) {
	dir := frictionIgnoreStore(t)
	walls := TopFriction(dir, 5, nil)
	if len(walls) != 1 {
		t.Fatalf("one wall is shared by every session here, got %d", len(walls))
	}
	if n := len(walls[0].Sessions); n != 3 {
		t.Fatalf("three sessions recall may serve hit that wall, the wall claims %d", n)
	}
	for _, s := range walls[0].Sessions {
		if s.Project == "w/scratch" {
			t.Fatalf("a session the ignore rule keeps out is evidence for the wall: %+v", s)
		}
	}
}

// A wall that only the ignored tree hits is not this machine's friction at all.
func TestTopFrictionDropsAWallOnlyTheIgnoredTreeHits(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	policyFile := filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		id := fmt.Sprintf("s%d", i)
		p := filepath.Join(root, "-w-scratch", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"deploying"}}` + "\n" +
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"make: *** [widget] Error 2"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if walls := TopFriction(dir, 5, nil); len(walls) != 0 {
		t.Fatalf("a wall only the ignored tree hits is not this machine's friction: %+v", walls)
	}
}

// A package install behind a mirror or a proxy fails in a handful of shapes,
// and none of them was a wall: `deja fix` had nothing for the session that
// found the mirror that works (#5599).
func TestThePackageInstallWallsAreWalls(t *testing.T) {
	for _, l := range []string{
		"ERROR: No matching distribution found for vllm==0.6.3",
		"ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)",
		"ERROR: HTTP error 403 while getting https://files.pythonhosted.org/packages/x.whl",
		"curl: (6) Could not resolve host: pypi.org",
		"[SSL: CERTIFICATE_VERIFY_FAILED] certificate verify failed: unable to get local issuer certificate",
		`ERROR: failed to solve: process "/bin/sh -c pip install vllm" did not complete successfully: exit code: 1`,
		"go: example.com/x@v1.2.3: reading https://proxy.golang.org/example.com/x/@v/v1.2.3.mod: 403 Forbidden",
		"dial tcp: lookup proxy.golang.org: no such host",
		"x509: certificate signed by unknown authority",
		"npm error 403 403 Forbidden - GET https://registry.npmjs.org/left-pad",
		"npm error code ECONNREFUSED",
		"error: externally-managed-environment",
		"Temporary failure in name resolution",
		"Error response from daemon: manifest unknown",
		"Error response from daemon: pull access denied for acme/api, repository does not exist",
		"Received HTTP code 407 Proxy Authentication Required from proxy after CONNECT",
	} {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("not recognised as a wall: %q", l)
		}
	}
}

// The same words in a sentence are not the tool saying them.
func TestASentenceAboutAnInstallErrorIsNotAWall(t *testing.T) {
	for _, l := range []string{
		"the mirror returns 403 Forbidden for packages that are not approved",
		"if the host has no such host entry, add it to /etc/hosts first",
		"the build did not complete successfully because of the proxy",
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("read as a wall: %q", l)
		}
	}
}

// The remedy is the same install pointed at the mirror, and it counts the
// first time it is seen: it is the failing command corrected.
func TestAMirrorInstallAnswersTheIndexThatRefused(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "pip3 install vllm==0.6.3", Time: now},
		{Role: "tool-output", Text: "ERROR: Could not find a version that satisfies the requirement vllm==0.6.3 (from versions: none)\nERROR: No matching distribution found for vllm==0.6.3", Time: now.Add(time.Second)},
		{Role: "command", Text: "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "Successfully installed vllm-0.6.3", Time: now.Add(2 * time.Minute)},
	}
	pairs := judgeFixes(fixPairsIn(ms, "claude:s1", "p"))
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %+v", pairs)
	}
	p := pairs[0]
	if p.Candidate {
		t.Errorf("the mirror install waits for a second session: %+v", p)
	}
	if p.Command != "pip3 install --index-url https://mirror.example.internal/simple vllm==0.6.3" {
		t.Errorf("remedy = %q", p.Command)
	}
}

// Source that carries an error string is a line about an error, not one. The
// bare-quote rule used to catch this and cost far more than it caught (#2430);
// what is left is the shapes source has around the quote — an assignment, a
// call, a struct field, a code span — which tool output does not (#2436).
func TestASourceLineCarryingAnErrorIsNotAWall(t *testing.T) {
	source := []string{
		`in := "the build failed: pgbouncer pool timed out and retried"`,
		`t.Fatalf("compressed dump not found: %d sessions", len(ss))`,
		`{Role: "tool-output", Text: "psql: connection refused on port 5432"},`,
		"`Error: Cannot find module ./config`,",
		`want = "fatal: repository not found, try again"`,
	}
	for _, l := range source {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a source line quoting an error was counted as one:\n  %s", l)
		}
	}

	// And the errors themselves, which quote what they could not find.
	output := []string{
		`psql: error: connection to server at "localhost", port 5432 failed: Connection refused`,
		`fatal: repository "https://example.invalid/x.git" not found`,
		`ERROR:  relation "orders" does not exist`,
		`curl: (7) Failed to connect to localhost port 5432 after 0 ms`,
		`docker: Error response from daemon: pull access denied for "acme/api"`,
	}
	for _, l := range output {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("an error was mistaken for source:\n  %s", l)
		}
	}
}

// The line bound was set to what a person can recognise, and real errors carry
// paths: a go build error naming a module and a file, a docker pull naming a
// registry, a k8s message naming a namespace and a pod. Measured over the store
// on this machine, the 120-character cap dropped 2,319 lines that name a wall
// the list knows — a third again of the 7,053 it recognised (#2438).
func TestALongErrorIsStillAWall(t *testing.T) {
	walls := []string{
		`/Users/someone/code/service/internal/handler/orders.go:118:24: undefined: repository.FindOrderByExternalReference in the orders package`,
		`docker: Error response from daemon: pull access denied for registry.example.internal/team/service, repository does not exist or may require authorisation`,
		`psql: error: connection to server at "db.internal.example.com" (198.51.100.7), port 5432 failed: Connection refused — is the server running there?`,
	}
	for _, l := range walls {
		// Longer than the bound these were dropped by, so each case still
		// stands for the errors that carry a path.
		if n := len([]rune(l)); n <= 120 {
			t.Fatalf("this case no longer tests the bound it was written for: %d runes", n)
		}
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a wall was dropped for its length (%d runes):\n  %s", len([]rune(l)), l)
		}
	}

	// A bound there still is: a whole page of output pasted as one line is not
	// a wall anyone can recognise, and hashing it would make a wall of its own
	// every run.
	if _, ok := FrictionLine("connection refused " + strings.Repeat("x", 400)); ok {
		t.Errorf("a line with no bound at all was counted as a wall")
	}
}

// The list was nine phrases about things not being found or permitted, and it
// matched 3 of 12 ordinary errors — missing runtime panics, database timeouts,
// auth failures and build failures. One miss was capitalisation alone: curl
// writes "Connection refused" (#729).
func TestIsFrictionCoversOrdinaryErrors(t *testing.T) {
	walls := []string{
		"bash: pytest: command not found",
		"go: module github.com/x/y: no such file or directory",
		"open /etc/secret: permission denied",
		"panic: runtime error: index out of range [5] with length 3",
		"ERROR: canceling statement due to statement timeout",
		"fatal: could not read Username for 'https://github.com'",
		"npm ERR! code ELIFECYCLE",
		"make: *** [build] Error 2",
		"curl: (7) Failed to connect to localhost port 5432: Connection refused",
		"TypeError: Cannot read properties of undefined (reading 'id')",
	}
	for _, l := range walls {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("missed a wall: %q", l)
		}
	}

	// Source is not an error, however much it talks about one — a comment
	// mentioning panic became the top wall once the marker list widened.
	notWalls := []string{
		`echo "App not found: $APP"`,
		`if [ ! -f x ]; then echo "cannot find it"; fi`,
		`print("permission denied")`,
		"// panic: this is a comment about panics",
		"# connection refused happens when the port is closed",
		"   * panic: in a doc comment",
		"    // panic: an indented comment",
		"-- no such file or directory (sql comment)",
		"  3 sessions  bash: pytest: command not found",
	}
	for _, l := range notWalls {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("treated source as a wall: %q", l)
		}
	}
}

// The friction check as it was before the phrase matcher (#5586), kept as
// the oracle the new one has to agree with line for line.

func legacyIsFriction(l string) bool {
	if n := utf8.RuneCountInString(l); n < frictionLineMin || n > frictionLineMax {
		return false
	}
	if legacyNamedTestFailure(l) {
		return true
	}
	if looksLikeSource(l) {
		return false
	}
	if isDejaOwnReport(l) {
		return false
	}
	low := strings.ToLower(l)
	for _, p := range []string{
		"command not found", "modulenotfounderror", "no module named",
		"not found: ", "cannot find", "no such file or directory",
		"undefined:", "connection refused", "permission denied",
		"panic:", "segmentation fault", "exception in thread",
		"nullpointerexception", "index out of range", "stack overflow",
		"typeerror:", "referenceerror:", "keyerror:", "attributeerror:",
		"fatal:", "npm err!", "rpc failed", "timeout expired",
		"statement timeout", "connection reset", "connection timed out",
		"] error ", "build failed", "compilation failed", "cannot be resolved",
		"duplicate key value", "deadlock detected", "access denied for",
		"failed to connect to", "cannot import name", "symbol(s) not found",
		"undefined symbol:", "undefined reference to",
		"failed to push some refs", "acquiring the state lock",
		"no space left on device",
		"go.mod file not found", "does not contain main module",
		"timed out after",
		"tool_use_error", "inputvalidationerror",
		"has been modified since read", "no such tool available",
		"jsondecodeerror", "valueerror:", "runtimeerror:", "oserror:",
		"filenotfounderror:", "importerror:", "unicodedecodeerror:",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	if strings.HasPrefix(low, "error") && strings.Contains(low, "does not exist") {
		return true
	}
	if noSuchTableRE.MatchString(low) &&
		(strings.HasPrefix(low, "no such table:") || strings.Contains(low, "error")) {
		return true
	}
	return false
}
func legacyTrimTimestamp(l string) string {
	if m := leadingTimestamp.FindString(l); m != "" {
		return strings.TrimSpace(l[len(m):])
	}
	return l
}
func legacyNamedTestFailure(l string) bool {
	m := goTestFailure.FindStringSubmatch(l)
	if m == nil {
		return false
	}
	// Source that quotes the shape is not a failure — the same reason the
	// quote and comment guards below exist.
	if strings.ContainsAny(l, "\"$") {
		return false
	}
	return len(m[1]) > 3
}

func frictionTestLines() []string {
	lines := []string{ //nolint:prealloc // a fixture literal the loop below grows
		"",
		"zsh:1: command not found: timeout",
		"bash: line 3: COMMAND NOT FOUND: whatever-tool",
		"Error: Cannot find module './config' from app.js",
		"ERROR:  relation \"orders\" does not exist at character 15",
		"the orders table does not exist yet, create it first",
		"Error: no such table: part (code 1) while syncing",
		"no such table: part",
		"there is no such table: the schema only has runs, really",
		"sqlite3.OperationalError: no such table: users found here",
		"--- FAIL: TestSpillWorkersAllocate (0.02s)",
		"--- FAIL: TestX (0.00s)",
		"--- SKIP: TestSomethingLong because it is slow",
		"    --- FAIL: TestIndented (0.01s)",
		"echo \"App not found: $APP\" >&2 # in the deploy script",
		"dial tcp 127.0.0.1:5432: connect: Connection Refused by peer",
		"panic: runtime error: index out of range [3] with length 3",
		"npm ERR! code ERESOLVE unable to resolve dependency tree",
		"[ERROR] Failed to execute goal on project app: compilation failed",
		"TypeError: Cannot read properties of undefined (reading 'x')",
		"KEYERROR: something went wrong with the Key here",
		"Ошибка: команда не найдена: timeout, command not found",
		"İmport error: CANNOT İMPORT NAME 'x' from 'y' module here",
		"json.decoder.JSONDecodeError: Expecting value: line 1 column 1",
		"2026-10-22T12:00:00Z ERROR connection reset by peer while reading",
		"[2026-10-22 12:00:00] fatal: not a git repository (or any parent)",
		"E   ModuleNotFoundError: No module named 'requests' here",
		"deja: fix · 2026-10-01",
		strings.Repeat("x", 199) + " permission denied",
		"a permission denied b " + strings.Repeat("я", 180),
	}
	r := rand.New(rand.NewSource(7)) //nolint:gosec // a seeded fixture, not crypto
	words := []string{"error", "Error", "ERROR", "not", "found", "no", "such", "table:", "does", "exist",
		"command", "Permission", "denied", "panic:", "--- FAIL:", "TestFoo", "echo", "\"", "$(", "K",
		"ё", "ß", "x", " ", "  ", ":", "1", "2026-10-22", "[", "]", "fatal:", "cannot", "find", "timed",
		"out", "after", "symbol(s)", "\xff", "İ", "undefined:", "npm", "err!", "E "}
	for range 20000 {
		var b strings.Builder
		for n := r.Intn(30); n > 0; n-- {
			b.WriteString(words[r.Intn(len(words))])
			if r.Intn(3) > 0 {
				b.WriteByte(' ')
			}
		}
		lines = append(lines, b.String())
	}
	return lines
}

// One pass over a line has to find what seventy strings.Contains on its
// lowered copy found, capitals and non-ASCII lines included.
func TestFrictionMatcherAgreesWithThePhraseList(t *testing.T) {
	for _, l := range frictionTestLines() {
		if got, want := isFriction(l), legacyIsFriction(l); got != want {
			t.Fatalf("isFriction(%q) = %v, want %v", l, got, want)
		}
		if got, want := trimTimestamp(l), legacyTrimTimestamp(l); got != want {
			t.Fatalf("trimTimestamp(%q) = %q, want %q", l, got, want)
		}
		if got, want := namedTestFailure(l), legacyNamedTestFailure(l); got != want {
			t.Fatalf("namedTestFailure(%q) = %v, want %v", l, got, want)
		}
	}
	// Every phrase, in any case, is a hit on its own.
	for _, p := range frictionPhrases {
		l := "ab " + strings.ToUpper(p) + " cd " + strings.Repeat("z", 20)
		if frictionPhrasesIn(l)&hitPhrase == 0 || !utf8.ValidString(l) {
			t.Errorf("%q is not found in %q", p, l)
		}
	}
}

// A line with no phrase in it costs no allocation now: lowering a copy of
// every line of tool output was most of what isFriction allocated (#5586).
func TestAnOrdinaryLineIsCheckedWithoutAllocating(t *testing.T) {
	l := "Compiling github.com/example/app/internal/server v0.0.0 (Took 1.2s)"
	if n := testing.AllocsPerRun(100, func() { _ = isFriction(l) }); n != 0 {
		t.Fatalf("isFriction allocated %v times for a line with no phrase", n)
	}
}

// The friction rules admit more lines than they did (#2430 through #2438), so
// what protects the answer is that a wall is counted by sessions rather than by
// occurrences. One session that fails four hundred times, and thirty ways once
// each, must not push out the error three separate sessions actually hit
// (#2446).
func TestOneNoisySessionDoesNotCrowdOutARealWall(t *testing.T) {
	now := time.Now()
	out := func(text string, at time.Time) model.Message {
		return model.Message{Role: roleToolOutput, Text: text, Time: at}
	}
	wall := "psql: error: connection to server at db-a, port 5432 failed: Connection refused"
	repeated := "worker.py:41: ModuleNotFoundError: No module named 'aiokafka'"
	// The runner's own prefix is stripped before a line is counted (#1637), so
	// the key a count lands under is the line as deja records it.
	repeatedKey, ok := FrictionLine(repeated)
	if !ok {
		t.Fatalf("the fixture is not friction at all: %s", repeated)
	}

	sessions := make([]model.Session, 0, 4)
	for k := range 3 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("real%d", k), Harness: "claude", Project: "work/app",
			Updated:  now.Add(-time.Duration(k) * time.Hour),
			Messages: []model.Message{out(wall, now.Add(-time.Duration(k)*time.Hour))},
		})
	}
	noisy := model.Session{ID: "noisy", Harness: "claude", Project: "work/app", Updated: now}
	for range 400 {
		noisy.Messages = append(noisy.Messages, out(repeated, now))
	}
	for i := range 30 {
		noisy.Messages = append(noisy.Messages,
			out(fmt.Sprintf("handler_%d.go:11:2: undefined: repository.Missing%d in the orders package", i, i), now))
	}
	sessions = append(sessions, noisy)

	counts := map[string]int{}
	for _, s := range sessions {
		seen := map[string]bool{}
		for _, m := range s.Messages {
			line, ok := FrictionLine(m.Text)
			if !ok || seen[line] {
				continue
			}
			seen[line] = true
			counts[line]++
		}
	}
	if got := counts[wall]; got != 3 {
		t.Errorf("the wall three sessions hit counts %d", got)
	}
	if got := counts[repeatedKey]; got != 1 {
		t.Errorf("four hundred failures in one session count %d, not once", got)
	}
	for line, n := range counts {
		if n >= FrictionMinSessions && line != wall {
			t.Errorf("a wall from one session reached the threshold: %q (%d)", line, n)
		}
	}
}

// Each wall's text is recovered from its newest session, and those are
// different sessions. One pass over the record log has to serve all of them:
// a pass per wall walked a 200 MB log six times on a session start.
func TestTopFrictionReadsTheLogOnceForSeveralWalls(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	walls := []string{
		"make: *** [widget] Error 2",
		"zsh:1: command not found: frobnicate",
		"fatal: refusing to merge unrelated histories",
	}
	for w, wall := range walls {
		for i := range 3 {
			id := fmt.Sprintf("w%ds%d", w, i)
			// Minutes apart so every wall has its own newest session.
			at := fmt.Sprintf("2026-07-01T1%d:%02d:00Z", w, i*10)
			body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":"the build keeps stopping"}}` + "\n" +
				`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","content":"` + wall + `"}]}}` + "\n"
			p := filepath.Join(root, "-w-app", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// Without frictext.bin, which answers with no pass at all.
	frictionTextsOff = true
	before := RecordLogScans()
	got := TopFriction(dir, 0, nil)
	frictionTextsOff = false
	if scans := RecordLogScans() - before; scans != 1 {
		t.Fatalf("recovering %d walls took %d passes over the record log, want 1", len(got), scans)
	}
	before = RecordLogScans()
	held := TopFriction(dir, 0, nil)
	if scans := RecordLogScans() - before; scans != 0 || fmt.Sprint(held) != fmt.Sprint(got) {
		t.Fatalf("with frictext.bin: %d passes and %+v, want 0 and %+v", scans, held, got)
	}
	if len(got) != len(walls) {
		t.Fatalf("got %d walls, want %d: %+v", len(got), len(walls), got)
	}
	seen := map[string]bool{}
	for _, f := range got {
		seen[f.Text] = true
	}
	for _, wall := range walls {
		line, _ := FrictionLine(wall)
		if !seen[line] {
			t.Fatalf("wall %q missing from %+v", line, got)
		}
	}
}

// The batched read hands back what reading the session on its own did.
func TestFrictionReaderMatchesASessionRead(t *testing.T) {
	dir := frictionIgnoreStore(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	rd := newFrictionReader(dir, m)
	for _, meta := range m.Sessions {
		rd.ahead = append(rd.ahead, meta)
	}
	for _, meta := range m.Sessions {
		got, ok := rd.toolOutput(meta)
		want, wok := newFrictionReader(dir, m).loadOne(meta)
		if ok != wok || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: batched %v %q, alone %v %q", meta.ID, ok, got, wok, want)
		}
	}
}

// The phrase list is what turns a line of tool output into a wall deja can
// count, name in the environment block and answer with `deja fix`. Measured
// against errors an agent actually hits, it recognised 5 of 24 — the missing
// ones are databases refusing, registries refusing, servers refusing, and
// builds that could not resolve something (#2434).
func TestTheWallsAnAgentActuallyHits(t *testing.T) {
	walls := []string{
		`ERROR:  relation "orders" does not exist`,
		`ERROR:  duplicate key value violates unique constraint "orders_pkey"`,
		`ERROR:  deadlock detected`,
		`Error 1045: Access denied for user 'app'@'localhost'`,
		`docker: Error response from daemon: pull access denied for acme/api`,
		`curl: (7) Failed to connect to localhost port 5432 after 0 ms`,
		`ImportError: cannot import name settings from app`,
		`ld: symbol(s) not found for architecture arm64`,
		`ld: undefined symbol: pthread_setname_np`,
		`ld.lld: error: undefined symbol: pthread_setname_np`,
		`/usr/bin/ld: server.c:(.text+0x1): undefined reference to 'pthread_setname_np'`,
		`error: failed to push some refs to origin`,
		`Error acquiring the state lock: ConditionalCheckFailedException`,
		`fatal: unable to access https://example.invalid/: Could not resolve host`,
		`write /tmp/build: no space left on device`,
	}
	for _, l := range walls {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a wall deja will meet again is not friction:\n  %s", l)
		}
	}

	// Prose is not a wall. These are sentences a person or an agent writes
	// about the same subjects, and counting them would fill `deja friction`
	// with things nobody tripped over.
	prose := []string{
		`the orders table does not exist yet, we create it in the migration`,
		`access denied is what you get without the role, so add it first`,
		`I could not connect the two ideas in that paragraph, rewriting it`,
		`we should push some refs to origin once the tests are green`,
		`the plan is to resolve the host name from the config instead`,
		`this causes an undefined symbol when linking the cgo archive`,
		`I think this may be an undefined symbol problem in the build`,
	}
	for _, l := range prose {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a sentence about a wall was counted as one:\n  %s", l)
		}
	}

	// A line that names a linker error is still source when it is a script,
	// a comment, or a quoted fixture — the same shapes #2430/#2431 reject.
	about := []string{
		`echo "undefined symbol: pthread_setname_np" >&2`,
		`// ld: undefined symbol: pthread_setname_np was the wall last week`,
		`want = "undefined reference to 'pthread_setname_np'"`,
		`# ld: undefined symbol: pthread_setname_np in the notes`,
	}
	for _, l := range about {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line about a linker error was read as one:\n  %s", l)
		}
	}
}

// dyld's first line is `Symbol not found: _foo` (Apple Loader.cpp). That
// already matches "not found: ", so #1551 does not add a bare
// "symbol not found" phrase.
func TestADyldMissingSymbolIsAlreadyAWall(t *testing.T) {
	// Public abort: https://stackoverflow.com/questions/50247343
	l := `dyld: Symbol not found: __ZdaPvm`
	if _, ok := FrictionLine(l); !ok {
		t.Errorf("dyld's missing-symbol line is not friction:\n  %s", l)
	}
}

// Adding an import moves every error in the file down a line. Keyed by that
// line, the same undefined symbol is a new wall each time — below the second
// sighting a fix pair needs and the three sessions `deja friction` needs, so
// the repair for something this machine hits weekly is never offered.
func TestACompilerErrorIsOneWallWhereverTheLineMoves(t *testing.T) {
	at4, ok := FrictionLine("./main.go:4:2: undefined: glimwraxHelper")
	if !ok {
		t.Fatal("a compile error is not read as friction at all")
	}
	at9, ok := FrictionLine("./main.go:9:2: undefined: glimwraxHelper")
	if !ok {
		t.Fatal("the same error one line down is not read as friction")
	}
	if frictionHash(at4) != frictionHash(at9) {
		t.Errorf("the same error at two lines is two walls:\n  %q\n  %q", at4, at9)
	}

	// The reader still sees where it was: the position is masked for the
	// signature, not taken out of the line.
	if !strings.Contains(at4, "4:2") {
		t.Errorf("the line the reader is shown lost its position: %q", at4)
	}

	// A different symbol is a different wall, and so is a different file.
	other, _ := FrictionLine("./main.go:4:2: undefined: otherHelper")
	if frictionHash(at4) == frictionHash(other) {
		t.Error("two different symbols were folded into one wall")
	}
	elsewhere, _ := FrictionLine("./cmd/run.go:4:2: undefined: glimwraxHelper")
	if frictionHash(at4) == frictionHash(elsewhere) {
		t.Error("the same symbol in two files was folded into one wall")
	}
}

// And the pair survives it: the command that settled the error at one line is
// offered when the same error comes back further down the file.
func TestAFixPairSurvivesTheLineMoving(t *testing.T) {
	now := time.Now()
	ms := []model.Message{
		{Role: "command", Text: "go build ./...", Time: now},
		{Role: "tool-output", Text: "./main.go:4:2: undefined: glimwraxHelper", Time: now},
		{Role: "command", Text: "glimwraxctl --sync && go build ./...", Time: now.Add(time.Minute)},
		{Role: "tool-output", Text: "ok", Time: now.Add(2 * time.Minute)},
	}
	pairs := fixPairsIn(ms, "claude:s1", "p")
	if len(pairs) != 1 {
		t.Fatalf("want one pair, got %d", len(pairs))
	}
	line, ok := FrictionLine("./main.go:9:2: undefined: glimwraxHelper")
	if !ok {
		t.Fatal("the moved error is not friction")
	}
	if pairs[0].Sig != frictionHash(line) {
		t.Error("the repair is filed under a signature the same error one line down does not reach")
	}
}

// normalizeFriction already strips the shell's `zsh:1: ` position prefix so one
// missing command counts once. Two other prefixes are just as mechanical: the
// `E   ` pytest puts in front of the failing line, and the timestamp docker,
// journalctl and CI put in front of everything. Left in, one error counted as
// two walls and `deja fix` missed the paste (#1637).
func TestNormalizeFrictionStripsMechanicalPrefixes(t *testing.T) {
	const want = "ModuleNotFoundError: No module named 'app.db'"
	for _, in := range []string{
		want,
		"E   " + want,
		"E " + want,
		"2026-07-12T10:00:00Z " + want,
		"2026-07-12 10:00:00 " + want,
		"[2026-07-12T10:00:00Z] " + want,
	} {
		if got := normalizeFriction(in); got != want {
			t.Errorf("normalizeFriction(%q) = %q, want %q", in, got, want)
		}
	}
	// The controls: nothing that carries meaning may be trimmed.
	for in, keep := range map[string]string{
		"Error: cannot find module 'express'":                                       "Error: cannot find module 'express'",
		"E2BIG: argument list too long":                                             "E2BIG: argument list too long",
		"Exception: no route to host":                                               "Exception: no route to host",
		"zsh:1: command not found: timeout":                                         "command not found: timeout",
		"npm ERR! code ELIFECYCLE":                                                  "npm ERR! code ELIFECYCLE",
		"EACCES: permission denied, open 'x'":                                       "EACCES: permission denied, open 'x'",
		"E: Unable to locate package golang":                                        "E: Unable to locate package golang",
		"2026 is not a year we support":                                             "2026 is not a year we support",
		"// `(eval):2: command not found: timeout` are one piece of friction; left": "// `(eval):2: command not found: timeout` are one piece of friction; left",
	} {
		if got := normalizeFriction(in); got != keep {
			t.Errorf("normalizeFriction(%q) = %q, want %q", in, got, keep)
		}
	}
	if _, ok := FrictionLine("// `(eval):2: command not found: timeout` are one piece of friction; left"); ok {
		t.Error("a comment quoting a shell error was accepted as friction")
	}
}

// A double quote anywhere in a line dropped it as source code. Tools quote the
// thing they could not find — a relation, a branch, a module, an image — so the
// rule threw away the errors an agent trips over most, while the same error
// without quotes was learned (#2431).
func TestAnErrorThatQuotesWhatItCouldNotFind(t *testing.T) {
	// Both of these are in the phrase list already — "connection refused" and
	// "fatal:" — and were dropped for their quotes alone.
	friction := []string{
		`psql: error: connection to server at "localhost", port 5432 failed: Connection refused`,
		`fatal: repository "https://example.invalid/x.git" not found`,
	}
	for _, l := range friction {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("an error deja will keep seeing is not friction:\n  %s", l)
		}
	}

	// Source is still source: a line that quotes an error is not one.
	source := []string{
		`echo "connection refused" >&2`,
		`  "message": "connection refused",`,
		`// panic: connection refused`,
		`printf "permission denied\n"`,
		`if [[ $out =~ "connection refused" ]]; then`,
	}
	for _, l := range source {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("a line about an error was read as one:\n  %s", l)
		}
	}
}

// The bound on what counts as a wall is about how much of a line a person can
// recognise, and it was counted in bytes — so a Russian error line was held to
// 60 characters and a Chinese one to 40, while English got 120. An
// 89-character Russian line was dropped where an 80-character English one was
// kept, and the wall a Russian-speaking user keeps hitting never reached
// `deja friction`, the environment block or `deja fix` (#1319).
func TestFrictionBoundsCountCharactersNotBytes(t *testing.T) {
	// Carries an English marker, as most real error lines do, so only the
	// length decides.
	for _, l := range []string{
		"ОШИБКА при развёртывании сервиса доставки заказов: connection refused при подключении к очереди повторов на резервном узле",
		"ОШИБКА при развёртывании сервиса доставки заказов и уведомлений: connection refused при подключении к очереди повторов узла",
	} {
		if n := utf8.RuneCountInString(l); n > frictionLineMax {
			t.Fatalf("the fixture is %d characters, past the bound itself", n)
		}
		if len(l) <= frictionLineMax {
			t.Fatalf("the fixture is %d bytes, inside the byte bound, so it proves nothing", len(l))
		}
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("a %d-character line was dropped for being %d bytes: %.40s",
				utf8.RuneCountInString(l), len(l), l)
		}
	}
}

// The bounds still bound: a line too short to name anything, and one past the
// limit in characters, are both out.
func TestFrictionBoundsStillHold(t *testing.T) {
	if _, ok := FrictionLine("panic: x"); ok {
		t.Error("a line under the minimum was accepted")
	}
	long := "connection refused " + strings.Repeat("очень длинное описание ", 12)
	if n := utf8.RuneCountInString(long); n <= frictionLineMax {
		t.Fatalf("the fixture is only %d characters", n)
	}
	if _, ok := FrictionLine(long); ok {
		t.Error("a line past the maximum in characters was accepted")
	}
}

// And English is unchanged, since bytes and characters agree there.
func TestFrictionKeepsEnglishAsItWas(t *testing.T) {
	if _, ok := FrictionLine("ModuleNotFoundError: No module named yaml while building the image"); !ok {
		t.Error("an ordinary English error line stopped counting as a wall")
	}
	if _, ok := FrictionLine("connection refused " + strings.Repeat("x", 200)); ok {
		t.Error("a very long English line was accepted")
	}
}

// The gate decides what gets a friction signature at ingest, so a store built
// before this keeps fewer of them — the bump is what makes the one rebuild
// that re-derives them happen.
func TestTheFrictionBoundRidesOnTheFormatVersion(t *testing.T) {
	if version < 27 {
		t.Errorf("index version is %d — the friction gate changed without the bump that re-derives it", version)
	}
}

// The count behind the after-tool hook's line (#2491): sessions carrying one
// wall's signature, gated by the caller's activation the way TopFriction is.
func TestFrictionSessionsCountsAndScopes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	if n := FrictionSessions(dir, 1, nil); n != 0 {
		t.Errorf("an index that does not exist counted %d sessions", n)
	}

	m := Manifest{Sessions: map[string]SessionMeta{
		"claude:a": {ID: "a", Harness: "claude", Project: "app", Hit: []uint64{7, 9}},
		"claude:b": {ID: "b", Harness: "claude", Project: "app", Hit: []uint64{7}},
		"claude:c": {ID: "c", Harness: "claude", Project: "other", Hit: []uint64{7}},
		"claude:d": {ID: "d", Harness: "claude", Project: "app", Hit: []uint64{9}},
		// One session hitting the same wall twice is still one session.
		"claude:e": {ID: "e", Harness: "claude", Project: "app", Hit: []uint64{7, 7}},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	if n := FrictionSessions(dir, 7, nil); n != 4 {
		t.Errorf("counted %d sessions for the wall, want 4", n)
	}
	if n := FrictionSessions(dir, 7, func(p string) bool { return p == "app" }); n != 3 {
		t.Errorf("counted %d in app, want 3", n)
	}
	if n := FrictionSessions(dir, 42, nil); n != 0 {
		t.Errorf("counted %d for a wall nobody hit", n)
	}

	// An empty dir falls back to the default, which the env var above points
	// at the same place.
	if n := FrictionSessions("", 7, nil); n != 4 {
		t.Errorf("the default dir counted %d, want 4", n)
	}
}

// A settled wall leaves the surfaces that say what is still in the way, and
// stays in TopFriction, which the history surfaces read (#5628).
func TestASettledWallLeavesOpenFrictionOnly(t *testing.T) {
	dir := frictionIgnoreStore(t)
	walls := TopFriction(dir, 5, nil)
	if len(walls) != 1 {
		t.Fatalf("want one wall, got %d", len(walls))
	}
	if walls[0].Hash == 0 || len(WallHandle(walls[0].Hash)) != 8 {
		t.Fatalf("wall has no handle: %+v", walls[0])
	}
	if _, ok := FindFriction(dir, nil); !ok {
		t.Fatal("the wall is not open before any settle")
	}
	if err := policy.SettleWall(WallHandle(walls[0].Hash), time.Now()); err != nil {
		t.Fatal(err)
	}
	if f, ok := FindFriction(dir, nil); ok {
		t.Errorf("a settled wall is still open: %+v", f)
	}
	if len(TopFriction(dir, 5, nil)) != 1 {
		t.Error("settling took the wall out of the history")
	}
}

// The plan hook warns about walls a plan is about to meet; a settled one is
// not something to warn about.
func TestPlanFrictionSkipsASettledWall(t *testing.T) {
	dir := planCorpus(t, 0)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(t.TempDir(), "policy.json"))
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if len(out) != 1 {
		t.Fatalf("findings = %d, want 1", len(out))
	}
	// These sessions carry no start time, so the newest update stands in.
	newest := NewestStart(out[0].Wall.Sessions)
	if newest.IsZero() {
		t.Fatal("no time for sessions that carry only an update")
	}
	if err := policy.SettleWall(WallHandle(out[0].Wall.Hash), newest.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0); len(out) != 0 {
		t.Errorf("a settled wall was reported on a plan: %+v", out)
	}
}

func TestFrictionLineKeepsSpecificErrors(t *testing.T) {
	for _, l := range []string{
		"zsh:1: command not found: shellcheck",
		"ModuleNotFoundError: No module named 'yaml'",
		"internal/index/store.go:41:2: undefined: signalLines",
		"dial tcp 127.0.0.1:5432: connect: connection refused",
	} {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("dropped a specific error: %q", l)
		}
	}
	for _, l := range []string{
		"Traceback (most recent call last):",
		"Error: exit status 1",
		"--- FAIL: TestThing (0.01s)",
		"not found",                          // too short to name anything
		`echo "❌ App not found: $APP"`,       // source, not a result
		`  9 sessions  command not found: x`, // this command's own output
		"ok  github.com/vshulcz/deja-vu/internal/index  1.2s",
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("kept a line that names nothing: %q", l)
		}
	}
}

// The same missing command reaches the corpus under three shell prefixes. Left
// unnormalized each lands below the threshold and none of them is ever
// reported, which is the bug this function exists for.
func TestFrictionLineNormalizesShellPosition(t *testing.T) {
	want := "command not found: timeout"
	for _, l := range []string{
		"zsh:1: command not found: timeout",
		"(eval):2: command not found: timeout",
		"  bash:15: command not found: timeout  ",
	} {
		got, ok := FrictionLine(l)
		if !ok || got != want {
			t.Errorf("FrictionLine(%q) = %q, %v; want %q, true", l, got, ok, want)
		}
	}
	// A colon that is not a line number keeps the line intact — a Go compile
	// error names its file and column and both matter.
	for _, l := range []string{
		"sh: tsc: command not found",
		"ModuleNotFoundError: No module named 'PIL'",
	} {
		if got, _ := FrictionLine(l); got != l {
			t.Errorf("FrictionLine(%q) = %q, want it unchanged", l, got)
		}
	}
}

func TestFrictionHashesFingerprintsToolOutputOnly(t *testing.T) {
	ms := []model.Message{
		{Role: "user", Text: "zsh:1: command not found: shellcheck"},
		{Role: roleToolOutput, Text: "zsh:1: command not found: shellcheck\nexit status 127"},
		{Role: roleToolOutput, Text: "(eval):9: command not found: shellcheck"},
	}
	got := frictionHashes(ms)
	// Three lines carry the error, but only two are tool output and those two
	// normalize to the same thing: one session, one piece of evidence.
	if len(got) != 1 {
		t.Fatalf("want one hash, got %d", len(got))
	}
	if got[0] != frictionHash("command not found: shellcheck") {
		t.Fatal("the hash is not the one the normalized line produces")
	}
	if len(frictionHashes([]model.Message{{Role: "user", Text: "all fine"}})) != 0 {
		t.Fatal("a session that tripped over nothing should carry nothing")
	}
}

func TestFrictionHashesCapped(t *testing.T) {
	ms := make([]model.Message, 0, frictionSessionCap+20)
	for i := range frictionSessionCap + 20 {
		ms = append(ms, model.Message{
			Role: roleToolOutput,
			Text: fmt.Sprintf("zsh:1: command not found: tool-number-%03d", i),
		})
	}
	if got := len(frictionHashes(ms)); got != frictionSessionCap {
		t.Fatalf("got %d hashes, want the cap %d", got, frictionSessionCap)
	}
}

// writeFrictionSessions lays down n claude sessions that each hit the same
// error under a different shell prefix, so the test also proves normalization
// survives the round trip through the manifest.
func writeFrictionSessions(t *testing.T, root string, n int) {
	t.Helper()
	proj := filepath.Join(root, "-tmp-friction")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		sid := fmt.Sprintf("fr%02d", i)
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				strconv.Itoa(i+1) + `T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				strconv.Itoa(i+1) + `T03:05:05Z","message":{"role":"user","content":[{"type":"tool_result",` +
				`"content":"(eval):` + strconv.Itoa(i+1) + `: command not found: shellcheck"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func frictionStore(t *testing.T, sessions int) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	writeFrictionSessions(t, claude, sessions)
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFindFrictionOverTheManifest(t *testing.T) {
	dir := frictionStore(t, FrictionMinSessions)
	f, ok := FindFriction(dir, nil)
	if !ok {
		t.Fatal("three sessions hitting one error should be reported")
	}
	if f.Text != "command not found: shellcheck" {
		t.Fatalf("text = %q", f.Text)
	}
	if len(f.Sessions) != FrictionMinSessions {
		t.Fatalf("sessions = %d", len(f.Sessions))
	}
	// Newest first: the date on the screen comes off the head of this list.
	for i := 1; i < len(f.Sessions); i++ {
		if f.Sessions[i].Updated.After(f.Sessions[i-1].Updated) {
			t.Fatal("sessions are not newest first")
		}
	}
	if !f.Last.Equal(f.Sessions[0].Updated) {
		t.Fatal("Last should be the newest session")
	}
}

func TestFindFrictionQuietBelowThreshold(t *testing.T) {
	dir := frictionStore(t, FrictionMinSessions-1)
	if _, ok := FindFriction(dir, nil); ok {
		t.Fatal("two sessions is a coincidence, not friction")
	}
}

func TestFindFrictionOnMissingStore(t *testing.T) {
	if _, ok := FindFriction(filepath.Join(t.TempDir(), "nope"), nil); ok {
		t.Fatal("no store, nothing to report")
	}
}

func TestFindFrictionSkipsLowSignalShellGlobWhenAnotherWallExists(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-Users-you-code-app")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		sid := fmt.Sprintf("glob%02d", i)
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				strconv.Itoa(i+1) + `T03:04:05Z","message":{"role":"user","content":"check glob"}}`,
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				strconv.Itoa(i+1) + `T03:05:05Z","message":{"role":"user","content":[{"type":"tool_result",` +
				`"content":"(eval):1: no matches found: *.nonexistent"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		sid := fmt.Sprintf("real%02d", i)
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				strconv.Itoa(i+1) + `T02:04:05Z","message":{"role":"user","content":"run shellcheck"}}`,
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				strconv.Itoa(i+1) + `T02:05:05Z","message":{"role":"user","content":[{"type":"tool_result",` +
				`"content":"(eval):1: command not found: shellcheck"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f, ok := FindFriction(dir, nil)
	if !ok {
		t.Fatal("expected FindFriction to return a wall")
	}
	if f.Text != "command not found: shellcheck" {
		t.Fatalf("FindFriction picked %q, want real error over zsh glob noise", f.Text)
	}
}

func TestFrictionSignatureFoldsZshEqualsAndIncludeGlobs(t *testing.T) {
	_, eqBase, ok := FrictionSignature("(eval):1: == not found")
	if !ok {
		t.Fatal("expected == not found to be recognised as friction")
	}
	for _, variant := range []string{
		"(eval):1: = not found",
		"(eval):1: === not found",
		"(eval):1: ==== not found",
		"(eval):1: ===== not found",
	} {
		line, sig, ok := FrictionSignature(variant)
		if !ok {
			t.Fatalf("expected %q to be recognised as friction", variant)
		}
		if sig != eqBase {
			t.Fatalf("signature for %q = %x, want %x (same as == not found)", variant, sig, eqBase)
		}
		if !isLowSignalShellFriction(line) {
			t.Fatalf("expected %q (%q) to be marked low-signal shell friction", variant, line)
		}
	}

	_, inc1, ok1 := FrictionSignature("(eval):1: no matches found: --include=*.go")
	_, inc2, ok2 := FrictionSignature("(eval):1: no matches found: --include=*.ts")
	if !ok1 || !ok2 || inc1 != inc2 {
		t.Fatalf("expected --include=<glob> zsh errors to share one signature: ok1=%v ok2=%v %x vs %x", ok1, ok2, inc1, inc2)
	}
}

func TestFrictionLineRecognisesAddressAlreadyInUse(t *testing.T) {
	lineGo, sigGo, okGo := FrictionSignature("listen tcp 127.0.0.1:8080: bind: address already in use")
	if !okGo || lineGo == "" {
		t.Fatalf("expected bind address already in use to match friction, got ok=%v line=%q", okGo, lineGo)
	}
	_, sigGoAltPort, okAlt := FrictionSignature("listen tcp 127.0.0.1:9090: bind: address already in use")
	if !okAlt || sigGo != sigGoAltPort {
		t.Fatalf("expected different ports to share signature: ok=%v %x vs %x", okAlt, sigGo, sigGoAltPort)
	}
	lineNode, _, okNode := FrictionSignature("Error: listen EADDRINUSE: address already in use :::3000")
	if !okNode || lineNode == "" {
		t.Fatalf("expected EADDRINUSE line to match friction, got ok=%v line=%q", okNode, lineNode)
	}
}

// checkFrictionTexts holds frictext.bin in dir to what reading the sessions
// back says: the walls of m, an entry for every session carrying one, each
// entry's lines and stamp as the session's tool output gives them, and
// TopFriction's answer the same with the file and without. It returns the
// file's content in a form a rebuild can be compared on.
func checkFrictionTexts(t *testing.T, dir string, m Manifest) string {
	t.Helper()
	ft, ok := decodeFrictionTextsFile(filepath.Join(dir, frictionTextsFile))
	if !ok {
		if len(m.Sessions) > 0 {
			t.Errorf("%s: no readable %s", dir, frictionTextsFile)
		}
		return ""
	}
	walls := frictionWalls(m)
	carriers := frictionCarriers(m, walls)
	if !sameWalls(ft.walls, walls) {
		t.Errorf("friction texts were written for %d walls, the manifest has %d", len(ft.walls), len(walls))
	}
	if !sameKeys(ft.entries, carriers) {
		t.Errorf("friction texts hold %d sessions, %d carry a wall", len(ft.entries), len(carriers))
	}
	all := map[uint64]string{}
	for h := range walls {
		all[h] = ""
	}
	if len(walls) > 0 && loadFrictionTexts(dir, m, all) == nil {
		t.Errorf("friction texts do not answer for the log they sit beside")
	}
	rd := newFrictionReader(dir, m)
	for k, meta := range carriers {
		stamp, lines := uint64(fnvOffset64), map[uint64]string{}
		outs, _ := rd.toolOutput(meta)
		for _, o := range outs {
			stamp = stampText(stamp, o)
			firstFrictionLines(o, walls, lines)
		}
		e := ft.entries[k]
		if fmt.Sprint(e.lines) != fmt.Sprint(lines) {
			t.Errorf("friction texts for %s:\n  file %q\n  read %q", k, e.lines, lines)
		}
		if e.stamp != stamp {
			t.Errorf("friction texts for %s: stamp %x, the session reads %x", k, e.stamp, stamp)
		}
	}
	top := func() string {
		var b strings.Builder
		for _, f := range TopFriction(dir, 0, nil) {
			fmt.Fprintf(&b, "%d %q\n", len(f.Sessions), f.Text)
		}
		return b.String()
	}
	with := top()
	frictionTextsOff = true
	without := top()
	frictionTextsOff = false
	if with != without {
		t.Errorf("TopFriction with the file:\n%s\nreading the sessions:\n%s", with, without)
	}
	var keys []string
	for k := range ft.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "walls %d\n", len(ft.walls))
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %x %v\n", k, ft.entries[k].stamp, ft.entries[k].lines)
	}
	return b.String()
}

// The walls' texts come from frictext.bin without a pass over the log, and a
// file that does not fit the log, whatever is wrong with it, gives what
// reading the sessions back gives.
func TestFrictionTextsAnswerOrFallBack(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	walls := []string{"make: *** [widget] Error 2", "zsh:1: command not found: frobnicate"}
	for w, wall := range walls {
		for i := range 3 {
			id := fmt.Sprintf("w%ds%d", w, i)
			at := fmt.Sprintf("2026-07-01T1%d:%02d:00Z", w, i*10)
			body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":"the build keeps stopping"}}` + "\n" +
				`{"type":"user","sessionId":"` + id + `","cwd":"/w/app","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","content":"` + wall + `"}]}}` + "\n"
			p := filepath.Join(root, "-w-app", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, frictionTextsFile)
	good, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("a build leaves no %s: %v", frictionTextsFile, err)
	}
	top := func() (string, int64) {
		invalidateManifestCache(dir)
		before := RecordLogScans()
		got := TopFriction(dir, 0, nil)
		return fmt.Sprint(got), RecordLogScans() - before
	}
	want, scans := top()
	if scans != 0 {
		t.Fatalf("with %s TopFriction took %d passes over the log", frictionTextsFile, scans)
	}
	otherLog := append([]byte(nil), good...)
	otherLog[16] ^= 0xff // inside the log fingerprint
	broken := map[string][]byte{
		"missing":   nil,
		"truncated": good[:len(good)-3],
		"bad magic": append([]byte("XXXXXXXX"), good[8:]...),
		"other log": otherLog,
	}
	for name, b := range broken {
		if b == nil {
			_ = os.Remove(file)
		} else if err := os.WriteFile(file, b, 0o600); err != nil {
			t.Fatal(err)
		}
		got, scans := top()
		if got != want {
			t.Errorf("%s file: %s, want %s", name, got, want)
		}
		if scans == 0 {
			t.Errorf("%s file: answered without reading the sessions", name)
		}
	}
}
