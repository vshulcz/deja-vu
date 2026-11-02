package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestDDDProjectScopeUnificationAndBareBasenameInvariant(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	candidates := []string{"work/api", "api"}

	if query.ProjectInScope("acme/api", "api") {
		t.Fatal("query.ProjectInScope allowed bare basename 'api' to match local 'acme/api' (#2333)")
	}
	if !query.ProjectInScope("imported:acme/api", "api") {
		t.Fatal("query.ProjectInScope rejected bare basename 'api' on synced 'imported:acme/api'")
	}
	if index.ProjectInScope("acme/api", "api") != query.ProjectInScope("acme/api", "api") {
		t.Fatal("index.ProjectInScope and query.ProjectInScope disagree")
	}

	sessions := []model.Session{
		{
			ID:      "foreign-local",
			Harness: "claude",
			Project: "acme/api",
			Updated: now,
			Messages: []model.Message{
				{Role: model.RoleUser, Text: "why did the acme client billing webhook fail"},
				{Role: model.RoleAssistant, Text: "settled it by rotating the acme webhook signing secret"},
			},
		},
		{
			ID:      "peer-synced",
			Harness: "claude",
			Project: "imported:peer/api",
			Updated: now.Add(-time.Hour),
			Messages: []model.Message{
				{Role: model.RoleUser, Text: "how does the api rate limiter handle burst traffic"},
				{Role: model.RoleAssistant, Text: "settled it by raising the token bucket burst to 50"},
			},
		},
		{
			ID:      "local-in-scope",
			Harness: "claude",
			Project: "src/work/api",
			Updated: now.Add(-2 * time.Hour),
			Messages: []model.Message{
				{Role: model.RoleUser, Text: "where is the request router configured"},
				{Role: model.RoleAssistant, Text: "settled it in internal/router/routes.go"},
			},
		},
	}

	res := search.BuildAutoRecall(sessions, search.AutoRecallOptions{
		Mode:         search.RecallSafe,
		ProjectNames: candidates,
		Now:          now,
	})
	for _, id := range res.IDs {
		if id == "foreign-local" {
			t.Fatalf("BuildAutoRecall leaked foreign local project 'acme/api' via bare 'api' candidate (#2333): %v", res.IDs)
		}
	}
	if len(res.IDs) != 2 {
		t.Fatalf("BuildAutoRecall returned %v, want [peer-synced local-in-scope]", res.IDs)
	}
}

func TestDDDDomainModelValueMethodsAndPredicates(t *testing.T) {
	workRoles := []string{
		model.RoleToolOutput,
		model.RoleFiles,
		model.RoleCommand,
		model.RoleEdit,
		model.RoleSummary,
		model.RoleWrote,
	}
	for _, role := range workRoles {
		m := model.Message{Role: role, Text: "payload"}
		if !m.IsWorkRecord() || m.IsSpoken() || !model.IsWorkRole(role) || !search.IsWorkRecord(role) {
			t.Errorf("work role %q not recognized consistently across model and search", role)
		}
	}
	for _, role := range []string{model.RoleUser, model.RoleAssistant, ""} {
		m := model.Message{Role: role, Text: "hello"}
		if m.IsWorkRecord() || !m.IsSpoken() || model.IsWorkRole(role) || search.IsWorkRecord(role) {
			t.Errorf("spoken role %q misclassified as work record", role)
		}
	}
	if !model.RoleMatches(model.RoleToolOutput, model.RoleTool) || !search.RoleMatches(model.RoleToolOutput, model.RoleTool) {
		t.Error("RoleMatches failed to map --role tool to stored tool-output")
	}

	s := model.Session{Harness: "claude", ID: "abc-123", Project: "imported:org/repo"}
	meta := index.SessionMeta{Harness: "claude", ID: "abc-123", Project: "imported:org/repo"}
	if s.Key() != "claude:abc-123" || meta.Key() != "claude:abc-123" {
		t.Errorf("Key() mismatch: session=%q meta=%q", s.Key(), meta.Key())
	}
	if !s.IsImported() || !meta.IsImported() {
		t.Error("IsImported() returned false for imported:org/repo")
	}
	if s.BareProject() != "org/repo" || meta.BareProject() != "org/repo" {
		t.Errorf("BareProject() mismatch: session=%q meta=%q", s.BareProject(), meta.BareProject())
	}
}

func TestDDDMatchedTitleHandlesExtendedLatinAndRejectsUnicodePunctuation(t *testing.T) {
	extLatin := model.Session{
		ID:    "ext-latin",
		Title: "fallback title",
		Messages: []model.Message{
			{Role: model.RoleUser, Text: "—"},
			{Role: model.RoleUser, Text: "Über die Datenbankmigration sprechen"},
		},
	}
	got := matchedTitle(extLatin, nil)
	if got != "Über die Datenbankmigration sprechen" {
		t.Fatalf("matchedTitle(%q) rejected extended Latin or accepted em-dash punctuation", got)
	}
}

