package index

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// A rule written for a package has to reach an agent that asks by file: the
// agent about to edit cmd/reconcile/match.go recalls with that path, and the
// session that settled how cmd/reconcile changes are checked never names the
// file (#4762).
func TestFilePathRecallFindsTheRuleForItsDirectory(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-ledger")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("replay", "Rule: every change in cmd/reconcile is checked with the dry-run replay of the August bank file; expect 4117 matched.")
	write("cron", "The reconcile cron ran at 00:00 UTC but the bank file lands at 01:30 UTC.")
	write("pgx", "pgx v5.7.0 broke COPY of the NUMERIC amount columns in the bulk import.")
	write("readme", "Added a two-line README intro for ledgerd.")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"cmd/reconcile/match.go",
		"cmd/reconcile/match.go tolerance",
		"/home/dev/ledger/cmd/reconcile/match.go",
		`cmd\reconcile\match.go`, // already split into words, ranks on them
		"cmd/reconcile/match.go amount tolerance 0.005 0.01",
	} {
		r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Sessions) == 0 || r.Sessions[0].ID != "replay" {
			var ids []string
			for _, s := range r.Sessions {
				ids = append(ids, s.ID)
			}
			t.Errorf("%q: got %v (tier %q), want the cmd/reconcile rule first", q, ids, r.Tier)
		}
	}
	// The file itself named in history still answers on the exact tier.
	write("file", "Changed the tolerance in cmd/reconcile/match.go to 0.005.")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "cmd/reconcile/match.go", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 1 || r.Sessions[0].ID != "file" || r.Tier == query.TierRelevance {
		t.Errorf("exact file hit: tier %q, %d sessions", r.Tier, len(r.Sessions))
	}
}

func TestParentDirs(t *testing.T) {
	for q, want := range map[string][]string{
		"cmd/reconcile/match.go":                  {"cmd/reconcile"},
		"/home/dev/ledger/cmd/reconcile/match.go": {"home/dev/ledger/cmd/reconcile", "cmd/reconcile"},
		"README.md":           nil,
		"cmd/main.go":         nil, // "cmd" alone names half the corpus
		"cmd/reconcile":       nil, // a directory, not a file
		"pkg/index tolerance": nil,
	} {
		if got := parentDirs(RelevanceTerms(q)); !reflect.DeepEqual(got, want) {
			t.Errorf("parentDirs(%q) = %q, want %q", q, got, want)
		}
	}
}

// A long question that names a file is about more than the file. When a session
// already answers several of its other words, a session that merely mentions
// the file's directory must not be put ahead of it: on a real store a marathon
// that listed docs/registry once went to the top of a thirteen-word query whose
// answer had been second.
func TestDirectorySessionsDoNotJumpAnAnswerToTheRestOfTheQuery(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-site")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("review", "Review of the registry footer: the mobile table overflows and the grpfold toggle hides the CTA.")
	write("listing", "Listed the tree: docs/registry, docs/guide, cmd/deja, internal/index.")
	write("other", "Bumped the Go toolchain in CI.")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "docs/registry/deepseek.html footer mobile table grpfold CTA", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 || r.Sessions[0].ID != "review" {
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("got %v (directory %q), want the review that answers the rest of the query first", ids, r.Directory)
	}
	if r.Directory != "" {
		t.Errorf("answer labelled as about directory %q although the lead session is not", r.Directory)
	}
}

func TestAnswersTheRestCountsWordsNotSubstrings(t *testing.T) {
	ss := []model.Session{{Messages: []model.Message{{Text: "Forget the setting, the numerical drift is fine."}}}}
	if answersTheRest(ss, RelevanceTerms("cmd/x/y.go set numeric")) {
		t.Error(`"set" in "setting" and "numeric" in "numerical" counted as the query's words`)
	}
	ss[0].Messages[0].Text = "Set the numeric column default."
	if !answersTheRest(ss, RelevanceTerms("cmd/x/y.go set numeric")) {
		t.Error("two of the query's words, whole, were not counted")
	}
}
