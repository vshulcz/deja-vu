package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/query"
)

func claudeNoCWDMessages(id, ts, role, content string) string {
	return fmt.Sprintf(`{"type":%q,"sessionId":%q,"timestamp":%q,"message":{"role":%q,"content":%s}}`, role, id, ts, role, content) + "\n"
}

func TestDirectoryRenamePairsInLinearTimeWithZeroPrefixReads(t *testing.T) {
	t.Run("batch_dir_rename_250_files_zero_prefix_reads", func(t *testing.T) {
		tmp := hermeticIndexEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		projA := filepath.Join(claudeRoot, "-Users-alex-dirA")
		projB := filepath.Join(claudeRoot, "-Users-alex-dirB")
		if err := os.MkdirAll(projA, 0o755); err != nil {
			t.Fatal(err)
		}

		const total = 250
		for i := range total {
			id := fmt.Sprintf("batch-%03d", i)
			pad := strings.Repeat("token ", i+1)
			line := claudeNoCWDMessages(id, "2026-03-01T10:00:00Z", "user", fmt.Sprintf("%q", "batchdir "+id+" "+pad))
			write(t, filepath.Join(projA, id+".jsonl"), line)
		}

		dir := filepath.Join(tmp, "idx")
		indexPass(t, dir)
		before := recordsSize(t, dir)

		if err := os.Rename(projA, projB); err != nil {
			t.Fatal(err)
		}

		renamePrefixReads.Store(0)
		indexPass(t, dir)

		if reads := renamePrefixReads.Load(); reads != 0 {
			t.Fatalf("renamePrefixReads = %d, want 0 for same-size batch rename", reads)
		}
		if files := lastIngestFiles.Load(); files != 0 {
			t.Fatalf("lastIngestFiles = %d, want 0 for pure rename", files)
		}
		if after := recordsSize(t, dir); after != before {
			t.Fatalf("records.bin grew from %d to %d bytes on pure directory rename", before, after)
		}
		sameAsRebuild(t, dir)
	})

	t.Run("duplicate_sizes_and_same_beats_grown", func(t *testing.T) {
		tmp := hermeticIndexEnv(t)
		claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
		proj := filepath.Join(claudeRoot, "-Users-alex-ties")
		dest := filepath.Join(claudeRoot, "-Users-alex-movedties")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}

		// Two files with equal byte length and distinct contents.
		u1 := claudeNoCWDMessages("tie-u1", "2026-03-02T10:00:00Z", "user", `"alpha quorum token 001"`)
		u2 := claudeNoCWDMessages("tie-u2", "2026-03-02T10:00:00Z", "user", `"bravo leases token 002"`)
		if len(u1) != len(u2) {
			t.Fatalf("test fixture bug: len(u1)=%d != len(u2)=%d", len(u1), len(u2))
		}
		write(t, filepath.Join(proj, "u1.jsonl"), u1)
		write(t, filepath.Join(proj, "u2.jsonl"), u2)

		// One same-size candidate and one candidate whose prefix matches u1 but grows,
		// sharing u1's original SafeSize vs another file. Here we create c1 and c2:
		// c1 stays identical size; d1 is deleted & replaced by an appended copy of c1's bytes?
		// Wait: test same-size match (s1 -> m1, unchanged bytes) competing alongside a grown
		// candidate (g1 -> g1-moved + extra turn) with distinct lengths.
		sameBody := claudeNoCWDMessages("same-1", "2026-03-02T11:00:00Z", "user", `"unchanged candidate payload"`)
		grownOrig := claudeNoCWDMessages("grow-1", "2026-03-02T12:00:00Z", "user", `"growing candidate payload"`)
		write(t, filepath.Join(proj, "s1.jsonl"), sameBody)
		write(t, filepath.Join(proj, "g1.jsonl"), grownOrig)

		// Two files with identical bytes (deterministic lexicographical tie-break).
		dupBody := claudeNoCWDMessages("dup-xy", "2026-03-02T13:00:00Z", "user", `"duplicate content deterministically matched"`)
		write(t, filepath.Join(proj, "dup-a.jsonl"), dupBody)
		write(t, filepath.Join(proj, "dup-b.jsonl"), dupBody)

		dir := filepath.Join(tmp, "idx")
		indexPass(t, dir)

		for _, mv := range [][2]string{
			{"u1.jsonl", "v1.jsonl"},
			{"u2.jsonl", "v2.jsonl"},
			{"s1.jsonl", "s1-moved.jsonl"},
			{"g1.jsonl", "g1-moved.jsonl"},
			{"dup-a.jsonl", "dup-x.jsonl"},
			{"dup-b.jsonl", "dup-y.jsonl"},
		} {
			if err := os.Rename(filepath.Join(proj, mv[0]), filepath.Join(dest, mv[1])); err != nil {
				t.Fatal(err)
			}
		}
		appendTo(t, filepath.Join(dest, "g1-moved.jsonl"),
			claudeNoCWDMessages("grow-1", "2026-03-02T12:05:00Z", "assistant", `"appended tail after move"`))
		bumpMTime(t, filepath.Join(dest, "g1-moved.jsonl"))

		renamePrefixReads.Store(0)
		indexPass(t, dir)
		if reads := renamePrefixReads.Load(); reads > 1 {
			t.Fatalf("renamePrefixReads = %d, want at most 1 (only the single grown candidate)", reads)
		}
		sameAsRebuild(t, dir)
	})
}

