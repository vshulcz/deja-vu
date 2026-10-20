package index

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// postingSnapshot is every posting of an index named by what it points at:
// the token, the session and the record. Offsets and session ids differ
// between an updated index and a rebuild, and what they resolve to must not.
func postingSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	byOrd := map[uint32]string{}
	for key, meta := range m.Sessions {
		byOrd[meta.Ord] = key
	}
	f, err := os.Open(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tbl := tablesFromManifest(m)
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, e := range entries {
		if isOverlay(e.Name()) {
			continue // read with its bucket
		}
		data, err := readBucket(filepath.Join(dir, "buckets", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for tok, posts := range data {
			if bucket(tok)+".bin" != e.Name() {
				t.Errorf("token %q filed in %s", tok, e.Name())
			}
			posts = liveOnly(posts, m.DeadRecords)
			if len(posts) == 0 {
				continue
			}
			for _, p := range posts {
				r, err := readRecordAt(f, p.Off, tbl)
				if err != nil {
					t.Fatalf("posting %q at %d: %v", tok, p.Off, err)
				}
				h := fnv.New64a()
				_, _ = h.Write([]byte(r.Text))
				out[tok] = append(out[tok], fmt.Sprintf("%s|%s|%s|%s|%x|tool=%v", byOrd[p.Sid], r.Key, r.Role, r.Time.UTC().Format(time.RFC3339Nano), h.Sum64(), p.Tool))
			}
		}
	}
	snap := map[string]string{}
	for tok, ps := range out {
		sort.Strings(ps)
		snap[tok] = strings.Join(ps, "\n")
	}
	return snap
}

func samePostingsAsRebuild(t *testing.T, inc string) {
	t.Helper()
	fresh := inc + "-postings"
	parityPass(t, fresh, true)
	samePostings(t, inc, fresh)
}

// samePostings fails the test where two indexes post differently.
func samePostings(t *testing.T, inc, fresh string) {
	t.Helper()
	a, b := postingSnapshot(t, inc), postingSnapshot(t, fresh)
	var diffs []string
	for tok := range a {
		if a[tok] != b[tok] {
			diffs = append(diffs, fmt.Sprintf("%q\n  incremental: %s\n  rebuild:     %s", tok, a[tok], b[tok]))
		}
	}
	for tok := range b {
		if _, ok := a[tok]; !ok {
			diffs = append(diffs, fmt.Sprintf("%q missing from the incremental index", tok))
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		if len(diffs) > 10 {
			diffs = diffs[:10]
		}
		t.Errorf("postings differ from a rebuild:\n%s", strings.Join(diffs, "\n"))
	}
}

// A pass that replaces one session tokenizes and encodes that session's
// records, not the store's: every other record is copied as stored and keeps
// its postings, moved to where it now sits (#5383). The postings still answer
// what a rebuild's do.
func TestReplacementPassWorkIsBoundedByTheChange(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	const sessions, turns = 40, 6
	body := func(id string, n int, tail string) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			role, text := "user", fmt.Sprintf(`"why does the %s retry loop spin, take %d"`, id, i)
			if i%2 == 1 {
				role, text = "assistant", fmt.Sprintf(`"the backoff in %s never resets, ещё раз %d 索引"`, id, i)
			}
			b.WriteString(parityClaudeLine(id, fmt.Sprintf("%02d:00Z", i), role, text))
		}
		if tail != "" {
			b.WriteString(parityClaudeLine(id, "59:00Z", "user", tail))
		}
		return b.String()
	}
	for i := 0; i < sessions; i++ {
		id := fmt.Sprintf("s%02d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body(id, turns, ""), false)
	}
	// A tool output long enough to be stored deflated, so a carried record
	// that is compressed is carried too.
	long := strings.Repeat("compiling pkg/ledger.go\\n--- FAIL: TestLedgerCursor\\n", 400)
	parityWrite(t, filepath.Join(proj, "s00.jsonl"),
		parityClaudeLine("s00", "58:00Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./ledger"}}]`)+
			parityClaudeLine("s00", "58:01Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":"`+long+`"}]`), true)
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)

	// Rewritten shorter, which no append can follow: the replacement pass.
	changed := filepath.Join(proj, "s07.jsonl")
	parityWrite(t, changed, body("s07", 2, `"quokka marmalade"`), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(changed, later, later)
	passKeyed.Store(0)
	passEncoded.Store(0)
	parityPass(t, inc, false)
	keyed, encoded := passKeyed.Load(), passEncoded.Load()
	// The control: the pass did replace s07 rather than append to it.
	s, ok, err := FindByIdentity(inc, "claude", "s07")
	if err != nil || !ok || len(s.Messages) != 3 {
		t.Fatalf("s07 after the rewrite = %d messages ok=%v err=%v, want 3", len(s.Messages), ok, err)
	}
	if keyed != 3 || encoded != 3 {
		t.Errorf("the pass tokenized %d and encoded %d records, want the 3 of the session it replaced", keyed, encoded)
	}
	sameAsRebuild(t, inc)
	samePostingsAsRebuild(t, inc)
}

// An old index whose postings do not all read is no source for the carried
// records' postings: they are keyed from their text again, and the result is
// still a rebuild's.
func TestReplacementPassRekeysWhenPostingsAreShort(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	// The pass that writes the log anew: an appending one keeps the postings
	// files it has and reads none of them.
	appendReplacing = false
	defer func() { appendReplacing = true }()
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("s%d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"),
			parityClaudeLine(id, "00:00Z", "user", fmt.Sprintf(`"pgbouncer pool %s"`, id))+
				parityClaudeLine(id, "01:00Z", "assistant", `"the pool is per process"`), false)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	// Postings files that no longer read, each its own way: the rest cannot
	// stand for them, and two workers failing at once must not crash the pass.
	if err := os.WriteFile(filepath.Join(inc, "buckets", bucket("tpgbouncer")+".bin"), []byte("not a bucket"), 0o600); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(inc, "buckets", bucket("tis")+".bin")
	if fi, err := os.Stat(short); err != nil {
		t.Fatal(err)
	} else if err := os.Truncate(short, fi.Size()-1); err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(proj, "s3.jsonl")
	parityWrite(t, changed, parityClaudeLine("s3", "00:00Z", "user", `"narwhal"`), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(changed, later, later)
	passKeyed.Store(0)
	parityPass(t, inc, false)
	if got := passKeyed.Load(); got != 11 {
		t.Errorf("keyed %d records, want all 11 of the store", got)
	}
	samePostingsAsRebuild(t, inc)
}
