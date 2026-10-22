package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Codex fires SessionEnd on exit — measured on codex 0.149.0, a logger under
// the event got {"session_id":…,"hook_event_name":"SessionEnd","reason":"other"}
// — and codex-auto wired nothing to it, so a session that had just quit stayed
// out of the next one's MCP recall for twenty minutes (#4545).
func TestCodexAutoWiresSessionEndAndUninstallRoundTrips(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CodexHome(), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "hooks": {
    "SessionEnd": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/usr/local/bin/mine --flush"
          }
        ]
      }
    ]
  }
}
`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := dejaHookCount(settingsHooks(t, b), "SessionEnd", "hook-session-end"); n != 1 {
		t.Fatalf("SessionEnd runs deja %d times, want once:\n%s", n, b)
	}
	if !strings.Contains(string(b), "/usr/local/bin/mine --flush") {
		t.Errorf("install took the reader's own SessionEnd hook:\n%s", b)
	}
	if st := codexHookWiringState(); len(st.missing) != 0 {
		t.Errorf("doctor reads codex events missing after a fresh install: %v", st.missing)
	}
	if _, err = installCodexHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(again, b) {
		t.Errorf("a second install changed the file:\n--- first\n%s\n--- second\n%s", b, again)
	}

	// What codex sends on exit clears that session's stamp.
	dir := t.TempDir()
	markSessionLive(dir, "01a0fb78-91d2-71f0-a72f-8b95f6ee7c8f")
	runHookSessionEnd(dir, strings.NewReader(`{"session_id":"01a0fb78-91d2-71f0-a72f-8b95f6ee7c8f","transcript_path":"/h/.codex/sessions/2026/10/02/rollout-x.jsonl","cwd":"/w","hook_event_name":"SessionEnd","reason":"other"}`))
	if liveSessionIDs(dir)["01a0fb78-91d2-71f0-a72f-8b95f6ee7c8f"] {
		t.Errorf("codex's SessionEnd payload left the session stamped: %v", readLiveSessions(dir))
	}

	if _, err = installCodexHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != seed {
		t.Errorf("uninstall did not give the file back as it was:\n--- got\n%s\n--- want\n%s", after, seed)
	}
}

// Cursor CLI 2026.09.02 runs sessionEnd with conversation_id and reason
// (bundle 4347.index.js, hasHooksForStep(sessionEnd)); cursor-auto wired
// nothing to it either (#4545).
func TestCursorAutoWiresSessionEndAndUninstallRoundTrips(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("CURSOR_CONFIG_DIR", "")
	path := filepath.Join(sources.CursorCLIHome(), "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "version": 1,
  "hooks": {
    "sessionEnd": [
      {
        "command": "/usr/local/bin/mine --flush"
      }
    ]
  }
}
`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installCursorHooks("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cursorCommands(t, cursorHooksFile(t, filepath.Dir(sources.CursorCLIHome())), "sessionEnd")
	want := hookExeInConfigs("/usr/local/bin/deja") + " hook-session-end"
	if len(got) != 2 || got[0] != "/usr/local/bin/mine --flush" || got[1] != want {
		t.Fatalf("sessionEnd = %q, want the reader's hook and %q:\n%s", got, want, b)
	}

	dir := t.TempDir()
	markSessionLive(dir, "c0ffee-conv")
	runHookSessionEnd(dir, strings.NewReader(`{"conversation_id":"c0ffee-conv","reason":"user_close","hook_event_name":"sessionEnd"}`))
	if liveSessionIDs(dir)["c0ffee-conv"] {
		t.Errorf("cursor's sessionEnd payload left the session stamped: %v", readLiveSessions(dir))
	}

	if _, err = installCursorHooks("/usr/local/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != seed {
		t.Errorf("uninstall did not give the file back as it was:\n--- got\n%s\n--- want\n%s", after, seed)
	}
}
