package index

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func hermeticIndexEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "default-index"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_AIDER_ROOTS", "")
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "antigravity"))
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	return tmp
}

func TestPhraseAndFuzzyIndexedSearch(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "fuzzy-index")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sessions := []model.Session{{ID: "phrase", Harness: "claude", Project: "p", Updated: base, Messages: []model.Message{{Role: "user", Text: "connection pool exhausted"}}}, {ID: "apart", Harness: "claude", Project: "p", Updated: base, Messages: []model.Message{{Role: "user", Text: "connection pool was exhausted"}}}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	result, err := SearchDetailed(dir, search.Options{Query: `"connection pool exhausted"`, All: true})
	if err != nil || len(result.Sessions) != 1 || result.Sessions[0].ID != "phrase" {
		t.Fatalf("phrase result=%#v err=%v", result, err)
	}
	result, err = SearchDetailed(dir, search.Options{Query: "exhaustd", All: true})
	if err != nil || !result.Fuzzy || len(result.Sessions) != 2 {
		t.Fatalf("fuzzy result=%#v err=%v", result, err)
	}
	o := search.Options{Query: "exhaustd", All: true, FuzzyVariants: result.Variants}
	hits, err := search.Run(result.Sessions, o)
	if err != nil || len(hits) != 2 || hits[0].Count == 0 {
		t.Fatalf("fuzzy pipeline hits=%#v err=%v", hits, err)
	}
}

func BenchmarkFuzzyTokenEnumeration(b *testing.B) {
	catalog := make(map[string]bool, 50000)
	for i := 0; i < 50000; i++ {
		catalog[fmt.Sprintf("synthetic-token-%05d", i)] = true
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = closeTokens("synthetic-token-12345", catalog)
	}
}

func TestFuzzyHelperCapsAndDistanceRules(t *testing.T) {
	catalog := map[string]bool{"abcdefgh": true, "abcdefgi": true, "abcdxfgh": true, "abcdefghij": true}
	got := closeTokens("abcdefgh", catalog)
	if len(got) > 8 || len(got) == 0 || got[0] != "abcdefgh" {
		t.Fatalf("close tokens=%v", got)
	}
	if damerauDistance("abcd", "acbd", 1) != 1 || damerauDistance("abcdefgh", "abcfghxy", 1) <= 1 {
		t.Fatal("distance cap or transposition is wrong")
	}
	if damerauDistance("界界", "界界界", 1) != 1 || abs(-4) != 4 || abs(4) != 4 {
		t.Fatal("unicode distance helpers failed")
	}
	if got := intersectPostingMaps(nil); got != nil {
		t.Fatalf("empty intersection=%v", got)
	}
	empty := t.TempDir()
	if catalog, err := tokenCatalog(empty); err != nil || len(catalog) != 0 {
		t.Fatalf("missing catalog=%v err=%v", catalog, err)
	}
	if posts, variants, err := fuzzyPostings(empty, []string{"abc"}, nil); err != nil || posts != nil || variants != nil {
		t.Fatalf("short fuzzy query=%v %v err=%v", posts, variants, err)
	}
	if posts, variants, err := fuzzyPostings(empty, []string{"abcd"}, nil); err != nil || posts != nil || variants != nil {
		t.Fatalf("unmatched fuzzy query=%v %v err=%v", posts, variants, err)
	}
	bad := filepath.Join(empty, "buckets")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "aa.bin"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenCatalog(empty); err == nil {
		t.Fatal("malformed catalog returned nil error")
	}
	if result, err := fuzzySearch(empty, Manifest{}, search.Options{Query: "abc"}); err != nil || result.Fuzzy {
		t.Fatalf("empty fuzzy search=%#v err=%v", result, err)
	}
}

func writeTinyIndex(t *testing.T, dir string) {
	t.Helper()
	tmp := dir + ".tmp"
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ss := []model.Session{
		{Harness: "claude", ID: "s1", Project: "proj-a", Path: "source-a", Started: base, Updated: base.Add(time.Minute), Messages: []model.Message{{Role: "user", Text: "alpha needle AKIAIOSFODNN7EXAMPLE", Time: base}, {Role: "assistant", Text: "opencode answer", Time: base.Add(time.Minute)}}},
		{Harness: "codex", ID: "s2", Project: "proj-b", Path: "source-b", Started: base.Add(time.Hour), Updated: base.Add(time.Hour), Messages: []model.Message{{Role: "user", Text: "regex only zzz", Time: base.Add(time.Hour)}}},
	}
	files := map[string]FileState{"source-a": {Path: "source-a", Size: 1, MTime: 2}, "source-b": {Path: "source-b", Size: 3, MTime: 4}}
	if err := writeSessions(tmp, dir, ss, files, "scope-a"); err != nil {
		t.Fatal(err)
	}
}

func TestIndexReadWriteAndQueryEdgeBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)
	if DefaultDir() != filepath.Join(tmp, "default-index") {
		t.Fatal("DefaultDir ignored env")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if got := displayPath(filepath.Join(home, "idx")); got != "~"+string(os.PathSeparator)+"idx" {
			t.Fatalf("displayPath home contraction = %q", got)
		}
	}
	if got := displayPath(filepath.Join(t.TempDir(), "idx")); strings.HasPrefix(got, "~") {
		t.Fatalf("displayPath outside home = %q", got)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "alpha"}, false, nil); err != nil {
		t.Fatalf("EnsureForSearch rebuild for scope mismatch: %v", err)
	}
	writeTinyIndex(t, dir)
	got, err := Search(dir, search.Options{Regex: true, Query: "["})
	if err != nil || len(got) != 2 {
		t.Fatalf("regex scan fallback got=%#v err=%v", got, err)
	}
	got, err = Search(dir, search.Options{Query: "code"})
	if err != nil || len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("substring postings got=%#v err=%v", got, err)
	}
	if recent, err := Recent(dir, 1); err != nil || len(recent) != 1 {
		t.Fatalf("Recent default recent=%#v err=%v", recent, err)
	}
	if recent, err := RecentProject(dir, "PROJ", 1); err != nil || len(recent) != 1 || len(recent[0].Messages) == 0 {
		t.Fatalf("RecentProject contains/limit=%#v err=%v", recent, err)
	}
	found, ok, err := FindByPrefix(dir, "missing")
	if err != nil || ok || found.ID != "" {
		t.Fatalf("FindByPrefix missing found=%#v ok=%v err=%v", found, ok, err)
	}
	stats, err := Redactions(dir)
	if err != nil || stats.Total == 0 || stats.Files["source-a"] == 0 {
		t.Fatalf("Redactions=%#v err=%v", stats, err)
	}
	if _, err := Search(filepath.Join(tmp, "missing"), search.Options{}); err == nil {
		t.Fatal("Search missing manifest returned nil error")
	}
	if _, err := Redactions(filepath.Join(tmp, "missing")); err == nil {
		t.Fatal("Redactions missing manifest returned nil error")
	}
}

func TestBucketRecordGobAndRecoveryErrors(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)
	bucketPath := filepath.Join(dir, "buckets", bucket("talpha")+".bin")
	if err := os.WriteFile(bucketPath, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Search(dir, search.Options{Query: "alpha"})
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("corrupt bucket err=%v", err)
	}
	var progress bytes.Buffer
	if ss, err := SearchWithRecovery(dir, search.Options{Query: "alpha"}, &progress); err != nil || ss != nil {
		t.Fatalf("recovery ss=%#v err=%v", ss, err)
	}
	if !strings.Contains(progress.String(), "index damaged") {
		t.Fatalf("missing recovery progress: %q", progress.String())
	}

	if err := writeBucket(filepath.Join(tmp, "bucket.bin"), map[string][]posting{"tok": {{Off: 2, Sid: 1}, {Off: 2, Sid: 1}, {Off: 9, Sid: 3}}}); err != nil {
		t.Fatal(err)
	}
	posts, err := readBucketToken(filepath.Join(tmp, "bucket.bin"), "tok")
	if err != nil || len(posts) != 2 || posts[1].Off != 9 {
		t.Fatalf("readBucketToken posts=%#v err=%v", posts, err)
	}
	if none, err := readBucketToken(filepath.Join(tmp, "bucket.bin"), "none"); err != nil || none != nil {
		t.Fatalf("missing token posts=%#v err=%v", none, err)
	}
	if got := decodePostings([]byte{0x80}); len(got) != 0 {
		t.Fatalf("truncated postings=%#v", got)
	}
	if uvarintLen(1) != 1 || uvarintLen(128) != 2 {
		t.Fatal("uvarintLen edges failed")
	}

	var buf bytes.Buffer
	if _, err := readRecordAt(os.NewFile(0, os.DevNull), 1); err == nil && runtime.GOOS != "windows" {
		t.Fatal("readRecordAt bad seek returned nil")
	}
	if _, err := readRecord(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("empty readRecord err=%v", err)
	}
	if _, err := decodeRecord([]byte{1, 'k'}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short decode err=%v", err)
	}
	gobPath := filepath.Join(tmp, "bad.gob")
	if err := os.WriteFile(gobPath, []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := readGob(gobPath, &out); err == nil || !strings.Contains(err.Error(), "bad.gob") {
		t.Fatalf("readGob err=%v", err)
	}
}

