package main

import (
	"bytes"
	"crypto/md5" //nolint:gosec // Cursor names its chat folder by this hash
	"encoding/hex"
	"encoding/json"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// aider appends to its history and loads none of it back unless it is started
// with --restore-chat-history, so "run aider there and it continues the same
// history" sent the reader into an empty chat. The flag restores the whole
// file, every launch in it, and the hint has to say so (#4329).
func TestAiderResumeNamesRestoreChatHistory(t *testing.T) {
	// The resolved temp dir, as resume_qwen_gone_test.go takes it: Windows
	// hands it out under its 8.3 name (RUNNER~1) (#4455).
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "proj")
	s := model.Session{Harness: "aider", ID: "aider-20909474c017-1", Path: filepath.Join(dir, ".aider.chat.history.md")}
	_, _, err = resumeCommand(s)
	if err == nil {
		t.Fatal("aider has no resume of one session; want the hint as an error")
	}
	msg := err.Error()
	for _, want := range []string{"aider --restore-chat-history", dir, "whole", "deja show " + s.ID} {
		if !strings.Contains(msg, want) {
			t.Errorf("hint %q does not say %q", msg, want)
		}
	}
	if strings.Contains(msg, "continues the same history") {
		t.Errorf("hint still says plain aider continues the history: %q", msg)
	}

	// A history moved off the default name is only found by naming it. The
	// path is printed as the paste-safe word, which quotes a Windows one for
	// its backslashes and ~ (#4455).
	s.Path = filepath.Join(dir, "notes", "chat.md")
	_, _, err = resumeCommand(s)
	if err == nil || !strings.Contains(err.Error(), "--chat-history-file "+shellQuoteForPaste(s.Path)) {
		t.Errorf("a moved history is not named: %v", err)
	}
}

// The command is pasted into a shell, so a path with a space in it is quoted.
func TestAiderResumeQuotesAMovedHistory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "my notes", "chat.md")
	_, _, err := resumeCommand(model.Session{Harness: "aider", ID: "aider-1", Path: p})
	if err == nil || !strings.Contains(err.Error(), "--chat-history-file '"+p+"'") {
		t.Errorf("the path is not quoted: %v", err)
	}
}

// The ChatGPT desktop app opens a Codex CLI session by its id at
// codex://threads/<id> (#4907), on the two systems it runs on.
func TestResumeAppOpensACodexSessionInTheChatGPTApp(t *testing.T) {
	const id = "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b"
	codex := model.Session{Harness: "codex", ID: id, Project: "app"}
	for goos, want := range map[string]string{
		"darwin":  "open codex://threads/" + id,
		"windows": "cmd /c start  codex://threads/" + id,
	} {
		argv, err := codexAppCommand(codex, goos)
		if err != nil || strings.Join(argv, " ") != want {
			t.Errorf("%s: %q, %v; want %q", goos, argv, err, want)
		}
	}
	if _, err := codexAppCommand(codex, "linux"); err == nil || !strings.Contains(err.Error(), "macOS and Windows") {
		t.Errorf("linux has no ChatGPT app, got %v", err)
	}
	for name, s := range map[string]model.Session{
		"another agent's session": {Harness: "claude", ID: id, Project: "app"},
		"a codex exec entry":      {Harness: "codex", ID: id, Project: "history"},
		"a synced session":        {Harness: "codex", ID: id, Project: "imported:laptop/app"},
		"an id with a quote":      {Harness: "codex", ID: id + "'", Project: "app"},
	} {
		if argv, err := codexAppCommand(s, "darwin"); err == nil {
			t.Errorf("%s opened as %q", name, argv)
		}
	}
}

// --app opens the session itself, so a flag that runs or restores a command
// alongside it is refused rather than dropped.
func TestResumeAppRefusesExecAndWriteBack(t *testing.T) {
	for _, extra := range []string{"--exec", "--write-back"} {
		err := runResume(t.TempDir(), []string{"019a2b3c", "--app", extra}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "neither --exec nor --write-back") {
			t.Errorf("--app %s: %v", extra, err)
		}
	}
}

// The continue-in picker leads with the app for a Codex session, and offers
// it for nothing else.
func TestContinuePickerOffersTheChatGPTAppForACodexSession(t *testing.T) {
	const id = "019a2b3c-4d5e-7f60-8a9b-0c1d2e3f4a5b"
	ts := []continueTarget{{id: "claude", installed: true}}
	got := withApp(ts, model.Session{Harness: "codex", ID: id, Project: "app"}, "darwin")
	if len(got) != 2 || got[0].id != chatgptApp || !got[0].installed || agentName(got[0].id) != "ChatGPT app" {
		t.Fatalf("codex session: %+v", got)
	}
	for _, s := range []model.Session{{Harness: "claude", ID: id, Project: "app"}, {Harness: "codex", ID: id, Project: "history"}} {
		if got := withApp(ts, s, "darwin"); len(got) != 1 {
			t.Errorf("%s/%s offered the app: %+v", s.Harness, s.Project, got)
		}
	}
	if got := withApp(ts, model.Session{Harness: "codex", ID: id, Project: "app"}, "linux"); len(got) != 1 {
		t.Errorf("offered on linux: %+v", got)
	}
}

func TestResumeRejectsBlankPrefix(t *testing.T) {
	dir := hermeticEnv(t)
	want := runResume(dir, nil, io.Discard)
	if want == nil {
		t.Fatal("resume without an id should fail")
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"empty", []string{""}},
		{"spaces", []string{"   "}},
		{"tabs and newline", []string{"\t\n"}},
		{"empty before another prefix", []string{"", "sess1234"}},
		{"blank after an option", []string{"--exec", "  "}},
		{"blank after a prefix", []string{"sess1234", "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runResume(dir, tc.args, io.Discard)
			if err == nil || err.Error() != want.Error() {
				t.Fatalf("runResume(%q) = %v; want %v", tc.args, err, want)
			}
		})
	}
}

func writeResumeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// `cmd --resume <id>` looks the id up under the project folder of the current
// directory only, so the command runs where the session did (#4372). The
// directory is the cwd on the v3 header line; the folder name is a lossy slug.
func TestResumeCommandCodeRunsInTheSessionDirectory(t *testing.T) {
	proj := t.TempDir()
	id := "1583a430-5538-475d-9190-0d726c7a374f"
	transcript := filepath.Join(t.TempDir(), "projects", "private-tmp-proj-cc", id+".jsonl")
	cwd, err := json.Marshal(proj)
	if err != nil {
		t.Fatal(err)
	}
	writeResumeFile(t, transcript,
		`{"type":"session","version":3,"id":"`+id+`","timestamp":"2026-10-01T20:09:14.839Z","cwd":`+string(cwd)+`}`+"\n"+
			`{"type":"message","id":"f82f93b8","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`+"\n")

	bin := "cmd"
	if runtime.GOOS == "windows" {
		bin = "cmdc"
	}
	dir, cmd, err := resumeCommand(model.Session{Harness: "commandcode", ID: id, Path: transcript})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin+" --resume "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the header's cwd %q", dir, proj)
	}

	// The older flat shape records no directory: `--session`, which finds the
	// id in any project, without a cd (#4460).
	old := filepath.Join(filepath.Dir(transcript), "aaaa1111.jsonl")
	writeResumeFile(t, old, `{"role":"user","content":"fix the retry loop","timestamp":"2026-09-16T12:00:05Z","sessionId":"aaaa1111"}`+"\n")
	dir, cmd, err = resumeCommand(model.Session{Harness: "commandcode", ID: "aaaa1111", Path: old})
	if err != nil {
		t.Fatal(err)
	}
	if dir != "" || cmd != bin+" --session aaaa1111" {
		t.Errorf("v2 session: got (%q, %q)", dir, cmd)
	}

	// A directory that is gone gets no cd that would fail before cmd starts.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	if dir, cmd, err := resumeCommand(model.Session{Harness: "commandcode", ID: id, Path: transcript}); err != nil || dir != "" || cmd != bin+" --session "+id {
		t.Errorf("got (%q, %q, %v) for a directory that is gone, want %s --session with no cd", dir, cmd, err, bin)
	}
}

// `cn --fork <id>` loads the history from anywhere and runs its tools in the
// current directory, so the fork runs in the session's workspace (#4375).
func TestResumeContinueForksInTheWorkspace(t *testing.T) {
	proj := t.TempDir()
	sessions := filepath.Join(t.TempDir(), "sessions")
	id := "5f38800d-4bfe-4a34-a6f1-b117d8ce618c"
	ws, err := json.Marshal(proj)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, id+".json")
	writeResumeFile(t, path, `{"sessionId":"`+id+`","title":"retry","workspaceDirectory":`+string(ws)+`,"history":[]}`)

	dir, cmd, err := resumeCommand(model.Session{Harness: "continue", ID: id, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "cn --fork "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the workspace %q", dir, proj)
	}

	// The IDE extension records the workspace as a file URI, and an older
	// session file leaves it to sessions.json.
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(proj)}).String()
	if runtime.GOOS == "windows" {
		uri = (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(proj)}).String()
	}
	list, err := json.Marshal([]map[string]string{{"sessionId": id, "workspaceDirectory": uri}})
	if err != nil {
		t.Fatal(err)
	}
	writeResumeFile(t, filepath.Join(sessions, "sessions.json"), string(list))
	writeResumeFile(t, path, `{"sessionId":"`+id+`","history":[]}`)
	if dir, _, _ := resumeCommand(model.Session{Harness: "continue", ID: id, Path: path}); filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q from a file URI in sessions.json, want %q", dir, proj)
	}

	// Gone: no cd.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(model.Session{Harness: "continue", ID: id, Path: path}); dir != "" {
		t.Errorf("dir = %q for a workspace that is gone", dir)
	}
}

// How many harnesses `deja resume` can reopen is written in prose and was
// checked by nothing, so it went stale in every direction at once. Four pages,
// three different numbers, none of them right:
//
//	find-a-session.html   "twenty of the thirty-three … the remaining five"
//	resume-a-session.html "Twenty of the twenty-five indexed harnesses"
//	lost-context.html     "Nineteen of thirty-three" and "twenty of the twenty-five do"
//
// and the list of exceptions named Continue, which resumes with `cn --fork`.
// A contributor then translated one of those sentences into Chinese, faithfully
// (#3778), which is how it surfaced.
//
// The registry is the source: `capabilities.resume` is tied to the code by
// TestCapabilityRegistryMatchesCode, so pinning the prose to it pins it to the
// commands deja actually prints.
type harnessResume struct {
	total   int
	resumes int
	without []string
	names   map[string]bool // display name -> resumes
}

func readHarnessResume(t *testing.T, root string) harnessResume {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID           string `json:"id"`
			DisplayName  string `json:"display_name"`
			Capabilities struct {
				Resume bool `json:"resume"`
			} `json:"capabilities"`
		} `json:"harnesses"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	out := harnessResume{names: map[string]bool{}}
	for _, h := range reg.Harnesses {
		if h.ID == "deja" {
			continue
		}
		out.total++
		name := h.DisplayName
		if name == "" {
			name = h.ID
		}
		out.names[name] = h.Capabilities.Resume
		if h.Capabilities.Resume {
			out.resumes++
			continue
		}
		out.without = append(out.without, name)
	}
	if out.resumes == 0 || len(out.without) == 0 {
		t.Fatal("the registry says every harness resumes, or none does; this test is reading the wrong field")
	}
	sort.Slice(out.without, func(i, j int) bool {
		return strings.ToLower(out.without[i]) < strings.ToLower(out.without[j])
	})
	return out
}

// numberWord reports whether a word is one of the counts these documents can
// be about, so "transcripts of thirty-three agents" is read as a claim about
// the total and not as a subset of something.
func numberWord(w string) bool {
	w = strings.ToLower(w)
	for _, known := range countWords {
		if known == w {
			return true
		}
	}
	return false
}

func TestTheResumeClaimMatchesTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	reg := readHarnessResume(t, root)
	wantTotal := countWord(t, reg.total)
	wantResumes := countWord(t, reg.resumes)

	// Every page, because the claim turned up on one nobody thought of. Both
	// spellings too: "of the thirty-three harnesses" and "of thirty-three
	// harnesses" — the second is how the one on lost-context.html hid.
	claim := regexp.MustCompile(`(?i)([a-z-]+) of (?:the )?([a-z-]+) (?:indexed )?(harnesses|agents)`)

	var pages []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".html", ".txt", ".md":
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 20 {
		t.Fatalf("only %d pages found; the walk is wrong", len(pages))
	}

	// A total with no noun after it: "twenty of the twenty-five do" sat on
	// lost-context.html under the sentence about resuming, and the claim above
	// cannot see it because there is nothing for it to be twenty-five of.
	// Only where the number stands for the harnesses themselves: "of thirty
	// task chains" is a bench corpus and is meant to stay thirty.
	bare := regexp.MustCompile(`(?i)of (?:the )?([a-z-]+)\s*(?:do|does|support|can|indexed|harnesses|agents|[.,;)]|—)`)

	claims := 0
	for _, path := range pages {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		for _, m := range claim.FindAllStringSubmatchIndex(text, -1) {
			whole := text[m[0]:m[1]]
			first, second := text[m[2]:m[3]], text[m[4]:m[5]]
			if !numberWord(second) {
				// "one of the registry entries" and other prose that happens
				// to fit the shape.
				continue
			}
			claims++
			if !strings.EqualFold(second, wantTotal) {
				t.Errorf("%s says %q; the registry has %d harnesses (%s)", name, whole, reg.total, wantTotal)
			}
			if !numberWord(first) {
				continue // a claim about all of them, not a subset
			}
			// A subset claim in a sentence about resuming is the resume count.
			lo := max(m[0]-200, 0)
			hi := min(m[1]+200, len(text))
			if !strings.Contains(strings.ToLower(text[lo:hi]), "resume") {
				continue
			}
			if !strings.EqualFold(first, wantResumes) {
				t.Errorf("%s says %q; %d harnesses resume (%s)", name, whole, reg.resumes, wantResumes)
			}
		}
		for _, m := range bare.FindAllStringSubmatch(text, -1) {
			if !numberWord(m[1]) || strings.EqualFold(m[1], wantTotal) {
				continue
			}
			// CHANGELOG keeps saying what was true at each release; so does a
			// line quoting an older measurement by its own count.
			t.Errorf("%s says %q, and there are %d harnesses (%s)", name, m[0], reg.total, wantTotal)
		}
	}
	if claims == 0 {
		t.Fatal("no page states how many harnesses there are; if that is on purpose, this test goes with it")
	}

	// And the exceptions, which is the half that was wrong rather than merely
	// old: a harness listed as unable to resume when it can sends the reader
	// to another tool for nothing.
	for _, page := range []string{"docs/guide/find-a-session.html", "docs/guide/resume-a-session.html"} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		text := string(raw)
		exceptions := text
		if i := strings.Index(exceptions, "The remaining "); i >= 0 {
			exceptions = exceptions[i:]
		} else if i := strings.Index(exceptions, "The other "); i >= 0 {
			exceptions = exceptions[i:]
		}
		if end := strings.Index(exceptions, "</p>"); end >= 0 {
			exceptions = exceptions[:end]
		}
		for _, missing := range reg.without {
			if !strings.Contains(exceptions, missing) {
				t.Errorf("%s does not name %s, which cannot be resumed", page, missing)
			}
		}
		for display, resumes := range reg.names {
			if !resumes {
				continue
			}
			// Roo Code and Kilo Code are named in those paragraphs on purpose:
			// half of each store resumes and the other half reopens in the
			// editor, and the pages say so.
			if display == "Roo Code" || display == "Kilo Code" {
				continue
			}
			if strings.Contains(exceptions, "— "+display) || strings.Contains(exceptions, ", "+display+" and ") {
				t.Errorf("%s lists %s among the harnesses without a resume path; the registry says it has one", page, display)
			}
		}
	}
}

// `cline --id` reopens the transcript from anywhere but runs its tools in the
// current directory, so the command has to run where the session did (#4318).
func TestResumeClineRunsInTheSessionDirectory(t *testing.T) {
	proj := t.TempDir()
	id := "1790000000000_abcde"
	dir := filepath.Join(t.TempDir(), "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	man, err := json.Marshal(map[string]string{"sessionId": id, "cwd": proj})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, id+".json"), man, 0o644); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, id+".messages.json")
	if err = os.WriteFile(transcript, []byte(`{"messages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, cmd, err := resumeCommand(model.Session{Harness: "cline", ID: id, Path: transcript})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "cline --id "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(got) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the manifest's cwd %q", got, proj)
	}

	// A directory that is gone gets no cd that would fail before cline starts.
	if err = os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	sess := model.Session{Harness: "cline", ID: id, Path: transcript}
	got, cmd, _ = resumeCommand(sess)
	if got != "" || cmd != "cline --id "+id {
		t.Errorf("got (%q, %q) for a directory that is gone", got, cmd)
	}
	// And it says so: the tools will run wherever the command is typed.
	if note := resumeDirGoneNote(sess, got); !strings.Contains(note, proj) {
		t.Errorf("no note naming the gone directory: %q", note)
	}

	// A relative cwd would be resolved against wherever deja runs.
	man, err = json.Marshal(map[string]string{"cwd": "."})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), man, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := resumeCommand(sess); got != "" {
		t.Errorf("dir = %q for a relative cwd", got)
	}
}

