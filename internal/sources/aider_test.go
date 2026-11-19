package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// aider writes every input as a `#### ` line, its own commands included, so
// `/undo` became a user message of its own and `/clear` was glued onto the
// question typed after it (#3248).
func TestAiderDropsItsOwnCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.md")
	const doc = `# aider chat started at 2026-08-01 10:00:00

#### /clear
#### why does the vantrell fetcher time out

the pool is exhausted before the second attempt

#### /undo
#### /add internal/retry/retry.go
#### and what did we settle about the retry cap

four, and the backoff is on the deadline

#### /etc/hosts is wrong on the build box

that is a different machine
`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	want := []string{
		"why does the vantrell fetcher time out",
		"and what did we settle about the retry cap",
		// A message that merely opens with a path is the person's.
		"/etc/hosts is wrong on the build box",
	}
	if strings.Join(users, "|") != strings.Join(want, "|") {
		t.Fatalf("user turns =\n  %s\nwant\n  %s",
			strings.Join(users, "\n  "), strings.Join(want, "\n  "))
	}
	// And the answers are untouched.
	found := false
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" && strings.Contains(m.Text, "pool is exhausted") {
			found = true
		}
	}
	if !found {
		t.Error("an answer went with the commands")
	}
}

// The classifier itself, since the list is what decides.
func TestAiderSlashCommandKnowsWhatIsACommand(t *testing.T) {
	for _, cmd := range []string{
		"/undo", "/clear", "/add internal/retry/retry.go", "/run go test ./...",
		"/drop", "/model gpt-5", "/tokens", "  /quit  ", "/GIT status",
		// The sub-coder logs the question again as its own line (#4325).
		"/ask why the retry cap is four", "/code add a backoff", "/context where is it",
	} {
		if !aiderSlashCommand(cmd) {
			t.Errorf("%q is one of aider's commands", cmd)
		}
	}
	for _, mine := range []string{
		"/etc/hosts is wrong on the build box",
		"/usr/local/bin/deja is on PATH now",
		"/",
		"why does /undo not work here",
		"",
	} {
		if aiderSlashCommand(mine) {
			t.Errorf("%q is the reader's, not a command", mine)
		}
	}
}

// aider prefixes its own output with "> " on the first line only: the
// --verbose dump and a multi-line commit message continue unprefixed, and the
// reader took those lines as the assistant speaking (#3311). Shape from a real
// 0.86.2 history.
func TestAiderOutputContinuationsAreNotSpeech(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.chat.history.md")
	body := strings.Join([]string{
		"",
		"# aider chat started at 2026-09-08 11:14:39",
		"",
		"> Command Line Args:   --verbose --model openai/mock-1",
		"Config File (/w/.aider.conf.yml):",
		"  read:              ['/w/.config/deja/aider-context.md']",
		"Defaults:",
		"  --map-refresh:     auto",
		"",
		"#### why does the query miss the tenant predicate",
		"",
		"The join drops it; I will add the predicate.",
		"",
		"> Applied edit to query.go",
		"> Commit 5ee830b add the tenant predicate",
		"    and keep the join order stable",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var users, assistants []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case "user":
			users = append(users, m.Text)
		case "assistant":
			assistants = append(assistants, m.Text)
		}
	}
	if len(users) != 1 || users[0] != "why does the query miss the tenant predicate" {
		t.Errorf("user turns = %q", users)
	}
	if len(assistants) != 1 || assistants[0] != "The join drops it; I will add the predicate." {
		t.Errorf("assistant turns = %q, want the reply alone — the dump and the commit tail are output", assistants)
	}
}

// The histories below are what aider 0.86.2 wrote against a stub model asked
// to cap a retry loop, one per edit format, with the launch line and the
// project directory replaced. Only the banner differs between them.
func aiderHistory(format string, reply string, after string) string {
	return "\n# aider chat started at 2026-10-08 13:12:09\n\n" +
		"> aider --model openai/stub --edit-format " + format + " --no-git --yes-always --message the retry helper never gives up, cap it retry.py NOTES.md  \n" +
		"> Creating empty file /work/proj/NOTES.md  \n" +
		"> Aider v0.86.2  \n" +
		"> Model: openai/stub with " + format + " edit format  \n" +
		"> Git repo: none  \n" +
		"> Repo-map: disabled  \n" +
		"> Added NOTES.md to the chat.  \n" +
		"> Added retry.py to the chat.  \n\n" +
		"#### the retry helper never gives up, cap it  \n\n" +
		reply + "\n" + after
}

