package sources

import (
	"strings"
	"testing"
)

func names(hs []Harness) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

func TestDejaStoresNarrowsTheRegistry(t *testing.T) {
	full := len(Registry())
	if full < 30 {
		t.Fatalf("the registry has %d harnesses; this test is reading the wrong thing", full)
	}

	t.Setenv(StoresEnv, "claude,codex")
	got := names(Registry())
	if len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Fatalf("DEJA_STORES=claude,codex left %v", got)
	}
	// Load order is the registry's own, not the order they were written in the
	// variable: the index walks stores in that order and the path matches rely
	// on it staying deterministic.
	t.Setenv(StoresEnv, "codex, claude")
	if got := names(Registry()); len(got) != 2 || got[0] != "claude" {
		t.Fatalf("order came from the variable, not the registry: %v", got)
	}

	// What deja can read is not what it is reading now. The count in the
	// documentation, the set --harness accepts and the names it suggests all
	// come from the full list; narrowing one run must not shrink any of them.
	if n := len(AllHarnesses()); n != full {
		t.Errorf("AllHarnesses is %d under a narrowed selection; it should stay %d", n, full)
	}
	if !IsKnownHarness("zed") {
		t.Error("--harness zed is rejected while DEJA_STORES silences zed; a known store stays a known name")
	}
	if len(HarnessNames()) != full {
		t.Error("HarnessNames shrank with the selection")
	}
}

func TestDejaStoresKnowsTheNotesStoreByTheNamePeopleSee(t *testing.T) {
	// Every surface calls it "notes"; only the registry entry is called "deja".
	// This is the store a loop over the published registry misses, which is why
	// the variable exists (#3802).
	t.Setenv(StoresEnv, "notes")
	if got := names(Registry()); len(got) != 1 || got[0] != "deja" {
		t.Fatalf("DEJA_STORES=notes left %v", got)
	}
	if StoreSilenced("deja") {
		t.Error("the notes store is silenced under DEJA_STORES=notes")
	}
	if !StoreSilenced("claude") {
		t.Error("claude is not silenced under DEJA_STORES=notes")
	}
	if err := StoresSelectionError(); err != nil {
		t.Errorf("notes is rejected as a name: %v", err)
	}
}

func TestDejaStoresUnsetReadsEverything(t *testing.T) {
	t.Setenv(StoresEnv, "")
	if len(Registry()) != len(AllHarnesses()) {
		t.Error("an empty DEJA_STORES narrowed the registry; empty means the default, which is everything")
	}
	if StoreSilenced("claude") {
		t.Error("a store is silenced with no selection in force")
	}
	if _, ok := StoresSelected(); ok {
		t.Error("an empty variable reports a selection in force")
	}
	// A variable holding only separators is the same mistake as an empty one,
	// and reading it as "no stores" would index nothing at all.
	t.Setenv(StoresEnv, " , ")
	if len(Registry()) != len(AllHarnesses()) {
		t.Error("DEJA_STORES=\" , \" silenced every store")
	}
}

func TestDejaStoresRefusesANameThatIsNotAStore(t *testing.T) {
	t.Setenv(StoresEnv, "claude,claud")
	err := StoresSelectionError()
	if err == nil {
		t.Fatal("a typo in DEJA_STORES is accepted; it silences every store and the run looks like a machine with no history")
	}
	if !strings.Contains(err.Error(), "claud,") && !strings.Contains(err.Error(), "claud ") {
		t.Errorf("the error does not name the typo: %v", err)
	}
	// And it says what the names are, because nobody can guess thirty-five of
	// them — under the name each is printed by, not the registry's own.
	if !strings.Contains(err.Error(), "notes") {
		t.Errorf("the error lists the registry's internal name for the notes store, or none at all: %v", err)
	}

	t.Setenv(StoresEnv, "claude,notes")
	if err := StoresSelectionError(); err != nil {
		t.Errorf("a correct selection is refused: %v", err)
	}
}

func TestDejaStoresReportsWhatItSelected(t *testing.T) {
	t.Setenv(StoresEnv, "notes,claude")
	got, ok := StoresSelected()
	if !ok {
		t.Fatal("a selection is in force and StoresSelected says otherwise")
	}
	if strings.Join(got, ",") != "claude,deja" {
		t.Errorf("StoresSelected returned %v", got)
	}
}