// CodeBuddy reopens a session with `codebuddy -r <id>`, found only under the
// folder of the directory it is run from; anywhere else it answers "No
// conversation found with session ID" (2.161.2). The transcript records that
// directory, so resume goes there, and refuses when it is gone (#4707).
//
// With -c as well: CodeBuddy hands SessionStart context to the model on a
// resume only when `continue` is set, and the -r id still picks the session,
// so `-r` alone reopened it without the recall deja had logged (#4718).
func TestResumeCodeBuddyGoesToItsDirectory(t *testing.T) {
	hermeticEnv(t)
	home, _ := os.UserHomeDir()
	work := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "01a10b10-f651-7360-aaab-18f648204bf8"
	path := filepath.Join(home, ".codebuddy", "projects", "app", id+".jsonl")
	line := `{"id":"u1","timestamp":1791187200000,"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}],"sessionId":"` + id + `","cwd":` + jsonString(work) + "}\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sub-agent run is reached through the session that spawned it.
	if _, cmd, err := resumeCommand(model.Session{Harness: "codebuddy", ID: "agent-a1", Kind: "subagent", Parent: id, Path: path}); err == nil || !strings.Contains(err.Error(), "deja resume "+id) {
		t.Fatalf("sub-agent: cmd %q err %v", cmd, err)
	}
	s := model.Session{Harness: "codebuddy", ID: id, Path: path}
	dir, cmd, err := resumeCommand(s)
	if err != nil || dir != work || cmd != "codebuddy -c -r "+id {
		t.Fatalf("got dir %q cmd %q err %v", dir, cmd, err)
	}

	if err := os.RemoveAll(work); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), work) || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a gone directory: %v", err)
	}

	// WorkBuddy writes the same store; deja knows no command that reopens it.
	wb := filepath.Join(home, ".workbuddy", "projects", "app", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(wb), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wb, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, cmd, err := resumeCommand(model.Session{Harness: "codebuddy", ID: id, Path: wb}); err == nil || !strings.Contains(err.Error(), "WorkBuddy") {
		t.Fatalf("WorkBuddy session: cmd %q err %v", cmd, err)
	}
}

// The cd goes to the workspace the session file stores, not to the project
// label derived from it, which is a relative path that only resolves from the
// workspace's parent (#4362).
func TestResumeCodeWhaleRunsInTheWorkspace(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "src", "retry-app")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "eeeeeeee-0000-4000-8000-000000000005"
	doc, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"metadata":       map[string]string{"id": id, "workspace": ws},
		"messages":       []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), id+".json")
	if err = os.WriteFile(path, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	s := model.Session{Harness: "codewhale", ID: id, Path: path, Project: "src/retry-app"}

	dir, cmd, err := resumeCommand(s)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "codewhale --resume "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(ws) {
		t.Errorf("dir = %q, want the session's workspace %q", dir, ws)
	}

	// A workspace that is gone gets no cd that would fail before codewhale starts.
	if err = os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}
	if dir, cmd, _ := resumeCommand(s); dir != "" || cmd != "codewhale --resume "+id {
		t.Errorf("got (%q, %q) for a workspace that is gone", dir, cmd)
	}
}

