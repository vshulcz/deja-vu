package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A transfer that stopped leaves a directory of whole batches and one cut off
// mid-line. Both halves of the answer matter: what did arrive is in, and what
// did not is named — with a non-zero exit, so a script that chains on the
// import does not read the loss as success.
func TestAnInterruptedTransferImportsWhatArrivedAndNamesWhatDidNot(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	batch := t.TempDir()
	rec := func(id, text string) string {
		return fmt.Sprintf(`{"harness":"claude","session_id":%q,"project":"work/app","role":"user",`+
			`"text":%q,"time":"2026-08-04T12:00:00Z","origin":"laptop"}`, id, text)
	}
	whole := rec("a0", "the first batch arrived whole") + "\n" + rec("a1", "and so did this one") + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-laptop-1.jsonl"), []byte(whole), 0o600); err != nil {
		t.Fatal(err)
	}
	torn := rec("b0", "this one was cut") + "\n" + `{"harness":"claude","session_id":"b1","project":"work/app","role":"user","text":"cut off mid-tra`
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-laptop-2.jsonl"), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}

	// What arrived is printed; what did not comes back as the error, which is
	// what carries the exit code — a script chaining on the import must not
	// read the loss as success.
	out, err := captureRun(t, "sync", "import", batch)
	if !strings.Contains(out, "imported 2 records") {
		t.Errorf("what arrived was not reported:\n%s", out)
	}
	if err == nil {
		t.Fatalf("an import that lost a file exited zero:\n%s", out)
	}
	if !strings.Contains(err.Error(), "deja-sync-laptop-2.jsonl") || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("the file that did not arrive was not named: %v", err)
	}

	// And the store agrees with the report: the whole batch is in, the cut one
	// is not.
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	listed, err := captureRun(t, "last", "10")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed, "the first batch arrived whole") {
		t.Errorf("the whole batch is not in the store:\n%s", listed)
	}
	if strings.Contains(listed, "this one was cut") {
		t.Errorf("a record from the truncated file was imported:\n%s", listed)
	}
}
