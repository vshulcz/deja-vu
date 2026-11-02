package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
				if !ok1 || !ok2 || !ok3 || selLeft.Sel.Name != "Harness" || litMid.Value != `":"` {
					return true
				}
				switch selRight.Sel.Name {
				case "ID", "Parent", "OrigID", "FormerID", "SessionID":
					pos := fset.Position(bin.Pos())
					t.Errorf("%s:%d concatenates .Harness + \":\" + .%s directly; call .Key(), .ParentKey(), .OrigKey(), .FormerKey(), or model.SessionKey(harness, id) instead",
						rel, pos.Line, selRight.Sel.Name)
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
func TestDDDLineageAndSpawnedKindInvariants(t *testing.T) {
	for _, kind := range []string{model.KindSubagent, model.KindSidechain, model.KindSubagentFork} {
		meta := index.SessionMeta{Harness: "copilot", ID: "s1", Kind: kind, Parent: "p1", OrigID: "orig1"}
		if !meta.IsSpawned() {
			t.Fatalf("SessionMeta(Kind=%q).IsSpawned() = false; want true", kind)
		}
		if got := meta.ParentKey(); got != "copilot:p1" {
			t.Fatalf("ParentKey() = %q", got)
		}
		if got := meta.OrigKey(); got != "copilot:orig1" {
			t.Fatalf("OrigKey() = %q", got)
		}
	}
	forkMeta := index.SessionMeta{Harness: "claude", ID: "f1", Kind: model.KindFork, Parent: "p1"}
	if forkMeta.IsSpawned() {
		t.Fatal("KindFork should not be IsSpawned()")
	}
	inParent := index.SessionMeta{Harness: "copilot", ID: "t1", Kind: model.KindSidechain, Parent: "p1"}
	if !inParent.InParentFile() {
		t.Fatal("copilot sidechain with parent should report InParentFile() = true")
	}
	emptyMeta := index.SessionMeta{Harness: "claude", ID: "s1"}
	if emptyMeta.ParentKey() != "" || emptyMeta.OrigKey() != "" || emptyMeta.InParentFile() {
		t.Fatal("unset lineage fields should return empty keys and false InParentFile()")
	}
	if got := (index.Identity{Harness: "claude", ID: "s1"}).Key(); got != "claude:s1" {
		t.Fatalf("Identity.Key() = %q", got)
	}
	if got := (index.CompactionState{Harness: "claude", SessionID: "s1"}).Key(); got != "claude:s1" {
		t.Fatalf("CompactionState.Key() = %q", got)
	}

	// Ensure no non-test file checks Kind == "subagent" || Kind == "sidechain"
	// directly instead of calling .IsSpawned() or model.SpawnedKind.
	repoRoot := filepath.Join("..", "..")
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
				bin, ok := n.(*ast.BinaryExpr)
				if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
					return true
				}
				sel, ok := bin.X.(*ast.SelectorExpr)
				lit, litOK := bin.Y.(*ast.BasicLit)
				if !ok || !litOK || sel.Sel.Name != "Kind" || lit.Kind != token.STRING {
					return true
				}
				val, err := strconv.Unquote(lit.Value)
				if err == nil && (val == model.KindSidechain || val == model.KindSubagentFork) {
					pos := fset.Position(bin.Pos())
					t.Errorf("%s:%d compares .Kind against %q directly; use .IsSpawned() or model.SpawnedKind(kind) so all spawned kinds remain consistent",
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

func TestDDDLifecycleResolutionEncapsulated(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if path == "lifecycle.go" || path == "promote.go" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if ok && ident.Name == "sources" && sel.Sel.Name == "PromotedLifecycles" {
				pos := fset.Position(sel.Pos())
				t.Errorf("cmd/deja/%s:%d calls sources.PromotedLifecycles() directly; use newLifecycleResolver / attachLifecycles / attachBlameLifecycles in lifecycle.go so synced and OrigID lifecycles stay unified",
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

func TestDDDNoteFromMetaAndSyncedCommandDecision(t *testing.T) {
	meta := index.SessionMeta{
		ID:            "imported-n1",
		OrigID:        "deja-note-claude-src1",
		Harness:       model.NotesHarness,
		Project:       "imported:org/app",
		Title:         "use -p 1",
		Lifecycle:     model.LifecycleAccepted,
		LifecycleNote: "Always pass -p 1 to go test.",
		LifecycleAt:   "2026-08-29",
	}
	note := noteFromMeta(meta)
	if note.Session != "claude:src1" {
		t.Fatalf("noteFromMeta.Session = %q; want claude:src1", note.Session)
	}
	if got := note.At.Format("2006-01-02"); got != "2026-08-29" {
		t.Fatalf("noteFromMeta.At = %q; want 2026-08-29", got)
	}

	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	recs := strings.Join([]string{
		`{"harness":"claude","session_id":"src1","project":"org/app","role":"command","text":"go test ./... -p 1","time":"2026-08-04T17:44:54Z"}`,
		`{"harness":"claude","session_id":"src1","project":"org/app","role":"user","text":"run tests sequentially","time":"2026-08-04T17:44:55Z"}`,
		`{"harness":"deja","session_id":"deja-note-claude-src1","project":"org/app","role":"user","text":"[accepted] Always run go test with -p 1 (from claude:src1, 2026-08-06)","time":"2026-08-06T17:44:54Z"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-aaaa-1.jsonl"), []byte(recs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "sync", "import", batch); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	// FindByIdentity resolves imported sessions by OrigID as well as local ID.
	if s, ok, err := index.FindByIdentity(dir, "claude", "src1"); err != nil || !ok || s.OrigID != "src1" {
		t.Fatalf("FindByIdentity by OrigID = (%+v, %v, %v)", s, ok, err)
	}
	// FindManyByIdentity, FindByID, FindByPrefix("claude:src1"), and PrefixMatches("claude:src1")
	// all resolve synced sessions by OrigID.
	if found, err := index.FindManyByIdentity(dir, []index.Identity{{Harness: "claude", ID: "src1"}}); err != nil || len(found) != 1 || found[0].OrigID != "src1" {
		t.Fatalf("FindManyByIdentity by OrigID = (%+v, %v)", found, err)
	}
	if s, ok, err := index.FindByID(dir, "src1"); err != nil || !ok || s.OrigID != "src1" {
		t.Fatalf("FindByID by OrigID = (%+v, %v, %v)", s, ok, err)
	}
	if s, ok, err := index.FindByPrefix(dir, "claude:src1"); err != nil || !ok || s.OrigID != "src1" {
		t.Fatalf("FindByPrefix(claude:src1) = (%+v, %v, %v)", s, ok, err)
	}
	if matches := index.PrefixMatches(dir, "claude:src1"); matches != 1 {
		t.Fatalf("PrefixMatches(claude:src1) = %d; want 1", matches)
	}
	// promotedCommandDecision resolves a synced accepted note back to its
	// synced source transcript and checks SessionRanCommand.
	if got := promotedCommandDecision(dir, "/work/org/app", "go test ./... -p 1"); !strings.Contains(got, "Always run go test with -p 1") {
		t.Fatalf("promotedCommandDecision = %q; want synced convention", got)
	}
}

func TestDDDWave15And16HandoverRecapAndSidecarInvariants(t *testing.T) {
	if isHandoverSession(model.Session{Harness: model.NotesHarness, ID: "deja-note-1"}) {
		t.Fatalf("isHandoverSession allowed synthetic deja note")
	}
	if isHandoverSession(model.Session{Harness: "claude", ID: "sub1", Kind: model.KindSubagent, Parent: "root1"}) {
		t.Fatalf("isHandoverSession allowed spawned subagent")
	}
	if isHandoverSession(model.Session{Harness: "opencode", ID: "fork1", Kind: model.KindSubagentFork}) {
		t.Fatalf("isHandoverSession allowed subagent_fork session")
	}
	if !isHandoverSession(model.Session{Harness: "claude", ID: "root1"}) {
		t.Fatalf("isHandoverSession rejected normal root session")
	}

	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	recs := strings.Join([]string{
		`{"harness":"claude","session_id":"s-claude","project":"org/app","role":"user","text":"investigate build failure","time":"` + now + `"}`,
		`{"harness":"claude","session_id":"s-claude","project":"org/app","role":"assistant","text":"We decided to use -tags=integration for the build.","time":"` + now + `"}`,
		`{"harness":"codex","session_id":"s-codex","project":"org/app","role":"user","text":"codex session","time":"` + now + `"}`,
		`{"harness":"codex","session_id":"s-codex","project":"org/app","role":"assistant","text":"codex reply","time":"` + now + `"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-bbbb-1.jsonl"), []byte(recs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "sync", "import", batch); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	t.Setenv("DEJA_EXCLUDE_HARNESSES", "codex")

	recap, err := index.ScanRecap(dir, 24*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	if recap.Considered != 1 {
		t.Fatalf("ScanRecap.Considered with DEJA_EXCLUDE_HARNESSES=codex = %d; want 1", recap.Considered)
	}
	metas, err := index.RecentProjectMetasUnder(dir, []string{"org/app"}, "", time.Time{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Harness == "codex" {
			t.Fatalf("RecentProjectMetasUnder leaked excluded harness codex: %+v", m)
		}
	}
}

func TestDDDStageAWiringDomainSymmetryAndBugs32To36(t *testing.T) {
	// Every installTargetNames() entry must be covered by doctorMCPConfigs(),
	// autoWirings(), or one of the explicit non-table targets.
	mcpCovered := map[string]bool{
		"trae-ide": true, // added per installed IDE build in doctorMCPConfigs()
	}
	mcpDupes := map[string]func(string) []string{}
	for _, cfg := range doctorMCPConfigs() {
		mcpCovered[cfg.name] = true
		mcpDupes[cfg.name] = cfg.dupes
	}
	autoCovered := map[string]bool{
		"claude": true, "codex": true,
	}
	for _, a := range autoWirings() {
		autoCovered[a.name] = true
	}
	for _, target := range installTargetNames() {
		if target == "statusline" || target == "sync-timer" {
			continue
		}
		if name, ok := strings.CutSuffix(target, "-auto"); ok {
			if !autoCovered[name] {
				t.Fatalf("install auto target %q missing from doctor autoWirings()", target)
			}
			continue
		}
		if !mcpCovered[target] && !autoCovered[target] {
			t.Fatalf("install target %q missing from both doctorMCPConfigs() and autoWirings()", target)
		}
	}

	// Bug #32: mcpServerMaps must include Muse's legacy `mcp_servers` block when
	// `mcpServers` is absent so dejaCommandIn and dejaEntrySwitchedOff work.
	museRoot := map[string]any{
		"mcp_servers": map[string]any{
			"deja": map[string]any{
				"command":  "/usr/local/bin/deja",
				"args":     []any{"mcp"},
				"disabled": true,
			},
		},
	}
	if maps := mcpServerMaps(museRoot); len(maps) != 1 {
		t.Fatalf("mcpServerMaps(muse legacy mcp_servers) = %d maps; want 1", len(maps))
	}
	tmp := t.TempDir()
	museFile := filepath.Join(tmp, "muse.json")
	if err := os.WriteFile(museFile, []byte(`{"mcp_servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"],"disabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dejaEntrySwitchedOff("muse", museFile) {
		t.Fatalf("dejaEntrySwitchedOff missed disabled entry in Muse legacy mcp_servers")
	}
	if got := dejaCommandIn(museFile); got != "/usr/local/bin/deja" {
		t.Fatalf("dejaCommandIn(muse legacy mcp_servers) = %q; want /usr/local/bin/deja", got)
	}

	// Bug #33 & #34: openclaw and zcode recognize hand-named entries (e.g. "deja-vu")
	// and expose duplicate-key detection under `mcp.servers`.
	nestedFile := filepath.Join(tmp, "nested.json")
	if err := os.WriteFile(nestedFile, []byte(`{"mcp":{"servers":{"deja":{"command":"deja","args":["mcp"]},"deja-vu":{"command":"/opt/bin/deja","args":["mcp"]}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorOpenClawWired(nestedFile) || !doctorZCodeWired(nestedFile) {
		t.Fatalf("openclaw/zcode wired check failed on nested mcp.servers")
	}
	for _, name := range []string{"openclaw", "zcode"} {
		dupesFn := mcpDupes[name]
		if dupesFn == nil {
			t.Fatalf("doctorMCPConfigs()[%q].dupes is nil; want duplicate key detector", name)
		}
		if keys := dupesFn(nestedFile); len(keys) != 2 || keys[0] != "deja" || keys[1] != "deja-vu" {
			t.Fatalf("%s dupes = %v; want [deja deja-vu]", name, keys)
		}
	}

	// Bug #35: kilocode recognizes 2.x CLI `mcp.servers` in kilo.jsonc and
	// exposes duplicate-key detection across both shapes.
	if !doctorKilocodeWired(nestedFile) {
		t.Fatalf("doctorKilocodeWired failed on 2.x CLI mcp.servers shape")
	}
	if mcpDupes["kilocode"] == nil {
		t.Fatalf("doctorMCPConfigs()[kilocode].dupes is nil")
	}
	if keys := mcpDupes["kilocode"](nestedFile); len(keys) != 2 {
		t.Fatalf("kilocode dupes on 2.x CLI shape = %v; want [deja deja-vu]", keys)
	}

	// Bug #36: pi uses the shared skill path and retires its old local skill copy.
	if got, want := guidancePath("pi"), sharedSkillPath(); got != want {
		t.Fatalf("guidancePath(pi) = %q; want sharedSkillPath %q", got, want)
	}
}

// TestDDDStageBToDArchitectureInvariants enforces the Anti-Corruption Layer in
// internal/sources/ (Stage B) and the unified Sidecar State Machine in
// internal/index/ (Stage C & D) at the AST level so regressions fail `go test`.
func TestDDDStageBToDArchitectureInvariants(t *testing.T) {
	repoRoot := ".."
	if _, err := os.Stat(filepath.Join(repoRoot, "internal", "sources")); err != nil {
		repoRoot = filepath.Join("..", "..")
	}
	// 1. Stage B ACL Invariant: No parser in internal/sources/ may call
	// projectFromPaths or handoffProject ad-hoc outside project_paths.go and antigravity.go.
	sourcesDir := filepath.Join(repoRoot, "internal", "sources")
	srcEntries, err := os.ReadDir(sourcesDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", sourcesDir, err)
	}
	fset := token.NewFileSet()
	allowedDirectCallers := map[string]bool{
		"project_paths.go": true,
		"antigravity.go":   true,
	}
	aclCallers := 0
	for _, e := range srcEntries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(sourcesDir, name)
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("ParseFile(%s): %v", path, parseErr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if (ident.Name == "projectFromPaths" || ident.Name == "handoffProject") && !allowedDirectCallers[name] {
				t.Errorf("Stage B ACL violation: %s calls %s directly instead of resolveHomeOrEmptyProject or resolveDirProject", name, ident.Name)
			}
			if (ident.Name == "resolveHomeOrEmptyProject" || ident.Name == "resolveDirProject") && name != "project_paths.go" {
				aclCallers++
			}
			return true
		})
	}
	if aclCallers < 12 {
		t.Fatalf("expected at least 12 harness parser calls to resolveHomeOrEmptyProject/resolveDirProject, got %d", aclCallers)
	}

	// 2. Stage C Sidecar State Machine Invariant:
	// - Both in-place append paths (ingest_append.go and sync.go) must call refreshAppendedSidecars.
	// - Legacy full-log re-scanners buildCommandsFromIndex and buildSessionFactsFromIndex must not exist.
	indexDir := filepath.Join(repoRoot, "internal", "index")
	idxEntries, err := os.ReadDir(indexDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", indexDir, err)
	}
	refreshCallers := map[string]bool{}
	for _, e := range idxEntries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(indexDir, name)
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("ParseFile(%s): %v", path, parseErr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				if x.Name.Name == "buildCommandsFromIndex" || x.Name.Name == "buildSessionFactsFromIndex" {
					t.Errorf("Stage C violation: legacy full-log sidecar scanner %s still defined in %s", x.Name.Name, name)
				}
			case *ast.CallExpr:
				if ident, ok := x.Fun.(*ast.Ident); ok && ident.Name == "refreshAppendedSidecars" {
					refreshCallers[name] = true
				}
			}
			return true
		})
	}
	for _, want := range []string{"ingest_append.go", "sync.go"} {
		if !refreshCallers[want] {
			t.Errorf("Stage C violation: %s does not route in-place sidecar updates through refreshAppendedSidecars", want)
		}
	}
}