// VS Code keeps chat history per workspace and Chat: Show Chats lists only the
// open one's, so resume has to say which folder to open (#4223).
func TestResumeCopilotChatNamesTheFolderToOpen(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "my proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "workspaceStorage", "abc")
	if err := os.MkdirAll(filepath.Join(ws, "chatSessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(proj)}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	if err := os.WriteFile(filepath.Join(ws, "workspace.json"), []byte(`{"folder":"`+u.String()+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := model.Session{Harness: "copilot-chat", ID: "11111111-2222-3333-4444-555555555555", Path: filepath.Join(ws, "chatSessions", "11111111-2222-3333-4444-555555555555.json")}
	_, _, err := resumeCommand(s)
	if err == nil || !strings.Contains(err.Error(), shellQuoteIfNeeded(proj)) {
		t.Fatalf("resume does not name the folder %s: %v", proj, err)
	}

	// The newer extension writes GitHub.copilot-chat/transcripts/<id>.jsonl
	// under the same storage hash.
	s.Path = filepath.Join(ws, "GitHub.copilot-chat", "transcripts", "11111111-2222-3333-4444-555555555555.jsonl")
	if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), shellQuoteIfNeeded(proj)) {
		t.Fatalf("a transcript-format chat does not name the folder %s: %v", proj, err)
	}

	// An empty-window chat has no folder: the plain line stays.
	s.Path = filepath.Join(root, "globalStorage", "emptyWindowChatSessions", "x.json")
	if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), "Show Chats") || strings.Contains(err.Error(), "code ") {
		t.Fatalf("an empty-window chat got %v", err)
	}
}

// Three pages say how many harnesses `deja resume` can hand a session back to,
// and one of them names the ones it cannot. Both halves come out of
// registry.json, which a test already checks against the code — but the prose
// was typed, so a harness landing with no resume path of its own would leave
// three pages claiming one more than there is.
func TestTheResumeCountFollowsTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Harnesses []struct {
			ID           string `json:"id"`
			DisplayName  string `json:"display_name"`
			Capabilities struct {
				Resume bool `json:"resume"`
			} `json:"capabilities"`
		} `json:"harnesses"`
	}
	if err = json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}

	var without []string
	resumable := 0
	for _, h := range reg.Harnesses {
		if h.Capabilities.Resume {
			resumable++
			continue
		}
		without = append(without, h.DisplayName)
	}
	if resumable == 0 || len(without) == 0 {
		t.Fatal("registry.json has no resume capabilities — this checks nothing")
	}
	total := countWord(t, len(reg.Harnesses))
	yes := countWord(t, resumable)

	// "twenty-seven of the thirty-four harnesses", in either voice and
	// wherever it sits — body text or the FAQ block in the page head.
	claim := regexp.MustCompile(`(?i)([a-z-]+) of the ([a-z-]+) (?:indexed )?harnesses`)
	pages := []string{
		"docs/guide/find-a-session.html",
		"docs/guide/resume-a-session.html",
		"docs/guide/lost-context.html",
	}
	for _, page := range pages {
		raw, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		text := proseOf(string(raw))
		found := claim.FindAllStringSubmatchIndex(text, -1)
		counted := 0
		for _, loc := range found {
			// Only the claims that are about resuming: a page is entitled to
			// say "three of the thirty-four harnesses" about something else,
			// and that is not this number. The word can be in the sentence
			// before — the FAQ answer names `deja resume` and then counts the
			// harnesses that "support it" — so the lookback is a paragraph's
			// worth rather than one sentence.
			from := max(loc[0]-250, 0)
			to := min(
				// "…can also be reopened with deja resume."
				loc[1]+120, len(text))
			if !strings.Contains(strings.ToLower(text[from:to]), "resume") {
				continue
			}
			counted++
			m := []string{
				text[loc[0]:loc[1]],
				text[loc[2]:loc[3]],
				text[loc[4]:loc[5]],
			}
			if got := strings.ToLower(m[1]); got != yes {
				t.Errorf("%s says %q of the harnesses resume; the registry has %d, so it is %q",
					page, got, resumable, yes)
			}
			if got := strings.ToLower(m[2]); got != total {
				t.Errorf("%s counts %q harnesses; the registry has %d, so it is %q",
					page, got, len(reg.Harnesses), total)
			}
		}
		if counted == 0 {
			t.Errorf("%s no longer says how many harnesses resume — the phrasing changed and this stopped checking it", page)
		}
	}

	// And the page that names the ones without a resume path names all of them
	// and nothing else.
	page := "docs/guide/find-a-session.html"
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page)))
	if err != nil {
		t.Fatal(err)
	}
	rest := regexp.MustCompile(`remaining ([a-z-]+) — ([^—]+) — have no resume path`)
	m := rest.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("%s no longer lists the harnesses without a resume path", page)
	}
	if m[1] != spelledCount(len(without)) {
		t.Errorf("%s says %q have no resume path; the registry says %d, so it is %q",
			page, m[1], len(without), spelledCount(len(without)))
	}
	named := strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' })
	var listed []string
	for _, n := range named {
		for part := range strings.SplitSeq(n, " and ") {
			if p := strings.TrimSpace(part); p != "" {
				listed = append(listed, p)
			}
		}
	}
	want := map[string]bool{}
	for _, n := range without {
		want[n] = true
	}
	for _, n := range listed {
		if !want[n] {
			t.Errorf("%s names %q as having no resume path; the registry does not", page, n)
			continue
		}
		delete(want, n)
	}
	for n := range want {
		t.Errorf("%s does not name %s, which has no resume path", page, n)
	}
}

// seedCursorChat lays out a CLI chat the way cursor-agent does: the transcript
// under projects/<folder with every non-alphanumeric blanked>, and meta.json
// under chats/<md5 of the cwd>/<id>.
func seedCursorChat(t *testing.T, cli, cwd, id string) string {
	t.Helper()
	folder := strings.TrimLeft(regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-"), "-")
	tp := filepath.Join(cli, "projects", folder, "agent-transcripts", id, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(tp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"hello there"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte(cwd)) //nolint:gosec // Cursor names its chat folder by this hash
	chat := filepath.Join(cli, "chats", hex.EncodeToString(sum[:]), id)
	if err := os.MkdirAll(chat, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chat, "meta.json"), []byte(`{"schemaVersion":1,"cwd":`+jsonString(cwd)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chat, "store.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return tp
}

// The folder name cannot be read back for a directory with a dot, a space or
// a non-ASCII name, and cursor-agent finds a chat only from the directory it
// ran in, so resume has to take that directory from the chat's meta.json
// (#4193).
func TestResumeCursorTakesTheDirectoryFromTheChat(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("CURSOR_CONFIG_DIR", cli)
	for i, name := range []string{"my.app", "проект", "two words"} {
		cwd := filepath.Join(tmp, "work", name)
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e8" + string(rune('0'+i))
		tp := seedCursorChat(t, cli, cwd, id)

		dir, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if dir != cwd || cmd != "cursor-agent --resume "+id {
			t.Errorf("%s: dir=%q cmd=%q, want %q", name, dir, cmd, cwd)
		}
		ss, err := sources.ParseCursorTranscript(tp)
		if err != nil || len(ss) != 1 || ss[0].Project != "work/"+name {
			t.Errorf("%s: project = %v %v", name, ss, err)
		}
	}

	// The directory is gone: there is no command that reopens the chat, and
	// printing one sends someone into an empty session.
	cwd := filepath.Join(tmp, "work", "gone.app")
	id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e99"
	tp := seedCursorChat(t, cli, cwd, id)
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a chat whose directory is gone: err = %v", err)
	}

	// The chat is no longer in cursor-agent's store although the directory is
	// there: the command would open an empty chat.
	cwd = filepath.Join(tmp, "work", "kept.app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	id = "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e98"
	tp = seedCursorChat(t, cli, cwd, id)
	if err := os.Remove(filepath.Join(cli, "chats", sources.CursorChatBucket(cwd), id, "store.db")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a chat gone from the store: err = %v", err)
	}
}

// A meta.json under a folder that is not the md5 of its cwd is a copy: the
// project can come from it, the cd cannot, because cursor-agent would look in
// a different folder.
func TestResumeCursorTrustsOnlyAChatFiledUnderItsDirectory(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("CURSOR_CONFIG_DIR", cli)
	cwd := filepath.Join(tmp, "work", "my.app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "5b0c1a7e-2f4d-4c1b-9a3e-7d2f1c0b9e97"
	tp := seedCursorChat(t, cli, cwd, id)
	right := filepath.Join(cli, "chats", sources.CursorChatBucket(cwd))
	if err := os.Rename(right, filepath.Join(cli, "chats", "0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	if got, verified := sources.CursorChatCWD(tp); got != cwd || verified {
		t.Fatalf("CursorChatCWD = %q %v, want %q unverified", got, verified, cwd)
	}
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Path: tp}); err == nil {
		t.Fatal("resume printed a cd into a directory cursor-agent would not find the chat under")
	}
}

// deja keeps a session after the agent deletes it, and resume printed a
// command the agent then refused: codex answered "No saved session found"
// (#4185). An archived rollout is still codex's and still resumes.
func TestResumeRefusesASessionTheAgentDeleted(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	codexRoot := filepath.Join(tmp, "codex")
	day := filepath.Join(codexRoot, "sessions", "2026", "10", "01")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	rollout := func(id, text string) string {
		p := filepath.Join(day, "rollout-2026-10-01T12-58-16-"+id+".jsonl")
		body := `{"timestamp":"2026-10-01T09:58:16Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-10-01T09:58:16Z","cwd":"/w","originator":"codex_exec","cli_version":"0.149.0","source":"exec"}}` + "\n" +
			`{"timestamp":"2026-10-01T09:58:17Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	deleted := "01a0f6e6-8cd3-72c2-84ff-367e6191e458"
	archived := "01a0f6e6-7f79-7703-9654-e9ac45b389fc"
	delPath := rollout(deleted, "pgbouncer prepared statements")
	arcPath := rollout(archived, "redis cluster ttl")

	var out bytes.Buffer
	if err := runResume(index.DefaultDir(), []string{deleted}, &out); err != nil || !strings.Contains(out.String(), "codex resume "+deleted) {
		t.Fatalf("before the delete: %q %v", out.String(), err)
	}

	if err := os.Remove(delPath); err != nil {
		t.Fatal(err)
	}
	arcDir := filepath.Join(codexRoot, "archived_sessions")
	if err := os.MkdirAll(arcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(arcPath, filepath.Join(arcDir, filepath.Base(arcPath))); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	err := runResume(index.DefaultDir(), []string{deleted}, &out)
	if err == nil || !strings.Contains(err.Error(), "deja show") || out.Len() != 0 {
		t.Errorf("a deleted session: out %q, err %v; want a refusal that points at deja show", out.String(), err)
	}
	out.Reset()
	if err := runResume(index.DefaultDir(), []string{archived}, &out); err != nil || !strings.Contains(out.String(), "codex resume "+archived) {
		t.Errorf("an archived session: out %q, err %v; want the resume command", out.String(), err)
	}
}

// opencode keeps a deleted session out of its database while deja keeps it
// searchable; the printed `opencode -s` then failed with "Session not found"
// (#4205).
func TestResumeRefusesAnOpencodeSessionDeletedInOpencode(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	script := `create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_kept','/work/app','2026-01-02T03:00:00Z','2026-01-02T03:00:00Z');
insert into message values('m1','ses_kept',1,'{"role":"user"}');
insert into part values('p1','m1','{"type":"text","text":"hello"}');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)

	gone := model.Session{Harness: "opencode", ID: "ses_deleted", Path: "/work/app"}
	if err := resumeGoneError(gone); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a session opencode no longer has was offered for resume: %v", err)
	}
	kept := model.Session{Harness: "opencode", ID: "ses_kept", Path: "/work/app"}
	if err := resumeGoneError(kept); err != nil {
		t.Fatalf("a session still in opencode was refused: %v", err)
	}
	// No store to ask: nothing is refused on a guess.
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "missing.db"))
	if err := resumeGoneError(gone); err != nil {
		t.Fatalf("refused with no store to ask: %v", err)
	}
}

// A session goose deleted stays searchable in deja, and the printed
// `goose session --resume --session-id <id>` then failed with "Cannot resume
// session … no such session exists" (#4271) — the opencode case of #4205.
func TestResumeRefusesAGooseSessionDeletedInGoose(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "sessions.db")
	script := `create table sessions(id text primary key, name text, working_dir text not null);
create table messages(id integer primary key, session_id text, role text, content_json text);
insert into sessions values('20261001_1','hunt','/tmp/proj');
insert into messages values(1,'20261001_1','user','[{"type":"text","text":"hello"}]');`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	gone := model.Session{Harness: "goose", ID: "20261001_2", Path: db}
	if err := resumeGoneError(gone); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a session goose no longer has was offered for resume: %v", err)
	}
	kept := model.Session{Harness: "goose", ID: "20261001_1", Path: db}
	if err := resumeGoneError(kept); err != nil {
		t.Fatalf("a session still in goose was refused: %v", err)
	}
	// No store to ask: nothing is refused on a guess.
	missing := model.Session{Harness: "goose", ID: "20261001_2", Path: filepath.Join(tmp, "missing.db")}
	if err := resumeGoneError(missing); err != nil {
		t.Fatalf("refused with no store to ask: %v", err)
	}
}

// A profile keeps its sessions in its own store, and `hermes --resume <id>`
// looks only in the active profile's: a session recorded under `hermes -p work`
// came back "Session not found" (#4248). A sticky `hermes profile use work` turns
// it around, and the root store then needs `-p default`.
func TestResumeHermesNamesTheProfileTheSessionIsIn(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "hermes")
	t.Setenv("DEJA_HERMES_HOME", home)
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) string {
		t.Helper()
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		if err != nil {
			t.Fatalf("resume %s: %v", path, err)
		}
		return cmd
	}
	profile := filepath.Join(home, "profiles", "work", "state.db")
	root := filepath.Join(home, "state.db")
	if got, want := cmdFor(profile), "hermes -p work --resume "+id; got != want {
		t.Errorf("profile session: %q, want %q", got, want)
	}
	if got, want := cmdFor(root), "hermes --resume "+id; got != want {
		t.Errorf("root session: %q, want %q", got, want)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "active_profile"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := cmdFor(root), "hermes -p default --resume "+id; got != want {
		t.Errorf("root session with work active: %q, want %q", got, want)
	}
	if got, want := cmdFor(profile), "hermes -p work --resume "+id; got != want {
		t.Errorf("profile session with work active: %q, want %q", got, want)
	}
}

// Run as a child of `hermes -p work`, deja inherits HERMES_HOME pointed at the
// profile's own directory, and read that as the root: the root store's sessions
// and the other profiles' came out with bare commands that open work's. Hermes
// takes the root to be two levels up from a directory under `profiles`, and in
// that mode the command always names the profile. A Postgres session keeps the
// command it had; a directory Hermes would not take as a profile name is
// refused rather than handed to -p.
func TestResumeHermesUnderAProfilesHome(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(root, "profiles", "work"))
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) (string, error) {
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		return cmd, err
	}
	for path, want := range map[string]string{
		filepath.Join(root, "state.db"):                      "hermes -p default --resume " + id,
		filepath.Join(root, "profiles", "work", "state.db"):  "hermes -p work --resume " + id,
		filepath.Join(root, "profiles", "other", "state.db"): "hermes -p other --resume " + id,
	} {
		if got, err := cmdFor(path); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", path, got, err, want)
		}
	}

	t.Setenv("DEJA_HERMES_HOME", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active_profile"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := cmdFor("hermes-pg:abc123"); err != nil || got != "hermes --resume "+id {
		t.Errorf("postgres session: %q, %v; want the plain command", got, err)
	}
	if got, err := cmdFor(filepath.Join(root, "profiles", "Work.Old", "state.db")); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Errorf("a directory Hermes takes for no profile: %q, %v; want a refusal", got, err)
	}
}

// `hermes sessions delete` takes the row out of state.db while deja keeps the
// session searchable, and the printed `hermes --resume` then failed with
// "Session not found" (#4250) — the opencode case of #4205.
func TestResumeRefusesAHermesSessionDeletedInHermes(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "state.db")
	script := `create table sessions(id text primary key, cwd text);
create table messages(id integer primary key, session_id text, role text, content text, timestamp real);
insert into sessions values('20261001_181726_aaaaaa','/tmp/proj');
insert into messages values(1,'20261001_181726_aaaaaa','user','hello',1);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, script).CombinedOutput(); err != nil {
		t.Fatalf("seed: %v %s", err, out)
	}
	gone := model.Session{Harness: "hermes", ID: "20261001_181726_5aedef", Path: db}
	if err := resumeGoneError(gone); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("a session hermes no longer has was offered for resume: %v", err)
	}
	kept := model.Session{Harness: "hermes", ID: "20261001_181726_aaaaaa", Path: db}
	if err := resumeGoneError(kept); err != nil {
		t.Fatalf("a session still in hermes was refused: %v", err)
	}
	// No store to ask: nothing is refused on a guess.
	missing := model.Session{Harness: "hermes", ID: "20261001_181726_5aedef", Path: filepath.Join(tmp, "missing.db")}
	if err := resumeGoneError(missing); err != nil {
		t.Fatalf("refused with no store to ask: %v", err)
	}
}

// The profile home Hermes exports may come with a trailing slash, through a
// symlink, or relative to where deja runs; each read as the root before, and a
// store outside the root fell through to `-p default`. Only the root's own
// store is `default`, and what is in neither place keeps the plain command.
func TestResumeHermesProfileHomeAsHermesWritesIt(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(filepath.Join(root, "profiles", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skip("no symlinks here")
	}
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) string {
		t.Helper()
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		if err != nil {
			t.Fatalf("resume %s: %v", path, err)
		}
		return cmd
	}
	rootDB := filepath.Join(root, "state.db")
	workDB := filepath.Join(root, "profiles", "work", "state.db")
	for _, home := range []string{
		filepath.Join(root, "profiles", "work") + string(filepath.Separator),
		filepath.Join(link, "profiles", "work"),
	} {
		t.Setenv("DEJA_HERMES_HOME", home)
		if got, want := cmdFor(rootDB), "hermes -p default --resume "+id; got != want {
			t.Errorf("HERMES_HOME=%s, root store: %q, want %q", home, got, want)
		}
		if got, want := cmdFor(workDB), "hermes -p work --resume "+id; got != want {
			t.Errorf("HERMES_HOME=%s, work store: %q, want %q", home, got, want)
		}
	}
	t.Setenv("DEJA_HERMES_HOME", filepath.Join(root, "profiles", "work"))
	if got, want := cmdFor(filepath.Join(tmp, "elsewhere", "state.db")), "hermes --resume "+id; got != want {
		t.Errorf("a store outside the root: %q, want %q", got, want)
	}
}

// Hermes takes no profile by a reserved name, and `-p default` is the root:
// a directory with one of those names under profiles/ is refused.
func TestResumeHermesRefusesAReservedProfileDirectory(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	t.Setenv("DEJA_HERMES_HOME", root)
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	for _, name := range []string{"default", "hermes", "test", "tmp", "root", "sudo"} {
		path := filepath.Join(root, "profiles", name, "state.db")
		_, cmd, err := resumeCommand(model.Session{ID: "20261001_182638_264fd2", Harness: "hermes", Project: "p", Path: path})
		if err == nil || !strings.Contains(err.Error(), "deja show") {
			t.Errorf("profiles/%s: %q, %v; want a refusal", name, cmd, err)
		}
	}
}

// A profile directory may itself be a symlink — profiles/big moved to a bigger
// disk. Hermes decides the profile on the path as written, so resolving it
// first took big's sessions for strays and printed the plain command, and with
// HERMES_HOME on big the root and the other profiles lost their flag too.
func TestResumeHermesSymlinkedProfile(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "hermes")
	if err := os.MkdirAll(filepath.Join(root, "profiles", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "data", "big"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "data", "big"), filepath.Join(root, "profiles", "big")); err != nil {
		t.Skip("no symlinks here")
	}
	t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
	const id = "20261001_182638_264fd2"
	cmdFor := func(path string) string {
		t.Helper()
		_, cmd, err := resumeCommand(model.Session{ID: id, Harness: "hermes", Project: "p", Path: path})
		if err != nil {
			t.Fatalf("resume %s: %v", path, err)
		}
		return cmd
	}
	bigDB := filepath.Join(root, "profiles", "big", "state.db")
	for _, home := range []string{root, filepath.Join(root, "profiles", "big")} {
		t.Setenv("DEJA_HERMES_HOME", home)
		for path, want := range map[string]string{
			bigDB: "hermes -p big --resume " + id,
			filepath.Join(root, "profiles", "work", "state.db"): "hermes -p work --resume " + id,
		} {
			if got := cmdFor(path); got != want {
				t.Errorf("HERMES_HOME=%s, %s: %q, want %q", home, path, got, want)
			}
		}
	}
	if got, want := cmdFor(filepath.Join(root, "state.db")), "hermes -p default --resume "+id; got != want {
		t.Errorf("HERMES_HOME on big, root store: %q, want %q", got, want)
	}
}

// A Kilo session from the CLI database carries the directory it ran in, never
// the database file — so the rule that told the two halves apart by comparing
// the path with the database refused every CLI session, which is the only half
// `kilo -s` is for (#3677).
func TestKiloResumeTellsTheCLIFromTheExtension(t *testing.T) {
	work := filepath.Join(hermeticEnv(t), "work", "api")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	cli := model.Session{Harness: "kilocode", ID: "ses_abc123", Path: work}
	dir, cmd, err := resumeCommand(cli)
	if err != nil {
		t.Fatalf("a CLI session was refused: %v", err)
	}
	if cmd != "kilo -s ses_abc123" {
		t.Errorf("command = %q", cmd)
	}
	if dir != work {
		t.Errorf("dir = %q, want the session's own directory", dir)
	}

	// An extension task is the half with no terminal command.
	roots := sources.KiloRoots()
	if len(roots) == 0 {
		t.Skip("no Kilo globalStorage root on this platform to build a task path from")
	}
	task := model.Session{
		Harness: "kilocode",
		ID:      "task-1",
		Path:    filepath.Join(roots[0], "tasks", "task-1", "api_conversation_history.json"),
	}
	if _, _, err := resumeCommand(task); err == nil {
		t.Error("an editor task was given a CLI command")
	}
}

// A `kiro-cli chat --no-interactive` run lives in data.sqlite3, keyed by the
// directory it ran in. Resume took the directory from the JSONL header beside
// a transcript, which a database row has none of, so the command ran wherever
// the reader stood and kiro-cli rewrote the session's cwd to it (#4305, #4300).
func TestResumeKiroDBSessionRunsInItsDirectory(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj's app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(tmp, "data.sqlite3")
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	seed := "CREATE TABLE conversations_v2 (key TEXT NOT NULL, conversation_id TEXT NOT NULL, value TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (key, conversation_id));\n" +
		"INSERT INTO conversations_v2 VALUES (" + q(proj) + ",'bf73e1f5-0000-4000-8000-000000000002','{}',1,1);"
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(seed)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	t.Setenv("DEJA_KIRO_DB", db)

	dir, line, err := resumeCommand(model.Session{Harness: "kiro", ID: "bf73e1f5-0000-4000-8000-000000000002", Path: db})
	if err != nil {
		t.Fatal(err)
	}
	if dir != proj || line != "kiro-cli chat --resume-id bf73e1f5-0000-4000-8000-000000000002" {
		t.Errorf("got (%q, %q), want the row's directory %q", dir, line, proj)
	}

	// An id that is not in the table, or one carrying a quote, finds nothing.
	for _, id := range []string{"0000", "x' OR '1'='1"} {
		if dir, _, _ := resumeCommand(model.Session{Harness: "kiro", ID: id, Path: db}); dir != "" {
			t.Errorf("id %q resumed in %q", id, dir)
		}
	}
}

func writeKiroFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// kiro-cli finds a session by id from anywhere, then runs it in the current
// directory and rewrites the header's cwd to it, so the command has to run
// where the session did (#4305).
func TestResumeKiroCLIRunsInTheSessionDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("DEJA_KIRO_ROOT", root)
	proj := t.TempDir()
	id := "11111111-2222-4333-8444-555555555555"
	transcript := filepath.Join(root, "cli", id+".jsonl")
	writeKiroFile(t, filepath.Join(root, "cli", id+".json"), `{"session_id":"`+id+`","cwd":"`+filepath.ToSlash(proj)+`"}`)
	writeKiroFile(t, transcript, `{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"fix the retry loop"}]}}`+"\n")

	dir, cmd, err := resumeCommand(model.Session{Harness: "kiro", ID: id, Path: transcript})
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "kiro-cli chat --resume-id "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the header's cwd %q", dir, proj)
	}

	// A directory that is gone gets no cd that would fail before kiro-cli starts.
	if err := os.RemoveAll(proj); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(model.Session{Harness: "kiro", ID: id, Path: transcript}); dir != "" {
		t.Errorf("dir = %q for a directory that is gone", dir)
	}
}

