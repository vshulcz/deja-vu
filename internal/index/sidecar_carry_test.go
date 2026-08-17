package index

import (
	"os"
	"path/filepath"
	"testing"
)

// An incremental update replaces the whole index directory, and the path that
// builds it never mines the sidecars — it deliberately never holds the whole
// corpus. So the swap used to delete them: one new session anywhere, and `fix`
// had nothing to answer with, ranking lost its query expansion, and neither
// came back until the next full rebuild. Ordinary use, silently degraded.
//
// This asserts the files survive, not what is in them. What is in them is
// allowed to be one update stale; being absent is not.
func TestIncrementalUpdateKeepsTheMinedSidecars(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	proj := filepath.Join(claude, "-work")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)

	// An error followed by a command, twice, which is the evidence a fix pair
	// needs before deja will keep it.
	session := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"build broken"}}
{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"undefined: renderInvoice in vendor/billing/api.go"}]}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go mod vendor && go build ./..."}}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"vendoring fixed it"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	session("a", "2026-01-02T03:04:05Z")
	session("b", "2026-01-03T03:04:05Z")

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Whatever the full build mined is what the incremental one has to keep.
	// cooccur needs more sessions than this fixture has and is carried by the
	// same code; asserting on it here would only pin the fixture's size.
	var want []string
	for _, name := range []string{fixesFile, commandsFile, cooccurFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			want = append(want, name)
		}
	}
	if len(want) < 2 {
		t.Fatalf("full build mined only %v — the fixture no longer clears the bars this test needs", want)
	}
	if len(FixesFor(dir, "undefined: renderInvoice in vendor/billing/api.go", 3, nil)) == 0 {
		t.Fatal("full build mined no fix pair; the fixture is not exercising what this test guards")
	}

	// One more session is all it takes: this is the incremental path.
	session("c", "2026-01-04T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("an incremental update dropped %s", name)
		}
	}
	if len(FixesFor(dir, "undefined: renderInvoice in vendor/billing/api.go", 3, nil)) == 0 {
		t.Error("fix went silent after an incremental update")
	}

	// Keeping the old pairs is half of it. A new session is a new file, and a
	// new file never takes the append path, so sessions arriving after the
	// first build are the only source of new pairs and all of them come
	// through here. Carrying alone left fix frozen at whatever the full build
	// had seen — which this assertion, and not the one above, is what catches.
	settled := func(name, ts string) {
		body := `{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":"migration broke"}}
{"type":"user","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"connection refused: flimbardb:5432"}]}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"docker compose restart flimbardb"}}]}}
{"type":"assistant","sessionId":"` + name + `","cwd":"/w","timestamp":"` + ts + `","message":{"role":"assistant","content":"restarting it cleared the migration"}}
`
		if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	settled("d", "2026-02-01T03:04:05Z")
	settled("e", "2026-02-02T03:04:05Z")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(FixesFor(dir, "connection refused: flimbardb:5432", 3, nil)) == 0 {
		t.Error("a pair from a session that arrived after the full build was never mined")
	}
	// And the old one is still there: learning must not cost what was carried.
	if len(FixesFor(dir, "undefined: renderInvoice in vendor/billing/api.go", 3, nil)) == 0 {
		t.Error("mining the new sessions dropped the carried pairs")
	}
}
