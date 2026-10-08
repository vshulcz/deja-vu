package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