// `kiro-cli --v3` writes the sess_ layout deja took for the IDE's, and lists
// the session in ~/.kiro/session-index; `kiro-cli --v3 chat --resume-id
// sess_…` continues it (#4307). One with no index entry is still the IDE's.
func TestResumeKiroV3Session(t *testing.T) {
	kiro := t.TempDir()
	root := filepath.Join(kiro, "sessions")
	t.Setenv("DEJA_KIRO_ROOT", root)
	proj := t.TempDir()
	id := "sess_2c040944-f344-45a1-bdfc-715e6f631292"
	transcript := filepath.Join(root, "f045c81011ce49e2", id, "messages.jsonl")
	writeKiroFile(t, filepath.Join(filepath.Dir(transcript), "session.json"), `{"id":"`+id+`","workspacePaths":["`+filepath.ToSlash(proj)+`"]}`)
	writeKiroFile(t, transcript, `{"timestamp":"2026-10-01T17:57:01Z","payload":{"type":"user","content":"fix the retry loop"}}`+"\n")

	s := model.Session{Harness: "kiro", ID: id, Path: transcript}
	if _, _, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), "IDE") {
		t.Fatalf("a session kiro-cli does not list: err = %v, want the IDE refusal", err)
	}

	writeKiroFile(t, filepath.Join(kiro, "session-index", "f045c81011ce49e2.jsonl"),
		`{"op":"add","sessionPath":"f045c81011ce49e2/`+id+`","at":1790877277801}`+"\n")
	dir, cmd, err := resumeCommand(s)
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "kiro-cli --v3 chat --resume-id "+id {
		t.Errorf("cmd = %q", cmd)
	}
	if filepath.Clean(dir) != filepath.Clean(proj) {
		t.Errorf("dir = %q, want the workspace %q", dir, proj)
	}
}

// The directory a resume line cds into is read off a session store, so it
// can hold anything a file name can. PowerShell, which the Windows line is
// pasted into, expands $(...) and backtick escapes inside the outer
// double-quoted -Command, ends it on U+201C/U+201D/U+201E as on ", and ends
// the inner single-quoted path on U+2018/U+2019/U+201A/U+201B as on '.
func TestWindowsResumeLineKeepsTheDirOneLiteral(t *testing.T) {
	const cmd = "claude --resume 019f"
	wrap := func(quoted string) string {
		return `powershell.exe -NoProfile -Command "Set-Location -LiteralPath ` + quoted + ` -ErrorAction Stop; ` + cmd + `"`
	}
	for _, c := range []struct {
		dir, want string
	}{
		{`C:\Users\bob's\app`, wrap(`'C:\Users\bob''s\app'`)},
		{"C:\\p\\a\u2018b", wrap("'C:\\p\\a\u2018\u2018b'")},
		{"C:\\p\\a\u2019b", wrap("'C:\\p\\a\u2019\u2019b'")},
		{"C:\\p\\a\u201ab", wrap("'C:\\p\\a\u201a\u201ab'")},
		{"C:\\p\\a\u201bb", wrap("'C:\\p\\a\u201b\u201bb'")},
		{`C:\p\50% a,b @x`, wrap(`'C:\p\50% a,b @x'`)},
	} {
		got, ok := resumeLine("windows", c.dir, cmd)
		if !ok || got != c.want {
			t.Errorf("resumeLine(windows, %q) = %q, %v\n want %q", c.dir, got, ok, c.want)
		}
	}
	for _, dir := range []string{
		`C:\p\a$(echo hi)b`,
		"C:\\p\\a`b",
		`C:\p\a"b`,
		"C:\\p\\a\u201cb",
		"C:\\p\\a\u201db",
		"C:\\p\\a\u201eb",
		"C:\\p\\a\x1b[2Jb",
		"C:\\p\\a\nb",
		// cmd.exe expands %name% even inside its double quotes, and !name!
		// under delayed expansion, before PowerShell sees the line: a user
		// named O'Brien turned %USERNAME% into a quote that ended the path.
		`C:\p\%USERNAME%;calc;#`,
		`C:\p\!USERNAME!;calc;#`,
	} {
		if got, ok := resumeLine("windows", dir, cmd); ok || got != cmd {
			t.Errorf("resumeLine(windows, %q) = %q, %v; want the command alone, no cd", dir, got, ok)
		}
	}
}

