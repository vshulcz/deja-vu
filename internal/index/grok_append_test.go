package index

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A live Grok session grows all day. Every touch used to reparse the whole
// updates.jsonl and rewrite the entire index — a gigabyte of records and
// buckets copied so a search could see one new line (#1522). Growth alone must
// take the append path now, while a rewind still rewrites: the prefix hash is
// what tells the two apart.
func TestGrokGrowthAppendsInsteadOfRewriting(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	dir := filepath.Join(tmp, "grok", "sessions", url.PathEscape("/work/live"), "019f-grok-append")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(dir, "summary.json")
	write := func(path, body string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tick := time.Now()
	write(summary, `{"info":{"id":"019f-grok-append"},"generated_title":"Live run","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:01Z"}`, tick)

	updates := filepath.Join(dir, "updates.jsonl")
	first := `{"timestamp":1785000001,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"appendfirstneedle opening question"},"_meta":{"promptIndex":0}}}}` + "\n"
	write(updates, first, tick)

	indexDir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(indexDir, search.Options{Query: "appendfirstneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}

	// One more ACP line, and a title bump alongside it — the two changes a live
	// session makes together.
	tick = tick.Add(10 * time.Second)
	second := `{"timestamp":1785000002,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"appendsecondneedle the answer"},"_meta":{}}}}` + "\n"
	write(updates, first+second, tick)
	write(summary, `{"info":{"id":"019f-grok-append"},"generated_title":"Live run continued","created_at":"2026-08-01T10:00:00Z","updated_at":"2026-08-01T10:00:09Z"}`, tick)

	var progress bytes.Buffer
	if err := EnsureForSearch(indexDir, search.Options{Query: "appendsecondneedle", Harness: "grok"}, false, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), "updated 1 file") {
		t.Errorf("growth did not take the append path:\n%s", progress.String())
	}
	for _, needle := range []string{"appendfirstneedle", "appendsecondneedle"} {
		ss, err := Search(indexDir, search.Options{Query: needle, Harness: "grok"})
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: %#v err=%v", needle, ss, err)
		}
	}
	// Exactly the two turns: an append that re-reads from byte zero would
	// index the opening question a second time and search would still look
	// right.
	full, ok, err := FindByID(indexDir, "019f-grok-append")
	if err != nil || !ok {
		t.Fatalf("session missing after append: ok=%v err=%v", ok, err)
	}
	if len(full.Messages) != 2 {
		t.Errorf("%d messages after one appended line, want 2:\n%#v", len(full.Messages), full.Messages)
	}

	recent, err := Recent(indexDir, 1)
	if err != nil || len(recent) != 1 {
		t.Fatalf("recent: %#v err=%v", recent, err)
	}
	if recent[0].Title != "Live run continued" {
		t.Errorf("title = %q — the append path dropped the summary bump", recent[0].Title)
	}

	// A rewind truncates and regrows past the old size, which looks exactly
	// like an append until the prefix is compared. Appending onto it would
	// leave the replaced turn in the index for good.
	tick = tick.Add(10 * time.Second)
	rewound := `{"timestamp":1785000003,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"appendrewoundneedle a different opening, written out at length so the file regrows past every byte the previous two lines occupied together, which is what makes a rewind indistinguishable from growth until the prefix itself is compared"},"_meta":{"promptIndex":0}}}}` + "\n"
	if len(rewound) <= len(first+second) {
		t.Fatal("rewound fixture must regrow past the indexed size")
	}
	write(updates, rewound, tick)
	if err := EnsureForSearch(indexDir, search.Options{Query: "appendrewoundneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(indexDir, search.Options{Query: "appendfirstneedle", Harness: "grok"})
	if err != nil || len(ss) != 0 {
		t.Fatalf("rewind kept the replaced turn: %#v err=%v", ss, err)
	}
	ss, err = Search(indexDir, search.Options{Query: "appendrewoundneedle", Harness: "grok"})
	if err != nil || len(ss) != 1 {
		t.Fatalf("rewind replacement missing: %#v err=%v", ss, err)
	}
}
