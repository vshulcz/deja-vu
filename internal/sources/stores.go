package sources

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// StoresEnv names the stores deja is allowed to read. Everything else resolves
// to nothing — no path, no warning, no "1 path could not be read".
//
// The alternative, and what every integrator and every stand in this repository
// wrote by hand until now, is a loop over forty-five DEJA_*_ROOT/_DB/_ROOTS
// names pointed at an empty directory. That loop is derived from the published
// registry, which documents harnesses, so it misses the one store that is not a
// harness: deja's own promoted notes. A stand built that way read the
// developer's real notes while believing it was hermetic, and once indexed
// 1,474 real sessions into a test index (#3802).
//
// A hook or an MCP server is started by somebody else's process, which is why
// this is an environment variable first: there is no command line to pass a
// flag on.
const StoresEnv = "DEJA_STORES"

// storesSelection reads the variable. The second result is false when it is
// unset or empty, which means "read everything" — the default deja has always
// had.
func storesSelection() (map[string]bool, bool) {
	raw, ok := os.LookupEnv(StoresEnv)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, false
	}
	want := map[string]bool{}
	for _, name := range strings.Split(raw, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		// "notes" is what deja calls its own store everywhere a person reads
		// it — in the index run, in doctor, in `deja sources`. Nobody would
		// guess that the registry entry behind it is called "deja".
		if name == "notes" {
			name = "deja"
		}
		want[name] = true
	}
	if len(want) == 0 {
		return nil, false
	}
	return want, true
}

// StoresSelected returns the store names deja will read, sorted, and whether
// the selection is in force at all. For the surfaces that tell a person what is
// happening rather than doing it.
func StoresSelected() ([]string, bool) {
	want, ok := storesSelection()
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(want))
	for name := range want {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, true
}

// StoreSilenced reports whether a store is one the selection excludes. Callers
// that keep their own list of stores — doctor's checks, which carry paths and a
// parser per store — ask this instead of filtering the registry.
func StoreSilenced(name string) bool {
	want, ok := storesSelection()
	if !ok {
		return false
	}
	return !want[name]
}

// StoresSelectionError reports a name in the variable that is not a store. A
// typo silences every store instead of narrowing to one, and the run that
// follows looks like a machine with no history rather than like a mistake —
// so this is checked once, loudly, before any command runs.
func StoresSelectionError() error {
	want, ok := storesSelection()
	if !ok {
		return nil
	}
	known := map[string]bool{}
	for _, h := range allHarnesses() {
		known[h.Name] = true
	}
	var bad []string
	for name := range want {
		if !known[name] {
			bad = append(bad, name)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	names := make([]string, 0, len(known))
	for name := range known {
		if name == "deja" {
			name = "notes"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Errorf("%s names %s, which %s not a store deja reads — the names are: %s",
		StoresEnv, strings.Join(bad, ", "), plural(len(bad)), strings.Join(names, ", "))
}

func plural(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