// fish reads \' and \\ inside single quotes as escapes where every other
// shell keeps them, so a single-quoted path with a backslash is a different
// word there: `a\'\';echo hi;#` ran `echo hi`.
func TestPosixResumeLineLeavesOutADirFishReadsDifferently(t *testing.T) {
	const cmd = "claude --resume 019f"
	got, ok := resumeLine("linux", "/tmp/bob's dir", cmd)
	if want := `cd '/tmp/bob'"'"'s dir' && ` + cmd; !ok || got != want {
		t.Errorf("resumeLine(linux, bob's dir) = %q, %v; want %q", got, ok, want)
	}
	for _, dir := range []string{`/tmp/a\'\';echo hi;#`, `/tmp/x\`, `/tmp/a\\b`, "/tmp/a\x1b[2Jb"} {
		if got, ok := resumeLine("linux", dir, cmd); ok || got != cmd {
			t.Errorf("resumeLine(linux, %q) = %q, %v; want the command alone, no cd", dir, got, ok)
		}
	}
}

// Whatever line is printed lands in the directory itself, in every shell on
// this machine that the line is meant for.
func TestResumeLineLandsInTheDirInEachShell(t *testing.T) {
	names := []string{"bob's dir", "a$(echo hi)b", "a`echo hi`b", "a;echo hi;b", "a\u2018b\u2019c", "50% a,b @x"}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	var dirs []string
	for _, n := range names {
		d := filepath.Join(base, n)
		if err := os.Mkdir(d, 0o755); err != nil {
			continue // a name this file system refuses
		}
		dirs = append(dirs, d)
	}
	run := func(t *testing.T, goos, cmdline string, shell ...string) {
		t.Helper()
		for _, d := range dirs {
			line, ok := resumeLine(goos, d, cmdline)
			if !ok {
				continue
			}
			script, body := filepath.Join(t.TempDir(), "line"), line+"\n"
			if goos == "windows" {
				// Windows PowerShell reads a .ps1 without a BOM as ANSI, which
				// a line pasted at its prompt never goes through.
				script, body = script+".ps1", "\ufeff"+body
			}
			if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.CommandContext(t.Context(), shell[0], append(shell[1:], script)...).CombinedOutput()
			got := strings.TrimSpace(string(out))
			if err != nil || got != d {
				t.Errorf("%s: %s\n printed %q, want %q (%v)", shell[0], line, got, d, err)
			}
		}
	}
	for _, sh := range []string{"sh", "bash", "zsh", "dash", "fish"} {
		if _, err := exec.LookPath(sh); err != nil || runtime.GOOS == "windows" {
			continue
		}
		t.Run(sh, func(t *testing.T) { run(t, "linux", "pwd", sh) })
	}
	// The Windows line runs powershell.exe inside whichever PowerShell it is
	// pasted into.
	if _, err := exec.LookPath("powershell.exe"); err == nil {
		for _, ps := range []string{"pwsh", "powershell.exe"} {
			if _, err := exec.LookPath(ps); err != nil {
				continue
			}
			t.Run(ps, func(t *testing.T) {
				run(t, "windows", "(Get-Location).ProviderPath", ps, "-NoProfile", "-NonInteractive", "-File")
			})
		}
	}
}

// Three commands taken from the tools' own sources, and the one case that has
// to refuse: a Kiro session belonging to the IDE, which reopens from the app
// and would otherwise get a CLI command that does nothing (#3651).
func TestResumeCommandsForTheNewHarnesses(t *testing.T) {
	for _, tc := range []struct {
		harness string
		id      string
		want    string
	}{
		{"kiro", "f2946a26-3735-4b08-8d05-c928010302d5", "kiro-cli chat --resume-id f2946a26-3735-4b08-8d05-c928010302d5"},
		{"kimchi", "reg-kimchi-001", "kimchi --session reg-kimchi-001"},
		{"gjc", "reg-gjc-001", "gjc --resume reg-gjc-001"},
	} {
		_, cmd, err := resumeCommand(model.Session{Harness: tc.harness, ID: tc.id})
		if err != nil {
			t.Errorf("%s: %v", tc.harness, err)
			continue
		}
		if cmd != tc.want {
			t.Errorf("%s = %q, want %q", tc.harness, cmd, tc.want)
		}
	}

	// Continue's flag forks rather than continues, so the command is offered
	// with the caveat beside it. Silence there would promise the wrong thing.
	if _, cmd, err := resumeCommand(model.Session{Harness: "continue", ID: "abc123"}); err != nil {
		t.Errorf("continue: %v", err)
	} else if cmd != "cn --fork abc123" {
		t.Errorf("continue = %q", cmd)
	}
	if note := resumeCaveats["continue"]; !strings.Contains(note, "fork") {
		t.Errorf("continue's caveat does not say it forks: %q", note)
	}

	_, _, err := resumeCommand(model.Session{Harness: "kiro", ID: "sess_00000000-0000-4000-8000-000000000000"})
	if err == nil {
		t.Fatal("an IDE session got a CLI command that would not find it")
	}
	if !strings.Contains(err.Error(), "IDE") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// gjc and Kimchi reopen a session only from the directory it ran in: from
// anywhere else gjc refuses ("is in another project") and Kimchi offers to fork
// it. The header line records that directory, and resume printed no cd
// (#4395, #4400).
func TestResumeGjcAndKimchiRunInTheSessionDirectory(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := func(name, cwd string) string {
		t.Helper()
		p := filepath.Join(tmp, name+".jsonl")
		header := `{"type":"session","version":3,"id":"01a0f93c-7bdc-7074-a7dd-641dd530ce0c","timestamp":"2026-10-01T21:00:00.000Z"`
		if cwd != "" {
			// Encoded, as the clients write it: a Windows path spliced in
			// raw is `C:\Users\…`, an invalid JSON escape, and the header
			// did not decode at all.
			enc, err := json.Marshal(cwd)
			if err != nil {
				t.Fatal(err)
			}
			header += `,"cwd":` + string(enc)
		}
		body := header + "}\n" + `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct{ harness, cmd string }{
		{"gjc", "gjc --resume 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
		{"kimchi", "kimchi --session 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
		// Senpi does what Kimchi does: from another directory it asks to fork
		// the session into it (#4426).
		{"senpi", "senpi --session 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
		// prime-agent refuses a session from another project and points at
		// --fork (#4408), so a gone directory needs the same line.
		{"prime", "prime-agent --resume 01a0f93c-7bdc-7074-a7dd-641dd530ce0c"},
	} {
		t.Run(c.harness, func(t *testing.T) {
			s := model.Session{Harness: c.harness, ID: "01a0f93c-7bdc-7074-a7dd-641dd530ce0c", Path: transcript(c.harness, proj)}
			dir, cmd, err := resumeCommand(s)
			if err != nil {
				t.Fatal(err)
			}
			if dir != proj || cmd != c.cmd {
				t.Fatalf("got (%q, %q), want (%q, %q)", dir, cmd, proj, c.cmd)
			}
			if note := resumeDirGoneNote(s, dir); note != "" {
				t.Errorf("a note about a directory that is there: %q", note)
			}

			// The directory is gone: no cd that stops the command, and a line
			// saying what the client will do instead.
			gone := filepath.Join(tmp, "gone")
			s.Path = transcript(c.harness+"-gone", gone)
			dir, _, err = resumeCommand(s)
			if err != nil || dir != "" {
				t.Fatalf("dir = %q, err = %v; want no cd into a directory that is gone", dir, err)
			}
			if note := resumeDirGoneNote(s, dir); !strings.Contains(note, gone) || !strings.Contains(note, "fork") {
				t.Errorf("note = %q, want it to name %s and the fork", note, gone)
			}

			// A header with no cwd: nothing to cd into, nothing to say.
			s.Path = transcript(c.harness+"-nocwd", "")
			if dir, _, _ := resumeCommand(s); dir != "" {
				t.Errorf("dir = %q from a header with no cwd", dir)
			}
			if note := resumeDirGoneNote(s, ""); note != "" {
				t.Errorf("note = %q from a header with no cwd", note)
			}
		})
	}
}

// resume takes an exact id, but naming one is still browsing under the search
// activation: show, share, promote and handoff all refuse a session a trust
// rule withholds, and resume must too — otherwise the command that reopens a
// hidden session (and, with --exec, the session itself) leaks past the rule.
func TestResumeObeysTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work to reopen"},"timestamp":"2026-07-01T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	// No rule: resume prints the reopening command, as it should.
	var open bytes.Buffer
	if err := runResume(dir, []string{"loc"}, &open); err != nil {
		t.Fatalf("plain resume failed: %v", err)
	}
	if !strings.Contains(open.String(), "claude --resume loc") {
		t.Fatalf("plain resume did not print the command: %q", open.String())
	}

	// A rule that withholds local sessions must stop resume from revealing one.
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	var out bytes.Buffer
	err := runResume(dir, []string{"loc"}, &out)
	if err == nil {
		t.Fatalf("resume leaked a policy-hidden session: %q", out.String())
	}
	if strings.Contains(out.String(), "claude --resume") {
		t.Errorf("resume printed the reopen command for a hidden session: %q", out.String())
	}
	if !strings.Contains(err.Error(), "no session matches") {
		t.Errorf("hidden resume error = %v, want the same not-found form the siblings use", err)
	}
}

// qwenTranscriptIn writes a one-record qwen transcript filed under the folder
// qwen names for cwd, recording cwd when record is set.
func qwenTranscriptIn(t *testing.T, root, cwd, id string, record bool) string {
	t.Helper()
	encoded := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(cwd, "-")
	chats := filepath.Join(root, "projects", encoded, "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{"sessionId": id, "type": "user", "message": map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}}}
	if record {
		rec["cwd"] = cwd
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chats, id+".jsonl")
	if err := os.WriteFile(path, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// qwen keys its sessions by the directory they ran in, so with that directory
// gone a bare `qwen -r <id>` answers "No saved session found". deja printed it
// anyway; it now refuses and names the directory and `deja show`, the way it
// does for a Cursor CLI chat (#4259).
func TestResumeQwenRefusesWhenItsDirectoryIsGone(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	gone := filepath.Join(tmp, "proj-qwen")
	path := qwenTranscriptIn(t, root, gone, "555a7310", true)

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "555a7310", Path: path})
	if err == nil {
		t.Fatalf("printed %q (dir %q) for a session whose directory is gone", resumeCmdLine(dir, cmd), dir)
	}
	for _, want := range []string{gone, "gone", "deja show 555a7310"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not say %q", err, want)
		}
	}

	// Control: with the directory back, the same transcript resumes there.
	if err = os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, cmd, err = resumeCommand(model.Session{Harness: "qwen", ID: "555a7310", Path: path})
	if err != nil || cmd != "qwen -r 555a7310" || dir != gone {
		t.Fatalf("with the directory in place: dir %q cmd %q err %v", dir, cmd, err)
	}
}

// Without a recorded cwd and with no folder on disk to read back, deja cannot
// tell where the session ran; the bare command fails the same way, so it says
// so instead of printing it.
func TestResumeQwenRefusesWhenItsDirectoryIsUnknown(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	path := qwenTranscriptIn(t, root, filepath.Join(tmp, "nowhere"), "a2d5a292", false)

	if _, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "a2d5a292", Path: path}); err == nil {
		t.Fatalf("printed %q with no directory to run it in", cmd)
	} else if !strings.Contains(err.Error(), "deja show a2d5a292") {
		t.Errorf("refusal %q does not point at deja show", err)
	}
}

// The folder name is ambiguous: /w/my-app and /w/my/app encode the same. With
// the recorded /w/my-app gone and /w/my/app on disk, reading the folder name
// sent qwen to a directory that holds none of its sessions. A recorded cwd is
// the answer whenever there is one (#4259).
func TestResumeQwenTrustsTheRecordedDirectoryOverTheFolderName(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	gone := filepath.Join(tmp, "w", "my-app")
	if err := os.MkdirAll(filepath.Join(tmp, "w", "my", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := qwenTranscriptIn(t, root, gone, "555a7310", true)

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "555a7310", Path: path})
	if err == nil {
		t.Fatalf("printed %q for a session whose recorded directory is gone", resumeCmdLine(dir, cmd))
	}
	if !strings.Contains(err.Error(), gone) {
		t.Errorf("refusal %q does not name the recorded directory %q", err, gone)
	}
}

// A transcript from before qwen recorded cwd still resumes from the folder
// name when that resolves on disk.
func TestResumeQwenWithoutARecordedCWDUsesTheFolder(t *testing.T) {
	// The long form: Windows hands out a temp dir under its 8.3 name
	// (RUNNER~1), which no directory listing carries, so the folder name
	// never resolved back to it.
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "qwen")
	t.Setenv("DEJA_QWEN_ROOT", root)
	realDir := filepath.Join(tmp, "projects", "app")
	if err = os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := qwenTranscriptIn(t, root, realDir, "a2d5a292", false)

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "a2d5a292", Path: path})
	if err != nil || cmd != "qwen -r a2d5a292" {
		t.Fatalf("an old transcript no longer resumes: %q %v", cmd, err)
	}
	if runtime.GOOS != "windows" && dir != realDir {
		t.Fatalf("dir = %q, want %q", dir, realDir)
	}
}

// Reasonix finds an id only in the store of the workspace it runs in, so the
// command goes with the workspace from the session's metadata. A session saved
// with no workspace is reached by its file path, which --resume also takes.
func TestResumeReasonix(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "relaylab")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20260920-101000.000000000-deepseek-chat.jsonl")
	meta, err := json.Marshal(map[string]any{"workspace_root": ws})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path+".meta", meta, 0o644); err != nil {
		t.Fatal(err)
	}
	id := "20260920-101000.000000000-deepseek-chat"
	gotDir, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != ws || cmd != "reasonix --resume "+id {
		t.Errorf("dir/cmd = %q/%q, want the workspace and the id", gotDir, cmd)
	}

	bare := filepath.Join(dir, "bare.jsonl")
	gotDir, cmd, err = resumeCommand(model.Session{Harness: "reasonix", ID: "bare", Path: bare})
	if reasonixPathPattern.MatchString(bare) {
		if err != nil || gotDir != "" || cmd != "reasonix --resume "+bare {
			t.Errorf("no workspace: dir/cmd/err = %q/%q/%v, want the file path", gotDir, cmd, err)
		}
	}

	spaced := filepath.Join(dir, "my sessions", "bare.jsonl")
	if _, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: "bare", Path: spaced}); err == nil {
		t.Errorf("a path with a space went on the command line unquoted: %q", cmd)
	}
}

// 1.x keeps a session as a directory, and its --resume matches an id only in
// the sessions-v4 store of the working directory: a project session goes with
// the directory whose slug is its store's, and the global and desktop stores
// have no command.
func TestResumeReasonixV4(t *testing.T) {
	state := t.TempDir()
	ws := filepath.Join(t.TempDir(), "relaylab")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	slugOf := ws
	if runtime.GOOS == "windows" {
		slugOf = strings.ToLower(slugOf)
	}
	slug := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(slugOf)
	session := func(store ...string) string {
		dir := filepath.Join(append([]string{state}, store...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		m, err := json.Marshal(map[string]any{"codec": "reasonix.session.linear/v4", "sessionId": filepath.Base(dir)})
		if err != nil {
			t.Fatal(err)
		}
		h, err := json.Marshal(map[string]any{"cwd": ws})
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string][]byte{"manifest.json": m, "header.json": h, "events.frames": nil} {
			if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return filepath.Join(dir, "events.frames")
	}
	id := "839559938275e9a3ebde5a804aa24edd"
	gotDir, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: session("projects", slug, "sessions-v4", id)})
	if err != nil || gotDir != ws || cmd != "reasonix --resume "+id {
		t.Errorf("project: dir/cmd/err = %q/%q/%v, want the workspace and the id", gotDir, cmd, err)
	}
	// The sessions-v4 store is found only from its workspace, so with that
	// gone there is nowhere to run the command from (#4459).
	gone := filepath.Join(t.TempDir(), "gone")
	slugOf = gone
	if runtime.GOOS == "windows" {
		slugOf = strings.ToLower(slugOf)
	}
	goneSlug := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(slugOf)
	ws = gone
	if _, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: session("projects", goneSlug, "sessions-v4", id)}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Errorf("gone workspace: cmd = %q, err = %v, want a refusal pointing at deja show", cmd, err)
	}
	for _, store := range [][]string{{"sessions-v4", id}, {"desktop-sessions-v5", "by-id", "desktop-1"}} {
		if _, cmd, err := resumeCommand(model.Session{Harness: "reasonix", ID: id, Path: session(store...)}); err == nil {
			t.Errorf("%s: printed %q for a store --resume does not search", filepath.Join(store...), cmd)
		}
	}
}

