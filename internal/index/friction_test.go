package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestFrictionLineKeepsSpecificErrors(t *testing.T) {
	for _, l := range []string{
		"zsh:1: command not found: shellcheck",
		"ModuleNotFoundError: No module named 'yaml'",
		"internal/index/store.go:41:2: undefined: signalLines",
		"dial tcp 127.0.0.1:5432: connect: connection refused",
	} {
		if _, ok := FrictionLine(l); !ok {
			t.Errorf("dropped a specific error: %q", l)
		}
	}
	for _, l := range []string{
		"Traceback (most recent call last):",
		"Error: exit status 1",
		"--- FAIL: TestThing (0.01s)",
		"not found",                          // too short to name anything
		`echo "❌ App not found: $APP"`,       // source, not a result
		`  9 sessions  command not found: x`, // this command's own output
		"ok  github.com/vshulcz/deja-vu/internal/index  1.2s",
	} {
		if _, ok := FrictionLine(l); ok {
			t.Errorf("kept a line that names nothing: %q", l)
		}
	}
}

// The same missing command reaches the corpus under three shell prefixes. Left
// unnormalized each lands below the threshold and none of them is ever
// reported, which is the bug this function exists for.
func TestFrictionLineNormalizesShellPosition(t *testing.T) {
	want := "command not found: timeout"
	for _, l := range []string{
		"zsh:1: command not found: timeout",
		"(eval):2: command not found: timeout",
		"  bash:15: command not found: timeout  ",
	} {
		got, ok := FrictionLine(l)
		if !ok || got != want {
			t.Errorf("FrictionLine(%q) = %q, %v; want %q, true", l, got, ok, want)
		}
	}
	// A colon that is not a line number keeps the line intact — a Go compile
	// error names its file and column and both matter.
	for _, l := range []string{
		"sh: tsc: command not found",
		"ModuleNotFoundError: No module named 'PIL'",
	} {
		if got, _ := FrictionLine(l); got != l {
			t.Errorf("FrictionLine(%q) = %q, want it unchanged", l, got)
		}
	}
}

func TestFrictionHashesFingerprintsToolOutputOnly(t *testing.T) {
	ms := []model.Message{
		{Role: "user", Text: "zsh:1: command not found: shellcheck"},
		{Role: roleToolOutput, Text: "zsh:1: command not found: shellcheck\nexit status 127"},
		{Role: roleToolOutput, Text: "(eval):9: command not found: shellcheck"},
	}
	got := frictionHashes(ms)
	// Three lines carry the error, but only two are tool output and those two
	// normalize to the same thing: one session, one piece of evidence.
	if len(got) != 1 {
		t.Fatalf("want one hash, got %d", len(got))
	}
	if got[0] != frictionHash("command not found: shellcheck") {
		t.Fatal("the hash is not the one the normalized line produces")
	}
	if len(frictionHashes([]model.Message{{Role: "user", Text: "all fine"}})) != 0 {
		t.Fatal("a session that tripped over nothing should carry nothing")
	}
}

func TestFrictionHashesCapped(t *testing.T) {
	var ms []model.Message
	for i := 0; i < frictionSessionCap+20; i++ {
		ms = append(ms, model.Message{
			Role: roleToolOutput,
			Text: fmt.Sprintf("zsh:1: command not found: tool-number-%03d", i),
		})
	}
	if got := len(frictionHashes(ms)); got != frictionSessionCap {
		t.Fatalf("got %d hashes, want the cap %d", got, frictionSessionCap)
	}
}

// writeFrictionSessions lays down n claude sessions that each hit the same
// error under a different shell prefix, so the test also proves normalization
// survives the round trip through the manifest.
func writeFrictionSessions(t *testing.T, root string, n int) {
	t.Helper()
	proj := filepath.Join(root, "-tmp-friction")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("fr%02d", i)
		lines := []string{
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				fmt.Sprint(i+1) + `T03:04:05Z","message":{"role":"user","content":"run the deploy script"}}`,
			`{"type":"user","sessionId":"` + sid + `","cwd":"/w","timestamp":"2026-01-0` +
				fmt.Sprint(i+1) + `T03:05:05Z","message":{"role":"user","content":[{"type":"tool_result",` +
				`"content":"(eval):` + fmt.Sprint(i+1) + `: command not found: shellcheck"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func frictionStore(t *testing.T, sessions int) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	writeFrictionSessions(t, claude, sessions)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFindFrictionOverTheManifest(t *testing.T) {
	dir := frictionStore(t, FrictionMinSessions)
	f, ok := FindFriction(dir)
	if !ok {
		t.Fatal("three sessions hitting one error should be reported")
	}
	if f.Text != "command not found: shellcheck" {
		t.Fatalf("text = %q", f.Text)
	}
	if len(f.Sessions) != FrictionMinSessions {
		t.Fatalf("sessions = %d", len(f.Sessions))
	}
	// Newest first: the date on the screen comes off the head of this list.
	for i := 1; i < len(f.Sessions); i++ {
		if f.Sessions[i].Updated.After(f.Sessions[i-1].Updated) {
			t.Fatal("sessions are not newest first")
		}
	}
	if !f.Last.Equal(f.Sessions[0].Updated) {
		t.Fatal("Last should be the newest session")
	}
}

func TestFindFrictionQuietBelowThreshold(t *testing.T) {
	dir := frictionStore(t, FrictionMinSessions-1)
	if _, ok := FindFriction(dir); ok {
		t.Fatal("two sessions is a coincidence, not friction")
	}
}

func TestFindFrictionOnMissingStore(t *testing.T) {
	if _, ok := FindFriction(filepath.Join(t.TempDir(), "nope")); ok {
		t.Fatal("no store, nothing to report")
	}
}

// Two walls hit by the same number of sessions: the one still being hit wins,
// because a wall the machine stopped running into is history, not friction.
func TestNewestOfBreaksTheTie(t *testing.T) {
	old := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	ms := []SessionMeta{{Updated: old}, {Updated: recent}, {Updated: old}}
	if got := newestOf(ms); !got.Equal(recent) {
		t.Fatalf("newestOf = %v, want %v", got, recent)
	}
	if got := newestOf(nil); !got.IsZero() {
		t.Fatalf("no sessions, no date, got %v", got)
	}
}
