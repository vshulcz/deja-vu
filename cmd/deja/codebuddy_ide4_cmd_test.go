package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// These tests build a miniature CodeBuddy IDE 4.x history under a temp home.
// Nothing here reads a contributor's history.
const (
	cb4CmdWS        = "0123456789abcdef0123456789abcdef"
	cb4CmdID        = "abcdef0123456789abcdef0123456789"
	cb4CmdCreatedAt = "2026-01-02T03:04:05Z"
)

// cb4CmdEnv pins every CodeBuddy root and the home directory so a developer's
// own stores cannot reach the assertions. It returns the 4.x store root.
func cb4CmdEnv(t *testing.T) string {
	t.Helper()
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	legacy := filepath.Join(tmp, "legacy-data")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", legacy)
	t.Setenv("DEJA_CODEBUDDY_ROOTS", filepath.Join(tmp, "cli-projects"))
	t.Setenv("CODEBUDDY_CONFIG_DIR", filepath.Join(tmp, "cli-config"))
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	t.Setenv("DEJA_STORES", "")
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "")
	return legacy
}

func cb4CmdWrite(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, ok := v.([]byte)
	if !ok {
		var err error
		b, err = json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// cb4CmdConversation writes one 4.x conversation with a user turn and an
// assistant turn. It returns the conversation index.json, whose shape resume
// and the write-back refusal both key on.
func cb4CmdConversation(t *testing.T, root string) string {
	t.Helper()
	ws := filepath.Join(root, "u1", "CodeBuddyIDE", "u1", "history", cb4CmdWS)
	conv := filepath.Join(ws, cb4CmdID)
	cb4CmdWrite(t, filepath.Join(ws, "index.json"), map[string]any{
		"conversations": []any{map[string]any{
			"id": cb4CmdID, "type": "craft", "name": "t",
			"createdAt": cb4CmdCreatedAt, "lastMessageAt": "2026-01-02T03:05:05Z",
		}},
	})
	cb4CmdWrite(t, filepath.Join(conv, "index.json"), map[string]any{
		"messages": []any{map[string]any{"id": "m1"}, map[string]any{"id": "m2"}},
		"requests": []any{map[string]any{
			"id": "r1", "messages": []string{"m1", "m2"}, "startedAt": 1767323045000,
		}},
	})
	user := `{"role":"user","content":[{"type":"text","text":"<user_query>\nhello\n</user_query>"}]}`
	cb4CmdWrite(t, filepath.Join(conv, "messages", "m1.json"), map[string]any{
		"id": "m1", "role": "user", "message": user, "extra": "{}", "createdAt": cb4CmdCreatedAt,
	})
	assistant := `{"role":"assistant","content":[{"type":"text","text":"hi"}]}`
	cb4CmdWrite(t, filepath.Join(conv, "messages", "m2.json"), map[string]any{
		"id": "m2", "role": "assistant", "message": assistant, "extra": "{}", "createdAt": cb4CmdCreatedAt,
	})
	// The IDE's own files beside the conversation: not transcripts deja lost.
	cb4CmdWrite(t, filepath.Join(conv, "index_bak.json"), map[string]any{})
	cb4CmdWrite(t, filepath.Join(conv, ".index_bak.json"), map[string]any{})
	cb4CmdWrite(t, filepath.Join(conv, "assets", "image.json"), map[string]any{})
	return filepath.Join(conv, "index.json")
}

func TestDoctorCodeBuddyIDE4RowCountsConversations(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	if !sources.IsCodeBuddyIDE4Session(index) {
		t.Fatal("fixture is not a 4.x conversation")
	}
	var out bytes.Buffer
	doctorHarnessStores(&out, t.TempDir(), true)
	row := ""
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(line, "  codebuddy ") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("no codebuddy row")
	}
	if !strings.Contains(row, "found") || !strings.Contains(row, legacy) {
		t.Errorf("row does not name the legacy root as found: %s", row)
	}
	if !strings.Contains(row, "1 file, 1 IDE 4.x conversation") {
		t.Errorf("row does not count both stores: %s", row)
	}
	if strings.Contains(row, "not recognised here") || strings.Contains(row, "subagent transcripts skipped") {
		t.Errorf("bookkeeping files reported as unread transcripts: %s", row)
	}
}

// cb4CmdDoctorRow is doctor's codebuddy store row, as the default report
// prints it: absent stores fold into one line and a found row keeps its own.
func cb4CmdDoctorRow(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	doctorHarnessStores(&out, t.TempDir(), false)
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(line, "  codebuddy ") {
			return line
		}
	}
	t.Fatalf("no codebuddy row:\n%s", out.String())
	return ""
}