// Every harness whose resume command goes into the directory a session ran in
// takes that directory from what the session recorded, and when it is gone
// says what the client does instead: refuses with `deja show` where the client
// finds the session only from there, or a note naming where it will run (or
// fork) where it reopens from anywhere. Command Code and a Reasonix JSONL
// session reopen from anywhere under another argument, so that is the command
// (#4456, #4459, #4460).
func TestResumeRecordedDirectoryPresentAndGone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_PI_ROOT", filepath.Join(tmp, "pi"))
	t.Setenv("DEJA_KIRO_ROOT", filepath.Join(tmp, "kiro"))
	t.Setenv("DEJA_ROO_CLI_ROOT", filepath.Join(tmp, "roo"))
	jsonStr := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	const (
		refuse = "refuse" // error naming the directory and `deja show`
		fork   = "fork"   // no cd, a note that the client offers to fork
		note   = "note"   // no cd, a note that it runs where the command does
	)
	cases := []struct {
		harness string
		gone    string
		session func(t *testing.T, name, dir string) model.Session
		goneCmd func(s model.Session) string // the command once the directory is gone, when it changes
	}{
		{"pi", fork, func(t *testing.T, _, dir string) model.Session {
			t.Helper()
			id := "01a0f828-1111-4222-8333-444455556666"
			p := filepath.Join(tmp, "pi", piSessionFolder(dir), "2026-10-01T09-00-00-000Z_"+id+".jsonl")
			writeResumeFile(t, p, `{"type":"session","version":3,"id":"`+id+`","timestamp":"2026-10-01T09:00:00.000Z","cwd":`+jsonStr(dir)+`}`+"\n")
			return model.Session{Harness: "pi", ID: id, Path: p}
		}, nil},
		{"grok", note, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			// QueryEscape, not PathEscape: the folder holds a Windows path's
			// colon too, which no Windows directory name can.
			p := filepath.Join(tmp, "grok", url.QueryEscape(dir), "019f-"+name, "updates.jsonl")
			writeResumeFile(t, p, "")
			writeResumeFile(t, filepath.Join(filepath.Dir(p), "summary.json"), `{"info":{"cwd":`+jsonStr(dir)+`}}`)
			return model.Session{Harness: "grok", ID: "019f-" + name, Path: p}
		}, nil},
		{"kiro", note, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "11111111-2222-4333-8444-555555555555"
			p := filepath.Join(tmp, "kiro", name, id+".jsonl")
			writeResumeFile(t, filepath.Join(tmp, "kiro", name, id+".json"), `{"session_id":"`+id+`","cwd":`+jsonStr(filepath.ToSlash(dir))+`}`)
			writeResumeFile(t, p, `{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"hi"}]}}`+"\n")
			return model.Session{Harness: "kiro", ID: id, Path: p}
		}, nil},
		{"continue", note, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "5f38800d-4bfe-4a34-a6f1-b117d8ce618c"
			p := filepath.Join(tmp, "continue", name, id+".json")
			writeResumeFile(t, p, `{"sessionId":"`+id+`","workspaceDirectory":`+jsonStr(dir)+`,"history":[]}`)
			return model.Session{Harness: "continue", ID: id, Path: p}
		}, nil},
		{"codewhale", note, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "eeeeeeee-0000-4000-8000-000000000005"
			p := filepath.Join(tmp, "codewhale", name, id+".json")
			writeResumeFile(t, p, `{"schema_version":1,"metadata":{"id":"`+id+`","workspace":`+jsonStr(dir)+`},"messages":[]}`)
			return model.Session{Harness: "codewhale", ID: id, Path: p}
		}, nil},
		{"commandcode", note, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "0c4d0000-1111-4222-8333-444455556666"
			p := filepath.Join(tmp, "commandcode", name, id+".jsonl")
			writeResumeFile(t, p, `{"type":"session","version":3,"id":"`+id+`","cwd":`+jsonStr(dir)+`}`+"\n")
			return model.Session{Harness: "commandcode", ID: id, Path: p}
		}, func(s model.Session) string { return commandCodeBin() + " --session " + s.ID }},
		{"reasonix", note, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "20261001-101000.000000000-deepseek-chat"
			p := filepath.Join(tmp, "reasonix", name, id+".jsonl")
			writeResumeFile(t, p+".meta", `{"workspace_root":`+jsonStr(dir)+`}`)
			return model.Session{Harness: "reasonix", ID: id, Path: p}
		}, func(s model.Session) string { return "reasonix --resume " + s.Path }},
		{"roo", refuse, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "01a07bf9-8882-7703-a3fa-245deb8ea752"
			if name == "gone" {
				id = "01a07bf9-8882-7703-a3fa-245deb8ea753"
			}
			return model.Session{Harness: "roo", ID: "roo-task-" + id, Path: rooCLITask(t, filepath.Join(tmp, "roo"), id, filepath.ToSlash(dir))}
		}, nil},
		{"crush", refuse, func(_ *testing.T, _, dir string) model.Session {
			return model.Session{Harness: "crush", ID: "942cbc1e-78c7-41cb-aa8a-78c3baab018c", Path: filepath.Join(dir, ".crush", "crush.db")}
		}, nil},
		{"clawcode", refuse, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "claw-rec-" + name
			p := filepath.Join(tmp, "clawcode", name, id+".jsonl")
			writeResumeFile(t, p, `{"type":"session_meta","session_id":`+jsonStr(id)+`,"workspace_root":`+jsonStr(dir)+`}`+"\n")
			return model.Session{Harness: "clawcode", ID: id, Path: p}
		}, nil},
		{"ante", note, func(t *testing.T, name, dir string) model.Session {
			t.Helper()
			id := "ses_ante-" + name
			sesDir := filepath.Join(tmp, "ante", name, id)
			p := filepath.Join(sesDir, "events.jsonl")
			writeResumeFile(t, p, `{"seq":0,"ts":1760000000000,"kind":"turn_started","turn":1}`+"\n")
			writeResumeFile(t, filepath.Join(sesDir, "meta.json"), `{"id":`+jsonStr(id)+`,"cwd":`+jsonStr(dir)+`}`)
			return model.Session{Harness: "ante", ID: id, Path: p}
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.harness, func(t *testing.T) {
			dir := filepath.Join(tmp, "w-"+c.harness, "proj")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			s := c.session(t, "here", dir)
			got, _, err := resumeCommand(s)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Clean(got) != filepath.Clean(dir) {
				t.Fatalf("dir = %q, want the recorded %q", got, dir)
			}
			if n := resumeDirGoneNote(s, got); n != "" {
				t.Errorf("a note about a directory that is there: %q", n)
			}

			gone := filepath.Join(tmp, "w-"+c.harness, "gone")
			s = c.session(t, "gone", gone)
			got, cmd, err := resumeCommand(s)
			if c.gone == refuse {
				if err == nil || !namesDir(err.Error(), gone) || !strings.Contains(err.Error(), "deja show") {
					t.Fatalf("dir = %q, err = %v; want a refusal naming %s and deja show", got, err, gone)
				}
				return
			}
			if err != nil || got != "" {
				t.Fatalf("dir = %q, err = %v; want no cd into a directory that is gone", got, err)
			}
			if c.goneCmd != nil && cmd != c.goneCmd(s) {
				t.Errorf("cmd = %q, want %q", cmd, c.goneCmd(s))
			}
			n := resumeDirGoneNote(s, got)
			if !namesDir(n, gone) || (c.gone == fork) != strings.Contains(n, "fork") {
				t.Errorf("note = %q, want it to name %s (fork: %v)", n, gone, c.gone == fork)
			}
		})
	}
}

// commandCodeBin is the name Command Code installs under: cmdc on Windows,
// where cmd is the shell.
func commandCodeBin() string {
	if runtime.GOOS == "windows" {
		return "cmdc"
	}
	return "cmd"
}

// namesDir reports whether s names dir, in the OS's separators or in the
// forward slashes Kiro and the Roo CLI record a Windows path in.
func namesDir(s, dir string) bool {
	return strings.Contains(s, dir) || strings.Contains(s, filepath.ToSlash(dir))
}

// pi folds every `/` of the directory into `-` for the session folder, so the
// folder of /x/my-app decodes to /x/my/app when that exists too; the header
// records the real one (#4456).
func TestResumePiTakesTheHeaderCwdNotTheFolderName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pi's folder encoding is of a unix path")
	}
	tmp := t.TempDir()
	t.Setenv("DEJA_PI_ROOT", filepath.Join(tmp, "pi"))
	realDir := filepath.Join(tmp, "w", "my-app")
	for _, d := range []string{realDir, filepath.Join(tmp, "w", "my", "app")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id := "01a0f828-1111-4222-8333-444455556666"
	p := filepath.Join(tmp, "pi", "--"+strings.ReplaceAll(strings.TrimPrefix(realDir, "/"), "/", "-")+"--", id+".jsonl")
	cwd, err := json.Marshal(realDir)
	if err != nil {
		t.Fatal(err)
	}
	writeResumeFile(t, p, `{"type":"session","version":3,"id":"`+id+`","cwd":`+string(cwd)+`}`+"\n")
	dir, cmd, err := resumeCommand(model.Session{Harness: "pi", ID: id, Path: p})
	if err != nil || dir != realDir || cmd != "pi --session "+id {
		t.Fatalf("got (%q, %q, %v), want (%q, pi --session %s)", dir, cmd, err, realDir, id)
	}
}

