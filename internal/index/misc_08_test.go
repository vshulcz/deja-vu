package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
	query "github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A new conversation is a new file, so the commonest update there is was the
// one that rewrote the whole store: `canAppendIncremental` refused any path it
// had not seen, and the replacement path reads every surviving record back
// through the tokenizer. Measured on a synthetic Codex store, one new
// one-message transcript cost 1.19s at 42.9 MB of records and 4.76s at
// 171.4 MB, against 0.11s and 0.30s for a message appended to a file already
// indexed — the cost followed the store, not the new file (#3500).
//
// The progress line is the assertion because it names the path taken: the
// append path says what it updated, the replacement path prints its
// changed/removed counts.
func TestANewTranscriptIsAppendedNotRewritten(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	first := filepath.Join(claude, "p", "first.jsonl")
	write(t, first, claudeLine("s-first", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The bytes already written, so the next pass can be held to not rewriting
	// them. This is the invariant the progress line only describes: whatever the
	// pass says it did, the records that were there have to still be there,
	// unchanged, at the same offsets — that is what makes the cost follow the new
	// file instead of the store.
	recordsBefore, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(claude, "p", "second.jsonl"),
		claudeLine("s-second", "2026-01-02T05:00:00Z", "the billing webhook retries twice"))
	var progress bytes.Buffer
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, "updated 1 file") {
		t.Errorf("a new transcript did not take the append path: %q", got)
	}
	recordsAfter, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recordsAfter) <= len(recordsBefore) {
		t.Fatalf("records.bin is %d bytes after the append and was %d — the new session went somewhere else",
			len(recordsAfter), len(recordsBefore))
	}
	if !bytes.Equal(recordsAfter[:len(recordsBefore)], recordsBefore) {
		t.Error("the records already on file were rewritten, so the pass paid for the whole store")
	}

	for query, want := range map[string]string{
		"billing webhook retries": "s-second",
		"exporter retries":        "s-first",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
}

// Resuming a parse is what a kind without an offset parser cannot do, and a
// file deja has never read needs no resuming — so it takes the append path
// too, read whole from its first byte.
//
// This used to be refused, and the refusal was not free: it applied to the
// whole batch, so one new file of such a kind sent every other changed file
// down the replacement path as well. The only surfaces that take that path are
// a CLI search, an MCP recall and `deja index`; a machine driven by hooks takes
// none of them, and ten transcripts — five senpi, five Copilot Chat — sat
// unread there for eight weeks while every pass saw them (#3747).
func TestANewFileOfAKindThatCannotResumeIsAppendedWhole(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	write(t, filepath.Join(claude, "p", "first.jsonl"),
		claudeLine("s-first", "2026-01-02T03:04:05Z", "the exporter retries without a pause"))
	dir := filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	recordsBefore, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}

	gemini := filepath.Join(os.Getenv("DEJA_GEMINI_ROOT"), "tmp", "gid", "chats", "g.json")
	write(t, gemini, `{"sessionId":"g-new","startTime":"2026-01-03T00:00:00Z","messages":`+
		`[{"type":"user","content":"the invoice job hammers the billing API"}]}`)
	var progress bytes.Buffer
	if err = Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if got := progress.String(); !strings.Contains(got, "updated 1 file") {
		t.Errorf("a new file of a kind that cannot resume took the replacement path: %q", got)
	}
	recordsAfter, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recordsAfter[:len(recordsBefore)], recordsBefore) {
		t.Error("the records already on file were rewritten, so the pass paid for the whole store")
	}
	// The point of the old refusal: a file marked read whose sessions were
	// never indexed. Both halves are checked — the session answers a query,
	// and the one already there still does.
	for query, want := range map[string]string{
		"invoice job hammers": "g-new",
		"exporter retries":    "s-first",
	} {
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 || ss[0].ID != want {
			t.Fatalf("%q found %#v, want the session %s", query, ss, want)
		}
	}
}

