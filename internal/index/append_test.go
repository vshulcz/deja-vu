package index

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// The gate lets a kind append because it declares an offset parser; this is
// the other half of that claim — that reading from where the last pass stopped
// actually keeps what was already there and adds what arrived, once each. Six
// kinds gained the path when the gate stopped naming harnesses (#2870), and a
// parser that resumes wrongly loses turns silently.
func TestEveryAppendableKindKeepsWhatItAlreadyHad(t *testing.T) {
	for _, c := range []struct {
		harness string
		env     string
		// rel is where the transcript sits under the harness root, and line is
		// one more record of the shape that file holds.
		rel     []string
		fixture string
		line    string
	}{
		{
			harness: "goose", env: "DEJA_GOOSE_ROOT",
			rel:     []string{"sessions", "20250724_1.jsonl"},
			fixture: filepath.Join("..", "..", "fixtures", "registry", "goose", "sessions", "20250724_1.jsonl"),
			line:    `{"role":"user","created":1785000900,"content":[{"type":"text","text":"appendneedle the second question"}]}`,
		},
		{
			harness: "kimi", env: "DEJA_KIMI_ROOT",
			rel:     []string{"sessions", "wd_demo_0123456789ab", "session_fixture01", "agents", "main", "wire.jsonl"},
			fixture: filepath.Join("..", "..", "fixtures", "registry", "kimi", "sessions", "wd_demo_0123456789ab", "session_fixture01", "agents", "main", "wire.jsonl"),
			// The fixture ends mid-stream, and a part appended to that step
			// is read whole (#4445); an assistant message closes it.
			line: `{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"appendneedle the second question"}]},"time":1782295900000}`,
		},
		{
			harness: "omp", env: "DEJA_OMP_ROOT",
			rel:     []string{"-workspace-demo", "session.jsonl"},
			fixture: filepath.Join("..", "..", "fixtures", "registry", "omp", "session.jsonl"),
			line:    `{"type":"message","id":"zz","parentId":"a1","timestamp":"2026-08-17T11:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"appendneedle the second question"}],"timestamp":1786968000000}}`,
		},
		{
			harness: "prime", env: "DEJA_PRIME_ROOT",
			rel:     []string{"session.jsonl"},
			fixture: filepath.Join("..", "..", "fixtures", "registry", "prime", "session.jsonl"),
			line:    `{"type": "message", "id": "z9", "parentId": "a1", "timestamp": "2026-08-17T11:00:00.000Z", "message": {"role": "user", "content": [{"type": "text", "text": "appendneedle the second question"}], "timestamp": 1786968000000}}`,
		},
		{
			harness: "qwen", env: "DEJA_QWEN_ROOT",
			rel:     []string{"projects", "-workspace-registry-demo", "chats", "registry-qwen.jsonl"},
			fixture: filepath.Join("..", "..", "fixtures", "registry", "qwen", "projects", "-workspace-registry-demo", "chats", "registry-qwen.jsonl"),
			line:    `{"type":"user","sessionId":"registry-qwen","timestamp":"2026-07-17T09:01:00Z","message":{"role":"user","parts":[{"text":"appendneedle the second question"}]}}`,
		},
		{
			harness: "openclaw", env: "DEJA_OPENCLAW_ROOT",
			rel:     []string{"main", "sessions", "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl"},
			fixture: filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agents", "main", "sessions", "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl"),
			line:    `{"type":"message","id":"z9","parentId":"a1","timestamp":"2026-07-20T09:01:00Z","message":{"role":"user","content":[{"type":"text","text":"appendneedle the second question"}]}}`,
		},
	} {
		t.Run(c.harness, func(t *testing.T) {
			tmp := t.TempDir()
			setHome(t, tmp)
			root := filepath.Join(tmp, c.harness)
			t.Setenv(c.env, root)
			path := filepath.Join(append([]string{root}, c.rel...)...)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(c.fixture)
			if err != nil {
				t.Skipf("no fixture for %s: %v", c.harness, err)
			}
			if err = os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(tmp, "index.db")
			if err = Ensure(dir, "", true, nil); err != nil {
				t.Fatal(err)
			}
			was := sessionsOf(t, dir, c.harness)
			if len(was) == 0 {
				t.Skipf("%s: the fixture indexed nothing, so an append proves nothing", c.harness)
			}

			// One more record, the way the harness writes them.
			if err = os.WriteFile(path, append(body, []byte(c.line+"\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
			var progress bytes.Buffer
			if err = Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			// The append path, not a re-read that happens to give the same
			// answer: a whole-file pass is correct too, and the point of the
			// gate is that it does not happen (#2870).
			if !strings.Contains(progress.String(), "updated 1 file") {
				t.Errorf("growth did not take the append path:\n%s", progress.String())
			}
			// The same sessions, still carrying what the header told the
			// parser. An offset parser that resumes past the header loses the
			// session's identity rather than its bytes: the turn lands under a
			// key of its own and the session it belongs to never grows.
			now := sessionsOf(t, dir, c.harness)
			for key, before := range was {
				after, ok := now[key]
				if !ok {
					t.Errorf("%s is gone after the append", key)
					continue
				}
				if after.Counted < before.Counted {
					t.Errorf("%s lost turns: %d, was %d", key, after.Counted, before.Counted)
				}
				if after.Project != before.Project {
					t.Errorf("%s changed project: %q, was %q", key, after.Project, before.Project)
				}
				if after.Title != before.Title {
					t.Errorf("%s changed title: %q, was %q", key, after.Title, before.Title)
				}
			}
			if len(now) != len(was) {
				t.Errorf("the append made %d sessions out of %d", len(now), len(was))
			}
			ss, err := Search(dir, search.Options{Query: "appendneedle", All: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(ss) != 1 {
				t.Errorf("the appended turn is in %d sessions, want 1", len(ss))
			}
			for _, s := range ss {
				n := 0
				for _, m := range s.Messages {
					if strings.Contains(m.Text, "appendneedle") {
						n++
					}
				}
				if n != 1 {
					t.Errorf("the appended turn is in the index %d times", n)
				}
			}
		})
	}
}

// sessionsOf is what a harness has in the store, by key.
func sessionsOf(t *testing.T, dir, harness string) map[string]SessionMeta {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]SessionMeta{}
	for _, m := range metas {
		if m.Harness == harness {
			out[m.Harness+":"+m.ID] = m
		}
	}
	return out
}

// A kind that declares an offset parser is a kind whose files can be read from
// where the last pass stopped. The gate that decides this kept a list of
// harness names instead, and six kinds with a parser were never on it — so
// their files were re-read whole on every pass, which is what #2075 found on
// the database side (#2870).
func TestEveryKindWithAnOffsetParserCanAppend(t *testing.T) {
	for _, k := range sources.KindsWithOffsetParsers() {
		if !appendableKind(k) {
			t.Errorf("%s declares an offset parser the append gate never reaches", k)
		}
	}
}

// replaceStore writes n Claude transcripts of a few turns each, indexes them
// into inc and returns the root, the project dir and inc.
func replaceStore(t *testing.T, n int) (string, string) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	for i := range n {
		id := fmt.Sprintf("s%03d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"), replaceBody(id, ""), false)
	}
	inc := filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	return proj, inc
}

func replaceBody(id, first string) string {
	var b strings.Builder
	for i := range 6 {
		role, text := "user", fmt.Sprintf(`"%swhy does the %s retry loop spin on ledger%d, take %d"`, first, id, i, i)
		if i%2 == 1 {
			role, text = "assistant", fmt.Sprintf(`"the backoff in %s never resets after pgbouncer%d drops, %s"`, id, i, strings.Repeat("cursor wal fsync ", 20))
		}
		first = ""
		b.WriteString(parityClaudeLine(id, fmt.Sprintf("%02d:00Z", i), role, text))
	}
	return b.String()
}

// rewriteSession replaces one transcript with a body that no append follows.
func rewriteSession(t *testing.T, proj, id string) {
	t.Helper()
	p := filepath.Join(proj, id+".jsonl")
	parityWrite(t, p, replaceBody(id, "quokka "), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
}

// indexFiles is every file under dir as stat saw it, by path below dir.
func indexFiles(t *testing.T, dir string) map[string]os.FileInfo {
	t.Helper()
	out := map[string]os.FileInfo{}
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		// Windows reads a file's identity from its path when first asked;
		// asked later, after a swap, it would be the new file's.
		_ = os.SameFile(fi, fi)
		out[rel] = fi
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// bytesWritten is what a pass wrote into dir, per file: all of a file that is
// not the one that was there, the growth of one that is.
func bytesWritten(before, after map[string]os.FileInfo) map[string]int64 {
	out := map[string]int64{}
	for rel, fi := range after {
		if was, ok := before[rel]; ok && os.SameFile(was, fi) {
			if d := fi.Size() - was.Size(); d > 0 {
				out[rel] = d
			}
			continue
		}
		out[rel] = fi.Size()
	}
	return out
}

// logAndPostings sums what went to records.bin and the postings, leaving out
// the manifest and the offset table: every pass writes those whole, an append
// of one turn included.
func logAndPostings(w map[string]int64) int64 {
	var n int64
	for rel, b := range w {
		if rel == "records.bin" || strings.HasPrefix(rel, "buckets"+string(filepath.Separator)) {
			n += b
		}
	}
	return n
}

// A pass that replaces one session of a store writes that session's records
// and postings, not the store's: records.bin stays the file it was and grows
// by the session, and the postings land in overlays (#5387). Measured against
// a store of 40 and of 160 sessions, so a cost that scales with the store
// cannot pass for the change.
func TestReplacingPassWritesTheChangeNotTheStore(t *testing.T) {
	var written [2]int64
	for i, n := range []int{40, 160} {
		proj, inc := replaceStore(t, n)
		log := filepath.Join(inc, "records.bin")
		before := indexFiles(t, inc)
		rewriteSession(t, proj, "s007")
		parityPass(t, inc, false)
		after := indexFiles(t, inc)
		if !os.SameFile(before["records.bin"], after["records.bin"]) {
			t.Fatalf("%d sessions: records.bin was written anew", n)
		}
		w := bytesWritten(before, after)
		written[i] = logAndPostings(w)
		// The bound: the session's records once, its postings a few times
		// over, and the overlay headers.
		grew := w["records.bin"]
		t.Logf("%d sessions: %d bytes of log and postings for a %d-byte session, log %d bytes", n, written[i], grew, after["records.bin"].Size())
		if limit := 4*grew + 16<<10; written[i] > limit {
			t.Errorf("%d sessions: the pass wrote %d bytes of log and postings, want at most %d for a %d-byte session (%v)", n, written[i], limit, grew, w)
		}
		if fi, err := os.Stat(log); err != nil || 10*grew > fi.Size() {
			t.Fatalf("%d sessions: the replaced session is %d of a %d-byte log, too much of it to tell the two apart", n, grew, fi.Size())
		}
		s, ok, err := FindByIdentity(inc, "claude", "s007")
		if err != nil || !ok || !strings.HasPrefix(s.Messages[0].Text, "quokka") || len(s.Messages) != 6 {
			t.Fatalf("%d sessions: s007 after the pass = %+v ok=%v err=%v", n, s.Messages, ok, err)
		}
		sameAsRebuild(t, inc)
		offsetTableMatchesAWalk(t, inc, true)
	}
	if written[1] > 2*written[0] {
		t.Errorf("the pass wrote %d bytes on 40 sessions and %d on 160: it grows with the store", written[0], written[1])
	}

	// The control: with the log written anew, the same pass writes the store.
	proj, inc := replaceStore(t, 40)
	appendReplacing = false
	defer func() { appendReplacing = true }()
	before := indexFiles(t, inc)
	rewriteSession(t, proj, "s007")
	parityPass(t, inc, false)
	w := bytesWritten(before, indexFiles(t, inc))
	if got := logAndPostings(w); got < before["records.bin"].Size() {
		t.Errorf("rewriting the log wrote %d bytes of a %d-byte log: the measure does not see a rewrite", got, before["records.bin"].Size())
	}
}

// Replaced records stay in the log until they are a quarter of it, and then
// the next replacing pass writes the log without them.
func TestDeadRecordsAreWrittenOutPastAQuarter(t *testing.T) {
	proj, inc := replaceStore(t, 8)
	inode := indexFiles(t, inc)["records.bin"]
	compacted := false
	for i := 0; i < 8 && !compacted; i++ {
		rewriteSession(t, proj, fmt.Sprintf("s%03d", i))
		parityPass(t, inc, false)
		m, err := readManifest(inc)
		if err != nil {
			t.Fatal(err)
		}
		now := indexFiles(t, inc)["records.bin"]
		if !os.SameFile(inode, now) {
			compacted = true
			if len(m.DeadRecords) != 0 || m.DeadBytes != 0 {
				t.Errorf("a written-out log still lists %d dead records", len(m.DeadRecords))
			}
			if hasOverlays(t, inc) {
				t.Error("a written-out log kept postings overlays")
			}
			break
		}
		if len(m.DeadRecords) == 0 || m.DeadBytes <= 0 {
			t.Fatalf("pass %d appended but lists %d dead records", i, len(m.DeadRecords))
		}
	}
	if !compacted {
		t.Fatal("eight replaced sessions of eight never wrote the log out")
	}
	sameAsRebuild(t, inc)
	offsetTableMatchesAWalk(t, inc, true)
}

func hasOverlays(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if isOverlay(e.Name()) {
			return true
		}
	}
	return false
}

// A pass that fails after appending leaves the previous index as it was:
// its log cut back, its manifest untouched, and the next pass appends again
// rather than rebuilding.
func TestFailedAppendLeavesThePreviousIndex(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	was := indexFiles(t, inc)["records.bin"].Size()
	rewriteSession(t, proj, "s003")
	beforeAppendSwap = func() error { return errors.New("killed") }
	err := Ensure(inc, "", false, io.Discard)
	beforeAppendSwap = nil
	if err == nil {
		t.Fatal("the pass did not fail")
	}
	if got := indexFiles(t, inc)["records.bin"].Size(); got != was {
		t.Fatalf("records.bin is %d bytes after the failed pass, was %d", got, was)
	}
	if _, statErr := os.Stat(filepath.Join(inc, appendPendingFile)); statErr == nil {
		t.Error("the pending mark outlived the pass")
	}
	if Damaged(inc) {
		t.Fatalf("the previous index reads as damaged: %s", DamageReason(inc))
	}
	s, ok, err := FindByIdentity(inc, "claude", "s003")
	if err != nil || !ok || strings.HasPrefix(s.Messages[0].Text, "quokka") {
		t.Fatalf("s003 after the failed pass = %+v ok=%v err=%v, want the old one", s.Messages, ok, err)
	}
	inode := indexFiles(t, inc)["records.bin"]
	parityPass(t, inc, false)
	if !os.SameFile(inode, indexFiles(t, inc)["records.bin"]) {
		t.Error("the pass after a failed one wrote the log anew")
	}
	sameAsRebuild(t, inc)
}

// A pass killed mid-append leaves records.bin longer than its manifest and the
// pending mark beside it. The next lock taker cuts the tail off, so the store
// is not rebuilt for it; the control shows the same tail without the mark is
// what a rebuild is for.
func TestKilledAppendIsCutBackUnderTheLock(t *testing.T) {
	_, inc := replaceStore(t, 6)
	log := filepath.Join(inc, "records.bin")
	was := indexFiles(t, inc)["records.bin"].Size()
	appendGarbage := func() {
		f, err := os.OpenFile(log, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte{9, 0, 0, 0, 1, 2, 3})
		_ = f.Close()
	}
	if err := os.WriteFile(filepath.Join(inc, appendPendingFile), []byte(strconv.FormatInt(was, 10)), 0o600); err != nil {
		t.Fatal(err)
	}
	appendGarbage()
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if got := indexFiles(t, inc)["records.bin"].Size(); got != was {
		t.Fatalf("records.bin is %d bytes after recovery, want %d", got, was)
	}
	if Damaged(inc) {
		t.Fatalf("damaged after recovery: %s", DamageReason(inc))
	}
	// Without the mark the tail is nobody's to cut.
	appendGarbage()
	unlock, err = lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if got := indexFiles(t, inc)["records.bin"].Size(); got == was {
		t.Fatal("a tail with no mark was cut")
	}
}

// When statting or truncating records.bin fails during recovery, append.pending
// stays on disk and appendsInPlace refuses to stage another append until a
// subsequent lock acquisition cuts the uncommitted tail back.
func TestUndoAppendKeepsPendingWhenTruncateFails(t *testing.T) {
	proj, inc := replaceStore(t, 8)
	logPath := filepath.Join(inc, "records.bin")
	pendingPath := filepath.Join(inc, appendPendingFile)
	was := indexFiles(t, inc)["records.bin"].Size()
	inode := indexFiles(t, inc)["records.bin"]

	origTrunc := truncateRecordsFile
	t.Cleanup(func() {
		truncateRecordsFile = origTrunc
		beforeAppendSwap = nil
	})

	if wErr := os.WriteFile(pendingPath, []byte(strconv.FormatInt(was, 10)), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	f, openErr := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if openErr != nil {
		t.Fatal(openErr)
	}
	if _, writeErr := f.Write([]byte{9, 0, 0, 0, 1, 2, 3}); writeErr != nil {
		t.Fatal(writeErr)
	}
	_ = f.Close()

	truncateRecordsFile = func(_ string, _ int64) error {
		return errors.New("sharing violation")
	}
	unlock, lockErr := lockDir(inc)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	unlock()

	if _, statErr := os.Stat(pendingPath); statErr != nil {
		t.Fatalf("append.pending removed when truncate failed: %v", statErr)
	}
	probeTmp := inc + ".probe"
	t.Cleanup(func() { _ = os.RemoveAll(probeTmp) })
	if mkErr := os.MkdirAll(filepath.Join(probeTmp, "buckets"), 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	if appendsInPlace(inc, probeTmp, Manifest{RecordsSize: was}) {
		t.Fatal("appendsInPlace staged append while unrecovered append.pending exists")
	}

	truncateRecordsFile = origTrunc
	unlock2, lockErr2 := lockDir(inc)
	if lockErr2 != nil {
		t.Fatal(lockErr2)
	}
	unlock2()
	if got := indexFiles(t, inc)["records.bin"].Size(); got != was {
		t.Fatalf("records.bin = %d bytes after recovery, want %d", got, was)
	}
	if _, statErr := os.Stat(pendingPath); !os.IsNotExist(statErr) {
		t.Fatalf("append.pending still present after successful recovery: %v", statErr)
	}
	if Damaged(inc) {
		t.Fatalf("damaged after recovery: %s", DamageReason(inc))
	}

	rewriteSession(t, proj, "s003")
	beforeAppendSwap = func() error { return errors.New("killed") }
	truncateRecordsFile = func(_ string, _ int64) error { return errors.New("sharing violation") }
	if ensureErr := Ensure(inc, "", false, io.Discard); ensureErr == nil {
		t.Fatal("expected Ensure failure under beforeAppendSwap hook")
	}
	if _, statErr := os.Stat(pendingPath); statErr != nil {
		t.Fatalf("append.pending removed during failed pass when truncate failed: %v", statErr)
	}

	beforeAppendSwap = nil
	truncateRecordsFile = origTrunc
	parityPass(t, inc, false)
	if !os.SameFile(inode, indexFiles(t, inc)["records.bin"]) {
		t.Error("pass after delayed recovery rebuilt records.bin instead of appending in place")
	}
	sameAsRebuild(t, inc)

	missingDir := t.TempDir()
	missingPending := filepath.Join(missingDir, appendPendingFile)
	if wErr := os.WriteFile(missingPending, []byte("12"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	undoAppend(missingDir, 12)
	if _, statErr := os.Stat(missingPending); !os.IsNotExist(statErr) {
		t.Fatalf("append.pending still present when records.bin is absent: %v", statErr)
	}

	notADir := filepath.Join(t.TempDir(), "notadir")
	if wErr := os.WriteFile(notADir, []byte("x"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	undoAppend(notADir, 12)
	if appendsInPlace(notADir, probeTmp, Manifest{RecordsSize: 12}) {
		t.Fatal("appendsInPlace succeeded on non-directory path")
	}
}

// A staging directory a pass could not clear may still hold a hard link to
// the live log. The next pass that writes the log anew must make its own file
// rather than truncate that one, which is the live log under another name.
func TestALeftoverStagingLinkNeverTruncatesTheLiveLog(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop a delete on windows or as root")
	}
	proj, inc := replaceStore(t, 12)
	log := filepath.Join(inc, "records.bin")
	was := indexFiles(t, inc)["records.bin"].Size()
	tmp := inc + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(log, filepath.Join(tmp, "records.bin")); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	// Stands in for a link Windows will not delete while a reader holds the
	// log open.
	if err := os.Chmod(tmp, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(tmp, 0o700) }()
	appendReplacing = false
	defer func() { appendReplacing = true }()
	rewriteSession(t, proj, "s004")
	_ = Ensure(inc, "", false, io.Discard)
	if got := indexFiles(t, inc)["records.bin"].Size(); got < was {
		t.Fatalf("the live log went from %d to %d bytes through the leftover link", was, got)
	}
	if Damaged(inc) {
		t.Fatalf("damaged: %s", DamageReason(inc))
	}
}

// A pass that lands while the per-prompt recall ranks leaves it the answer a
// ranking made after the pass gives: it is read again from the new
// generation, not scored from one bucket of each (#5459).
func TestARankingAcrossAPassAnswersAsOneAfterIt(t *testing.T) {
	proj, inc := replaceStore(t, 40)
	for range 3 {
		rewriteSession(t, proj, "s007")
		parityPass(t, inc, false)
	}
	if m, err := readManifest(inc); err != nil || len(m.DeadRecords) == 0 {
		t.Fatalf("control: the rewrites listed no dead records (err %v)", err)
	}
	// The pass holds the lock, so the ranking reads without it, as it does
	// beside a pass in another process.
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// Between a bucket and its overlays, on each read of the session's word:
	// the ranking reads it twice, once to see it is there at all.
	landed := 0
	bucketBaseRead = func(tok string) {
		if landed == 2 || tok != "ts007" {
			return
		}
		landed++
		rewriteSession(t, proj, "s007")
		if err := ensureHeld(inc, "", true, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { bucketBaseRead = nil }()
	ranked := func() string {
		ss, matched, strong, _, idf, err := ProjectRelevantNamingCut(inc, nil, []string{"s007", "pgbouncer3"}, 5, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for i, s := range ss {
			fmt.Fprintf(&b, "%s:%d:%d:%d ", s.ID, len(s.Messages), matched[i], strong[i])
		}
		fmt.Fprintf(&b, "idf %v", idf)
		return b.String()
	}
	got := ranked()
	if landed != 2 {
		t.Fatalf("control: %d passes landed during the ranking, want 2", landed)
	}
	if want := ranked(); got != want {
		t.Fatalf("ranked across the passes:\n %s\nafter them:\n %s", got, want)
	}
}

// A second pass that lands while a reader reads a session again for the
// first one zeroes what that reread reads. The reader goes once more, and
// gets the session as the last pass left it (#5460).
func TestASecondPassDuringTheRereadStillGetsTheSession(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	pinned, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	meta := pinned.Sessions["claude:s007"]
	rewriteSession(t, proj, "s007")
	parityPass(t, inc, false)
	landed := 0
	sessionReread = func() {
		if landed > 0 {
			return
		}
		landed++
		p := filepath.Join(proj, "s007.jsonl")
		parityWrite(t, p, replaceBody("s007", "wombat "), false)
		later := time.Now().Add(4 * time.Second)
		_ = os.Chtimes(p, later, later)
		parityPass(t, inc, false)
	}
	defer func() { sessionReread = nil }()
	s, ok, err := loadSessionMeta(inc, pinned, meta)
	if err != nil || !ok {
		t.Fatalf("loadSessionMeta: ok=%v err=%v", ok, err)
	}
	if landed != 1 {
		t.Fatalf("control: %d passes landed during the reread, want 1", landed)
	}
	if len(s.Messages) != 6 || !strings.Contains(s.Messages[0].Text, "wombat") {
		t.Fatalf("got %d messages, want the 6 of the last rewrite", len(s.Messages))
	}
}

// secretStore is replaceStore with s007's first turn carrying word, rewritten
// without it and indexed again. It returns the index and a copy of the log
// from before the rewrite.
func secretStore(t *testing.T, word string) (inc string, before []byte) {
	t.Helper()
	proj, inc := replaceStore(t, 12)
	p := filepath.Join(proj, "s007.jsonl")
	parityWrite(t, p, replaceBody("s007", word+" "), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
	parityPass(t, inc, false)
	if !filesHold(t, inc, word) {
		t.Fatalf("control: no index file holds %q before the rewrite", word)
	}
	before, err := os.ReadFile(filepath.Join(inc, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	parityWrite(t, p, replaceBody("s007", ""), false)
	later = later.Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
	inode := indexFiles(t, inc)["records.bin"]
	parityPass(t, inc, false)
	if !os.SameFile(inode, indexFiles(t, inc)["records.bin"]) {
		t.Fatal("the rewrite wrote the log anew: the append path is not under test")
	}
	if m, err := readManifest(inc); err != nil || len(m.DeadRecords) == 0 {
		t.Fatalf("the rewrite listed no dead records (err %v)", err)
	}
	return inc, before
}

// filesHold reports whether any file under dir holds word.
func filesHold(t *testing.T, dir, word string) bool {
	t.Helper()
	found := false
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || found {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // walks the test's own temp dir
		if err == nil && bytes.Contains(b, []byte(word)) {
			found = true
		}
		return nil
	})
	return found
}

// Text a user took out of a transcript leaves the index's files on the pass
// that replaced it: the record log and the postings both (#5429).
func TestRemovedTextLeavesTheIndexFilesOnTheReplacingPass(t *testing.T) {
	inc, _ := secretStore(t, "hunter2zanzibarpw")
	if filesHold(t, inc, "hunter2zanzibarpw") {
		var where []string
		_ = filepath.Walk(inc, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("hunter2zanzibarpw")) { //nolint:gosec // walks the test's own temp dir
					where = append(where, p)
				}
			}
			return nil
		})
		t.Fatalf("the removed word is still in %v", where)
	}
	sameAsRebuild(t, inc)
	if Damaged(inc) {
		t.Fatalf("damaged: %s", DamageReason(inc))
	}
}

// An older deja meeting this index rebuilds it and carries kept sessions out
// of the log, walking it without the dead list. The replaced records must not
// read as the session's to that walk (#5428).
func TestADeadRecordIsNoSessionsToAWalkThatIgnoresTheDeadList(t *testing.T) {
	inc, _ := secretStore(t, "staleseven")
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	blind := tablesFromStrings(m.RecordStrings) // what a format-2 build reads
	dead := tablesFromManifest(m)
	walked := 0
	err = eachStoredRecord(filepath.Join(inc, "records.bin"), blind, func(off int64, payload []byte) error {
		walked++
		r, decodeErr := decodeRecord(payload, blind)
		if decodeErr != nil {
			return decodeErr
		}
		if !dead.isDead(off) {
			return nil
		}
		// What the older carry asks of a record: a transcript it had on
		// file, and a session it had.
		_, file := m.Files[r.SourcePath]
		_, session := m.Sessions[r.Key]
		if file || session || r.Text != "" {
			t.Errorf("dead record at %d reads as %s of %s: %q", off, r.Key, r.SourcePath, r.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if walked <= len(m.DeadRecords) {
		t.Fatalf("walked %d records of a log with %d dead", walked, len(m.DeadRecords))
	}
}

// A pass killed between its swap and the overwrite leaves the mark; the next
// lock taker overwrites every dead record the manifest lists.
func TestALockTakerFinishesAnInterruptedScrub(t *testing.T) {
	inc, before := secretStore(t, "hunter2zanzibarpw")
	log := filepath.Join(inc, "records.bin")
	f, err := os.OpenFile(log, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The log as it was before the overwrite: the old bytes back in place.
	if _, err = f.WriteAt(before, 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err = os.WriteFile(filepath.Join(inc, scrubPendingFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); !bytes.Contains(b, []byte("hunter2zanzibarpw")) {
		t.Fatal("control: the restored log does not hold the word")
	}
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if b, _ := os.ReadFile(log); bytes.Contains(b, []byte("hunter2zanzibarpw")) {
		t.Fatal("the lock taker left the dead records as they were")
	}
	if _, err := os.Stat(filepath.Join(inc, scrubPendingFile)); err == nil {
		t.Error("the mark outlived the overwrite")
	}
	sameAsRebuild(t, inc)
}

// A postings overlay that goes missing is damage, as a missing bucket is
// (#5426).
func TestALostOverlayReadsAsDamage(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	rewriteSession(t, proj, "s003")
	parityPass(t, inc, false)
	entries, err := os.ReadDir(filepath.Join(inc, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	var overlay string
	for _, e := range entries {
		if isOverlay(e.Name()) {
			overlay = filepath.Join(inc, "buckets", e.Name())
			break
		}
	}
	if overlay == "" {
		t.Fatal("the replacing pass wrote no overlay")
	}
	if Damaged(inc) {
		t.Fatalf("control: damaged before the loss: %s", DamageReason(inc))
	}
	invalidateManifestCache(inc)
	if err := os.Remove(overlay); err != nil {
		t.Fatal(err)
	}
	if got := DamageReason(inc); got != "some postings files are missing" {
		t.Fatalf("DamageReason after losing %s = %q", filepath.Base(overlay), got)
	}
}

// An in-place merge hands back the overlays it made redundant rather than
// removing them: the caller commits a count without them first, so a reader's
// damage check never finds fewer files than the manifest it read (#5426).
func TestAnInPlaceMergeLeavesItsRedundantOverlaysToTheCaller(t *testing.T) {
	d := t.TempDir()
	base := filepath.Join(d, "ab.bin")
	layer := func(n, from int) map[string][]posting {
		out := map[string][]posting{}
		for i := range n {
			out["ab"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+strings.Repeat("y", i/26)] = []posting{{Off: int64(from + i), Sid: 1}}
		}
		return out
	}
	for i, data := range []map[string][]posting{layer(4000, 0), layer(200, 10000), layer(20, 20000), layer(12, 30000)} {
		p := base
		if i > 0 {
			p = overlayPath(base, i)
		}
		if err := writeBucket(p, data); err != nil {
			t.Fatal(err)
		}
	}
	files := countBucketFiles(d)
	stale, err := addOverlays(d, d, bucketPostings{"ab": layer(10, 40000)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if staleCount(stale) == 0 {
		t.Fatal("control: the add merged nothing away")
	}
	if got := countBucketFiles(d); got < files {
		t.Fatalf("the merge removed files itself: %d before, %d after", files, got)
	}
	for _, g := range stale {
		for _, p := range g {
			if _, err = os.Stat(p); err != nil {
				t.Fatalf("%s went before the caller committed: %v", filepath.Base(p), err)
			}
		}
	}
	dropStale(stale)
	if got := countBucketFiles(d); got != files+1-staleCount(stale) && got != files-staleCount(stale) {
		t.Fatalf("after the drop: %d files", got)
	}
	data, err := readBucket(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4000 {
		t.Fatalf("the bucket and its overlays hold %d words after the merge", len(data))
	}
}

// On Windows a reader holding the live log keeps the parked copy of the last
// index, which shares it, from going. The next swap parks one name further
// rather than failing, and recovery restores the newest copy.
func TestASwapParksPastACopyAReaderHolds(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "index.db")
	for _, d := range []string{dir, dir + ".old", dir + ".next"} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "id"), []byte(filepath.Base(d)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prevOS, prevRemove := renameOS, removeParked
	renameOS = "windows"
	removeParked = func(p string) error {
		if p == dir+".old" {
			return &os.PathError{Op: "remove", Path: p, Err: syscall.Errno(32)}
		}
		return os.RemoveAll(p)
	}
	defer func() { renameOS, removeParked = prevOS, prevRemove }()
	if err := swapIndexDir(dir, dir+".next"); err != nil {
		t.Fatalf("the swap failed on a held parking spot: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "id")); string(b) != "index.db.next" {
		t.Fatalf("live index is %q", b)
	}
	// The swap's own copy went once it was done with; the held one stays.
	if _, err := os.Stat(dir + ".old.1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copy this swap parked is still there: %v", err)
	}
	// A crash between the two renames: the newest copy is the one put back.
	if err := os.Rename(dir, dir+".old.1"); err != nil {
		t.Fatal(err)
	}
	recoverIndexDir(dir)
	if b, _ := os.ReadFile(filepath.Join(dir, "id")); string(b) != "index.db.next" {
		t.Fatalf("recovery restored %q, want the newest copy", b)
	}
	// Not a reader: the message #1009 asked for still comes.
	removeParked = func(p string) error {
		return &os.PathError{Op: "remove", Path: p, Err: syscall.EPERM}
	}
	if err := os.MkdirAll(dir+".next", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := swapIndexDir(dir, dir+".next"); err == nil || !strings.Contains(err.Error(), "remove that directory") {
		t.Fatalf("a parking spot held by permissions: err = %v", err)
	}
}

// A reader that read the manifest before a replacing pass swapped in, and the
// records after the pass zeroed the ones it replaced, gets the session as the
// new manifest has it, not an empty one.
func TestAReaderHoldingTheLastManifestStillGetsAReplacedSession(t *testing.T) {
	proj, inc := replaceStore(t, 12)
	pinned, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := pinned.Sessions["claude:s007"]
	if !ok {
		t.Fatal("control: no claude:s007 in the manifest")
	}
	rewriteSession(t, proj, "s007")
	parityPass(t, inc, false)
	if m, manifestErr := readManifest(inc); manifestErr != nil || len(m.DeadRecords) == 0 {
		t.Fatalf("control: the rewrite listed no dead records (err %v)", manifestErr)
	}
	s, ok, err := loadSessionMeta(inc, pinned, meta)
	if err != nil || !ok {
		t.Fatalf("loadSessionMeta: ok=%v err=%v", ok, err)
	}
	if len(s.Messages) != 6 || !strings.Contains(s.Messages[0].Text, "quokka") {
		t.Fatalf("a reader on the last manifest got %d messages, want the 6 of the rewrite", len(s.Messages))
	}
}

// `deja index` on an index that looks up to date still finishes a scrub a
// killed pass left, and clears the parked copy it left: it returns before
// Ensure takes the lock, so nothing else would.
func TestTheUpToDatePathFinishesAnInterruptedScrub(t *testing.T) {
	inc, before := secretStore(t, "hunter2zanzibarpw")
	log := filepath.Join(inc, "records.bin")
	f, err := os.OpenFile(log, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(before, 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(inc, scrubPendingFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(inc+".old", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inc+".old", "sessions.gob"), []byte("hunter2zanzibarpw"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(inc)
	if fresh, _ := UpToDate(inc, ""); !fresh {
		t.Fatal("control: the index does not read as up to date")
	}
	SweepStaleTmp(inc)
	if b, _ := os.ReadFile(log); bytes.Contains(b, []byte("hunter2zanzibarpw")) {
		t.Error("the removed word is still in records.bin")
	}
	if _, err := os.Stat(inc + ".old"); err == nil {
		t.Error("the parked copy is still there")
	}
	if _, err := os.Stat(filepath.Join(inc, scrubPendingFile)); err == nil {
		t.Error("the mark is still there")
	}
}

// A pass killed after it zeroed the records and before it moved the
// sidecars' fingerprint leaves them naming the log as it was. The lock taker
// that finishes the scrub moves them, so they keep answering.
func TestFinishingAScrubRestampsTheSidecarsAKilledPassLeft(t *testing.T) {
	inc, before := secretStore(t, "hunter2zanzibarpw")
	log := filepath.Join(inc, "records.bin")
	_, after, err := logFingerprint(log)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(log, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(before, 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	// The state just before the scrub: the log unzeroed, the sidecars
	// stamped for it, the mark as the pass writes it.
	_, pre, err := logFingerprint(log)
	if err != nil {
		t.Fatal(err)
	}
	restamp(filepath.Join(inc, recordOffsetsFile), after, pre)
	if err = markScrubPending(inc); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	// Killed here: the records zeroed, the sidecars not yet moved.
	if err = scrubRecords(log, m.DeadRecords); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	lf, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if _, err := readRecordOffsetsFor(inc, lf, nil); err != nil {
		t.Fatalf("recoffsets.bin no longer matches the log: %v", err)
	}
}

// A CJK word is posted as bigrams, each of whose bucket is named from a hash
// of the whole key. Removed, none of its bigrams stays readable.
func TestRemovedCJKTextLeavesThePostings(t *testing.T) {
	inc, _ := secretStore(t, "麒麟獬豸")
	for _, pair := range []string{"麒麟", "麟獬", "獬豸"} {
		if filesHold(t, filepath.Join(inc, "buckets"), pair) {
			t.Errorf("the bigram %s of the removed word is still in buckets/", pair)
		}
	}
	sameAsRebuild(t, inc)
}

// A blanked word stays in its bucket and keeps nothing the bucket's name
// does not already say: the two letters an ASCII bucket is named after, and
// none of a word filed under a hash.
func TestBlankedWordKeepsOnlyTheBucketName(t *testing.T) {
	if n := len(hashedFillers()); n != len(hexBuckets) {
		t.Fatalf("fillers for %d of %d hashed buckets", n, len(hexBuckets))
	}
	for _, tok := range []string{"t麒麟", "t麟", "да", "éa", "привет", "zanzibar", "naïve", "я"} {
		b, ok := blankedWord(tok)
		if !ok {
			if len(tok) > 2 {
				t.Errorf("%q: not blanked", tok)
			}
			continue
		}
		if len(b) != len(tok) || bucket(string(b)) != bucket(tok) {
			t.Errorf("%q: blanked to %q, bucket %s, want %s", tok, b, bucket(string(b)), bucket(tok))
		}
		keep := 0
		if bucket(tok) == safe(tok[:2]) {
			keep = 2
		}
		if !bytes.Equal(b[:keep], []byte(tok[:keep])) {
			t.Errorf("%q: blanked to %q, which moved the bucket's letters", tok, b)
		}
		for _, r := range tok[keep:] {
			if r >= utf8.RuneSelf && bytes.Contains(b, []byte(string(r))) || r < utf8.RuneSelf && keep > 0 && bytes.IndexByte(b[keep:], byte(r)) >= 0 { //nolint:gosec // r < utf8.RuneSelf on this side of the ||
				t.Errorf("%q: blanked to %q, which still holds %q", tok, b, r)
			}
		}
	}
}

// Both command tables are mined from every record the index holds, so rebuilding
// them on an append that cannot have changed them is a scan of records.bin for
// nothing — 2.9s of an 8.6s pass on a 191 MB store. An append of speech leaves
// them alone; an append that carries a command rebuilds them (#3500).
func TestAnAppendRebuildsTheCommandTablesOnlyWhenItCarriesWork(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two sessions running the same command, because the table keeps what
	// recurs — one sighting writes no table to compare against.
	write(t, filepath.Join(claude, "p", "first.jsonl"),
		claudeLine("s-first", "2026-01-02T03:04:05Z", "the exporter retries without a pause")+
			claudeBashLine("s-first", "2026-01-02T03:05:00Z", "go test ./internal/exporter/"))
	write(t, filepath.Join(claude, "p", "second.jsonl"),
		claudeLine("s-second", "2026-01-02T04:04:05Z", "the exporter again")+
			claudeBashLine("s-second", "2026-01-02T04:05:00Z", "go test ./internal/exporter/"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	stamp := func() int64 {
		fi, err := os.Stat(filepath.Join(dir, commandsFile))
		if err != nil {
			t.Fatalf("the command table was never written: %v", err)
		}
		return fi.ModTime().UnixNano()
	}
	was := stamp()

	write(t, filepath.Join(claude, "p", "speech.jsonl"),
		claudeLine("s-speech", "2026-01-02T06:00:00Z", "the billing webhook retries twice"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if now := stamp(); now != was {
		t.Error("an append of speech rebuilt the command table, which it cannot change")
	}

	write(t, filepath.Join(claude, "p", "work.jsonl"),
		claudeLine("s-work", "2026-01-02T07:00:00Z", "run the suite")+
			claudeBashLine("s-work", "2026-01-02T07:01:00Z", "go test ./internal/exporter/"))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if now := stamp(); now == was {
		t.Error("an append carrying a command left the command table as it was")
	}
}

// claudeBashLine is one assistant turn running a command, the shape the command
// table is mined from.
func claudeBashLine(sid, ts, cmd string) string {
	return `{"type":"assistant","sessionId":"` + sid + `","cwd":"/w","timestamp":"` + ts +
		`","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + cmd + `"}}]}}` + "\n"
}

// One changed file that could not be appended sent every changed file in the
// pass down the replacement path, which parses each from byte zero: a 1.27 GB
// Claude transcript that grew 281 KB was read whole because two other
// transcripts kept their size (#5324). Decided per file now: the grown one is
// appended from its offset, and only the rewritten one is re-read.
//
// The progress lines name the path each file took: the append path says what
// it updated, the replacement path how many transcripts it re-read.
func TestAMixedBatchAppendsWhatItCanAndReplacesTheRest(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	// Same length, different bytes: a real rewrite, which must still be read.
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the queue warms on boot"))
	bumpMTime(t, same)

	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("the grown transcript was not appended from its offset: %q", got)
	}
	if !strings.Contains(got, replacementPassMarker+" 1 changed transcript\n") &&
		!strings.Contains(got, replacementPassMarker+" 1 changed transcript,") {
		t.Errorf("the replacement path read more than the rewritten file: %q", got)
	}

	for query, want := range map[string]string{
		"billing webhook retries": "s-grown",
		"exporter retries":        "s-grown",
		"queue warms":             "s-same",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
	if ss, _ := Search(dir, search.Options{Query: "cache warms", All: true}); len(ss) != 0 {
		t.Errorf("the rewritten file kept its old text: %#v", ss)
	}
	if ok, _ := UpToDate(dir, ""); !ok {
		t.Error("the index is not current after the pass")
	}
}

// A file whose mtime moved and whose bytes did not has nothing to read. It is
// recorded with its new state, so it neither forces a re-read nor keeps the
// index stale for the next pass (#5324).
func TestATouchedTranscriptIsRecordedNotReread(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot")+
		claudeLine("s-same", "2026-01-02T03:04:06Z", "token ghp_abcdefghijklmnop0123456789QRSTUV"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	before, err := Redactions(dir)
	if err != nil || before.Files[same] == 0 {
		t.Fatalf("control: the planted secret was not counted: %#v %v", before, err)
	}

	bumpMTime(t, same)
	if ok, _ := UpToDate(dir, ""); ok {
		t.Fatal("control: a new mtime should make the index look stale before the pass")
	}
	var progress bytes.Buffer
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, replacementPassMarker) || strings.Contains(got, "updated") {
		t.Errorf("a touched transcript was read again: %q", got)
	}
	if ok, _ := UpToDate(dir, ""); !ok {
		t.Error("the new mtime was not recorded, so every pass sees the file as changed")
	}
	// What the read derived is still the index's: recording the mtime is not
	// a reason to forget the file's redactions.
	if after, redactErr := Redactions(dir); redactErr != nil || after.Files[same] != before.Files[same] {
		t.Errorf("redactions for the touched file: %d, was %d (%v)", after.Files[same], before.Files[same], redactErr)
	}

	// Touched beside a grown file: the pass is a plain append.
	bumpMTime(t, same)
	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	progress.Reset()
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, replacementPassMarker) || !strings.Contains(got, "updated 1 file") {
		t.Errorf("a touched file sent the batch to replacement: %q", got)
	}
	ss, err := Search(dir, search.Options{Query: "cache warms", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s-same" {
		t.Fatalf("the touched file's session went missing: %#v", ss)
	}
}

// A transcript deleted in the same pass is kept, and the kept mark the first
// half writes into the walk must not reach the second half as a file put back.
func TestAMixedBatchKeepsADeletedTranscript(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "p", "grown.jsonl")
	same := filepath.Join(claude, "p", "same.jsonl")
	gone := filepath.Join(claude, "p", "gone.jsonl")
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the cache warms on boot"))
	write(t, gone, claudeLine("s-gone", "2026-01-02T03:04:05Z", "the lockfile drifts nightly"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "the billing webhook retries twice"))
	write(t, same, claudeLine("s-same", "2026-01-02T03:04:05Z", "the queue warms on boot"))
	bumpMTime(t, same)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file") || !strings.Contains(got, "no longer on disk") {
		t.Errorf("progress: %q", got)
	}
	if strings.Contains(got, "skipping") {
		t.Errorf("the second half read the deleted file: %q", got)
	}
	for query, want := range map[string]string{
		"lockfile drifts": "s-gone",
		"billing webhook": "s-grown",
		"queue warms":     "s-same",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Files[gone].Kept {
		t.Errorf("the deleted transcript lost its kept mark: %#v", m.Files[gone])
	}
}

func bumpMTime(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	next := fi.ModTime().Add(3 * time.Second)
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}

// Two transcripts that hold one session are reconciled only when they are
// read together. Split, the grown one was appended and the other replaced on
// its own, and the replacement wrote the grown file's turn a second time
// (#5358 is the multi-pass form). Each case ends where a rebuild would.
func TestAMixedBatchSharingASessionMatchesRebuild(t *testing.T) {
	const t0, t1, t2 = "2026-01-02T03:04:05Z", "2026-01-02T03:04:06Z", "2026-01-02T04:00:00Z"
	cases := []struct {
		name, id string
		setup    func(t *testing.T, a, b, c string)
		change   func(t *testing.T, a, b, c string)
	}{
		{
			name: "grown and rewritten",
			id:   "s-x",
			setup: func(t *testing.T, a, b, _ string) {
				t.Helper()
				write(t, a, claudeLine("s-x", t0, "alpha one"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
			},
			change: func(t *testing.T, a, b, _ string) {
				t.Helper()
				appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta queue"))
				bumpMTime(t, b)
			},
		},
		{
			name: "grown and a torn line finished",
			id:   "s-x",
			setup: func(t *testing.T, a, b, _ string) {
				t.Helper()
				write(t, a, claudeLine("s-x", t0, "alpha one"))
				line := claudeLine("s-x", t1, "beta cache")
				write(t, b, claudeLine("s-x", t0, "alpha one")+line[:len(line)-10])
			},
			change: func(t *testing.T, a, b, _ string) {
				t.Helper()
				appendTo(t, a, claudeLine("s-x", t2, "alpha three"))
				write(t, b, claudeLine("s-x", t0, "alpha one")+claudeLine("s-x", t1, "beta cache"))
				bumpMTime(t, b)
			},
		},
		{
			name: "new copy beside a rewrite",
			id:   "s-b",
			setup: func(t *testing.T, a, b, _ string) {
				t.Helper()
				write(t, a, claudeLine("s-a", t0, "alpha one"))
				write(t, b, claudeLine("s-b", t0, "bravo two"))
			},
			change: func(t *testing.T, _, b, c string) {
				t.Helper()
				write(t, c, claudeLine("s-b", t0, "bravo two")+claudeLine("s-b", t2, "charlie four"))
				write(t, b, claudeLine("s-b", t0, "bravo too"))
				bumpMTime(t, b)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			a := filepath.Join(claude, "p", "a.jsonl")
			b := filepath.Join(claude, "p", "b.jsonl")
			c := filepath.Join(claude, "p", "c.jsonl")
			tc.setup(t, a, b, c)
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			tc.change(t, a, b, c)
			indexPass(t, dir)
			sessionMatchesRebuild(t, dir, "claude", tc.id)
		})
	}
}

// sessionMatchesRebuild is matchesRebuild with the messages compared as a set: the
// order two files' turns interleave in follows the order they are read, which
// neither path fixes.
func sessionMatchesRebuild(t *testing.T, dir, harness, id string) {
	t.Helper()
	fresh := filepath.Join(t.TempDir(), "rebuild.db")
	if err := Ensure(fresh, "", true, nil); err != nil {
		t.Fatal(err)
	}
	sorted := func(v string) string {
		lines := strings.Split(v, "\n")
		slices.Sort(lines)
		return strings.Join(lines, "\n")
	}
	got, want := sessionView(t, dir, harness, id)+rowView(t, dir, harness, id), sessionView(t, fresh, harness, id)+rowView(t, fresh, harness, id)
	if sorted(got) != sorted(want) {
		t.Errorf("%s:%s after incremental passes:\n%s\nafter a rebuild:\n%s", harness, id, got, want)
	}
}

// The control: distinct sessions still split, and still match a rebuild.
func TestAMixedBatchOfDistinctSessionsSplitsAndMatchesRebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	a := filepath.Join(claude, "p", "a.jsonl")
	b := filepath.Join(claude, "p", "b.jsonl")
	write(t, a, claudeLine("s-a", "2026-01-02T03:04:05Z", "alpha one"))
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "beta cache"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	appendTo(t, a, claudeLine("s-a", "2026-01-02T04:00:00Z", "alpha three"))
	write(t, b, claudeLine("s-b", "2026-01-02T03:04:05Z", "beta queue"))
	bumpMTime(t, b)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("distinct sessions no longer split: %q", got)
	}
	sessionMatchesRebuild(t, dir, "claude", "s-a")
	sessionMatchesRebuild(t, dir, "claude", "s-b")
}

// A new transcript beside a torn tail must not make a large, grown transcript
// dead weight in records.bin (#6077).
func TestATornTailAndNewFileStillAppendTheGrownTranscript(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	big := filepath.Join(claude, "p", "big.jsonl")
	torn := filepath.Join(claude, "p", "torn.jsonl")
	var body strings.Builder
	for i := range 200 {
		body.WriteString(claudeLine("s-big", fmt.Sprintf("2026-01-02T03:%02d:%02dZ", i/60, i%60), fmt.Sprintf("turn %d about the exporter retry loop", i)))
	}
	write(t, big, body.String())
	full := claudeLine("s-torn", "2026-01-02T03:04:05Z", "the cache warms on boot")
	write(t, torn, claudeLine("s-torn", "2026-01-02T03:04:00Z", "first turn")+full[:len(full)/2])
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f := before.Files[torn]; f.SafeSize >= f.Size || before.DeadBytes != 0 {
		t.Fatalf("control: torn file = %+v, dead bytes = %d", f, before.DeadBytes)
	}
	var bigOffsets []int64
	tbl := tablesFromManifest(before)
	if err = eachStoredRecord(filepath.Join(dir, "records.bin"), tbl, func(off int64, payload []byte) error {
		r, decErr := decodeRecord(payload, tbl)
		if decErr == nil && r.SourcePath == big {
			bigOffsets = append(bigOffsets, off)
		}
		return decErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(bigOffsets) != 200 {
		t.Fatalf("control: big transcript has %d records, want 200", len(bigOffsets))
	}

	appendTo(t, torn, full[len(full)/2:])
	appendTo(t, big, claudeLine("s-big", "2026-01-02T05:00:00Z", "one new turn on the big session"))
	write(t, filepath.Join(claude, "p", "new.jsonl"), claudeLine("s-new", "2026-01-02T05:00:01Z", "a new session starts"))
	var progress bytes.Buffer
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	got := progress.String()
	if !strings.Contains(got, "updated 1 file (1 new message)") {
		t.Errorf("the 200-turn transcript was not appended: %q", got)
	}
	if !strings.Contains(got, replacementPassMarker+" 2 changed transcripts") {
		t.Errorf("replacement should read only the torn and new files: %q", got)
	}
	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Only the torn file's first turn may be dead; none of big's 200 turns.
	if len(after.DeadRecords) != 1 {
		t.Errorf("dead records = %d, want only the torn file's old turn (dead bytes %d)", len(after.DeadRecords), after.DeadBytes)
	}
	for _, off := range bigOffsets {
		if slices.Contains(after.DeadRecords, off) {
			t.Fatalf("the 200-turn transcript has a dead record at %d", off)
		}
	}
	t.Logf("progress: %q; dead bytes %d -> %d, none from the 200-turn transcript", got, before.DeadBytes, after.DeadBytes)
	sessionMatchesRebuild(t, dir, "claude", "s-big")
	sessionMatchesRebuild(t, dir, "claude", "s-torn")
	sessionMatchesRebuild(t, dir, "claude", "s-new")
}

func TestANewFileSharingAnAppendedSessionKeepsTheBatchWhole(t *testing.T) {
	for _, tc := range []struct {
		name, initial, id string
	}{
		{"existing ID", claudeLine("s-grown", "2026-01-02T03:04:05Z", "alpha one"), "s-grown"},
		// No earlier user/assistant line names this file's session, so the
		// first such line can introduce an ID without preventing an append.
		{"introduced tail ID", "{\"type\":\"summary\",\"summary\":\"waiting for the first prompt\"}\n", "s-tail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			grown := filepath.Join(claude, "p", "grown.jsonl")
			rewritten := filepath.Join(claude, "p", "rewritten.jsonl")
			write(t, grown, tc.initial)
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta cache"))
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			before, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.id == "s-tail" {
				if _, ok := before.Sessions["claude:"+tc.id]; ok {
					t.Fatal("control: tail ID already has a session row")
				}
				if _, ok := before.Empty["claude:"+tc.id]; ok {
					t.Fatal("control: tail ID already has an empty row")
				}
			}
			line := claudeLine(tc.id, "2026-01-02T04:00:00Z", "alpha three")
			appendTo(t, grown, line)
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta queue"))
			bumpMTime(t, rewritten)
			write(t, filepath.Join(claude, "p", "new.jsonl"), line+claudeLine(tc.id, "2026-01-02T05:00:00Z", "charlie four"))
			changed, _, _ := diffWalk(currentFiles(""), before.Files)
			appendable, rest := splitAppendable(changed, before.Files, before.Sessions)
			if _, ok := appendable[grown]; !ok || len(appendable) != 1 || len(rest) != 2 {
				t.Fatalf("control: must reach the overlap guard with one append and two rest files: %v %v", appendable, rest)
			}
			var progress bytes.Buffer
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			got := progress.String()
			if strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
				t.Errorf("overlapping sessions must be replaced together: %q", got)
			}
			t.Logf("overlap guard: 1 append candidate, 2 rest files; progress: %q", got)
			sessionMatchesRebuild(t, dir, "claude", tc.id)
			sessionMatchesRebuild(t, dir, "claude", "s-other")
		})
	}
}

// A known file's torn first line has no row or Copies entry yet. Completing
// it can reveal the same session as an append, just as a new file can.
func TestAKnownFileWithoutARowSharingAnAppendKeepsTheBatchWhole(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "z", "s.jsonl")
	torn := filepath.Join(claude, "a", "s.jsonl")
	const command = "go test ./zzsplit"
	full := `{"type":"assistant","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call1","name":"Bash","input":{"command":"` + command + `"}}]}}` + "\n"
	write(t, grown, full)
	write(t, torn, full[:len(full)/2])
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f, known := before.Files[torn]; !known || f.SafeSize >= f.Size {
		t.Fatalf("control: torn file must be known and incomplete: %+v, known=%t", f, known)
	}
	for _, rows := range []map[string]SessionMeta{before.Sessions, before.Empty} {
		for _, row := range rows {
			if slices.Contains(sessionFiles(row), torn) {
				t.Fatalf("control: a session row already names the torn file: %+v", row)
			}
		}
	}
	if row := before.Sessions["claude:s"]; row.Path != grown || len(row.Copies) != 0 || !row.RanCommand {
		t.Fatalf("control: command must belong to the grown file alone: %+v", row)
	}

	appendTo(t, grown, claudeLine("s", "2026-01-02T04:00:00Z", "alpha three"))
	appendTo(t, torn, full[len(full)/2:])
	// A second session running the command makes it visible in ReadCommands,
	// which lists only commands run in at least two distinct sessions.
	write(t, filepath.Join(claude, "n", "new.jsonl"), strings.Replace(full, `"sessionId":"s"`, `"sessionId":"s-new"`, 1))
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
		t.Errorf("overlapping sessions must be replaced together: %q", got)
	}
	if !commandsHold(dir, command) {
		t.Error("the command disappeared after completing the known torn file")
	}
	sessionMatchesRebuild(t, dir, "claude", "s")
	sessionMatchesRebuild(t, dir, "claude", "s-new")
	sameCommandTables(t, dir)
}

// A row names the known rest file's old session, but its appended lines can
// move the whole file to a session the append half touches.
func TestAKnownFileChangingSessionToAnAppendKeepsTheBatchWhole(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	grown := filepath.Join(claude, "z", "s.jsonl")
	changed := filepath.Join(claude, "a", "old.jsonl")
	const command = "go test ./zzsplit"
	full := `{"type":"assistant","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call1","name":"Bash","input":{"command":"` + command + `"}}]}}` + "\n"
	write(t, grown, full)
	write(t, changed, claudeLine("old", "2026-01-02T03:04:00Z", "first turn"))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if row := before.Sessions["claude:old"]; row.Path != changed || len(row.Copies) != 0 {
		t.Fatalf("control: a row must name the known rest file: %+v", row)
	}
	if row := before.Sessions["claude:s"]; row.Path != grown || len(row.Copies) != 0 || !row.RanCommand {
		t.Fatalf("control: command must belong to the grown file alone: %+v", row)
	}

	appendTo(t, grown, claudeLine("s", "2026-01-02T04:00:00Z", "alpha three"))
	appendTo(t, changed, full)
	// ReadCommands lists commands run in at least two distinct sessions.
	write(t, filepath.Join(claude, "n", "new.jsonl"), strings.Replace(full, `"sessionId":"s"`, `"sessionId":"new"`, 1))
	files, _, _ := diffWalk(currentFiles(""), before.Files)
	appendable, rest := splitAppendable(files, before.Files, before.Sessions)
	if _, ok := appendable[grown]; !ok || len(appendable) != 1 || len(rest) != 2 {
		t.Fatalf("control: must reach the overlap guard with one append and two rest files: %v %v", appendable, rest)
	}
	if _, ok := rest[changed]; !ok {
		t.Fatal("control: the changed session ID must send the known file to replacement")
	}
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
		t.Errorf("overlapping sessions must be replaced together: %q", got)
	}
	if !commandsHold(dir, command) {
		t.Error("the command disappeared after the known file changed session ID")
	}
	for _, u := range ReadCommands(dir) {
		if strings.TrimPrefix(u.Command, "$ ") == command && u.Sessions != 2 {
			t.Errorf("command has %d sessions, want 2", u.Sessions)
		}
	}
	for _, id := range []string{"old", "s", "new"} {
		sessionMatchesRebuild(t, dir, "claude", id)
	}
	sameCommandTables(t, dir)
}

func TestSplitSessionKeysMustBeDisjoint(t *testing.T) {
	s := model.Session{Harness: "codex", ID: "s"}
	for _, tc := range []struct {
		name               string
		rows, empty        map[string]SessionMeta
		appended, replaced []model.Session
		overlap            bool
	}{
		{
			name:     "disjoint old and parsed keys",
			rows:     map[string]SessionMeta{"codex:a": {Path: "a"}, "codex:b": {Path: "b"}},
			appended: []model.Session{s}, replaced: []model.Session{{Harness: "codex", ID: "t"}},
		},
		{
			name:    "old row names both halves",
			rows:    map[string]SessionMeta{"codex:s": {Path: "a", Copies: []string{"b"}}},
			overlap: true,
		},
		{
			name:     "old append row and parsed rest key",
			rows:     map[string]SessionMeta{"codex:s": {Path: "a"}},
			replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "parsed append key and old rest row",
			rows:     map[string]SessionMeta{"codex:s": {Path: "b"}},
			appended: []model.Session{s}, overlap: true,
		},
		{
			name:     "parsed keys in both halves",
			appended: []model.Session{s}, replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "empty append row and parsed rest key",
			empty:    map[string]SessionMeta{"codex:s": {Path: "a"}},
			replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "parsed append key and empty rest row",
			empty:    map[string]SessionMeta{"codex:s": {Path: "b"}},
			appended: []model.Session{s}, overlap: true,
		},
		{
			name:     "append copy beside a kept owner",
			rows:     map[string]SessionMeta{"codex:s": {Path: "kept", Copies: []string{"a"}}},
			replaced: []model.Session{s}, overlap: true,
		},
		{
			name:     "rest copy beside a kept owner",
			empty:    map[string]SessionMeta{"codex:s": {Path: "kept", Copies: []string{"b"}}},
			appended: []model.Session{s}, overlap: true,
		},
		{
			name:     "same ID in different harnesses",
			rows:     map[string]SessionMeta{"claude:s": {Path: "b"}},
			appended: []model.Session{s},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := Manifest{Sessions: tc.rows, Empty: tc.empty}
			ready := &splitReads{
				appendFiles: map[string]splitRead{"a": {sessions: tc.appended}},
				restFiles:   map[string]splitRead{"b": {sessions: tc.replaced}},
			}
			if got := splitOverlaps(old, map[string]FileState{"a": {}}, map[string]FileState{"b": {}}, ready); got != tc.overlap {
				t.Errorf("overlap = %t, want %t", got, tc.overlap)
			}
		})
	}
}

// A rewritten history can lose a session that an appended rollout takes over.
// Its old row must keep the two files in the same replacement pass.
func TestACodexHistoryLosingAnAppendedSessionKeepsTheBatchWhole(t *testing.T) {
	tmp := parityEnv(t, map[string]string{"DEJA_CODEX_ROOT": "codex", "DEJA_XCODE_CODEX_ROOT": "xcode-codex"})
	t.Setenv("CODEX_HOME", "")
	const s = "019a0000-0000-7000-8000-000000000001"
	const other = "019a0000-0000-7000-8000-000000000002"
	const fresh = "019a0000-0000-7000-8000-000000000003"
	const prompt = "why does the exporter retry loop stall"
	at := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	history := filepath.Join(tmp, "codex", "history.jsonl")
	rollout := filepath.Join(tmp, "xcode-codex", "sessions", "rollout-"+s+".jsonl")
	meta := func(id string) string {
		return codexLine(at, "session_meta", map[string]any{"id": id, "cwd": "/w/app"})
	}
	message := codexLine(at, "response_item", map[string]any{"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": prompt}}})
	otherLine := codexHistoryLine(other, at, "why does the cache warm on boot")
	write(t, history, codexHistoryLine(s, at, prompt)+otherLine)
	write(t, rollout, meta(s))
	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if row := before.Sessions["codex:"+s]; row.Path != history || len(row.Copies) != 0 {
		t.Fatalf("control: S must belong to history alone: %+v", row)
	}
	if _, known := before.Files[rollout]; !known {
		t.Fatal("control: the metadata-only rollout must already be known")
	}
	write(t, history, otherLine)
	appendTo(t, rollout, message)
	write(t, filepath.Join(tmp, "xcode-codex", "sessions", "rollout-"+fresh+".jsonl"), meta(fresh)+message)
	changed, _, _ := diffWalk(currentFiles(""), before.Files)
	appendable, rest := splitAppendable(changed, before.Files, before.Sessions)
	if _, ok := appendable[rollout]; !ok || len(appendable) != 1 || len(rest) != 2 {
		t.Fatalf("control: must reach the overlap guard with one append and two rest files: %v %v", appendable, rest)
	}
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); strings.Contains(got, "updated") || !strings.Contains(got, replacementPassMarker+" 3 changed transcripts") {
		t.Errorf("history and its rollout must be replaced together: %q", got)
	}
	for _, id := range []string{s, other, fresh} {
		sessionMatchesRebuild(t, dir, "codex", id)
	}
}

// Early reads must carry their diagnostics to the half that records the file,
// without losing or doubling counts when append writes its manifest first.
func TestASplitWithANewFileKeepsItsReadAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		whole    bool
	}{
		{"distinct session", "s-new", false},
		{"shares appended session", "s-grown", true},
		{"shares replaced session", "s-other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticIndexEnv(t)
			claude := os.Getenv("DEJA_CLAUDE_ROOT")
			grown := filepath.Join(claude, "p", "grown.jsonl")
			rewritten := filepath.Join(claude, "p", "rewritten.jsonl")
			fresh := filepath.Join(claude, "p", "new.jsonl")
			write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "alpha one"))
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta cache"))
			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)
			appendTo(t, grown, "{bad tail}\n"+claudeLine("s-grown", "2026-01-02T04:00:00Z", "alpha three"))
			write(t, rewritten, claudeLine("s-other", "2026-01-02T03:04:05Z", "beta queue"))
			bumpMTime(t, rewritten)
			write(t, fresh, "{bad new}\n"+claudeLine(tc.id, "2026-01-02T05:00:00Z", "token ghp_abcdefghijklmnop0123456789QRSTUV"))
			var progress bytes.Buffer
			if err := Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			if appended := strings.Contains(progress.String(), "updated 1 file"); appended == tc.whole {
				t.Errorf("whole replacement = %t, progress: %q", tc.whole, progress.String())
			}
			m, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{fresh, grown} {
				if got := m.IngestFiles[p].Malformed; got != 1 {
					t.Errorf("%s malformed lines = %d, want 1", filepath.Base(p), got)
				}
			}
			if got := m.Files[fresh].Redactions; got != 1 {
				t.Errorf("new file redactions = %d, want 1", got)
			}
			if ok, _ := UpToDate(dir, ""); !ok {
				t.Error("early read's file state was not recorded")
			}
			sessionMatchesRebuild(t, dir, "claude", tc.id)
		})
	}
}

func makePaddedLines(n int, lineFn func(i int, pad string) string) string {
	const padLen = 16 << 10
	pad := strings.Repeat("p", padLen)
	var b strings.Builder
	b.Grow(n * (padLen + 160))
	for i := range n {
		b.WriteString(lineFn(i, pad))
	}
	return b.String()
}

func corruptMidSameLen(mid string) string {
	const prefix = "corrupt_middle_never_read_"
	if len(mid) <= len(prefix)+1 {
		panic("mid too short")
	}
	return prefix + strings.Repeat("x", len(mid)-len(prefix)-1) + "\n"
}

// When an appendable file in a split batch is deferred during appendPass (for
// example codex history waiting on its matching rollout), Pass 2 must leave it
// at its pre-pass state instead of falling back to a full offset-0 re-read.
func TestSplitBatchDefersChangedAppendableWithoutFullRead(t *testing.T) {
	tmp := parityEnv(t, map[string]string{
		"DEJA_CLAUDE_ROOT": "claude",
		"DEJA_CODEX_ROOT":  "codex",
	})
	t.Setenv("CODEX_HOME", "")

	// withoutDeferred must also drop unknown deferred files from Pass 2.
	unknownPath := filepath.Join(tmp, "unknown.jsonl")
	knownPath := filepath.Join(tmp, "known.jsonl")
	fUnknown := FileState{Size: 10}
	fKnownOld := FileState{Size: 20}
	fKnownNew := FileState{Size: 40}
	filtered := withoutDeferred(
		map[string]FileState{unknownPath: fUnknown, knownPath: fKnownNew},
		map[string]splitRead{unknownPath: {deferred: true}, knownPath: {deferred: true}},
		map[string]FileState{knownPath: fKnownOld},
	)
	if _, exists := filtered[unknownPath]; exists {
		t.Fatalf("withoutDeferred kept unknown deferred file: %v", filtered)
	}
	if got := filtered[knownPath]; got != fKnownOld {
		t.Fatalf("withoutDeferred known = %+v, want %+v", got, fKnownOld)
	}

	const oldID = "019a0000-0000-7000-8000-000000000101"
	const pendingID = "019a0000-0000-7000-8000-000000000102"
	oldTime := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	history := filepath.Join(tmp, "codex", "history.jsonl")
	grown := filepath.Join(tmp, "claude", "p", "grown.jsonl")
	rewritten := filepath.Join(tmp, "claude", "p", "rewritten.jsonl")

	// Exceed 2 * 512 KiB so head and tail prefixSample windows stay identical
	// when the middle lines are corrupted; any offset-0 re-read would hit them.
	makeHistPad := func(i int, pad string) string {
		return codexHistoryLine(oldID, oldTime.Add(time.Duration(i)*time.Second), "hist head "+pad)
	}
	histHead := makePaddedLines(36, makeHistPad)
	histMid := makePaddedLines(5, makeHistPad)
	histTail := makePaddedLines(36, makeHistPad)

	write(t, history, histHead+histMid+histTail)
	write(t, grown, claudeLine("s-grown", "2026-01-02T03:04:05Z", "grown alpha"))
	write(t, rewritten, claudeLine("s-rewritten", "2026-01-02T03:04:05Z", "rewrite before"))

	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := before.Files[history]; !ok || before.Files[history].SafeSize == 0 {
		t.Fatalf("history must be indexed with SafeSize > 0: %+v", before.Files[history])
	}

	// Poison the middle (outside both prefixSample windows) and append a fresh
	// history entry with no rollout yet so readAppended defers history.
	pendingPrompt := "pending rollout delta prompt"
	pendingLine := codexHistoryLine(pendingID, time.Now().UTC(), pendingPrompt)
	write(t, history, histHead+corruptMidSameLen(histMid)+histTail+pendingLine)
	appendTo(t, grown, claudeLine("s-grown", "2026-01-02T04:00:00Z", "grown omega"))
	write(t, rewritten, claudeLine("s-rewritten", "2026-01-02T03:04:05Z", "rewrite after"))
	bumpMTime(t, rewritten)

	var progress bytes.Buffer
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	gotProgress := progress.String()
	if !strings.Contains(gotProgress, "updated 1 file") {
		t.Errorf("expected grown to append cleanly in pass 1, progress=%q", gotProgress)
	}
	wantReplace := replacementPassMarker + " 1 changed transcript,"
	if !strings.Contains(gotProgress, wantReplace) {
		t.Errorf("expected pass 2 to replace only rewritten (%q), progress=%q", wantReplace, gotProgress)
	}

	after, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after.Files[history] != before.Files[history] {
		t.Errorf("deferred history state changed: got=%+v want=%+v", after.Files[history], before.Files[history])
	}
	if mal := after.IngestFiles[history].Malformed; mal != 0 {
		t.Errorf("deferred history malformed = %d (full re-read hit poisoned middle), want 0", mal)
	}

	// Once the matching rollout appears, a later incremental pass picks up the
	// deferred append from SafeSize without re-reading the poisoned middle.
	rollout := filepath.Join(tmp, "codex", "sessions", "rollout-"+pendingID+".jsonl")
	write(t, rollout,
		codexLine(time.Now().UTC(), "session_meta", map[string]any{"id": pendingID, "cwd": "/w/app"})+
			codexLine(time.Now().UTC(), "response_item", map[string]any{
				"type":    "message",
				"role":    "user",
				"content": []any{map[string]any{"type": "input_text", "text": pendingPrompt}},
			}),
	)
	indexPass(t, dir)
	finalManifest, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mal := finalManifest.IngestFiles[history].Malformed; mal != 0 {
		t.Errorf("post-recovery history malformed = %d (re-read middle), want 0", mal)
	}
	if finalManifest.Files[history].SafeSize <= before.Files[history].SafeSize {
		t.Errorf("post-recovery history SafeSize did not advance: before=%d after=%d",
			before.Files[history].SafeSize, finalManifest.Files[history].SafeSize)
	}
	if _, ok := finalManifest.Sessions["codex:"+pendingID]; !ok {
		t.Errorf("deferred session %q was not indexed once rollout appeared", pendingID)
	}
	if gotView := sessionView(t, dir, "codex", pendingID); !strings.Contains(gotView, pendingPrompt) {
		t.Errorf("recovered session view missing pending prompt %q: %s", pendingPrompt, gotView)
	}
}

// Across offset-resumable harnesses, both pure incremental append and split-batch
// append must never re-read bytes before SafeSize (proven by poisoning bytes
// strictly between the head and tail prefixSample windows and asserting
// Malformed stays zero while appended messages are indexed).
func TestIncrementalAndSplitReadByteBudgetAcrossHarnesses(t *testing.T) {
	for _, splitMode := range []bool{false, true} {
		name := "pure_append"
		if splitMode {
			name = "split_batch"
		}
		t.Run(name, func(t *testing.T) {
			tmp := parityEnv(t, map[string]string{
				"DEJA_CLAUDE_ROOT": "claude",
				"DEJA_CODEX_ROOT":  "codex",
				"DEJA_QWEN_ROOT":   "qwen",
			})
			t.Setenv("CODEX_HOME", "")

			baseTime := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)

			type harnessFile struct {
				path         string
				prefix, mid  string
				tail, delta  string
				deltaToken   string
				harness, sid string
			}

			makeClaude := func() harnessFile {
				p := filepath.Join(tmp, "claude", "p", "budget-claude.jsonl")
				line := func(_ int, pad string) string {
					return claudeLine("s-claude-budget", "2026-06-02T10:00:05Z", "claudemsg "+pad)
				}
				head := makePaddedLines(36, line)
				mid := makePaddedLines(5, line)
				tail := makePaddedLines(36, line)
				tok := "claude_delta_sentinel_budget"
				return harnessFile{
					path:       p,
					prefix:     head,
					mid:        mid,
					tail:       tail,
					delta:      claudeLine("s-claude-budget", "2026-06-02T10:05:00Z", tok),
					deltaToken: tok,
					harness:    "claude",
					sid:        "s-claude-budget",
				}
			}

			makeCodexRollout := func() harnessFile {
				const sid = "019a0000-0000-7000-8000-000000000201"
				p := filepath.Join(tmp, "codex", "sessions", "rollout-"+sid+".jsonl")
				meta := codexLine(baseTime, "session_meta", map[string]any{"id": sid, "cwd": "/w/app"})
				line := func(i int, pad string) string {
					return codexLine(baseTime.Add(time.Duration(i)*time.Second), "response_item", map[string]any{
						"type":    "message",
						"role":    "user",
						"content": []any{map[string]any{"type": "input_text", "text": "codexmsg " + pad}},
					})
				}
				head := meta + makePaddedLines(36, line)
				mid := makePaddedLines(5, line)
				tail := makePaddedLines(36, line)
				tok := "codex_delta_sentinel_budget"
				return harnessFile{
					path:   p,
					prefix: head,
					mid:    mid,
					tail:   tail,
					delta: codexLine(baseTime.Add(time.Hour), "response_item", map[string]any{
						"type":    "message",
						"role":    "user",
						"content": []any{map[string]any{"type": "input_text", "text": tok}},
					}),
					deltaToken: tok,
					harness:    "codex",
					sid:        sid,
				}
			}

			makeQwen := func() harnessFile {
				p := filepath.Join(tmp, "qwen", "projects", "-tmp-qwen", "chats", "session.jsonl")
				line := func(_ int, pad string) string {
					return `{"type":"user","sessionId":"s-qwen-budget","timestamp":"2026-06-02T10:00:05Z","message":{"role":"user","parts":[{"text":"qwenmsg ` + pad + `"}]}}` + "\n"
				}
				head := makePaddedLines(36, line)
				mid := makePaddedLines(5, line)
				tail := makePaddedLines(36, line)
				tok := "qwen_delta_sentinel_budget"
				return harnessFile{
					path:       p,
					prefix:     head,
					mid:        mid,
					tail:       tail,
					delta:      `{"type":"user","sessionId":"s-qwen-budget","timestamp":"2026-06-02T10:05:00Z","message":{"role":"user","parts":[{"text":"` + tok + `"}]}}` + "\n",
					deltaToken: tok,
					harness:    "qwen",
					sid:        "s-qwen-budget",
				}
			}

			files := []harnessFile{makeClaude(), makeCodexRollout(), makeQwen()}
			for _, hf := range files {
				write(t, hf.path, hf.prefix+hf.mid+hf.tail)
			}
			rewritten := filepath.Join(tmp, "claude", "p", "other-rewritten.jsonl")
			if splitMode {
				write(t, rewritten, claudeLine("s-split-other", "2026-06-02T09:00:00Z", "split initial"))
			}

			dir := filepath.Join(tmp, "idx")
			indexPass(t, dir)

			before, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, hf := range files {
				if before.Files[hf.path].SafeSize != int64(len(hf.prefix)+len(hf.mid)+len(hf.tail)) {
					t.Fatalf("%s initial SafeSize = %d, want %d", hf.harness, before.Files[hf.path].SafeSize, len(hf.prefix)+len(hf.mid)+len(hf.tail))
				}
			}

			for _, hf := range files {
				write(t, hf.path, hf.prefix+corruptMidSameLen(hf.mid)+hf.tail+hf.delta)
			}
			if splitMode {
				write(t, rewritten, claudeLine("s-split-other", "2026-06-02T09:00:00Z", "split rewritten"))
				bumpMTime(t, rewritten)
			}

			var progress bytes.Buffer
			if err = Ensure(dir, "", false, &progress); err != nil {
				t.Fatal(err)
			}
			gotProgress := progress.String()
			if !strings.Contains(gotProgress, "updated 3 files") {
				t.Errorf("expected 3 appended files, progress=%q", gotProgress)
			}
			if splitMode {
				wantReplace := replacementPassMarker + " 1 changed transcript,"
				if !strings.Contains(gotProgress, wantReplace) {
					t.Errorf("split pass should replace only rewritten (%q), progress=%q", wantReplace, gotProgress)
				}
			} else if strings.Contains(gotProgress, replacementPassMarker) {
				t.Errorf("pure append unexpectedly triggered replacement pass: %q", gotProgress)
			}

			after, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, hf := range files {
				if mal := after.IngestFiles[hf.path].Malformed; mal != 0 {
					t.Errorf("%s malformed = %d (read before SafeSize!), want 0", hf.harness, mal)
				}
				wantSize := int64(len(hf.prefix) + len(hf.mid) + len(hf.tail) + len(hf.delta))
				if after.Files[hf.path].SafeSize != wantSize {
					t.Errorf("%s SafeSize = %d, want %d", hf.harness, after.Files[hf.path].SafeSize, wantSize)
				}
				if _, ok := after.Sessions[hf.harness+":"+hf.sid]; !ok {
					t.Errorf("%s session %q missing after append", hf.harness, hf.sid)
				}
				if gotView := sessionView(t, dir, hf.harness, hf.sid); !strings.Contains(gotView, hf.deltaToken) {
					t.Errorf("%s session view missing appended delta %q", hf.harness, hf.deltaToken)
				}
			}
		})
	}
}

// An incremental pass appends to records.bin under the lock and commits the
// manifest after. A reader in that gap holds a complete snapshot, and calling
// it damaged turned an MCP recall away as "indexing" 17 times in 60 (#4266).
// A shorter log is still damage, lock or no lock.
func TestAnAppendInFlightIsNotDamage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "the vorpelsnark retry budget"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := DamageReason(dir); got != "" {
		t.Fatalf("the fixture is wrong — a fresh store is damaged: %q", got)
	}

	// The pass: lock held, records appended, manifest not yet rewritten.
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unlock() }()
	rp := filepath.Join(dir, "records.bin")
	f, err := os.OpenFile(rp, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("records of a session the manifest has not committed yet"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}

	if got := DamageReason(dir); got != "" {
		t.Errorf("an append in flight was called damage: %q", got)
	}
	res, err := SearchDetailed(dir, query.Options{Query: "vorpelsnark"})
	if err != nil || len(res.Sessions) == 0 {
		t.Errorf("the committed snapshot did not answer during the append: %d sessions, %v", len(res.Sessions), err)
	}

	// Truncated below what the manifest committed: damage, with the pass
	// running and without it.
	if err := os.Truncate(rp, 10); err != nil {
		t.Fatal(err)
	}
	if !Damaged(dir) {
		t.Error("a truncated record log under a running pass was not reported")
	}
	unlock()
	unlock = func() {}
	if !Damaged(dir) {
		t.Error("a truncated record log was not reported")
	}
}