// piSessionFolder is pi's own name for a project's session folder
// (session-manager.ts): one leading separator dropped, every `/`, `\` and `:`
// a dash. Folding only `/` left a Windows path's `C:\` in the name, and mkdir
// refused it (#4521).
func piSessionFolder(dir string) string {
	if strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, `\`) {
		dir = dir[1:]
	}
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(dir) + "--"
}

func TestPiSessionFolderIsPisOnBothSeparators(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/x/proj":    "--Users-x-proj--",
		`C:\Users\x\proj`:  "--C--Users-x-proj--",
		`\\srv\share\proj`: "---srv-share-proj--",
	} {
		if got := piSessionFolder(in); got != want {
			t.Errorf("piSessionFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

// A Kimi or Qwen sub-agent log is a session in deja under
// DEJA_INCLUDE_SUBAGENTS=1, but neither client opens one on its own: resume
// printed `kimi --session agent-1-<parent>`, an id Kimi has never heard of
// (#4483). It says so, and names the session that spawned it.
func TestResumeRefusesAKimiOrQwenSubagent(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	qwenParent := qwenTranscriptIn(t, filepath.Join(tmp, "qwen"), proj, "q-1", true)
	qwenChild := filepath.Join(filepath.Dir(filepath.Dir(qwenParent)), "subagents", "q-1", "agent-a1.jsonl")
	for _, s := range []model.Session{
		{Harness: "qwen", ID: "agent-a1-q-1", Kind: "subagent", Parent: "q-1", Path: qwenChild},
		{Harness: "kimi", ID: "agent-1-session_t01", Kind: "subagent", Parent: "session_t01",
			Path: filepath.Join(tmp, "kimi", "sessions", "wd", "session_t01", "agents", "agent-1", "wire.jsonl")},
		{Harness: "gemini", ID: "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6", Kind: "subagent", Parent: "f7b7a064-d884-4582-a228-e95a8c1ac4f3",
			Path: filepath.Join(tmp, "gemini", "tmp", "proj", "chats", "f7b7a064-d884-4582-a228-e95a8c1ac4f3", "24b39ab6-b48a-48e4-80f9-0ba7bb24acc6.jsonl")},
	} {
		dir, cmd, err := resumeCommand(s)
		if err == nil {
			t.Errorf("%s: printed %q for a sub-agent run", s.Harness, resumeCmdLine(dir, cmd))
			continue
		}
		for _, want := range []string{"sub-agent", "deja resume " + s.Parent} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: refusal %q does not say %q", s.Harness, err, want)
			}
		}
	}
	// Control: the parent still resumes.
	if _, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "q-1", Path: qwenParent}); err != nil || cmd != "qwen -r q-1" {
		t.Errorf("the parent resumes as %q: %v", cmd, err)
	}
}

// A Kimi /btw side question is read as a fork of the session it was asked in,
// and Kimi does not open the fork on its own either (#4484).
func TestResumeRefusesAKimiBtwFork(t *testing.T) {
	s := model.Session{Harness: "kimi", ID: "agent-1-session_t01", Kind: "fork", Parent: "session_t01",
		Path: filepath.Join(t.TempDir(), "kimi", "sessions", "wd", "session_t01", "agents", "agent-1", "wire.jsonl")}
	dir, cmd, err := resumeCommand(s)
	if err == nil {
		t.Fatalf("printed %q for a /btw fork", resumeCmdLine(dir, cmd))
	}
	if !strings.Contains(err.Error(), "deja resume session_t01") {
		t.Errorf("refusal %q does not name the session to resume", err)
	}
}

// A session /fork or `kimi fork` made is a session of its own, which
// `kimi --session` opens; resume called it a /btw side question (#5226).
func TestResumeOpensAKimiSessionFork(t *testing.T) {
	s := model.Session{Harness: "kimi", ID: "session_fork", Kind: "fork", Parent: "session_src",
		Path: filepath.Join(t.TempDir(), "kimi", "sessions", "wd", "session_fork", "agents", "main", "wire.jsonl")}
	_, cmd, err := resumeCommand(s)
	if err != nil || !strings.Contains(cmd, "session_fork") {
		t.Fatalf("a kimi session fork resumes as %q: %v", cmd, err)
	}
}

// A Cursor CLI subagent was told it had left cursor-agent's store, which it
// was never in: its state lives in the parent's chat, so the refusal names
// the parent. Hermes and OpenClaw do reopen a child, checked live, so theirs
// is passed through (#5303).
func TestResumeSubagentChildPerClient(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cursor")
	t.Setenv("DEJA_CURSOR_CLI_ROOT", cli)
	t.Setenv("CURSOR_CONFIG_DIR", cli)
	cwd := filepath.Join(tmp, "work", "my-app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := "11111111-0000-0000-0000-000000000001"
	child := "22222222-0000-0000-0000-000000000002"
	tp := seedCursorChat(t, cli, cwd, parent)
	cp := filepath.Join(filepath.Dir(tp), "subagents", child+".jsonl")
	if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp, []byte(`{"role":"user","message":{"content":[{"type":"text","text":"write the notes"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: child, Kind: "subagent", Parent: parent, Path: cp})
	if err == nil {
		t.Fatalf("cursor: printed %q for a subagent", resumeCmdLine(dir, cmd))
	}
	for _, want := range []string{"sub-agent", "deja resume " + parent} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("cursor: refusal %q does not say %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "no longer in") {
		t.Errorf("cursor: refusal %q says the child left a store it was never in", err)
	}
	// Control: the parent still resumes.
	if _, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: parent, Path: tp}); err != nil || !strings.Contains(cmd, "cursor-agent --resume "+parent) {
		t.Errorf("cursor parent resumes as %q: %v", cmd, err)
	}

	hermes := model.Session{Harness: "hermes", ID: "20261018_084719_441ac7", Kind: "subagent", Parent: "20261018_084704_a4b1b7",
		Path: filepath.Join(tmp, "hermes", "state.db")}
	if _, cmd, err := resumeCommand(hermes); err != nil || cmd != "hermes --resume "+hermes.ID {
		t.Errorf("hermes child resumes as %q: %v", cmd, err)
	}

	sessions := filepath.Join(tmp, "openclaw", "agents", "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	key := "agent:main:subagent:8b21e74e-29b0-4184-ab79-488c87723f7b"
	if err := os.WriteFile(filepath.Join(sessions, "sessions.json"), []byte(`{"agent:main:main":{"sessionId":"p"},"`+key+`":{"sessionId":"kid","spawnedBy":"agent:main:main"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claw := model.Session{Harness: "openclaw", ID: "kid", Kind: "subagent", Parent: "p", Path: filepath.Join(sessions, "kid.jsonl")}
	if _, cmd, err := resumeCommand(claw); err != nil || cmd != "openclaw chat --session "+key {
		t.Errorf("openclaw child resumes as %q: %v", cmd, err)
	}
}

func TestResumeCommandPerHarness(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("DEJA_ZCODE_LEGACY_ROOT", filepath.Join(tmp, "zcode-legacy"))
	realDir := filepath.Join(tmp, "projects", "my-app")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := strings.ReplaceAll(realDir, string(filepath.Separator), "-")
	claudePath := filepath.Join(string(filepath.Separator), "claude", "projects", encoded, "abc.jsonl")
	grokPath := filepath.Join(tmp, "grok-sessions", url.PathEscape(realDir), "019f-grok", "updates.jsonl")

	aFile := filepath.Join(tmp, "ses_3.json")
	if err := os.WriteFile(aFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		s       model.Session
		wantDir string
		wantCmd string
		wantErr string
	}{
		{"claude with resolvable dir", model.Session{Harness: "claude", ID: "abc-123", Project: "projects/my-app", Path: claudePath}, realDir, "claude --resume abc-123", ""},
		{"codex rollout", model.Session{Harness: "codex", ID: "uuid-1", Project: "my-app"}, "", "codex resume uuid-1", ""},
		{"codex history entry", model.Session{Harness: "codex", ID: "uuid-2", Project: "history"}, "", "", "nothing to resume"},
		// Claude Code deleted the transcript; its prompt log is all that is left (#5046).
		{"claude prompts only", model.Session{Harness: "claude", ID: "abc-124", Project: "proj/app", Path: filepath.Join(tmp, ".claude", "history.jsonl")}, "", "", "nothing to resume"},
		{"opencode with dir", model.Session{Harness: "opencode", ID: "ses_1", Project: "my-app", Path: realDir}, realDir, "opencode -s ses_1", ""},
		// opencode reopens a session from any directory; a cd into a deleted one
		// stopped the command before it started (#4201).
		{"opencode with its dir gone", model.Session{Harness: "opencode", ID: "ses_2", Project: "gone", Path: filepath.Join(tmp, "projects", "gone")}, "", "opencode -s ses_2", ""},
		{"opencode path that is a file", model.Session{Harness: "opencode", ID: "ses_3", Project: "f", Path: aFile}, "", "opencode -s ses_3", ""},
		{"kilo with its dir gone", model.Session{Harness: "kilocode", ID: "ses_4", Project: "gone", Path: filepath.Join(tmp, "projects", "gone")}, "", "kilo -s ses_4", ""},
		// The ZCode terminal client takes the sess_ id its database stores, and
		// reopens it from anywhere; the cd keeps the agent in the project (#4430).
		{"zcode cli session", model.Session{Harness: "zcode", ID: "sess_319135bc-f58b-40b8-b7df-4c32660105f1", Project: "my-app", Path: realDir}, realDir, "zcode --resume sess_319135bc-f58b-40b8-b7df-4c32660105f1", ""},
		{"zcode with its dir gone", model.Session{Harness: "zcode", ID: "sess_2", Project: "gone", Path: filepath.Join(tmp, "projects", "gone")}, "", "zcode --resume sess_2", ""},
		{"zcode legacy snapshot", model.Session{Harness: "zcode", ID: "acp-9", Project: "my-app", Path: filepath.Join(tmp, "zcode-legacy", "ab12", "task-1.json")}, "", "", "restore-legacy-sessions"},
		{"zcode desktop transcript", model.Session{Harness: "zcode", ID: "abc", Project: "my-app", Path: filepath.Join(tmp, "zcode", "projects", encoded, "abc.jsonl")}, "", "", "CLI database"},
		{"grok build session", model.Session{Harness: "grok", ID: "019f-grok", Project: "my-app", Path: grokPath}, realDir, "grok --resume 019f-grok", ""},
		{"grok-dev row", model.Session{Harness: "grok", ID: "019f-dev", Project: "my-app", Path: filepath.Join(tmp, "grok.db")}, "", "", "grok-dev store"},
		{"imported", model.Session{Harness: "claude", ID: "imported-9f5", Project: "imported:my-app"}, "", "", "another machine"},
		{"unknown harness", model.Session{Harness: "mystery", ID: "x"}, "", "", "don't know how"},
	}
	for _, c := range cases {
		if runtime.GOOS == "windows" && c.name == "claude with resolvable dir" {
			continue // claude encodes unix-style absolute paths; resolution is a no-op on windows
		}
		dir, cmd, err := resumeCommand(c.s)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("%s: err = %v, want %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if dir != c.wantDir || cmd != c.wantCmd {
			t.Fatalf("%s: got (%q, %q), want (%q, %q)", c.name, dir, cmd, c.wantDir, c.wantCmd)
		}
	}
}

func TestFormatResumeCommand(t *testing.T) {
	dir := filepath.Join("tmp", "project's dir")
	got := resumeCmdLine(dir, "claude --resume 019f")
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(got, "powershell.exe -NoProfile") || !strings.Contains(got, "project''s dir") || !strings.Contains(got, "-ErrorAction Stop") || !strings.Contains(got, "claude --resume 019f") {
			t.Fatalf("Windows resume command = %q", got)
		}
		return
	}
	want := "cd " + shellQuote(dir) + " && claude --resume 019f"
	if got != want {
		t.Fatalf("resume command = %q, want %q", got, want)
	}
}

func TestRunResumePrintAndErrors(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-resume")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"resume123","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"resume needle"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "resume123.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	var out bytes.Buffer
	if err := runResume(index.DefaultDir(), []string{"resume"}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "claude --resume resume123") {
		t.Fatalf("resume output = %q", got)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing", nil, "resume needs an id prefix"},
		{"exec without prefix", []string{"--exec"}, "resume needs an id prefix"},
		{"not found", []string{"nope"}, `no session matches "nope"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var discard bytes.Buffer
			err := runResume(index.DefaultDir(), tc.args, &discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// qwen scopes `qwen sessions list` to the current project, so the id alone is
// not enough: run the command anywhere else and it reopens nothing.
func TestResumeQwenRunsInTheProjectDirectory(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "projects", "my-app")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	path := qwenTranscriptIn(t, filepath.Join(tmp, "qwen"), realDir, "a2d5a292", true)

	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "a2d5a292", Project: "my-app", Path: path})
	if err != nil {
		t.Fatalf("qwen resume: %v", err)
	}
	if cmd != "qwen -r a2d5a292" {
		t.Fatalf("cmd = %q", cmd)
	}
	if runtime.GOOS != "windows" && dir != realDir {
		t.Fatalf("dir = %q, want the project directory %q", dir, realDir)
	}
}

// A directory named outside ASCII is blanked in qwen's folder name, so the cd
// comes from the cwd the transcript records (#4258).
func TestResumeQwenRunsInANonASCIIProjectDirectory(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "проект q")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	encoded := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(realDir, "-")
	chats := filepath.Join(tmp, "qwen", "projects", encoded, "chats")
	if err := os.MkdirAll(chats, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chats, "ee561e79.jsonl")
	cwd, err := json.Marshal(realDir)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"sessionId":"ee561e79","type":"user","cwd":` + string(cwd) + `,"message":{"role":"user","parts":[{"text":"hi"}]}}` + "\n"
	if err = os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, cmd, err := resumeCommand(model.Session{Harness: "qwen", ID: "ee561e79", Path: path})
	if err != nil || cmd != "qwen -r ee561e79" {
		t.Fatalf("qwen resume: %q %v", cmd, err)
	}
	if dir != realDir {
		t.Fatalf("dir = %q, want %q", dir, realDir)
	}
}

// Crush looks for a session in the store under the current directory and
// nowhere else, so the same id resolves to nothing when the command is run
// anywhere but the project the store sits under.
func TestCrushResumeRunsInTheProject(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "my-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, ".crush", "crush.db")
	id := "942cbc1e-78c7-41cb-aa8a-78c3baab018c"

	dir, cmd, err := resumeCommand(model.Session{Harness: "crush", ID: id, Project: "my-app", Path: path})
	if err != nil {
		t.Fatalf("crush resume: %v", err)
	}
	if cmd != "crush --session "+id {
		t.Fatalf("cmd = %q", cmd)
	}
	if dir != project {
		t.Fatalf("dir = %q, want the project directory %q", dir, project)
	}

	// An id that is not a uuid never reaches a command line.
	if _, _, err := resumeCommand(model.Session{Harness: "crush", ID: "x; rm -rf /", Project: "p", Path: path}); err == nil {
		t.Fatal("a non-uuid id was accepted")
	}
}

// Kimi Code refuses a session from any directory but the one it was created
// in, so the command cds into the workDir state.json records; one that is gone
// is refused with a pointer to deja show, since the bare command fails too.
func TestResumeKimiRunsInTheSessionDirectory(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "proj-kimi")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "session_f39de7f4-4831-4734-a289-fc75046cc67e"
	sessionDir := filepath.Join(tmp, "kimi", "sessions", "wd_proj-kimi_f31a2b2fc330", id)
	main := filepath.Join(sessionDir, "agents", "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	writeState := func(workDir string) {
		b, err := json.Marshal(map[string]string{"title": "t", "workDir": workDir})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sessionDir, "state.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(main, "wire.jsonl")

	writeState(project)
	dir, cmd, err := resumeCommand(model.Session{Harness: "kimi", ID: id, Path: path})
	if err != nil || cmd != "kimi --session "+id {
		t.Fatalf("kimi resume: %q %v", cmd, err)
	}
	if dir != project {
		t.Fatalf("dir = %q, want the session's workDir %q", dir, project)
	}

	writeState(filepath.Join(tmp, "gone"))
	if dir, cmd, err := resumeCommand(model.Session{Harness: "kimi", ID: id, Path: path}); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("resume = (%q, %q, %v) for a workDir that no longer exists, want a refusal naming deja show", dir, cmd, err)
	}
}

// prime-agent resumes a session only from the project it ran in ("belongs to
// a different project"), so the command cds into the header's cwd; a cwd that
// is gone gets no cd (#4408).
func TestResumePrimeRunsInTheSessionDirectory(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "proj-prime")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "01a0f98f-da37-7692-b35e-1d5dba9f2589"
	path := filepath.Join(tmp, "prime", "sessions", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeHeader := func(cwd string) {
		head, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": cwd, "rlmDepth": 0})
		if err != nil {
			t.Fatal(err)
		}
		turn := `{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}`
		if err := os.WriteFile(path, []byte(string(head)+"\n"+turn+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeHeader(project)
	dir, cmd, err := resumeCommand(model.Session{Harness: "prime", ID: id, Path: path})
	if err != nil || cmd != "prime-agent --resume "+id {
		t.Fatalf("prime resume: %q %v", cmd, err)
	}
	if dir != project {
		t.Fatalf("dir = %q, want the header's cwd %q", dir, project)
	}

	writeHeader(filepath.Join(tmp, "gone"))
	if dir, _, _ := resumeCommand(model.Session{Harness: "prime", ID: id, Path: path}); dir != "" {
		t.Fatalf("dir = %q for a cwd that no longer exists, want none", dir)
	}
}

// Cursor's CLI transcripts are named after the chat id `--resume` takes, while
// IDE chats come out of a different store and reopen only in the editor.
func TestResumeCursorSplitsCLIFromIDE(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "app")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Cursor encodes without the leading separator: Users-x-app, not -Users-x-app.
	encoded := strings.TrimPrefix(strings.ReplaceAll(realDir, string(filepath.Separator), "-"), "-")
	id := "de875c53-88ae-4e73-8953-9813479364d8"
	path := filepath.Join(tmp, "projects", encoded, "agent-transcripts", id, id+".jsonl")
	// cursor-agent opens the chat from chats/<md5 of the directory>/<id>.
	t.Setenv("CURSOR_CONFIG_DIR", tmp)
	t.Setenv("DEJA_CURSOR_CLI_ROOT", tmp)
	store := filepath.Join(tmp, "chats", sources.CursorChatBucket(realDir), id, "store.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The folder name does not decode back to a Windows path; meta.json names it.
	meta, err := json.Marshal(map[string]string{"cwd": realDir})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(store), "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	dir, cmd, err := resumeCommand(model.Session{Harness: "cursor", ID: id, Project: "app", Path: path})
	if err != nil {
		t.Fatalf("cursor resume: %v", err)
	}
	if cmd != "cursor-agent --resume "+id {
		t.Fatalf("cmd = %q", cmd)
	}
	if dir != realDir {
		t.Fatalf("dir = %q, want the project directory %q", dir, realDir)
	}

	// An IDE chat carries a composer id from state.vscdb, which cursor-agent
	// does not take: printing the command anyway sends someone to a new chat.
	if _, _, err := resumeCommand(model.Session{Harness: "cursor", ID: "composer-1", Path: filepath.Join(tmp, "state.vscdb")}); err == nil {
		t.Fatal("an IDE chat produced a terminal command")
	}
}

// gemini finds a session only from the directory it ran in (#4211); the
// store records it in projects.json and in the project folder's .project_root.
func TestResumeGeminiRunsInTheProjectDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".gemini")
	t.Setenv("DEJA_GEMINI_ROOT", root)
	work := filepath.Join(t.TempDir(), "проект app")
	chats := filepath.Join(root, "tmp", "app", "chats")
	for _, d := range []string{work, chats} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(chats, "session-2026-10-01T12-50-a5bc80ac.jsonl")
	s := model.Session{Harness: "gemini", ID: "a5bc80ac-786c", Project: "app", Path: path}

	// .project_root alone, as the folder writes it.
	if err := os.WriteFile(filepath.Join(root, "tmp", "app", ".project_root"), []byte(work), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, cmd, err := resumeCommand(s)
	if err != nil || cmd != "gemini --resume a5bc80ac-786c" || dir != work {
		t.Fatalf("resume = (%q, %q, %v), want the command run in %q", dir, cmd, err, work)
	}
	// projects.json is the fallback when the folder keeps no .project_root.
	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"projects":{` + jsonString(other) + `:"app"}}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(s); dir != work {
		t.Fatalf("dir = %q, want .project_root's %q over the registry", dir, work)
	}
	if err := os.Remove(filepath.Join(root, "tmp", "app", ".project_root")); err != nil {
		t.Fatal(err)
	}
	if dir, _, _ := resumeCommand(s); dir != other {
		t.Fatalf("dir = %q, want the registry's %q", dir, other)
	}
	// A directory that is gone is refused: gemini would not find the
	// session from anywhere else.
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	if dir, cmd, err := resumeCommand(s); err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("resume = (%q, %q, %v) for a directory that is gone, want a refusal naming deja show", dir, cmd, err)
	}
}

// An older store keys the project folder by a hash of the path and records
// nothing to invert — the command carries no cd.
func TestResumeGeminiPrintsNoDirectory(t *testing.T) {
	t.Setenv("DEJA_GEMINI_ROOT", t.TempDir())
	dir, cmd, err := resumeCommand(model.Session{Harness: "gemini", ID: "a5bc80ac-786c", Project: "app", Path: "/g/tmp/app/chats/s.jsonl"})
	if err != nil {
		t.Fatalf("gemini resume: %v", err)
	}
	if cmd != "gemini --resume a5bc80ac-786c" {
		t.Fatalf("cmd = %q", cmd)
	}
	if dir != "" {
		t.Fatalf("dir = %q, want none — the store keeps a hash, not a path", dir)
	}
}

// OpenClaw addresses a conversation by key, and the uuid its file is named
// after opens nothing. The key lives in the store beside the transcript.
func TestResumeOpenClawUsesTheSessionKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents", "main", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "7de16196.jsonl")
	store := `{"agent:main:other":{"sessionId":"0000"},"agent:main:main":{"sessionId":"7de16196"}}`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(store), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cmd, err := resumeCommand(model.Session{Harness: "openclaw", ID: "7de16196", Project: "openclaw-main", Path: path})
	if err != nil {
		t.Fatalf("openclaw resume: %v", err)
	}
	if cmd != "openclaw chat --session agent:main:main" {
		t.Fatalf("cmd = %q", cmd)
	}

	// A transcript the store does not know about has no key, and a command
	// built from the uuid would open a new conversation instead of that one.
	orphan := filepath.Join(dir, "deadbeef.jsonl")
	if _, _, err := resumeCommand(model.Session{Harness: "openclaw", ID: "deadbeef", Path: orphan}); err == nil {
		t.Fatal("a session missing from the store resumed anyway")
	}
}

func TestResumeOpenClawSQLiteUsesTheCurrentSessionKey(t *testing.T) {
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "registry", "openclaw", "agent", "openclaw-agent.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "agents", "main", "agent", "openclaw-agent.sqlite")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	create := exec.CommandContext(t.Context(), "sqlite3", db)
	create.Stdin = bytes.NewReader(sql)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create OpenClaw store: %v: %s", err, out)
	}
	// The transcript fixture omits the current-session table; resume must not
	// guess which window the key opens when that mapping is absent.
	if _, _, err := resumeCommand(model.Session{Harness: "openclaw", ID: "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d", Path: db}); err == nil {
		t.Fatal("resume accepted a store without a current-session mapping")
	}
	// A reset leaves both windows searchable, but only the pointed-to one
	// may be reopened under their shared key.
	check := func(current, old string) {
		t.Helper()
		sessions, err := sources.ParseOpenClawDB(db)
		if err != nil || len(sessions) != 2 {
			t.Fatalf("parse OpenClaw store: sessions=%v err=%v", sessions, err)
		}
		for _, s := range sessions {
			_, cmd, err := resumeCommand(s)
			switch s.ID {
			case current:
				if err != nil || cmd != "openclaw chat --session agent:main:main" {
					t.Errorf("current session %s: cmd=%q err=%v", s.ID, cmd, err)
				}
			case old:
				if err == nil {
					t.Errorf("old window %s reopened the current session with %q", s.ID, cmd)
				} else if !strings.Contains(err.Error(), "not the current session") {
					// It is in the store; saying otherwise sends the user looking for it.
					t.Errorf("old window %s: %v", s.ID, err)
				}
			default:
				t.Errorf("unexpected session %s", s.ID)
			}
		}
	}
	first := "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	second := "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e"
	stmt := "CREATE TABLE session_nodes (session_key TEXT PRIMARY KEY, current_session_id TEXT NOT NULL); " +
		"INSERT INTO session_nodes VALUES ('agent:main:main', '" + second + "');"
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmt).CombinedOutput(); err != nil {
		t.Fatalf("add current-session pointer: %v: %s", err, out)
	}
	check(second, first)
	// Changing the authoritative pointer must change which window is safe to resume.
	stmt = "UPDATE session_nodes SET current_session_id = '" + first + "' WHERE session_key = 'agent:main:main'"
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, stmt).CombinedOutput(); err != nil {
		t.Fatalf("update current-session pointer: %v: %s", err, out)
	}
	check(first, second)
}

// Hermes was the one harness deja gave up on, while its own CLI takes the
// exact session ID deja indexes.
func TestResumeHermes(t *testing.T) {
	dir, cmd, err := resumeCommand(model.Session{ID: "20260727_135519_a55c71", Harness: "hermes", Project: "p"})
	if err != nil {
		t.Fatalf("hermes resume: %v", err)
	}
	if dir != "" {
		t.Fatalf("hermes resume should not need a directory, got %q", dir)
	}
	if cmd != "hermes --resume 20260727_135519_a55c71" {
		t.Fatalf("cmd = %q", cmd)
	}
}

// resumeCmdLine is the line runResume prints on this machine.
func resumeCmdLine(dir, cmdline string) string {
	line, _ := resumeLine(runtime.GOOS, dir, cmdline)
	return line
}

// A workspace read off a Roo CLI task is whatever history_item.json says, so
// resumeWord sees attacker-written text. These are the ones that broke out of
// the quoted word, or into roo's options, in some shell the line is pasted into.
func TestResumeWordRefusesWhatSomeShellActsOn(t *testing.T) {
	for _, s := range []string{
		// fish reads \' inside single quotes as a quote, so the word runs on
		// past its end and the next quoted word turns inside out: with the cd
		// in front, `;id;` ran.
		`/tmp/;id;\`,
		`C:\`,
		// PowerShell closes a single-quoted string on any of these, and the
		// outer double-quoted -Command on the double ones.
		"C:\\x\u2019; calc; \u2019",
		"C:\\x\u2018",
		"C:\\x\u201a",
		"C:\\x\u201b",
		"C:\\x\u201c; calc",
		"C:\\x\u201d",
		"C:\\x\u201e",
		// A leading dash is an option to roo, not its -w value.
		"--yolo",
		"-w",
		// cmd.exe expands these inside the Windows line's double quotes.
		`C:\%USERNAME%&calc`,
		`C:\!USERNAME!&calc`,
	} {
		if w, ok := resumeWord(s); ok {
			t.Errorf("resumeWord(%q) = %q, want refused", s, w)
		}
	}
}

// PowerShell, which a Windows resume line runs in, reads a bare a,b as two
// arguments and a bare @x as a splatted variable, so those are quoted.
func TestResumeWordQuotesWhatPowerShellSplits(t *testing.T) {
	for _, s := range []string{"C:/a,--yolo", "@x", "50%"} {
		w, ok := resumeWord(s)
		if !ok || w != "'"+s+"'" {
			t.Errorf("resumeWord(%q) = %q, %v; want it quoted", s, w, ok)
		}
	}
}

// Whatever resumeWord lets onto a command, --exec reads back as the one
// argument it was, and no character a shell acts on is left outside quotes.
func FuzzResumeWordRoundTrips(f *testing.F) {
	for _, s := range []string{"", "/tmp/proj", `C:\Users\JOHNSM~1\My Projects\app`, "a b", "bob's", "$(id)", "`id`", "a\nb", `x\`, "-x", "a,b", "\u2019", "é", "a\u00a0b", `\\srv\share`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		w, ok := resumeWord(s)
		if !ok {
			return
		}
		if !utf8.ValidString(s) {
			t.Fatalf("resumeWord(%q) = %q; --exec reads an invalid byte back as U+FFFD", s, w)
		}
		line := "roo -w " + w + " --session-id 01a07bf9-8882-7703-a3fa-245deb8ea753"
		got, err := resumeArgv(line)
		if err != nil || len(got) != 5 || got[2] != s {
			t.Fatalf("resumeWord(%q) = %q, read back as %q, %v", s, w, got, err)
		}
		if strings.HasPrefix(s, "-") || strings.HasSuffix(s, `\`) || strings.ContainsAny(s, "'\"$`\u2018\u2019\u201a\u201b\u201c\u201d\u201e") {
			t.Fatalf("resumeWord(%q) = %q, want refused", s, w)
		}
		if !strings.HasPrefix(w, "'") {
			for _, r := range w {
				if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_+:./-", r) {
					t.Fatalf("resumeWord(%q) = %q bare with %q in it", s, w, r)
				}
			}
		}
	})
}

