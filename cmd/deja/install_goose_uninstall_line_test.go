package main

import (
	"strings"
	"testing"
)

// The install names the hooks plugin it creates; the uninstall removed it in
// silence (#3208).
func TestUninstallGooseAutoNamesTheHooksItRemoves(t *testing.T) {
	gooseHomeForTest(t)
	if _, err := installGooseAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	res, err := installGooseAuto("/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Path+" "+res.Note, "hooks.json") {
		t.Fatalf("uninstall did not name the hooks plugin: path=%q note=%q", res.Path, res.Note)
	}
}
