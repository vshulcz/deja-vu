package main

import (
	"fmt"
	"io"
	"os"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Zed is the one harness where deja can end up wired twice with nothing to
// catch it. `deja install zed` writes a context server into settings.json and
// skips when the extension is already there — but a user who installs the
// extension afterwards has both, and neither side can see the other: the
// extension runs inside Zed's WASM sandbox and reads no settings.
//
// Nothing breaks. Both entries start `deja mcp` and answer the same, so the
// cost is the agent seeing every tool twice — invisible unless something says
// it out loud.
func doctorDoubleWiring(w io.Writer) {
	if !zedWiredTwice(sources.ZedSettingsPath()) {
		return
	}
	fmt.Fprintf(w, "  %-12s %-11s %s\n", "zed", "twice",
		"deja is in settings and the Zed extension is installed — `deja uninstall zed` keeps the extension")
}

// zedWiredTwice reports deja's own context server and the extension's sitting
// in the same settings file. The extension's key is its id, which Zed writes
// when the extension is installed.
func zedWiredTwice(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := string(b)
	open := zedTopLevelOpen(text)
	if open < 0 {
		return false
	}
	block := zedFindKey(text, open+1, zedServerKey)
	if block == nil {
		return false
	}
	return zedFindKey(text, block.valueOpen+1, "deja") != nil &&
		zedFindKey(text, block.valueOpen+1, zedExtensionServerID) != nil
}