func nlIndex(t *testing.T, texts ...string) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, len(texts))
	for i, text := range texts {
		sessions = append(sessions, model.Session{
			ID: string(rune('a' + i)), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: text}},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStemFallbackIngToSPair(t *testing.T) {
	dir := nlIndex(t, "staging domain fails CORS but prod works")
	result, err := SearchDetailed(dir, search.Options{Query: "cors failing staging", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || !result.Stemmed {
		t.Fatalf("failing->fails not recovered: sessions=%d stemmed=%v", len(result.Sessions), result.Stemmed)
	}
	if vs := result.Variants["failing"]; len(vs) == 0 {
		t.Fatalf("variants missing failing: %v", result.Variants)
	}
}

func TestStemFallbackShortTermPlural(t *testing.T) {
	dir := nlIndex(t, "rate limiter lets short bursts through the bucket")
	result, err := SearchDetailed(dir, search.Options{Query: "limiter let bursts", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("let->lets not recovered: %+v", result)
	}
}

func TestStemFallbackDropsFillerToken(t *testing.T) {
	dir := nlIndex(t,
		"rate limiter lets short bursts through the bucket",
		"why do refresh tokens die after rotation",
	)
	// "why" matches only the other session; no session holds all tokens.
	result, err := SearchDetailed(dir, search.Options{Query: "why limiter lets bursts through", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
		t.Fatalf("filler token still constrains the AND: %+v", result.Sessions)
	}
	if vs, ok := result.Variants["why"]; !ok || len(vs) != 1 || vs[0] != "" {
		t.Fatalf("dropped token not marked optional: %v", result.Variants)
	}
}

func TestStemFallbackKeepsAndWhenFullMatchExists(t *testing.T) {
	dir := nlIndex(t,
		"rate limiter lets short bursts through the bucket",
		"why does the rate limiter lets bursts pass in this trace",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "why limiter lets bursts", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Sessions {
		if s.ID == "b" {
			return
		}
	}
	t.Fatalf("full-coverage session missing from results: %+v", result.Sessions)
}

func TestStemFallbackNeedsTwoAnchors(t *testing.T) {
	dir := nlIndex(t, "rate limiter lets short bursts through")
	// Only one token exists in the corpus — dropping the rest would leave a
	// single-term query the user did not ask for.
	result, err := SearchDetailed(dir, search.Options{Query: "zzqx wwvv limiter", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 0 {
		t.Fatalf("single anchor produced results: %+v", result.Sessions)
	}
}

func TestCloseTierDisagreeFallsThroughToRelevance(t *testing.T) {
	// Session a answers the question but lacks "birthday" in any form, so the
	// substring intersection lands only on b — an incidental session whose
	// long words contain every query token. The ladder used to stop at b.
	dir := nlIndex(t,
		"picked a scarf as the gift for my sister celebration",
		"gifted birthdays sisterhood newsletter volume nine",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "sister birthday gift", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) < 2 || result.Sessions[0].ID != "a" {
		t.Fatalf("relevance must lead when substring hits disagree: %+v", idsOf(result.Sessions))
	}
	if result.Sessions[1].ID != "b" {
		t.Fatalf("substring hit must survive as the tail: %+v", idsOf(result.Sessions))
	}
}

// A two-word question has no rest to keep once a word drops, and it came back
// empty while the same word in a three-word question was dropped (#5711).
func TestTwoWordQueryFallsBackToTheWordTheStoreHolds(t *testing.T) {
	dir := nlIndex(t,
		"we kept the ledger outbox in sqlite after the review",
		"standup notes about lunch",
		"standup notes about the mobile gap",
		"standup notes about release dates",
		"standup notes about a readme typo",
		"standup notes about the font size",
		"standup notes about the old logo",
		"standup notes about parking",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "zookeeper outbox", All: true, LoneWord: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(result.Sessions); len(got) == 0 || got[0] != "a" {
		t.Fatalf("the session holding the known word is not on the page: %v", got)
	}
	if vs := result.Variants["zookeeper"]; len(vs) != 1 || vs[0] != "" {
		t.Fatalf("the unknown word is not marked dropped: %v", result.Variants)
	}
	// Only where the caller asked: deja search keeps its silence.
	plain, err := SearchDetailed(dir, search.Options{Query: "zookeeper outbox", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Sessions) != 0 {
		t.Fatalf("a search that did not ask for the fallback got it: %v", idsOf(plain.Sessions))
	}
}

// The word left standing has to say something on its own: a word most of the
// store holds would put the store on the page.
func TestTwoWordQueryDoesNotFallBackToACommonWord(t *testing.T) {
	dir := nlIndex(t,
		"we kept the ledger outbox in sqlite after the review",
		"standup notes about lunch",
		"standup notes about the mobile gap",
		"standup notes about release dates",
		"standup notes about a readme typo",
		"standup notes about the font size",
		"standup notes about the old logo",
		"standup notes about parking",
	)
	for _, q := range []string{"zookeeper standup", "zookeeper the"} {
		result, err := SearchDetailed(dir, search.Options{Query: q, All: true, LoneWord: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) != 0 {
			t.Errorf("%q fell back to a word most sessions hold: %v", q, idsOf(result.Sessions))
		}
	}
}

func idsOf(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

// A rewritten session file goes through the non-append update path as a
// replacement. Its postings must carry the same Ord the manifest stores,
// otherwise cutPostingsBySession drops them and the session becomes
// unsearchable.
func TestReplacedSessionStaysSearchable(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-ordchurn")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := `{"type":"user","sessionId":"ord1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"oldneedle question first"}}` + "\n"
	s2 := `{"type":"user","sessionId":"ord2","timestamp":"2026-01-02T04:04:05Z","message":{"role":"user","content":"second session text"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "ord1.jsonl"), []byte(s1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "ord2.jsonl"), []byte(s2), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "oldneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}

	// Rewrite ord1 shorter (same session, smaller file, so canAppendIncremental
	// rejects it and the non-append replacement path runs) while ord2 keeps its
	// Ord — a fresh nextSessionOrd for ord1 would no longer match its postings.
	s1b := `{"type":"user","sessionId":"ord1","timestamp":"2026-01-02T05:04:05Z","message":{"role":"user","content":"newneedle"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "ord1.jsonl"), []byte(s1b), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "newneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "newneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "ord1" || len(ss[0].Messages) != 1 {
		t.Fatalf("replaced session not searchable: %#v", ss)
	}
	// Manifest Ord and posting Sid must agree.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Sessions["claude:ord1"]
	if !ok {
		t.Fatal("no meta for claude:ord1")
	}
	posts, err := postingsFor(dir, "tnewneedle")
	if err != nil || len(posts) == 0 {
		t.Fatalf("postings err=%v n=%d", err, len(posts))
	}
	for _, p := range posts {
		if p.Sid != meta.Ord {
			t.Fatalf("posting Sid=%d != manifest Ord=%d", p.Sid, meta.Ord)
		}
	}
}

// TestStripsHarnessPlumbing pins what comes out of a transcript that a harness
// wrote to itself. Measured on a real index before this existed: 304 records
// were nothing but plumbing (#551).
func TestStripsHarnessPlumbing(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"system reminder", "before <system-reminder>do not do X</system-reminder> after", "before  after"},
		{"local command caveat", "<local-command-caveat>Caveat: generated by the user</local-command-caveat>ok", "ok"},
		{"slash command echo", "<command-name>/clear</command-name>\n<command-args></command-args>", ""},
		{"interrupt marker on its own line", "real question\n[Request interrupted by user for tool use]\nmore", "real question\nmore"},
		{"hook envelope line", "UserPromptSubmit hook additional context:\nkept", "kept"},
		{"no visible output", "[Your previous response had no visible output. Please continue]", ""},
	}
	for _, c := range cases {
		if got := stripSelfRecall(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestKeepsWhatIsNotPlumbing is the half that matters more: a message that
// merely *mentions* a marker — a bug report, this repository's own tests — must
// survive, and command output must never be touched, because it is the most
// useful thing a transcript holds.
func TestKeepsWhatIsNotPlumbing(t *testing.T) {
	keep := []string{
		"the parser should ignore <system-reminder> tags mid-sentence",
		"we saw [Request interrupted by user] inside a quoted log and it broke",
		"<bash-stdout>ok  github.com/x  1.2s</bash-stdout>",
		"deja indexes UserPromptSubmit hook additional context: as if a person said it",
	}
	for _, in := range keep {
		if got := stripSelfRecall(in); got != in {
			t.Errorf("stripped a mention rather than plumbing:\n in:  %q\n out: %q", in, got)
		}
	}
}

// TestFileRecordsStayOutOfOrdinaryRanking is the same rule tool records follow:
// a list of files a turn touched answers "which files", never "what did we
// decide", and left in the ordinary ranking a path would lift a session because
// it contained the words of a question.
func TestFileRecordsStayOutOfOrdinaryRanking(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	ss := []model.Session{{
		ID: "s1", Harness: "claude", Project: "app",
		Started: time.Unix(1700000000, 0), Updated: time.Unix(1700000000, 0),
		Messages: []model.Message{
			{Role: "user", Text: "why is the pool slow", Time: time.Unix(1700000000, 0)},
			{Role: roleFiles, Text: "/repo/internal/db/pool_slow_path.go", Time: time.Unix(1700000001, 0)},
		},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(dir, search.Options{Query: "pool slow", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range hits {
		for _, m := range s.Messages {
			if m.Role == roleFiles {
				t.Fatalf("file record served to an ordinary query: %q", m.Text)
			}
		}
	}
	byRole, err := Search(dir, search.Options{Query: "pool slow", Role: roleFiles, All: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range byRole {
		for _, m := range s.Messages {
			if m.Role == roleFiles {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("asking by role returned no file records")
	}
}

// Session deltas use modulo-2^32 arithmetic; losing a decrease or boundary ID would make #492 return the wrong session.
func TestPostingSessionDeltaRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		blocks [][]posting
	}{
		{
			name: "single posting",
			blocks: [][]posting{
				{
					{Off: 11, Sid: 7},
				},
			},
		},
		{
			name: "ascending session IDs",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 1},
					{Off: 5, Sid: 2},
					{Off: 20, Sid: 100},
				},
			},
		},
		{
			name: "decreasing session ID",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 100},
					{Off: 2, Sid: 7},
				},
			},
		},
		{
			name: "zero session ID",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 0},
				},
			},
		},
		{
			name: "session ID high bit",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 1 << 31},
				},
			},
		},
		{
			name: "maximum session ID",
			blocks: [][]posting{
				{
					{Off: 1, Sid: math.MaxUint32},
				},
			},
		},
		{
			name: "session ID wrap",
			blocks: [][]posting{
				{
					{Off: 1, Sid: math.MaxUint32},
					{Off: 2, Sid: 0},
				},
			},
		},
		{
			name: "both tool values",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 42, Tool: false},
					{Off: 2, Sid: 42, Tool: true},
				},
			},
		},
		{
			name: "previous session resets between blocks",
			blocks: [][]posting{
				{
					{Off: 1, Sid: 77},
					{Off: 2, Sid: 80},
				},
				{
					{Off: 3, Sid: 77},
					{Off: 5, Sid: 80},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, want := range tt.blocks {
				got := decodePostings(encodePostings(want))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("block %d: got %#v, want %#v", i, got, want)
				}
			}
		})
	}
}

// Repeated session IDs must beat absolute encoding; otherwise #492 pays a rebuild without the measured storage gain.
func TestPostingSessionDeltaWireShrinks(t *testing.T) {
	const sid uint32 = 1 << 28

	posts := make([]posting, 64)
	for i := range posts {
		posts[i] = posting{Off: int64(i + 1), Sid: sid}
	}

	var scratch [binary.MaxVarintLen64]byte
	var prevOff int64
	absoluteLen := 0
	for _, p := range posts {
		absoluteLen += binary.PutUvarint(scratch[:], uint64(p.Off-prevOff)) //nolint:gosec // postings ascend by offset
		absoluteLen += binary.PutUvarint(scratch[:], uint64(p.Sid)<<1)
		prevOff = p.Off
	}

	if got := len(encodePostings(posts)); got >= absoluteLen {
		t.Fatalf("encoded length: got %#v, want less than %#v", got, absoluteLen)
	}
}

// A cut varint is an incomplete posting, not a zero-valued posting; #492 must preserve that partial-read boundary.
func TestPostingSessionDeltaTruncatedVarint(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
	}{
		{
			name: "truncated offset",
			wire: []byte{0x80},
		},
		{
			name: "truncated session",
			wire: []byte{1, 0x80},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodePostings(tt.wire)
			want := []posting{}
			if len(got) != 0 {
				t.Fatalf("decodePostings: got %#v, want %#v", got, want)
			}
		})
	}
}

// TestPostingSessionDeltaIsZigzagOnTheWire pins the encoding itself, not only
// that it round-trips. A plain modular delta round-trips too, so the table
// above cannot tell the two apart; a step backwards is where they diverge.
// Session 100 then 7 writes the deltas 100 and -93 as zigzag 200 and 185, two
// varint bytes each. Without zigzag the second would be 0xffffffa3, five
// bytes, on every posting whose session id steps back (#492).
func TestPostingSessionDeltaIsZigzagOnTheWire(t *testing.T) {
	got := encodePostings([]posting{{Off: 1, Sid: 100}, {Off: 2, Sid: 7}})
	want := []byte{0x01, 0x90, 0x03, 0x01, 0xf2, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("encodePostings wire = % x, want % x", got, want)
	}
	back := decodePostings(got)
	if len(back) != 2 || back[0].Sid != 100 || back[1].Sid != 7 {
		t.Fatalf("decodePostings round trip = %#v", back)
	}
}

// TestOpenBucketDirRefusesTheOlderMagic is the other half of the format break.
// The version bump rebuilds a writable index, but a directory that cannot be
// locked is served with no version check at all, so a v31 bucket has to fail
// here rather than decode its session ids under the new rule (#492).
func TestOpenBucketDirRefusesTheOlderMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x00.bin")
	old := append([]byte("DJB1"), 0x00)
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openBucketDir(path); !errors.Is(err, errCorruptIndex) {
		t.Fatalf("openBucketDir on a DJB1 bucket = %v, want errCorruptIndex", err)
	}
}

func sampleLine(body string) string {
	return claudeLine("s1", "2026-01-01T00:00:00Z", body+strings.Repeat("x", 200))
}

// The sample has to catch what the full hash caught: a transcript truncated
// and rewritten past its old length, which looks exactly like an append. It is
// caught wherever the rewrite starts — either past SafeSize, where the indexed
// bytes really are untouched, or before it, where the window ending at
// SafeSize falls inside the rewritten region.
func TestPrefixSampleCatchesARewriteAtEveryDepth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	var b strings.Builder
	for range 4000 {
		b.WriteString(sampleLine("original "))
	}
	original := b.String()
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	size := int64(len(original))
	want := filePrefixSample(path, size)
	if want == 0 {
		t.Fatal("no sample for a file that exists")
	}

	for _, depth := range []float64{0, 0.25, 0.5, 0.9, 0.999} {
		keep := int(float64(len(original)) * depth)
		var r strings.Builder
		r.WriteString(original[:keep])
		for r.Len() < len(original)+5000 {
			r.WriteString(sampleLine("rewritten "))
		}
		if err := os.WriteFile(path, []byte(r.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := filePrefixSample(path, size); got == want {
			t.Errorf("a rewrite keeping the first %.1f%% passed as an append", depth*100)
		}
	}

	// An append still verifies: the bytes up to the old size are the same
	// bytes, so the sample over them is unchanged.
	if err := os.WriteFile(path, []byte(original+sampleLine("appended ")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := filePrefixSample(path, size); got != want {
		t.Errorf("an append was rejected: %d != %d", got, want)
	}
}

// The bounded read stated as a property rather than as a duration, which a
// busy machine would make flaky: two files differing only in the middle share
// a sample. That is the trade this makes. A rewind rewrites from its
// truncation point to the end, so it always disturbs the window ending at
// SafeSize; a rewrite that changes only the middle, keeps both windows and
// lands on exactly the same length is not something a transcript does.
func TestPrefixSampleDoesNotReadTheMiddle(t *testing.T) {
	dir := t.TempDir()
	big := []byte(strings.Repeat("a", 4<<20))
	a := filepath.Join(dir, "a.jsonl")
	if err := os.WriteFile(a, big, 0o644); err != nil {
		t.Fatal(err)
	}
	middle := append([]byte(nil), big...)
	middle[len(middle)/2] = 'b'
	b := filepath.Join(dir, "b.jsonl")
	if err := os.WriteFile(b, middle, 0o644); err != nil {
		t.Fatal(err)
	}
	if filePrefixSample(a, int64(len(big))) != filePrefixSample(b, int64(len(middle))) {
		t.Fatalf("the sample covers the middle, so the read is not bounded after all")
	}
}

// A file shorter than one window is covered end to end, which is the case
// every ordinary transcript starts in.
func TestPrefixSampleCoversASmallFileWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := []byte(strings.Repeat("a", 4096))
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	want := filePrefixSample(path, int64(len(body)))
	body[len(body)/2] = 'b'
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if filePrefixSample(path, int64(len(body))) == want {
		t.Fatal("a change inside a small file went unnoticed")
	}
}

func TestForgetTombstoneLifecycle(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index"))
	dir := DefaultDir()
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	ptbl := newRecordTables()
	if _, err = writeRecord(f, Record{Key: "claude:abc123", Role: "user", Text: "one", Time: time.Now()}, ptbl); err != nil {
		t.Fatal(err)
	}
	if _, err = writeRecord(f, Record{Key: "claude:keep", Role: "user", Text: "two", Time: time.Now()}, ptbl); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, RecordStrings: ptbl.strs, Sessions: map[string]SessionMeta{
		"claude:abc123": {ID: "abc123", Harness: "claude", Project: "secret", Updated: time.Now()},
		"claude:keep":   {ID: "keep", Harness: "claude", Project: "public", Updated: time.Now()},
	}}); err != nil {
		t.Fatal(err)
	}
	r, err := Forget(dir, ForgetOptions{Session: "abc", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Sessions != 1 || r.Tombstones != 1 || len(Tombstones()) != 0 {
		t.Fatalf("dry run=%#v tombstones=%v", r, Tombstones())
	}
	r, err = Forget("", ForgetOptions{Project: "SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Sessions != 1 || r.Messages != 1 || r.Tombstones != 1 {
		t.Fatalf("forget=%#v", r)
	}
	if got := Tombstones(); len(got) != 1 || got[0] != "claude:abc123" {
		t.Fatalf("tombstones=%v", got)
	}
	if _, err := Unforget("", "abc123", nil); err != nil {
		t.Fatal(err)
	}
	if len(Tombstones()) != 0 {
		t.Fatal("unforget did not remove tombstone")
	}
}

func TestOldManifestRedactionCompatibility(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeGob(filepath.Join(dir, "manifest.gob"), manifestCore{Version: 9}); err != nil {
		t.Fatal(err)
	}
	if err := writeGob(filepath.Join(dir, "sessions.gob"), map[string]SessionMeta{}); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil || stats.Total != 0 || len(stats.Rules) != 0 {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
	if _, err := RedactionReport(dir); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyHelpers(t *testing.T) {
	meta := SessionMeta{ID: "abcdef", Project: "My-App", Updated: time.Unix(20, 0)}
	if !sessionMatches(meta, ForgetOptions{Session: "abc"}) || !sessionMatches(meta, ForgetOptions{Project: "app"}) || !sessionMatches(meta, ForgetOptions{Before: time.Unix(21, 0)}) {
		t.Fatal("selectors did not match")
	}
	if sessionMatches(meta, ForgetOptions{Session: "zzz"}) || sessionMatches(meta, ForgetOptions{Project: "none"}) || sessionMatches(meta, ForgetOptions{Before: time.Unix(10, 0)}) || sessionMatches(meta, ForgetOptions{}) {
		t.Fatal("selectors matched unexpectedly")
	}
	ss := []model.Session{{Harness: "a", ID: "one"}, {Harness: "a", ID: "two"}}
	if got := filterTombstonedSet(ss, nil); len(got) != len(ss) {
		t.Fatalf("empty tombstones filtered=%#v", got)
	}
	if got := filterTombstonedSet(ss, map[string]bool{"a:one": true}); len(got) != 1 || got[0].ID != "two" {
		t.Fatalf("filtered=%#v", got)
	}
}

func TestTombstonePersistenceAndForgetNoop(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	if err := writeTombstones(map[string]bool{"z:last": true, "a:first": true}); err != nil {
		t.Fatal(err)
	}
	if got := Tombstones(); len(got) != 2 || got[0] != "a:first" || got[1] != "z:last" {
		t.Fatalf("tombstones=%v", got)
	}
	if _, err := Unforget("", "first", nil); err != nil {
		t.Fatal(err)
	}
	if got := Tombstones(); len(got) != 1 || got[0] != "z:last" {
		t.Fatalf("after suffix unforget=%v", got)
	}
	if _, err := Unforget("", "z:", nil); err != nil {
		t.Fatal(err)
	}
	if len(Tombstones()) != 0 {
		t.Fatal("prefix unforget left tombstones")
	}

	dir := filepath.Join(home, "index")
	if err := os.MkdirAll(filepath.Join(dir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	result, err := Forget(dir, ForgetOptions{Project: "missing"})
	if err != nil || result.Sessions != 0 || result.Messages != 0 || result.Tombstones != 0 ||
		result.Notes != 0 || len(result.Keys) != 0 {
		t.Fatalf("no-op forget=%#v err=%v", result, err)
	}
}

func TestRedactionRuleCarryAndReport(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := Manifest{
		Redacted:       4,
		RedactionRules: map[string]int{"claude:aws-secret": 2, "opencode:bearer-token": 2, "malformed": 9},
		Files:          map[string]FileState{},
	}
	m := Manifest{Files: map[string]FileState{}}
	carryRedactions(&m, old, map[string]bool{"claude-source": true})
	if m.RedactionRules["claude:aws-secret"] != 2 || m.RedactionRules["opencode:bearer-token"] != 2 {
		t.Fatalf("carried rules=%v", m.RedactionRules)
	}
	m = Manifest{Files: map[string]FileState{}}
	carryRedactions(&m, old, map[string]bool{sources.OpencodeDB(): true})
	if _, ok := m.RedactionRules["opencode:bearer-token"]; ok {
		t.Fatalf("skipped opencode rule carried: %v", m.RedactionRules)
	}
	if err := writeManifest(dir, Manifest{Version: version, Files: map[string]FileState{}, Redacted: 3, RedactionRules: map[string]int{"claude:credential": 3}, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	stats, err := Redactions(dir)
	if err != nil || stats.Total != 3 || stats.Rules["claude"]["credential"] != 3 {
		t.Fatalf("redaction report=%#v err=%v", stats, err)
	}
}

func TestReadRecordsForForgetMissingIndex(t *testing.T) {
	if got := readRecordsForForget(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing records=%v", got)
	}
}

func TestRebuildHonorsTombstonedLoadedSession(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	path := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl")
	write(t, path, claudeLine("dead-session", "2026-01-02T03:04:05Z", "tombstone needle"))
	dir := filepath.Join(tmp, "index")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil || len(m.Sessions) != 1 {
		t.Fatalf("manifest sessions=%d err=%v", len(m.Sessions), err)
	}
	for key := range m.Sessions {
		if err := rebuildWithTombstones(dir, "", "", currentFiles(""), nil, map[string]bool{key: true}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := Search(dir, search.Options{Query: "tombstone", All: true}); err != nil || len(got) != 0 {
		t.Fatalf("tombstoned search=%#v err=%v", got, err)
	}
}

func TestUnforgetDoesNotOverMatchHarness(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	if err := writeTombstones(map[string]bool{"claude:abc": true, "codex:abc": true, "cursor:xyz": true}); err != nil {
		t.Fatal(err)
	}
	// A bare "c" is an id-prefix: it must NOT resurrect whole harnesses.
	if _, err := Unforget("", "c", nil); err != nil {
		t.Fatal(err)
	}
	if got := len(Tombstones()); got != 3 {
		t.Fatalf("bare prefix c wrongly matched harnesses: %v", Tombstones())
	}
	// An id-prefix that really matches an id works.
	if _, err := Unforget("", "ab", nil); err != nil {
		t.Fatal(err)
	}
	if got := len(Tombstones()); got != 1 {
		t.Fatalf("id-prefix ab: %v", Tombstones())
	}
	// A harness-scoped prefix still works.
	if _, err := Unforget("", "cursor:", nil); err != nil {
		t.Fatal(err)
	}
	if len(Tombstones()) != 0 {
		t.Fatalf("harness scope left: %v", Tombstones())
	}
}

// The command printed nothing at all, so "restored one session" and "that
// prefix matched nothing" looked the same to the person running it (#672).
func TestUnforgetReportsHowManyItLifted(t *testing.T) {
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := writeTombstones(map[string]bool{"claude:a1": true, "claude:a2": true, "cursor:b1": true}); err != nil {
		t.Fatal(err)
	}
	if n, err := Unforget("", "claude:", nil); err != nil || n != 2 {
		t.Fatalf("lifted %d err=%v, want 2", n, err)
	}
	if n, err := Unforget("", "claude:", nil); err != nil || n != 0 {
		t.Fatalf("second run lifted %d err=%v, want 0", n, err)
	}
	// A prefix that matches nothing must not rewrite the file: on a crash
	// between write and rename it would take every other tombstone with it,
	// and there is nothing to change.
	before, err := os.Stat(tombstonePath())
	if err != nil {
		t.Fatal(err)
	}
	if n, unforgetErr := Unforget("", "nosuch", nil); unforgetErr != nil || n != 0 {
		t.Fatalf("a prefix matching nothing lifted %d err=%v", n, unforgetErr)
	}
	after, err := os.Stat(tombstonePath())
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("rewrote the tombstone file for a no-op")
	}
	// The one that was never named is still forgotten.
	if got := Tombstones(); len(got) != 1 || got[0] != "cursor:b1" {
		t.Fatalf("tombstones = %v", got)
	}
}

type recordingProgress struct {
	mu       sync.Mutex
	phases   []string
	advances map[string]int
}

func (r *recordingProgress) Phase(name string, _ int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases = append(r.phases, name)
}

func (r *recordingProgress) Advance(units int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.phases) == 0 {
		return
	}
	if r.advances == nil {
		r.advances = map[string]int{}
	}
	r.advances[r.phases[len(r.phases)-1]] += units
}

func (r *recordingProgress) Harness(string, int, int) {}

// seedClaudeCorpus writes enough of a transcript for a build to have something
// to report on.
func seedClaudeCorpus(t *testing.T, root string) {
	t.Helper()
	proj := filepath.Join(root, "projects", "app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		var b []byte
		when := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC).Add(time.Duration(i) * time.Hour)
		for j := range 4 {
			line, err := json.Marshal(map[string]any{
				"type":      "user",
				"sessionId": "s" + string(rune('a'+i)),
				"timestamp": when.Add(time.Duration(j) * time.Minute).Format(time.RFC3339),
				"message":   map[string]any{"role": "user", "content": "connection pool exhausted again"},
			})
			if err != nil {
				t.Fatal(err)
			}
			b = append(append(b, line...), '\n')
		}
		if err := os.WriteFile(filepath.Join(proj, "s"+string(rune('a'+i))+".jsonl"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestFirstSearchBuildReportsPhases guards the path almost every user takes to
// their first index: they run a search, not `deja index`. That path built
// through rebuildForSearch, which reported no phases at all, so the first-run
// display sat on "starting" with an unmoving bar for the whole build while
// `deja index` — which few people run first — animated correctly.
func TestFirstSearchBuildReportsPhases(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	seedClaudeCorpus(t, filepath.Join(tmp, "claude"))

	rec := &recordingProgress{}
	SetProgress(rec)
	defer SetProgress(nil)

	dir := filepath.Join(tmp, "idx")
	if err := EnsureForSearch(dir, query.Options{Query: "connection pool"}, false, nil); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, p := range rec.phases {
		seen[p] = true
	}
	for _, want := range []string{"reading sessions", "indexing messages", "writing index"} {
		if !seen[want] {
			t.Fatalf("phase %q never reported; got %v", want, rec.phases)
		}
	}
	// A phase that announces a total and never advances is a bar that does not
	// move, which is the defect this test exists for.
	for _, want := range []string{"indexing messages", "writing index"} {
		if rec.advances[want] == 0 {
			t.Fatalf("phase %q reported no progress; advances=%v", want, rec.advances)
		}
	}
}

// countingProgress records what the reading stage promised and what it
// delivered.
type countingProgress struct {
	mu       sync.Mutex
	phase    string
	total    map[string]int
	advanced map[string]int
}

func (c *countingProgress) Phase(name string, total int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.phase = name
	c.total[name] = total
}

func (c *countingProgress) Advance(units int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advanced[c.phase] += units
}

func (c *countingProgress) Harness(string, int, int) {}

// The bar is sized in files and moved in stores. It counted a file under its
// kind — "cline-sdk" — and advanced by the store the parse loop names, so a
// corpus of cline sessions filled the total and left the bar where it was
// (#2242).
func TestEveryFileCountedInTheBarAlsoMovesIt(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	cline := filepath.Join(tmp, "cline")
	if err := os.MkdirAll(cline, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLINE_ROOT", cline)

	stamp := time.Now().Add(-time.Hour).UnixMilli()
	for i := range 4 {
		body := fmt.Sprintf(`{"agent":"lead","messages":[{"ts":%d,"role":"user","content":"hello there"}]}`, stamp)
		name := fmt.Sprintf("s%d.messages.json", i)
		if err := os.WriteFile(filepath.Join(cline, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"hello there"}}`, at)
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := &countingProgress{total: map[string]int{}, advanced: map[string]int{}}
	SetProgress(p)
	defer SetProgress(nil)
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	total, advanced := p.total["reading sessions"], p.advanced["reading sessions"]
	p.mu.Unlock()
	// The premise: every file on disk is in the total, or the bar being short
	// proves nothing.
	if total != 5 {
		t.Fatalf("the reading stage was sized at %d for five files on disk", total)
	}
	if advanced != total {
		t.Errorf("the reading stage promised %d files and moved the bar by %d", total, advanced)
	}
}

// projectScope is projectInScope over a list of candidates, answered once per
// project name. It must agree with asking each candidate in turn.
func TestProjectScopeAgreesWithProjectInScope(t *testing.T) {
	projects := []string{
		"", "-", "api", "acme/api", "work/api", "Work/API", "imported:api", "imported:peer/acme/api",
		`c:\work\api`, `C:\Work\Api`, "deja-vu", "goprojects/deja-vu", "deja-push", "x/deja-vu/sub",
		"imported:", "/", "api/", "Ünïcode/Проект", "ünïcode/проект",
	}
	wants := [][]string{
		nil,
		{""},
		{"api"},
		{"work/api", "api"},
		{"WORK/api"},
		{"deja-vu", "goprojects/deja-vu"},
		{"проект", "ünïcode/ПРОЕКТ"},
		{"", "acme/api", ""},
		{`work\api`},
	}
	for _, ws := range wants {
		scope := newProjectScope(ws...)
		for _, p := range projects {
			want := false
			for _, w := range ws {
				if projectInScope(p, w) {
					want = true
					break
				}
			}
			// Twice: the second answer comes from the memo.
			for range 2 {
				if got := scope.has(p); got != want {
					t.Fatalf("wants %q, project %q: scope says %v, projectInScope says %v", ws, p, got, want)
				}
			}
		}
	}
}

func TestEndsWithSegment(t *testing.T) {
	for _, c := range []struct {
		p, w string
		want bool
	}{
		{"acme/api", "api", true},
		{`acme\api`, "api", true},
		{"api", "api", false},
		{"/api", "api", true},
		{"xapi", "api", false},
		{"acme/api", "acme/api", false},
		{"a/acme/api", "acme/api", true},
		{"", "", false},
		{"/", "", true},
	} {
		if got := endsWithSegment(c.p, c.w); got != c.want {
			t.Errorf("endsWithSegment(%q, %q) = %v, want %v", c.p, c.w, got, c.want)
		}
	}
}

// The automatic scope takes a candidate as a path suffix, which is right for
// the full forms — one project reached under different parents, worktrees of
// one repo — and wrong for the bare basename the candidates also carry: with it
// a session start in /work/api recalled a client's acme/api "from this
// project" (#2333).
func TestProjectScopeDoesNotMatchOnABareBasename(t *testing.T) {
	cases := []struct {
		project, want string
		in            bool
	}{
		{"work/api", "work/api", true},
		{"work/api", "api", false}, // the bare name reaches through the full form
		{"acme/api", "api", false}, // another project that ends the same way
		{"acme/api", "acme/api", true},
		{"src/clients/acme/api", "acme/api", true}, // the full form still reaches
		{"imported:acme/api", "acme/api", true},    // a peer's copy of that project
		{"imported:acme/api", "api", true},         // a peer's path is not this machine's
		{`acme\api`, "acme/api", false},            // windows separator, path form
		{`src\acme\api`, `acme\api`, true},
		{"acme/api", "", false},
	}
	for _, c := range cases {
		if got := projectInScope(c.project, c.want); got != c.in {
			t.Errorf("projectInScope(%q, %q) = %v, want %v", c.project, c.want, got, c.in)
		}
	}
}

// The strict rule drops the allowance synced work gets: right for recall, wrong
// for a command that packages one session for another agent (#2347).
func TestStrictProjectScopeDropsTheSyncedAllowance(t *testing.T) {
	cases := []struct {
		project, want string
		in            bool
	}{
		{"work/api", "work/api", true},
		{"imported:work/api", "work/api", true},
		{"imported:clients/acme/api", "api", false},
		{"imported:goprojects/svc", "svc", false},
		{"imported:svc", "svc", true},
		{"src/clients/acme/api", "acme/api", true},
		{"acme/api", "", false},
	}
	for _, c := range cases {
		if got := ProjectInScopeStrict(c.project, c.want); got != c.in {
			t.Errorf("ProjectInScopeStrict(%q, %q) = %v, want %v", c.project, c.want, got, c.in)
		}
	}
	// And the loose rule still answers the synced case recall depends on.
	if !ProjectInScope("imported:goprojects/svc", "svc") {
		t.Error("the loose rule no longer reaches a peer's project by its local name")
	}
}

// The shape a decision has after it crosses a machine boundary: a session of
// its own carrying the state, since the receiving machine's notes file knows
// nothing about it (#2510).
func TestPromotedNoteMetasFindsWhatArrivedBySync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEJA_INDEX_DIR", dir)
	m := Manifest{Sessions: map[string]SessionMeta{
		"deja:imported-1": {ID: "imported-1", Harness: "deja", Project: "imported:home/app",
			OrigID: "deja-note-claude-dec", Lifecycle: "accepted", LifecycleNote: "the retry budget stays at 5"},
		"claude:imported-2": {ID: "imported-2", Harness: "claude", Project: "imported:home/app", OrigID: "dec"},
		"claude:local":      {ID: "local", Harness: "claude", Project: "home/app"},
	}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}

	got := PromotedNoteMetas(dir, nil)
	if len(got) != 1 || got[0].OrigID != "deja-note-claude-dec" {
		t.Fatalf("the note session is not what came back: %+v", got)
	}
	if n := PromotedNoteMetas(dir, func(p string) bool { return p == "home/app" }); len(n) != 0 {
		t.Errorf("a project the caller withholds still came back: %+v", n)
	}
	if n := PromotedNoteMetas("", nil); len(n) != 1 {
		t.Errorf("the default dir found %d, want 1", len(n))
	}
}

// Promoted notes are served newest-first so the latest correction is the answer
// that holds (#812). An incremental build appends the new lines to what the log
// already has, so the order came out oldest-first and the correction sat last —
// the failure #812 was written for (#944).
func TestPromotedNoteKeepsCorrectionsNewestFirstAfterAnIncrementalBuild(t *testing.T) {
	tmp := t.TempDir()
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))

	line := func(ts, state, text string) string {
		b, err := json.Marshal(map[string]string{
			"ts": ts, "project": "p", "text": text,
			"kind": "promoted", "session": "claude:s1", "state": state, "title": "pool cap",
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	body := line("2026-07-12T10:00:00Z", "accepted", "first answer: raise the pool cap to 200") +
		line("2026-07-13T10:00:00Z", "accepted", "second answer: 50 is enough")
	if err := os.WriteFile(notes, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// The correction arrives after the index exists, so the next build is
	// incremental.
	if err := os.WriteFile(notes, []byte(body+line("2026-07-20T10:00:00Z", "rejected", "rolled back, the cap stays at 20")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	s, ok, err := FindByPrefix(dir, "deja-note-claude-s1")
	if err != nil || !ok {
		t.Fatalf("promoted note not found: %v", err)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(s.Messages))
	}
	if !strings.Contains(s.Messages[0].Text, "rolled back") {
		t.Errorf("the note leads with %q, not the newest correction", s.Messages[0].Text)
	}

	// The search path assembles sessions separately and must agree.
	ss, err := Search(dir, query.Options{Query: "cap", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range ss {
		if got.ID != "deja-note-claude-s1" || len(got.Messages) < 2 {
			continue
		}
		if !strings.Contains(got.Messages[0].Text, "rolled back") {
			t.Errorf("search leads with %q, not the newest correction", got.Messages[0].Text)
		}
	}
}

// seedCleanWord indexes a session whose only content word is the one given, so
// a hit cannot come from a stem sitting elsewhere in the sentence.
func seedCleanWord(t *testing.T, word string) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/proj",
		"timestamp": "2026-08-20T10:00:00Z",
		"message":   map[string]any{"role": "user", "content": "we looked at " + word + " yesterday"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// `close` is what deja reports for three different mechanisms, and the rows
// below use all three. Worth knowing before reading them as one thing:
//
//   - suffixes reach through the stem tier — "ies" to "y" and the rest of
//     oneSuffixStep — which reports itself as close (retrieval.go:2310);
//   - hyphens and a mistyped long word reach through the fuzzy tier, which is
//     the edit-distance walk that reports the same tier (retrieval.go:2293);
//   - the two exact rows reach because the index tokeniser breaks a run at any
//     rune that is not a letter, a digit, `_` or `-` (retrieval.go:2858), so
//     `parser.go` is stored as `parser` and `go`, and identifier parts are
//     added on top of that — while query.Tokens keeps `parser.go` as one token.
//
// The point of the table is the last row. The edit-distance walk visits only
// the length buckets within its limit of the query's length, which is what puts
// an Arabic word's marked form out of reach (#1941), and in ordinary English it
// closes exactly one direction: a query token longer than the stored one by
// more than that limit. `parser.go` is nine runes, `parser` is six, the buckets
// are seven to eleven, and the two are never compared.
func TestWhatAQueryStillReaches(t *testing.T) {
	for _, c := range []struct {
		name, stored, query string
		hits                int
		tier                string
	}{
		{"suffix, one letter", "parser", "parsers", 1, query.TierClose},
		{"suffix, two letters", "retry", "retries", 1, query.TierClose},
		{"suffix, three letters", "connect", "connecting", 1, query.TierClose},
		{"hyphen dropped", "work-tree", "worktree", 1, query.TierClose},
		{"hyphen added", "worktree", "work-tree", 1, query.TierClose},
		{"long word, one edit", "connection_pool", "connection_poll", 1, query.TierClose},
		{"long word, three edits", "connection_pool", "connectionpool", 1, query.TierClose},
		{"filename against its stem", "parser.go", "parser", 1, query.TierExact},
		{"long name against short stem", "connection_pool_manager.go", "connection", 1, query.TierExact},
		{"stem against its filename", "parser", "parser.go", 0, ""},
	} {
		dir := seedCleanWord(t, c.stored)
		res, err := SearchDetailed(dir, query.Options{Query: c.query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Sessions) != c.hits {
			t.Errorf("%s: %d hits, want %d", c.name, len(res.Sessions), c.hits)
		}
		if c.hits > 0 && res.Tier != c.tier {
			t.Errorf("%s: answered on the %q tier, want %q", c.name, res.Tier, c.tier)
		}
	}
}

// seedQuerySideCorpus writes sessions whose text is decomposed (NFD), the form
// a macOS path hands back, and returns the index dir.
func seedQuerySideCorpus(t *testing.T, sessions int) string {
	t.Helper()
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	const word = "cafe\u0301" // e + combining acute, written as escapes on purpose
	for s := range sessions {
		lines := make([]string, 0, 20)
		for i := range 20 {
			text := fmt.Sprintf("the %s parser retried %d times before the pool was exhausted", word, i)
			lines = append(lines, fmt.Sprintf(`{"type":"user","sessionId":"s%d","cwd":"/proj","timestamp":"2026-08-20T10:%02d:00Z","message":{"role":"user","content":%q}}`, s, i%60, text))
		}
		if err := os.WriteFile(filepath.Join(claude, fmt.Sprintf("s%d.jsonl", s)), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The per-record check ran the query side — parts, composed form, CJK-folded
// form — for every record it looked at, so a decomposed query, which takes the
// retry path on every record, allocated more than twice what the same search
// allocated with an ASCII query (49385 against 22984 over 120 sessions). The
// query does not change between records (#1885).
//
// Measured as a ratio against the plain query on the same corpus, so the
// assertion does not depend on the machine or on how much the reader's Go
// version allocates elsewhere. The threshold is 1.3 because the two searches
// allocate the same amount once the query side is shared — the ratio is 1.0
// here and 2.14 on the branch before this — and because -race and a low GOGC
// move both sides together (checked).
func TestTheQuerySideIsNotRebuiltForEveryRecord(t *testing.T) {
	dir := seedQuerySideCorpus(t, 40)
	allocs := func(q string) float64 {
		return testing.AllocsPerRun(5, func() {
			if _, err := Search(dir, query.Options{Query: q, All: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
	plain := allocs("pool exhausted")
	decomposed := allocs("cafe\u0301 parser")
	if plain == 0 {
		t.Fatal("the plain search allocated nothing, so the ratio below says nothing")
	}
	if ratio := decomposed / plain; ratio > 1.3 {
		t.Errorf("a decomposed query allocates %.2fx the plain one (%v against %v): the query side is being rebuilt per record",
			ratio, decomposed, plain)
	}
}

// The retry paths still do what they are for: a query typed in one normal form
// finds text stored in the other.
func TestADecomposedQueryStillFindsComposedText(t *testing.T) {
	dir := seedQuerySideCorpus(t, 2)
	for _, q := range []string{"cafe\u0301 parser", "caf\u00e9 parser"} {
		got, err := Search(dir, query.Options{Query: q, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Errorf("%+q found nothing", q)
		}
	}
}

// deja notices when someone asks the same thing again, by folding a question to
// a stem and matching stems. A stem needs five words, and Chinese, Japanese and
// Korean write no separator between them, so a question in those scripts was one
// word however much it asked and never got a stem at all.
func TestQuestionStemAcrossScripts(t *testing.T) {
	for name, q := range map[string]string{
		"english": "why does the scheduler run tasks twice",
		"chinese": "调度器为什么会重复执行任务",
		"korean":  "스케줄러가작업을두번실행하는이유는무엇입니까",
		"russian": "почему планировщик выполняет задачи дважды",
	} {
		if questionStem(q) == "" {
			t.Errorf("%s: no stem, so asking this twice is never noticed", name)
		}
		if _, ok := askedHashOf(q + "？"); !ok {
			t.Errorf("%s: not tracked as an asking at all", name)
		}
	}
}

// The bar still has to keep trivia out: "ok" and "继续" repeat in every session
// and mean nothing.
func TestShortAskingsStillHaveNoStem(t *testing.T) {
	for name, q := range map[string]string{
		"english short": "why not",
		"chinese short": "为什么",
		"russian short": "а почему",
	} {
		if got := questionStem(q); got != "" {
			t.Errorf("%s: got stem %q, want none", name, got)
		}
	}
}

// The same question asked twice folds to the same stem, which is the whole
// point of having one.
func TestSameChineseQuestionFoldsTogether(t *testing.T) {
	a, ok1 := askedHashOf("调度器为什么会重复执行任务？")
	b, ok2 := askedHashOf("调度器为什么会重复执行任务？")
	if !ok1 || !ok2 || a != b {
		t.Errorf("the same question hashed to %d and %d (ok %v, %v)", a, b, ok1, ok2)
	}
}

// qwenLine writes one transcript line in the shape qwen stores: the role is on
// the envelope and the text sits in message.parts.
func qwenLine(i int, text string) string {
	role, inner := "user", "user"
	if i%2 == 1 {
		role, inner = "assistant", "model"
	}
	b := mustJSON(map[string]any{
		"type": role, "sessionId": "s1", "timestamp": "2026-08-30T09:00:00.000Z",
		"message": map[string]any{"role": inner, "parts": []any{map[string]any{"text": text}}},
	})
	return string(b)
}

// qwen declares an offset parser and was not on the list of harnesses the
// append path will trust, so every pass re-read the whole file — the shape
// #2075 found on the database side (#2870).
//
// The list is per harness on purpose: appending onto a file that was rewritten
// would leave the old text in the index, so a harness joins it only with a
// fixture that shows both halves.
func TestAGrowingQwenSessionIsAppendedNotReread(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "none-claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "none-codex"))
	t.Setenv("DEJA_GOOSE_DB", filepath.Join(tmp, "none-goose.db"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none-opencode.db"))
	t.Setenv("DEJA_GROK_DB", filepath.Join(tmp, "none-grok.db"))
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(tmp, "none-hermes"))
	t.Setenv("DEJA_ZED_DB", filepath.Join(tmp, "none-zed.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	chats := filepath.Join(root, "projects", "-work-app", "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(chats, "s1.jsonl")
	write := func(lines []string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed := make([]string, 0, 6)
	for i := range 6 {
		seed = append(seed, qwenLine(i, fmt.Sprintf("seed %d zonkomatic", i)))
	}
	write(seed)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	hits := func(term string) int {
		t.Helper()
		got, err := Search(dir, search.Options{Query: term, All: true})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range got {
			for _, m := range s.Messages {
				if strings.Contains(m.Text, term) {
					n++
				}
			}
		}
		return n
	}
	if hits("seed 0 ") != 1 {
		t.Fatalf("the fixture did not index, so this measures nothing")
	}

	// A turn is appended: it has to arrive, and the earlier ones must not be
	// counted twice — a cursor that hands back what it already gave grows the
	// session on every pass (#2025).
	write(append(append([]string{}, seed...), qwenLine(6, "appended vantorquell")))
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("appended vantorquell"); n != 1 {
		t.Errorf("the appended turn is not indexed: %d", n)
	}
	if n := hits("seed 0 "); n != 1 {
		t.Errorf("an earlier turn is held %d times", n)
	}

	// And a rewind: the file is rewritten with different text and regrows past
	// its old length, which looks exactly like an append until the prefix is
	// compared. The old text must not survive.
	rewritten := make([]string, 0, 9)
	for i := range 9 {
		rewritten = append(rewritten, qwenLine(i, fmt.Sprintf("rewritten %d zonkomatic", i)))
	}
	write(rewritten)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := hits("seed 0 "); n != 0 {
		t.Errorf("text from before the rewrite survived in the index: %d", n)
	}
	if n := hits("rewritten 8 "); n != 1 {
		t.Errorf("the rewritten tail is not indexed: %d", n)
	}
}

func TestQwenChangedAndAppendedFiles(t *testing.T) {
	root := t.TempDir()
	setHome(t, filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	path := filepath.Join(root, "qwen", "projects", "-tmp-index", "chats", "session.jsonl")
	line := `{"type":"user","sessionId":"q","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","parts":[{"text":"index text"}]}}` + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := parseChangedFile("qwen", path, FileState{}); err != nil || len(got) != 1 {
		t.Fatalf("changed qwen = %#v, %v", got, err)
	}
	if got, err := parseAppendedFile("qwen", path, FileState{}, false); err != nil || len(got) != 1 {
		t.Fatalf("appended qwen = %#v, %v", got, err)
	}
}

// perFileProgress keeps what a build reported, so a test can ask when the
// bar moved rather than only where it ended.
type perFileProgress struct {
	mu     sync.Mutex
	phase  string
	order  []string
	steps  map[string][]int // phase -> the advances it received
	totals map[string]int
}

func newPerFileProgress() *perFileProgress {
	return &perFileProgress{steps: map[string][]int{}, totals: map[string]int{}}
}

func (r *perFileProgress) Phase(name string, total int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = name
	r.order = append(r.order, name)
	r.totals[name] = total
}

func (r *perFileProgress) Advance(units int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps[r.phase] = append(r.steps[r.phase], units)
}

func (r *perFileProgress) Harness(string, int, int) {}

func (r *perFileProgress) advances(phase string) []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.steps[phase]...)
}

// The read phase advanced once per store, so a machine whose corpus is mostly
// one harness watched "reading sessions 0%" until that harness finished — ten
// seconds of a thirty-second rebuild on a real store (#3372). It moves per
// file now, and a store still counts its weight exactly once.
func TestTheReadPhaseMovesPerFile(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude")
	at := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	const files = 6
	for i := range files {
		dir := filepath.Join(claude, "proj")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		line := fmt.Sprintf(`{"type":"user","sessionId":"quokkabloom-%d","timestamp":%q,`+
			`"message":{"role":"user","content":"the quokkabloom exporter drops spans"}}`, i, at)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("quokkabloom-%d.jsonl", i)), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	rec := newPerFileProgress()
	SetProgress(rec)
	defer SetProgress(nil)
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}

	got := rec.advances("reading sessions")
	if len(got) < files {
		t.Errorf("the read phase reported %d advances for %d files: %v", len(got), files, got)
	}
	sum := 0
	for _, n := range got {
		sum += n
	}
	if total := rec.totals["reading sessions"]; sum != total {
		t.Errorf("the read phase advanced %d of %d units — a store counted twice or not at all", sum, total)
	}
}

// The indexing phase counted sessions the moment they were handed to the
// spiller, so the bar read 1% and then 99% while the workers still had a third
// of the corpus to go. It counts messages now, reported as each batch is
// actually indexed (#3372).
func TestTheIndexingPhaseCountsMessagesAsTheyAreIndexed(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	const sessions, msgs = 3, 40
	for i := range sessions {
		var lines bytes.Buffer
		for k := range msgs {
			fmt.Fprintf(&lines,
				`{"type":"user","sessionId":"quokkabloom-%d","timestamp":%q,`+
					`"message":{"role":"user","content":"quokkabloom span %d of session %d"}}`+"\n",
				i, at, k, i)
		}
		if err := os.WriteFile(filepath.Join(claude, fmt.Sprintf("quokkabloom-%d.jsonl", i)), lines.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	rec := newPerFileProgress()
	SetProgress(rec)
	defer SetProgress(nil)
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	if total := rec.totals["indexing messages"]; total < sessions*msgs {
		t.Errorf("the phase counts %d units for %d messages — still counting sessions", total, sessions*msgs)
	}
	sum := 0
	for _, n := range rec.advances("indexing messages") {
		sum += n
	}
	if sum < sessions*msgs {
		t.Errorf("the phase advanced %d of %d messages", sum, sessions*msgs)
	}
}

// The four sidecar builders walked every session under the previous phase's
// last percentage, so a build stood still for the eight seconds they took on a
// real store (#3372). They are a phase of their own now.
func TestTheSidecarsAreAPhaseOfTheirOwn(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "proj")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"quokkabloom-side","timestamp":%q,`+
		`"message":{"role":"user","content":"the quokkabloom exporter drops spans"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "side.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	rec := newPerFileProgress()
	SetProgress(rec)
	defer SetProgress(nil)
	if err := Ensure(filepath.Join(tmp, "index.db"), "", true, nil); err != nil {
		t.Fatal(err)
	}
	const phase = "mining fixes and commands"
	if total := rec.totals[phase]; total != 5 {
		t.Fatalf("%q reports %d units, want one per builder", phase, total)
	}
	sum := 0
	for _, n := range rec.advances(phase) {
		sum += n
	}
	if sum != 5 {
		t.Errorf("%q advanced %d of 5 — a builder reports nothing", phase, sum)
	}
	// Mining runs before indexing so each session's messages can be dropped as
	// soon as its records and postings are handed to the spiller (#5571).
	minedAt, indexedAt := -1, -1
	for i, name := range rec.order {
		switch name {
		case phase:
			minedAt = i
		case "indexing messages":
			indexedAt = i
		}
	}
	if minedAt < 0 || indexedAt < 0 || minedAt >= indexedAt {
		t.Errorf("want %q before \"indexing messages\"; phase order was %v", phase, rec.order)
	}
}

// How old the index is and what this build may do with it are different
// questions. FormatDirection answers the first; the sentence doctor prints was
// written for the second, and driving it from the first called a store one
// content version behind unreadable while `--deep` re-parsed its sessions
// (#3597).
func TestReadStateOf(t *testing.T) {
	dir := t.TempDir()
	if got := ReadStateOf(dir); got != ReadStateCurrent {
		t.Errorf("no manifest = %v, want ReadStateCurrent", got)
	}
	for _, tc := range []struct {
		name    string
		version int
		format  int
		want    ReadState
	}{
		{"what this build writes", version, onDiskFormat, ReadStateCurrent},
		{"a layout this build cannot read", version, onDiskFormat + 1, ReadStateUnreadable},
		{"written by a newer deja", version + 1, onDiskFormat, ReadStateNewer},
		{"below the redaction floor", redactionFloor - 1, onDiskFormat, ReadStateWithheld},
		{"at the floor but behind", redactionFloor, onDiskFormat, floorState()},
		{"one derivation behind", version - 1, onDiskFormat, olderRulesState()},
	} {
		m := Manifest{Version: tc.version, Format: tc.format, Sessions: map[string]SessionMeta{}}
		if err := writeManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		if got := ReadStateOf(dir); got != tc.want {
			t.Errorf("%s (version %d, format %d) = %v, want %v",
				tc.name, tc.version, tc.format, got, tc.want)
		}
	}

	// The layout outranks the version: a store this build cannot open is not
	// merely holding text written under older rules.
	m := Manifest{Version: redactionFloor - 1, Format: onDiskFormat + 1, Sessions: map[string]SessionMeta{}}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := ReadStateOf(dir); got != ReadStateUnreadable {
		t.Errorf("an unreadable layout below the floor = %v, want ReadStateUnreadable", got)
	}
}

// floorState and olderRulesState keep the table honest as the constants move.
// A release where redactionFloor has caught up with version leaves no room for
// the states between them, and a table that hard-coded one would then be
// asserting something the build cannot produce.
func floorState() ReadState {
	if redactionFloor < version {
		return ReadStateOlderRules
	}
	return ReadStateCurrent
}

func olderRulesState() ReadState {
	if version-1 < redactionFloor {
		return ReadStateWithheld
	}
	return ReadStateOlderRules
}

// mustRebuildBeforeAnswering and ReadStateOf must not disagree: they are the
// same rule, and the two states that stop an answer are the two that force the
// rebuild (#3598 turned on exactly that pairing).
func TestReadStateAgreesWithMustRebuild(t *testing.T) {
	for _, m := range []Manifest{
		{Version: version, Format: onDiskFormat},
		{Version: version, Format: onDiskFormat + 1},
		{Version: version + 1, Format: onDiskFormat},
		{Version: redactionFloor - 1, Format: onDiskFormat},
		{Version: version - 1, Format: onDiskFormat},
	} {
		blocks := mustRebuildBeforeAnswering(m, version)
		dir := t.TempDir()
		m.Sessions = map[string]SessionMeta{}
		if err := writeManifest(dir, m); err != nil {
			t.Fatal(err)
		}
		state := ReadStateOf(dir)
		quiet := state == ReadStateWithheld || state == ReadStateUnreadable
		if blocks != quiet {
			t.Errorf("version %d format %d: mustRebuild=%v but state=%v",
				m.Version, m.Format, blocks, state)
		}
	}
}

// A version bump forces a rebuild, and during it every reading surface chooses
// between an answer under the old rules and no answer at all. ReadableSnapshot
// is that choice: the layout this build writes can be read, a layout it cannot
// be sure of cannot.
func TestReadableSnapshotSeparatesLayoutFromContent(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "readable")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	if !ReadableSnapshot(dir) {
		t.Fatal("a store this build just wrote reads as unreadable")
	}
	// What an upgrade leaves behind: stale content, this build's layout.
	if err := SetManifestVersionForTest(dir, CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if IsCurrentVersion(dir) {
		t.Fatal("the fixture is not one version behind")
	}
	if !ReadableSnapshot(dir) {
		t.Error("a stale content version made the layout unreadable")
	}
	// What a store written before the field existed carries.
	if err := SetManifestFormatForTest(dir, 0); err != nil {
		t.Fatal(err)
	}
	if ReadableSnapshot(dir) {
		t.Error("a store that cannot say its layout was read anyway")
	}
	// And a layout from some future build is equally not this one's.
	if err := SetManifestFormatForTest(dir, 999); err != nil {
		t.Fatal(err)
	}
	if ReadableSnapshot(dir) {
		t.Error("a layout this build does not write was read anyway")
	}
	// A directory with no manifest at all has nothing to read.
	if ReadableSnapshot(filepath.Join(tmp, "absent")) {
		t.Error("a missing store reads as readable")
	}
}

// AnswersWhileRebuilding is ReadableSnapshot with the redaction floor: a store
// below it reads, but must not be quoted while it is re-read.
func TestAnswersWhileRebuildingHoldsTheRedactionFloor(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "floor")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := SetManifestVersionForTest(dir, CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if !AnswersWhileRebuilding(dir) {
		t.Error("a store one content version behind does not answer")
	}
	if err := SetManifestVersionForTest(dir, redactionFloor-1); err != nil {
		t.Fatal(err)
	}
	if !ReadableSnapshot(dir) || AnswersWhileRebuilding(dir) {
		t.Error("a store below the redaction floor answers while rebuilding")
	}
	if err := SetManifestVersionForTest(dir, CurrentVersionForTest()-1); err != nil {
		t.Fatal(err)
	}
	if err := SetManifestFormatForTest(dir, 0); err != nil {
		t.Fatal(err)
	}
	if AnswersWhileRebuilding(dir) {
		t.Error("a layout this build cannot read answers while rebuilding")
	}
	if AnswersWhileRebuilding(filepath.Join(tmp, "absent")) {
		t.Error("a missing store answers")
	}
}

// A Claude session started from a home directory takes its project from the
// files it worked on. An appended tail with no files of its own names only the
// home directory, and that must not replace what the whole file said.
func TestAppendedTailKeepsTheProjectFromPaths(t *testing.T) {
	root, dir := allHarnessEnv(t)
	repo := filepath.Join(root, "code", "alpha")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "claude", "-home-me", "s.jsonl")
	head := claudeLineAt("s1", "2026-07-17T09:00:00Z", "projneedle look at alpha", "/home/me")
	var headSb23 strings.Builder
	for i, f := range []string{"a.go", "b.go", "c.go"} {
		headSb23.WriteString(claudeEditAt("s1", "2026-07-17T09:00:0"+string(rune('1'+i))+"Z", filepath.Join(repo, f), "/home/me"))
	}
	head += headSb23.String()
	write(t, p, head)
	o := search.Options{Query: "projneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	appendTo(t, p, claudeLineAt("s1", "2026-07-17T09:05:00Z", "projneedle now explain it", "/home/me"))
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	rebuilt := filepath.Join(t.TempDir(), "rebuilt")
	if err := EnsureForSearch(rebuilt, o, true, nil); err != nil {
		t.Fatal(err)
	}
	project := func(dir string) string {
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions["claude:s1"].Project
	}
	if got, want := project(dir), project(rebuilt); got != want || !strings.HasSuffix(want, "alpha") {
		t.Errorf("after the append the project is %q, a rebuild says %q", got, want)
	}
}

// A last line that parses but has no newline yet is indexed; the next pass
// must not read it again when the writer finishes the line and appends more.
func TestUnterminatedLastLineIsIndexedOnce(t *testing.T) {
	root, dir := allHarnessEnv(t)
	p := filepath.Join(root, "claude", "-tmp-p", "s.jsonl")
	first := claudeLine("s1", "2026-01-02T03:04:05Z", "dupneedle first")
	second := strings.TrimSuffix(claudeLine("s1", "2026-01-02T03:04:06Z", "dupneedle second"), "\n")
	write(t, p, first+second)

	o := search.Options{Query: "dupneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir, o); err != nil || len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("baseline: %#v err=%v", ss, err)
	}
	appendTo(t, p, "\n"+claudeLine("s1", "2026-01-02T03:04:07Z", "dupneedle third"))
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "dupneedle", All: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, s := range ss {
		for _, m := range s.Messages {
			texts = append(texts, m.Text)
		}
	}
	if len(texts) != 3 {
		t.Fatalf("after append: %d messages %q, want 3 (first, second, third once each)", len(texts), texts)
	}
}

// A container with a read-only mount, or a locked-down work machine, can still
// answer every question its index already holds. deja used to stop instead:
// `deja "query"` died on `open …/idx.lock: permission denied` while the index
// beside it was complete and readable.
func TestReadOnlyIndexStillAnswers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test relies on")
	}
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	writeLines(t, filepath.Join(claude, "project", "s.jsonl"),
		claudeLine("s1", "2026-02-01T00:01:00Z", "READONLY probe"))

	// Build the index somewhere writable, then take away the ability to
	// create the lock file beside it.
	locked := filepath.Join(home, "locked")
	dir := filepath.Join(locked, "idx")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	// The build left a lock file behind; remove it so the assertion below
	// is about what the read path does, not what the build did.
	_ = os.Remove(dir + ".lock")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	// The freshness check cannot run, and that is not a reason to refuse.
	if err := EnsureForSearch(dir, query.Options{Query: "readonly", All: true}, false, io.Discard); err != nil {
		t.Fatalf("EnsureForSearch on a read-only index: %v", err)
	}
	got, err := Search(dir, query.Options{Query: "readonly", All: true})
	if err != nil {
		t.Fatalf("search on a read-only index: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("read-only index answered with nothing")
	}

	// The lock file must not have been created, or the fallback is really a
	// write that happened to succeed.
	if _, err := os.Stat(dir + ".lock"); err == nil {
		t.Fatal("a lock file appeared in a read-only directory")
	}
}

// The fallback is for permission alone. Every other failure to take the lock
// still has to surface, or a broken index reads as an empty one.
func TestLockFailuresOtherThanPermissionStillSurface(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// A path whose parent is a file, not a directory: MkdirAll fails with
	// something that is not a permission problem.
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, err := tryLockDir(filepath.Join(blocker, "idx"))
	if err == nil {
		t.Fatal("a structurally impossible lock path reported no error")
	}
	if ok {
		t.Fatal("reported holding a lock it could not take")
	}
}

// A recall whose words never meet as typed walks the part-of-word step, then
// ranks, then hangs that ranking under a thin answer. Each step used to read
// what the one before it had already read (#5378).
func recallReadsStore(t *testing.T) string {
	t.Helper()
	h := newTwoWayEnv(t)
	// Past tailFloor, so a thin answer gets a ranked tail under it.
	for i, topic := range []string{"pool", "queue", "cache", "outbox", "router", "parser", "mailer", "billing", "search", "upload", "worker", "gateway"} {
		sid := fmt.Sprintf("r%d", i)
		h.put("app", sid, richSession("app", sid, 2+i%20, topic))
	}
	h.put("svc", "s5", hPrompt("svc", "s5", 6, 0, "the frobnicator handshake times out after the deploy")+
		hSay("svc", "s5", 6, 1, "The handshake timeout comes from the frobnicator retry loop."))
	// One session holding most of the log, the shape of a marathon.
	var long strings.Builder
	long.WriteString(hPrompt("svc", "s4", 5, 0, "the ledger marathon starts here"))
	for i := 1; i < 120; i++ {
		long.WriteString(hSay("svc", "s4", 5, i%60, fmt.Sprintf("ledger step %d of the marathon", i)))
	}
	h.put("svc", "s4", long.String())
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestOneRankingPerLadderPass(t *testing.T) {
	dir := recallReadsStore(t)
	before := relevanceRuns.Load()
	res, err := searchDetailedRead(dir, query.Options{Query: "frobnicat handshake times"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("the part-of-word step found nothing; the test no longer reaches the tail")
	}
	if n := relevanceRuns.Load() - before; n != 1 {
		t.Errorf("one pass of the ladder ranked the store %d times, want 1", n)
	}
}

func TestPartOfWordStepReadsTheCatalogsDirectories(t *testing.T) {
	dir := recallReadsStore(t)
	if _, err := tokenIndexCached(dir); err != nil {
		t.Fatal(err)
	}
	bare := []string{"zorble", "frobnic"}
	before := bucketDirReads.Load()
	posts, variants, err := intersectSubstringPostingsDetailed(dir, bare)
	if err != nil {
		t.Fatal(err)
	}
	if n := bucketDirReads.Load() - before; n != 0 {
		t.Errorf("the part-of-word step read %d bucket directories off disk with the catalog cached, want 0", n)
	}
	if len(posts) == 0 {
		t.Fatal("no postings; the comparison below would be vacuous")
	}
	// The same answer as reading every directory off disk.
	idx, _ := tokenIndexCached(dir)
	paths := make([]bucketDir, 0, len(idx.dirs))
	for _, d := range idx.dirs {
		paths = append(paths, bucketDir{path: d.path})
	}
	wantPosts, wantVariants, _, err := substringPostingsFrom(paths, bare, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(posts, wantPosts) || fmt.Sprint(variants) != fmt.Sprint(wantVariants) {
		t.Errorf("catalog read gives %v %v, disk read %v %v", posts, variants, wantPosts, wantVariants)
	}
}

func TestOneLongSessionLoadsThroughTheTable(t *testing.T) {
	dir := recallReadsStore(t)
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, r := range recs {
		if r.Record.Key == "claude:s4" {
			want = append(want, r.Record.Role+"|"+r.Record.Text)
		}
	}
	if int64(len(want))*offsetsWalkShare <= int64(len(recs)) {
		t.Fatalf("s4 holds %d of %d records, under the walk share; the test proves nothing", len(want), len(recs))
	}
	before := atomic.LoadInt64(&recordOffsetReads)
	s, ok, err := FindByIdentity(dir, "claude", "s4")
	if err != nil || !ok {
		t.Fatalf("load s4: %v %v", ok, err)
	}
	if atomic.LoadInt64(&recordOffsetReads) == before {
		t.Error("one long session walked the whole log instead of reading its own records")
	}
	got := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		got = append(got, m.Role+"|"+m.Text)
	}
	if !slices.Equal(got, want) {
		t.Errorf("table read gives %d records, the walk %d", len(got), len(want))
	}
}

// The close-spelling step stops comparing a word once it is past the limit;
// within it, the distance is the one the full table gives.
func TestDamerauRunesStopsOnlyPastTheLimit(t *testing.T) {
	full := func(a, b []rune) int {
		d := make([][]int, len(a)+1)
		for i := range d {
			d[i] = make([]int, len(b)+1)
			d[i][0] = i
		}
		for j := range d[0] {
			d[0][j] = j
		}
		for i := 1; i <= len(a); i++ {
			for j := 1; j <= len(b); j++ {
				cost := 1
				if a[i-1] == b[j-1] {
					cost = 0
				}
				d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
				if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
					d[i][j] = min(d[i][j], d[i-2][j-2]+1)
				}
			}
		}
		return d[len(a)][len(b)]
	}
	alphabet := []rune("абвгдеж")
	rng := rand.New(rand.NewSource(5378)) //nolint:gosec // a seeded fixture, not crypto
	word := func() []rune {
		w := make([]rune, 1+rng.Intn(12))
		for i := range w {
			w[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return w
	}
	for range 20000 {
		a, b := word(), word()
		for _, limit := range []int{1, 2} {
			want := full(a, b)
			got := damerauDistanceRunes(string(a), string(b), limit)
			if (want <= limit) != (got <= limit) || (want <= limit && got != want) {
				t.Fatalf("damerauDistanceRunes(%q, %q, %d) = %d, full table %d", string(a), string(b), limit, got, want)
			}
		}
	}
}

func TestReadRecordsAtReadsRequestedOffsets(t *testing.T) {
	dir := recallReadsStore(t)
	all, err := ReadRecords(dir)
	if err != nil || len(all) < 3 {
		t.Fatalf("ReadRecords = %d, err = %v", len(all), err)
	}
	before := RecordLogScans()
	got, err := ReadRecordsAt(dir, []int64{all[2].Offset, all[0].Offset, all[2].Offset})
	if err != nil {
		t.Fatal(err)
	}
	if RecordLogScans() != before {
		t.Fatalf("ReadRecordsAt scanned the full log (%d -> %d)", before, RecordLogScans())
	}
	if len(got) != 2 || got[0].Offset != all[0].Offset || got[0].Record.Text != all[0].Record.Text || got[1].Offset != all[2].Offset || got[1].Record.Text != all[2].Record.Text {
		t.Fatalf("ReadRecordsAt = %+v, want [%+v, %+v]", got, all[0], all[2])
	}
	if _, err := ReadRecordsAt(filepath.Join(t.TempDir(), "missing"), []int64{0}); err == nil {
		t.Fatal("missing index directory was accepted")
	}
}

// Every case here is a line a real week actually printed before the trimming
// existed (#544).
func TestARecapLineIsTrimmedNotRewritten(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "a sentence that ran into a heading",
			in:   "the provider pass has to run before the assignment rules ## Next",
			want: "the provider pass has to run before the assignment rules",
		},
		{
			name: "a sentence that swallowed a command",
			in:   "``` $ registry/cursor.html sitemap=2026-07-17 git=2026-09-21 ```",
			want: "",
		},
		{
			name: "a list marker from the message it came from",
			in:   "- the watchdog now waits twelve minutes before it calls a reconnect broken",
			want: "the watchdog now waits twelve minutes before it calls a reconnect broken",
		},
		{
			name: "a sentence that began inside a bold run",
			in:   "who stars it.** the radar now takes fresh stars with their profiles",
			want: "the radar now takes fresh stars with their profiles",
		},
		{
			name: "a fragment",
			in:   "fixed that",
			want: "",
		},
		{
			name: "the agent narrating its own turn",
			in:   "Report delivered to team-lead in full, with the architecture map and the coverage numbers",
			want: "",
		},
		{
			name: "an instruction it was given",
			in:   "Verify important conclusions by reading the actual source files before answering",
			want: "",
		},
		{
			name: "the agent announcing its next step",
			in:   "Now let me verify one more potential issue — whether the append path can have kept files",
			want: "",
		},
		{
			name: "deja's own recall, quoted back",
			in:   "déjà vu: tokscale was already configured for local usage — reusing it (deja:ses_f45a9)",
			want: "",
		},
		{
			name: "subagent launch narration",
			in:   "I have launched a background research agent to check the deployment state on the server.",
			want: "",
		},
		{
			name: "sentence running into a markdown table row",
			in:   "Body proportions now match 100% across all frames: | build | slim | slim |",
			want: "Body proportions now match 100% across all frames:",
		},
		{
			name: "shell pipe inside backticks is kept whole",
			in:   "Fixed the desktop overflow so `curl -fsSL https://example.com/install.sh | sh` fits on one line.",
			want: "Fixed the desktop overflow so `curl -fsSL https://example.com/install.sh | sh` fits on one line.",
		},
	}
	for _, c := range cases {
		if got := trimRecapLine(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// A line over the ceiling is cut at a sentence end, not mid-word, and never
// rewritten.
func TestALongLineIsCutAtASentence(t *testing.T) {
	first := strings.Repeat("the pool was exhausted and the retry budget was wrong. ", 3)
	got := trimRecapLine(first + strings.Repeat("tail ", 40))
	if got == "" {
		t.Fatal("a long line was dropped instead of cut")
	}
	if len([]rune(got)) > recapLineMax {
		t.Errorf("the cut line is %d runes, over the %d ceiling: %q", len([]rune(got)), recapLineMax, got)
	}
	if !strings.HasSuffix(got, ".") {
		t.Errorf("the line was not cut at a sentence: %q", got)
	}
}

// One session states a conclusion in three messages, each adding a clause. All
// three used to be rows, because a prefix comparison catches none of them —
// the restatement begins differently every time.
func TestOneConclusionStatedTwiceIsOneLine(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Now().Add(-2 * time.Hour)
	say := func(text string, minute int) model.Message {
		return model.Message{Role: "assistant", Text: text, Time: at.Add(time.Duration(minute) * time.Minute)}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s1", Harness: "claude", Project: "work/app", Path: "/tmp/s1.jsonl", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the anomaly on the upper stroke", Time: at},
			say("fixed the anomaly: the cause was the static-shape adapter taking any ambiguous form", 1),
			say("the cause turned out to be mine: the static-shape adapter takes any ambiguous form in frame", 2),
			say("decided: the retry budget stays at four attempts because a duplicate is worse than a drop", 3),
		},
	}})
	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 1 {
		t.Fatalf("want one session with lines, got %d: %+v", len(r.Sessions), r.Sessions)
	}
	lines := r.Sessions[0].Lines
	if len(lines) == 0 {
		t.Fatal("the fixture produced no lines at all, so this test proves nothing")
	}
	if len(lines) != 2 {
		t.Fatalf("want two lines — the restatement collapsed — got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if r.Considered != 1 || r.Spoke != 1 {
		t.Errorf("the window counts are wrong: considered=%d spoke=%d", r.Considered, r.Spoke)
	}
}

// The requirement nobody had scoped: this text is written to be pasted, so it
// goes through the outbound pass and the count of what that masked is part of
// the answer.
func TestARecapMasksWhatIdentifiesTheMachine(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Now().Add(-time.Hour)
	writeStore(t, dir, []model.Session{{
		ID: "s2", Harness: "claude", Project: "work/net", Path: "/tmp/s2.jsonl", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the handshake hangs", Time: at},
			{Role: "assistant", Time: at.Add(time.Minute),
				Text: "root cause: the default route moved to 10.14.3.7 while the source address stayed on the old subnet"},
			{Role: "assistant", Time: at.Add(2 * time.Minute),
				Text: "the fix landed in /Users/rivera/src/net/route.go and the probe now names gw.internal instead"},
		},
	}})
	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(allRecapLines(r), "\n")
	if all == "" {
		t.Fatal("the fixture produced no lines at all, so this test proves nothing")
	}
	for _, gone := range []string{"10.14.3.7", "/Users/rivera", "gw.internal"} {
		if strings.Contains(all, gone) {
			t.Errorf("%q reached outbound text:\n%s", gone, all)
		}
	}
	if r.Masked.Total() == 0 {
		t.Errorf("nothing was reported as masked: %v", r.Masked)
	}
}

// A project named after the home directory carries an account name, and the
// heading of text written to be pasted is the last place for one.
func TestAHomeDirectoryIsNotAProjectName(t *testing.T) {
	if !RecapProjectIsHome("Users/rivera") {
		t.Error("a home path was treated as a project")
	}
	if !RecapProjectIsHome("home/rivera") {
		t.Error("a linux home path was treated as a project")
	}
	if RecapProjectIsHome("goprojects/deja-vu") {
		t.Error("a real project was called a home directory")
	}
}

// A line too long to print is cut, and where it is cut decides whether the
// result reads as a sentence or as a bug. Three shapes reach this: a sentence
// end late enough to keep, none at all, and a single token with no space to
// fall back to — the last one is a URL or a path, which a real week prints.
func TestALineTooLongIsCutWhereItCanBe(t *testing.T) {
	long := strings.Repeat("token ", 60) // 360 runes, no sentence end
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "cut at the sentence end",
			in:   strings.Repeat("a", 150) + ". " + strings.Repeat("b", 120),
			want: strings.Repeat("a", 150) + ".",
		},
		{
			name: "no sentence end, so the last word and an ellipsis",
			in:   long,
			want: strings.TrimSpace(long[:215]) + "…",
		},
		{
			name: "one long token has no word to cut at",
			in:   strings.Repeat("x", 300),
			want: strings.Repeat("x", 220) + "…",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cutAtSentence(c.in, recapLineMax)
			if got != c.want {
				t.Errorf("cut to %d runes:\n got %q\nwant %q", recapLineMax, got, c.want)
			}
		})
	}
}

// The CLI passes its own default through, and a caller that passes none — a
// script, or the MCP surface — must not get a session with no lines at all.
func TestAskingForNoLinesPerSessionStillGivesSome(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Now().Add(-time.Hour)
	writeStore(t, dir, []model.Session{{
		ID: "s3", Harness: "claude", Project: "work/app", Path: "/tmp/s3.jsonl", Updated: at,
		Messages: []model.Message{
			{Role: "user", Text: "the queue drains before the worker exits", Time: at},
			{Role: "assistant", Time: at.Add(time.Minute),
				Text: "decided: the worker waits for the queue to drain, because a dropped job costs more than a slow shutdown"},
		},
	}})
	r, err := ScanRecap(dir, 24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 1 || len(r.Sessions[0].Lines) == 0 {
		t.Fatalf("a zero budget produced nothing: %+v", r.Sessions)
	}
}

// A wide window on a busy machine is thousands of sessions, and reading them
// all to print twelve is wasted work — so the scan stops at the cap. What the
// cap must not do is stay quiet about it: the counts above the lines are the
// window, and a reader comparing them has to know only the newest were read.
func TestAWideWindowStopsAtTheCapAndSaysSo(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	now := time.Now()
	sessions := make([]model.Session, 0, recapSessionCap+20)
	for i := range recapSessionCap + 20 {
		at := now.Add(-time.Duration(i) * time.Minute)
		sessions = append(sessions, model.Session{
			ID:      fmt.Sprintf("s%03d", i),
			Harness: "claude", Project: "work/app",
			Path:    fmt.Sprintf("/tmp/s%03d.jsonl", i),
			Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "the worker stalls on the third batch", Time: at},
				{Role: "assistant", Time: at,
					Text: fmt.Sprintf("decided: batch %d keeps its own cursor, because one shared cursor rewound every retry", i)},
			},
		})
	}
	writeStore(t, dir, sessions)

	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Considered != recapSessionCap+20 {
		t.Errorf("considered=%d, want the whole window %d", r.Considered, recapSessionCap+20)
	}
	if r.Read != recapSessionCap {
		t.Errorf("read=%d, want the cap %d named out loud", r.Read, recapSessionCap)
	}
	if r.Spoke > recapSessionCap {
		t.Errorf("spoke=%d, which is more sessions than were read", r.Spoke)
	}
	// The newest end of the window, not an arbitrary slice of it: session s000
	// is the most recent one.
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "s000" {
		t.Errorf("the newest session is not first: %+v", r.Sessions[:min(2, len(r.Sessions))])
	}
}

// A recap is text for other people, so an ignored project must not reach it —
// and the count has to say one was held back, otherwise the window looks
// smaller than it is for no stated reason.
func TestAnIgnoredProjectIsWithheldAndCounted(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	pol := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(pol, []byte(`{"ignore":["*/secret-client/*","secret-client"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", pol)

	at := time.Now().Add(-time.Hour)
	say := func(text string) model.Message {
		return model.Message{Role: "assistant", Text: text, Time: at.Add(time.Minute)}
	}
	writeStore(t, dir, []model.Session{
		{
			ID: "open", Harness: "claude", Project: "work/app", Path: "/tmp/open.jsonl", Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "the upload retries forever", Time: at},
				say("decided: the upload gives up after four attempts, because a stuck job blocks the queue behind it"),
			},
		},
		{
			ID: "closed", Harness: "claude", Project: "secret-client", Path: "/tmp/secret-client/s.jsonl", Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "the invoice total is off by a cent", Time: at},
				say("root cause: the rounding happened twice, once per currency conversion in the invoice path"),
			},
		},
	})

	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Withheld != 1 {
		t.Errorf("withheld=%d, want the ignored project counted once", r.Withheld)
	}
	if r.Considered != 1 {
		t.Errorf("considered=%d, want only the session that is not ignored", r.Considered)
	}
	all := strings.Join(allRecapLines(r), "\n")
	if all == "" {
		t.Fatal("the fixture produced no lines at all, so this test proves nothing")
	}
	if strings.Contains(all, "invoice") {
		t.Errorf("the ignored project's text reached the recap:\n%s", all)
	}
}

// The other spelling of a home directory in the manifest: not the path, the
// bare base name, which is an account name with nothing around it.
func TestTheBareHomeDirectoryNameIsAlsoNotAProject(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory to compare against")
	}
	if base := filepath.Base(home); !RecapProjectIsHome(base) {
		t.Errorf("%q is the home directory's own name and was treated as a project", base)
	}
	if RecapProjectIsHome("") {
		t.Error("a session with no project at all was called a home directory")
	}
}

func allRecapLines(r Recap) []string {
	var out []string
	for _, s := range r.Sessions {
		out = append(out, s.Lines...)
	}
	return out
}

func TestCutAtSentenceDoesNotCutInsideURLOrFilename(t *testing.T) {
	in := strings.Repeat("a", 150) + " see [subagents.jsonl](https://github.com/vshulcz/deja-vu/pull/5762) and more text after the link that pushes it over two hundred and twenty runes"
	got := cutAtSentence(in, recapLineMax)
	if strings.HasSuffix(got, "github.") || strings.HasSuffix(got, "subagents.") {
		t.Fatalf("cutAtSentence treated a dot inside a URL or filename as a sentence end: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected word-boundary ellipsis cut, got %q", got)
	}
}

func TestChineseRecapLineAndDeduplication(t *testing.T) {
	line1 := trimRecapLine("决定：将数据库连接池的空闲超时改为三十秒以避免连接泄漏。")
	if line1 == "" {
		t.Fatal("Chinese conclusion of 27 runes was dropped by trimRecapLine")
	}
	if noise := trimRecapLine("已启动子代理正在等待后台任务完成。"); noise != "" {
		t.Fatalf("Chinese subagent process noise survived trimRecapLine: %q", noise)
	}
	w1 := recapWords("决定：将数据库连接池的空闲超时改为三十秒。")
	w2 := recapWords("結論：將數據庫連接池的空閒超時改為三十秒，並增加了測試。")
	if !sameThoughtTwice([]map[string]bool{w1}, w2) {
		t.Errorf("restated Chinese conclusion (Traditional vs Simplified) was not deduplicated: w1=%v w2=%v", w1, w2)
	}
}

func TestScanRecapSkipsSubagentsAndSidechains(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	at := time.Now().Add(-time.Hour)
	writeStore(t, dir, []model.Session{
		{
			ID: "main-sess", Harness: "antigravity", Project: "work/app", Path: "/tmp/main.jsonl", Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "fix the pool leak", Time: at},
				{Role: "assistant", Text: "decided: cap idle connections at four to stop the pool leak on reconnect", Time: at.Add(time.Minute)},
			},
		},
		{
			ID: "sub-sess", Harness: "antigravity", Kind: "subagent", Parent: "main-sess", Project: "work/app", Path: "/tmp/sub.jsonl", Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "find the pool leak", Time: at},
				{Role: "assistant", Text: "root cause: the idle connection cap was zero in the worker config", Time: at.Add(time.Minute)},
			},
		},
	})
	r, err := ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Considered != 1 || len(r.Sessions) != 1 || r.Sessions[0].ID != "main-sess" {
		t.Fatalf("expected only main-sess in recap, got considered=%d sessions=%+v", r.Considered, r.Sessions)
	}
}

// receiptTurns writes, from minute at on, a person asking, a test run that
// fails, and the model saying what failed: the line a receipt backs.
func receiptTurns(t *testing.T, path, id string, at int, test string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	ts := func(i int) string {
		return time.Date(2026, 3, 4, 10, at+i, 0, 0, time.UTC).Format(time.RFC3339)
	}
	tid := fmt.Sprintf("toolu_%s_%d", id, at)
	for i, rec := range []map[string]any{
		{"type": "user", "message": map[string]any{"role": "user", "content": "why does " + test + " fail in slug_test.go"}},
		{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{
			"type": "tool_use", "id": tid, "name": "Bash", "input": map[string]any{"command": "go test -run " + test + " ./slug"}}}}},
		{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": tid, "content": "--- FAIL: " + test + " (0.00s)\n    slug_test.go:41: got \"a-b\" want \"a_b\"\nFAIL", "is_error": true}}}},
		{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{
			"type": "text", "text": test + " failed at slug_test.go:41 because the separator is a hyphen, want a_b"}}}},
	} {
		rec["sessionId"] = id
		rec["timestamp"] = ts(i)
		rec["cwd"] = "/work/app"
		rec["entrypoint"] = "cli"
		rec["uuid"] = fmt.Sprintf("%s-%d-%d", id, at, i)
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func receiptOf(t *testing.T, dir, id, test string) []string {
	t.Helper()
	r := evidence.Open(dir)
	if r == nil {
		t.Fatalf("no receipts in %s", dir)
	}
	return r.Lookup("claude", id, test+" failed at slug_test.go:41 because the separator is a hyphen, want a_b")
}

// A session that arrives after the last full build gets its receipts in the
// next ordinary pass, both as a new transcript and as turns appended to one
// the index already holds, and the file is the one a rebuild writes.
func TestReceiptsFollowIncrementalPasses(t *testing.T) {
	dir, proj := pairStore(t)
	receiptTurns(t, filepath.Join(proj, "one.jsonl"), "one", 0, "TestSlugEmpty")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := receiptOf(t, dir, "one", "TestSlugEmpty"); len(got) == 0 {
		t.Fatal("the first build wrote no receipt for its session")
	}

	// A new transcript, then more turns in the first one.
	receiptTurns(t, filepath.Join(proj, "two.jsonl"), "two", 0, "TestSlugUnicode")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	receiptTurns(t, filepath.Join(proj, "one.jsonl"), "one", 10, "TestSlugTrim")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ id, test string }{{"one", "TestSlugEmpty"}, {"two", "TestSlugUnicode"}, {"one", "TestSlugTrim"}} {
		got := receiptOf(t, dir, c.id, c.test)
		if len(got) == 0 || !strings.Contains(got[0], c.test) {
			t.Errorf("%s/%s: receipt after the ordinary pass = %q, want the failing run", c.id, c.test, got)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"claude:one", "claude:two"} {
		if !evidence.Open(dir).Covers(m.Sessions[key].Updated) {
			t.Errorf("%s: receipts do not cover the session after the pass that changed it", key)
		}
	}

	inc, err := os.ReadFile(filepath.Join(dir, "receipts.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(filepath.Join(dir, "receipts.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inc, full) {
		t.Errorf("receipts after the ordinary passes (%d bytes) differ from a rebuild's (%d bytes)", len(inc), len(full))
	}
}

// Without receipts from a full build, an ordinary pass writes none: a file
// with only the new session's would cover every other one.
func TestIncrementalPassWritesNoPartialReceipts(t *testing.T) {
	dir, proj := pairStore(t)
	receiptTurns(t, filepath.Join(proj, "one.jsonl"), "one", 0, "TestSlugEmpty")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "receipts.bin")); err != nil {
		t.Fatal(err)
	}
	receiptTurns(t, filepath.Join(proj, "two.jsonl"), "two", 0, "TestSlugUnicode")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "receipts.bin")); err == nil {
		t.Error("an ordinary pass wrote receipts for its own sessions alone")
	}
}

// #2070 put the ignore rule where metas turn into servable sessions so it would
// hold on every surface. The manifest walk was not one of them: `Recent` and the
// per-project variants build their result straight from the manifest, so
// `deja last` led with 253 rows out of 400 from the tree deja says it does not
// recall from, and the same helpers feed the session-start block, the brief,
// handoff, the page and the MCP resource list (#2541).
func TestTheRecentListingSkipsIgnoredSessions(t *testing.T) {
	tmp := t.TempDir()
	jobs := filepath.Join(tmp, "home", ".claude", "jobs", "abc", "projects", "-w-app")
	realPath := filepath.Join(tmp, "home", "projects", "-w-app")
	for _, d := range []string{jobs, realPath} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	line := func(sid, role, text string) string {
		return `{"type":"` + role + `","sessionId":"` + sid + `","cwd":"/w/app",` +
			`"timestamp":"2026-01-02T03:04:05Z","message":{"role":"` + role +
			`","content":"` + text + `"}}`
	}
	write := func(dir, sid, text string) {
		body := strings.Join([]string{
			line(sid, "user", text),
			line(sid, "assistant", "Decision: we pinned the frobnicator to one shard."),
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(jobs, "scratch", "the frobnicator keeps dropping its widgets under load")
	write(realPath, "keeper", "the frobnicator keeps dropping its widgets under load")

	// Both roots are read: the ignored one is a subtree of the store, which is
	// how it looks on a real machine.
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "home"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	recent, err := Recent(dir, 50)
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the session that is not ignored is listed at all.
	found := false
	for _, s := range recent {
		if s.ID == "keeper" {
			found = true
		}
		if s.ID == "scratch" {
			t.Errorf("the listing served an ignored session: %s (%s)", s.ID, s.Path)
		}
	}
	if !found {
		t.Fatalf("the real session is not in the listing of %d, so this measures nothing", len(recent))
	}

	inProject, err := RecentInProject(dir, "w/app", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range inProject {
		if s.ID == "scratch" {
			t.Errorf("RecentInProject served an ignored session: %s", s.Path)
		}
	}
	byName, err := RecentProject(dir, "app", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range byName {
		if s.ID == "scratch" {
			t.Errorf("RecentProject served an ignored session: %s", s.Path)
		}
	}
	many, err := RecentProjects(dir, []string{"w/app", "app"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range many {
		if s.ID == "scratch" {
			t.Errorf("RecentProjects served an ignored session: %s", s.Path)
		}
	}

	// Named directly, it still answers: a person asking for a session by id is
	// asking for it.
	if _, ok, err := FindByIdentity(dir, "claude", "scratch"); err != nil || !ok {
		t.Errorf("an ignored session named by id did not answer: ok=%v err=%v", ok, err)
	}
}

// RecentInProject is the scoped sibling of RecentProject: it answers with the
// sessions of this project, not of every project whose name contains the
// string. `deja handoff` packaged a client's acme/api from a directory named
// api through the loose one (#2336).
func TestRecentInProjectStaysInsideTheProject(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	dir := filepath.Join(tmp, "index.db")
	write := func(dirName, id, cwd string, hoursAgo int) {
		d := filepath.Join(root, dirName)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
		line := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":%q,"cwd":%q,`+
			`"message":{"role":"user","content":"a question about %s"}}`, id, at, cwd, id)
		if err := os.WriteFile(filepath.Join(d, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Three sessions in the client's project, all newer than the one that
	// belongs to this checkout: a window taken before scoping would hold none
	// of ours.
	write("-clients-acme-api", "acme1", "/clients/acme/api", 1)
	write("-clients-acme-api", "acme2", "/clients/acme/api", 2)
	write("-clients-acme-api", "acme3", "/clients/acme/api", 3)
	write("-work-api", "mine", "/work/api", 9)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	loose, err := RecentProject(dir, "api", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(loose) != 4 {
		t.Fatalf("the loose helper found %d sessions, want all four — otherwise this measures nothing", len(loose))
	}

	got, err := RecentInProject(dir, "work/api", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("RecentInProject = %v, want this checkout's own session", got)
	}
	// The limit still applies within the project.
	got, err = RecentInProject(dir, "acme/api", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "acme1" {
		t.Fatalf("RecentInProject with a limit = %v, want the two newest of that project", got)
	}
	// A name nothing answers to is empty rather than everything.
	got, err = RecentInProject(dir, "nothing/here", 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("RecentInProject for an unknown project = %v, err %v", got, err)
	}
}

func TestNewestFirstMetaIsATotalOrder(t *testing.T) {
	early := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	meta := func(h, id string, at time.Time) SessionMeta {
		return SessionMeta{Harness: h, ID: id, Updated: at}
	}
	cases := []struct {
		name string
		a, b SessionMeta
		want bool
	}{
		{"newer first", meta("claude", "z", late), meta("claude", "a", early), true},
		{"older last", meta("claude", "a", early), meta("claude", "z", late), false},
		{"tie falls to harness", meta("claude", "z", early), meta("cursor", "a", early), true},
		{"tie falls to harness, reversed", meta("cursor", "a", early), meta("claude", "z", early), false},
		{"tie falls to id", meta("claude", "a", early), meta("claude", "b", early), true},
		{"tie falls to id, reversed", meta("claude", "b", early), meta("claude", "a", early), false},
		{"identical is not less", meta("claude", "a", early), meta("claude", "a", early), false},
	}
	for _, c := range cases {
		if got := newestFirstMeta(c.a, c.b); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
		s := func(m SessionMeta) model.Session {
			return model.Session{Harness: m.Harness, ID: m.ID, Updated: m.Updated}
		}
		if got := newestFirstSession(s(c.a), s(c.b)); got != c.want {
			t.Errorf("%s (session): got %v", c.name, got)
		}
	}
}

// A store where several sessions share a stamp — a day's work imported in one
// go, a harness that stamps per-day, transcripts restored from a backup —
// reordered itself on every run: six calls to `deja last`, six answers (#713).
func TestRecentIsStableWhenTimestampsTie(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Enough sessions that map order actually varies between reads.
	for i := range 60 {
		id := "s" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		body := `{"type":"user","sessionId":"` + id + `","cwd":"/w/p","timestamp":"2026-08-01T09:00:00Z","message":{"role":"user","content":"work ` + id + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	first, err := RecentMatching(dir, 5, query.Options{})
	if err != nil || len(first) != 5 {
		t.Fatalf("%d sessions err=%v", len(first), err)
	}
	for run := range 20 {
		got, err := RecentMatching(dir, 5, query.Options{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if got[i].ID != first[i].ID {
				t.Fatalf("run %d position %d: %q, first run had %q", run, i, got[i].ID, first[i].ID)
			}
		}
	}
}

func TestReadRecordRejectsHugeLengthPrefix(t *testing.T) {
	p := filepath.Join(t.TempDir(), "records.bin")
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], 0xFFFFFFFF) // ~4GiB claimed
	if err := os.WriteFile(p, append(hdr[:], 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	err := eachRecord(p, newRecordTables(), func(Record) { t.Fatal("should not decode a corrupt record") })
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("expected corrupt-index error, got %v", err)
	}
}

// checkRecordOffsets reads every session of dir through the offset table,
// one session at a time and forced past the walk-share cut, and fails when
// that does not give exactly the records a walk of the log gives for it.
func checkRecordOffsets(t *testing.T, dir string, m Manifest, recs []OffsetRecord) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, recordOffsetsFile)); err != nil {
		return
	}
	defer func(share int64) { offsetsWalkShare = share }(offsetsWalkShare)
	offsetsWalkShare = 1
	walked := map[string][]string{}
	for _, r := range recs {
		walked[r.Record.Key] = append(walked[r.Record.Key], fmt.Sprintf("%s|%q", r.Record.Role, r.Record.Text))
	}
	tb := tablesFromManifest(m)
	for key := range m.Sessions {
		before := atomic.LoadInt64(&recordOffsetReads)
		var got []string
		err := eachRecordForKeys(filepath.Join(dir, "records.bin"), tb, map[string]bool{key: true}, func(r Record) {
			got = append(got, fmt.Sprintf("%s|%q", r.Role, r.Text))
		})
		if err != nil {
			t.Fatalf("offset read of %s: %v", key, err)
		}
		if atomic.LoadInt64(&recordOffsetReads) == before {
			t.Errorf("%s: the offset table did not answer", key)
		}
		if !slices.Equal(got, walked[key]) {
			t.Errorf("%s: the offset table reads %d records, the walk %d:\n  table %q\n  walk  %q", key, len(got), len(walked[key]), got, walked[key])
		}
	}
}

// A session is read through the offset table, and a table that does not fit
// the log it sits beside, whatever is wrong with it, gives the records a walk
// gives.
func TestSessionLoadsThroughTheOffsetTable(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	h.put("svc", "s3", richSession("svc", "s3", 4, "cache"))
	h.put("svc", "s4", richSession("svc", "s4", 5, "ledger"))
	h.put("svc", "s5", richSession("svc", "s5", 6, "outbox"))
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	table := filepath.Join(dir, recordOffsetsFile)
	good, err := os.ReadFile(table)
	if err != nil {
		t.Fatalf("a build leaves no offset table: %v", err)
	}
	load := func() ([]string, int64) {
		before := atomic.LoadInt64(&recordOffsetReads)
		s, ok, findErr := FindByIdentity(dir, "claude", "s2")
		if findErr != nil || !ok {
			t.Fatalf("load s2: %v %v", ok, findErr)
		}
		out := make([]string, 0, len(s.Messages))
		for _, m := range s.Messages {
			out = append(out, m.Role+"|"+m.Text)
		}
		return out, atomic.LoadInt64(&recordOffsetReads) - before
	}
	want, reads := load()
	if reads == 0 {
		t.Fatal("loading one session of five walked the log instead of reading the table")
	}

	broken := map[string][]byte{
		"missing":   nil,
		"truncated": good[:len(good)/2],
		"bad magic": append([]byte("XXXXXXXX"), good[8:]...),
		// Every offset moved by one record frame: the reads land on the
		// wrong records, and the key check catches it.
		"wrong offsets": shiftFirstOffset(good),
	}
	for name, b := range broken {
		if b == nil {
			_ = os.Remove(table)
		} else if err = os.WriteFile(table, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _ := load(); !slices.Equal(got, want) {
			t.Errorf("%s table: s2 reads\n%q\nwant\n%q", name, got, want)
		}
	}

	// A log written again under a table built for the old one.
	if err = os.WriteFile(table, good, 0o600); err != nil {
		t.Fatal(err)
	}
	h.put("app", "s2", richSession("app", "s2", 3, "worker"))
	if err = Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	walked := map[string]bool{}
	recs, err := ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Record.Key == "claude:s2" {
			walked[r.Record.Role+"|"+r.Record.Text] = true
		}
	}
	got, _ := load()
	for _, line := range got {
		if !walked[line] {
			t.Errorf("after the rewrite s2 reads a record the log does not hold: %q", line)
		}
	}
}

// shiftFirstOffset adds one to every key's first offset, leaving the header
// and the directory intact: each read lands inside a record, not on one.
func shiftFirstOffset(b []byte) []byte {
	out := append([]byte(nil), b...)
	n := int(binary.LittleEndian.Uint32(out[24+sha256.Size:]))
	data := recordOffsetsHeaderSize + 16*n
	for i := range n {
		p := data + int(binary.LittleEndian.Uint64(out[recordOffsetsHeaderSize+16*i+8:])) //nolint:gosec // offsets the writer just wrote
		kl, w := binary.Uvarint(out[p:])
		p += w + int(kl) //nolint:gosec // a key length the writer just wrote
		_, w = binary.Uvarint(out[p:])
		p += w
		out[p]++
	}
	return out
}

// ProjectRelevantNamingCut on a corpus where the ranked candidates hold more
// than a quarter of all records still uses bounded offset reads from
// recordoffsets.bin rather than walking the full log or materializing every
// non-matching record of a marathon session (#5901).
func TestProjectRelevantBoundsCandidateSessionReadsThroughOffsetTable(t *testing.T) {
	h := newTwoWayEnv(t)
	// Four marathon sessions (220 messages each) holding >95% of all records.
	for i := range 4 {
		id := "m" + string(rune('1'+i))
		day := 2 + i
		var b strings.Builder
		b.WriteString(hPrompt("app", id, day, 0, "how do we configure kestrel retries"))
		for m := 1; m <= 109; m++ {
			b.WriteString(hSay("app", id, day, m%59, "routine background turn "+string(rune('A'+m%26))+string(rune('0'+m/26))+" without target keyword"))
			b.WriteString(hPrompt("app", id, day, m%59, "next routine question "+string(rune('A'+m%26))+string(rune('0'+m/26))))
		}
		b.WriteString(hSay("app", id, day, 59, "we decided kestrel retries cap at four"))
		h.put("app", id, b.String())
	}
	h.put("svc", "other", richSession("svc", "other", 10, "outbox"))
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	before := atomic.LoadInt64(&recordOffsetReads)
	ss, _, _, _, _, err := ProjectRelevantNamingCut(dir, []string{"me/app"}, []string{"kestrel"}, 4, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&recordOffsetReads) == before {
		t.Fatal("ProjectRelevantNamingCut walked the log instead of reading recordoffsets.bin")
	}
	if len(ss) != 4 {
		t.Fatalf("got %d sessions, want 4", len(ss))
	}
	for _, s := range ss {
		if len(s.Messages) > maxWindowedSessionRecords {
			t.Errorf("session %s loaded %d messages, want <= %d", s.ID, len(s.Messages), maxWindowedSessionRecords)
		}
		if s.Held < 200 {
			t.Errorf("marathon session %s Held = %d, want >= 200 so prompt narrowing still fires", s.ID, s.Held)
		}
	}
}

func TestFindTailByIdentityAndRecentProjectsUnderTailMatchFullTail(t *testing.T) {
	h := newTwoWayEnv(t)
	var b strings.Builder
	b.WriteString(hPrompt("app", "long", 2, 0, "opening turn"))
	for m := 1; m <= 25; m++ {
		b.WriteString(hSay("app", "long", 2, m, "reply "+string(rune('A'+m%26))))
		b.WriteString(hPrompt("app", "long", 2, m, "prompt "+string(rune('A'+m%26))))
	}
	b.WriteString(hSay("app", "long", 2, 30, "final decision on retry.go"))
	h.put("app", "long", b.String())
	dir := filepath.Join(h.tmp, "a.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	full, ok, err := FindByIdentity(dir, "claude", "long")
	if err != nil || !ok || len(full.Messages) <= 10 {
		t.Fatalf("full load = (%d msgs, %v, %v)", len(full.Messages), ok, err)
	}
	wantTail := full.Messages[len(full.Messages)-6:]

	before := atomic.LoadInt64(&recordOffsetReads)
	tailed, ok, err := FindTailByIdentity(dir, "claude", "long", 6)
	if err != nil || !ok {
		t.Fatalf("FindTailByIdentity: %v %v", ok, err)
	}
	if atomic.LoadInt64(&recordOffsetReads) == before {
		t.Fatal("FindTailByIdentity did not use recordoffsets.bin")
	}
	if len(tailed.Messages) != 6 {
		t.Fatalf("FindTailByIdentity returned %d messages, want 6", len(tailed.Messages))
	}
	for i := range wantTail {
		if tailed.Messages[i].Text != wantTail[i].Text || tailed.Messages[i].Role != wantTail[i].Role {
			t.Fatalf("tail[%d] = %#v, want %#v", i, tailed.Messages[i], wantTail[i])
		}
	}

	recent, err := RecentProjectsUnderTail(dir, []string{"me/app"}, "", 12, 6)
	if err != nil || len(recent) != 1 {
		t.Fatalf("RecentProjectsUnderTail = (%d, %v)", len(recent), err)
	}
	if len(recent[0].Messages) != 6 {
		t.Fatalf("RecentProjectsUnderTail returned %d messages, want 6", len(recent[0].Messages))
	}
	for i := range wantTail {
		if recent[0].Messages[i].Text != wantTail[i].Text {
			t.Fatalf("recent tail[%d] = %q, want %q", i, recent[0].Messages[i].Text, wantTail[i].Text)
		}
	}
}

// records.bin stores a message time as a nanosecond count, which only spans
// 1678-2262. A transcript stamped 2999 wrapped to 1829 and one stamped 1200
// wrapped to 1784: both come back as ordinary-looking dates, so `deja stats`
// reported a Range starting two centuries before the store's oldest session
// and nothing looked wrong. Out of range must stay out of range.
func TestOutOfRangeMessageTimesDoNotWrap(t *testing.T) {
	cases := []struct {
		name  string
		when  time.Time
		after bool // true: must land at or after the far future edge
	}{
		{"far future", time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"before the epoch window", time.Date(1200, 5, 6, 0, 0, 0, 0, time.UTC), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "records.bin")
			f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			rw, err := newRecordWriter(f, newRecordTables())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rw.write(Record{Key: "claude:s1", SourcePath: "/tmp/s1.jsonl", Role: "user", Text: "stamped", Time: c.when}); err != nil {
				t.Fatal(err)
			}
			if err := rw.Close(); err != nil {
				t.Fatal(err)
			}
			var got Record
			if err := eachRecord(p, newRecordTables(), func(r Record) { got = r }); err != nil {
				t.Fatal(err)
			}
			if got.Time.IsZero() {
				t.Fatalf("%v decoded as unstamped, which means the message has no date at all", c.when)
			}
			if c.after && got.Time.Before(maxRecordTime) {
				t.Fatalf("%v decoded as %v — a future stamp moved into the past", c.when, got.Time)
			}
			if !c.after && got.Time.After(minRecordTime) {
				t.Fatalf("%v decoded as %v — an ancient stamp moved forward", c.when, got.Time)
			}
		})
	}

	// An in-range stamp is untouched, and an unstamped record still reads as
	// unstamped: the clamp must not swallow either.
	when := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	if got := recordNanos(when); got != when.UnixNano() {
		t.Errorf("recordNanos rewrote an ordinary stamp: %d want %d", got, when.UnixNano())
	}
	if got := recordNanos(time.Time{}); got != zeroTimeUnixNano {
		t.Errorf("recordNanos lost the unstamped marker: %d want %d", got, zeroTimeUnixNano)
	}
}

// The substring (fuzzy) tier scans every bucket and used to skip a bucket it
// could not open, so a corrupt store silently under-matched instead of
// surfacing the damage for the recovery path to rebuild.
func TestSubstringTierSurfacesACorruptBucket(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "opencode indexing throughput"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no buckets to corrupt")
	}
	for _, e := range entries {
		if err = os.WriteFile(filepath.Join(dir, "buckets", e.Name()), []byte("XXXXXXXX garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// "code" is a substring of the indexed "opencode", so this reaches the
	// substring scan rather than short-circuiting empty.
	_, _, err = intersectSubstringPostingsDetailed(dir, []string{"code"})
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error from the substring tier, got %v", err)
	}
}

// FirstMatch (the per-prompt hook path) probes candidate queries and used to
// lump a scanRecords error together with "no match" and skip to the next
// candidate, so a corrupt store looked like a prompt with nothing to recall.
func TestFirstMatchSurfacesCorruption(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "proj",
		Messages: []model.Message{{Role: "user", Text: "kafka consumer lag climbing"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	// [size][keyID][pathID][roleID][flag]: past the three interned ids is the
	// encoding byte, and an unknown one is corruption.
	off := 4
	_, k := binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k
	b[off] = 0xFF
	if err = os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err = FirstMatch(dir, []string{"kafka"}, 5)
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error from FirstMatch, got %v", err)
	}
}

// The offset read path (postings resolve to record offsets) must also surface a
// corrupt record rather than skipping it and reporting a clean result. A posting
// offset is committed, so a record that will not decode there is corruption.
func TestEachRecordAtSurfacesCorruption(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "proj",
		Messages: []model.Message{{Role: "user", Text: "kafka consumer lag climbing"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	// Flip the first record's encoding flag (after the two leading varints).
	// [size][keyID][pathID][roleID][flag]: past the three interned ids is the
	// encoding byte, and an unknown one is corruption.
	off := 4
	_, k := binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k
	b[off] = 0xFF
	if err = os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(rp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	called := false
	err = eachRecordAt(f, []int64{0}, tablesFromManifest(m), func(Record) { called = true })
	if err == nil || !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error from the offset path, got %v", err)
	}
	if called {
		t.Fatal("a corrupt record was delivered instead of surfaced")
	}
}

// A record whose framing is intact (valid length prefix, all bytes present) but
// whose payload will not decode is corruption in the middle of the log, not the
// tolerated in-flight tail. The read path must surface it so a rebuild can heal
// the store, not hand back a session with the broken message silently dropped.
func TestReadSurfacesAMidLogDecodeError(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{{
		Harness: "claude", ID: "a", Project: "proj",
		Messages: []model.Message{{Role: "user", Text: "postgres deadlock on the outbox table"}},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}

	// Flip the record's encoding flag to an unknown value. Layout per record:
	// a 4-byte little-endian length, then payload = [key id varint][source id
	// varint][flag byte]. The bytes stay the same length, so framing still reads
	// and the corruption only shows when the payload is decoded.
	rp := filepath.Join(dir, "records.bin")
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.LittleEndian.Uint32(b[:4]))
	if n == 0 || 4+n > len(b) {
		t.Fatalf("unexpected record framing: n=%d len=%d", n, len(b))
	}
	off := 4
	_, k := binary.Uvarint(b[off:])
	off += k
	_, k = binary.Uvarint(b[off:])
	off += k // now at the flag byte
	b[off] = 0xFF
	if err = os.WriteFile(rp, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = RecentProject(dir, "proj", 0)
	if err == nil {
		t.Fatal("a corrupt record was dropped silently; the read returned no error")
	}
	if !IsCorrupt(err) {
		t.Fatalf("want a corrupt-index error, got %v", err)
	}
}

// The commands come out of a transcript with the shell prompt some harnesses
// store in front of them, and with it in place `$` was the program of every
// command ever recorded — so the same-program test compared `$` with `$` and
// passed for any two lines. On a real store 21 of the 163 repaired answers ran
// a different program and three were `cd` somewhere else.
func TestAPromptPrefixDoesNotMakeEveryCommandTheSameProgram(t *testing.T) {
	if repairedVariant("$ go test ./cmd/deja -run TestPasses", "$ rm cmd/deja/zz_probe_test.go; go test ./cmd/deja") {
		t.Error("removing a probe file counts as a repair of a failing test")
	}
	if repairedVariant("$ cd ~/one-place && make check", "$ cd ~/another-place && make check") {
		t.Error("going somewhere else counts as a repair")
	}
	// And the shape the bound was chosen for still holds with the prompt there.
	if !repairedVariant(`$ grep -rn "quokkabloom" --include=*.go internal`, `$ grep -rn "quokkabloom" --include="*.go" internal`) {
		t.Error("the corrected glob is no longer a repair")
	}
}

// Dropping the wrapper the shell could not find is the repair for the wall this
// machine hits most: 19 sessions have run into a missing `timeout`.
func TestDroppingAMissingWrapperIsARepair(t *testing.T) {
	if !repairedVariant("$ timeout 12 launchctl kickstart -k system/dev.example.svc",
		"$ launchctl kickstart -k system/dev.example.svc") {
		t.Error("the same command without the missing wrapper is not recognised as the repair")
	}
	if got := commandProgram(commandTokens("$ timeout 90 ssh -o ConnectTimeout=10 host uptime")); got != "ssh" {
		t.Errorf("the program behind the wrapper is %q", got)
	}
	// A duration is what timeout takes; a command that happens to be named
	// after it is still the program.
	if got := commandProgram(commandTokens("$ timeout --help")); got != "timeout" {
		t.Errorf("timeout with no command after it is %q", got)
	}
}

func TestRelevanceSixWordQueryPrefersFocusedSessionOverMarathon(t *testing.T) {
	h := newTwoWayEnv(t)
	// Seed background corpus so IDF estimates behave realistically.
	for i := range 24 {
		sid := fmt.Sprintf("bg-%d", i)
		h.put("app", sid, hPrompt("app", sid, 2, 0, fmt.Sprintf("background deploy check item %d retry budget", i))+
			hSay("app", sid, 2, 1, "verified baseline configuration"))
	}
	// Marathon session: 1,500 turns scattering query terms across distant messages.
	var marathon strings.Builder
	marathon.WriteString(hPrompt("svc", "marathon-1500", 5, 0, "ongoing cluster maintenance thread"))
	words := []string{"cobalt", "harbor", "turbine", "manifest", "quorum", "latch"}
	for i := 1; i <= 1500; i++ {
		w := words[i%len(words)]
		extra := ""
		if i%30 == 0 {
			extra = " retry backoff"
		}
		marathon.WriteString(hSay("svc", "marathon-1500", 5+i/60, i%60, fmt.Sprintf("step %d checking %s telemetry%s", i, w, extra)))
	}
	h.put("svc", "marathon-1500", marathon.String())

	// Short focused session: co-occurs all six terms in a single message.
	h.put("svc", "focused-win", hPrompt("svc", "focused-win", 6, 0, "cobalt harbor turbine manifest quorum latch resolution")+
		hSay("svc", "focused-win", 6, 1, "Fixed cobalt harbor turbine manifest quorum latch mismatch in one pass."))

	dir := filepath.Join(h.tmp, "idx.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	// Check both exact and fallback relevance tier (7th missing term forces relevanceSearch).
	for _, q := range []string{
		"cobalt harbor turbine manifest quorum latch",
		"cobalt harbor turbine manifest quorum latch unindexedword",
	} {
		res, err := searchDetailedRead(dir, query.Options{
			Query: q,
			Limit: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Sessions) < 2 {
			t.Fatalf("query %q got %d sessions, want at least 2", q, len(res.Sessions))
		}
		if res.Sessions[0].ID != "focused-win" {
			t.Fatalf("query %q got top session %q (behind %q), want focused-win over marathon", q, res.Sessions[0].ID, res.Sessions[1].ID)
		}
	}
}
