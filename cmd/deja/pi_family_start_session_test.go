package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hook-context marks the session that is starting as live, which is what keeps
// it out of its own MCP recall. The pi-family extensions asked for the digest
// with empty stdin, so the mark went on no session and the asking session
// ranked first in its own first turn — the gap #4246 closed for Hermes and
// #4273 for the opencode package (#4394).
func TestPiFamilyStartSendsTheSession(t *testing.T) {
	pkg, err := os.ReadFile(filepath.Join("..", "..", "extensions", "pi", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, src, id string }{
		{"pi-auto", piExtensionTS("/bin/deja"), "sessionID()"},
		{"omp-auto", ompExtensionJS("/bin/deja"), "sessionID()"},
		{"prime-auto", primeExtensionTS("/bin/deja"), "sessionID()"},
		{"pi package", string(pkg), "session"},
	} {
		calls := strings.Split(tc.src, `run(["hook-context"]`)[1:]
		if len(calls) == 0 {
			t.Errorf("%s: no hook-context call at all", tc.name)
			continue
		}
		for _, call := range calls {
			end := strings.Index(call, ");")
			if end < 0 {
				end = len(call)
			}
			payload := call[:end]
			if !strings.Contains(payload, "session_id: "+tc.id) || !strings.Contains(payload, "cwd:") {
				t.Errorf("%s: hook-context is asked without the session and its directory: run([\"hook-context\"]%s)", tc.name, payload)
			}
		}
	}
}
