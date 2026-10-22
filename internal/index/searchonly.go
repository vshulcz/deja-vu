package index

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/vshulcz/deja-vu/internal/policy"
)

// SearchOnlyEnv builds the index for conversation search alone. The five
// derived tables a build mines after writing the records — fixes,
// co-occurrence, commands, command failures and session facts — are about 13%
// of a build's CPU, and someone who only searches pays for them on every pass
// (#5395).
const SearchOnlyEnv = "DEJA_SEARCH_ONLY"

// SearchOnly reports whether builds skip the derived tables:
// DEJA_SEARCH_ONLY=1, or "search_only": true in policy.json. The variable wins
// either way, so DEJA_SEARCH_ONLY=0 overrides the file.
func SearchOnly() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(SearchOnlyEnv))) {
	case "":
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
	return policy.SearchOnlyOn()
}

// BuiltSearchOnly reports whether the index on disk was built without the
// derived tables, so a command that reads one can say why it has nothing.
func BuiltSearchOnly(dir string) bool {
	if dir == "" {
		dir = DefaultDir()
	}
	m, err := readManifestCached(dir)
	return err == nil && m.SearchOnly
}

// settingBuilds counts the commands in flight that may change the setting an
// index was built under: `deja index` run by hand.
var settingBuilds atomic.Int32

// ApplySearchOnly marks this process as one whose build takes the setting
// from the environment and policy.json rather than from the index on disk.
// Only `deja index` run by hand does: a hook, the MCP server or the detached
// warmup run under whatever environment their host gave them, and letting
// them flip the setting rebuilt the whole store each time a shell and a hook
// disagreed. It returns the undo.
func ApplySearchOnly() func() {
	settingBuilds.Add(1)
	var once sync.Once
	return func() { once.Do(func() { settingBuilds.Add(-1) }) }
}

// searchOnlyFor is the setting a build of dir writes under: the one asked for
// on a first build or a `deja index`, the one on disk otherwise.
func searchOnlyFor(dir string) bool {
	if settingBuilds.Load() > 0 {
		return SearchOnly()
	}
	if m, err := readManifestCached(dir); err == nil {
		return m.SearchOnly
	}
	return SearchOnly()
}

// searchOnlyAfter is searchOnlyFor for a pass that holds the manifest on disk.
func searchOnlyAfter(old Manifest) bool {
	if settingBuilds.Load() > 0 {
		return SearchOnly()
	}
	return old.SearchOnly
}

// miningChanged reports a `deja index` asked for the other setting than the
// index was built under. Turning it on drops the tables and turning it off
// mines them from every session, and only a full pass does either.
func miningChanged(m Manifest) bool {
	return settingBuilds.Load() > 0 && m.SearchOnly != SearchOnly()
}
