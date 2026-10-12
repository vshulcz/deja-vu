package main

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

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
