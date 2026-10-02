package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Cherry Studio writes Claude Code transcripts from a desktop app, and appends
// the same API call three or four times as the stream progresses: a new uuid
// each time, the same requestId and message id, the text growing. Read plainly
// that is one reply stored three times in prefixes — measured on this fixture
// before the collapse: "the advisory", "the advisory lock was", "the advisory
// lock was never released" (#3644).
const cherrySnapshotTranscript = `{"type":"user","sessionId":"cs-1","timestamp":"2026-07-17T09:00:00.000Z","cwd":"/work/api","message":{"role":"user","content":"why does the migration hang"}}
{"type":"assistant","uuid":"u1","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:01.000Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory"}]}}
{"type":"assistant","uuid":"u2","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:01.500Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory lock was"}]}}
{"type":"assistant","uuid":"u3","requestId":"req-1","sessionId":"cs-1","timestamp":"2026-07-17T09:00:02.000Z","message":{"id":"msg-1","role":"assistant","content":[{"type":"text","text":"the advisory lock was never released"}]}}
{"type":"assistant","uuid":"u4","requestId":"req-2","sessionId":"cs-1","timestamp":"2026-07-17T09:00:05.000Z","message":{"id":"msg-2","role":"assistant","content":[{"type":"text","text":"rerun the migration with a lock timeout"}]}}
`

func writeCherryStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "CherryStudio", "Data", "Agents", ".claude", "projects", "-work-api")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cs-1.jsonl"), []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", filepath.Dir(root))
	return root
}

func TestCherryStudioCollapsesAStreamingRun(t *testing.T) {
	writeCherryStore(t)

	files := CherryStudioSessionFiles()
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	ss := LoadCherryStudio()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "cherrystudio" {
		t.Errorf("harness = %q — a Cherry Studio session must not report as Claude Code", s.Harness)
	}
	var assistant []string
	for _, m := range s.Messages {
		if m.Role == "assistant" {
			assistant = append(assistant, m.Text)
		}
	}
	if len(assistant) != 2 {
		t.Fatalf("assistant messages = %d %q, want one per API call", len(assistant), assistant)
	}
	if assistant[0] != "the advisory lock was never released" {
		t.Errorf("the collapsed reply is %q, want the complete snapshot", assistant[0])
	}
	if assistant[1] != "rerun the migration with a lock timeout" {
		t.Errorf("the second call was folded into the first: %q", assistant[1])
	}
	// The user turn survives the collapse.
	if len(s.Messages) < 3 || s.Messages[0].Role != "user" {
		t.Errorf("the question is missing: %+v", s.Messages)
	}
}

// The same file read through the stock Claude parser keeps every snapshot,
// which is what makes the collapse a property of this store rather than a
// change to Claude Code's own reader.
func TestClaudeItselfIsUnchangedByTheCollapse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cs-1.jsonl")
	if err := os.WriteFile(p, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseClaudeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d", len(ss))
	}
	n := 0
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" {
			n++
		}
	}
	if n != 4 {
		t.Errorf("the stock reader kept %d assistant records, want all four", n)
	}
	if ss[0].Harness != "claude" {
		t.Errorf("harness = %q, want claude", ss[0].Harness)
	}
}

// A stock Claude transcript must not be claimed by this harness, and a Cherry
// Studio one must not be claimed by Claude's kind.
func TestCherryStudioAndClaudeDoNotClaimEachOther(t *testing.T) {
	root := writeCherryStore(t)
	cherry := filepath.Join(root, "cs-1.jsonl")

	stockHome := t.TempDir()
	stock := filepath.Join(stockHome, ".claude", "projects", "-work-api")
	if err := os.MkdirAll(stock, 0o755); err != nil {
		t.Fatal(err)
	}
	stockFile := filepath.Join(stock, "s-1.jsonl")
	if err := os.WriteFile(stockFile, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", stock)

	kinds := map[string]string{}
	for _, h := range Registry() {
		for _, k := range h.Kinds {
			for _, p := range []string{cherry, stockFile} {
				if k.Match(p) {
					kinds[p] += h.Name + ":" + k.Name + " "
				}
			}
		}
	}
	if !strings.Contains(kinds[cherry], "cherrystudio") {
		t.Errorf("the Cherry Studio transcript is claimed by %q", kinds[cherry])
	}
	if strings.Contains(kinds[stockFile], "cherrystudio") {
		t.Errorf("a stock Claude transcript is claimed by cherrystudio: %q", kinds[stockFile])
	}
}

// cherryHome points every app-dir lookup at a fresh home, so the default
// Cherry Studio data dir is a temp dir on every platform.
func cherryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("DEJA_CHERRYSTUDIO_ROOTS", "")
	return home
}

