package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCodeArtsFixture(t *testing.T, root string) string {
	t.Helper()
	// Two-level bucket tree: sessions/<launch-bucket>/<session-uuid>/
	dir := filepath.Join(root, "2026-10-30-05-47-28-a198caeba310ddb3", "ff119714-beda-42a7-b9c2-06ee66fb0927")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{
  "session_id": "ff119714-beda-42a7-b9c2-06ee66fb0927",
  "title": "用 scp 拉取 onetab 到本地工作目录",
  "working_directory": "S:\\work\\WCIS",
  "created_at": "2026-10-29T21:47:28.195777600Z",
  "updated_at": "2026-10-30T10:05:26.482245Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	history := "" +
		`{"id":"m1","role":"User","content":{"SinglePart":{"Text":{"text":"把 onetab 拉到本地"}}},"timestamp":"2026-10-29T21:47:28.572955600Z"}` + "\n" +
		`{"id":"m2","role":"Assistant","content":{"SinglePart":{"Text":{"text":"好的，开始拉取"}}},"reasoning_content":"先确认路径","timestamp":"2026-10-29T21:47:31Z"}` + "\n" +
		`{"id":"m3","role":"Tool","content":{"MultiPart":[{"ToolResult":{"tool_call_id":"c1","result":{"tool_output":"done"}}}]},"timestamp":"2026-10-29T21:47:32Z"}` + "\n" +
		`{malformed json` + "\n" +
		`{"id":"m4","role":"Assistant","content":{"MultiPart":[{"Text":{"text":"拉取完成"}},{"ToolCall":{"id":"c2","name":"bash"}}]},"timestamp":"2026-10-29T21:47:40Z"}` + "\n" +
		// The byte-range index is not a session and must never be listed.
		""
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.index.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCodeArtsParse(t *testing.T) {
	root := t.TempDir()
	dir := writeCodeArtsFixture(t, root)
	t.Setenv("DEJA_CODEARTS_ROOT", root)

	files := CodeArtsFiles()
	if len(files) != 1 || filepath.Base(files[0]) != "history.jsonl" {
		t.Fatalf("CodeArtsFiles() = %v, want one history.jsonl", files)
	}
	ss, err := ParseCodeArtsFile(files[0])
	if err != nil {
		t.Fatalf("ParseCodeArtsFile: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "codearts" || s.ID != "ff119714-beda-42a7-b9c2-06ee66fb0927" {
		t.Fatalf("harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "用 scp 拉取 onetab 到本地工作目录" {
		t.Fatalf("title = %q", s.Title)
	}
	if s.Project == "" {
		t.Fatal("project empty: working_directory from meta.json was not read")
	}
	if s.Started.IsZero() || !s.Started.Equal(time.Date(2026, 10, 29, 21, 47, 28, 195777600, time.UTC)) {
		t.Fatalf("started = %v", s.Started)
	}
	// 3 spoken messages: user, assistant, and the assistant's MultiPart Text.
	// The ToolResult, ToolCall and reasoning_content are not speech; the
	// malformed line is skipped rather than failing the whole file.
	if len(s.Messages) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(s.Messages), s.Messages)
	}
	want := []struct{ role, text string }{
		{"user", "把 onetab 拉到本地"},
		{"assistant", "好的，开始拉取"},
		{"assistant", "拉取完成"},
	}
	for i, w := range want {
		if s.Messages[i].Role != w.role || s.Messages[i].Text != w.text {
			t.Fatalf("message %d = %+v, want role=%q text=%q", i, s.Messages[i], w.role, w.text)
		}
	}
	if !IsCodeArtsHistory(filepath.Join(dir, "history.jsonl")) {
		t.Fatal("IsCodeArtsHistory(history.jsonl) = false")
	}
	if IsCodeArtsHistory(filepath.Join(dir, "messages.index.jsonl")) {
		t.Fatal("IsCodeArtsHistory(messages.index.jsonl) = true")
	}
	if IsCodeArtsHistory(filepath.Join(root+"-kernel", "b", "s", "history.jsonl")) {
		t.Fatal("IsCodeArtsHistory(sibling root) = true")
	}
}

func TestCodeArtsParseFromOffset(t *testing.T) {
	root := t.TempDir()
	dir := writeCodeArtsFixture(t, root)
	t.Setenv("DEJA_CODEARTS_ROOT", root)
	p := filepath.Join(dir, "history.jsonl")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// A resumed read past the existing bytes yields nothing new but still
	// returns the session shell with its id/title from meta.json (#2870).
	ss, err := parseCodeArtsFileFromOffset(p, fi.Size())
	if err != nil {
		t.Fatalf("parseCodeArtsFileFromOffset: %v", err)
	}
	if len(ss) != 0 {
		t.Fatalf("got %d sessions at EOF, want 0", len(ss))
	}

	extra := `{"id":"m5","role":"User","content":{"SinglePart":{"Text":{"text":"再补一条"}}},"timestamp":"2026-10-30T11:00:00Z"}` + "\n"
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(extra)
	_ = f.Close()
	ss, err = parseCodeArtsFileFromOffset(p, fi.Size())
	if err != nil {
		t.Fatalf("parseCodeArtsFileFromOffset after append: %v", err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 || ss[0].Messages[0].Text != "再补一条" {
		t.Fatalf("resumed parse = %+v, want one appended user message", ss)
	}
	if ss[0].ID != "ff119714-beda-42a7-b9c2-06ee66fb0927" {
		t.Fatalf("resumed parse lost session id: %q", ss[0].ID)
	}
}

func TestCodeArtsLoad(t *testing.T) {
	root := t.TempDir()
	writeCodeArtsFixture(t, root)
	t.Setenv("DEJA_CODEARTS_ROOT", root)
	ss := LoadCodeArts()
	if len(ss) != 1 || ss[0].Harness != "codearts" {
		t.Fatalf("LoadCodeArts() = %+v, want one codearts session", ss)
	}
}