const aiderOldRetry = "def retry(fn):\n    while True:\n        try:\n            return fn()\n        except Exception:\n            pass"

func aiderRecords(t *testing.T, doc string) (edits, wrote []string, dir string) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, ".aider.chat.history.md")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		case "assistant":
			if strings.Contains(m.Text, "<<<<<<< SEARCH") && !strings.Contains(m.Text, "retry.py") {
				t.Errorf("a block lost its file name: %q", m.Text)
			}
		}
	}
	return edits, wrote, dir
}

func aiderWroteHas(t *testing.T, wrote []string, path, line string) {
	t.Helper()
	h, ok := HashWrittenLine(line)
	if !ok {
		t.Fatalf("%q is too short to be evidence", line)
	}
	for _, w := range wrote {
		if p, has := WroteRecordHas(w, h); has && p == path {
			return
		}
	}
	t.Fatalf("no wrote record under %s holds %q: %q", path, line, wrote)
}

// aider's history holds the reply as the model wrote it, edits included, and
// the reader kept only the "Applied edit to" line: the file without the change
// (#595). The SEARCH side is the replaced span, the REPLACE side the written
// one, and a block that creates a file has no replaced side.
func TestAiderRecordsBothSidesOfASearchReplaceBlock(t *testing.T) {
	reply := "The retry helper never gives up. I'll cap it.\n\n" +
		"retry.py\n```python\n<<<<<<< SEARCH\n" + aiderOldRetry + "\n=======\n" +
		"def retry(fn, attempts=5):\n    for _ in range(attempts):\n        try:\n            return fn()\n        except Exception:\n            last = None\n    raise RuntimeError(\"retry: out of attempts\")\n" +
		">>>>>>> REPLACE\n```\n\nAnd a note:\n\n" +
		"NOTES.md\n```markdown\n<<<<<<< SEARCH\n=======\nretry gives up after five attempts now\n>>>>>>> REPLACE\n```\n"
	after := "> Tokens: 10 sent, 10 received.  \n> Applied edit to NOTES.md  \n> Applied edit to retry.py  \n"
	edits, wrote, dir := aiderRecords(t, aiderHistory("diff", reply, after))
	retry := filepath.Join(dir, "retry.py")
	if strings.Join(edits, "|") != retry+"\n"+aiderOldRetry {
		t.Fatalf("edits = %q", edits)
	}
	aiderWroteHas(t, wrote, retry, `    raise RuntimeError("retry: out of attempts")`)
	aiderWroteHas(t, wrote, filepath.Join(dir, "NOTES.md"), "retry gives up after five attempts now")
}

// A block that did not match is followed by aider's error, not by an "Applied
// edit" line, and changed nothing: its text is not a span.
func TestAiderSkipsABlockThatFailedToMatch(t *testing.T) {
	reply := "Trying.\n\nretry.py\n```python\n<<<<<<< SEARCH\ndef retry_forever_this_text_is_not_in_the_file(fn):\n=======\ndef retry(fn, attempts=3):\n>>>>>>> REPLACE\n```\n"
	after := "> Tokens: 10 sent, 10 received.  \n" +
		"> The LLM did not conform to the edit format.  \n" +
		"> https://aider.chat/docs/troubleshooting/edit-errors.html  \n" +
		"> # 1 SEARCH/REPLACE block failed to match!\n\n" +
		"## SearchReplaceNoExactMatch: This SEARCH block failed to exactly match lines in retry.py\n" +
		"<<<<<<< SEARCH\ndef retry_forever_this_text_is_not_in_the_file(fn):\n=======\ndef retry(fn, attempts=3):\n>>>>>>> REPLACE\n\n" +
		"The SEARCH section must exactly match an existing block of lines including all white space, comments, indentation, docstrings, etc  \n\n" +
		"I could not match it, stopping here.\n\n" +
		"> Tokens: 10 sent, 10 received.  \n"
	edits, wrote, _ := aiderRecords(t, aiderHistory("diff", reply, after))
	if len(edits) != 0 || len(wrote) != 0 {
		t.Fatalf("an edit that never applied was recorded: edits=%q wrote=%q", edits, wrote)
	}
}

