package embed

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// countingClient answers every embed request and records how many texts it was
// asked to embed.
func countingClient(t *testing.T, embedded *atomic.Int64) *Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []string `json:"input"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		n := len(body.Input)
		embedded.Add(int64(n))
		out := make([][]float32, n)
		for i := range out {
			out[i] = []float32{1, 0}
		}
		payload, _ := json.Marshal(map[string]any{"embeddings": out})
		_, _ = w.Write(payload)
	}))
	t.Cleanup(ts.Close)
	return &Client{URL: ts.URL, Model: "test"}
}

func embedStore(t *testing.T) (dir string, addSession func(id string)) {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude")
	if err := os.MkdirAll(filepath.Join(root, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id string) {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"session ` + id + ` about the scheduler"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, "project", id+".jsonl"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		write(id)
	}
	for key, value := range map[string]string{
		"HOME": tmp, "USERPROFILE": tmp, "DEJA_CLAUDE_ROOT": root,
		"DEJA_CODEX_ROOT": filepath.Join(tmp, "codex"), "DEJA_OPENCODE_DB": filepath.Join(tmp, "open.db"),
		"DEJA_AIDER_ROOTS": filepath.Join(tmp, "aider"), "DEJA_GEMINI_ROOT": filepath.Join(tmp, "gemini"),
		"DEJA_CURSOR_ROOT": filepath.Join(tmp, "cursor"), "DEJA_CURSOR_CLI_ROOT": filepath.Join(tmp, "cursor-cli"),
		"DEJA_ANTIGRAVITY_ROOT": filepath.Join(tmp, "antigravity"), "DEJA_GROK_ROOT": filepath.Join(tmp, "grok"),
		"DEJA_QWEN_ROOT": filepath.Join(tmp, "qwen"),
	} {
		t.Setenv(key, value)
	}
	dir = filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	// Growing a session already in the index appends in place, and since #3500
	// so does a brand-new transcript: what moves every offset is a rebuild, or
	// a rewind that sends the pass down the replacement path.
	return dir, func(id string) {
		f, err := os.OpenFile(filepath.Join(root, "project", "a.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		line := `{"type":"user","sessionId":"a","timestamp":"2026-01-02T00:00:00Z","message":{"role":"user","content":"and then ` + id + ` happened to the scheduler"}}` + "\n"
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := index.Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// Embedding costs a network call per record. A session that grows is appended
// to records.bin in place, so every vector already paid for still points at the
// record it was made from — only the new message needs embedding.
func TestEmbedIndexOnlyEmbedsWhatIsNew(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, addSession := embedStore(t)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if first := embedded.Load(); first != 5 {
		t.Fatalf("first build embedded %d records, want 5 — the fixture is wrong", first)
	}
	addSession("f") // one more message in a session already indexed
	embedded.Store(0)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if again := embedded.Load(); again != 1 {
		t.Errorf("one more message re-embedded %d records, want 1", again)
	}
}

// The layout rule underneath it, stated directly: a stamp that changed is a
// rebuild and invalidates every offset; the same stamp with a file that only
// grew is an append and invalidates nothing; a file that shrank moved records.
func TestSameLayout(t *testing.T) {
	for name, tc := range map[string]struct {
		was, now string
		want     bool
	}{
		"identical":         {"2026-01-01T00:00:00Z+100", "2026-01-01T00:00:00Z+100", true},
		"appended":          {"2026-01-01T00:00:00Z+100", "2026-01-01T00:00:00Z+180", true},
		"shrank":            {"2026-01-01T00:00:00Z+180", "2026-01-01T00:00:00Z+100", false},
		"rebuilt":           {"2026-01-01T00:00:00Z+100", "2026-01-01T00:00:09Z+100", false},
		"no size at all":    {"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z+100", false},
		"empty":             {"", "2026-01-01T00:00:00Z+100", false},
		"no stamp":          {"+100", "+180", false},
		"size not a number": {"2026-01-01T00:00:00Z+abc", "2026-01-01T00:00:00Z+180", false},
		"negative size":     {"2026-01-01T00:00:00Z+-5", "2026-01-01T00:00:00Z+180", false},
	} {
		if got := sameLayout(tc.was, tc.now); got != tc.want {
			t.Errorf("%s: sameLayout(%q, %q) = %v", name, tc.was, tc.now, got)
		}
	}
}

// A brand-new transcript is appended too, since #3500: the records already on
// file do not move, so their vectors still point at them and only the new
// message is embedded. This is the expensive case going cheap — before that
// change every new conversation made every vector again.
func TestEmbedIndexOnlyEmbedsANewSessionsOwnRecords(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, _ := embedStore(t)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	line := `{"type":"user","sessionId":"z","timestamp":"2026-01-03T00:00:00Z","message":{"role":"user","content":"a brand new session about the scheduler"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "project", "z.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	embedded.Store(0)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if again := embedded.Load(); again != 1 {
		t.Errorf("a new transcript re-embedded %d records, want only its own 1", again)
	}
}