func TestRenamedJSONTranscriptUpdatesInPlaceAndAcrossProjects(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	geminiRoot := filepath.Join(tmp, "gemini")
	t.Setenv("DEJA_GEMINI_ROOT", geminiRoot)

	chatsA := filepath.Join(geminiRoot, "tmp", "proj-alpha", "chats")
	if err := os.MkdirAll(chatsA, 0o755); err != nil {
		t.Fatal(err)
	}

	g1JSON := `{"sessionId":"gem-json-1","startTime":"2026-03-05T10:00:00.000Z","lastUpdated":"2026-03-05T10:01:00.000Z","messages":[{"id":"m1","timestamp":"2026-03-05T10:00:01.000Z","type":"user","content":"gemini json renameneedle in place"}]}`
	g2JSON := `{"sessionId":"gem-json-2","startTime":"2026-03-05T11:00:00.000Z","lastUpdated":"2026-03-05T11:01:00.000Z","messages":[{"id":"m2","timestamp":"2026-03-05T11:00:01.000Z","type":"user","content":"gemini json project move token"}]}`
	p1 := filepath.Join(chatsA, "session-gem-1.json")
	p2 := filepath.Join(chatsA, "session-gem-2.json")
	write(t, p1, g1JSON)
	write(t, p2, g2JSON)

	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before := recordsSize(t, dir)

	// Rename one .json inside chats/, then move the whole project folder proj-alpha -> proj-beta.
	p1Moved := filepath.Join(chatsA, "session-gem-1-moved.json")
	if err := os.Rename(p1, p1Moved); err != nil {
		t.Fatal(err)
	}
	renamePrefixReads.Store(0)
	indexPass(t, dir)
	if files := lastIngestFiles.Load(); files != 0 {
		t.Fatalf("same-dir .json rename: lastIngestFiles = %d, want 0", files)
	}
	if after := recordsSize(t, dir); after != before {
		t.Fatalf("same-dir .json rename: records.bin grew from %d to %d", before, after)
	}
	if reads := renamePrefixReads.Load(); reads != 0 {
		t.Fatalf("same-dir .json rename: renamePrefixReads = %d, want 0", reads)
	}

	projBeta := filepath.Join(geminiRoot, "tmp", "proj-beta")
	if err := os.Rename(filepath.Join(geminiRoot, "tmp", "proj-alpha"), projBeta); err != nil {
		t.Fatal(err)
	}
	renamePrefixReads.Store(0)
	indexPass(t, dir)
	if files := lastIngestFiles.Load(); files != 0 {
		t.Fatalf("cross-project .json dir move: lastIngestFiles = %d, want 0", files)
	}
	if after := recordsSize(t, dir); after != before {
		t.Fatalf("cross-project .json dir move: records.bin grew from %d to %d", before, after)
	}
	if reads := renamePrefixReads.Load(); reads != 0 {
		t.Fatalf("cross-project .json dir move: renamePrefixReads = %d, want 0", reads)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := m.Files[p1]; ok && f.Kept {
		t.Fatalf("ghost Kept row remained for old path %s", p1)
	}
	if f, ok := m.Files[p2]; ok && f.Kept {
		t.Fatalf("ghost Kept row remained for old path %s", p2)
	}
	if meta := m.Sessions["gemini:gem-json-2"]; meta.Project != "proj-beta" {
		t.Fatalf("gemini:gem-json-2 project = %q, want %q", meta.Project, "proj-beta")
	}
	sameAsRebuild(t, dir)
}

func TestRenamedFilenameKeyedTranscriptMatchesColdRebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	memDirA := filepath.Join(claudeRoot, "-Users-alex-alpha", "memory")
	memDirB := filepath.Join(claudeRoot, "-Users-alex-beta", "memory")
	if err := os.MkdirAll(memDirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memDirB, 0o755); err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(memDirA, "architecture.md")
	newPath := filepath.Join(memDirB, "decisions.md")
	body := "# Architecture\n\n- decision: validate fencing tokens on every write lease\n- constraint: reject stale generation epochs\n"
	write(t, oldPath, body)

	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)

	beforeMetas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeMetas) == 0 {
		t.Fatal("expected claude auto-memory note to be indexed before rename")
	}
	oldID := beforeMetas[0].ID

	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)

	afterMetas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range afterMetas {
		if m.ID == oldID {
			t.Fatalf("stale path-derived id %q survived rename", oldID)
		}
	}
	sameAsRebuild(t, dir)
}