// udiff writes a unified diff in a ```diff fence; the removed lines are the
// span.
func TestAiderReadsAUnifiedDiffEdit(t *testing.T) {
	reply := "Capping the loop.\n\n```diff\n--- retry.py\n+++ retry.py\n@@ ... @@\n" +
		"-def retry(fn):\n-    while True:\n+def retry(fn, attempts=5):\n+    for _ in range(attempts):\n         try:\n             return fn()\n```\n"
	after := "> Tokens: 10 sent, 10 received.  \n> Applied edit to retry.py  \n"
	edits, wrote, dir := aiderRecords(t, aiderHistory("udiff", reply, after))
	retry := filepath.Join(dir, "retry.py")
	if strings.Join(edits, "|") != retry+"\ndef retry(fn):\n    while True:" {
		t.Fatalf("edits = %q", edits)
	}
	aiderWroteHas(t, wrote, retry, "def retry(fn, attempts=5):")
}

// whole rewrites the file, so there is a written side and no replaced one; in
// any other format a fenced block under a file name is only shown.
func TestAiderWholeFormatRecordsTheWrittenFile(t *testing.T) {
	reply := "Here is the capped helper.\n\nretry.py\n```python\n" +
		"def retry(fn, attempts=5):\n    for _ in range(attempts):\n        try:\n            return fn()\n        except Exception:\n            pass\n    raise RuntimeError(\"retry: out of attempts\")\n```\n"
	after := "> Tokens: 10 sent, 10 received.  \n> Applied edit to retry.py  \n"
	edits, wrote, dir := aiderRecords(t, aiderHistory("whole", reply, after))
	if len(edits) != 0 {
		t.Fatalf("whole has no replaced side: %q", edits)
	}
	aiderWroteHas(t, wrote, filepath.Join(dir, "retry.py"), `    raise RuntimeError("retry: out of attempts")`)

	edits, wrote, _ = aiderRecords(t, aiderHistory("diff", reply, after))
	if len(edits) != 0 || len(wrote) != 0 {
		t.Fatalf("a code block in diff format is not an edit: edits=%q wrote=%q", edits, wrote)
	}
}

// A session's id is its own, not its place in the file: the same launch keeps
// its id whether or not the file still holds the launches before it, and two
// launches in one second still get two ids (#4332).
func TestAiderSessionIDDoesNotDependOnItsPlaceInTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".aider.chat.history.md")
	const later = "# aider chat started at 2026-10-01 09:00:00\n\n#### rename the config loader\n\nRenamed it.\n"
	parse := func(doc string) []string {
		t.Helper()
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseAiderFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(ss))
		for _, s := range ss {
			ids = append(ids, s.ID)
		}
		return ids
	}
	both := parse("# aider chat started at 2026-09-30 10:00:00\n\n#### add jitter\n\nAdded.\n\n" + later)
	alone := parse(later)
	if len(both) != 2 || len(alone) != 1 || both[1] != alone[0] {
		t.Fatalf("the 09:00 launch is %v beside an earlier one and %v alone", both, alone)
	}
	if both[0] == alone[0] {
		t.Fatalf("a new file's first launch took the deleted file's first id %q", both[0])
	}
	twin := parse(later + "\n" + later)
	if len(twin) != 2 || twin[0] == twin[1] {
		t.Fatalf("two launches in one second share an id: %v", twin)
	}
}

