package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A synced index has to answer as well as the one it came from, and every
// change to ranking is a chance for it to stop. Imported sessions differ from
// local ones in exactly the ways ranking cares about: their project carries the
// peer's prefix, their paths are the peer's paths, and their records arrive
// through a different writer.
//
// This walks one imported session through the parts that changed today —
// project scoping by path segment, the record-role filter, and the relevance
// tier — because none of the benchmark corpora contain an imported session at
// all.
func importedIndex(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	// The peer knows this project by a path form this machine never uses.
	lines := `{"harness":"claude","session_id":"peer1","project":"goprojects/svc","role":"user","text":"the ratelimiter dropped every burst over the smoothing window","time":"` + when + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"goprojects/svc","role":"assistant","text":"raised the burst budget and it settled","time":"` + when + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"goprojects/svc","role":"command","text":"go test ./internal/ratelimit","time":"` + when + `"}` + "\n"
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-peer.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Search reaches it, and does not hand back the command record it would hide
// on a local session.
func TestAnImportedSessionAnswersLikeALocalOne(t *testing.T) {
	dir := importedIndex(t)
	result, err := SearchDetailed(dir, search.Options{Query: "ratelimiter smoothing window", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("a synced session that answers the question was not found at all")
	}
	for _, s := range result.Sessions {
		for _, m := range s.Messages {
			if m.Role == roleCommand {
				t.Errorf("a synced session served a command record, which the same query hides locally: %q", m.Text)
			}
		}
	}
}

// And the project it belongs to is reachable by the name this machine knows,
// not only by the peer's path. Scoping matches whole path segments now, so the
// peer's prefix has to be understood rather than merely contained.
func TestASyncedProjectIsInScopeUnderTheLocalName(t *testing.T) {
	if !projectInScope("imported:goprojects/svc", "svc") {
		t.Error("a synced project is out of scope under the name this machine knows it by")
	}
	if !projectInScope("imported:svc", "svc") {
		t.Error("a synced project is out of scope under its own name")
	}
	if projectInScope("imported:goprojects/svc-archive", "svc") {
		t.Error("a different synced project was pulled into this one's scope")
	}
}

// The auto-recall path is where a synced index earns its keep: the peer's work
// arriving without anyone asking. It loads sessions through the same servable
// filter as search, so this checks it survives that too.
func TestAutoRecallReachesSyncedWork(t *testing.T) {
	dir := importedIndex(t)
	ss, _, _, _, err := ProjectRelevant(dir, []string{"svc"}, []string{"ratelimiter", "smoothing", "window"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Fatal("auto-recall found nothing in a synced project")
	}
	found := false
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "ratelimiter") {
				found = true
			}
			if m.Role == roleCommand {
				t.Errorf("auto-recall served a command record from a synced session: %q", m.Text)
			}
		}
	}
	if !found {
		t.Error("the synced session came back without the line that answers the question")
	}
}

// Relevance ranking, reached directly, so a synced session is not merely found
// by exact match while the tier that handles plainly worded questions drops it.
func TestRelevanceRanksSyncedSessions(t *testing.T) {
	dir := importedIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{
		Query: "what happened with the ratelimiter and the smoothing window", All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Skip("the relevance tier declines small stores by design; nothing to check here")
	}
	for _, s := range result.Sessions {
		for _, msg := range s.Messages {
			if msg.Role == roleCommand {
				t.Errorf("the relevance tier served a command record from a synced session: %q", msg.Text)
			}
		}
	}
}
