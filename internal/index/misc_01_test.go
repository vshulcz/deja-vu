package index

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func hermeticIndexEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	// Hermes's own switch moves deja with it (#3203): pinned so a developer
	// with HERMES_HOME exported never has a test read or write their Hermes.
	t.Setenv("HERMES_HOME", "")
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
	// Zed resolves its store through the platform data directory, so both the
	// XDG roots and the deja override are pinned. Without this an index run
	// reads the contributor's own threads.
	t.Setenv("DEJA_ZED_ROOT", filepath.Join(tmp, "zed"))
	t.Setenv("DEJA_ZED_DB", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("FLATPAK_XDG_DATA_HOME", "")
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
	if err != nil || !result.Fuzzy || result.Tier != search.TierClose || len(result.Sessions) != 2 {
		t.Fatalf("fuzzy result=%#v err=%v", result, err)
	}
	o := search.Options{Query: "exhaustd", All: true, Tier: result.Tier, FuzzyVariants: result.Variants}
	hits, err := search.Run(result.Sessions, o)
	if err != nil || len(hits) != 2 || hits[0].Count == 0 {
		t.Fatalf("fuzzy pipeline hits=%#v err=%v", hits, err)
	}
	if hits[0].Tier != search.TierClose || hits[0].TierDetail == "" {
		t.Fatalf("fuzzy tier=%#v", hits)
	}
	result, err = SearchDetailed(dir, search.Options{Query: "haust", All: true})
	if err != nil || result.Tier != search.TierClose || len(result.Variants["haust"]) == 0 {
		t.Fatalf("substring tier=%#v err=%v", result, err)
	}
}

func BenchmarkFuzzyTokenEnumeration(b *testing.B) {
	catalog := make(map[string]bool, 50000)
	for i := range 50000 {
		catalog[fmt.Sprintf("synthetic-token-%05d", i)] = true
	}
	b.ResetTimer()
	for range b.N {
		_ = closeTokens("synthetic-token-12345", newTokenIndex(catalog))
	}
}

func BenchmarkStemTokenEnumeration(b *testing.B) {
	catalog := make(map[string]bool, 50000)
	for i := range 50000 {
		catalog[fmt.Sprintf("synthetic-token-%05d", i)] = true
	}
	b.ResetTimer()
	for range b.N {
		_ = stemMatches("synthetic-token-12345", catalog)
	}
}

func TestREADMEQueryUsesStopWordsAndWordForms(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "readme-index")
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ss := []model.Session{{ID: "jwt", Harness: "claude", Project: "p", Updated: when, Messages: []model.Message{{Role: "user", Text: "rotated the jwt refresh token", Time: when}}}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	result, err := SearchDetailed(dir, search.Options{Query: "have we dealt with jwt refresh rotation before", All: true})
	if err != nil || !result.Stemmed || len(result.Sessions) != 1 || result.Sessions[0].ID != "jwt" {
		t.Fatalf("README query result=%#v err=%v", result, err)
	}
	if len(result.Variants["rotation"]) == 0 {
		t.Fatalf("missing rotation variants: %#v", result.Variants)
	}
}

func TestSuffixForms(t *testing.T) {
	tests := map[string][]string{
		"rotation":   {"rotate", "rotated", "rotating"},
		"rotated":    {"rotate", "rotation", "rotating"},
		"tokens":     {"token"},
		"deployment": {"deploy"},
	}
	for word, want := range tests {
		got := suffixForms(word)
		for _, form := range want {
			if !containsString(got, form) {
				t.Errorf("suffixForms(%q)=%v, missing %q", word, got, form)
			}
		}
	}
}

