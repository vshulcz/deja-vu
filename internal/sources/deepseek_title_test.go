package sources

import (
	"os"
	"path/filepath"
	"testing"
)

// dsh names a session twice: a stand-in it cuts out of the opening message, and
// the real one when its title model answers. The log is append-only, so the
// last event is the name the harness is showing — deja kept the first, and
// listed every dsh session under a sentence cut mid-phrase (#2551).
func TestDeepSeekTakesTheTitleTheHarnessSettledOn(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-t1","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab"}
{"type":"session/title","seq":10,"time":1787320263581,"data":{"title":"how many connections do","source":{"kind":"fallback"}}}
{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"how many connections do we hold per shard?"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":20,"time":1787320263700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"forty, and the pool is the reason"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"session/title","seq":21,"time":1787320263800,"data":{"title":"connections per shard","source":{"kind":"provider","provider":"session-title-first-prompt-llm"}}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session-t1", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("parsed %d sessions, want 1", len(ss))
	}
	if ss[0].Title != "connections per shard" {
		t.Errorf("title = %q, want the one the harness settled on", ss[0].Title)
	}
}

// And a session the title model never answered for keeps the stand-in rather
// than losing its name: it is still what dsh shows.
func TestDeepSeekKeepsTheOnlyTitleItWasGiven(t *testing.T) {
	log := `{"type":"session","version":0,"id":"session-t2","createdAt":1787320263519,"cwd":"/work/pgbouncer-lab"}
{"type":"session/title","seq":10,"time":1787320263581,"data":{"title":"how many connections do","source":{"kind":"fallback"}}}
{"type":"user/message","seq":7,"time":1787320263580,"data":{"content":[{"type":"text","text":"how many connections do we hold per shard?"}],"source":{"kind":"user"},"role":"user"}}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "session-t2", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Title != "how many connections do" {
		t.Fatalf("title = %q", ss[0].Title)
	}
}
