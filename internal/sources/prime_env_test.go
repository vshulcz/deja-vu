package sources

import (
	"path/filepath"
	"testing"
)

// PRIME_AGENT_CODING_AGENT_DIR moves prime-agent's whole user directory —
// settings, extensions, skills and the default session root — and prime's own
// docs name it as the override (config.ts getAgentDir). deja read only the
// session variables, so on a relocated install it wrote settings.json where
// prime would not look and called it wired (#3276).
func TestPrimeAgentDirMovesConfigAndSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_SESSION_DIR", "")
	t.Setenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR", "")
	moved := filepath.Join(home, "elsewhere", "agent")
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", moved)
	if got := PrimeConfigDir(); got != moved {
		t.Errorf("config dir = %q, want %q", got, moved)
	}
	if got, want := PrimeRoot(), filepath.Join(moved, "sessions"); got != want {
		t.Errorf("session root = %q, want %q", got, want)
	}
}

// With both session variables set prime reads PRIME_AGENT_SESSION_DIR first and
// the legacy PRIME_AGENT_CODING_AGENT_SESSION_DIR second (config.ts:628); deja
// had them the other way round, so the two disagreed on the root (#3277).
func TestPrimeSessionDirPrecedenceMatchesPrime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "")
	t.Setenv("PRIME_AGENT_SESSION_DIR", filepath.Join(home, "new"))
	t.Setenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR", filepath.Join(home, "legacy"))
	if got, want := PrimeRoot(), filepath.Join(home, "new"); got != want {
		t.Errorf("root = %q, want the current variable %q", got, want)
	}
}

// A leading ~ is prime's own spelling for the variable (its README shows it);
// prime expands it, so deja does too.
func TestPrimeAgentDirExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "~/custom/agent")
	if got, want := PrimeConfigDir(), filepath.Join(home, "custom", "agent"); got != want {
		t.Errorf("config dir = %q, want %q", got, want)
	}
}