func TestCrossDirectoryRenameSidecarsZeroRecordReads(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	projA := filepath.Join(claudeRoot, "-Users-alex-projalpha")
	projB := filepath.Join(claudeRoot, "-Users-alex-projbeta")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionWithWork := func(id, t0, t1, t2, t3, t4, t5, t6, errSym string) string {
		mod := strings.ToLower(errSym)
		return claudeNoCWDMessages(id, t0, "user", `"check build failure and recurring command"`) +
			claudeNoCWDMessages(id, t1, "assistant", `[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"make testcheck"}}]`) +
			claudeNoCWDMessages(id, t2, "user", `[{"type":"tool_result","tool_use_id":"c1","content":"ok"}]`) +
			claudeNoCWDMessages(id, t3, "assistant", `[{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"python3 app.py"}}]`) +
			claudeNoCWDMessages(id, t4, "user", fmt.Sprintf(`[{"type":"tool_result","tool_use_id":"c2","is_error":true,"content":"ModuleNotFoundError: No module named '%s'\nexit status 1"}]`, mod)) +
			claudeNoCWDMessages(id, t5, "assistant", fmt.Sprintf(`[{"type":"tool_use","id":"c3","name":"Bash","input":{"command":"pip install %s"}}]`, mod)) +
			claudeNoCWDMessages(id, t6, "user", fmt.Sprintf(`[{"type":"tool_result","tool_use_id":"c3","content":"Successfully installed %s"}]`, mod))
	}

	write(t, filepath.Join(projA, "s1.jsonl"), sessionWithWork("side-s1",
		"2026-03-10T10:00:00Z", "2026-03-10T10:01:00Z", "2026-03-10T10:02:00Z",
		"2026-03-10T10:03:00Z", "2026-03-10T10:04:00Z", "2026-03-10T10:05:00Z", "2026-03-10T10:06:00Z", "brokenQuorumToken"))
	write(t, filepath.Join(projA, "s2.jsonl"), sessionWithWork("side-s2",
		"2026-03-10T11:00:00Z", "2026-03-10T11:01:00Z", "2026-03-10T11:02:00Z",
		"2026-03-10T11:03:00Z", "2026-03-10T11:04:00Z", "2026-03-10T11:05:00Z", "2026-03-10T11:06:00Z", "brokenQuorumToken"))

	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)

	if len(ReadCommands(dir)) == 0 {
		t.Fatal("precondition: expected non-empty ReadCommands before rename")
	}
	if len(ReadCommandFails(dir)) == 0 {
		t.Fatal("precondition: expected non-empty ReadCommandFails before rename")
	}
	if len(ReadFixes(dir)) == 0 {
		t.Fatal("precondition: expected non-empty ReadFixes before rename")
	}
	if len(ReadSessionFacts(dir)) == 0 {
		t.Fatal("precondition: expected non-empty ReadSessionFacts before rename")
	}

	// Remove recoffsets.bin to prove sidecar reprojection reads zero stored records without it.
	_ = os.Remove(filepath.Join(dir, "recoffsets.bin"))

	if err := os.Rename(projA, projB); err != nil {
		t.Fatal(err)
	}

	commandRecordsMined.Store(0)
	seenBefore := atomic.LoadInt64(&commandFailRecordsSeen)
	storedRecordsWalked.Store(0)
	renamePrefixReads.Store(0)

	indexPass(t, dir)

	if mined := commandRecordsMined.Load(); mined != 0 {
		t.Fatalf("commandRecordsMined = %d, want 0 on pure cross-directory rename", mined)
	}
	if seen := atomic.LoadInt64(&commandFailRecordsSeen) - seenBefore; seen != 0 {
		t.Fatalf("commandFailRecordsSeen = %d, want 0 on pure cross-directory rename", seen)
	}
	if walked := storedRecordsWalked.Load(); walked != 0 {
		t.Fatalf("storedRecordsWalked = %d, want 0 on pure cross-directory rename", walked)
	}
	if reads := renamePrefixReads.Load(); reads != 0 {
		t.Fatalf("renamePrefixReads = %d, want 0 on pure cross-directory rename", reads)
	}

	for _, cmd := range ReadCommands(dir) {
		if len(cmd.ByProject) == 0 {
			t.Fatalf("expected non-empty ByProject for %q", cmd.Command)
		}
		for proj := range cmd.ByProject {
			if proj != "alex/projbeta" {
				t.Fatalf("command project = %q, want alex/projbeta", proj)
			}
		}
	}
	for _, f := range ReadCommandFails(dir) {
		if f.Project != "alex/projbeta" {
			t.Fatalf("command fail project = %q, want alex/projbeta", f.Project)
		}
	}
	for _, fx := range ReadFixes(dir) {
		if fx.Project != "alex/projbeta" {
			t.Fatalf("fix pair project = %q, want alex/projbeta", fx.Project)
		}
	}

	hits, err := Search(dir, query.Options{Query: "brokenQuorumToken", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Project != "alex/projbeta" {
		t.Fatalf("search hits = %+v, want project alex/projbeta", hits)
	}

	sameAsRebuild(t, dir)
	fresh := dir + "-fresh"
	gotFixes := normalizeFixPairsUTC(ReadFixes(dir))
	wantFixes := normalizeFixPairsUTC(ReadFixes(fresh))
	slices.SortFunc(gotFixes, func(a, b FixPair) int { return strings.Compare(a.Error+a.Command, b.Error+b.Command) })
	slices.SortFunc(wantFixes, func(a, b FixPair) int { return strings.Compare(a.Error+a.Command, b.Error+b.Command) })
	if !reflect.DeepEqual(gotFixes, wantFixes) {
		t.Fatalf("ReadFixes mismatch vs cold rebuild:\n got: %+v\nwant: %+v", gotFixes, wantFixes)
	}
}

func TestDirectoryRenameWithConcurrentAppendAndScale(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	dir1 := filepath.Join(claudeRoot, "-Users-alex-scale1")
	dir2 := filepath.Join(claudeRoot, "-Users-alex-scale2")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}

	const n = 50
	for i := range n {
		id := fmt.Sprintf("scale-%02d", i)
		write(t, filepath.Join(dir1, id+".jsonl"),
			claudeNoCWDMessages(id, "2026-03-11T09:00:00Z", "user", fmt.Sprintf("%q", "scale session "+id+" "+strings.Repeat("x", i+1))))
	}

	dir := filepath.Join(tmp, "idx")
	indexPass(t, dir)
	before := recordsSize(t, dir)

	if err := os.Rename(dir1, dir2); err != nil {
		t.Fatal(err)
	}
	appendedPath := filepath.Join(dir2, "scale-00.jsonl")
	appendTo(t, appendedPath, claudeNoCWDMessages("scale-00", "2026-03-11T09:05:00Z", "assistant", `"concurrentappendtoken unique reply"`))
	bumpMTime(t, appendedPath)

	renamePrefixReads.Store(0)
	commandRecordsMined.Store(0)

	indexPass(t, dir)

	if reads := renamePrefixReads.Load(); reads != 1 {
		t.Fatalf("renamePrefixReads = %d, want 1 (only the 1 appended moved file)", reads)
	}
	if files := lastIngestFiles.Load(); files != 1 {
		t.Fatalf("lastIngestFiles = %d, want 1", files)
	}
	delta := recordsSize(t, dir) - before
	if delta <= 0 || delta >= 2048 {
		t.Fatalf("records.bin delta = %d bytes, want small append delta in (0, 2048)", delta)
	}

	hits, err := Search(dir, query.Options{Query: "concurrentappendtoken", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "scale-00" || hits[0].Project != "alex/scale2" {
		t.Fatalf("unexpected search hits after rename+append: %+v", hits)
	}

	sameAsRebuild(t, dir)
}

func setup50kRenameBench(b *testing.B) (claudeRoot, idxDir string) {
	b.Helper()
	tmp := b.TempDir()
	b.Setenv("HOME", tmp)
	b.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	b.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	b.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "cache"))
	b.Setenv("DEJA_INDEX_DIR", "")
	claudeRoot = filepath.Join(tmp, "claude")
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	b.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	b.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	b.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	b.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	b.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	b.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	b.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	b.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "no-grok"))
	b.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "no-qwen"))

	const dirs = 500
	const perDir = 100
	for d := range dirs {
		proj := filepath.Join(claudeRoot, fmt.Sprintf("-Users-alex-p%03d", d))
		if err := os.MkdirAll(proj, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := range perDir {
			id := fmt.Sprintf("b%03d-%02d", d, f)
			body := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-04-01T00:00:00Z","message":{"role":"user","content":%q}}`+"\n",
				id, fmt.Sprintf("bench payload %03d-%02d", d, f))
			if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}

	idxDir = filepath.Join(tmp, "idx")
	if err := Ensure(idxDir, "", true, nil); err != nil {
		b.Fatal(err)
	}
	return claudeRoot, idxDir
}

