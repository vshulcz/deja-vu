package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Codex pins each approved hook in config.toml by its place in hooks.json. An
// uninstall left the pins for deja's hooks behind, so the file did not come
// back and a reinstall started trusted without a review (#4183). The reader's
// own hook moves up when deja's goes, and its pin has to move with it.
func TestUninstallTakesCodexTrustPinsWithTheHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEJA_CODEX_ROOT", "")
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codex, "hooks.json")
	cfgPath := filepath.Join(codex, "config.toml")
	cfg := "# mine\nmodel = \"luna\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	// The reader adds a SessionStart hook of their own after deja's, and
	// codex pins all of them once the user approves.
	b, _ := os.ReadFile(hooksPath)
	mine := `{"hooks":[{"type":"command","command":"~/bin/mine.sh"}]}`
	withMine := strings.Replace(string(b), `"SessionStart": [`, `"SessionStart": [`+"\n      "+mine+",", 1)
	if withMine == string(b) {
		t.Fatalf("could not place the reader's hook:\n%s", b)
	}
	// Theirs first, deja's second.
	if err := os.WriteFile(hooksPath, []byte(withMine), 0o600); err != nil {
		t.Fatal(err)
	}
	pin := func(event string, g int) string {
		return "\n[hooks.state." + strconv.Quote(hooksPath+":"+event+":"+strconv.Itoa(g)+":0") + "]\ntrusted_hash = \"sha256:" + event + strconv.Itoa(g) + "\"\n"
	}
	approved := cfg + "\n[hooks.state]\n" + pin("session_start", 0) + pin("session_start", 1) + pin("user_prompt_submit", 0) + pin("pre_tool_use", 0) + pin("post_tool_use", 0) + pin("pre_compact", 0)
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfgPath)
	want := cfg + "\n[hooks.state]\n" + pin("session_start", 0)
	if string(got) != want {
		t.Errorf("after uninstall config.toml is\n%s\nwant\n%s", got, want)
	}

	// A table codex writes after the pins keeps the blank line in front of it.
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	tail := "\n[tui.model_availability_nux]\n\"gpt-5.6-sol\" = 1\n"
	approved = cfg + "\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n\n[hooks.state]\n" + pin("session_start", 0) + pin("pre_compact", 0) + tail
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, cfgPath), cfg+"\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n"+tail; got != want {
		t.Errorf("after uninstall config.toml is\n%q\nwant\n%q", got, want)
	}

	// And with nothing of the reader's left, the file comes back as it was.
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	approved = cfg + "\n[hooks.state]\n" + pin("session_start", 0) + pin("pre_compact", 0)
	if err := os.WriteFile(cfgPath, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != cfg {
		t.Errorf("after uninstall config.toml is\n%q\nwant\n%q", got, cfg)
	}
}

func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The shapes a review found broken in the first cut of #4183.
func TestRewriteCodexHookTrustKeepsWhatIsNotAPin(t *testing.T) {
	file := "/h/.codex/hooks.json"
	paths := map[string]bool{file: true}
	pin := func(pos string, extra string) string {
		return "[hooks.state." + strconv.Quote(file+":"+pos) + "]" + extra + "\ntrusted_hash = \"" + pos + "\"\n"
	}
	gone := map[codexHookPos]*codexHookPos{{"session_start", 0, 0}: nil}
	up := &codexHookPos{"session_start", 0, 0}

	// A comment above the next table stays with it.
	in := "model = \"x\"\n\n[hooks.state]\n\n" + pin("session_start:0:0", "") + "\n# my work profile\n[profiles.work]\nmodel = \"y\"\n"
	if got, want := rewriteCodexHookTrust(in, paths, gone), "model = \"x\"\n\n# my work profile\n[profiles.work]\nmodel = \"y\"\n"; got != want {
		t.Errorf("comment above the next table:\ngot  %q\nwant %q", got, want)
	}

	// A move onto a key a stale pin already holds does not write it twice.
	moves := map[codexHookPos]*codexHookPos{{"session_start", 0, 0}: nil, {"session_start", 1, 0}: up}
	in = "[hooks.state]\n\n" + pin("session_start:0:0", "") + "\n" + pin("session_start:1:0", "") + "\n" + pin("session_start:0:0", "")
	got := rewriteCodexHookTrust(in, paths, moves)
	if n := strings.Count(got, strconv.Quote(file+":session_start:0:0")); n != 1 {
		t.Errorf("key written %d times:\n%s", n, got)
	}
	if !strings.Contains(got, `trusted_hash = "session_start:1:0"`) {
		t.Errorf("the moved pin was lost:\n%s", got)
	}

	// A literal-string key — codex's form for a path with a backslash — is
	// read, and a renamed one is written back the same way with its comment.
	win := `C:\Users\x\.codex\hooks.json`
	lit := "[hooks.state.'" + win + ":session_start:1:0'] # mine\r\ntrusted_hash = \"b\"\r\n"
	got = rewriteCodexHookTrust(lit, map[string]bool{win: true}, map[codexHookPos]*codexHookPos{{"session_start", 1, 0}: up})
	if want := "[hooks.state.'" + win + ":session_start:0:0'] # mine\r\ntrusted_hash = \"b\"\r\n"; got != want {
		t.Errorf("literal key:\ngot  %q\nwant %q", got, want)
	}
}

