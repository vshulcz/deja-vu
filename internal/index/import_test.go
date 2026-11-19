package index

import (
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// Importing into a dir that was never indexed writes the initial manifest.
// That manifest used to snapshot every local transcript as already seen while
// ingesting none, so the next index found itself fresh and skipped the
// reader's own sessions — local history stayed invisible until a full rebuild.
// A local session and the imported records must both be reachable after a
// plain import-then-index.
func TestImportBeforeFirstIndexKeepsLocalSessions(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-local")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"local1","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"localneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "local1.jsonl"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")

	// Import first, into a dir that has never been indexed.
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	batchDir := filepath.Join(tmp, "batches")
	writeSyncBatch(t, batchDir, []SyncRecord{
		{Harness: "claude", SessionID: "remote1", Project: "deja-vu", Role: "user", Text: "importneedle question", Time: base},
	})
	if n, err := Import(dir, batchDir); err != nil || n != 1 {
		t.Fatalf("import n=%d err=%v", n, err)
	}

	// The plain index every reader runs next must ingest the local session.
	if err := EnsureForSearch(dir, search.Options{Query: "localneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "localneedle", All: true}); err != nil {
		t.Fatal(err)
	} else if len(ss) != 1 {
		t.Fatalf("local session lost after import-then-index: got %d", len(ss))
	}
	// And the imported records are still there.
	if ss, err := Search(dir, search.Options{Query: "importneedle", All: true}); err != nil {
		t.Fatal(err)
	} else if len(ss) != 1 || !strings.HasPrefix(ss[0].Project, "imported:") {
		t.Fatalf("imported record lost: %#v", ss)
	}
}

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

// A batch whose every record belongs to a project the receiver excludes is
// dropped — correctly, since the exclude list keeps a project out of this
// machine's memory and a sync must not put it back. Nothing counted it, so
// "imported 0 records" read like an empty or already-synced batch, the misread
// #1118 recorded for the drop one line above (#2666).
func TestImportCountsWhatTheExcludeListKeptOut(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// This package shares one XDG_CONFIG_HOME across its tests and
	// sources.ExcludePath() reads it, so a list written through that path
	// outlives the test — the trap #2654 hit with the policy file.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := filepath.Join(home, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	var bodySb28 strings.Builder
	for _, id := range []string{"a1", "a2", "a3"} {
		bodySb28.WriteString(`{"harness":"claude","session_id":"` + id + `","project":"alpha","role":"user","text":"the zonkobuffer keeps overflowing","time":"2026-07-01T10:00:00Z","origin":"mini"}` + "\n")
	}
	body += bodySb28.String()
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-test.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	excl := sources.ExcludePath()
	if err := os.MkdirAll(filepath.Dir(excl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(excl, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "index.db")
	n, err := Import(dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the excluded project must not be imported, got %d records", n)
	}
	if got := ImportSkippedExcluded(); got != 3 {
		t.Fatalf("three records were dropped by the reader's own pattern, the count says %d", got)
	}
}

// And a batch nothing excludes leaves the counter at zero, so the line only
// appears when it has something to report.
func TestImportCountsNothingExcludedWhenNoPatternMatches(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// This package shares one XDG_CONFIG_HOME across its tests and
	// sources.ExcludePath() reads it, so a list written through that path
	// outlives the test — the trap #2654 hit with the policy file.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := filepath.Join(home, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"harness":"claude","session_id":"a1","project":"alpha","role":"user","text":"the zonkobuffer keeps overflowing","time":"2026-07-01T10:00:00Z","origin":"mini"}` + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-test.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "index.db")
	if _, err := Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	if got := ImportSkippedExcluded(); got != 0 {
		t.Fatalf("nothing was excluded, the count says %d", got)
	}
}

// A peer who still holds the batch hands it over again, which is the ordinary
// case — the same folder synced twice. The record is dropped for two reasons at
// once, and only one of them is worth saying: the dedupe ledger answered first,
// so the import went silent exactly when it had something to report, while a
// wiped-and-rebuilt index said it (#980).
func TestReimportOfAForgottenSessionStillSaysItWasLeftOut(t *testing.T) {
	tmp := t.TempDir()
	// Tombstones live under XDG_CONFIG_HOME, which the package shares: one
	// left behind here reads as forgotten in every test that runs after.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "solo", Project: "work/api", Role: "user", Text: "peer record about the vault rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, exp)
	if err != nil || n != 1 {
		t.Fatalf("first import: %d records, %v", n, err)
	}
	if ImportSkippedForgotten() != 0 {
		t.Fatalf("a fresh batch was reported as forgotten: %d", ImportSkippedForgotten())
	}

	id := ImportedSessionID("claude", "solo")
	if _, err = Forget(dir, ForgetOptions{Session: "claude:" + id}); err != nil {
		t.Fatal(err)
	}

	n, err = Import(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a forgotten session came back: %d records", n)
	}
	if got := ImportSkippedForgotten(); got != 1 {
		t.Errorf("the re-import dropped the record silently: skipped=%d", got)
	}
}

// A session forgotten here was forgotten under its own id. Import checked only
// the imported id, so a peer's copy walked the same text back into local search
// under a new one while `forget --list` still called it forgotten (#968).
func TestImportLeavesOutSessionsThisMachineForgot(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	store := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"shared","timestamp":"2026-07-11T10:00:00Z","cwd":"/p","message":{"role":"user","content":"the secret I decided to forget"}}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "shared.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: "shared"}); err != nil {
		t.Fatal(err)
	}

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for _, r := range []SyncRecord{
		{Harness: "claude", SessionID: "shared", Project: "p", Role: "user", Text: "the secret I decided to forget"},
		{Harness: "claude", SessionID: "fresh", Project: "p", Role: "user", Text: "a peer session about the ticker"},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}

	added, err := Import(dir, exp)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Errorf("imported %d records, want 1 — the forgotten one must stay out", added)
	}
	if n := ImportSkippedForgotten(); n != 1 {
		t.Errorf("reported %d records left out, want 1", n)
	}
	ss, err := Search(dir, query.Options{Query: "secret I decided to forget", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("forgotten text came back through a peer's batch: %d sessions", len(ss))
	}
	// What the peer has that this machine never forgot still arrives.
	ss, err = Search(dir, query.Options{Query: "peer session about the ticker", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Error("a fresh peer session was dropped too")
	}
}

// A Copilot CLI sub-agent run has no tombstone of its own: its parent's covers
// it. A peer's copy of the run names the parent, and stays out with it (#5216).
func TestImportLeavesOutTheRunOfAForgottenCopilotParent(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_COPILOT_ROOT", filepath.Join(tmp, "copilot"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	const parent, child = "f67048ad-042c-42f4-ac33-5db51c4d48b0", "717532a2-bf69-4164-be09-03daacd1762f"
	store := filepath.Join(tmp, "copilot", parent)
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"session.start","data":{"sessionId":"` + parent + `","startTime":"2026-10-18T05:31:46.707Z","context":{"cwd":"/p/app"}},"timestamp":"2026-10-18T05:31:46.707Z"}` + "\n" +
		`{"type":"user.message","data":{"content":"the parent turn"},"timestamp":"2026-10-18T05:31:47.305Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "events.jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Forget(dir, ForgetOptions{Session: parent}); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "copilot", SessionID: child, Project: "p/app", Role: "user", Text: "the helper's grackleplume draft", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if added, err := Import(dir, exp); err != nil || added != 0 {
		t.Errorf("imported %d records (%v), want the forgotten parent's run left out", added, err)
	}
}

// A record with no session_id cannot be attributed and is dropped. Dropping it
// silently made "imported 2 records" from a 3-record batch read as a complete
// transfer; the count is surfaced so a partial import is visible (#1118).
func TestImportCountsRecordsItCannotAttribute(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := os.MkdirAll(filepath.Join(tmp, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	batch := `{"harness":"claude","session_id":"g1","project":"p","role":"user","text":"one","time":"2026-07-20T10:00:00Z"}` + "\n" +
		`{"harness":"claude","project":"p","role":"user","text":"no session id","time":"2026-07-20T10:01:00Z"}` + "\n" +
		`{"harness":"claude","session_id":"g3","project":"p","role":"user","text":"three","time":"2026-07-20T10:02:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(in, "deja-sync-x-1.jsonl"), []byte(batch), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("imported = %d, want 2", n)
	}
	if got := ImportSkippedIncomplete(); got != 1 {
		t.Errorf("ImportSkippedIncomplete = %d, want 1", got)
	}
}

// A peer's session grows and is re-exported: the second sync batch carries only
// the new records (the old ones are already in the import ledger and skipped).
// The imported meta was rebuilt from just that batch and overwrote Touched, so a
// file the peer edited in the first batch stopped being blamable after the
// second import. The merge must keep the earlier files, not replace them.
func TestReimportKeepsEarlierTouchedFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}

	t1 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	batch1 := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"working on auth","time":"` + t1 + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"svc","role":"files","text":"/repo/auth.go","time":"` + t1 + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-a.jsonl"), []byte(batch1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}

	// Second batch: the same session, later, touching a different file.
	t2 := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	batch2 := `{"harness":"claude","session_id":"peer1","project":"svc","role":"user","text":"now the database layer","time":"` + t2 + `"}` + "\n" +
		`{"harness":"claude","session_id":"peer1","project":"svc","role":"files","text":"/repo/db.go","time":"` + t2 + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(shared, "deja-sync-b.jsonl"), []byte(batch2), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, shared); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found *SessionMeta
	for i := range metas {
		if metas[i].OrigID == "peer1" {
			found = &metas[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("imported session not found in %d metas", len(metas))
	}
	has := func(p string) bool {
		return slices.Contains(found.Touched, p)
	}
	if !has("/repo/db.go") {
		t.Errorf("Touched=%v missing the new file /repo/db.go", found.Touched)
	}
	if !has("/repo/auth.go") {
		t.Errorf("Touched=%v dropped /repo/auth.go from the first import — blame can no longer attribute it", found.Touched)
	}
}

// The batch file's name comes from the sending machine — scp copies whatever
// matched over there — and the import built its errors out of it raw, so a
// name with an escape byte in it rewrote the screen of whoever was watching a
// sync fail (#1847).
func TestAnImportErrorDoesNotHandTheTerminalAPeersFileName(t *testing.T) {
	dir := t.TempDir()
	batch := filepath.Join(dir, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "deja-sync-\x1b[31mHACK\rrewound-" + strings.Repeat("w", 120) + ".jsonl"
	if err := os.WriteFile(filepath.Join(batch, name), []byte("{not a record}\n"), 0o644); err != nil {
		t.Skipf("the filesystem refused the name: %v", err)
	}

	_, err := Import(filepath.Join(dir, "index.db"), batch)
	if err == nil {
		t.Fatal("a batch of nonsense imported cleanly, so there is no error to inspect")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\r") {
		t.Errorf("the error carries an escape or a rewind: %q", msg[:min(len(msg), 120)])
	}
	// The control: the reader can still tell which file failed.
	if !strings.Contains(msg, "deja-sync-") || !strings.Contains(msg, ".jsonl") {
		t.Errorf("the error no longer names the file at all: %q", msg[:min(len(msg), 120)])
	}
}

// An ordinary batch name is untouched, so the bound is not paid by every sync.
func TestAnOrdinaryBatchNameIsReportedAsItIs(t *testing.T) {
	dir := t.TempDir()
	batch := filepath.Join(dir, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "deja-sync-9f2a1b-20260825T090000Z.jsonl"
	if err := os.WriteFile(filepath.Join(batch, name), []byte("{not a record}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Import(filepath.Join(dir, "index.db"), batch)
	if err == nil {
		t.Fatal("a batch of nonsense imported cleanly")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("an ordinary name was altered: %q", err.Error())
	}
}

// The count and the list have to agree: one file whose name carries the
// separator used to read as three entries under a sentence that said one
// (#1847).
func TestOneFileCannotReadAsSeveral(t *testing.T) {
	dir := t.TempDir()
	batch := filepath.Join(dir, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "one.jsonl; two.jsonl; three.jsonl"
	if err := os.WriteFile(filepath.Join(batch, name), []byte("{not a record}\n"), 0o644); err != nil {
		t.Skipf("the filesystem refused the name: %v", err)
	}
	_, err := Import(filepath.Join(dir, "index.db"), batch)
	if err == nil {
		t.Fatal("a batch of nonsense imported cleanly")
	}
	msg := err.Error()
	if !strings.Contains(msg, "from 1 file") {
		t.Fatalf("the fixture did not produce the single-file sentence: %q", msg)
	}
	// One quoted name, whatever it contains.
	if strings.Count(msg, `"`) != 2 {
		t.Errorf("the name is not delimited, so it can read as several: %q", msg)
	}
	if !strings.Contains(msg, `"one.jsonl; two.jsonl; three.jsonl"`) {
		t.Errorf("the name was altered rather than delimited: %q", msg)
	}
}

// Every new session asked nextSessionOrd for its ordinal, and that walks the
// whole manifest — so importing n sessions cost O(n²): a 100k batch took 306s
// against 1.9s for forgetting the same 100k (#1024).
func TestImportedOrdinalsAreDistinctAndCheap(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var body []byte
	const n = 300
	for i := range n {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: fixedID(i), Project: "work/api",
			Role: "user", Text: "record about the pool cap"})
		if err != nil {
			t.Fatal(err)
		}
		body = append(append(body, b...), '\n')
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := Import(dir, exp); err != nil || got != n {
		t.Fatalf("import: %d records, %v", got, err)
	}

	// The ordinal is what postings point at, so a repeat would merge two
	// sessions' hits into one row.
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint32]string{}
	for _, m := range metas {
		if prev, ok := seen[m.Ord]; ok {
			t.Fatalf("sessions %s and %s share ordinal %d", prev, m.ID, m.Ord)
		}
		seen[m.Ord] = m.ID
	}
	if len(seen) != n {
		t.Errorf("got %d distinct ordinals for %d sessions", len(seen), n)
	}

	// A second batch continues above the first rather than colliding with it.
	body = nil
	for i := n; i < n+50; i++ {
		b := mustJSON(SyncRecord{Harness: "claude", SessionID: fixedID(i), Project: "work/api",
			Role: "user", Text: "another record"})
		body = append(append(body, b...), '\n')
	}
	if err = os.WriteFile(filepath.Join(exp, "deja-sync-2.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	metas, err = AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen = map[uint32]string{}
	for _, m := range metas {
		if prev, ok := seen[m.Ord]; ok {
			t.Fatalf("after the second batch, %s and %s share ordinal %d", prev, m.ID, m.Ord)
		}
		seen[m.Ord] = m.ID
	}
	if len(seen) != n+50 {
		t.Errorf("got %d distinct ordinals for %d sessions", len(seen), n+50)
	}
}

func fixedID(i int) string {
	const digits = "0123456789"
	return "s" + string([]byte{digits[i/100%10], digits[i/10%10], digits[i%10]})
}

// A shared folder is an outbox as well as an inbox: the machine that wrote a
// batch imports that same directory, and its own records came back as a second
// "imported" copy of every session it holds (#987).
func TestImportSkipsThisMachinesOwnBatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	claude := filepath.Join(tmp, "claude", "-tmp-x31")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	rec := `{"type":"user","message":{"role":"user","content":"first session about the pool cap"},"timestamp":"2026-07-12T10:00:00Z","sessionId":"b31","cwd":"/tmp/x31"}` + "\n"
	if err := os.WriteFile(filepath.Join(claude, "s.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportFull(dir, shared); err != nil {
		t.Fatal(err)
	}
	n, err := Import(dir, shared)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("this machine imported its own batch: %d records", n)
	}
	if ImportSkippedOwn() == 0 {
		t.Errorf("the own-copy drop was not reported")
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if strings.HasPrefix(m.Project, "imported:") {
			t.Errorf("a session of this machine came back as imported: %s · %s", m.Project, m.ID)
		}
	}

	// The backside: a peer sends genuinely new lines for a session id this
	// machine also holds, and those must still land.
	peer := filepath.Join(tmp, "from-peer")
	if err = os.MkdirAll(peer, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "b31", Project: "tmp/x31", Role: "user",
		Text: "a new line the peer added to the same session", Time: time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(peer, "deja-sync-p.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(dir, peer); err != nil || n != 1 {
		t.Errorf("a peer's new line for a shared session id was dropped: %d records, %v", n, err)
	}
	if ImportSkippedOwn() != 0 {
		t.Errorf("a peer's line was counted as this machine's own: %d", ImportSkippedOwn())
	}
}

// A batch from a machine that indexed everything: a session with a prompt,
// tool output and a command, and a sub-agent run.
const switchedBatch = `{"harness":"claude","session_id":"p1","project":"alpha","role":"user","text":"why does the zonkobuffer overflow","time":"2026-07-01T10:00:00Z","origin":"mini"}
{"harness":"claude","session_id":"p1","project":"alpha","role":"tool-output","text":"zonkoerror: buffer overflow at 0x10","time":"2026-07-01T10:00:01Z","origin":"mini"}
{"harness":"claude","session_id":"p1","project":"alpha","role":"command","text":"go test ./zonko","time":"2026-07-01T10:00:02Z","origin":"mini"}
{"harness":"claude","session_id":"agent-p2","project":"alpha","role":"user","text":"look for the zonkobuffer cap","time":"2026-07-01T10:00:03Z","origin":"mini","kind":"sidechain"}
`

func writeSwitchedBatch(t *testing.T, home string) string {
	t.Helper()
	// Every switch starts on, whatever the shell running the tests set.
	for _, k := range []string{"DEJA_INDEX_TOOL_OUTPUT", "DEJA_INDEX_COMMANDS", "DEJA_INDEX_PATHS", "DEJA_INDEX_EDITS", "DEJA_INDEX_WRITES", "DEJA_INCLUDE_SUBAGENTS"} {
		t.Setenv(k, "")
	}
	batch := filepath.Join(home, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-test.jsonl"), []byte(switchedBatch), 0o600); err != nil {
		t.Fatal(err)
	}
	return batch
}

// importedRoles is every imported session's stored roles, keyed by its id.
func importedRoles(t *testing.T, dir string) map[string]string {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string][]string{}
	if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		if meta, ok := m.Sessions[r.Key]; ok && r.SourcePath == syncImportPath {
			by[meta.OrigID] = append(by[meta.OrigID], r.Role)
		}
	}); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for id, roles := range by {
		sort.Strings(roles)
		out[id] = strings.Join(roles, ",")
	}
	return out
}

// DEJA_INDEX_TOOL_OUTPUT=0, DEJA_INDEX_COMMANDS=0 and DEJA_INCLUDE_SUBAGENTS=0
// held for what this machine's readers parse, and a peer's batch walked the
// same tool output, commands and sub-agent runs in past them (#5425).
func TestImportHoldsAPeersRecordsToTheLocalSwitches(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := writeSwitchedBatch(t, home)
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	claude := filepath.Join(home, "claude", "-tmp-local")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(home, "notes.jsonl"))
	local := filepath.Join(claude, "l.jsonl")
	line := func(text, at string) string {
		return `{"type":"user","message":{"role":"user","content":"` + text + `"},"timestamp":"` + at + `","sessionId":"l1","cwd":"/tmp/local"}` + "\n"
	}
	if err := os.WriteFile(local, []byte(line("a local question", "2026-07-02T10:00:00Z")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	n, err := Import(dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("only the prompt is on under these switches, %d records imported", n)
	}
	if got := ImportSkippedSwitched(); got != 3 {
		t.Errorf("three records were switched off here, the count says %d", got)
	}
	if got := importedRoles(t, dir); len(got) != 1 || got["p1"] != "user" {
		t.Errorf("stored %v, want only p1's prompt", got)
	}

	// An index pass after the import keeps the session as it arrived: the
	// append-only pass over a grown local transcript and a rebuild both carry
	// imported records.
	f, err := os.OpenFile(local, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line("a second local question", "2026-07-02T10:01:00Z")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	before, _ := readManifest(dir)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := readManifest(dir); !after.BuiltAt.After(before.BuiltAt) {
		t.Fatalf("the append pass did not run")
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := importedRoles(t, dir); len(got) != 1 || got["p1"] != "user" {
		t.Errorf("after index passes stored %v, want only p1's prompt", got)
	}

	// What a switch left out was not entered in the ledger, so the same batch
	// imported with the switches on brings it in.
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	t.Setenv("DEJA_INDEX_COMMANDS", "")
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	if n, err := Import(dir, batch); err != nil || n != 3 {
		t.Errorf("re-import with the switches on: %d records, %v; want 3", n, err)
	}
	got := importedRoles(t, dir)
	if got["p1"] != "command,tool-output,user" || got["agent-p2"] != "user" {
		t.Errorf("after re-import stored %v", got)
	}
}

// Control: with every switch on the whole batch lands, the run keeps its kind
// and nothing is counted, so the test above is not passing on an import that
// drops these records regardless.
func TestImportTakesEveryRoleWithTheSwitchesOn(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "")
	t.Setenv("DEJA_INDEX_COMMANDS", "")
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	batch := writeSwitchedBatch(t, home)
	dir := filepath.Join(home, "index.db")

	n, err := Import(dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("every record is on, %d imported", n)
	}
	if got := ImportSkippedSwitched(); got != 0 {
		t.Errorf("nothing is switched off, the count says %d", got)
	}
	got := importedRoles(t, dir)
	if got["p1"] != "command,tool-output,user" || got["agent-p2"] != "user" {
		t.Errorf("stored %v", got)
	}
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, m := range metas {
		kinds[m.OrigID] = m.Kind
	}
	if kinds["agent-p2"] != "sidechain" || kinds["p1"] != "" {
		t.Errorf("kinds after import: %v", kinds)
	}
}

// The sender names a sub-agent run's kind, so the receiver can apply its own
// DEJA_INCLUDE_SUBAGENTS to it.
func TestExportCarriesASubagentRunsKind(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	batch := writeSwitchedBatch(t, home)
	dir := filepath.Join(home, "index.db")
	if _, err := Import(dir, batch); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(home, "out")
	if _, err := ExportRelay(dir, out, ""); err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(filepath.Join(out, "*.jsonl"))
	kinds := map[string]string{}
	for _, p := range paths {
		if err := readSyncFile(p, func(sr SyncRecord) error {
			kinds[sr.SessionID] = string(sr.Kind)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if kinds["agent-p2"] != "sidechain" || kinds["p1"] != "" {
		t.Errorf("exported kinds: %v", kinds)
	}
}

// The import path redacted a peer's text but threw the count away, so
// `stats --redaction` under-reported protection on an imported store: two
// secrets removed, the total said one. Import now tallies its redactions the
// way local ingest does, and rolls the count back with a refused file.
func TestImportCountsItsRedactions(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(id, text string) string {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: id, Project: "p", Role: "user", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	// Two records, each carrying one AWS access key.
	if err := os.WriteFile(filepath.Join(in, "deja-sync.jsonl"),
		[]byte(rec("s1", "key AKIAIOSFODNN7EXAMPLE one")+rec("s2", "key AKIAIOSFODNN7EXAMPLE two")), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err := Import(dir, in); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Redacted != 2 {
		t.Fatalf("import counted %d redactions, want 2 (one per imported secret)", m.Redacted)
	}

	// Re-importing the same batch redacts nothing new — the records dedupe
	// before they are counted, so the total must not climb.
	if _, err = Import(dir, in); err != nil {
		t.Fatal(err)
	}
	if m, err = readManifest(dir); err != nil {
		t.Fatal(err)
	}
	if m.Redacted != 2 {
		t.Fatalf("re-import double-counted redactions: total=%d, want 2", m.Redacted)
	}
}

// A file refused for truncation takes its redaction count with it: the count
// is applied only once the file has been read whole, so a secret in the half
// before the break does not inflate the total for records that never landed.
func TestImportRedactionRollsBackWithARefusedFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := func(id, text string) string {
		b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: id, Project: "p", Role: "user", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// A valid record with a secret, then a truncated line — the file is refused.
	body := rec("t1", "secret AKIAIOSFODNN7EXAMPLE here") + "\n" + `{"harness":"claude","session_id":"t1","role":"user","text":"trunc`
	if err := os.WriteFile(filepath.Join(in, "deja-sync-trunc.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err := Import(dir, in); err == nil {
		t.Fatal("a truncated file was not reported")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Redacted != 0 {
		t.Fatalf("a refused file left its redaction count behind: total=%d, want 0", m.Redacted)
	}
}

// Rebuilding an index that holds imported records passes them through
// preRedactSessions again; a Cookie header followed by JSON-escaped newlines
// must not erode 383 bytes or double-count redactions on each rebuild (#5763).
func TestRebuildPreservesImportedCookieRecordAndRedactionCounts(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	text := `"headers": "Cookie: session=secret_value_123456\nContent-Type: application/json\n\n` + strings.Repeat("ordinary-body-", 35) + `"`
	b, err := json.Marshal(SyncRecord{Harness: "claude", SessionID: "cookie-imp", Project: "p", Role: "user", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(in, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err = Import(dir, in); err != nil {
		t.Fatal(err)
	}
	s1 := importedSessions(dir).sessions
	if len(s1) != 1 || len(s1[0].Messages) != 1 {
		t.Fatalf("imported session missing: %#v", s1)
	}
	wantText := s1[0].Messages[0].Text

	if err = EnsureForSearch(dir, search.Options{Query: "ordinary-body-"}, true, nil); err != nil {
		t.Fatal(err)
	}
	s2 := importedSessions(dir).sessions
	if len(s2) != 1 || len(s2[0].Messages) != 1 {
		t.Fatalf("session lost on rebuild: %#v", s2)
	}
	if s2[0].Messages[0].Text != wantText {
		t.Fatalf("rebuild eroded imported record (%d -> %d bytes):\n  before: %q\n  after:  %q",
			len(wantText), len(s2[0].Messages[0].Text), wantText, s2[0].Messages[0].Text)
	}
	m2, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Redacted != 0 {
		t.Fatalf("rebuild re-redacted an already-redacted imported record: m2.Redacted=%d (%v)",
			m2.Redacted, m2.RedactionRules)
	}
}

// The import path counts its redactions, but a count is not proof the secret
// left the stored bytes — the cooccur leak (its count was right while the
// sidecar held raw text) is exactly that failure. Peer-pushed text is
// attacker-influenced, so this imports a secret and asserts it survives in no
// file of the index: records, manifest title, or postings.
func TestImportLeavesNoRawSecretOnDisk(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	in := filepath.Join(home, "export")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "ghp_abcdefghijklmnop0123456789QRSTUV"
	b, err := json.Marshal(SyncRecord{
		Harness: "claude", SessionID: "s1", Project: "p", Role: "user",
		Text: "please rotate " + secret + " before the deploy audit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "deja-sync.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "idx")
	if _, err := Import(dir, in); err != nil {
		t.Fatal(err)
	}

	checked := 0
	var walk func(string)
	walk = func(p string) {
		info, err := os.Stat(p)
		if err != nil {
			return
		}
		if info.IsDir() {
			subs, _ := os.ReadDir(p)
			for _, s := range subs {
				walk(filepath.Join(p, s.Name()))
			}
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return
		}
		checked++
		if strings.Contains(string(data), secret) {
			t.Errorf("imported secret survived into %s", p)
		}
	}
	walk(dir)
	if checked == 0 {
		t.Fatal("no index files were read")
	}
}

// Touched is a ranking, and #1333 established that a ranking cannot be merged
// without the numbers behind it — which is why SessionMeta.TouchHits exists and
// why local ingest carries it. The import path derives Touched from the records
// (counting as it goes) and then throws the counts away, so a peer's session
// merges by rank alone: a file the second batch touched once leads one the first
// batch touched throughout (#2558).
func TestImportKeepsTheCountsBehindTouched(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(tmp, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	rec := func(role, text string, n int) string {
		return `{"harness":"claude","session_id":"peer1","project":"svc","role":"` + role +
			`","text":"` + text + `","time":"` + at.Add(time.Duration(n)*time.Minute).Format(time.RFC3339) + `"}` + "\n"
	}
	batch := func(name, body string) {
		if err := os.WriteFile(filepath.Join(shared, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Import(dir, shared); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(shared, name)); err != nil {
			t.Fatal(err)
		}
	}
	// The first batch passes over one file once.
	batch("deja-sync-a.jsonl", rec("user", "a first look", 0)+rec("files", "/repo/tail.go", 1))

	meta := importedPeerMeta(t, dir)
	if len(meta.TouchHits) != len(meta.Touched) {
		t.Errorf("Touched=%v carries %d counts", meta.Touched, len(meta.TouchHits))
	}

	// The second works on another file throughout.
	second := rec("user", "working on the pool", 10)
	var secondSb55 strings.Builder
	for i := range 5 {
		secondSb55.WriteString(rec("files", "/repo/pool.go", 11+i))
	}
	second += secondSb55.String()
	batch("deja-sync-b.jsonl", second)

	meta = importedPeerMeta(t, dir)
	if len(meta.Touched) < 2 {
		t.Fatalf("Touched=%v, want both files", meta.Touched)
	}
	if meta.Touched[0] != "/repo/pool.go" {
		t.Errorf("Touched=%v hits=%v — the file touched once leads the one touched throughout",
			meta.Touched, meta.TouchHits)
	}
}

func importedPeerMeta(t *testing.T, dir string) SessionMeta {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range metas {
		if meta.OrigID == "peer1" {
			return meta
		}
	}
	t.Fatalf("imported session not found in %d metas", len(metas))
	return SessionMeta{}
}

// The same session on two machines has to be ranked the same way. #2558 gave
// the import path the counts behind Touched; Words and Counted were still
// dropped, and both feed ranking — Words is the length BM25 normalises by, and
// a zero there is exactly the marathon-wins case search.go's comment describes;
// Counted is the corpus size in messages, where a session with no count
// degrades to one document (#2569).
func TestImportedSessionIsCountedLikeALocalOne(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "claude", "-w-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(home, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))

	line := func(role, text, at string) string {
		return `{"type":"` + role + `","sessionId":"s1","cwd":"/w/app","timestamp":"` + at +
			`","message":{"role":"` + role + `","content":"` + text + `"}}` + "\n"
	}
	body := line("user", "why does the pool starve under load?", "2026-08-01T10:00:00Z") +
		line("assistant", "the fix was the retry budget, not the pool", "2026-08-01T10:02:00Z") +
		line("user", "and what did the pool change cost us?", "2026-08-01T10:03:00Z") +
		line("assistant", "two hours and a revert", "2026-08-01T10:04:00Z")
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(tmp, "local.db")
	t.Setenv("DEJA_INDEX_DIR", local)
	if err := Ensure(local, "", true, nil); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "export")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportFull(local, exp); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(tmp, "remote.db")
	if _, err := Import(remote, exp); err != nil {
		t.Fatal(err)
	}

	find := func(dir string) SessionMeta {
		t.Helper()
		metas, err := AllMeta(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range metas {
			if m.ID == "s1" || m.OrigID == "s1" {
				return m
			}
		}
		t.Fatalf("session not found among %d metas in %s", len(metas), dir)
		return SessionMeta{}
	}
	here, there := find(local), find(remote)
	if here.Words == 0 {
		t.Fatal("the local session has no word count, so this measures nothing")
	}
	if there.Words != here.Words {
		t.Errorf("imported words=%d, local words=%d — the two machines normalise the same session differently",
			there.Words, here.Words)
	}
	if there.Counted != here.Counted {
		t.Errorf("imported counted=%d, local counted=%d — the corpus size disagrees across the sync",
			there.Counted, here.Counted)
	}
}