func BenchmarkRename50k(b *testing.B) {
	claudeRoot, idxDir := setup50kRenameBench(b)

	b.Run("single_file_rename", func(b *testing.B) {
		p0 := filepath.Join(claudeRoot, "-Users-alex-p000", "b000-00.jsonl")
		p1 := filepath.Join(claudeRoot, "-Users-alex-p000", "b000-00-renamed.jsonl")
		b.ResetTimer()
		for i := range b.N {
			src, dst := p0, p1
			if i%2 == 1 {
				src, dst = p1, p0
			}
			if err := os.Rename(src, dst); err != nil {
				b.Fatal(err)
			}
			if err := Ensure(idxDir, "", false, nil); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("dir_rename_100", func(b *testing.B) {
		d0 := filepath.Join(claudeRoot, "-Users-alex-p001")
		d1 := filepath.Join(claudeRoot, "-Users-alex-p001-moved")
		b.ResetTimer()
		for i := range b.N {
			src, dst := d0, d1
			if i%2 == 1 {
				src, dst = d1, d0
			}
			if err := os.Rename(src, dst); err != nil {
				b.Fatal(err)
			}
			if err := Ensure(idxDir, "", false, nil); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("dir_rename_100_with_1_append", func(b *testing.B) {
		d0 := filepath.Join(claudeRoot, "-Users-alex-p002")
		d1 := filepath.Join(claudeRoot, "-Users-alex-p002-moved")
		b.ResetTimer()
		for i := range b.N {
			b.StopTimer()
			src, dst := d0, d1
			if i%2 == 1 {
				src, dst = d1, d0
			}
			if err := os.Rename(src, dst); err != nil {
				b.Fatal(err)
			}
			target := filepath.Join(dst, "b002-00.jsonl")
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				b.Fatal(err)
			}
			turn := fmt.Sprintf(`{"type":"assistant","sessionId":"b002-00","timestamp":%q,"message":{"role":"assistant","content":%q}}`+"\n",
				time.Date(2026, 4, 1, 1, 0, i+1, 0, time.UTC).Format(time.RFC3339),
				fmt.Sprintf("bench appended %d", i))
			if _, err := f.WriteString(turn); err != nil {
				_ = f.Close()
				b.Fatal(err)
			}
			_ = f.Close()
			now := time.Now().Add(time.Duration(i+1) * time.Second)
			_ = os.Chtimes(target, now, now)
			b.StartTimer()

			if err := Ensure(idxDir, "", false, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}