func TestDDDNoRawImportedPrefixOutsideDomainModel(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	fset := token.NewFileSet()
	for _, sub := range []string{"cmd/deja", "internal"} {
		root := filepath.Join(repoRoot, sub)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "internal/model/model.go" {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, err := strconv.Unquote(lit.Value)
				if err == nil && val == model.ImportedPrefix {
					pos := fset.Position(lit.Pos())
					t.Errorf("%s:%d hardcodes %q string literal; use s.IsImported(), s.BareProject(), model.IsImportedProject(p), model.BareProject(p), or model.ImportedPrefix instead",
						rel, pos.Line, model.ImportedPrefix)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestDDDNoRawSessionKeyConcatenationInCLI(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok || bin.Op != token.ADD {
				return true
			}
			leftBin, ok := bin.X.(*ast.BinaryExpr)
			if !ok || leftBin.Op != token.ADD {
				return true
			}
			selLeft, ok1 := leftBin.X.(*ast.SelectorExpr)
			litMid, ok2 := leftBin.Y.(*ast.BasicLit)
			selRight, ok3 := bin.Y.(*ast.SelectorExpr)
			if ok1 && ok2 && ok3 && selLeft.Sel.Name == "Harness" && litMid.Value == `":"` && selRight.Sel.Name == "ID" {
				pos := fset.Position(bin.Pos())
				t.Errorf("cmd/deja/%s:%d concatenates .Harness + \":\" + .ID directly; call .Key() or model.SessionKey(harness, id) instead",
					path, pos.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDDDHookseenLedgerEncapsulated(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if path == "hook_seen.go" || path == "bench_prompt.go" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(lit.Value)
			if err == nil && val == ".hookseen" {
				pos := fset.Position(lit.Pos())
				t.Errorf("cmd/deja/%s:%d accesses .hookseen directly; use the seenLines / rememberInjected* aggregate in hook_seen.go",
					path, pos.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDDDPromotedNoteDomainUnification(t *testing.T) {
	if got := model.PromotedNoteID("claude:s1"); got != "deja-note-claude-s1" {
		t.Fatalf("PromotedNoteID = %q", got)
	}
	if got := model.RepoPromotedNoteID("claude:s1"); got != "deja-repo-note-claude-s1" {
		t.Fatalf("RepoPromotedNoteID = %q", got)
	}
	// Hyphenated harness round-trip (#copilot-chat):
	copilotNote := model.Session{Harness: model.NotesHarness, ID: model.PromotedNoteID("copilot-chat:s-42")}
	if src, ok := copilotNote.PromotedNoteSourceKey(); !ok || src != "copilot-chat:s-42" {
		t.Fatalf("PromotedNoteSourceKey(%q) = (%q, %v), want (\"copilot-chat:s-42\", true)", copilotNote.ID, src, ok)
	}
	// Synced repo-scoped promoted note:
	syncedRepoNote := model.Session{
		Harness: model.NotesHarness,
		ID:      "imported-abc123",
		OrigID:  "deja-repo-note-claude-s1",
	}
	if !syncedRepoNote.IsPromotedNote() || syncedRepoNote.PromotedNoteID() != "deja-repo-note-claude-s1" {
		t.Fatalf("synced repo note not recognized: IsPromotedNote=%v PromotedNoteID=%q",
			syncedRepoNote.IsPromotedNote(), syncedRepoNote.PromotedNoteID())
	}
	if src, ok := syncedRepoNote.PromotedNoteSourceKey(); !ok || src != "claude:s1" {
		t.Fatalf("syncedRepoNote.PromotedNoteSourceKey() = (%q, %v), want (\"claude:s1\", true)", src, ok)
	}
	if !index.IsPromotedNote("deja", "imported-abc123", "deja-repo-note-claude-s1") {
		t.Fatal("index.IsPromotedNote failed on synced repo promoted note")
	}
	// LiftNotesAboveTheirSource must lift repo-scoped promoted notes above their source session:
	hits := []search.Hit{
		{Session: model.Session{Harness: "claude", ID: "s1"}, Score: 10},
		{Session: syncedRepoNote, Score: 2},
	}
	search.LiftNotesAboveTheirSource(hits)
	if hits[0].Session.ID != "imported-abc123" || hits[1].Session.ID != "s1" {
		t.Fatalf("LiftNotesAboveTheirSource did not lift repo-scoped promoted note: got [%s, %s]",
			hits[0].Session.ID, hits[1].Session.ID)
	}
}

func TestDDDNoRawPromotedNotePrefixOutsideDomainModel(t *testing.T) {
	fset := token.NewFileSet()
	repoRoot := filepath.Join("..", "..")
	for _, sub := range []string{"cmd/deja", "internal"} {
		root := filepath.Join(repoRoot, sub)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "internal/model/model.go" {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, err := strconv.Unquote(lit.Value)
				if err == nil && (val == model.PromotedNotePrefix || val == model.RepoPromotedNotePrefix) {
					pos := fset.Position(lit.Pos())
					t.Errorf("%s:%d hardcodes %q string literal; use s.PromotedNoteID(), s.IsPromotedNote(), s.PromotedNoteSourceKey(), s.CandidatePromotedNoteIDs(), or model.PromotedNoteID instead",
						rel, pos.Line, val)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
