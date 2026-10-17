package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// payloadNamesCwd is a payload object that carries cwd beside the session id.
var payloadNamesCwd = regexp.MustCompile(`session_id"?:[^}]*\bcwd\b`)

// The plugin hosts ended a session with a bare id, so the session-end hook
// could not tell which project's start digest to refresh, and the next
// session there was served a digest without the one that just ended (#5121).
// Every call they make to hook-session-end names the directory now.
func TestPluginHostsNameTheProjectWhenASessionEnds(t *testing.T) {
	sources := map[string]string{
		"opencode 2.x":   opencodePluginJS("/bin/deja"),
		"opencode 1.x":   opencodeLegacyPluginJS("/bin/deja"),
		"kilocode":       kilocodePluginJS("/bin/deja"),
		"pi":             piExtensionTS("/bin/deja"),
		"prime":          primeExtensionTS("/bin/deja"),
		"omp":            ompExtensionJS("/bin/deja"),
		"openclaw":       openclawPluginJS("/bin/deja"),
		"hermes plugin":  hermesPluginPy("/bin/deja"),
		"hermes memory":  hermesMemoryPy("/bin/deja"),
		"cline":          clinePluginJS("/bin/deja"),
		"amp":            ampPluginTS("/bin/deja"),
		"deepseek (dsh)": dshAutoJS("/bin/deja"),
	}
	// The packages shipped outside `deja install` make the same call.
	for _, rel := range []string{"opencode/index.js", "dsh/index.js", "openclaw/index.mjs", "hermes/__init__.py", "pi/index.ts"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "extensions", rel))
		if err != nil {
			t.Fatal(err)
		}
		sources["extensions/"+rel] = string(b)
	}
	for name, src := range sources {
		calls := 0
		for _, line := range strings.Split(src, "\n") {
			code := strings.TrimSpace(line)
			if !strings.Contains(code, "hook-session-end") || !strings.Contains(code, "session_id") || strings.HasPrefix(code, "//") || strings.HasPrefix(code, "#") {
				continue
			}
			calls++
			if !payloadNamesCwd.MatchString(code) {
				t.Errorf("%s ends a session without its project: %s", name, code)
			}
		}
		if calls == 0 {
			t.Errorf("%s: no hook-session-end call found, so this proves nothing", name)
		}
	}
}
