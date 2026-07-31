package main

import (
	"bytes"
	"fmt"
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
	// "session across" rather than "sessions across": the count is pluralised,
	// and a fixture with one session used to read "1 sessions across 1 agents".
	for _, want := range []string{"session", "across", "recent", "déjà vu moment", "deja log"} {
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

// A store whose work is all older than a week opened with two zero lines —
// "today 0 sessions" and "this week 0 sessions · 0 recalls" — which is the
// worst possible first screen for someone whose history is real but not recent.
func TestBriefReplacesAQuietWeekWithTheSpanItHolds(t *testing.T) {
	tmp := hermeticEnv(t)
	proj := filepath.Join(tmp, "claude", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, day := range []string{"11", "13", "15"} {
		line := `{"type":"user","sessionId":"s` + day + `","cwd":"/w","timestamp":"2026-04-` + day +
			`T03:04:05Z","message":{"role":"user","content":"why does the pool exhaust under load ` +
			string(rune('a'+i)) + `?"}}`
		if err := os.WriteFile(filepath.Join(proj, "s"+day+".jsonl"), []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runBrief(dir, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "covering") || !strings.Contains(got, "Apr 11 2026") {
		t.Fatalf("want the span the index holds:\n%s", got)
	}
	for _, unwanted := range []string{"today      0", "this week  0"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("a zero line survived: %q\n%s", unwanted, got)
		}
	}
}

// The first screen names a wall and the count beside it; running the command
// it points at must produce the same number, or the tool reads as broken.
func TestBriefNamesTheWallAndAgreesWithTheCommand(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-f")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	for i := 0; i < index.FrictionMinSessions; i++ {
		sid := fmt.Sprintf("wf%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/f","timestamp":"2026-07-2` +
			fmt.Sprint(i) + `T10:00:00Z","message":{"role":"user","content":"run the linters"}}` + "\n" +
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w/f","timestamp":"2026-07-2` +
			fmt.Sprint(i) + `T10:05:00Z","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"zsh:` + fmt.Sprint(i+1) + `: command not found: shellcheck"}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var brief bytes.Buffer
	if err := runBrief(dir, &brief); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.String(), "command not found: shellcheck") {
		t.Fatalf("the wall is missing from the first screen:\n%s", brief.String())
	}
	want := fmt.Sprintf("%d sessions", index.FrictionMinSessions)
	if !strings.Contains(brief.String(), want) {
		t.Fatalf("want %q on the screen:\n%s", want, brief.String())
	}
	var cmd bytes.Buffer
	if err := runFriction(dir, nil, &cmd); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd.String(), want) {
		t.Fatalf("the command disagrees with the screen:\n%s", cmd.String())
	}
}
