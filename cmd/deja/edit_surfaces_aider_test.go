package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// An aider SEARCH/REPLACE edit reaches restore, files, the after-compaction
// block and blame (#595).
func TestAiderEditReachesTheEditSurfaces(t *testing.T) {
	tmp := hermeticEnv(t)
	repo := editSurfaceRepo(t, tmp)
	t.Setenv("DEJA_AIDER_ROOTS", tmp)
	const span = "def retry_frobnicator(fn):\n    while True:\n        return fn()"
	doc := "\n# aider chat started at 2026-10-08 13:12:09\n\n" +
		"> Model: openai/stub with diff edit format  \n" +
		"> Added retry.py to the chat.  \n\n" +
		"#### the frobnicator retry helper never gives up, cap it  \n\n" +
		"Capping it.\n\nretry.py\n```python\n<<<<<<< SEARCH\n" + span + "\n=======\n" +
		"def retry_frobnicator(fn, attempts=5):\n    for _ in range(attempts):\n        return fn()\n>>>>>>> REPLACE\n```\n\n" +
		"> Tokens: 10 sent, 10 received.  \n> Applied edit to retry.py  \n"
	if err := os.WriteFile(filepath.Join(repo, ".aider.chat.history.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := sources.ParseAiderFile(filepath.Join(repo, ".aider.chat.history.md"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	id := ss[0].ID
	if !strings.HasPrefix(id, "aider-") {
		t.Fatalf("id = %q", id)
	}
	assertEditSurfaces(t, "aider", id, filepath.Join(repo, "retry.py"), span, "frobnicator")
}