func TestSyncErrorPathsAndHelpers(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)
	if _, err := Export(filepath.Join(tmp, "missing"), filepath.Join(tmp, "out")); err == nil {
		t.Fatal("Export missing manifest returned nil")
	}
	if _, err := Export(dir, string([]byte{0})); err == nil {
		t.Fatal("Export bad out dir returned nil")
	}
	bad := filepath.Join(tmp, "bad-sync.jsonl")
	if err := os.WriteFile(bad, []byte(`{"harness":"claude","session_id":"s"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("stop")
	if err := readSyncFile(bad, func(SyncRecord) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("readSyncFile callback err=%v", err)
	}
	if err := readSyncFile(filepath.Join(tmp, "missing.jsonl"), func(SyncRecord) error { return nil }); err == nil {
		t.Fatal("readSyncFile missing returned nil")
	}
	emptyBatch := filepath.Join(tmp, "empty-batch")
	if err := os.MkdirAll(emptyBatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := Import(filepath.Join(tmp, "newidx"), emptyBatch); err != nil || n != 0 {
		t.Fatalf("empty import n=%d err=%v", n, err)
	}
	if got := shortHash(""); got == "" || strings.Contains(got, "-") {
		t.Fatalf("shortHash=%q", got)
	}
}

func TestLowLevelIndexHelpers(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	old := Manifest{Files: map[string]FileState{"keep": {Path: "keep", Redactions: 2}, "skip": {Path: "skip", Redactions: 5}}}
	m := Manifest{Files: map[string]FileState{"keep": {Path: "keep"}, "skip": {Path: "skip"}}}
	carryRedactions(&m, old, map[string]bool{"skip": true})
	if m.Redacted != 2 || m.Files["skip"].Redactions != 0 {
		t.Fatalf("carryRedactions=%#v", m)
	}
	files := map[string]FileState{"db": {Path: "db"}}
	setStoreLastUpdated(files, map[string]SessionMeta{"x": {Harness: "h", Updated: time.Unix(0, 7)}}, "h", "db")
	if files["db"].LastUpdated != 7 {
		t.Fatalf("LastUpdated=%d", files["db"].LastUpdated)
	}
	if got := recordMatchesQuery(Record{Text: "Hello World"}, search.Options{Query: "hello missing"}); got {
		t.Fatal("recordMatchesQuery matched absent token")
	}
	if got := bucket("!"); !strings.HasPrefix(got, "x") {
		t.Fatalf("short bucket=%q", got)
	}
	if got := safe("a-Z_9"); got != "a___9" {
		t.Fatalf("safe=%q", got)
	}
	if got := harnessForPath(filepath.Join(tmp, "unknown.txt")); got != "" {
		t.Fatalf("unknown harness=%q", got)
	}
	if _, err := parseChangedFile("", filepath.Join(tmp, "unknown.txt"), FileState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := parseAppendedFile("", filepath.Join(tmp, "unknown.txt"), FileState{SafeSize: 10, Size: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDirQueriesFiltersAndCorruptBucketBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := DefaultDir()
	writeTinyIndex(t, dir)
	if got, err := Search("", search.Options{Query: "absent"}); err != nil || got != nil {
		t.Fatalf("default Search absent=%#v err=%v", got, err)
	}
	if got, err := Recent("", 2); err != nil || len(got) != 2 {
		t.Fatalf("default Recent=%#v err=%v", got, err)
	}
	if got, err := RecentProject("", "proj-a", 0); err != nil || len(got) != 1 {
		t.Fatalf("default RecentProject=%#v err=%v", got, err)
	}
	if got, ok, err := FindByPrefix("", "s1"); err != nil || !ok || got.ID != "s1" {
		t.Fatalf("default FindByPrefix=%#v ok=%v err=%v", got, ok, err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := scanRecords(dir, m, search.Options{Harness: "none"}, nil); err != nil || len(got) != 0 {
		t.Fatalf("harness filter=%#v err=%v", got, err)
	}
	if got, err := scanRecords(dir, m, search.Options{Project: "none"}, nil); err != nil || len(got) != 0 {
		t.Fatalf("project filter=%#v err=%v", got, err)
	}
	if got, err := scanRecords(dir, m, search.Options{Role: "system"}, nil); err != nil || len(got) != 0 {
		t.Fatalf("role filter=%#v err=%v", got, err)
	}
	old := m.Sessions["claude:s1"]
	old.Updated = time.Now().Add(-48 * time.Hour)
	m.Sessions["claude:s1"] = old
	if got, err := scanRecords(dir, m, search.Options{Since: time.Hour}, nil); err != nil || len(got) != 0 {
		t.Fatalf("since filter=%#v err=%v", got, err)
	}
	if got := cutPostingsBySession([]posting{{Off: 1, Sid: 99}}, m, search.Options{}); got != nil {
		t.Fatalf("unknown sid cut=%#v", got)
	}
	var many []posting
	manyManifest := Manifest{Sessions: map[string]SessionMeta{}}
	for i := 1; i <= 20; i++ {
		many = append(many, posting{Off: int64(i), Sid: uint32(i)})
		manyManifest.Sessions[fmt.Sprintf("h:%d", i)] = SessionMeta{ID: fmt.Sprint(i), Harness: "h", Project: "p", Ord: uint32(i), Updated: time.Now().Add(time.Duration(i) * time.Second)}
	}
	if got := cutPostingsBySession(many, manyManifest, search.Options{}); len(got) != 20 {
		t.Fatalf("candidate cut len=%d", len(got))
	}

	for name, data := range map[string][]byte{
		"magic":  []byte("NOPE"),
		"count":  bucketMagic,
		"toklen": append(append([]byte{}, bucketMagic...), 1),
	} {
		p := filepath.Join(tmp, name+".bin")
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := openBucketDir(p); err == nil || !IsCorrupt(err) {
			t.Fatalf("%s corrupt err=%v", name, err)
		}
	}
	var b bytes.Buffer
	b.Write(bucketMagic)
	b.WriteByte(1)
	b.WriteByte(3)
	b.WriteString("ab")
	shortTok := filepath.Join(tmp, "short-token.bin")
	if err := os.WriteFile(shortTok, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openBucketDir(shortTok); err == nil || !IsCorrupt(err) {
		t.Fatalf("short token err=%v", err)
	}
	b.Reset()
	b.Write(bucketMagic)
	b.WriteByte(1)
	b.WriteByte(1)
	b.WriteByte('a')
	b.Write([]byte{1, 2})
	shortFixed := filepath.Join(tmp, "short-fixed.bin")
	if err := os.WriteFile(shortFixed, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openBucketDir(shortFixed); err == nil || !IsCorrupt(err) {
		t.Fatalf("short fixed err=%v", err)
	}
}

func TestCurrentFilesAllHarnessesAndRecordEdgeCases(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	notes := os.Getenv("DEJA_NOTES_FILE")
	if err := os.WriteFile(notes, []byte(`{"ts":"2026-01-02T03:04:05Z","project":"notes-app","text":"first durable note"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "proj", "s.jsonl")
	codexHist := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "history.jsonl")
	codexRoll := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "01", "02", "rollout-2026-01-02T00-00-00-r.jsonl")
	aider := filepath.Join(tmp, "aider", ".aider.chat.history.md")
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Dir(aider))
	gemini := filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp", "gid", "chats", "g.jsonl")
	cursorDB := filepath.Join(os.Getenv("DEJA_CURSOR_ROOT"), "globalStorage", "state.vscdb")
	cursorTr := filepath.Join(os.Getenv("DEJA_CURSOR_CLI_ROOT"), "projects", "p", "agent-transcripts", "c", "c.jsonl")
	ag := filepath.Join(os.Getenv("DEJA_ANTIGRAVITY_ROOT"), "brain", "traj", ".system_generated", "logs", "transcript.jsonl")
	for _, p := range []string{claude, codexHist, codexRoll, aider, gemini, cursorDB, cursorTr, ag, os.Getenv("DEJA_OPENCODE_DB"), notes} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := currentFiles("")
	for _, p := range []string{claude, codexHist, codexRoll, aider, gemini, cursorDB, cursorTr, ag, os.Getenv("DEJA_OPENCODE_DB"), notes} {
		if _, ok := files[p]; !ok {
			t.Fatalf("currentFiles missing %s in %#v", p, files)
		}
	}
	if lastCompleteLineOffset(filepath.Join(tmp, "missing"), 10) != 10 {
		t.Fatal("missing lastCompleteLineOffset did not return size")
	}
	noNL := filepath.Join(tmp, "nonl.txt")
	if err := os.WriteFile(noNL, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lastCompleteLineOffset(noNL, 3) != 0 {
		t.Fatal("unterminated short file did not return 0")
	}
	buf := appendField(nil, "k")
	if _, err := decodeRecord(buf); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("decode missing source err=%v", err)
	}
	buf = appendField(buf, "src")
	buf = appendField(buf, "role")
	if rec, err := decodeRecord(buf); !errors.Is(err, io.ErrUnexpectedEOF) || rec.Key != "k" {
		t.Fatalf("decode missing time rec=%#v err=%v", rec, err)
	}
	buf = binary.LittleEndian.AppendUint64(buf, uint64(time.Now().UnixNano()))
	if _, err := decodeRecord(buf); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("decode missing text err=%v", err)
	}
	if _, err := scanRecords(filepath.Join(tmp, "missing-index"), Manifest{}, search.Options{}, nil); err == nil {
		t.Fatal("scanRecords missing records returned nil")
	}
}