func TestWriteBackAppendKeepsTheLastLineWhole(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "session_index.jsonl")
	if err := os.WriteFile(p, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeBackAppend(root, p, []byte(`{"b":2}`+"\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{"a":1}`+"\n"+`{"b":2}`+"\n" {
		t.Errorf("index = %q", b)
	}
	if err := writeBackAppend(root, filepath.Join(t.TempDir(), "x.jsonl"), []byte("x\n")); err == nil {
		t.Error("appended outside the store")
	}
}

// Grok needs summary.json beside updates.jsonl. One already there stops the
// write before anything lands, and the two go back together otherwise.
func TestWriteBackSidecarIsNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_GROK_ROOT", root)
	id := "01a11b02-2491-7ae0-ba1b-48dd82cc8841"
	dir := filepath.Join(root, "sessions", "%2Fwork%2Fapi", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(dir, "summary.json")
	if err := os.WriteFile(summary, []byte(`{"mine":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s := model.Session{Harness: "grok", ID: id, Path: filepath.Join(dir, "updates.jsonl"), Started: at,
		Messages: []model.Message{{Role: "user", Text: "why", Time: at}, {Role: "assistant", Text: "because", Time: at}}}
	err := writeBackSession(t.TempDir(), s, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "summary.json") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Errorf("updates.jsonl was written beside a summary.json deja did not write")
	}
	if b, _ := os.ReadFile(summary); string(b) != `{"mine":true}` {
		t.Errorf("summary.json changed: %s", b)
	}

	if err := os.Remove(summary); err != nil {
		t.Fatal(err)
	}
	if err := writeBackSession(t.TempDir(), s, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{s.Path, summary} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s not written: %v", filepath.Base(p), err)
		}
	}
}

// A transcript the agent deleted is written back from the index, in the
// agent's format and at the path it was read from, and then resumes (#4617).
// The index keeps the commands and tool output the file cannot carry, and
// reads only what the agent appends afterwards.
func TestResumeWriteBackRestoresADeletedClaudeSession(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(tmp, "claude", "projects")
	proj := filepath.Join(root, "-work-api")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("CODEX_HOME", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))

	id := "5a1c0de0-1111-4222-8333-444455556666"
	path := filepath.Join(proj, id+".jsonl")
	body := `{"type":"user","sessionId":"` + id + `","uuid":"u1","parentUuid":null,"timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"why does the pool exhaust"}}` + "\n" +
		`{"type":"assistant","sessionId":"` + id + `","uuid":"a1","parentUuid":"u1","timestamp":"2026-10-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./internal/pool"}}]}}` + "\n" +
		`{"type":"user","sessionId":"` + id + `","uuid":"u2","parentUuid":"a1","timestamp":"2026-10-01T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"FAIL pool exhausted","is_error":true}]}}` + "\n" +
		`{"type":"assistant","sessionId":"` + id + `","uuid":"a2","parentUuid":"u2","timestamp":"2026-10-01T10:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"raise MaxOpenConns to 50"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	var out bytes.Buffer
	if err := runResume(dir, []string{id}, &out); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runResume(dir, []string{id}, &out); err == nil || !strings.Contains(err.Error(), "--write-back") {
		t.Fatalf("a deleted session should point at --write-back: %v", err)
	}

	out.Reset()
	if err := runResume(dir, []string{id, "--write-back"}, &out); err != nil {
		t.Fatalf("write-back: %v", err)
	}
	if !strings.Contains(out.String(), "claude --resume "+id) {
		t.Errorf("after the write-back resume prints %q, want the claude command", out.String())
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("nothing written at %s: %v", path, err)
	}
	for _, want := range []string{"why does the pool exhaust", "raise MaxOpenConns to 50", `"sessionId":"` + id + `"`} {
		if !strings.Contains(string(written), want) {
			t.Errorf("written transcript lacks %q:\n%s", want, written)
		}
	}
	if strings.Contains(string(written), "FAIL pool exhausted") {
		t.Errorf("tool output went into the file as a turn:\n%s", written)
	}

	// Run again: the file is there, nothing is rewritten.
	before, _ := os.Stat(path)
	out.Reset()
	if err = runResume(dir, []string{id, "--write-back"}, &out); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("a second write-back touched the file")
	}

	// The agent appends a turn; the index keeps the command record it had and
	// adds the new turn.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"type":"user","sessionId":"` + id + `","uuid":"u3","parentUuid":"x","timestamp":"2026-10-02T10:00:00Z","message":{"role":"user","content":"is the cap raised"}}` + "\n")
	_ = f.Close()
	if err = index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByPrefix(dir, id)
	if err != nil || !ok {
		t.Fatalf("session gone from the index: %v", err)
	}
	roles := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
	}
	got := strings.Join(roles, ",")
	if !strings.Contains(got, "command") || !strings.HasSuffix(got, "user") {
		t.Errorf("after the append the index holds %s; want the command record kept and the new turn added", got)
	}
}

func TestWriteBackNeverOverwritesOrLeavesTheStore(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a", "s.jsonl")
	if err := writeBackCreate(root, p, []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeBackCreate(root, p, []byte("two\n")); err == nil || !strings.Contains(err.Error(), "does not overwrite") {
		t.Fatalf("an existing transcript was not refused: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one\n" {
		t.Fatalf("existing transcript changed: %q", b)
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if err := writeBackCreate(root, filepath.Join(root, "link", "s.jsonl"), []byte("x\n")); err == nil {
		t.Fatal("wrote through a link out of the store")
	}
	if _, err := os.Stat(filepath.Join(outside, "s.jsonl")); err == nil {
		t.Fatal("a file landed outside the store")
	}
}

func TestWriteBackRefusalSaysWhatIsMissing(t *testing.T) {
	tmp := hermeticEnv(t)
	s := model.Session{Harness: "opencode", ID: "ses_x", Path: filepath.Join(tmp, "work")}
	err := writeBackSession(index.DefaultDir(), s, &bytes.Buffer{})
	// With no database to ask, opencode's session is not known to be gone,
	// and nothing is written.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s = model.Session{Harness: "nosuch", ID: "x", Path: filepath.Join(tmp, "gone.jsonl")}
	err = writeBackSession(index.DefaultDir(), s, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "deja show") {
		t.Fatalf("an unsupported harness: %v", err)
	}
}
