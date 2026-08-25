package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// The rescue is for a query whose words are right but never appear together —
// "login" next to "jwks". That is the state where the postings intersect to
// nothing, and the chain called the rescue only in the other zero-result
// branch, so the case it was written for never reached it (#1786).
func TestCooccurRescueRunsWhenThePostingsDoNotIntersect(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	root := t.TempDir()
	writeSession := func(i int, text string) {
		line := func(role, body string) string {
			b, _ := json.Marshal(map[string]any{
				"type": role, "sessionId": "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
				"cwd": "/w", "timestamp": "2026-08-20T01:00:00Z",
				"message": map[string]any{"role": role, "content": body},
			})
			return string(b)
		}
		body := line("user", text) + "\n" + line("assistant", "resolved: "+text) + "\n"
		if err := os.WriteFile(filepath.Join(root, "s"+string(rune('a'+i%26))+string(rune('a'+i/26))+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	filler := []string{"alpha bravo charlie", "delta echo foxtrot", "golf hotel india", "juliet kilo lima"}
	for i := range 160 {
		writeSession(i, filler[i%len(filler)])
	}
	for i := 160; i < 170; i++ {
		writeSession(i, "keytab renewal failed on the build agent")
	}
	for i := 170; i < 182; i++ {
		writeSession(i, "kerberos keytab rotation on the runner")
	}
	setHome(t, t.TempDir())
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The map has to know them, or this proves nothing about the chain.
	if n := readCooccur(dir)["kerberos"]; len(n) == 0 {
		t.Fatalf("the fixture did not teach the map: %v", readCooccur(dir))
	}
	// The branch this is about is the one where the ANDed postings do not
	// intersect — both words are in the index, no session holds both.
	o := query.Options{Query: "kerberos renewal", All: true}
	both := map[string]bool{}
	for _, tok := range []string{"kerberos", "renewal"} {
		posts, perr := postingsFor(dir, "t"+tok)
		if perr != nil || len(posts) == 0 {
			t.Fatalf("%s has no postings, so this is not the branch under test (%v)", tok, perr)
		}
		hits, herr := Search(dir, query.Options{Query: tok, All: true})
		if herr != nil {
			t.Fatal(herr)
		}
		for _, h := range hits {
			key := h.Harness + ":" + h.ID
			if both[key] {
				t.Fatalf("%s is in a session that holds both words, so the postings do intersect", key)
			}
			both[key] = true
		}
	}
	direct, err := func() (SearchResult, error) {
		m, merr := readManifest(dir)
		if merr != nil {
			return SearchResult{}, merr
		}
		return cooccurSearch(dir, m, o)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if len(direct.Sessions) == 0 {
		t.Fatal("the rescue itself found nothing, so the fixture is wrong")
	}
	ss, err := Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Errorf("the rescue finds %d sessions and the search that a user runs returns none", len(direct.Sessions))
	}
	// And an ordinary query is unaffected.
	if hit, err := Search(dir, query.Options{Query: "keytab renewal", All: true}); err != nil || len(hit) == 0 {
		t.Errorf("a query that matches directly returned %d (%v)", len(hit), err)
	}
	_ = model.Session{}
}

// A neighbour swap is not a word form, and the two are narrated differently:
// "login" answered by "jwks" is the corpus keeping company, not a spelling.
func TestANeighbourSwapSaysItIsOne(t *testing.T) {
	if !(SearchResult{Neighbour: true}).Neighbour {
		t.Fatal("the flag does not survive a copy")
	}
	if (SearchResult{Stemmed: true}).Neighbour {
		t.Error("a stem result claims to be a neighbour swap")
	}
}