// A legacy root may be a history directory. The workspace index one level
// under it is the IDE's list of conversations, not one of them: the row counts
// the conversations alone and reports no file it failed to recognise, the
// same as for the Data root above it.
func TestDoctorCodeBuddyIDE4HistoryRootCountsConversationsOnly(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	// A second workspace whose list names a conversation with no messages yet.
	other := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(index))), "fedcba9876543210fedcba9876543210")
	cb4CmdWrite(t, filepath.Join(other, "index.json"), map[string]any{"conversations": []any{}})
	history := filepath.Dir(filepath.Dir(filepath.Dir(index)))
	for _, root := range []string{legacy, history} {
		t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", root)
		if got := sources.CodeBuddyIDE4SessionFiles(); len(got) != 1 || got[0] != index {
			t.Fatalf("root %s: 4.x files = %v, want the one conversation", root, got)
		}
		row := cb4CmdDoctorRow(t)
		if !strings.Contains(row, "1 file, 1 IDE 4.x conversation") {
			t.Errorf("root %s: row does not count one conversation: %s", root, row)
		}
		if strings.Contains(row, "not recognised here") {
			t.Errorf("root %s: workspace indexes reported as unread transcripts: %s", root, row)
		}
	}
}

// The human doctor report routes the JSON store inspection through the same
// row. A 4.x-only machine has a found codebuddy row with the conversation
// count, not a folded "missing" one.
func TestDoctorTextCodeBuddyIDE4UsesTheReportRow(t *testing.T) {
	legacy := cb4CmdEnv(t)
	cb4CmdConversation(t, legacy)
	dir := t.TempDir()
	var out bytes.Buffer
	printDoctorText(&out, collectDoctorReport(func() (string, bool) { return "", false }, dir), nil, dir, true, true)
	row := ""
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(line, "  codebuddy ") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("no codebuddy row")
	}
	if !strings.Contains(row, "found") || !strings.Contains(row, legacy) || !strings.Contains(row, "1 IDE 4.x conversation") {
		t.Errorf("row does not report the 4.x store: %s", row)
	}
}

// doctor --json names both roots and probes a 4.x index.json with the 4.x
// reader: a JSONL reader would report the store's newest file as parsed-zero.
func TestDoctorJSONCodeBuddyIDE4Store(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	now := time.Now()
	if err := os.Chtimes(index, now, now); err != nil {
		t.Fatal(err)
	}
	cli := os.Getenv("DEJA_CODEBUDDY_ROOTS")
	report := collectDoctorReport(nil, t.TempDir())
	var store *doctorStore
	for i := range report.Stores {
		if report.Stores[i].Name == "codebuddy" {
			store = &report.Stores[i]
			break
		}
	}
	if store == nil {
		t.Fatal("doctor --json carries no codebuddy store")
	}
	if store.State != "ok" {
		t.Errorf("state = %q, want ok (error %q)", store.State, store.Error)
	}
	if store.Files != 1 {
		t.Errorf("files = %d, want 1", store.Files)
	}
	if !contains(store.Paths, legacy) || !contains(store.Paths, cli) {
		t.Errorf("paths = %v, want the CLI and 4.x roots", store.Paths)
	}
}

