package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
	jsonStr := func(s string) string { b, _ := json.Marshal(s); return string(b) }

	const (
		refuse = "refuse" // error naming the directory and `deja show`
		fork   = "fork"   // no cd, a note that the client offers to fork
		note   = "note"   // no cd, a note that it runs where the command does
	)
	cases := []struct {
		harness string
		gone    string
		session func(name, dir string) model.Session
		goneCmd func(s model.Session) string // the command once the directory is gone, when it changes
	}{
		{"pi", fork, func(name, dir string) model.Session {
			id := "01a0f828-1111-4222-8333-444455556666"
			p := filepath.Join(tmp, "pi", "--"+strings.ReplaceAll(strings.TrimPrefix(dir, "/"), "/", "-")+"--", "2026-10-01T09-00-00-000Z_"+id+".jsonl")
			writeResumeFile(t, p, `{"type":"session","version":3,"id":"`+id+`","timestamp":"2026-10-01T09:00:00.000Z","cwd":`+jsonStr(dir)+`}`+"\n")
			return model.Session{Harness: "pi", ID: id, Path: p}
		}, nil},
		{"grok", note, func(name, dir string) model.Session {
			p := filepath.Join(tmp, "grok", url.PathEscape(dir), "019f-"+name, "updates.jsonl")
			writeResumeFile(t, p, "")
			writeResumeFile(t, filepath.Join(filepath.Dir(p), "summary.json"), `{"info":{"cwd":`+jsonStr(dir)+`}}`)
			return model.Session{Harness: "grok", ID: "019f-" + name, Path: p}
		}, nil},
		{"kiro", note, func(name, dir string) model.Session {
			id := "11111111-2222-4333-8444-555555555555"
			p := filepath.Join(tmp, "kiro", name, id+".jsonl")
			writeResumeFile(t, filepath.Join(tmp, "kiro", name, id+".json"), `{"session_id":"`+id+`","cwd":`+jsonStr(filepath.ToSlash(dir))+`}`)
			writeResumeFile(t, p, `{"version":"v1","kind":"Prompt","data":{"content":[{"kind":"text","data":"hi"}]}}`+"\n")
			return model.Session{Harness: "kiro", ID: id, Path: p}
		}, nil},
		{"continue", note, func(name, dir string) model.Session {
			id := "5f38800d-4bfe-4a34-a6f1-b117d8ce618c"
			p := filepath.Join(tmp, "continue", name, id+".json")
			writeResumeFile(t, p, `{"sessionId":"`+id+`","workspaceDirectory":`+jsonStr(dir)+`,"history":[]}`)
			return model.Session{Harness: "continue", ID: id, Path: p}
		}, nil},
		{"codewhale", note, func(name, dir string) model.Session {
			id := "eeeeeeee-0000-4000-8000-000000000005"
			p := filepath.Join(tmp, "codewhale", name, id+".json")
			writeResumeFile(t, p, `{"schema_version":1,"metadata":{"id":"`+id+`","workspace":`+jsonStr(dir)+`},"messages":[]}`)
			return model.Session{Harness: "codewhale", ID: id, Path: p}
		}, nil},
		{"commandcode", note, func(name, dir string) model.Session {
			id := "0c4d0000-1111-4222-8333-444455556666"
			p := filepath.Join(tmp, "commandcode", name, id+".jsonl")
			writeResumeFile(t, p, `{"type":"session","version":3,"id":"`+id+`","cwd":`+jsonStr(dir)+`}`+"\n")
			return model.Session{Harness: "commandcode", ID: id, Path: p}
		}, func(s model.Session) string { return "cmd --session " + s.ID }},
		{"reasonix", note, func(name, dir string) model.Session {
			id := "20261001-101000.000000000-deepseek-chat"
			p := filepath.Join(tmp, "reasonix", name, id+".jsonl")
			writeResumeFile(t, p+".meta", `{"workspace_root":`+jsonStr(dir)+`}`)
			return model.Session{Harness: "reasonix", ID: id, Path: p}
		}, func(s model.Session) string { return "reasonix --resume " + s.Path }},
		{"roo", refuse, func(name, dir string) model.Session {
			id := "01a07bf9-8882-7703-a3fa-245deb8ea752"
			if name == "gone" {
				id = "01a07bf9-8882-7703-a3fa-245deb8ea753"
			}
			return model.Session{Harness: "roo", ID: "roo-task-" + id, Path: rooCLITask(t, filepath.Join(tmp, "roo"), id, filepath.ToSlash(dir))}
		}, nil},
		{"crush", refuse, func(name, dir string) model.Session {
			return model.Session{Harness: "crush", ID: "942cbc1e-78c7-41cb-aa8a-78c3baab018c", Path: filepath.Join(dir, ".crush", "crush.db")}
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.harness, func(t *testing.T) {
			dir := filepath.Join(tmp, "w-"+c.harness, "proj")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			s := c.session("here", dir)
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
			s = c.session("gone", gone)
			got, cmd, err := resumeCommand(s)
			if c.gone == refuse {
				if err == nil || !strings.Contains(err.Error(), gone) || !strings.Contains(err.Error(), "deja show") {
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
			if !strings.Contains(n, gone) || (c.gone == fork) != strings.Contains(n, "fork") {
				t.Errorf("note = %q, want it to name %s (fork: %v)", n, gone, c.gone == fork)
			}
		})
	}
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
	real := filepath.Join(tmp, "w", "my-app")
	for _, d := range []string{real, filepath.Join(tmp, "w", "my", "app")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id := "01a0f828-1111-4222-8333-444455556666"
	p := filepath.Join(tmp, "pi", "--"+strings.ReplaceAll(strings.TrimPrefix(real, "/"), "/", "-")+"--", id+".jsonl")
	cwd, _ := json.Marshal(real)
	writeResumeFile(t, p, `{"type":"session","version":3,"id":"`+id+`","cwd":`+string(cwd)+`}`+"\n")
	dir, cmd, err := resumeCommand(model.Session{Harness: "pi", ID: id, Path: p})
	if err != nil || dir != real || cmd != "pi --session "+id {
		t.Fatalf("got (%q, %q, %v), want (%q, pi --session %s)", dir, cmd, err, real, id)
	}
}
