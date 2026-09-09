package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The npm package and the extension `deja install pi-auto` writes are the same
// integration reaching pi two ways, and they drifted: the package had
// session_start and before_agent_start only, so anyone who installed from npm
// got no repair after a failed command, no read-before-edit line, and nothing
// back after a compaction — features that had shipped months earlier (#3180).
func TestThePiPackageWiresTheSameEventsAsTheInstaller(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := string(b)
	installer := piExtensionTS("/usr/local/bin/deja")

	for _, event := range piEventsIn(installer) {
		if !strings.Contains(pkg, `pi.on("`+event+`"`) {
			t.Errorf("the package does not handle %q, which the installer's extension does", event)
		}
	}
	// And nothing the installer does not: a handler only the package has is
	// the same drift facing the other way.
	for _, event := range piEventsIn(pkg) {
		if !strings.Contains(installer, `pi.on("`+event+`"`) {
			t.Errorf("the package handles %q and the installer's extension does not", event)
		}
	}
}

func piEventsIn(src string) []string {
	var out []string
	for _, part := range strings.Split(src, `pi.on("`)[1:] {
		if i := strings.IndexByte(part, '"'); i > 0 {
			out = append(out, part[:i])
		}
	}
	return out
}
