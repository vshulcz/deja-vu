package index

import "testing"

// Seven parser fixes changed what the readers index (#3289); a store built
// at the previous version keeps the old rows until it is rebuilt, and the
// rebuild only happens when the version says so.
func TestAStoreFromBeforeTheParserFixesIsNotCurrent(t *testing.T) {
	dir := t.TempDir()
	if err := writeManifestOnly(dir, Manifest{Version: 34}); err != nil {
		t.Fatal(err)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("a version-34 store reads as current, so nothing re-reads the sources after the parser fixes")
	}
}