// deja sources reads both stores with the same loader as the index, names
// both roots and counts the 4.x conversation's turns.
func TestSourcesCodeBuddyIDE4Store(t *testing.T) {
	legacy := cb4CmdEnv(t)
	cb4CmdConversation(t, legacy)
	var out bytes.Buffer
	printSourcesTo(&out, t.TempDir())
	row := ""
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(line, "codebuddy\t") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no codebuddy row")
	}
	if !strings.Contains(row, "sessions=1") || !strings.Contains(row, "messages=2") {
		t.Errorf("row does not count the 4.x conversation: %s", row)
	}
	if !strings.Contains(row, legacy) {
		t.Errorf("row does not name the legacy root: %s", row)
	}
	// The configured roots are what the loader walks, even when commonDir
	// narrows the printed location to the files that exist.
	items := sources.LoadCodeBuddyAll()
	if len(items) != 1 || items[0].ID != cb4CmdID {
		t.Fatalf("LoadCodeBuddyAll = %#v, want the 4.x conversation", items)
	}
	// Both roots are named even when the CLI store holds no transcript.
	if !strings.Contains(strings.Join(append(sources.CodeBuddyRoots(), sources.CodeBuddyIDE4Roots()...), string(os.PathListSeparator)), legacy) {
		t.Error("configured roots do not include the 4.x store")
	}
}

// A 4.x conversation has no .jsonl transcript for `codebuddy -r` to look up.
// Say what would make it resumable and where the conversation can be read now.
func TestResumeCodeBuddyIDE4CannotOpen(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	s := model.Session{Harness: "codebuddy", ID: cb4CmdID, Path: index}
	_, cmd, err := resumeCommand(s)
	if err == nil || cmd != "" {
		t.Fatalf("cmd %q err %v, want a refusal", cmd, err)
	}
	for _, want := range []string{"CodeBuddy IDE 4.x", "`codebuddy -r` cannot open", "CodeBuddy IDE 5.x converts it", "`deja resume` works", "`deja show "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "index has no <id>.jsonl path") {
		t.Errorf("refusal gives the CLI transcript reason for a 4.x conversation: %v", err)
	}
}

// Once IDE 5.x converts a 4.x conversation, the CodeBuddy Code transcript is
// the session under the same id. Doctor probes it with the CLI reader rather
// than reporting the old index as parsed-zero.
func TestDoctorProbeCodeBuddyIDE4Converted(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	cli := os.Getenv("DEJA_CODEBUDDY_ROOTS")
	marker := filepath.Join(cli, "folder", cb4CmdID, ".codebuddy-ide-4-conversion.json")
	cb4CmdWrite(t, marker, map[string]any{"conversationId": cb4CmdID})
	transcript := filepath.Join(cli, "folder", cb4CmdID+".jsonl")
	line := `{"id":"u1","timestamp":1767323045000,"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}],"sessionId":"` + cb4CmdID + `"}` + "\n"
	cb4CmdWrite(t, transcript, []byte(line))
	if got := sources.CodeBuddyIDE4ConvertedTranscript(index); got != transcript {
		t.Fatalf("converted transcript = %q, want %q", got, transcript)
	}
	ss, err := doctorProbeCodeBuddy(index)
	if err != nil || len(ss) != 1 || ss[0].ID != cb4CmdID {
		t.Fatalf("probe = %#v, %v, want the converted session", ss, err)
	}
	// deja resume works from the converted transcript: it is a CodeBuddy Code
	// session with its own .jsonl path, not the 4.x index.
	if _, _, err := resumeCommand(ss[0]); err != nil && strings.Contains(err.Error(), "CodeBuddy IDE 4.x") {
		t.Errorf("converted session still refused as 4.x: %v", err)
	}
}

