package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/usage"
)

func seedBriefIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"b1","cwd":"/w/a","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"jwks rotation cache stale problem"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "b1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBriefShowsMemoryAlive(t *testing.T) {
	dir := seedBriefIndex(t)
	usage.RecordDigest(dir, usage.KindRecall, strings.Repeat("x", 512), 1, 4096)
	usage.RecordDigest(dir, usage.KindDejaVu, "dv digest", 1, 2048)
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"sessions across", "recent", "déjà vu moment", "deja log"} {
		if !strings.Contains(s, want) {
			t.Fatalf("brief missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Usage:") {
		t.Fatal("brief fell back to usage text")
	}
}

func TestBriefFallsBackToUsageWithoutIndex(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	if err := runBrief(index.DefaultDir(), &out); err != nil {
		t.Fatal(err)
	}
	// printUsage writes to stdout, not our buffer — the contract is simply
	// that runBrief does not error and prints nothing misleading.
	if strings.Contains(out.String(), "sessions across") {
		t.Fatalf("brief invented an index: %q", out.String())
	}
}

func TestDejaVuCountersFlow(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordDigest(dir, usage.KindDejaVu, "digest", 2, 1024)
	usage.RecordDigest(dir, usage.KindHook, "start digest", 1, 512)
	if got := usage.DejaVuWeek(dir); got != 1 {
		t.Fatalf("DejaVuWeek = %d, want 1", got)
	}
	tot := usage.Totals(dir)
	if tot.DejaVuMoments != 1 || tot.Injections != 2 {
		t.Fatalf("totals = %+v", tot)
	}
}

func TestDejaVuLineShape(t *testing.T) {
	s := model.Session{Title: "jwks cache rotation broke login on the gateway and it hurt", Updated: time.Now().AddDate(0, 0, -21)}
	line := dejaVuLine(s)
	if !strings.Contains(line, "you have been here") || !strings.Contains(line, "jwks") {
		t.Fatalf("dejaVuLine = %q", line)
	}
}
