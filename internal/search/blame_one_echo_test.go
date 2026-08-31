package search

import (
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Naming the path is evidence; naming it once and saying nothing else is not.
// A pasted absolute path and a `git diff --stat` row each mention the file once
// and outranked the session that debugged it, because the rule that puts a path
// first asked nothing about how much the session had to say (#2854).
func TestOneEchoOfThePathDoesNotOutrankAWorkingSession(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	target := BlameTarget{FullPath: "/work/api/cmd/deja/mcp.go", Base: "mcp.go", Stem: "mcp"}

	echo := model.Session{
		Harness: "opencode", ID: "echo", Project: "api", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: " cmd/deja/mcp.go | 4 +-",
		}},
	}
	worked := model.Session{
		Harness: "claude", ID: "worked", Project: "api", Updated: now.Add(-24 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-24 * time.Hour),
			Text: "mcp.go is where the budget trim happens; mcp.go cuts from the tail, and mcp.go was what broke the windows build",
		}},
	}
	hits := Blame([]model.Session{echo, worked}, target, BlameOptions{All: true})
	if len(hits) != 2 {
		t.Fatalf("both sessions name the file: %d hits", len(hits))
	}
	if hits[0].Session.ID != "worked" {
		t.Errorf("one echo of the path outranked the session that worked on the file: %s first", hits[0].Session.ID)
	}

	// And a session that names the path *and* has something to say still comes
	// first: the rule is about evidence, not about punishing paths.
	saidMore := model.Session{
		Harness: "claude", ID: "said_more", Project: "api", Updated: now.Add(-48 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-48 * time.Hour),
			Text: "cmd/deja/mcp.go holds the budget; cmd/deja/mcp.go trims from the tail",
		}},
	}
	hits = Blame([]model.Session{echo, worked, saidMore}, target, BlameOptions{All: true})
	if hits[0].Session.ID != "said_more" {
		t.Errorf("the session that named the path twice did not come first: %s", hits[0].Session.ID)
	}
}

// A real diffstat carries its own summary line, so asking the whole message
// whether it says anything let every path above that line ride on it — the
// question is about the line the path is on (#2854).
func TestADiffstatDoesNotVouchForItsOwnRows(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	target := BlameTarget{FullPath: "/work/api/cmd/deja/mcp.go", Base: "mcp.go", Stem: "mcp"}
	diffstat := model.Session{
		Harness: "opencode", ID: "diffstat", Project: "api", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: " cmd/deja/mcp.go      | 4 +-\n cmd/deja/recall.go   | 2 +-\n 2 files changed, 6 insertions(+), 2 deletions(-)",
		}},
	}
	worked := model.Session{
		Harness: "claude", ID: "worked", Project: "api", Updated: now.Add(-24 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-24 * time.Hour),
			Text: "mcp.go is where the budget trim happens; mcp.go cuts from the tail, and mcp.go broke the windows build",
		}},
	}
	hits := Blame([]model.Session{diffstat, worked}, target, BlameOptions{All: true})
	if len(hits) != 2 || hits[0].Session.ID != "worked" {
		t.Errorf("a diffstat outranked the session that worked on the file: %+v", hits)
	}
}

// And a session that says what it did in a language with no Latin letters is
// saying it: the words were counted as ASCII only.
func TestWordsAreCountedInAnyScript(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	target := BlameTarget{FullPath: "/work/api/cmd/deja/mcp.go", Base: "mcp.go", Stem: "mcp"}
	said := model.Session{
		Harness: "claude", ID: "said", Project: "api", Updated: now.Add(-48 * time.Hour),
		Messages: []model.Message{{
			Role: "assistant", Time: now.Add(-48 * time.Hour),
			Text: "починил cmd/deja/mcp.go — падал на виндах",
		}},
	}
	bare := model.Session{
		Harness: "claude", ID: "bare", Project: "api", Updated: now,
		Messages: []model.Message{{
			Role: "user", Time: now,
			Text: "mcp.go mcp.go mcp.go again",
		}},
	}
	hits := Blame([]model.Session{said, bare}, target, BlameOptions{All: true})
	if len(hits) != 2 || hits[0].Session.ID != "said" {
		t.Errorf("a session that said what it did was not read as saying it: %+v", hits)
	}
}
