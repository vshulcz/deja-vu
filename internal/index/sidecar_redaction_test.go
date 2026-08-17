package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both full-build paths scrub the sessions in place before mining anything from
// them, because the record loop redacts into a local and leaves the sessions
// holding the raw text. The incremental path had no such call, so a secret in
// an error line or a command reached fixes.gob whole — while rebuilding the
// same corpus from scratch produced a clean one. Which build path a machine
// happened to take decided whether its credentials were written to disk.
//
// The assertion is on the bytes of the index, not on what a command prints: the
// leak is the file, and every reader of it inherits the problem.
func TestIncrementalUpdateRedactsWhatItMines(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	const secret = "sk-live-ZZTOPSECRET1234567890abcd"
	plain := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"ordinary work"}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"nothing special"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// An error carrying a credential, settled by a command carrying the same
	// one — which is what makes it a pair worth mining.
	leaky := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"deploy is broken"}}
{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"connection refused: db.internal:5432 for ` + secret + `"}]}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"kubectl create secret generic api --from-literal=token=` + secret + `"}}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"recreated it and the deploy came back"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(tmp, "index.db")
	plain("a", "2026-02-01T03:04:05Z")
	plain("b", "2026-02-02T03:04:05Z")
	plain("c", "2026-02-03T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if found := filesHolding(t, dir, secret); len(found) > 0 {
		t.Fatalf("the full build already leaks into %v — this test can no longer tell the paths apart", found)
	}

	// The sessions holding it arrive afterwards: the incremental path.
	leaky("d", "2026-03-01T03:04:05Z")
	leaky("e", "2026-03-02T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Absence of the secret proves nothing if nothing was mined. Pin that the
	// pair exists first, so this cannot pass by the fixture quietly stopping to
	// qualify — the evidence rules in fixPairsIn are strict and have moved
	// before.
	if len(ReadFixes(dir)) == 0 {
		t.Fatal("no pair was mined, so the assertion below would hold for the wrong reason")
	}
	if found := filesHolding(t, dir, secret); len(found) > 0 {
		t.Errorf("an incremental update wrote the raw credential into %v", found)
	}

	// A machine that already ran the leaky build carries its poisoned file
	// forward, and carrying is all that ever happens to a pair whose session is
	// not re-read. Simulate that: put the credential back into the pair on disk
	// and let one ordinary session trigger an update.
	pairs := ReadFixes(dir)
	pairs[0].Error = "connection refused: db.internal:5432 for " + secret
	if err := writeGob(filepath.Join(dir, fixesFile), pairs); err != nil {
		t.Fatal(err)
	}
	plain("f", "2026-04-01T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if found := filesHolding(t, dir, secret); len(found) > 0 {
		t.Errorf("a credential carried in from an older index survived the update, in %v", found)
	}
}

// filesHolding names the files under dir whose bytes contain want.
func filesHolding(t *testing.T, dir, want string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), want) {
			out = append(out, filepath.Base(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
