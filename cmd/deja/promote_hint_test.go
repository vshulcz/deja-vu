package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// promote prints how to correct the mark it just made. It echoed the selector
// the reader typed, so a prefix that stood for several sessions sent the
// correction to whichever is newest — a different session from the one just
// marked (#884).
func TestPromoteHintNamesTheSessionItMarked(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, when string }{
		{"deja-2026-08-01-hyper-service", "2026-08-01T10:00:00Z"},
		{"deja-2026-08-01-super-service", "2026-07-30T10:00:00Z"},
	} {
		line := `{"type":"user","message":{"role":"user","content":"pool exhausted in ` + tc.id + `"},"timestamp":"` + tc.when + `","sessionId":"` + tc.id + `","cwd":"/proj"}`
		if err := os.WriteFile(filepath.Join(store, tc.id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := captureRunStderr(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "promote", "deja-2026…er-service", "--state", "rejected", "--note", "did not hold")
	if err != nil {
		t.Fatal(err)
	}
	marked := "deja-2026-08-01-hyper-service"
	if !strings.Contains(out, "promoted claude:"+marked) {
		t.Fatalf("promoted a different session:\n%s", out)
	}
	if !strings.Contains(out, "deja promote "+marked+" --state") {
		t.Errorf("the correction hint does not name the session that was marked:\n%s", out)
	}
	if strings.Contains(out, "deja promote deja-2026…er-service --state") {
		t.Errorf("the hint hands back the ambiguous selector:\n%s", out)
	}
}
