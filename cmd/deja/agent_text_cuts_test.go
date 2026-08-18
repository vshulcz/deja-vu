package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// The conventions block and the environment block are text an agent reads. Both
// cut long entries at a byte count, which lands mid-character on anything but
// ASCII — a decision recorded in Russian reached the model as invalid UTF-8
// (#1319).
func TestAgentFacingTextIsCutOnRuneBoundaries(t *testing.T) {
	long := map[string]string{
		"cyrillic": strings.Repeat("решение про очередь повторов ", 20),
		"cjk":      strings.Repeat("重试队列的决定 ", 40),
		"ascii":    strings.Repeat("the retry queue decision ", 20),
	}
	for name, text := range long {
		got := conventionLine(sources.PromotedNote{Text: text})
		if !utf8.ValidString(got) {
			t.Errorf("conventions line for %s is not valid UTF-8: %q", name, got)
		}
		if got == "" {
			t.Errorf("conventions line for %s came back empty", name)
		}
	}
}

// A note that fits keeps its whole text, and a title still wins over the body.
func TestAShortConventionKeepsItsText(t *testing.T) {
	if got := conventionLine(sources.PromotedNote{Text: "queue retries with full jitter"}); got != "queue retries with full jitter" {
		t.Errorf("a short note was changed: %q", got)
	}
	if got := conventionLine(sources.PromotedNote{Title: "retry policy", Text: "long body"}); got != "retry policy" {
		t.Errorf("the title should name the note: %q", got)
	}
	// The first sentence, when there is one.
	if got := conventionLine(sources.PromotedNote{Text: "Retry three times. Then give up."}); got != "Retry three times." {
		t.Errorf("the first sentence was not taken: %q", got)
	}
}

// The mark left where text was cut has to be the character, not what a
// mis-decoded copy of it turns into: this constant held the Shift_JIS reading
// of "…" and the plan hook appended that to every truncated finding.
func TestTheTruncationMarkIsAnEllipsis(t *testing.T) {
	got := truncatePlanBytes(strings.Repeat("a", 100), 20)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncation mark is %q", got[len(got)-6:])
	}
	if strings.Contains(got, "窶") {
		t.Errorf("the mangled ellipsis is still there: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated text is not valid UTF-8: %q", got)
	}
}

// The environment block names the walls this machine keeps hitting, and it cut
// each one at 96 bytes — mid-character on a wall recorded in Russian or
// Chinese, and the block goes straight into the model's context. Through the
// block itself, since that is where the cut lives.
func TestTheEnvironmentBlockCutsOnRuneBoundaries(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-w")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// English shape so it is recognised as a wall, Cyrillic tail so the 96-byte
	// cut lands inside a character. 105 bytes: past the 96-byte cut, inside the
	// 120-byte bound on what counts as a wall, and with the cut falling on an
	// odd offset into two-byte letters — at the obvious phrasing it fell on a
	// boundary and the test could not fail.
	wall := "ModuleNotFoundError: No module named очередьповторовпредпродакшенмодуля"
	for i := 0; i < index.FrictionMinSessions; i++ {
		sid := fmt.Sprintf("w%02d", i)
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/w","timestamp":"2026-07-2` +
			fmt.Sprint(i%10) + `T10:00:00Z","message":{"role":"user","content":"run the checks"}}` + "\n" +
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w/w","timestamp":"2026-07-2` +
			fmt.Sprint(i%10) + `T10:05:00Z","message":{"role":"user","content":[{"type":"tool_result",` +
			`"content":"` + wall + `"}]}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	got := environmentBlock(dir, policy.ActivationAuto)
	if got == "" {
		t.Fatal("no environment block to judge")
	}
	if !utf8.ValidString(got) {
		t.Errorf("a wall in Russian was cut mid-character:\n%q", got)
	}
}

// The friction screen prints walls to a terminal, and cut each at 76 bytes.
func TestFrictionLinesCutOnRuneBoundaries(t *testing.T) {
	for _, pad := range []string{"", "a", "ab"} {
		got := trimFriction(pad + strings.Repeat("ошибка подключения ", 20))
		if !utf8.ValidString(got) {
			t.Errorf("pad %q: a wall printed as broken bytes: %q", pad, got)
		}
		if len(got) > 79 {
			t.Errorf("pad %q: the line ran past its bound at %d bytes", pad, len(got))
		}
	}
	short := "npm ERR! code ELIFECYCLE"
	if got := trimFriction(short); got != short {
		t.Errorf("a short wall was changed: %q", got)
	}
}