// A rewrite moves every record, and the vectors used to go with the offsets:
// the next run embedded the whole store again (#4976). Each vector now finds
// its record by identity, so only the record the rewrite changed is sent.
func TestEmbedIndexEmbedsOnlyWhatARewriteChanged(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, _ := embedStore(t)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// Rewritten rather than grown: a rewind cannot be appended onto, so the
	// pass replaces the session, and the vectors of its old records go.
	rewound := `{"type":"user","sessionId":"a","timestamp":"2026-01-04T00:00:00Z","message":{"role":"user","content":"the scheduler story, rewritten"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "project", "a.jsonl"), []byte(rewound), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	embedded.Store(0)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if again := embedded.Load(); again != 1 {
		t.Errorf("a replaced records.bin re-embedded %d records, want only the rewritten 1", again)
	}
}

// A version 1 sidecar has no identities, so its vectors cannot follow a rewrite
// and every record is embedded again, as before.
func TestAVersion1SidecarIsEmbeddedAgainAfterARewrite(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, _ := embedStore(t)
	s, err := EmbedIndex(dir, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range s.Vectors {
		s.Vectors[i].Ident = 0
	}
	if err := write(dir, s); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	rewound := `{"type":"user","sessionId":"a","timestamp":"2026-01-04T00:00:00Z","message":{"role":"user","content":"the scheduler story, rewritten"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "project", "a.jsonl"), []byte(rewound), 0o600); err != nil {
		t.Fatal(err)
	}
	// Written again whole: an ordinary pass appends the rewritten transcript
	// and leaves the offsets a version 1 vector holds valid (#5387).
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	embedded.Store(0)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if again := embedded.Load(); again != 5 {
		t.Errorf("a version 1 sidecar after a rewrite re-embedded %d records, want all 5", again)
	}
}

// A version 1 sidecar on the index it was built for keeps every vector: the
// next run gives each its record's identity and embeds nothing.
func TestAVersion1SidecarIsRekeyedWhileItsLayoutHolds(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, _ := embedStore(t)
	s, err := EmbedIndex(dir, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range s.Vectors {
		s.Vectors[i].Ident = 0
	}
	if err := write(dir, s); err != nil {
		t.Fatal(err)
	}
	embedded.Store(0)
	again, err := EmbedIndex(dir, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := embedded.Load(); n != 0 {
		t.Errorf("a version 1 sidecar on its own index re-embedded %d records, want none", n)
	}
	if len(again.Vectors) != 5 || !keyed(again) {
		t.Errorf("rekeyed sidecar holds %d vectors, keyed=%v; want 5 and true", len(again.Vectors), keyed(again))
	}
}

// The identity reads the length of the whole text and its first and last 256
// bytes, so two long records that start alike but differ in length or ending
// are told apart, and a record read again names itself the same way.
func TestRecordIdentTellsLongRecordsApart(t *testing.T) {
	head := strings.Repeat("the scheduler retries ", 20)
	a := index.Record{Key: "claude:a", Role: "user", Text: head + "once"}
	b := index.Record{Key: "claude:a", Role: "user", Text: head + "twice over"}
	if RecordIdent(a) == RecordIdent(b) {
		t.Error("two long records of different lengths share an identity")
	}
	c := index.Record{Key: "claude:a", Role: "user", Text: head + "ending one"}
	d := index.Record{Key: "claude:a", Role: "user", Text: head + "ending two"}
	if RecordIdent(c) == RecordIdent(d) {
		t.Error("two long records of one length that end apart share an identity")
	}
	if RecordIdent(a) != RecordIdent(index.Record{Key: "claude:a", Role: "user", Text: head + "once"}) {
		t.Error("the same record read twice named itself differently")
	}
}

// A harness that writes no time per turn dates them all from the file's
// modification time, so each pass that grew the session moved every record's
// time. The identity held the time, and the next embed sent the whole session
// again (#4976); it holds only what the vector is made from now.
func TestEmbedIndexKeepsVectorsWhenOnlyTheTimeMoved(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, _ := embedStore(t)
	tdir := filepath.Join(os.Getenv("DEJA_CURSOR_CLI_ROOT"), "projects", "work", "agent-transcripts", "cur")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tdir, "cur.jsonl")
	turns := func(n int, at time.Time) {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, `{"role":"user","message":{"content":[{"type":"text","text":"cursor question %d about the scheduler"}]}}`+"\n", i)
			fmt.Fprintf(&b, `{"role":"assistant","message":{"content":[{"type":"text","text":"cursor answer %d"}]}}`+"\n", i)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		if err := index.Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	turns(3, start)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if n := embedded.Load(); n != 5+6 {
		t.Fatalf("first embed sent %d records, want 11; is the transcript indexed?", n)
	}
	turns(4, start.Add(5*time.Minute))
	embedded.Store(0)
	if _, err := EmbedIndex(dir, client, nil); err != nil {
		t.Fatal(err)
	}
	if n := embedded.Load(); n != 2 {
		t.Errorf("a session that grew by one exchange re-embedded %d records, want its 2 new ones", n)
	}
}

// A version 2 sidecar keyed its vectors with the time in the identity. Its
// vectors still find their records after a rewrite, by that identity, and are
// rekeyed: upgrading deja does not embed the store again.
func TestAVersion2SidecarIsRekeyedAfterARewrite(t *testing.T) {
	var embedded atomic.Int64
	client := countingClient(t, &embedded)
	dir, _ := embedStore(t)
	recs, err := index.ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := EmbedIndex(dir, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	v2 := map[int64]uint64{}
	for _, r := range recs {
		v2[r.Offset] = recordIdentV2(r.Record)
	}
	for i := range s.Vectors {
		s.Vectors[i].Ident = v2[s.Vectors[i].Offset]
	}
	if err := write(dir, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint16(raw[4:], 2)
	if err := os.WriteFile(Path(dir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	rewound := `{"type":"user","sessionId":"a","timestamp":"2026-01-04T00:00:00Z","message":{"role":"user","content":"the scheduler story, rewritten"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "project", "a.jsonl"), []byte(rewound), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	embedded.Store(0)
	again, err := EmbedIndex(dir, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := embedded.Load(); n != 1 {
		t.Errorf("a version 2 sidecar after a rewrite re-embedded %d records, want only the rewritten 1", n)
	}
	got, err := readFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != sidecarVersion || len(again.Vectors) != 5 {
		t.Errorf("sidecar is version %d with %d vectors, want %d and 5", got.Version, len(again.Vectors), sidecarVersion)
	}
}