// Matching positions stays linear in the hooks: an insertion sort inside the
// loop made 1,500 hooks take seconds.
func TestCodexTrustMovesIsFastOnALongHooksFile(t *testing.T) {
	var groups []any
	for i := 0; i < 3000; i++ {
		groups = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "c" + strconv.Itoa(i)}}})
	}
	before := map[string]any{"hooks": map[string]any{"SessionStart": groups}}
	after := map[string]any{"hooks": map[string]any{"SessionStart": groups[1:]}}
	start := time.Now()
	moves := codexTrustMoves(before, after)
	if took := time.Since(start); took > time.Second {
		t.Errorf("3000 hooks took %v", took)
	}
	if to := moves[codexHookPos{"session_start", 2999, 0}]; to == nil || to.group != 2998 {
		t.Errorf("last hook moved to %v, want group 2998", to)
	}
}

// Text inside a multi-line string is not a header, and a comment at the end
// of the file is not part of the last pin.
func TestRewriteCodexHookTrustReadsTOMLNotLines(t *testing.T) {
	file := "/h/hooks.json"
	paths := map[string]bool{file: true}
	moves := map[codexHookPos]*codexHookPos{{"session_start", 0, 0}: nil, {"session_start", 1, 0}: {"session_start", 0, 0}}
	in := "[profile]\ninstr = \"\"\"\n[hooks.state.\"/h/hooks.json:session_start:0:0\"]\n\"\"\"\n"
	if got := rewriteCodexHookTrust(in, paths, moves); got != in {
		t.Errorf("a line inside a multi-line string was read as a pin:\n%q", got)
	}

	in = "model = \"x\"\n\n[hooks.state.\"/h/hooks.json:session_start:0:0\"]\ntrusted_hash = \"x\"\n# keep me at eof\n"
	if got, want := rewriteCodexHookTrust(in, paths, moves), "model = \"x\"\n# keep me at eof\n"; got != want {
		t.Errorf("comment at the end of the file:\ngot  %q\nwant %q", got, want)
	}

	if got := quoteTOMLKey("a\x7fb\"c\\d", false); got != `"a\u007Fb\"c\\d"` {
		t.Errorf("TOML quoting: %s", got)
	}
}

// An install that drops a second copy of deja's entry moves the reader's hook
// after it up a place, and the pin at that place is the dropped copy's. The
// pins have to follow on install as they do on uninstall (#4227).
func TestInstallDroppingADuplicateMovesTheReadersPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEJA_CODEX_ROOT", "")
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codex, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codex, "hooks.json")
	cfgPath := filepath.Join(codex, "config.toml")
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(readString(t, hooksPath)), &root); err != nil {
		t.Fatal(err)
	}
	hooks := root["hooks"].(map[string]any)
	ups := hooks["UserPromptSubmit"].([]any)
	mine := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "~/bin/mine.sh"}}}
	hooks["UserPromptSubmit"] = []any{ups[0], ups[0], mine}
	b, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(hooksPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	pin := func(g int, hash string) string {
		return "\n[hooks.state." + strconv.Quote(hooksPath+":user_prompt_submit:"+strconv.Itoa(g)+":0") + "]\ntrusted_hash = \"sha256:" + hash + "\"\n"
	}
	cfg := "model = \"luna\"\n\n[hooks.state]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg+pin(0, "deja")+pin(1, "copy")+pin(2, "mine")), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if got, want := readString(t, cfgPath), cfg+pin(0, "deja")+pin(1, "mine"); got != want {
		t.Errorf("after install config.toml is\n%s\nwant\n%s", got, want)
	}
}
