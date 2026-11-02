package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func filesMsg(paths ...string) model.Message {
	t := ""
	var tSb14 strings.Builder
	for i, p := range paths {
		if i > 0 {
			tSb14.WriteString("\n")
		}
		tSb14.WriteString(p)
	}
	t += tSb14.String()
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
	// "/" rather than filepath.Join: a project name is an identifier and is
	// spelled the same on every platform.
	want := filepath.Base(filepath.Dir(a)) + "/alpha"
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

func TestResolveHomeOrEmptyAndDirProjectACL(t *testing.T) {
	// A session started from a home directory (~ -> "Users/alice") that opens
	// with a deja handoff prompt resolves its project from the handoff prompt
	// even before touching 3 files.
	handoffMsg := model.Message{
		Role: "user",
		Text: "Continuing a task handed off from a claude session (project goprojects/deja-vu, 2026-10-01).",
	}
	s := model.Session{
		Harness:  "codex",
		ID:       "s1",
		Project:  "Users/alice",
		Messages: []model.Message{handoffMsg},
	}
	resolveHomeOrEmptyProject(&s)
	if s.Project != "goprojects/deja-vu" {
		t.Fatalf("resolveHomeOrEmptyProject(handoff) = %q; want goprojects/deja-vu", s.Project)
	}

	// Same for directory-derived sessions (claude, mcode) started from ~.
	s2 := model.Session{
		Harness:  "claude",
		ID:       "s2",
		Project:  "home/alice",
		Messages: []model.Message{handoffMsg},
	}
	resolveDirProject(&s2)
	if s2.Project != "goprojects/deja-vu" || s2.ProjectFromDir {
		t.Fatalf("resolveDirProject(handoff from home) = (%q, %v); want (goprojects/deja-vu, false)", s2.Project, s2.ProjectFromDir)
	}

	// parseCompactionSession preserves non-Claude parser project attribution
	// when workspace is empty (e.g. transcriptHead on Copilot/Qwen).
	copilotDir := filepath.Join(t.TempDir(), "1111-2222")
	if err := os.MkdirAll(copilotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copilotPath := filepath.Join(copilotDir, "events.jsonl")
	copilotData := strings.Join([]string{
		`{"type":"session.start","timestamp":"2026-10-01T10:00:00Z","data":{"sessionId":"1111-2222","startTime":"2026-10-01T10:00:00Z","context":{"cwd":"/work/org/app"}}}`,
		`{"type":"user.message","timestamp":"2026-10-01T10:00:01Z","data":{"content":"hello"}}`,
	}, "\n") + "\n"
	compSess, err := parseCompactionSession(copilotPath, "copilot", "", []byte(copilotData))
	if err != nil {
		t.Fatal(err)
	}
	if compSess.Project != "org/app" {
		t.Fatalf("parseCompactionSession(copilot, workspace=\"\").Project = %q; want org/app", compSess.Project)
	}
}
