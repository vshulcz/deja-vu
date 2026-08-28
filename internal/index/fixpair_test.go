package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "brew install coreutils for timeout", Time: now.Add(time.Minute)},
	}
	// Far enough that the old six-record window never reached it.
	for i := 0; i < 12; i++ {
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
	ms := []model.Message{
		{Role: "tool-output", Text: "zsh:1: command not found: timeout", Time: now},
		{Role: "command", Text: "curl --max-time 5 example.internal", Time: now.Add(time.Minute)},
	}
	for i := 0; i < 12; i++ {
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
	if len(pairs) != 1 {
		t.Fatalf("want the retry stored, got %d pairs: %+v", len(pairs), pairs)
	}
	if pairs[0].Command != "curl --max-time 5 example.internal" {
		t.Errorf("wrong command stored: %q", pairs[0].Command)
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
	secret := "AKIAIOSFODNN7EXAMPLE"
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