func TestStemMatchCapsAndShortTerms(t *testing.T) {
	catalog := map[string]bool{"token": true}
	if got := stemMatches("to", catalog); len(got) != 0 {
		t.Fatalf("short unmatched stem=%v", got)
	}
	if got := stemMatches("token", catalog); len(got) != 1 || got[0] != "token" {
		t.Fatalf("short exact stem=%v", got)
	}
	for i := range 12 {
		catalog[fmt.Sprintf("rotate-form-%02d", i)] = true
	}
	if got := stemMatches("rotate", catalog); len(got) > 8 {
		t.Fatalf("stem cap=%v", got)
	}
	if posts, variants, err := stemPostings(t.TempDir(), []string{"tiny"}, nil); err != nil || posts != nil || variants != nil {
		t.Fatalf("short stem postings=%v %v err=%v", posts, variants, err)
	}
	if result, err := stemSearch(t.TempDir(), Manifest{}, search.Options{Query: "rotation"}); err != nil || result.Stemmed {
		t.Fatalf("empty stem search=%#v err=%v", result, err)
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func TestFuzzyHelperCapsAndDistanceRules(t *testing.T) {
	catalog := map[string]bool{"abcdefgh": true, "abcdefgi": true, "abcdxfgh": true, "abcdefghij": true}
	got := closeTokens("abcdefgh", newTokenIndex(catalog))
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
	if recent, recentErr := Recent(dir, 1); recentErr != nil || len(recent) != 1 {
		t.Fatalf("Recent default recent=%#v err=%v", recent, recentErr)
	}
	if recent, projectErr := RecentProject(dir, "PROJ", 1); projectErr != nil || len(recent) != 1 || len(recent[0].Messages) == 0 {
		t.Fatalf("RecentProject contains/limit=%#v err=%v", recent, projectErr)
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
	// The rebuild keeps the session whose transcript is gone, the way
	// `deja index --force` does.
	if ss, searchErr := SearchWithRecovery(dir, search.Options{Query: "alpha"}, &progress); searchErr != nil || len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("recovery ss=%#v err=%v", ss, searchErr)
	}
	if !strings.Contains(progress.String(), "index damaged") {
		t.Fatalf("missing recovery progress: %q", progress.String())
	}

	if err = writeBucket(filepath.Join(tmp, "bucket.bin"), map[string][]posting{"tok": {{Off: 2, Sid: 1}, {Off: 2, Sid: 1}, {Off: 9, Sid: 3}}}); err != nil {
		t.Fatal(err)
	}
	posts, err := readBucketToken(filepath.Join(tmp, "bucket.bin"), "tok", nil)
	if err != nil || len(posts) != 2 || posts[1].Off != 9 {
		t.Fatalf("readBucketToken posts=%#v err=%v", posts, err)
	}
	if none, tokenErr := readBucketToken(filepath.Join(tmp, "bucket.bin"), "none", nil); tokenErr != nil || none != nil {
		t.Fatalf("missing token posts=%#v err=%v", none, tokenErr)
	}
	if got := decodePostings([]byte{0x80}); len(got) != 0 {
		t.Fatalf("truncated postings=%#v", got)
	}
	if uvarintLen(1) != 1 || uvarintLen(128) != 2 {
		t.Fatal("uvarintLen edges failed")
	}

	var buf bytes.Buffer
	// os.NewFile(0, …) installs a finalizer that closes fd 0; once the GC
	// runs it, an unrelated os.Create later in the suite gets the recycled
	// descriptor and fails mid-write. Open the device instead.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	if _, err := readRecordAt(devNull, 1, newRecordTables()); err == nil && runtime.GOOS != "windows" {
		t.Fatal("readRecordAt bad seek returned nil")
	}
	if _, _, err := newLogWalk(&buf, newRecordTables(), 0).next(); !errors.Is(err, io.EOF) {
		t.Fatalf("empty log walk err=%v", err)
	}
	if _, err := decodeRecord([]byte{1, 'k'}, newRecordTables()); !IsCorrupt(err) {
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
	// Path names the store the row came from, which is what the stamp is
	// about: a row from another store must not raise this one's watermark.
	setStoreLastUpdated(files, map[string]SessionMeta{
		"x":     {Harness: "h", Path: "db", Updated: time.Unix(0, 7)},
		"other": {Harness: "h", Path: "another-db", Updated: time.Unix(0, 900)},
	}, "h", "db")
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
	if _, err := parseAppendedFile("", filepath.Join(tmp, "unknown.txt"), FileState{SafeSize: 10, Size: 1}, false); err != nil {
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
		manyManifest.Sessions[fmt.Sprintf("h:%d", i)] = SessionMeta{ID: strconv.Itoa(i), Harness: "h", Project: "p", Ord: uint32(i), Updated: time.Now().Add(time.Duration(i) * time.Second)}
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
	// A directory entry that ends before its posting-block length.
	b.Reset()
	b.Write(bucketMagic)
	b.WriteByte(1)
	b.WriteByte(1)
	b.WriteByte('a')
	shortFixed := filepath.Join(tmp, "short-fixed.bin")
	if err := os.WriteFile(shortFixed, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openBucketDir(shortFixed); err == nil || !IsCorrupt(err) {
		t.Fatalf("short fixed err=%v", err)
	}
	// A block length no file could hold must be rejected rather than
	// truncated into a uint32.
	b.Reset()
	b.Write(bucketMagic)
	b.WriteByte(1)
	b.WriteByte(1)
	b.WriteByte('a')
	var huge [binary.MaxVarintLen64]byte
	b.Write(huge[:binary.PutUvarint(huge[:], uint64(math.MaxUint32)+9)])
	hugeBlock := filepath.Join(tmp, "huge-block.bin")
	if err := os.WriteFile(hugeBlock, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openBucketDir(hugeBlock); err == nil || !IsCorrupt(err) {
		t.Fatalf("huge block err=%v", err)
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
	// A record is [keyID][pathID][role][time][text]; truncating at each
	// boundary must fail cleanly rather than return a half-built record.
	rtbl := testTables(Record{Key: "k", SourcePath: "src"})
	buf := binary.AppendUvarint(nil, rtbl.intern("k"))
	if _, err := decodeRecord(buf, rtbl); !IsCorrupt(err) {
		t.Fatalf("decode missing source err=%v", err)
	}
	buf = binary.AppendUvarint(buf, rtbl.intern("src"))
	if _, err := decodeRecord(buf, rtbl); !IsCorrupt(err) {
		t.Fatalf("decode missing role err=%v", err)
	}
	buf = binary.AppendUvarint(buf, rtbl.intern("role"))
	if _, err := decodeRecord(buf, rtbl); !IsCorrupt(err) {
		t.Fatalf("decode missing encoding flag err=%v", err)
	}
	buf = append(buf, recordRaw)
	if rec, err := decodeRecord(buf, rtbl); !IsCorrupt(err) ||
		rec.Key != "k" || rec.SourcePath != "src" || rec.Role != "role" {
		t.Fatalf("decode missing time rec=%#v err=%v", rec, err)
	}
	buf = binary.LittleEndian.AppendUint64(buf, uint64(time.Now().UnixNano()))
	if _, err := decodeRecord(buf, rtbl); !IsCorrupt(err) {
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
		want int
	}{
		{name: "cursor-db full", path: cursorDB, old: FileState{Size: 10, SafeSize: 20}},
		{name: "cursor-db since", path: cursorDB, old: FileState{LastUpdated: time.Now().UnixNano()}},
		{name: "gemini default", path: gemini},
		{name: "antigravity default", path: ag, want: 1},
		{name: "unknown default", path: unknown},
	} {
		t.Run("appended "+tc.name, func(t *testing.T) {
			got, err := parseAppendedFile("", tc.path, tc.old, false)
			if err != nil || len(got) != tc.want {
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
		// The caveat is not a title, and with no other user turn the assistant's
		// first sentence fills in rather than leaving a blank line (#692).
		{name: "caveat", s: model.Session{Messages: []model.Message{{Role: "user", Text: "Caveat: noisy"}, {Role: "assistant", Text: "skip"}}}, want: "skip"},
		{name: "only tool output", s: model.Session{Messages: []model.Message{{Role: "tool-output", Text: "panic: boom"}}}, want: "tool output: panic: boom"},
		{name: "nothing worth naming", s: model.Session{Messages: []model.Message{{Role: "files", Text: "/repo/pool.go"}}}, want: ""},
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
		{1, 1},
		{1, 1, 1},
		{1, 1, 1, recordRaw},
	} {
		if _, err := decodeRecord(data, newRecordTables()); !IsCorrupt(err) {
			t.Fatalf("decodeRecord(%v) err=%v", data, err)
		}
	}
	// An encoding byte the reader does not know is corruption, not EOF.
	if _, err := decodeRecord([]byte{1, 1, 1, 9}, newRecordTables()); !IsCorrupt(err) {
		t.Fatalf("unknown encoding flag err=%v, want a corruption error", err)
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
	if _, err = readRecordAt(f, 99, newRecordTables()); !errors.Is(err, io.EOF) {
		t.Fatalf("readRecordAt bad offset err=%v", err)
	}

	if got, postErr := intersectSubstringPostings(filepath.Join(tmp, "missing-index"), []string{"nope"}); postErr != nil || got != nil {
		t.Fatalf("missing substring postings=%#v err=%v", got, postErr)
	}
	dir := filepath.Join(tmp, "substring-index")
	if err = os.MkdirAll(filepath.Join(dir, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, postErr := intersectSubstringPostings(dir, nil); postErr != nil || got != nil {
		t.Fatalf("empty substring postings=%#v err=%v", got, postErr)
	}
	bp := filepath.Join(dir, "buckets", bucket("talpha")+".bin")
	if err = writeBucket(bp, map[string][]posting{
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
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
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
	if err := updateIndex(dir, "", m.Scope, m.Files, false, nil); err != nil || lastIngestFiles.Load() != 0 {
		t.Fatalf("fresh update err=%v last=%d", err, lastIngestFiles.Load())
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
	if _, _, _, err := appendPrepared(filepath.Join(tmp, "missing-index"), "", "", old, old.Files, old.Files, nil); err == nil {
		t.Fatal("appendPrepared missing dir returned nil")
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
	if got, err := parseAppendedFile("", filepath.Join(os.Getenv("DEJA_CURSOR_ROOT"), "state.vscdb"), FileState{LastUpdated: time.Now().UnixNano()}, false); err != nil || got != nil {
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

// Coverage multiplies a session's score by how much of the query it covers, and
// it used to count every word the informative gate let through. That gate is
// deliberately generous: it decides whether a term is worth speaking about at
// all, and takes the more forgiving of its two readings of "rare" so a subject
// word is never dismissed as filler. The two readings come apart on the shape a
// real store is full of — a word sitting in one message of each of many long
// sessions is ordinary counted in sessions and rare counted in messages.
//
// Paying for coverage on the forgiving reading pays a session for the words a
// question happens to be phrased with. "Can you suggest a hotel for my trip" is
// four such words and one that matters.
func TestCoverageIsPaidOnTheWordsThatIdentify(t *testing.T) {
	// Session 1 covers three ordinary words and nothing else; session 2 carries
	// the one word that identifies.
	if got := coverageCount(3, 0, 1); got != 0 {
		t.Errorf("a session covering only ordinary words was still paid for "+
			"covering %d of them", got)
	}
	if got := coverageCount(1, 1, 1); got != 1 {
		t.Errorf("the session carrying the identifying word lost its coverage: %d", got)
	}
}

// A question made entirely of ordinary words has nothing to count strictly, and
// counting them is all there is: dropping to zero coverage everywhere would
// leave the ranking with one less signal exactly where it is thinnest.
func TestCoverageKeepsTheGenerousCountWhenNothingIdentifies(t *testing.T) {
	if got := coverageCount(3, 0, 0); got != 3 {
		t.Errorf("the generous count was dropped on a query with no identifying "+
			"word: %d", got)
	}
	if got := coverageCount(2, 0, 0); got != 2 {
		t.Errorf("the generous count was dropped on a query with no identifying "+
			"word: %d", got)
	}
}

// A *os.File closed out from under a recordWriter (or handed to it already
// closed) makes every write path fail its underlying I/O: this exercises
// newRecordWriter's Seek error, write()'s two Write error returns, Close()'s
// Flush and file-Close error returns, writeRecord's Seek/Write errors, and
// readRecordAt's Seek error -- all otherwise unreachable since a healthy
// *os.File's small buffered writes never fail in a hermetic test.
func TestRecordIOErrorsViaClosedFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "rec.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err = newRecordWriter(f, newRecordTables()); err == nil {
		t.Fatal("newRecordWriter on closed file returned nil error")
	}

	// Build a recordWriter directly (same package) with a 1-byte buffer so
	// even the 4-byte header write forces an immediate flush to the closed
	// underlying file, hitting write()'s first Write error return.
	f2, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{Key: "k", SourcePath: "s", Role: "user", Text: "hello world this is long enough to overflow", Time: time.Now()}
	if err = f2.Close(); err != nil {
		t.Fatal(err)
	}
	rw := &recordWriter{f: f2, w: bufio.NewWriterSize(f2, 1), tables: newRecordTables()}
	if _, err = rw.write(rec); err == nil {
		t.Fatal("write on closed file returned nil error")
	}
	if err = rw.Close(); err == nil {
		t.Fatal("Close on already-closed file returned nil error")
	}

	// A bigger buffer means the small header write is only buffered, not
	// flushed, so the body write is what actually fails.
	f2b, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = f2b.Close(); err != nil {
		t.Fatal(err)
	}
	rwBig := &recordWriter{f: f2b, w: bufio.NewWriterSize(f2b, 8), tables: newRecordTables()}
	if _, err = rwBig.write(rec); err == nil {
		t.Fatal("write (body) on closed file returned nil error")
	}

	// writeRecord's Seek succeeds on a read-only fd; only the subsequent
	// Write fails, since the fd was never opened for writing.
	if err = os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f3, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f3.Close()
	if _, err = writeRecord(f3, rec, newRecordTables()); err == nil {
		t.Fatal("writeRecord on a read-only file returned nil error")
	}

	f4, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f4.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecordAt(f4, 0, newRecordTables()); err == nil {
		t.Fatal("readRecordAt on closed file returned nil error")
	}
}

// writeGobAtomic must clean up its temp file and surface the error when the
// value cannot be gob-encoded (e.g. a func value).
func TestWriteGobAtomicEncodeError(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.gob")
	if err := writeGobAtomic(p, func() {}); err == nil {
		t.Fatal("writeGobAtomic with unencodable value returned nil error")
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("writeGobAtomic left temp file behind: err=%v", err)
	}
}

func TestEncodeDecodePostingsEdgeCases(t *testing.T) {
	if got := encodePostings(nil); got != nil {
		t.Fatalf("encodePostings(nil)=%v want nil", got)
	}
	// First varint (offset delta) decodes fully; second varint (sid) is a
	// truncated continuation byte with nothing following.
	got := decodePostings([]byte{1, 0x80})
	if len(got) != 0 {
		t.Fatalf("decodePostings truncated sid = %#v, want empty", got)
	}
}

func TestRecordsIntactMissingFile(t *testing.T) {
	tmp := t.TempDir()
	if !recordsIntact(tmp, Manifest{RecordsSize: 0}) {
		t.Fatal("recordsIntact false for RecordsSize<=0")
	}
	if recordsIntact(tmp, Manifest{RecordsSize: 10}) {
		t.Fatal("recordsIntact true when records.bin is missing")
	}
}

// intersectSubstringPostings ignores non-.bin entries and subdirectories in
// buckets/, but a bucket it cannot open or a posting block that will not read is
// corruption: it surfaces for the recovery path to rebuild rather than being
// skipped into a silent under-match. A genuine ReadDir failure surfaces too.
func TestIntersectSubstringPostingsSkipBranches(t *testing.T) {
	// Benign non-bucket entries beside a valid bucket are ignored and the real
	// match still comes back.
	func() {
		tmp := t.TempDir()
		bucketsDir := filepath.Join(tmp, "buckets")
		if err := os.MkdirAll(filepath.Join(bucketsDir, "subdir"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bucketsDir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeBucket(filepath.Join(bucketsDir, "good.bin"), map[string][]posting{"talpha": {{Off: 1, Sid: 1}}}); err != nil {
			t.Fatal(err)
		}
		got, err := intersectSubstringPostings(tmp, []string{"alp"})
		if err != nil {
			t.Fatalf("benign skips surfaced an error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("substring match lost through the skip branches: %#v", got)
		}
	}()

	// A bucket that will not open is corruption, surfaced now.
	func() {
		tmp := t.TempDir()
		bucketsDir := filepath.Join(tmp, "buckets")
		if err := os.MkdirAll(bucketsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bucketsDir, "corrupt.bin"), []byte("bad"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := intersectSubstringPostings(tmp, []string{"alp"}); !IsCorrupt(err) {
			t.Fatalf("unopenable bucket err=%v, want a corruption error", err)
		}
	}()

	// A posting block truncated past EOF is corruption, surfaced now.
	func() {
		tmp := t.TempDir()
		bucketsDir := filepath.Join(tmp, "buckets")
		if err := os.MkdirAll(bucketsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		goodPath := filepath.Join(bucketsDir, "good.bin")
		if err := writeBucket(goodPath, map[string][]posting{"talpha": {{Off: 1, Sid: 1}}}); err != nil {
			t.Fatal(err)
		}
		// Truncate right after the directory section so openBucketDir still
		// succeeds but the ReadAt into the postings payload runs past EOF.
		entries, gf, err := openBucketDir(goodPath)
		if err != nil {
			t.Fatal(err)
		}
		dataStart := entries[0].off
		_ = gf.Close()
		if err := os.Truncate(goodPath, int64(dataStart)); err != nil { //nolint:gosec // dataStart is a small offset
			t.Fatal(err)
		}
		if _, err := intersectSubstringPostings(tmp, []string{"alp"}); !IsCorrupt(err) {
			t.Fatalf("truncated posting block err=%v, want a corruption error", err)
		}
	}()

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		unreadableParent := filepath.Join(t.TempDir(), "unreadable-parent")
		unreadableBuckets := filepath.Join(unreadableParent, "buckets")
		if err := os.MkdirAll(unreadableBuckets, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(unreadableBuckets, 0o300); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(unreadableBuckets, 0o755) }()
		if _, err := intersectSubstringPostings(unreadableParent, []string{"x"}); err == nil {
			t.Fatal("intersectSubstringPostings on unreadable buckets dir returned nil")
		}
	}
}

// lockDir must surface an OpenFile failure when the lock file's parent
// directory exists but cannot accept a new file.
func TestLockDirOpenFileFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "idx")
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(parent, 0o755) }()
	if _, err := lockDir(dir); err == nil {
		t.Fatal("lockDir with read-only parent returned nil error")
	}
}

// exportRecords must resolve dir=="", fall back to the record's Key when
// SourcePath is empty, and skip a record whose Key has no manifest entry
// rather than exporting a session-less orphan.
func TestExportDefaultDirSourceFallbackAndOrphanSkip(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := DefaultDir()
	writeTinyIndex(t, dir)
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	// Append two records directly (same-package, bypassing session ingest):
	// one with a real Key but empty SourcePath (must fall back to r.Key as
	// the export source grouping), one with a Key absent from the manifest
	// (must be skipped as an orphan).
	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Appending continues the index's own string table; a fresh one would
	// hand id 0 to a new string and repoint records that already use it.
	atbl := mustTables(t, dir)
	rw, err := newRecordWriter(rf, atbl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rw.write(Record{Key: "claude:s1", SourcePath: "", Role: "user", Text: "no source path text", Time: base}); err != nil {
		t.Fatal(err)
	}
	if _, err = rw.write(Record{Key: "ghost:nope", SourcePath: "orphan-source", Role: "user", Text: "orphan text", Time: base}); err != nil {
		t.Fatal(err)
	}
	if err = rw.Close(); err != nil {
		t.Fatal(err)
	}
	am, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	am.RecordStrings = atbl.strs
	if err = writeManifest(dir, am); err != nil {
		t.Fatal(err)
	}

	n, err := Export("", filepath.Join(tmp, "batch-out"))
	if err != nil {
		t.Fatalf("Export('') err=%v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(tmp, "batch-out", "*.jsonl"))
	var all string
	var allSb283 strings.Builder
	for _, p := range matches {
		b, _ := os.ReadFile(p)
		allSb283.Write(b)
	}
	all += allSb283.String()
	if strings.Contains(all, "orphan text") {
		t.Fatal("orphan record with no manifest session was exported")
	}
	if !strings.Contains(all, "no source path text") {
		t.Fatal("record with empty SourcePath (key fallback) was not exported")
	}
	if n == 0 {
		t.Fatal("Export('') exported nothing from the tiny fixture index")
	}
}

// Import must resolve dir=="", fail cleanly when the destination path is a
// regular file (initEmptyIndex's MkdirAll fails), and surface a bad glob
// pattern instead of silently returning zero.
func TestImportDefaultDirAndInitAndGlobErrors(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	emptyBatch := filepath.Join(tmp, "empty-batch")
	if err := os.MkdirAll(emptyBatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := Import("", emptyBatch); err != nil || n != 0 {
		t.Fatalf("Import('') n=%d err=%v", n, err)
	}

	blockedDir := filepath.Join(tmp, "blocked-index")
	if err := os.WriteFile(blockedDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(blockedDir, emptyBatch); err == nil {
		t.Fatal("Import into a file path returned nil error")
	}

	badPattern := filepath.Join(tmp, "batch-with-[unclosed")
	if _, err := Import(filepath.Join(tmp, "another-index"), badPattern); err == nil {
		t.Fatal("Import with malformed glob pattern returned nil error")
	}
}

// initEmptyIndex must surface an os.Create failure when records.bin's path
// is blocked by a pre-existing directory.
func TestInitEmptyIndexRecordsCreateFailure(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "records.bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := initEmptyIndex(dir); err == nil {
		t.Fatal("initEmptyIndex with records.bin as a directory returned nil error")
	}
}

// readSyncFile must surface a bufio.Scanner error other than io.EOF: a
// single line larger than the scanner's 8MiB max token size trips
// bufio.ErrTooLong.
func TestReadSyncFileScannerTooLong(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "huge.jsonl")
	huge := make([]byte, 9*1024*1024)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(p, huge, 0o644); err != nil {
		t.Fatal(err)
	}
	err := readSyncFile(p, func(SyncRecord) error { return nil })
	if err == nil {
		t.Fatal("readSyncFile with an oversized line returned nil error")
	}
}

func TestLoadDirectCall(t *testing.T) {
	hermeticIndexEnv(t)
	if got := load(""); got != nil {
		t.Fatalf("load('') on an empty hermetic env = %#v, want nil/empty", got)
	}
}

func TestWriteGobCreateError(t *testing.T) {
	tmp := t.TempDir()
	if err := writeGob(filepath.Join(tmp, "missing-parent", "x.gob"), map[string]int{"a": 1}); err == nil {
		t.Fatal("writeGob with missing parent dir returned nil error")
	}
}

// lastCompleteLineOffset's ReadAt must fail cleanly (return size) when asked
// to read more than the file actually holds.
func TestLastCompleteLineOffsetReadAtError(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "short.txt")
	if err := os.WriteFile(p, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lastCompleteLineOffset(p, 1<<20); got != 1<<20 {
		t.Fatalf("lastCompleteLineOffset with oversized size = %d, want %d", got, 1<<20)
	}
}

func TestDecodeRecordMissingRole(t *testing.T) {
	buf := appendField(nil, "key")
	buf = appendField(buf, "src")
	if _, err := decodeRecord(buf, newRecordTables()); err == nil {
		t.Fatal("decodeRecord missing role field returned nil error")
	}
}

// eachRecord's readRecord can surface a genuine I/O error distinct from a
// clean EOF/truncation: opening a directory as if it were records.bin means
// the very first Read returns "is a directory", not io.EOF.
func TestEachRecordNonEOFReadError(t *testing.T) {
	tmp := t.TempDir()
	dirAsFile := filepath.Join(tmp, "not-a-file")
	if err := os.MkdirAll(dirAsFile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := eachRecord(dirAsFile, newRecordTables(), func(Record) {}); err == nil {
		t.Fatal("eachRecord over a directory returned nil error")
	}
}

func TestIntersectPostingsEmptyKeys(t *testing.T) {
	if got, err := intersectPostings(t.TempDir(), nil); err != nil || got != nil {
		t.Fatalf("intersectPostings(nil keys)=%#v err=%v", got, err)
	}
}

// readBucket and readBucketToken must surface a ReadAt failure when a
// bucket's declared postings payload runs past the truncated file's end.
func TestReadBucketAndTokenReadAtError(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "b.bin")
	if err := writeBucket(p, map[string][]posting{"talpha": {{Off: 1, Sid: 1}}}); err != nil {
		t.Fatal(err)
	}
	entries, f, err := openBucketDir(p)
	if err != nil {
		t.Fatal(err)
	}
	dataStart := entries[0].off
	_ = f.Close()
	if err := os.Truncate(p, int64(dataStart)); err != nil { //nolint:gosec // dataStart is a small offset
		t.Fatal(err)
	}
	if _, err := readBucket(p); err == nil {
		t.Fatal("readBucket with truncated payload returned nil error")
	}
	if _, err := readBucketToken(p, "talpha", nil); err == nil {
		t.Fatal("readBucketToken with truncated payload returned nil error")
	}
}

// importedSessions must skip a sync-import-tagged record whose session key
// has no manifest entry (a manifest edited out from under it).
func TestImportedSessionsOrphanRecordSkipped(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	rf, err := os.Create(filepath.Join(tmp, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(rf, newRecordTables())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "claude:ghost", SourcePath: syncImportPath, Role: "user", Text: "orphan", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Sessions: map[string]SessionMeta{}}
	if err := writeManifest(tmp, m); err != nil {
		t.Fatal(err)
	}
	got := importedSessions(tmp)
	if len(got.sessions) != 0 {
		t.Fatalf("importedSessions kept an orphan record: %#v", got.sessions)
	}
}

// parseChangedFile/parseAppendedFile must fall back to a full opencode parse
// (not the *Since variant) when the old FileState carries no LastUpdated.
func TestParseOpencodeFallbackWithoutLastUpdated(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	db := os.Getenv("DEJA_OPENCODE_DB")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = tmp
	if _, err := parseChangedFile("", db, FileState{}); err != nil {
		t.Fatalf("parseChangedFile opencode fallback err=%v", err)
	}
	if _, err := parseAppendedFile("", db, FileState{}, false); err != nil {
		t.Fatalf("parseAppendedFile opencode fallback err=%v", err)
	}
	if _, err := parseAppendedFile("", db, FileState{LastUpdated: time.Now().UnixNano()}, false); err != nil {
		t.Fatalf("parseAppendedFile opencode since-branch err=%v", err)
	}
}

func TestScanRecordsMetaMissingAndOpenError(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	rf, err := os.Create(filepath.Join(tmp, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(rf, newRecordTables())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rw.write(Record{Key: "nope:1", SourcePath: "s", Role: "user", Text: "x", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = rw.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := scanRecords(tmp, Manifest{Sessions: map[string]SessionMeta{}}, search.Options{}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("scanRecords with unknown session key=%#v err=%v", got, err)
	}
	if _, err := scanRecords(t.TempDir(), Manifest{}, search.Options{}, []int64{0}); err == nil {
		t.Fatal("scanRecords with offsets over a missing records.bin returned nil")
	}
}

func TestCutPostingsBySessionTieBreak(t *testing.T) {
	updated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := Manifest{Sessions: map[string]SessionMeta{
		"h:1": {ID: "1", Harness: "h", Ord: 1, Updated: updated},
		"h:2": {ID: "2", Harness: "h", Ord: 2, Updated: updated},
	}}
	posts := []posting{{Off: 1, Sid: 1}, {Off: 2, Sid: 2}}
	got := cutPostingsBySession(posts, m, search.Options{})
	if len(got) != 2 {
		t.Fatalf("cutPostingsBySession tie-break=%#v", got)
	}
}

// The full-replace path of updateIndex (a harness never eligible for
// incremental append, e.g. aider) must skip a file whose parse fails,
// keeping its FileState for an existing entry and dropping it entirely for
// a brand-new one, and must surface a blocked staging directory.
func TestUpdateIndexReplacePathParseFailureSkipBranches(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	aiderRoot := filepath.Join(tmp, "aider")
	t.Setenv("DEJA_AIDER_ROOTS", aiderRoot)
	aiderFile := filepath.Join(aiderRoot, ".aider.chat.history.md")
	write(t, aiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### aiderbase question\n\naiderbase answer\n")

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Grow the file (so it's picked up as "changed") but make it unreadable;
	// aider is not incremental-eligible, so this drives the replace path's
	// parseChangedFile-error branch for a file already known to old.Files.
	if err := os.WriteFile(aiderFile, []byte(strReplaceGrow()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(aiderFile, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure with unreadable known aider file err=%v", err)
	}
	_ = os.Chmod(aiderFile, 0o644)
	if ss, err := Search(dir, search.Options{Query: "aiderbase", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("original aider content must survive a skipped re-parse: ss=%#v err=%v", ss, err)
	}

	// A brand-new file that's unreadable from the moment currentFiles()
	// notices it: old.Files has no entry for it, so the skip branch takes
	// the delete(files, p) path instead of restoring an old FileState.
	newAiderFile := filepath.Join(aiderRoot, "second", ".aider.chat.history.md")
	write(t, newAiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### unreadable q\n\na\n")
	if err := os.Chmod(newAiderFile, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(newAiderFile, 0o644) }()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure with new unreadable aider file err=%v", err)
	}

	// Blocked own staging dir: canAppendIncremental is false for aider, so a
	// content-changed aider file drives the replace path's MkdirAll.
	blockedParent := filepath.Join(tmp, "blocked-parent")
	if err := os.MkdirAll(blockedParent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir2 := filepath.Join(blockedParent, "idx2")
	if err := Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aiderFile, []byte("# aider chat started at 2026-01-01 00:00:00\n\n#### grownmore q\n\na\nmore content to grow the file size further\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedParent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(blockedParent, 0o755) }()
	if err := Ensure(dir2, "", false, nil); err == nil {
		t.Fatal("Ensure replace-path with blocked own staging dir returned nil")
	}
}

func strReplaceGrow() string {
	return "# aider chat started at 2026-01-01 00:00:00\n\n#### aiderbase question\n\naiderbase answer\n\n#### more\n\nmore\n"
}

// updateIndex's replace path must silently drop a raw record with an empty
// SourcePath and one whose Key has no manifest entry, rather than exporting
// or resurrecting a ghost session.
func TestUpdateIndexReplacePathOrphanRecordsDropped(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	aiderRoot := filepath.Join(tmp, "aider")
	t.Setenv("DEJA_AIDER_ROOTS", aiderRoot)
	aiderFile := filepath.Join(aiderRoot, ".aider.chat.history.md")
	write(t, aiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### keep question\n\nkeep answer\n")

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	rf, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := newRecordWriter(rf, newRecordTables())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "claude:has-empty-source", SourcePath: "", Role: "user", Text: "empty source text", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.write(Record{Key: "ghost:nowhere", SourcePath: "some-untracked-source", Role: "user", Text: "ghost text", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}

	// A second aider file addition (aider is never incremental-eligible)
	// forces the replace path, which rescans dir/records.bin including our
	// two injected orphan records.
	write(t, filepath.Join(aiderRoot, "second", ".aider.chat.history.md"),
		"# aider chat started at 2026-01-01 01:00:00\n\n#### keep2 question\n\nkeep2 answer\n")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, search.Options{Query: "empty source text", All: true}); err != nil || len(ss) != 0 {
		t.Fatalf("record with empty SourcePath and no session was resurrected: %#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "ghost text", All: true}); err != nil || len(ss) != 0 {
		t.Fatalf("orphan ghost record was resurrected: %#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "keep answer", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("legitimate record lost during replace path: %#v err=%v", ss, err)
	}
}

// Two messages with identical role, timestamp, and text within one rebuild
// pass must be deduplicated by seenMsgs, not indexed twice.
func TestRebuildExactDuplicateMessageDeduped(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	// Two separate session files, same session id, one message that is a
	// byte-for-byte duplicate (role+timestamp+text) of the other.
	write(t, filepath.Join(claudeRoot, "p", "a.jsonl"), claudeLine("dupmsg", "2026-01-02T03:04:05Z", "exact dup needle"))
	write(t, filepath.Join(claudeRoot, "p", "b.jsonl"), claudeLine("dupmsg", "2026-01-02T03:04:05Z", "exact dup needle"))
	dir := filepath.Join(tmp, "idx")
	if err := rebuild(dir, "claude", "", currentFiles("claude"), nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "exact dup needle", All: true})
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("exact duplicate message not deduped: ss=%#v err=%v", ss, err)
	}
}

func TestEnsureDefaultDirAndInternalRecordsIntactForce(t *testing.T) {
	hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claudeRoot, "p", "s.jsonl"), claudeLine("s1", "2026-01-02T03:04:05Z", "ensuredefault needle"))
	if err := Ensure("", "", false, nil); err != nil {
		t.Fatalf("Ensure('') err=%v", err)
	}
	dir := DefaultDir()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.RecordsSize <= 0 {
		t.Skip("no size stamp to truncate against")
	}
	if err := os.Truncate(filepath.Join(dir, "records.bin"), m.RecordsSize/2); err != nil {
		t.Fatal(err)
	}
	// Ensure() (unlike EnsureForSearch) has no outer recordsIntact gate: its
	// own internal check inside updateIndex must catch the truncated file
	// and force a rebuild.
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure over truncated records.bin err=%v", err)
	}
	if ss, err := Search(dir, search.Options{Query: "ensuredefault needle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("not healed after internal recordsIntact force: ss=%#v err=%v", ss, err)
	}
}

// EnsureForSearch's incremental-update branch (manifest fresh in version and
// scope, but files changed) must wrap a genuine updateIndex failure.
func TestEnsureForSearchWrapsUpdateIndexError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	aiderRoot := filepath.Join(tmp, "aider")
	t.Setenv("DEJA_AIDER_ROOTS", aiderRoot)
	aiderFile := filepath.Join(aiderRoot, ".aider.chat.history.md")
	write(t, aiderFile, "# aider chat started at 2026-01-01 00:00:00\n\n#### base q\n\nbase a\n")

	parent := filepath.Join(tmp, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "idx")
	o := search.Options{Query: "base"}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	// Grow the aider file (aider is never incremental-eligible, so this
	// drives updateIndex's non-append replace path) and block its own
	// staging dir by making the index's parent read-only.
	if err := os.WriteFile(aiderFile, []byte("# aider chat started at 2026-01-01 00:00:00\n\n#### base q\n\nbase a\n\n#### more\n\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(parent, 0o755) }()
	if err := EnsureForSearch(dir, o, false, nil); err == nil {
		t.Fatal("EnsureForSearch with blocked updateIndex replace path returned nil")
	}
}

// A query with zero exact-token postings must fall through to the substring
// expansion, and a real (non-NotExist) failure there must surface as a
// wrapped error; a query that does have postings but whose sessions are all
// filtered out by cutPostingsBySession must return an empty result, not an
// error.
func TestSearchSubstringPostingsDirErrorAndEmptyAfterCut(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)

	if err := os.Chmod(filepath.Join(dir, "buckets"), 0o300); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "buckets"), 0o755) }()
	if _, err := Search(dir, search.Options{Query: "nonexistenttoken12345"}); err == nil {
		t.Fatal("Search substring fallback over unreadable buckets dir returned nil")
	}
	_ = os.Chmod(filepath.Join(dir, "buckets"), 0o755)

	if ss, err := Search(dir, search.Options{Query: "alpha", Harness: "does-not-exist"}); err != nil || ss != nil {
		t.Fatalf("Search filtered to nothing by cutPostingsBySession = %#v err=%v", ss, err)
	}
}

// appendPrepared's own defensive branches: a nil Sessions map must be
// initialized, and a changed path absent from old.Files (violating the
// normal canAppendIncremental invariant, reachable only via this direct
// same-package call) must be dropped from Files rather than panicking.
func TestAppendIncrementalDirectDefensiveBranches(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "idx", "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "idx", "records.bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A recognized (claude) path so parseAppendedFile actually attempts I/O
	// and fails, rather than silently no-op'ing on an unrecognized harness.
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	unknown := filepath.Join(claudeRoot, "p", "unknown.jsonl")
	if err := os.MkdirAll(filepath.Dir(unknown), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unknown, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(unknown, 0o644) }()
	old := Manifest{Files: map[string]FileState{}, Sessions: nil}
	changed := map[string]FileState{unknown: {Path: unknown, Size: 1}}
	files := map[string]FileState{unknown: {Path: unknown, Size: 1}}
	if _, _, _, err := appendPrepared(filepath.Join(tmp, "idx"), "", "", old, files, changed, nil); err != nil {
		t.Fatalf("appendPrepared defensive branches err=%v", err)
	}
	if _, ok := files[unknown]; ok {
		t.Fatalf("changed path absent from old.Files should have been dropped: %#v", files)
	}
}

// A claude file that grows but becomes unreadable mid-pass must keep its old
// FileState so the next run retries it, rather than losing track of it.
func TestAppendIncrementalUnreadableGrownFileKeepsOldState(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "keepold needle"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(claudeLine("s1", "2026-01-02T03:05:05Z", "keepold second")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Chmod(file, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(file, 0o644) }()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("Ensure over unreadable grown claude file err=%v", err)
	}
	_ = os.Chmod(file, 0o644)
	if ss, err := Search(dir, search.Options{Query: "keepold needle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("original content lost when append retry deferred: %#v err=%v", ss, err)
	}
}

// A second, brand-new session id appended to an already-tracked claude file
// drives appendIncremental's new-meta branch (meta.ID=="") along with its
// Started-is-zero and Title-is-empty carry-in checks.
func TestAppendIncrementalNewSessionMidFile(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "midfile first session"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A distinct sessionId appended to the same file: the append parses a
	// brand-new session with no prior manifest entry.
	if _, err = f.WriteString(claudeLine("s2-brand-new", "2026-01-02T04:00:00Z", "midfile second session")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "midfile second session", All: true})
	if err != nil || len(ss) != 1 {
		t.Fatalf("new mid-file session not indexed: %#v err=%v", ss, err)
	}
	if ss[0].ID != "s2-brand-new" {
		t.Fatalf("new session meta not populated: %#v", ss[0])
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:s2-brand-new"]
	if !ok || meta.Title == "" || meta.Started.IsZero() {
		t.Fatalf("new session's Title/Started not carried in via metaForSession: %#v", meta)
	}
}

// appendIncremental must surface a writeBucketsConcurrent failure when a
// brand-new bucket file can't be created, and a writeManifest failure when
// the index dir itself can't accept a new temp file, distinctly.
func TestAppendIncrementalBucketAndManifestWriteErrors(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")

	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "bucketerr existingtoken"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "buckets"), 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "buckets"), 0o755) }()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// A brand-new token forces a brand-new bucket file, which requires
	// creating a new entry in the now read-only buckets directory.
	if _, err = f.WriteString(claudeLine("s1", "2026-01-02T03:06:05Z", "zzzneverseenbeforetoken")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err = Ensure(dir, "", false, nil); err == nil {
		t.Fatal("Ensure with unwritable buckets dir for a new token returned nil")
	}
	_ = os.Chmod(filepath.Join(dir, "buckets"), 0o755)

	file2 := filepath.Join(claudeRoot, "p2", "s2.jsonl")
	write(t, file2, claudeLine("s2", "2026-01-02T03:04:05Z", "manifesterr existingtoken"))
	dir2 := filepath.Join(tmp, "idx2")
	if err = Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Block the manifest tmp path instead of chmod: lockDir re-tightens the
	// directory to 0700, so permission games no longer stick.
	if err = os.MkdirAll(filepath.Join(dir2, "manifest.gob.tmp", "block"), 0o700); err != nil {
		t.Fatal(err)
	}
	f2, err := os.OpenFile(file2, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Re-use the exact same token: no new bucket file is needed, so buckets
	// writes succeed even though buckets itself lives under a read-only
	// parent; only the dir-level manifest write should fail.
	if _, err := f2.WriteString(claudeLine("s2", "2026-01-02T03:06:05Z", "manifesterr existingtoken")); err != nil {
		t.Fatal(err)
	}
	_ = f2.Close()
	if err := Ensure(dir2, "", false, nil); err == nil {
		t.Fatal("Ensure with unwritable index dir for manifest returned nil")
	}
}

// Import must surface a genuine readManifest failure distinct from the
// "no manifest yet" initEmptyIndex path.
func TestImportManifestCorruptAfterHasManifestTrue(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("truncated"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyBatch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(emptyBatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dir, emptyBatch); err == nil {
		t.Fatal("Import over a corrupt (but present) manifest returned nil error")
	}
}

// A corrupt bucket file colliding with an imported token's bucket must
// surface through Import -> appendImportedRecords -> loadBucket as a
// non-NotExist error, not a silent empty result.
func TestImportCorruptBucketPropagatesError(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	tok := "zzimportcollision"
	bpath := filepath.Join(dir, "buckets", bucket("t"+tok)+".bin")
	if err := os.WriteFile(bpath, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{
		{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: tok, Time: time.Now()},
	})
	if _, err := Import(dir, batch); err == nil {
		t.Fatal("Import colliding with a corrupt bucket returned nil error")
	}
}

// appendImportedRecords must fail cleanly when its records.bin path doesn't
// exist and can't be created (a missing parent directory).
func TestAppendImportedRecordsMissingDir(t *testing.T) {
	tmp := t.TempDir()
	m := &Manifest{Sessions: map[string]SessionMeta{}}
	missing := filepath.Join(tmp, "no-such-dir", "nested")
	if err := appendImportedRecords(missing, m, map[string][]Record{"k": {{Key: "k", Text: "x"}}}, map[string]SessionMeta{"k": {ID: "k"}}); err == nil {
		t.Fatal("appendImportedRecords with a missing parent dir returned nil error")
	}
}

// Importing an older-timestamped batch for a session already known from a
// newer batch must keep the newer Updated time (old.Updated.After branch).
func TestImportOlderBatchKeepsNewerUpdated(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	newer := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)
	batch1 := filepath.Join(tmp, "batch1")
	writeSyncBatch(t, batch1, []SyncRecord{{Harness: "claude", SessionID: "ord1", Project: "p", Role: "user", Text: "newer import msg", Time: newer}})
	if _, err := Import(dir, batch1); err != nil {
		t.Fatal(err)
	}
	batch2 := filepath.Join(tmp, "batch2")
	writeSyncBatch(t, batch2, []SyncRecord{{Harness: "claude", SessionID: "ord1", Project: "p", Role: "assistant", Text: "older import msg", Time: older}})
	if _, err := Import(dir, batch2); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "claude:" + ImportedSessionID("claude", "ord1")
	if !m.Sessions[key].Updated.Equal(newer) {
		t.Fatalf("Updated regressed to the older batch: %#v", m.Sessions[key])
	}
}

// Import's appendImportedRecords must surface a bucket-write failure when
// buckets/ can't accept a brand-new file.
func TestImportBlockedBucketsDirWriteError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "buckets"), 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "buckets"), 0o755) }()
	batch := filepath.Join(tmp, "batch")
	writeSyncBatch(t, batch, []SyncRecord{{Harness: "claude", SessionID: "s1", Project: "p", Role: "user", Text: "blockedbucket text", Time: time.Now()}})
	if _, err := Import(dir, batch); err == nil {
		t.Fatal("Import with unwritable buckets dir returned nil error")
	}
}

// exportRecords must surface a records.bin read failure distinct from the
// manifest read, a write failure into a blocked outDir, and a final
// writeManifest failure when the index dir itself is blocked.
func TestExportRecordsErrorBranches(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)

	dir := filepath.Join(tmp, "idx")
	writeTinyIndex(t, dir)
	if err := os.Remove(filepath.Join(dir, "records.bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(dir, filepath.Join(tmp, "out1")); err == nil {
		t.Fatal("Export with missing records.bin returned nil error")
	}

	dir2 := filepath.Join(tmp, "idx2")
	writeTinyIndex(t, dir2)
	outDir := filepath.Join(tmp, "out2")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(outDir, 0o755) }()
	if _, err := Export(dir2, outDir); err == nil {
		t.Fatal("Export into a read-only outDir returned nil error")
	}

	dir3 := filepath.Join(tmp, "idx3")
	writeTinyIndex(t, dir3)
	// A directory squatting on manifest.gob.tmp makes writeGobAtomic fail
	// regardless of directory permissions (lockDir re-tightens those). The
	// watermark commit writes only manifest.gob, never sessions.gob.
	if err := os.MkdirAll(filepath.Join(dir3, "manifest.gob.tmp", "block"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Export(dir3, filepath.Join(tmp, "out3")); err == nil {
		t.Fatal("Export with a blocked manifest tmp returned nil error")
	}
}

// EnsureForSearch must resolve dir=="" through DefaultDir, and a second call
// against an already-fresh, intact index must take the cheap "return nil"
// path instead of re-scanning or re-indexing.
func TestEnsureForSearchDefaultDirAndFastPath(t *testing.T) {
	hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(claudeRoot, "p")
	write(t, filepath.Join(proj, "s.jsonl"),
		claudeLine("s1", "2026-01-02T03:04:05Z", "fastpathneedle"))

	o := search.Options{Query: "fastpathneedle"}
	if err := EnsureForSearch("", o, false, nil); err != nil {
		t.Fatalf("EnsureForSearch dir=='' err=%v", err)
	}
	// Second call: nothing changed, force=false -> manifestFresh+recordsIntact
	// must short-circuit to nil without touching updateIndex/rebuild.
	if err := EnsureForSearch("", o, false, nil); err != nil {
		t.Fatalf("EnsureForSearch fast path err=%v", err)
	}
	if ss, err := Search("", o); err != nil || len(ss) != 1 {
		t.Fatalf("search after fast path=%#v err=%v", ss, err)
	}
}

// SearchWithRecovery must pass through a clean search unchanged, and when the
// forced rebuild it triggers on a corrupt index itself fails, it must surface
// that failure rather than mask it.
func TestSearchWithRecoveryPassthroughAndForcedRebuildFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	parent := filepath.Join(tmp, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "idx")
	writeTinyIndex(t, dir)

	o := search.Options{Query: "alpha"}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	// EnsureForSearch always indexes with scope=="", so the first call above
	// rebuilds from (empty) real sources and discards the fixture; restore it.
	writeTinyIndex(t, dir)
	ss, err := SearchWithRecovery(dir, o, nil)
	if err != nil || len(ss) != 1 {
		t.Fatalf("clean passthrough ss=%#v err=%v", ss, err)
	}

	// Corrupt a bucket so Search reports errCorruptIndex, and make the
	// recovery rebuild's dir+".tmp" staging MkdirAll fail by making its
	// parent read-only (the lock file already exists, so lockDir itself
	// still succeeds).
	bucketPath := filepath.Join(dir, "buckets", bucket("talpha")+".bin")
	if err := os.WriteFile(bucketPath, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(parent, 0o755) }()
	if _, err := SearchWithRecovery(dir, o, nil); err == nil {
		t.Fatal("SearchWithRecovery with blocked rebuild returned nil error")
	}
}

// Recent/RecentProject/FindByPrefix/HasManifest must all resolve dir=="" and
// propagate a missing-manifest error rather than panicking; FindByPrefix must
// pick the most recently updated of several prefix matches and surface a
// records read error separately from the manifest read.
func TestRecentFamilyDefaultDirAndMissingManifest(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	if HasManifest("") {
		t.Fatal("HasManifest('') true with nothing built")
	}
	missing := filepath.Join(tmp, "does-not-exist")
	if _, err := Recent(missing, 1); err == nil {
		t.Fatal("Recent missing manifest returned nil")
	}
	if _, err := RecentProject(missing, "p", 1); err == nil {
		t.Fatal("RecentProject missing manifest returned nil")
	}
	if _, _, err := FindByPrefix(missing, "p"); err == nil {
		t.Fatal("FindByPrefix missing manifest returned nil")
	}

	if err := EnsureForSearch("", search.Options{Query: "pfx", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claudeRoot, "p1", "a.jsonl"), claudeLine("pfx-a", "2026-01-01T00:00:00Z", "pfx one"))
	write(t, filepath.Join(claudeRoot, "p2", "b.jsonl"), claudeLine("pfx-b", "2026-01-01T01:00:00Z", "pfx two"))
	if err := EnsureForSearch("", search.Options{Query: "pfx", All: true}, true, nil); err != nil {
		t.Fatal(err)
	}
	found, ok, err := FindByPrefix("", "pfx")
	if err != nil || !ok || found.ID != "pfx-b" {
		t.Fatalf("FindByPrefix multi-match latest: found=%#v ok=%v err=%v", found, ok, err)
	}

	// Now delete records.bin: readManifest still succeeds but recordsForKey
	// must surface its own error distinctly.
	dir2 := DefaultDir()
	if err := os.Remove(filepath.Join(dir2, "records.bin")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FindByPrefix("", "pfx"); err == nil {
		t.Fatal("FindByPrefix with missing records.bin returned nil")
	}
	if _, err := RecentProject("", "p", 1); err == nil {
		t.Fatal("RecentProject with missing records.bin returned nil")
	}
}

// A dir+".tmp" staging path blocked by a pre-existing regular file must fail
// MkdirAll in the full rebuild; a records.bin path blocked by a
// pre-existing directory must fail the subsequent os.Create.
func TestRebuildStagingDirBlocked(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based permission simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)

	// rebuild always os.RemoveAll(dir+".tmp") before
	// recreating it, so a pre-existing file or directory there gets wiped
	// before MkdirAll runs. To make the staging MkdirAll itself fail, the
	// dir's parent must be read-only: RemoveAll on the not-yet-existing
	// tmp path is then a harmless no-op, but MkdirAll cannot create it.
	blocked := filepath.Join(tmp, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(blocked, 0o755) }()

	dir := filepath.Join(blocked, "idx-a")
	if err := rebuild(dir, "", "", map[string]FileState{}, nil); err == nil {
		t.Fatal("rebuild with read-only parent returned nil")
	}
}

// A codex session appearing both in a rollout file (real project, later
// timestamps) and in history.jsonl (Project=="history", zero timestamps
// because ts is omitted) under the same session id drives every branch of
// the session-merge logic in both rebuild() and writeSessionsWithSync():
// Started.IsZero, old.Updated.After, Project=="history" carry-over, and
// Title=="" carry-over.
func TestCodexDualSourceDuplicateMergeBranches(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	codexRoot := os.Getenv("DEJA_CODEX_ROOT")
	write(t, filepath.Join(codexRoot, "sessions", "2026", "01", "01", "rollout-2026-01-01T12-00-00-dup1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"dup1","cwd":"/p/real-project"}}`+"\n"+
			`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"rollout dupneedle question"}}`+"\n")
	// ts omitted (not a number) -> parseTimeAny returns zero time, so the
	// history-derived session has Started==Updated==zero.
	write(t, filepath.Join(codexRoot, "history.jsonl"),
		`{"session_id":"dup1","text":"history dupneedle question"}`+"\n")

	dir := filepath.Join(tmp, "rebuild-idx")
	if err := rebuild(dir, "codex", "", currentFiles("codex"), nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "dupneedle", All: true})
	if err != nil || len(ss) != 1 {
		t.Fatalf("rebuild merged codex dup: ss=%#v err=%v", ss, err)
	}
	if ss[0].Project != "p/real-project" {
		t.Fatalf("rebuild merge did not carry real project: %#v", ss[0])
	}

	dir2 := filepath.Join(tmp, "writesessions-idx")
	tmp2 := dir2 + ".tmp"
	if err = os.MkdirAll(filepath.Join(tmp2, "buckets"), 0o755); err != nil {
		t.Fatal(err)
	}
	sessLoad := loadProgress("codex", nil)
	if err = writeSessionsWithSync(tmp2, dir2, sessLoad, currentFiles("codex"), "", importedState{}); err != nil {
		t.Fatal(err)
	}
	ss2, err := Search(dir2, search.Options{Query: "dupneedle", All: true})
	if err != nil || len(ss2) != 1 {
		t.Fatalf("writeSessionsWithSync merged codex dup: ss=%#v err=%v", ss2, err)
	}
	if ss2[0].Project != "p/real-project" {
		t.Fatalf("writeSessionsWithSync merge did not carry real project: %#v", ss2[0])
	}
}

// carryRedactions must skip an old file entry no longer present in the
// new Files map; Redactions("") must resolve DefaultDir.
func TestCarryRedactionsBranches(t *testing.T) {
	_ = hermeticIndexEnv(t)

	m3 := &Manifest{Files: map[string]FileState{}}
	carryRedactions(m3, Manifest{Files: map[string]FileState{"gone": {Path: "gone", Redactions: 3}}}, map[string]bool{})
	if m3.Redacted != 0 {
		t.Fatalf("carryRedactions carried a file absent from the new map: %#v", m3)
	}

	dir := DefaultDir()
	writeTinyIndex(t, dir)
	if stats, err := Redactions(""); err != nil || stats.Total == 0 {
		t.Fatalf("Redactions('') = %#v err=%v", stats, err)
	}
}

// updateIndex must skip a syncImportPath entry when scanning old.Files for
// removals (it is virtual, never a real removed source); a corrupt bucket
// discovered mid-append must fail with errCorruptIndex and be caught by
// updateIndex, triggering a full rebuild instead of surfacing the error.
func TestUpdateIndexSyncImportSkipAndCorruptAppendRebuild(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "syncskip needle one"))

	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Manually mark the virtual import path as tracked so updateIndex's
	// removed-file scan walks over it.
	m.Files[syncImportPath] = FileState{Path: syncImportPath}
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	// Append a second line so the append-eligible path runs and old.Files
	// still contains syncImportPath during the removed-file scan.
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(claudeLine("s1", "2026-01-02T03:05:05Z", "syncskip needle two")); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err = Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("update with virtual import path in Files: %v", err)
	}
	if ss, searchErr := Search(dir, search.Options{Query: "syncskip needle two", All: true}); searchErr != nil || len(ss) == 0 {
		t.Fatalf("appended message missing: %#v err=%v", ss, searchErr)
	}

	// Corrupt-bucket-during-append case: precreate a garbage bucket file for
	// the exact bucket a new token will land in, then append content whose
	// new token hashes into that bucket.
	dir2 := filepath.Join(tmp, "idx2")
	file2 := filepath.Join(claudeRoot, "p2", "s2.jsonl")
	write(t, file2, claudeLine("s2", "2026-01-02T03:04:05Z", "corruptbucket base"))
	if err = Ensure(dir2, "", false, nil); err != nil {
		t.Fatal(err)
	}
	tok := "zzcorruptcollision"
	bpath := filepath.Join(dir2, "buckets", bucket("t"+tok)+".bin")
	if err = os.WriteFile(bpath, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	f2, err := os.OpenFile(file2, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f2.WriteString(claudeLine("s2", "2026-01-02T03:05:05Z", tok)); err != nil {
		t.Fatal(err)
	}
	_ = f2.Close()
	if err := Ensure(dir2, "", false, nil); err != nil {
		t.Fatalf("append with corrupt bucket must self-heal via rebuild, got err=%v", err)
	}
	if ss, err := Search(dir2, search.Options{Query: tok, All: true}); err != nil || len(ss) == 0 {
		t.Fatalf("healed content missing after corrupt-bucket rebuild: %#v err=%v", ss, err)
	}
}

// appendIncremental must surface (not swallow) a genuine, non-corrupt bucket
// read failure so updateIndex's "append: %w" wrap path executes.
func TestUpdateIndexAppendNonCorruptErrorWrapped(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod-based unreadable file simulation is non-root unix-only")
	}
	tmp := hermeticIndexEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	file := filepath.Join(claudeRoot, "p", "s.jsonl")
	write(t, file, claudeLine("s1", "2026-01-02T03:04:05Z", "permneedle base"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	tok := "permlockedtoken"
	bpath := filepath.Join(dir, "buckets", bucket("t"+tok)+".bin")
	if err := os.WriteFile(bpath, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bpath, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(bpath, 0o644) }()
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(claudeLine("s1", "2026-01-02T03:05:05Z", tok)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	err = Ensure(dir, "", false, nil)
	if err == nil {
		t.Fatal("Ensure with unreadable bucket file returned nil error")
	}
}

func TestAddIndexKeysDedupBranch(t *testing.T) {
	buckets := bucketPostings{}
	addIndexKeys(buckets, "alpha alpha alpha", 5, 1, time.Time{}, false)
	total := 0
	for _, toks := range buckets {
		for _, posts := range toks {
			total += len(posts)
		}
	}
	if total != 1 {
		t.Fatalf("addIndexKeys duplicate token dedup failed, total postings=%d", total)
	}
}
