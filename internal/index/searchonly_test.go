package index

import (
	"os"
	"path/filepath"
	"testing"
)

// A search-only build writes the records and the postings and none of the
// derived tables, an append keeps it that way, and turning the setting off
// mines them on the next pass (#5395).
func TestASearchOnlyBuildSkipsTheDerivedTables(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "policy.json"))
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, sid := range []string{"s-one", "s-two"} {
		write(t, filepath.Join(claude, "p", sid+".jsonl"),
			claudeLine(sid, "2026-01-02T03:04:05Z", "the exporter retries without a pause")+
				claudeBashLine(sid, "2026-01-02T03:05:00Z", "go test ./internal/exporter/"))
	}
	derived := []string{cooccurFile, fixRawFile, commandsFile, commandFailStateFile, sessionFactsFile}
	present := func(dir string) []string {
		var got []string
		for _, name := range derived {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				got = append(got, name)
			}
		}
		return got
	}

	// The control: the same store mined writes the tables this test looks for.
	full := filepath.Join(tmp, "full")
	if err := Ensure(full, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if len(present(full)) == 0 {
		t.Fatal("an ordinary build wrote none of the derived tables, so this proves nothing")
	}

	t.Setenv(SearchOnlyEnv, "1")
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 {
		t.Errorf("a search-only build wrote %v", got)
	}
	if !BuiltSearchOnly(dir) {
		t.Error("the index does not record that it was built search-only")
	}
	if n, err := SessionCount(dir); err != nil || n != 2 {
		t.Errorf("a search-only index holds %d sessions (%v), want 2", n, err)
	}

	// A new transcript, and a turn added to one already read: the second is
	// the append path, which mines the tables on its own.
	write(t, filepath.Join(claude, "p", "s-three.jsonl"),
		claudeLine("s-three", "2026-01-02T05:00:00Z", "run the suite")+
			claudeBashLine("s-three", "2026-01-02T05:01:00Z", "go test ./internal/exporter/"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(claude, "p", "s-one.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(claudeBashLine("s-one", "2026-01-02T06:01:00Z", "go vet ./internal/exporter/"))
	_ = f.Close()
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 {
		t.Errorf("an update of a search-only index wrote %v", got)
	}

	// A pass nobody asked for keeps the setting the index was built under:
	// a hook started without the user's environment does not rebuild the
	// store over it, and the shell does not rebuild it back.
	t.Setenv(SearchOnlyEnv, "")
	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 || !BuiltSearchOnly(dir) {
		t.Errorf("an automatic pass under another setting changed the index: %v, search-only %v", got, BuiltSearchOnly(dir))
	}

	// `deja index` run by hand applies it.
	undo := ApplySearchOnly()
	err = Ensure(dir, "", false, nil)
	undo()
	if err != nil {
		t.Fatal(err)
	}
	if len(present(dir)) == 0 || BuiltSearchOnly(dir) {
		t.Errorf("turning search-only off left the tables unmined: %v", present(dir))
	}

	// And the policy file turns it on the same way.
	if err = os.WriteFile(filepath.Join(tmp, "policy.json"), []byte(`{"search_only": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	undo = ApplySearchOnly()
	err = Ensure(dir, "", false, nil)
	undo()
	if err != nil {
		t.Fatal(err)
	}
	if got := present(dir); len(got) > 0 || !BuiltSearchOnly(dir) {
		t.Errorf(`"search_only": true left %v behind`, got)
	}
}
