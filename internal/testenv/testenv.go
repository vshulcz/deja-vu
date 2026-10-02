// Package testenv keeps a test binary off the developer's real agent stores.
package testenv

import (
	"os"
	"regexp"
	"strings"
)

// location matches a variable that names a place: a harness home, a store
// root, a database or a config file.
var location = regexp.MustCompile(`^[A-Z0-9]+(_[A-Z0-9]+)*_(HOME|DIR|DIRS|DIR_NAME|ROOT|ROOTS|DB|FILE|PATH|CONFIG)$`)

// toolchain names match location but point at the build tools, not at an
// agent; the suite needs them as the developer has them.
var toolchain = map[string]bool{
	"GIT_EXEC_PATH":     true,
	"PKG_CONFIG_PATH":   true,
	"SSL_CERT_FILE":     true,
	"SSL_CERT_DIR":      true,
	"LD_LIBRARY_PATH":   true,
	"DYLD_LIBRARY_PATH": true,
	"XDG_CACHE_HOME":    true,
	"XDG_RUNTIME_DIR":   true,
}

// secrets would let a test reach a real account.
var secrets = []string{"GITHUB_TOKEN", "OPENAI_API_KEY"}

// Redirects reports whether name can point deja at something outside the test.
// A name ending in _HELPER is a test talking to a copy of its own binary
// (DEJA_MCP_ORPHAN_HELPER), so it is never scrubbed: losing it made that child
// exit at once, which only Windows noticed.
func Redirects(name string) bool {
	if toolchain[name] || strings.HasSuffix(name, "_HELPER") {
		return false
	}
	for _, s := range secrets {
		if name == s {
			return true
		}
	}
	return strings.HasPrefix(name, "DEJA_") || location.MatchString(name)
}

// Scrub unsets every variable Redirects names, except those starting with one
// of keep, then sets pinned. Hand-kept lists drifted twice: the notes keys
// (#1141) and HERMES_HOME (#4178) were honoured by the code before any
// TestMain cleared them, and the suite wrote into the developer's real stores.
// keep is for the variables a test passes to a copy of its own binary under
// a name that does not end in _HELPER.
func Scrub(pinned map[string]string, keep ...string) {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !Redirects(name) || kept(name, keep) {
			continue
		}
		if err := os.Unsetenv(name); err != nil {
			panic(err)
		}
	}
	for name, value := range pinned {
		if err := os.Setenv(name, value); err != nil {
			panic(err)
		}
	}
}

func kept(name string, keep []string) bool {
	for _, p := range keep {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
