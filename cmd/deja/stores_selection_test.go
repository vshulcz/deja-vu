package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctor keeps its own list of stores — each row carries paths and a parser —
// so narrowing the registry does not narrow the report on its own. A silenced
// store used to appear as `1 path could not be read`, which is the noise
// DEJA_STORES exists to remove, reported about a store nobody asked for.
func TestDoctorDropsTheStoresDejaStoresSilences(t *testing.T) {
	full := len(doctorStoreChecks())
	if full < 30 {
		t.Fatalf("doctor checks %d stores; this test is reading the wrong list", full)
	}

	t.Setenv(sources.StoresEnv, "claude,notes")
	var got []string
	for _, c := range doctorStoreChecks() {
		got = append(got, c.name)
	}
	if len(got) != 2 {
		t.Fatalf("doctor still checks %v", got)
	}
	// "notes" in the variable, "deja" in the check list: the row keeps the
	// name the rest of the code uses.
	if got[0] != "claude" || got[1] != "deja" {
		t.Errorf("doctor checks %v", got)
	}
}

// The rows a person reads are printed by a different path than the JSON, and
// filtering one left the other listing thirty-two stores as missing under a
// line that had just said deja was reading two.
func TestDoctorRowsDropTheSilencedStores(t *testing.T) {
	hermeticEnv(t)
	t.Setenv(sources.StoresEnv, "claude,notes")
	var b strings.Builder
	doctorHarnesses(&b, t.TempDir())
	out := b.String()
	if !strings.Contains(out, "reading only claude, notes ("+sources.StoresEnv+")") {
		t.Errorf("doctor does not say which stores it is reading:\n%s", out)
	}
	for _, silenced := range []string{"codex", "cursor", "zed"} {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), silenced+" ") {
				t.Errorf("doctor prints a row for %s, which DEJA_STORES silences: %q", silenced, line)
			}
		}
	}
	if !strings.Contains(out, "claude") {
		t.Error("doctor dropped the store it was told to read")
	}
}

// The selection is validated once, before a command runs, and every command
// carries the refusal — a hook or the MCP server is started by somebody else's
// process, and a typo there would otherwise read as "no history on this
// machine".
func TestATypoInDejaStoresStopsTheCommand(t *testing.T) {
	t.Setenv(sources.StoresEnv, "claude,opencodee")
	err := run([]string{"sources"})
	if err == nil {
		t.Fatal("deja ran with a store name that does not exist")
	}
	if !strings.Contains(err.Error(), "opencodee") {
		t.Errorf("the refusal does not name the typo: %v", err)
	}
}
