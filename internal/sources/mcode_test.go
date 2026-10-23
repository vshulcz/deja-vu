package sources

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// mcodeFixture is the published transcript: one turn, one command, one edit.
const mcodeFixture = "mcode/v2/sessions/2026/10/20/02-57-27-477-session_bXZzXzBmMWUyZDNjNGI1YTY5Nzg4Nzk2YTViNGMzZDJlMWYw/messages.jsonl"

func mcodeFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "fixtures", "registry", filepath.FromSlash(mcodeFixture))
}

func TestMcodeFileReadsTheTurnsAndTheWork(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "mcode" {
		t.Fatalf("harness = %q, want mcode", s.Harness)
	}
	if s.ID != "mvs_0f1e2d3c4b5a69788796a5b4c3d2e1f0" {
		t.Fatalf("id = %q, want the decoded session id", s.ID)
	}
	if s.Project == "" {
		t.Fatal("project is empty")
	}
	if s.Started.IsZero() || s.Updated.IsZero() {
		t.Fatalf("clock is empty: %#v", s)
	}
	roles := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
	}
	for _, want := range []string{"user", "assistant", RoleToolOutput, RoleCommand, RoleFiles, RoleEdit} {
		if !containsRole(roles, want) {
			t.Fatalf("roles = %v, want a %s record", roles, want)
		}
	}
}

func TestMcodeFileKeepsOnlyWhatThePersonTyped(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	first := firstUserMessage(ss[0])
	if first == nil {
		t.Fatal("the session holds no user turn")
	}
	if first.Text != "How do I install the mcode harness?" {
		t.Fatalf("user text = %q, want the canonical text alone", first.Text)
	}
	if strings.Contains(first.Text, "system-reminder") {
		t.Fatal("the runtime's own context was indexed as the person's words")
	}
}

// The runtime writes a compaction summary where the turns it replaced were,
// and keeps those turns in its own snapshots. The summary is the only record
// of them the transcript holds, so it is indexed — under the summary role,
// which is what keeps it from reading as speech nobody said (#4801).
func TestMcodeFileIndexesTheCompactionSummaryUnderItsOwnRole(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var summaries, elsewhere int
	for _, m := range ss[0].Messages {
		if !strings.Contains(m.Text, "MCODE-SUMMARY") {
			continue
		}
		if m.Role == RoleSummary {
			summaries++
		} else {
			elsewhere++
		}
	}
	if summaries != 1 || elsewhere != 0 {
		t.Errorf("summary indexed %d times under its role and %d times under another, want once and never",
			summaries, elsewhere)
	}
}

func TestMcodeFileLeavesTheThinkingOut(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "The harness needs a reader") {
			t.Fatal("the model's thinking was indexed as what it said")
		}
	}
}

func TestMcodeFileResumesFromAnOffset(t *testing.T) {
	path := mcodeFixturePath(t)
	full, err := ParseMcodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A resume starts past the first turn, so the title has to come from
	// somewhere else: the line is read again for it. The first line of a
	// compacted session is the summary, which is not a turn — the cut is
	// after the first one that is.
	cut := int64(bytes.IndexByte(b, '\n') + 1)
	if i := bytes.Index(b, []byte(`"role":"user"`)); i >= 0 {
		if j := bytes.IndexByte(b[i:], '\n'); j >= 0 {
			cut = int64(i + j + 1)
		}
	}
	tail, err := ParseMcodeFileFromOffset(path, cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 {
		t.Fatalf("sessions = %d, want 1", len(tail))
	}
	if tail[0].Title != full[0].Title {
		t.Fatalf("resumed title = %q, want %q", tail[0].Title, full[0].Title)
	}
	if len(tail[0].Messages) >= len(full[0].Messages) {
		t.Fatalf("resumed messages = %d, want fewer than the whole session's %d",
			len(tail[0].Messages), len(full[0].Messages))
	}
}

func TestMcodeSessionIDIsReadFromTheManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"),
		[]byte(`{"schemaVersion":1,"sessionId":"mvs_from_manifest"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, mcodeTranscriptName)
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if id := mcodeSessionID(path); id != "mvs_from_manifest" {
		t.Fatalf("id = %q, want the manifest's", id)
	}
}

// The runtime keeps more beside a transcript than the four files it names:
// the snapshots it was working from, and the reports it spilled when a tool
// printed more than it would hold inline. None of them is a conversation, so
// none of them may be counted as a file deja could not read.
func TestMcodeSidecarsAreNamedRatherThanCountedAsUnread(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "2026", "10", "20", "03-00-00-000-session_abc")
	for _, rel := range []string{
		mcodeTranscriptName,
		"manifest.json",
		"history-catalog.json",
		"llm-call.json",
		"user-message-locators.jsonl",
		filepath.Join("snapshots", "env-g1.json"),
		filepath.Join("snapshots", "g1.jsonl"),
		filepath.Join("reports", "tool-outputs", "call-1.png"),
		filepath.Join("reports", "tool-outputs", "call-1.txt"),
	} {
		p := filepath.Join(session, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_MCODE_ROOT", root)

	seen := McodeSessionFiles()
	if len(seen) != 1 {
		t.Fatalf("transcripts = %d, want 1: %v", len(seen), seen)
	}
	beside := McodeSidecarFiles()
	if len(beside) != 8 {
		t.Fatalf("sidecars = %d, want 8: %v", len(beside), beside)
	}
	for _, p := range beside {
		if strings.HasSuffix(p, mcodeTranscriptName) {
			t.Fatalf("the transcript was filed as bookkeeping: %s", p)
		}
	}
}

// The registry loader finds the session and files it under the manifest's id.
func TestMcodeTheStoreIsReadableUnderTheManifestID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "10", "20", "03-00-00-000-session_abc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schemaVersion":1,"sessionId":"mvs_lookup",` +
		`"createdAtMs":1792465960806,"updatedAtMs":1792465960806}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	turn := `{"message_id":"m1","turn_id":"t1","message":{"role":"user",` +
		`"content":[{"type":"text","text":"what did we decide about mcode?"}],` +
		`"timestamp":1792465960900}}`
	if err := os.WriteFile(filepath.Join(dir, mcodeTranscriptName), []byte(turn+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_MCODE_ROOT", root)

	ss := LoadMcode()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	if ss[0].ID != "mvs_lookup" {
		t.Fatalf("id = %q, want the manifest's", ss[0].ID)
	}
}

// A summary with nothing in it is not a summary: the runtime writes one
// wherever it cut, and a cut that took nothing leaves the field empty.
func TestMcodeAnEmptyCompactionSummaryIsNotIndexed(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"message_id":"m1","turn_id":"t1:compaction","message":{"role":"compactionSummary","summary":"   ","timestamp":1792465960900}}`,
		`{"message_id":"m2","turn_id":"t1","message":{"role":"user","content":[{"type":"text","text":"carry on"}],"timestamp":1792465960900}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, mcodeTranscriptName), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseMcodeFile(filepath.Join(dir, mcodeTranscriptName))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleSummary {
			t.Fatal("an empty summary was indexed")
		}
	}
}

func containsRole(roles []string, want string) bool {
	return slices.Contains(roles, want)
}

// firstUserMessage is the first turn the person wrote. A session that has
// been compacted opens with the summary the compaction wrote, so the first
// message of a session is not always the person's.
func firstUserMessage(s model.Session) *model.Message {
	for i, m := range s.Messages {
		if m.Role == "user" {
			return &s.Messages[i]
		}
	}
	return nil
}
