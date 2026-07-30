package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func filesMsg(paths ...string) model.Message {
	t := ""
	for i, p := range paths {
		if i > 0 {
			t += "\n"
		}
		t += p
	}
	return model.Message{Role: RoleFiles, Text: t}
}

// A repo is a directory with a .git in it; a folder is not. The fixture builds
// two so the majority rule has something to choose between.
func repoFixture(t *testing.T) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(tmp, name, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tmp, name, "internal"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(tmp, "alpha"), filepath.Join(tmp, "beta")
}

func TestProjectFromPathsTakesTheMajorityRepo(t *testing.T) {
	a, b := repoFixture(t)
	ms := []model.Message{
		filesMsg(filepath.Join(a, "internal", "x.go"), filepath.Join(a, "internal", "y.go")),
		filesMsg(filepath.Join(a, "main.go")),
		filesMsg(filepath.Join(b, "main.go")),
	}
	got := projectFromPaths(ms)
	want := filepath.Join(filepath.Base(filepath.Dir(a)), "alpha")
	if got != want {
		t.Fatalf("project = %q, want %q", got, want)
	}
}

// A session genuinely split between two repositories should keep whatever name
// it already had rather than be filed under a coin flip.
func TestProjectFromPathsDeclinesWithoutAMajority(t *testing.T) {
	a, b := repoFixture(t)
	ms := []model.Message{
		filesMsg(filepath.Join(a, "one.go"), filepath.Join(a, "two.go")),
		filesMsg(filepath.Join(b, "one.go"), filepath.Join(b, "two.go")),
	}
	if got := projectFromPaths(ms); got != "" {
		t.Fatalf("project = %q, want no answer", got)
	}
}

func TestProjectFromPathsIgnoresTheAgentsOwnFiles(t *testing.T) {
	a, _ := repoFixture(t)
	ms := []model.Message{
		filesMsg("/Users/x/.claude/projects/-Users-x/scratchpad/probe.py"),
		filesMsg("/private/tmp/claude-501/-Users-x/tasks/run.output"),
		filesMsg(filepath.Join(a, "main.go")),
	}
	// One real path is not three, so there is not enough evidence to override.
	if got := projectFromPaths(ms); got != "" {
		t.Fatalf("project = %q, want no answer from one real path", got)
	}
	if !agentScratch("/Users/x/.claude/projects/p/scratchpad/probe.py") {
		t.Fatal("a scratch clone under .claude must not count as a project")
	}
}

func TestProjectFromPathsNeedsEvidence(t *testing.T) {
	a, _ := repoFixture(t)
	ms := []model.Message{filesMsg(filepath.Join(a, "main.go"))}
	if got := projectFromPaths(ms); got != "" {
		t.Fatalf("project = %q, want no answer from a single file", got)
	}
	if got := projectFromPaths(nil); got != "" {
		t.Fatalf("project = %q, want no answer with no files", got)
	}
}

func TestRepoRootTerminatesAtTheVolumeRoot(t *testing.T) {
	// The walk used to stop only at "/", so on Windows it spun forever at the
	// volume root. A timeout rather than an assertion: the failure mode is a
	// hang, and it only reproduces on the OS whose root is not "/".
	done := make(chan string, 1)
	go func() {
		done <- repoRoot(filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "nowhere", "deep"))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("repoRoot did not terminate at the volume root")
	}
}
