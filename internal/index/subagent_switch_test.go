package index

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

func museRec(stream, typ, payload string) string {
	return `{"schema_version":1,"id":"r","stream":{"kind":"session","id":"` + stream + `"},"sequence":1,"recorded_at":1791200000000000,"record_type":"event","payload_type":"` + typ + `","payload":` + payload + "}\n"
}

func writeMuseLog(t *testing.T, dir, id, workspaceType, prompt string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := museRec(id, workspaceType, `{"record":{"workspace_root":"/w/app"}}`) +
		museRec(id, "runtime.session", `{"kind":"run","event":{"kind":"started","prompt":"`+prompt+`"}}`)
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A transcript the listing leaves out while it is still on disk was not
// deleted. Turning DEJA_INCLUDE_SUBAGENTS off after a pass with it on kept
// every child in the index and said "N transcripts no longer on disk", about
// files sitting where they always were; a rebuild drops them. The incremental
// pass ends where the rebuild does (#4739).
func TestUnlistedTranscriptOnDiskIsNotKeptAsDeleted(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "muse", "sessions")
	parent := filepath.Join(root, "2026", "10", "05", "01a10be5-2bcd-7153-bacf-761a1bd3642b")
	child := filepath.Join(parent, "subagent", "01a10be5-3064-7d21-a788-cb874c890d18")
	writeMuseLog(t, parent, "01a10be5-2bcd-7153-bacf-761a1bd3642b", "runtime.session.metadata", "spawn a lister")
	writeMuseLog(t, child, "01a10be5-3064-7d21-a788-cb874c890d18", "session.workspace_branch.observed", "list the quuxchild files")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_MUSE_ROOTS", root)
	dir := filepath.Join(tmp, "index.db")

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxchild"}); len(ss) != 1 {
		t.Fatalf("with the switch on the child is not indexed: %#v", ss)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	var log bytes.Buffer
	if err := Ensure(dir, "", false, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "no longer on disk") {
		t.Errorf("a child still on disk was reported gone:\n%s", log.String())
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxchild"}); len(ss) != 0 {
		t.Fatalf("with the switch off the child is still indexed, which a rebuild would not do: %#v", ss)
	}
	if ss, _ := Search(dir, search.Options{Query: "lister"}); len(ss) != 1 {
		t.Fatalf("the parent left with the child: %#v", ss)
	}
}

// The other half: a pass that cannot see a store at all — a hook started with
// a stripped environment that lost DEJA_MUSE_ROOTS (#4738) — is not a setting
// leaving a file out, and must not take the store's sessions with it. Nothing
// in this run's view claims those paths, so they stay, and they are not
// described as deleted either: they are on disk.
func TestStoreOutsideThisRunsViewIsKept(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "custom", "muse", "sessions")
	session := filepath.Join(root, "2026", "10", "05", "01a10be5-2bcd-7153-bacf-761a1bd3642b")
	writeMuseLog(t, session, "01a10be5-2bcd-7153-bacf-761a1bd3642b", "runtime.session.metadata", "the quuxroot migration")
	setHome(t, filepath.Join(tmp, "home"))
	dir := filepath.Join(tmp, "index.db")

	t.Setenv("DEJA_MUSE_ROOTS", root)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxroot"}); len(ss) != 1 {
		t.Fatalf("the session was not indexed: %#v", ss)
	}

	t.Setenv("DEJA_MUSE_ROOTS", "")
	var log bytes.Buffer
	if err := Ensure(dir, "", false, &log); err != nil {
		t.Fatal(err)
	}
	if ss, _ := Search(dir, search.Options{Query: "quuxroot"}); len(ss) != 1 {
		t.Fatalf("a pass that could not see the store dropped its session: %#v\n%s", ss, log.String())
	}
	if strings.Contains(log.String(), "no longer on disk") {
		t.Errorf("a transcript on disk was described as gone:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "1 transcript outside the stores this run reads") {
		t.Errorf("the pass did not say why it kept the session:\n%s", log.String())
	}
}
