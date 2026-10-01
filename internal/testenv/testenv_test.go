package testenv

import (
	"os"
	"testing"
)

func TestRedirects(t *testing.T) {
	for name, want := range map[string]bool{
		"HERMES_HOME":                  true,
		"OPENCLAW_STATE_DIR":           true,
		"CLINE_MCP_SETTINGS_PATH":      true,
		"CLAUDE_CODE_PROJECT_DIR_NAME": true,
		"DEJA_HERMES_PG_DSN":           true,
		"GITHUB_TOKEN":                 true,
		"PATH":                         false,
		"HOME":                         false,
		"GOPATH":                       false,
		"GIT_EXEC_PATH":                false,
		"XDG_CACHE_HOME":               false,
		"TERM":                         false,
		"DEJA_MCP_ORPHAN_HELPER":       false,
	} {
		if got := Redirects(name); got != want {
			t.Errorf("Redirects(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestScrubKeepsHelperSignalsAndPins(t *testing.T) {
	t.Setenv("HERMES_HOME", "/real/hermes")
	t.Setenv("DEJA_PASS_HOME", "/parent/home")
	t.Setenv("DEJA_EMBED_URL", "http://real")
	t.Setenv("DEJA_INDEX_DIR", "/real/index")
	Scrub(map[string]string{"DEJA_INDEX_DIR": "/tmp/index"}, "DEJA_PASS_")
	if _, ok := os.LookupEnv("HERMES_HOME"); ok {
		t.Error("HERMES_HOME survived")
	}
	if _, ok := os.LookupEnv("DEJA_EMBED_URL"); ok {
		t.Error("DEJA_EMBED_URL survived")
	}
	if got := os.Getenv("DEJA_PASS_HOME"); got != "/parent/home" {
		t.Errorf("kept prefix lost: %q", got)
	}
	if got := os.Getenv("DEJA_INDEX_DIR"); got != "/tmp/index" {
		t.Errorf("pin not applied: %q", got)
	}
}
