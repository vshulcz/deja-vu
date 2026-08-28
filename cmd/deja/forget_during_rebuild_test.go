package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// `deja forget` is the one command that destroys history on purpose, and a
// build can be running while it does — a hook, another terminal, the warmup
// after an upgrade. The index lock is what keeps the two apart; this holds the
// outcome that matters rather than the mechanism: exactly what was named goes,
// nothing else does, and the store is still an index afterwards (#2427).
func TestForgetAgainstARebuildDropsOnlyWhatItNamed(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	for _, project := range []string{"keep", "gone"} {
		store := filepath.Join(root, "-work-"+project)
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 40; i++ {
			at := time.Now().Add(-time.Duration(i) * time.Minute).UTC().Format(time.RFC3339)
			line := fmt.Sprintf(`{"type":"user","sessionId":"%s%d","timestamp":%q,"cwd":"/work/%s",`+
				`"message":{"role":"user","content":"the retry budget in %s take %d"}}`,
				project, i, at, project, project, i)
			name := filepath.Join(store, fmt.Sprintf("%s%d.jsonl", project, i))
			if err := os.WriteFile(name, []byte(strings.Repeat(line+"\n", 8)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// A rebuild, the heaviest thing that can be holding the index.
		_ = index.Ensure(dir, "", true, nil)
	}()
	out, errOut := captureBoth(t, "forget", "--project", "work/gone", "--all-matches")
	wg.Wait()
	if !strings.Contains(out+errOut, "sessions dropped") {
		t.Fatalf("forget did not report what it did:\n%s\n%s", out, errOut)
	}

	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("the index is not usable after the two ran together: %v", err)
	}
	if index.Damaged(dir) {
		t.Fatalf("the store was left damaged")
	}
	metas, err := index.Recent(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	kept, gone := 0, 0
	for _, m := range metas {
		switch {
		case strings.HasSuffix(m.Project, "work/gone"):
			gone++
		case strings.HasSuffix(m.Project, "work/keep"):
			kept++
		}
	}
	if gone != 0 {
		t.Errorf("%d sessions from the forgotten project came back", gone)
	}
	if kept != 40 {
		t.Errorf("the other project holds %d of its 40 sessions", kept)
	}
}
