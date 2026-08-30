package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// excludeAfterIndex indexes a store and only then writes the pattern, which is
// the sequence a reader actually performs: see a project they did not want
// indexed, write the rule, look again.
func excludeAfterIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"x1", "x2", "x3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-secret", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/secret","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func writeExclude(t *testing.T, body string) {
	t.Helper()
	path := sources.ExcludePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// doctor is the screen that says what is in force — it prints the trust policy
// per activation and the ignore rule by name. For the exclude list it said
// "N active patterns", which is false in the one state a reader is in when
// they look: pattern written, index not rebuilt, and the next search still
// serving the project they meant to hide (#2664).
func TestDoctorSaysAnExcludePatternIsNotAppliedYet(t *testing.T) {
	excludeAfterIndex(t)
	writeExclude(t, "secret\n")
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exclusions") {
		t.Fatalf("doctor no longer reports the exclude list at all:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "exclusions") {
			continue
		}
		if strings.Contains(line, "active") && !strings.Contains(line, "rebuild") {
			t.Fatalf("doctor calls a pattern active that covers nothing already indexed:\n%s", line)
		}
		if !strings.Contains(line, "rebuild") {
			t.Fatalf("doctor does not say what would apply the pattern:\n%s", line)
		}
	}
}

// Once the index is built under the same list, the line is a plain statement
// again.
func TestDoctorSaysNothingExtraWhenTheExcludeListIsInForce(t *testing.T) {
	excludeAfterIndex(t)
	writeExclude(t, "secret\n")
	if _, err := captureRun(t, "index", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "exclusions") && strings.Contains(line, "rebuild") {
			t.Fatalf("the pattern is in force; nothing should point at a rebuild:\n%s", line)
		}
	}
}