// An index entry from before the conversion still names the 4.x index.json.
// Resume must follow the converted transcript, whose records hold the working
// directory `codebuddy -r` needs, rather than asking the IDE index for one.
func TestResumeCodeBuddyIDE4ConvertedUsesTheTranscript(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	cli := os.Getenv("DEJA_CODEBUDDY_ROOTS")
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(cli, "folder", cb4CmdID, ".codebuddy-ide-4-conversion.json")
	cb4CmdWrite(t, marker, map[string]any{"conversationId": cb4CmdID})
	transcript := filepath.Join(cli, "folder", cb4CmdID+".jsonl")
	line := `{"id":"u1","timestamp":1767323045000,"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}],"sessionId":"` + cb4CmdID + `","cwd":` + jsonString(work) + `}` + "\n"
	cb4CmdWrite(t, transcript, []byte(line))
	if got := sources.CodeBuddyIDE4ConvertedTranscript(index); got != transcript {
		t.Fatalf("converted transcript = %q, want %q", got, transcript)
	}
	s := model.Session{Harness: "codebuddy", ID: cb4CmdID, Path: index}
	dir, cmd, err := resumeCommand(s)
	if err != nil || dir != work || cmd != "codebuddy -c -r "+cb4CmdID {
		t.Fatalf("dir %q cmd %q err %v, want the transcript's directory and command", dir, cmd, err)
	}
	if s.Path != index {
		t.Error("resumeCommand mutated the session it was given")
	}
}

// The doctor row names a 4.x root only when it holds a conversation. An
// existing-but-empty CLI root still counts as present, matching the other
// transcript rows; with neither root on disk the row folds as missing.
func TestDoctorCodeBuddyIDE4LocationAndPresence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		legacy     bool
		cliDir     bool
		wantLegacy bool
		wantFound  bool
	}{
		{"empty CLI root and 4.x store", true, true, true, true},
		{"CLI root and conversation only", true, false, true, true},
		{"empty CLI root, no 4.x store", false, true, false, true},
		{"neither root", false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := cb4CmdEnv(t)
			cli := os.Getenv("DEJA_CODEBUDDY_ROOTS")
			if tc.legacy {
				cb4CmdConversation(t, legacy)
			}
			if tc.cliDir {
				if err := os.MkdirAll(cli, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			doctorHarnessStores(&out, t.TempDir(), true)
			row := ""
			for line := range strings.SplitSeq(out.String(), "\n") {
				if strings.HasPrefix(line, "  codebuddy ") {
					row = line
					break
				}
			}
			if row == "" {
				t.Fatalf("no codebuddy row:\n%s", out.String())
			}
			if got := strings.Contains(row, legacy); got != tc.wantLegacy {
				t.Errorf("row names the 4.x root = %v, want %v: %s", got, tc.wantLegacy, row)
			}
			if got := strings.Contains(row, "found"); got != tc.wantFound {
				t.Errorf("row presence = %v, want %v: %s", got, tc.wantFound, row)
			}
			if !strings.Contains(row, cli) {
				t.Errorf("row omits the CLI root: %s", row)
			}
		})
	}
}

// doctor probes a CodeBuddy Code transcript with the CLI reader, and a 4.x
// conversation nobody converted with the 4.x reader.
func TestDoctorProbeCodeBuddyPicksTheReader(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	ss, err := doctorProbeCodeBuddy(index)
	if err != nil || len(ss) != 1 || ss[0].ID != cb4CmdID || ss[0].Path != index {
		t.Fatalf("probe of the 4.x index = %#v, %v; want the 4.x conversation", ss, err)
	}
	cli := os.Getenv("DEJA_CODEBUDDY_ROOTS")
	const id = "cli0123456789abcdef"
	transcript := filepath.Join(cli, "work-p", id+".jsonl")
	line := `{"id":"u1","timestamp":1767323045000,"type":"message","role":"user","content":[{"type":"input_text","text":"from the CLI"}],"sessionId":"` + id + `"}` + "\n"
	cb4CmdWrite(t, transcript, []byte(line))
	ss, err = doctorProbeCodeBuddy(transcript)
	if err != nil || len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("probe of the CLI transcript = %#v, %v; want the CLI session", ss, err)
	}
}