// aider writes its history at the git root it runs in, not in $HOME, so a
// project nobody listed in DEJA_AIDER_ROOTS was never read. `deja aider`
// records the directory it starts aider in (#4326).
func TestAiderFilesReadsRecordedProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("DEJA_AIDER_ROOTS", "")
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	proj := t.TempDir()
	hist := filepath.Join(proj, ".aider.chat.history.md")
	if err := os.WriteFile(hist, []byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AiderFiles(); len(got) != 0 {
		t.Fatalf("nothing recorded yet, found %v", got)
	}
	for range 2 {
		if err := RecordAiderProject(proj); err != nil {
			t.Fatal(err)
		}
	}
	if got := AiderFiles(); len(got) != 1 || got[0] != hist {
		t.Fatalf("AiderFiles = %v, want [%s]", got, hist)
	}
	b, err := os.ReadFile(AiderProjectsPath())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), proj); n != 1 {
		t.Fatalf("project recorded %d times, want once:\n%s", n, b)
	}
}

// One history reached under two spellings is one history. A project recorded
// through a symlink, or typed in another case on a case-insensitive disk, and
// also under DEJA_AIDER_ROOTS was read twice, and every session in it was
// indexed twice under two ids.
func TestAiderFilesReadsOneHistoryOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("AIDER_CHAT_HISTORY_FILE", "")
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".aider.chat.history.md"), []byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(proj, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	t.Setenv("DEJA_AIDER_ROOTS", root)
	if err := RecordAiderProject(link); err != nil {
		t.Fatal(err)
	}
	if got := AiderFiles(); len(got) != 1 {
		t.Fatalf("AiderFiles = %v, want the one history once", got)
	}
}

// The list keeps the projects that are still there: a deleted one is dropped
// the next time `deja aider` records a project, so the list does not grow
// with every directory aider was ever started in.
func TestRecordAiderProjectDropsDeletedProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	gone := filepath.Join(t.TempDir(), "gone")
	kept := t.TempDir()
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{gone, kept} {
		if err := RecordAiderProject(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := RecordAiderProject(kept); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(AiderProjectsPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), gone) || !strings.Contains(string(b), kept) {
		t.Fatalf("list after %s was deleted:\n%s", gone, b)
	}
}

// aider writes what it did as its own output — `> Applied edit to x`,
// `> Added x to the chat.` — and a shell command as a `#### /run` input. None of
// it reached the index, and a launch that only ran a command was dropped whole
// (#4324).
func TestAiderRecordsFilesAndCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.chat.history.md")
	const doc = "# aider chat started at 2026-09-30 10:00:00\n\n" +
		"> Added worker.py to the chat.  \n\n" +
		"#### fix the retry loop in worker.py  \n\n" +
		"worker.py\n```python\ndef retry(n):\n    pass\n```\n\n" +
		"> Applied edit to worker.py  \n\n" +
		"#### /add docs/notes.md  \n" +
		"> Added docs/notes.md to the chat  \n\n" +
		"#### /read-only CONVENTIONS.md  \n" +
		"> Added CONVENTIONS.md to read-only files.  \n\n" +
		"# aider chat started at 2026-09-30 10:05:00\n\n" +
		"#### /run pytest -q tests/test_worker.py  \n" +
		"> Add 0.1k tokens of command output to the chat? (Y)es/(N)o [Yes]: y  \n" +
		"> Added 3 lines of output to the chat.  \n\n" +
		"#### !go test ./...  \n\n" +
		"#### /git status  \n\n" +
		"#### /test make check  \n\n" +
		"> Running npm test  \n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want 2 — the launch that only ran commands is a session", len(ss))
	}
	got := func(s int, role string) []string {
		var out []string
		for _, m := range ss[s].Messages {
			if m.Role == role {
				out = append(out, m.Text)
			}
		}
		return out
	}
	wantFiles := []string{
		filepath.Join(dir, "worker.py"),
		filepath.Join(dir, "docs", "notes.md"),
		filepath.Join(dir, "CONVENTIONS.md"),
	}
	if f := got(0, RoleFiles); strings.Join(f, "|") != strings.Join(wantFiles, "|") {
		t.Errorf("files = %q, want %q", f, wantFiles)
	}
	if c := got(1, RoleFiles); len(c) != 0 {
		t.Errorf("`Added 3 lines of output` is not a file: %q", c)
	}
	wantCmds := []string{"pytest -q tests/test_worker.py", "go test ./...", "git status", "make check", "npm test"}
	if c := got(1, RoleCommand); strings.Join(c, "|") != strings.Join(wantCmds, "|") {
		t.Errorf("commands = %q, want %q", c, wantCmds)
	}
	if u := got(1, "user"); len(u) != 0 {
		t.Errorf("a command is not a question: %q", u)
	}
}

