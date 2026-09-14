package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// `deja doctor` names a package — `needs-sqlite3`, `needs-zstd` — for a store it
// found and could not read. For a harness the reader does not use that is
// advice for a problem they do not have, and the exclude file had no way to say
// so: it took project patterns only (#3499).
func TestAHarnessCanBeExcluded(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "")
	path := filepath.Join(dir, "deja", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("")
	if HarnessExcluded("opencode") {
		t.Error("an empty exclude file excluded a store")
	}

	write("# stores this reader will never want read\nharness:opencode\nharness:Zed\n")
	for _, name := range []string{"opencode", "zed", "ZED"} {
		if !HarnessExcluded(name) {
			t.Errorf("%q is in the exclude file and was not excluded", name)
		}
	}
	if HarnessExcluded("claude") {
		t.Error("a store nobody named was excluded")
	}

	// The prefix keeps the two namespaces apart in both directions. A harness
	// line carries its prefix into the project matcher, where it matches no
	// project anyone has:
	write("harness:continue\n")
	for _, project := range []string{"continue", "continue-deploy", "work/continue-api"} {
		if ExcludedProject(project) {
			t.Errorf("a harness line excluded the project %q", project)
		}
	}
	// and a project pattern does not exclude the store of the same name, which
	// is the direction that would otherwise silently stop reading a harness.
	write("opencode\n")
	if HarnessExcluded("opencode") {
		t.Error("a project pattern excluded the store of that name")
	}
	if !ExcludedProject("work/opencode") {
		t.Error("the project pattern stopped working")
	}

	// The environment says the same thing, for a machine where the file is not
	// the place to put it — CI, a container, a one-off run.
	write("")
	t.Setenv("DEJA_EXCLUDE_HARNESSES", " Crush , goose ")
	for _, name := range []string{"crush", "goose"} {
		if !HarnessExcluded(name) {
			t.Errorf("DEJA_EXCLUDE_HARNESSES did not exclude %q", name)
		}
	}
	if HarnessExcluded("cline") {
		t.Error("the environment excluded a store it does not name")
	}
}
