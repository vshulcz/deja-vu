package main

import (
	"strings"
	"testing"
)

// `gemini extensions install https://github.com/vshulcz/deja-vu` does not clone
// the repository: it resolves the release asset for the platform, unpacks it and
// reads gemini-extension.json from the top of that archive. Until v0.18.0 the
// manifest lived only in git, so the command every gallery visitor is shown
// ended at "Configuration file not found" — the listing worked and the install
// did not, which is the worst of the two failures because nothing reports it.
//
// The same holds for GEMINI.md: the manifest names it as contextFileName, and an
// extension whose context file is missing installs and then teaches nothing.
func TestReleaseArchiveCarriesTheGeminiExtension(t *testing.T) {
	cfg := string(repoFile(t, ".goreleaser.yaml"))
	_, archives, ok := strings.Cut(cfg, "\narchives:")
	if !ok {
		t.Fatal(".goreleaser.yaml has no archives section")
	}
	// Stop at the next top-level key so a filename appearing elsewhere in the
	// file cannot pass for a shipped one.
	if end := strings.Index(archives, "\nchecksum:"); end >= 0 {
		archives = archives[:end]
	}
	for _, want := range []string{"gemini-extension.json", "GEMINI.md"} {
		if !strings.Contains(archives, want) {
			t.Errorf("the release archive does not carry %s, so `gemini extensions install` fails on the published asset", want)
		}
	}
}