func copyFixture(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", from))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Cherry Studio runs agents on three runtimes and gives each its own store
// under Data/Agents: .claude, .pi/sessions and .dsh/sessions. Before the fix
// only the Claude one was read, so a Cherry agent on pi or dsh left nothing in
// the index (#4342).
func TestCherryStudioReadsItsPiAndDshAgents(t *testing.T) {
	cherryHome(t)
	agents := filepath.Join(cherryStudioAppDirs()[0], "Data", "Agents")
	pi := filepath.Join(agents, ".pi", "sessions", "2026-07-17T10-00-00-000Z_c0ffee00-0000-4000-8000-000000000002.jsonl")
	dsh := filepath.Join(agents, ".dsh", "sessions", "--work-pgbouncer-lab--", "session-c0ffee00-0000-4000-8000-000000000003", "session.jsonl")
	copyFixture(t, "fixtures/registry/pi/session.jsonl", pi)
	copyFixture(t, "fixtures/registry/deepseek/sessions/--work-pgbouncer-lab--/session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1/session.jsonl", dsh)

	got := map[string]bool{}
	for _, s := range LoadCherryStudio() {
		if s.Harness != "cherrystudio" {
			t.Errorf("%s: harness = %q, want cherrystudio", s.Path, s.Harness)
		}
		if len(s.Messages) == 0 {
			t.Errorf("%s: no messages", s.Path)
		}
		got[s.Path] = true
	}
	if !got[pi] || !got[dsh] {
		t.Fatalf("read %v, want the pi and the dsh session", got)
	}
	// The incremental path must route each file to the same reader: the dsh
	// log would otherwise fall to the deepseek kind, which matches by name.
	for _, p := range []string{pi, dsh} {
		if k := KindForPath(p); !strings.HasPrefix(k, "cherrystudio") {
			t.Errorf("%s is claimed by kind %q", p, k)
		}
	}
}

// Cherry Studio lets a user move its data dir; the new place is kept in
// ~/.cherrystudio/boot-config.json under app.user_data_path, a map from the
// executable to the directory. Before the fix deja only looked at the default
// and found nothing after a move (#4347).
func TestCherryStudioFollowsAMovedDataDir(t *testing.T) {
	home := cherryHome(t)
	moved := filepath.Join(t.TempDir(), "moved-data")
	cfg := `{"app.disable_hardware_acceleration":false,"app.user_data_path":{"/Applications/Cherry Studio.app/Contents/MacOS/Cherry Studio":` + strconvQuote(moved) + `},"temp.user_data_relocation":null}`
	if err := os.MkdirAll(filepath.Join(home, ".cherrystudio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cherrystudio", "boot-config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(moved, "Data", "Agents", ".claude", "projects", "-work-api", "cs-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	files := CherryStudioSessionFiles()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("files = %v, want the transcript in the moved dir", files)
	}
	if k := KindForPath(p); k != "cherrystudio" {
		t.Errorf("kind = %q, want cherrystudio", k)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// Cherry Studio runs dsh with DSH_HOME inside its data dir, so a deja it starts
// inherits a DeepSeek root that is Cherry's store. The log belongs to Cherry
// Studio alone; listed by deepseek too, it was counted under both (#4342).
func TestCherryStudioDshLogIsNotAlsoDeepSeeks(t *testing.T) {
	cherryHome(t)
	dshHome := filepath.Join(cherryStudioAppDirs()[0], "Data", "Agents", ".dsh")
	log := filepath.Join(dshHome, "sessions", "--work-pgbouncer-lab--", "session-c0ffee00-0000-4000-8000-000000000003", "session.jsonl")
	copyFixture(t, "fixtures/registry/deepseek/sessions/--work-pgbouncer-lab--/session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1/session.jsonl", log)
	t.Setenv("DEJA_DEEPSEEK_ROOT", "")
	t.Setenv("DSH_HOME", dshHome)

	if !slices.Contains(CherryStudioSessionFiles(), log) {
		t.Fatalf("Cherry Studio does not list its own dsh log, so this measures nothing")
	}
	if got := DeepSeekSessionFiles(); slices.Contains(got, log) {
		t.Errorf("deepseek lists Cherry Studio's dsh log too: %q", got)
	}
}

// A data dir moved to the home directory makes the legacy <dir>/.claude/projects
// root Claude Code's own store. Cherry Studio must not list those sessions as
// its own (#4347).
func TestCherryStudioMovedToHomeLeavesClaudesStoreAlone(t *testing.T) {
	home := cherryHome(t)
	t.Setenv("DEJA_CLAUDE_ROOT", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	stock := filepath.Join(home, ".claude", "projects", "-work-api", "s-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(stock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stock, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cherrystudio"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]any{"app.user_data_path": map[string]string{"/Applications/Cherry Studio.app/Contents/MacOS/Cherry Studio": home}})
	if err := os.WriteFile(filepath.Join(home, ".cherrystudio", "boot-config.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ClaudeFiles(), stock) {
		t.Fatalf("Claude does not list its own transcript, so this measures nothing")
	}
	if got := CherryStudioSessionFiles(); slices.Contains(got, stock) {
		t.Errorf("Cherry Studio lists Claude Code's own transcript: %q", got)
	}
}

// A move can leave the old data dir behind. The directory the app runs from
// comes first, so a reader that takes one file of the app's — its database,
// for doctor — reads the live one rather than the copy left at the default
// (#4347).
func TestCherryStudioMovedDirComesBeforeTheDefault(t *testing.T) {
	home := cherryHome(t)
	moved := filepath.Join(home, "elsewhere", "CherryStudio")
	if err := os.MkdirAll(filepath.Join(home, ".cherrystudio"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(map[string]any{"app.user_data_path": map[string]string{"/Applications/Cherry Studio.app/Contents/MacOS/Cherry Studio": moved}})
	if err := os.WriteFile(filepath.Join(home, ".cherrystudio", "boot-config.json"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := cherryStudioAppDirs()
	if len(dirs) != 2 || dirs[0] != moved || dirs[1] != cherryStudioDefaultDir() {
		t.Errorf("app dirs = %q, want the moved one, then the default", dirs)
	}
}

// A tail that carries more snapshots of the call the stored part ended on
// rewrites a stored reply and cannot be appended; a tail that starts a new
// call can (#4346).
func TestCherryStudioResumesOnlyOnANewCall(t *testing.T) {
	lines := strings.SplitAfter(cherrySnapshotTranscript, "\n")
	p := filepath.Join(t.TempDir(), "cs-1.jsonl")
	if err := os.WriteFile(p, []byte(cherrySnapshotTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	at := func(n int) int64 { return int64(len(strings.Join(lines[:n], ""))) }
	if CherryStudioResumes(p, at(2)) {
		t.Error("a tail continuing req-1 was allowed to append to its half reply")
	}
	if !CherryStudioResumes(p, at(4)) {
		t.Error("a tail starting req-2 was refused")
	}
	if !CherryStudioResumes(p, at(1)) {
		t.Error("a tail after the user line was refused")
	}
}

// Resumes reads the tail only up to the first line that names a call. A
// stream's snapshots run back to back, so once another call starts the stored
// one is done, and reading on cost a search the whole tail before the inline
// append cap could hand it off (#4346).
func TestCherryStudioResumesStopsAtTheNextCall(t *testing.T) {
	lines := strings.SplitAfter(cherrySnapshotTranscript, "\n")
	at := int64(len(strings.Join(lines[:2], "")))
	// After the stored half of req-1: a new call, then a stray req-1 line the
	// check must not reach.
	body := strings.Join(lines[:2], "") + lines[4] + lines[1]
	p := filepath.Join(t.TempDir(), "cs-1.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !CherryStudioResumes(p, at) {
		t.Error("read past the call that started after the stored one")
	}
}
