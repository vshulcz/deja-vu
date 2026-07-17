package index

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
)

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "deja-index-test-")
	if err != nil {
		panic(err)
	}
	stores := map[string]string{
		"HOME":                  root,
		"USERPROFILE":           root,
		"DEJA_CLAUDE_ROOT":      filepath.Join(root, "claude"),
		"DEJA_CODEX_ROOT":       filepath.Join(root, "codex"),
		"DEJA_OPENCODE_DB":      filepath.Join(root, "opencode.db"),
		"DEJA_AIDER_ROOTS":      filepath.Join(root, "aider"),
		"DEJA_GEMINI_ROOT":      filepath.Join(root, "gemini"),
		"DEJA_CURSOR_ROOT":      filepath.Join(root, "cursor"),
		"DEJA_CURSOR_CLI_ROOT":  filepath.Join(root, "cursor-cli"),
		"DEJA_ANTIGRAVITY_ROOT": filepath.Join(root, "antigravity"),
		"DEJA_GROK_ROOT":        filepath.Join(root, "grok"),
		"DEJA_QWEN_ROOT":        filepath.Join(root, "qwen"),
	}
	for key, value := range stores {
		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func TestGrokIndexGrowthRenameAndRewind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(tmp, "grok"))

	sessionDir := filepath.Join(tmp, "grok", "sessions", url.PathEscape("/work/grok-project"), "019f-grok-index")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"019f-grok-index"},"generated_title":"Grok index test","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T10:00:01Z"}`
	summaryPath := filepath.Join(sessionDir, "summary.json")
	if err := os.WriteFile(summaryPath, []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	updates := filepath.Join(sessionDir, "updates.jsonl")
	first := `{"timestamp":1782900001,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"grokindexneedle question"},"_meta":{"promptIndex":0}}}}` + "\n"
	if err := os.WriteFile(updates, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	indexDir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(indexDir, search.Options{Query: "grokindexneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(indexDir, search.Options{Query: "grokindexneedle", Harness: "grok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Harness != "grok" || ss[0].Project != "grok-project" {
		t.Fatalf("bad indexed session: %#v", ss)
	}
	recent, err := Recent(indexDir, 1)
	if err != nil || len(recent) != 1 || recent[0].Title != "Grok index test" {
		t.Fatalf("bad indexed title: %#v err=%v", recent, err)
	}
	renamedSummary := `{"info":{"id":"019f-grok-index"},"generated_title":"Grok title test","created_at":"2026-07-01T10:00:00Z","updated_at":"2026-07-01T10:00:01Z"}`
	if len(renamedSummary) != len(summary) {
		t.Fatal("renamed summary must preserve size to exercise mtime freshness")
	}
	if err := os.WriteFile(summaryPath, []byte(renamedSummary), 0o644); err != nil {
		t.Fatal(err)
	}
	tick := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(summaryPath, tick, tick); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(indexDir, search.Options{Query: "grokindexneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	recent, err = Recent(indexDir, 1)
	if err != nil || len(recent) != 1 || recent[0].Title != "Grok title test" {
		t.Fatalf("summary-only rename was not indexed: %#v err=%v", recent, err)
	}

	cwdPath := filepath.Join(filepath.Dir(sessionDir), ".cwd")
	firstCWD := "/work/moved-project\n"
	secondCWD := "/work/other-project\n"
	if len(firstCWD) != len(secondCWD) {
		t.Fatal("cwd fixtures must have equal size to exercise mtime freshness")
	}
	if err := os.WriteFile(cwdPath, []byte(firstCWD), 0o644); err != nil {
		t.Fatal(err)
	}
	tick = tick.Add(time.Second)
	if err := os.Chtimes(cwdPath, tick, tick); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(indexDir, search.Options{Query: "grokindexneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	recent, err = Recent(indexDir, 1)
	if err != nil || len(recent) != 1 || recent[0].Project != "moved-project" {
		t.Fatalf("new Grok cwd marker was not indexed: %#v err=%v", recent, err)
	}
	if err := os.WriteFile(cwdPath, []byte(secondCWD), 0o644); err != nil {
		t.Fatal(err)
	}
	tick = tick.Add(time.Second)
	if err := os.Chtimes(cwdPath, tick, tick); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(indexDir, search.Options{Query: "grokindexneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(indexDir, search.Options{Query: "grokindexneedle", Harness: "grok", Project: "other-project"})
	if err != nil || len(ss) != 1 {
		t.Fatalf("changed Grok cwd marker was not indexed: %#v err=%v", ss, err)
	}

	f, err := os.OpenFile(updates, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	second := `{"timestamp":1782900002,"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"replacementneedle answer"}},"_meta":{"promptId":"p1"}}}` + "\n"
	if _, err := f.WriteString(second); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	tick = tick.Add(time.Second)
	if err := os.Chtimes(updates, tick, tick); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(indexDir, search.Options{Query: "replacementneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(indexDir, search.Options{Query: "replacementneedle", Harness: "grok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "replacementneedle answer" {
		t.Fatalf("Grok growth was not indexed cleanly: %#v", ss)
	}
	ss, err = Search(indexDir, search.Options{Query: "grokindexneedle", Harness: "grok"})
	if err != nil || len(ss) != 1 {
		t.Fatalf("Grok growth lost existing records: %#v err=%v", ss, err)
	}

	rewound := first + `{"timestamp":1782900003,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"rewoundneedle replacement conversation that is longer than the removed assistant response"},"_meta":{"promptIndex":1}}}}` + "\n"
	if len(rewound) <= len(first)+len(second) {
		t.Fatal("rewound fixture must regrow past the previously indexed size")
	}
	if err := os.WriteFile(updates, []byte(rewound), 0o644); err != nil {
		t.Fatal(err)
	}
	tick = tick.Add(time.Second)
	if err := os.Chtimes(updates, tick, tick); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(indexDir, search.Options{Query: "replacementneedle", Harness: "grok"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(indexDir, search.Options{Query: "replacementneedle", Harness: "grok"})
	if err != nil || len(ss) != 0 {
		t.Fatalf("Grok rewind retained truncated records: %#v err=%v", ss, err)
	}
	ss, err = Search(indexDir, search.Options{Query: "rewoundneedle", Harness: "grok"})
	if err != nil || len(ss) != 1 {
		t.Fatalf("Grok rewind replacement was not indexed: %#v err=%v", ss, err)
	}
}