func TestNotesIncrementalIndex(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	notes := os.Getenv("DEJA_NOTES_FILE")
	if err := os.WriteFile(notes, []byte(`{"ts":"2026-01-02T03:04:05Z","project":"notes-app","text":"first durable note"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "notes-index")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	var progress strings.Builder
	if got := loadProgress("deja", &progress); len(got) != 1 || !strings.Contains(progress.String(), "notes") {
		t.Fatalf("notes loader=%#v progress=%q", got, progress.String())
	}
	if got, err := Search(dir, search.Options{Query: "first", All: true}); err != nil || len(got) != 1 || got[0].Harness != "deja" {
		t.Fatalf("first note=%#v err=%v", got, err)
	}
	f, err := os.OpenFile(notes, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"ts":"2026-01-02T04:04:05Z","project":"notes-app","text":"second durable note"}` + "\n")
	_ = f.Close()
	if err := EnsureForSearch(dir, search.Options{All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := Search(dir, search.Options{Query: "second", All: true}); err != nil || len(got) != 1 {
		t.Fatalf("second note=%#v err=%v", got, err)
	}
}

func TestDuplicateSessionBranchesAndAdditionalParsers(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(claudeRoot, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", maxIndexedText+10)
	for i, text := range []string{"first duplicate", long, "first duplicate"} {
		line := fmt.Sprintf(`{"type":"user","sessionId":"dup","timestamp":"2026-01-02T03:0%d:05Z","message":{"role":"user","content":%q}}`+"\n", i, text)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "dup-index")
	if err := rebuild(dir, "claude", "", currentFiles("claude"), nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "duplicate", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("duplicate rebuild search=%#v err=%v", ss, err)
	}

	tmpOut := filepath.Join(tmp, "manual.tmp")
	if err := os.MkdirAll(filepath.Join(tmpOut, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	dups := []model.Session{
		{Harness: "h", ID: "same", Project: "history", Title: "", Started: base.Add(time.Hour), Updated: base, Messages: []model.Message{{Role: "user", Text: long, Time: base}}},
		{Harness: "h", ID: "same", Project: "better", Title: "kept", Started: base, Updated: base.Add(time.Hour), Messages: []model.Message{{Role: "assistant", Text: "manual duplicate branch", Time: base.Add(time.Hour)}}},
	}
	if err := writeSessionsWithSync(tmpOut, filepath.Join(tmp, "manual-index"), dups, map[string]FileState{}, "", importedState{}); err != nil {
		t.Fatal(err)
	}

	aider := filepath.Join(tmp, "aider", ".aider.chat.history.md")
	gemini := filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp", "gid", "chats", "g.json")
	cursorTr := filepath.Join(os.Getenv("DEJA_CURSOR_CLI_ROOT"), "projects", "p", "agent-transcripts", "c", "c.jsonl")
	ag := filepath.Join(os.Getenv("DEJA_ANTIGRAVITY_ROOT"), "brain", "traj", ".system_generated", "logs", "transcript.jsonl")
	fixtures := map[string]string{
		aider:    "# aider chat started at 2026-01-01 00:00:00\n#### q\na\n",
		gemini:   `{"sessionId":"g","startTime":"2026-01-01T00:00:00Z","messages":[{"type":"user","content":"g text"}]}`,
		cursorTr: `{"role":"user","message":{"content":"c text"}}` + "\n",
		ag:       `{"source":"USER_EXPLICIT","created_at":"2026-01-01T00:00:00Z","content":"ag text"}` + "\n",
	}
	for p, data := range fixtures {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := parseChangedFile("", p, FileState{}); err != nil || len(got) != 1 {
			t.Fatalf("parseChanged %s=%#v err=%v", p, got, err)
		}
	}
	for _, p := range []string{aider, gemini, cursorTr, ag} {
		if got := harnessForPath(p); got == "" {
			t.Fatalf("harnessForPath empty for %s", p)
		}
	}
}

func TestImportOldMetaBranchAndSkips(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	base := time.Date(2026, 3, 1, 1, 0, 0, 0, time.UTC)
	batch1 := filepath.Join(tmp, "batch1")
	writeSyncBatch(t, batch1, []SyncRecord{{Harness: "claude", SessionID: "same", Project: "p", Role: "user", Text: "one import old", Time: base}, {Harness: "", SessionID: "skip", Text: "skip", Time: base}})
	if n, err := Import(dir, batch1); err != nil || n != 1 {
		t.Fatalf("first import n=%d err=%v", n, err)
	}
	batch2 := filepath.Join(tmp, "batch2")
	writeSyncBatch(t, batch2, []SyncRecord{{Harness: "claude", SessionID: "same", Project: "p", Role: "assistant", Text: "two import old", Time: base.Add(time.Hour)}})
	if n, err := Import(dir, batch2); err != nil || n != 1 {
		t.Fatalf("second import n=%d err=%v", n, err)
	}
	ss, err := Search(dir, search.Options{Query: "import old", All: true})
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("imported old branch sessions=%#v err=%v", ss, err)
	}
}

func TestRequestedPureCheapIndexCoverageBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)

	cursorDB := filepath.Join(os.Getenv("DEJA_CURSOR_ROOT"), "globalStorage", "state.vscdb")
	gemini := filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp", "gid", "chats", "g.json")
	ag := filepath.Join(os.Getenv("DEJA_ANTIGRAVITY_ROOT"), "brain", "traj", ".system_generated", "logs", "transcript.jsonl")
	unknown := filepath.Join(tmp, "unknown.txt")
	for _, p := range []string{cursorDB, gemini, ag, unknown} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cursorDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gemini, []byte(`{"sessionId":"g","startTime":"2026-01-01T00:00:00Z","messages":[{"type":"user","content":"hello gemini"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ag, []byte(`{"source":"USER_EXPLICIT","created_at":"2026-01-01T00:00:00Z","content":"hello antigravity"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		old  FileState
		want int
	}{
		{name: "cursor-db full", path: cursorDB},
		{name: "cursor-db since", path: cursorDB, old: FileState{LastUpdated: time.Now().UnixNano()}},
		{name: "gemini", path: gemini, want: 1},
		{name: "antigravity", path: ag, want: 1},
		{name: "unknown", path: unknown},
	} {
		t.Run("changed "+tc.name, func(t *testing.T) {
			got, err := parseChangedFile("", tc.path, tc.old)
			if err != nil || len(got) != tc.want {
				t.Fatalf("parseChangedFile len=%d err=%v", len(got), err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		path string
		old  FileState
	}{
		{name: "cursor-db full", path: cursorDB, old: FileState{Size: 10, SafeSize: 20}},
		{name: "cursor-db since", path: cursorDB, old: FileState{LastUpdated: time.Now().UnixNano()}},
		{name: "gemini default", path: gemini},
		{name: "antigravity default", path: ag},
		{name: "unknown default", path: unknown},
	} {
		t.Run("appended "+tc.name, func(t *testing.T) {
			got, err := parseAppendedFile("", tc.path, tc.old)
			if err != nil || got != nil {
				t.Fatalf("parseAppendedFile got=%#v err=%v", got, err)
			}
		})
	}

	for _, tc := range []struct {
		name string
		s    model.Session
		want string
	}{
		{name: "local command", s: model.Session{Messages: []model.Message{{Role: "user", Text: "<local-command x"}, {Role: "user", Text: "real title"}}}, want: "real title"},
		{name: "command", s: model.Session{Messages: []model.Message{{Role: "user", Text: "<command-name>"}, {Role: "user", Text: "next"}}}, want: "next"},
		{name: "task notification", s: model.Session{Messages: []model.Message{{Role: "user", Text: "<task-notification z"}, {Role: "user", Text: "task"}}}, want: "task"},
		{name: "teammate", s: model.Session{Messages: []model.Message{{Role: "user", Text: "<teammate-message z"}, {Role: "user", Text: "team"}}}, want: "team"},
		{name: "caveat", s: model.Session{Messages: []model.Message{{Role: "user", Text: "Caveat: noisy"}, {Role: "assistant", Text: "skip"}}}, want: ""},
	} {
		t.Run("title "+tc.name, func(t *testing.T) {
			if got := sessionTitle(tc.s); got != tc.want {
				t.Fatalf("sessionTitle=%q want %q", got, tc.want)
			}
		})
	}

	manifestDir := filepath.Join(tmp, "manifest-check")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if HasManifest(manifestDir) {
		t.Fatal("empty dir has manifest")
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.gob"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if HasManifest(manifestDir) {
		t.Fatal("manifest without sessions detected")
	}

	idx := filepath.Join(tmp, "empty-index")
	if err := initEmptyIndex(idx); err != nil {
		t.Fatal(err)
	}
	if !HasManifest(idx) {
		t.Fatal("initEmptyIndex did not write manifest files")
	}
	if fi, err := os.Stat(filepath.Join(idx, "records.bin")); err != nil || fi.Size() != 0 {
		t.Fatalf("records.bin stat=%v err=%v", fi, err)
	}

	for _, tc := range []struct {
		name string
		rec  Record
		o    search.Options
		want bool
	}{
		{name: "regex bypass", rec: Record{Text: ""}, o: search.Options{Regex: true, Query: "missing"}, want: true},
		{name: "empty query", rec: Record{Text: ""}, o: search.Options{}, want: true},
		{name: "all tokens", rec: Record{Text: "Hello brave world"}, o: search.Options{Query: "hello world"}, want: true},
		{name: "missing token", rec: Record{Text: "Hello world"}, o: search.Options{Query: "hello absent"}, want: false},
	} {
		t.Run("match "+tc.name, func(t *testing.T) {
			if got := recordMatchesQuery(tc.rec, tc.o); got != tc.want {
				t.Fatalf("recordMatchesQuery=%v want %v", got, tc.want)
			}
		})
	}
	if got := queryKeys("b aa b ccc"); strings.Join(got, ",") != "tccc,taa" {
		t.Fatalf("queryKeys=%#v", got)
	}
	if got := queryKeys("! ?"); got != nil {
		t.Fatalf("empty queryKeys=%#v", got)
	}
}

func TestRequestedCodecLineAndSubstringBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	if got := lastCompleteLineOffset(filepath.Join(tmp, "empty"), 0); got != 0 {
		t.Fatalf("empty lastCompleteLineOffset=%d", got)
	}
	largeNoNL := filepath.Join(tmp, "large-nonl")
	large := strings.Repeat("x", 64*1024+5)
	if err := os.WriteFile(largeNoNL, []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lastCompleteLineOffset(largeNoNL, int64(len(large))); got != 0 {
		t.Fatalf("large no newline offset=%d, want 0: no complete line exists yet", got)
	}
	exact := filepath.Join(tmp, "exact-window")
	exactData := strings.Repeat("x", 64*1024-1) + "\n"
	if err := os.WriteFile(exact, []byte(exactData), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lastCompleteLineOffset(exact, int64(len(exactData))); got != int64(len(exactData)) {
		t.Fatalf("exact boundary offset=%d", got)
	}

	for _, data := range [][]byte{
		{0x80},
		appendField(nil, "key"),
		[]byte("garbage"),
	} {
		if _, err := decodeRecord(data); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("decodeRecord(%v) err=%v", data, err)
		}
	}
	recPath := filepath.Join(tmp, "records.bin")
	if err := os.WriteFile(recPath, []byte{0, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(recPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := readRecordAt(f, 99); !errors.Is(err, io.EOF) {
		t.Fatalf("readRecordAt bad offset err=%v", err)
	}

	if got, err := intersectSubstringPostings(filepath.Join(tmp, "missing-index"), []string{"nope"}); err != nil || got != nil {
		t.Fatalf("missing substring postings=%#v err=%v", got, err)
	}
	dir := filepath.Join(tmp, "substring-index")
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := intersectSubstringPostings(dir, nil); err != nil || got != nil {
		t.Fatalf("empty substring postings=%#v err=%v", got, err)
	}
	bp := filepath.Join(dir, "buckets", bucket("talpha")+".bin")
	if err := writeBucket(bp, map[string][]posting{
		"talpha": {{Off: 1, Sid: 1}, {Off: 2, Sid: 2}},
		"tbeta":  {{Off: 2, Sid: 2}, {Off: 3, Sid: 3}},
		"tgamma": {{Off: 2, Sid: 2}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := intersectSubstringPostings(dir, []string{"alp", "bet", "gam", "ignored"})
	if err != nil || len(got) != 1 || got[0].Off != 2 || got[0].Sid != 2 {
		t.Fatalf("multi substring postings=%#v err=%v", got, err)
	}
	got, err = intersectSubstringPostings(dir, []string{"alp", "zzz"})
	if err != nil || got != nil {
		t.Fatalf("disjoint substring postings=%#v err=%v", got, err)
	}
}

func TestIndexErrorBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	blockedParent := filepath.Join(tmp, "blocked")
	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(blockedParent, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(blockedParent, 0o500); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(blockedParent, 0o755) }()
		badDir := filepath.Join(blockedParent, "child", "idx")
		for name, fn := range map[string]func() error{
			"Ensure":          func() error { return Ensure(badDir, "", false, nil) },
			"EnsureForSearch": func() error { return EnsureForSearch(badDir, search.Options{}, false, nil) },
			"Search": func() error {
				_, err := Search(badDir, search.Options{})
				return err
			},
			"Recent": func() error {
				_, err := Recent(badDir, 1)
				return err
			},
			"RecentProject": func() error {
				_, err := RecentProject(badDir, "p", 1)
				return err
			},
			"FindByPrefix": func() error {
				_, _, err := FindByPrefix(badDir, "p")
				return err
			},
		} {
			if err := fn(); err == nil {
				t.Fatalf("%s blocked dir returned nil", name)
			}
		}
	}

	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateIndex(dir, "", m.Scope, m.Files, false, nil); err != nil || lastIngestFiles != 0 {
		t.Fatalf("fresh update err=%v last=%d", err, lastIngestFiles)
	}
	if !manifestFresh(m, m.Files, m.Scope) || manifestFresh(m, m.Files, "other") {
		t.Fatal("manifestFresh scope branch failed")
	}
	if _, err := readManifest(filepath.Join(tmp, "missing-manifest")); err == nil {
		t.Fatal("readManifest missing returned nil")
	}
	manifestOnly := filepath.Join(tmp, "manifest-only")
	if err := os.MkdirAll(manifestOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeGob(filepath.Join(manifestOnly, "manifest.gob"), manifestCore{Version: version}); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(manifestOnly); err == nil {
		t.Fatal("readManifest missing sessions returned nil")
	}
	badWriteDir := filepath.Join(tmp, "file-as-dir")
	if err := os.WriteFile(badWriteDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(badWriteDir, Manifest{}); err == nil {
		t.Fatal("writeManifest file dir returned nil")
	}
	if err := writeSessions(filepath.Join(tmp, "missing-tmp"), filepath.Join(tmp, "out"), nil, nil, ""); err == nil {
		t.Fatal("writeSessions missing tmp returned nil")
	}
	if _, err := indexTextParallel(func(func(tokenJob)) error { return errors.New("feed") }); err == nil {
		t.Fatal("indexTextParallel feed error returned nil")
	}
	if err := writeBucketsConcurrent(badWriteDir, bucketPostings{"ta": {"talpha": {{Off: 1, Sid: 1}}}}); err == nil {
		t.Fatal("writeBucketsConcurrent bad dir returned nil")
	}
	if err := writeBucket(filepath.Join(tmp, "missing-parent", "bucket.bin"), map[string][]posting{}); err == nil {
		t.Fatal("writeBucket missing parent returned nil")
	}
	if _, err := readBucket(filepath.Join(tmp, "missing-bucket.bin")); err == nil {
		t.Fatal("readBucket missing returned nil")
	}

	changed := filepath.Join(tmp, "changed.jsonl")
	line := `{"type":"user","sessionId":"new","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"alpha append"}}` + "\n"
	if err := os.WriteFile(changed, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	old := Manifest{Files: map[string]FileState{changed: {Path: changed, Size: 0}}, Sessions: map[string]SessionMeta{}, Scope: ""}
	if _, _, err := appendIncremental(filepath.Join(tmp, "missing-index"), "", "", old, old.Files, old.Files); err == nil {
		t.Fatal("appendIncremental missing dir returned nil")
	}
	if got := canAppendIncremental(nil, nil); got {
		t.Fatal("empty canAppendIncremental true")
	}
	if got := canAppendIncremental(map[string]FileState{"x": {Path: "x", Size: 1}}, map[string]FileState{}); got {
		t.Fatal("new file appendable")
	}
	if got, err := parseChangedFile("", os.Getenv("DEJA_OPENCODE_DB"), FileState{LastUpdated: time.Now().UnixNano()}); err != nil || got != nil {
		t.Fatalf("opencode lastupdated parse=%#v err=%v", got, err)
	}
	if got, err := parseAppendedFile("", filepath.Join(os.Getenv("DEJA_CURSOR_ROOT"), "state.vscdb"), FileState{LastUpdated: time.Now().UnixNano()}); err != nil || got != nil {
		t.Fatalf("cursor lastupdated parse=%#v err=%v", got, err)
	}
}

func TestFuzzySearchFilterAndCatalogErrors(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "fuzzy-edge")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sessions := []model.Session{{ID: "one", Harness: "claude", Project: "p", Updated: base, Messages: []model.Message{{Role: "user", Text: "connection pool exhausted"}}}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}

	// A harness filter that excludes every candidate session must yield an
	// empty non-fuzzy result, not an error.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := fuzzySearch(dir, m, search.Options{Query: "exhaustd", Harness: "codex"}); err != nil || result.Fuzzy || len(result.Sessions) != 0 {
		t.Fatalf("filtered fuzzy = %#v err=%v", result, err)
	}

	// A bucket entry with a corrupt header surfaces as an error.
	if err := os.WriteFile(filepath.Join(dir, "buckets", "zz.bin"), []byte("not a bucket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenCatalog(dir); err == nil {
		t.Fatal("expected tokenCatalog error for squatting directory")
	}
	if _, err := fuzzySearch(dir, m, search.Options{Query: "exhaustd"}); err == nil {
		t.Fatal("expected fuzzySearch to propagate catalog error")
	}

	// buckets squatted by a regular file: ReadDir fails with a non-NotExist
	// error that must propagate. Windows maps this to a NotExist-style error,
	// which tokenCatalog treats as an empty catalog.
	if runtime.GOOS == "windows" {
		return
	}
	flat := filepath.Join(tmp, "flat-index")
	if err := os.MkdirAll(flat, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(flat, "buckets"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenCatalog(flat); err == nil {
		t.Fatal("expected tokenCatalog ReadDir error")
	}
}

func TestFuzzySearchWithPhraseTokens(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "fuzzy-phrase")
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	sessions := []model.Session{{ID: "one", Harness: "claude", Project: "p", Updated: base, Messages: []model.Message{{Role: "user", Text: "connection pool exhausted"}}}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A quoted phrase alongside a fuzzy token exercises the phrase branch of
	// the fuzzy candidate collection.
	result, err := fuzzySearch(dir, m, search.Options{Query: `exhaustd "connection pool"`, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Fuzzy && len(result.Sessions) != 0 {
		t.Fatalf("unexpected result %#v", result)
	}

	// The full SearchDetailed path where postings match but the phrase filter
	// rejects every record falls through to the fuzzy branch.
	result, err = SearchDetailed(dir, search.Options{Query: `"pool connection"`, All: true})
	if err != nil || len(result.Sessions) != 0 {
		t.Fatalf("reversed phrase result=%#v err=%v", result, err)
	}
}
