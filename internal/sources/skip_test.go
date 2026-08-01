package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// An index run narrates every store it read; staying silent about the one it
// could not made an empty deja look like an empty history (#794). The note
// must stay off on a machine that never used the harness, where it would be
// noise about a tool nobody needs.
func TestSkipReasonNamesTheMissingToolOnlyWhenTheStoreIsThere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("PATH", home) // no sqlite3 on it
	if SQLite3Available() {
		t.Skip("sqlite3 still resolvable")
	}

	for _, h := range []string{"opencode", "cursor", "grok", "hermes", "goose", "claude"} {
		if got := SkipReason(h); got != "" {
			t.Errorf("%s with no store on disk: %q, want no note", h, got)
		}
	}

	db := OpencodeDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SkipReason("opencode"); got != "sqlite3 CLI not found" {
		t.Errorf("opencode with a store and no CLI: %q", got)
	}
	// A harness deja reads without the CLI is never explained away by it.
	if got := SkipReason("claude"); got != "" {
		t.Errorf("claude: %q, want no note", got)
	}
}

// With the CLI present there is nothing to explain, store or no store.
func TestSkipReasonSilentWhenTheToolIsThere(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("no sqlite3 on this machine")
	}
	for _, h := range []string{"opencode", "cursor", "grok", "hermes", "goose"} {
		if got := SkipReason(h); got != "" {
			t.Errorf("%s: %q, want no note", h, got)
		}
	}
}