// aider logs `/ask <q>` and then `<q>` again when the sub-coder takes it, so
// keeping the command line indexed the question twice and titled the session
// with the command (#4325).
func TestAiderAskIsIndexedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".aider.chat.history.md")
	const doc = "# aider chat started at 2026-09-30 10:10:00\n\n" +
		"#### /ask what does worker.py do?  \n\n" +
		"#### what does worker.py do?  \n\n" +
		"It retries n times with a one second sleep.\n\n" +
		"#### /code add jitter  \n\n" +
		"#### add jitter  \n\n" +
		"Done.\n\n" +
		"#### /context where is the backoff  \n\n" +
		"#### where is the backoff  \n\n" +
		"In worker.py.\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	want := []string{"what does worker.py do?", "add jitter", "where is the backoff"}
	if strings.Join(users, "|") != strings.Join(want, "|") {
		t.Fatalf("user turns = %q, want %q", users, want)
	}
}

// AiderSessionAt has to name the session the way the parser does, or the live
// recall asks as a session the index does not hold.
func TestAiderSessionAtMatchesTheParser(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".aider.chat.history.md")
	first := "\n# aider chat started at 2026-10-01 09:00:00\n\n#### first  \n\nok\n\n" +
		// A header inside a fence is text, not a session.
		"```\n# aider chat started at 2026-10-01 09:30:00\n```\n"
	// Two launches in one second: the second id carries a suffix.
	second := "\n# aider chat started at 2026-10-02 10:00:00\n\n#### second  \n\nok\n"
	third := "\n# aider chat started at 2026-10-02 10:00:00\n\n#### third  \n\nok\n"
	if err := os.WriteFile(path, []byte(first+second+third), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(path)
	if err != nil || len(ss) != 3 {
		t.Fatalf("parsed %d sessions (%v)", len(ss), err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	oct1, oct2 := at("2026-10-01 08:59:59"), at("2026-10-02 10:00:00")
	if got := AiderSessionAt(path, 0, oct1); got != ss[0].ID {
		t.Errorf("first: %q, the parser says %q", got, ss[0].ID)
	}
	if got := AiderSessionAt(path, int64(len(first+second)), oct2); got != ss[2].ID {
		t.Errorf("same-second second launch: %q, the parser says %q", got, ss[2].ID)
	}
	// Two aiders started together, both before either wrote its header:
	// nothing says which header is whose.
	if got := AiderSessionAt(path, int64(len(first)), oct2); got != "" {
		t.Errorf("two headers from the launch second, got %q", got)
	}
	// A header from long after the launch is another aider's.
	if got := AiderSessionAt(path, 0, oct1.Add(-time.Hour)); got != "" {
		t.Errorf("a header an hour after launch was taken, got %q", got)
	}
	if got := AiderSessionAt(path, int64(len(first+second+third)), oct2); got != "" {
		t.Errorf("no header past the end yet, got %q", got)
	}
	// Replaced by a shorter file: its last session is the one being written.
	if got := AiderSessionAt(path, 1<<20, oct2); got != ss[2].ID {
		t.Errorf("replaced file: %q, want %q", got, ss[2].ID)
	}
	if got := AiderSessionAt(filepath.Join(t.TempDir(), "none.md"), 0, oct1); got != "" {
		t.Errorf("missing file: %q", got)
	}
}

// aider logs every input as a "#### " line, commands included; a line that is
// only a slash command is not a question, and `/clear` was glued onto the
// question typed after it (#3248).
func TestParseAiderSkipsSlashCommands(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".aider.chat.history.md")
	body := "# aider chat started at 2026-09-08 01:00:00\n\n#### /add src/pager.go\n\n#### /undo\n\n#### /clear\n\n#### why does the pager stall on the last page\n\nbecause the cursor overflows\n\n#### /etc/hosts is wrong on the staging box, fix the entry\n\ndone\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	want := []string{"why does the pager stall on the last page", "/etc/hosts is wrong on the staging box, fix the entry"}
	if len(users) != len(want) || users[0] != want[0] || users[1] != want[1] {
		t.Fatalf("user lines = %q, want %q", users, want)
	}
}

const aiderFixture = `# aider chat started at 2026-07-16 10:32:01

> /usr/local/bin/aider --model sonnet
> Aider v0.86.1

#### fix the off-by-one in pager.go
#### keep the tests green

I'll fix the loop bound:

` + "```go" + `
for i := 0; i < n; i++ {
#### this is code, not a user line
> and this is not tool output
` + "```" + `

> Applied edit to pager.go
> Commit a1b2c3d fix: off-by-one

#### <blank>

# aider chat started at 2026-07-16 11:00:00

#### second session question

Second session answer here.
`

func TestParseAiderFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".aider.chat.history.md")
	if err := os.WriteFile(p, []byte(aiderFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseAiderFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 {
		t.Fatalf("sessions = %d, want 2", len(ss))
	}
	s1 := ss[0]
	if s1.Harness != "aider" || s1.Started.Hour() != 10 {
		t.Fatalf("bad session meta: %#v", s1)
	}
	if len(s1.Messages) != 3 {
		t.Fatalf("s1 messages = %d, want user+assistant+files: %#v", len(s1.Messages), s1.Messages)
	}
	if f := s1.Messages[2]; f.Role != RoleFiles || f.Text != filepath.Join(dir, "pager.go") {
		t.Fatalf("applied edit not recorded as a file: %#v", f)
	}
	if s1.Messages[0].Role != "user" || s1.Messages[0].Text != "fix the off-by-one in pager.go\nkeep the tests green" {
		t.Fatalf("user message wrong: %#v", s1.Messages[0])
	}
	a := s1.Messages[1]
	if a.Role != "assistant" {
		t.Fatalf("assistant role wrong: %#v", a)
	}
	// fence content preserved verbatim, prefixes inside not treated as boundaries
	for _, want := range []string{"loop bound", "#### this is code", "> and this is not tool output"} {
		if !strings.Contains(a.Text, want) {
			t.Fatalf("assistant text missing %q: %q", want, a.Text)
		}
	}
	s2 := ss[1]
	if len(s2.Messages) != 2 || s2.Messages[0].Text != "second session question" || s2.Messages[1].Text != "Second session answer here." {
		t.Fatalf("s2 wrong: %#v", s2.Messages)
	}
	if s1.ID == s2.ID || s1.ID == "" {
		t.Fatalf("session ids not distinct: %q %q", s1.ID, s2.ID)
	}
}

func TestAiderFilesDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	roots := t.TempDir()
	proj := filepath.Join(roots, "group", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(home, ".aider.chat.history.md"),
		filepath.Join(roots, ".aider.chat.history.md"),
		filepath.Join(proj, ".aider.chat.history.md"),
	} {
		if err := os.WriteFile(p, []byte("# aider chat started at 2026-01-01 00:00:00\n#### q\nans\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_AIDER_ROOTS", roots)
	files := AiderFiles()
	if len(files) != 3 {
		t.Fatalf("discovered %d files, want 3: %v", len(files), files)
	}
}

// aider writes its local wall clock with no zone. Read as UTC, every aider
// time showed shifted by the user's offset (#5053): 23:45 here must stay 23:45.
func TestAiderHeaderReadsAsLocalWallTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), aiderHistoryName)
	body := aiderSessionMark + " 2026-07-20 23:45:00\n\n#### why does the pool run dry\n\nthe cap is too low\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	for _, zone := range []*time.Location{time.FixedZone("pago", -11*3600), time.FixedZone("kiritimati", 14*3600)} {
		time.Local = zone
		ss, err := ParseAiderFile(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse: %d sessions, %v", len(ss), err)
		}
		got := ss[0].Started
		if want := time.Date(2026, 7, 20, 23, 45, 0, 0, zone); !got.Equal(want) {
			t.Errorf("%s: started %v, want the wall clock in this zone, %v", zone, got, want)
		}
		if wall := got.In(time.Local).Format("2006-01-02 15:04:05"); wall != "2026-07-20 23:45:00" {
			t.Errorf("%s: shows as %s, want 2026-07-20 23:45:00", zone, wall)
		}
	}
}
