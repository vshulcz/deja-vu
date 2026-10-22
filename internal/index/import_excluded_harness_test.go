package index

import (
	"os"
	"path/filepath"
	"testing"
)

// One record each: a harness this machine excludes, a project its ignore rule
// covers, and one in neither.
const ruleBatch = `{"harness":"opencode","session_id":"oc1","project":"work/app","role":"user","text":"zorbleflux opencode question","time":"2026-08-04T12:00:00Z","origin":"mini"}
{"harness":"claude","session_id":"cl1","project":"/Users/me/scratch/ci-run","role":"user","text":"zorbleflux scratch question","time":"2026-08-04T12:01:00Z","origin":"mini"}
{"harness":"claude","session_id":"cl2","project":"work/app","role":"user","text":"zorbleflux control question","time":"2026-08-04T12:02:00Z","origin":"mini"}
`

func ruleHome(t *testing.T) (dir, batch, policyFile string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "")
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
	batch = filepath.Join(home, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-test.jsonl"), []byte(ruleBatch), 0o600); err != nil {
		t.Fatal(err)
	}
	policyFile = filepath.Join(home, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	return filepath.Join(home, "index.db"), batch, policyFile
}

// DEJA_EXCLUDE_HARNESSES and the ignore rule held for this machine's own
// stores, and a peer's batch put the same harness and the same tree into its
// index — from where an excluded harness's session went on with the next
// relay (#5435).
func TestImportHoldsAPeersRecordsToExcludedHarnessesAndIgnoredProjects(t *testing.T) {
	dir, batch, policyFile := ruleHome(t)
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "OpenCode")
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*/scratch/*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := Import(dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("only cl2 is in neither rule, %d records imported", n)
	}
	if h, i := ImportSkippedHarness(), ImportSkippedIgnored(); h != 1 || i != 1 {
		t.Errorf("left out: harness %d, ignored %d; want 1 and 1", h, i)
	}
	if got := importedRoles(t, dir); len(got) != 1 || got["cl2"] != "user" {
		t.Errorf("stored %v, want only cl2", got)
	}

	// Neither drop is entered in the ledger: lifting the rules and importing
	// the same batch brings both in.
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "")
	if err := os.Remove(policyFile); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, batch); err != nil || n != 2 {
		t.Errorf("re-import with the rules lifted: %d records, %v; want 2", n, err)
	}
	if got := importedRoles(t, dir); len(got) != 3 {
		t.Errorf("after re-import stored %v", got)
	}
	if h, i := ImportSkippedHarness(), ImportSkippedIgnored(); h != 0 || i != 0 {
		t.Errorf("nothing is excluded now, counts %d and %d", h, i)
	}
}

// What arrived before the harness was excluded does not go out on the next
// relay either.
func TestRelayHoldsBackAnExcludedHarness(t *testing.T) {
	dir, batch, _ := ruleHome(t)
	if _, err := Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	exported := func() map[string]bool {
		out := t.TempDir()
		if _, err := ExportRelay(dir, out, ""); err != nil {
			t.Fatal(err)
		}
		paths, _ := filepath.Glob(filepath.Join(out, "*.jsonl"))
		got := map[string]bool{}
		for _, p := range paths {
			if err := readSyncFile(p, func(sr SyncRecord) error {
				got[sr.SessionID] = true
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	// Control: with nothing excluded every session relays.
	if got := exported(); !got["oc1"] || !got["cl2"] {
		t.Fatalf("relay without a rule: %v", got)
	}
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "opencode")
	if got := exported(); got["oc1"] || !got["cl2"] {
		t.Errorf("relay with opencode excluded: %v", got)
	}
}
