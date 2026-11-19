package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/nfcfold"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func TestIdentifierParts(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"call getUserById now", []string{"get", "user"}}, // "by","id" < 3 runes
		{"refresh_token_rotation broke", []string{"refresh", "token", "rotation"}},
		{"JSONDataReader failed", []string{"json", "data", "reader"}},
		{"kebab-case-slug", []string{"kebab", "case", "slug"}},
		{"plain words only here", nil},
		{"short a_b", nil},
	}
	for _, tc := range cases {
		got := identifierParts(tc.in)
		sort.Strings(got)
		want := append([]string(nil), tc.want...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("identifierParts(%q) = %v, want %v", tc.in, got, want)
		}
	}
}

func TestIdentifierSplitRetrieval(t *testing.T) {
	dir := nlIndex(t,
		"the bug is in getUserProfile handler",
		"unrelated session about docker",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "user profile", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
		t.Fatalf("split retrieval missed camelCase: %+v", result.Sessions)
	}
}

func TestRetrievalKeysFetchesMoreCandidates(t *testing.T) {
	keys := []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7", "k8", "k9"}
	got := retrievalKeys(keys)
	if len(got) != 8 {
		t.Fatalf("retrievalKeys cap = %d, want 8", len(got))
	}
	short := []string{"a", "b"}
	if len(retrievalKeys(short)) != 2 {
		t.Fatal("short key lists must pass through")
	}
}

func TestWiderKeyCapShrinksCandidateSet(t *testing.T) {
	// 40 sessions share five long common words; one also has a rare short
	// token. The old 3-longest cap fetched only common postings (40
	// candidates for the scan); fetching the rare token's postings too
	// collapses the AND to 1 before any record is read.
	texts := make([]string, 0, 40)
	for range 39 {
		texts = append(texts, "performance regression investigation deployment pipeline filler")
	}
	texts = append(texts, "performance regression investigation deployment pipeline zzq")
	dir := nlIndex(t, texts...)

	keys := queryKeys("performance regression investigation deployment pipeline zzq")
	oldSel := keys
	if len(oldSel) > 3 {
		oldSel = oldSel[:3]
	}
	oldPosts, err := intersectPostings(dir, oldSel)
	if err != nil {
		t.Fatal(err)
	}
	newPosts, err := intersectPostings(dir, retrievalKeys(keys))
	if err != nil {
		t.Fatal(err)
	}
	if len(oldPosts) < 30 {
		t.Fatalf("fixture broken: old-cap candidates = %d, want ~40", len(oldPosts))
	}
	if len(newPosts) >= len(oldPosts)/10 {
		t.Fatalf("wide cap did not narrow candidates: old=%d new=%d", len(oldPosts), len(newPosts))
	}
}

// Rarity answers two questions and one number cannot serve both.
//
// "Is this term worth speaking up about" takes the rarer of the two verdicts —
// counted in sessions and counted in messages with each session capped — so a
// subject word does not read as filler merely because the marathons that hold
// most of a store all mention it.
//
// "What is this match worth" is the ranking weight, and there the capped-message
// view is wrong: it lifts every term a few long sessions happen to repeat and
// reorders the top of the result. Measured on LongMemEval-S, using the gate's
// number to weight the score cost 1.7 points of hit@1 (84.9 -> 83.2), with
// preference questions falling from 36.7% to 26.7%. Splitting them scores 85.3%
// while the gate keeps the recall it was introduced for.
func TestTheTwoRarityRolesStaySeparate(t *testing.T) {
	// A store of 1500 sessions; the term lives in 16 of them, and its messages
	// are concentrated enough that the capped-message view calls it much rarer.
	const sessions, minSess = 1500, 16
	totalDocs, minDF := 20000.0, 48.0

	rank := rankIDF(sessions, minSess)
	gate := gateIDF(totalDocs, int(minDF), rank)

	wantRank := math.Log(float64(sessions+1) / float64(minSess+1))
	if math.Abs(rank-wantRank) > 1e-9 {
		t.Errorf("rank weight = %.4f, want documents counted in sessions (%.4f)", rank, wantRank)
	}
	if gate <= rank {
		t.Errorf("the gate scores %.4f against the ranking's %.4f — it is not taking the rarer verdict", gate, rank)
	}

	// The other direction: a word that saturates one long session. Counted in
	// capped messages it looks common; the session count is what keeps it rare,
	// and the gate has to follow whichever says rare.
	saturating := gateIDF(400, 25, rankIDF(120, 1))
	if saturating < rankIDF(120, 1)-1e-9 {
		t.Errorf("a word filling one session scores %.4f, below its session verdict %.4f", saturating, rankIDF(120, 1))
	}

	// And the weight never inherits that: whatever the gate decides, the score
	// is the session view.
	if w := rankIDF(120, 1); math.Abs(w-math.Log(121.0/2.0)) > 1e-9 {
		t.Errorf("rank weight drifted: %.4f", w)
	}
}

// The prompt hook's rareness floor is min(4.0, TopIDF). It stays at 4.0 from 109
// sessions up, which covers the 3,512-session store P2 was replayed on, so
// lowering it for small stores cannot move that replay.
func TestTopIDFReachesTheFixedFloorAt109Sessions(t *testing.T) {
	const fixedFloor = 4.0 // search.identifierIDFFloor
	if got := topIDF(108); got >= fixedFloor {
		t.Errorf("108 sessions already reach the floor: %v", got)
	}
	for _, n := range []int{109, 3512} {
		if got := topIDF(n); got < fixedFloor {
			t.Errorf("%d sessions fall short of the floor: %v", n, got)
		}
	}
	if got := TopIDF(t.TempDir()); !math.IsInf(got, 1) {
		t.Errorf("a store that cannot be read gave a ceiling of %v; it must leave the floor alone", got)
	}
}

func TestNoteQuerySubjectSeenAndUnseen(t *testing.T) {
	sessions := make(map[string]SessionMeta, 120)
	for i := range 120 {
		sessions[string(rune(i+1))] = SessionMeta{}
	}
	acc := &relevanceAcc{
		m:     Manifest{Sessions: sessions},
		idfOf: map[string]float64{},
	}
	acc.noteQuerySubject("common", 10, true)
	if acc.queryBestSeen != 1 || acc.queryBestUnseen != 0 {
		t.Fatalf("after seen subject: seen=%d unseen=%d", acc.queryBestSeen, acc.queryBestUnseen)
	}
	acc.noteQuerySubject("common2", 10, false)
	if acc.queryBestSeen != 1 || acc.queryBestUnseen != 1 {
		t.Fatalf("after tied unseen subject: seen=%d unseen=%d", acc.queryBestSeen, acc.queryBestUnseen)
	}
	acc.noteQuerySubject("common3", 10, true)
	if acc.queryBestSeen != 2 || acc.queryBestUnseen != 1 {
		t.Fatalf("after tied seen subject: seen=%d unseen=%d", acc.queryBestSeen, acc.queryBestUnseen)
	}
	acc.noteQuerySubject("pgbouncer", 1, false)
	if acc.queryBestSeen != 0 || acc.queryBestUnseen != 1 {
		t.Fatalf("after rarer unseen subject: seen=%d unseen=%d", acc.queryBestSeen, acc.queryBestUnseen)
	}
}

// A directory the trust policy says is not to be recalled must be unreachable
// from every surface, not from the one that happens to filter. `WithoutIgnored`
// was called at a single call site — the CLI's own search — so `deja doctor`
// printed "not recalled */.claude/jobs/*" while the per-prompt hook injected
// those sessions into every message.
func TestIgnoredSessionsAreOutOfReachEverywhere(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "home", ".claude", "jobs", "abc", "projects")
	proj := filepath.Join(root, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string) string {
		return `{"type":"` + role + `","sessionId":"scratch","cwd":"/w/app",` +
			`"timestamp":"2026-01-02T03:04:05Z","message":{"role":"` + role +
			`","content":"` + text + `"}}`
	}
	if err := os.WriteFile(filepath.Join(proj, "scratch.jsonl"),
		[]byte(strings.Join([]string{
			line("user", "the frobnicator keeps dropping its widgets under load"),
			line("assistant", "Decision: we pinned the frobnicator to one shard."),
		}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The scoring path, which is what `deja search` walks.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "frobnicator", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 0 {
		t.Errorf("an ignored session came back from search: %d", len(r.Sessions))
	}

	// The relevance path, which is what a sentence-shaped question reaches.
	r, err = SearchWithRecoveryDetailed(dir,
		query.Options{Query: "why does the frobnicator drop widgets under load", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 0 {
		t.Errorf("an ignored session came back from the relevance tier: %d", len(r.Sessions))
	}

	// The per-prompt hook's own ranking, which is the most automatic surface
	// deja has and the one this rule most has to hold on.
	ss, _, _, _, err := ProjectRelevantSkipping(dir, []string{"w/app", "app"},
		[]string{"frobnicator", "widgets"}, 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 0 {
		t.Errorf("an ignored session was ranked for injection: %d", len(ss))
	}
}

// The per-term counts and the withheld count are both read off the postings,
// which hold every session — including the ones the ignore rule keeps out of
// every answer (#2562).
func TestTermCountsAndWithheldCountFollowTheIgnoreRule(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "claude")
	jobs := filepath.Join(root, ".claude", "jobs", "abc", "-w-app")
	realPath := filepath.Join(root, "-w-app")
	for _, d := range []string{jobs, realPath} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, sid, text string) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/app","timestamp":"2026-01-02T03:04:05Z",` +
			`"message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(jobs, "scratch1", "why did the quokka telemetry sharding keep failing")
	write(jobs, "scratch2", "the quokka telemetry sharding again")
	write(realPath, "keeper", "the invoice renderer lost its footer")

	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	terms := []string{"quokka", "telemetry", "sharding"}
	counts := TermSessionCounts(dir, terms)
	for _, term := range terms {
		if counts[term] != 0 {
			t.Errorf("%q counted in %d sessions search cannot serve", term, counts[term])
		}
	}
	// The words of a session that is served are still counted.
	if got := TermSessionCounts(dir, []string{"invoice"}); got["invoice"] != 1 {
		t.Errorf("invoice counted %d times, want 1", got["invoice"])
	}
	if n := IgnoredWithAllTerms(dir, terms); n != 2 {
		t.Errorf("withheld = %d, want the two sessions that hold every term", n)
	}
	// Nothing withheld for a query the rule does not touch, and nothing for a
	// query the store has never seen.
	if n := IgnoredWithAllTerms(dir, []string{"invoice", "renderer"}); n != 0 {
		t.Errorf("withheld = %d for a served answer", n)
	}
	if n := IgnoredWithAllTerms(dir, []string{"zzzqqq"}); n != 0 {
		t.Errorf("withheld = %d for a word nobody wrote", n)
	}
	if n := IgnoredWithAllTerms(dir, nil); n != 0 {
		t.Errorf("withheld = %d for no terms at all", n)
	}
	// An unreadable store answers "nothing withheld" rather than failing the
	// command it decorates.
	if n := IgnoredWithAllTerms(filepath.Join(tmp, "nowhere"), terms); n != 0 {
		t.Errorf("withheld = %d with no index", n)
	}
	if got := servableSids(filepath.Join(tmp, "nowhere")); got != nil {
		t.Errorf("servableSids on a missing index = %v, want nil so counts stand", got)
	}
	if strings.TrimSpace(dir) == "" {
		t.Fatal("unreachable")
	}
}

// The withheld count (#2562) intersects raw term postings, while the tiers
// stem: a query typed as "pool" is answered by a session that says "pools".
// Measured over 22 real queries the two agreed everywhere, but the case is
// constructible, and there the line goes quiet exactly when it is needed —
// search serves nothing, the rule is why, and nobody says so (#2573).
func TestTheWithheldCountFollowsTheWordFormsSearchDoes(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	root := filepath.Join(home, "claude")
	jobs := filepath.Join(root, ".claude", "jobs", "abc", "-w-app")
	realPath := filepath.Join(root, "-w-app")
	for _, d := range []string{jobs, realPath} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(dir, sid, text string) {
		body := `{"type":"user","sessionId":"` + sid + `","cwd":"/w/app","timestamp":"2026-01-02T03:04:05Z",` +
			`"message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The withheld session writes the plural; the reader types the singular.
	write(jobs, "scratch", "the quokka pools kept starving under load")
	write(realPath, "keeper", "the invoice renderer lost its footer")

	setHome(t, home)
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: this store really does know the plural as another form.
	if forms := OtherWordForms(dir, []string{"pool"}); len(forms["pool"]) == 0 {
		t.Fatal("the store knows no other form of \"pool\", so this measures nothing")
	}
	if n := IgnoredWithAllTerms(dir, []string{"quokka", "pool", "starving"}); n != 1 {
		t.Errorf("withheld = %d, want the session that says \"pools\"", n)
	}
	// The exact spelling still counts once, not twice.
	if n := IgnoredWithAllTerms(dir, []string{"quokka", "pools", "starving"}); n != 1 {
		t.Errorf("withheld = %d for the exact spelling", n)
	}
	// And a query the rule does not touch stays at zero.
	if n := IgnoredWithAllTerms(dir, []string{"invoice", "renderer"}); n != 0 {
		t.Errorf("withheld = %d for a served answer", n)
	}
}

// The commands table keys by project while the ignore rule matches on the
// session's path, so the callers that read that table cannot apply the rule
// exactly. This names the projects it touches at all, which is the safe
// direction for them (#2652).
func TestProjectsTouchedByIgnore(t *testing.T) {
	dir := halvedStore(t)
	touched := ProjectsTouchedByIgnore(dir)
	if !touched["w/scratch"] {
		t.Fatalf("the ignored project is not named: %v", touched)
	}
	if touched["w/keep"] {
		t.Fatalf("a project the rule does not touch was named: %v", touched)
	}
	if len(touched) != 1 {
		t.Fatalf("one project is covered by this rule, got %v", touched)
	}
}

// A store with no rule in force names nothing, and a missing store answers
// nothing rather than failing the caller.
func TestProjectsTouchedByIgnoreOnAStoreWithoutOne(t *testing.T) {
	if got := ProjectsTouchedByIgnore(t.TempDir()); len(got) != 0 {
		t.Fatalf("an unreadable store should name no projects, got %v", got)
	}
}

func TestFixesForChecksSessionIgnoreAndTabSeparatedFailLine(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	when := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	writeStore(t, dir, []model.Session{
		{
			ID: "job1", Harness: "claude", Project: "w/keep",
			Path: "/home/u/.claude/jobs/j1/session.jsonl", Updated: when,
			Messages: []model.Message{
				{Role: roleToolOutput, Text: "FAIL\texample.com/pkg/sub [build failed]", Time: when},
				{Role: roleCommand, Text: "go build ./pkg/sub", Time: when.Add(time.Minute)},
			},
		},
		{
			ID: "k1", Harness: "claude", Project: "w/keep",
			Path: "/home/u/.claude/projects/-w-keep/k1.jsonl", Updated: when.Add(2 * time.Minute),
			Messages: []model.Message{
				{Role: roleToolOutput, Text: "FAIL\texample.com/pkg/main [build failed]", Time: when},
				{Role: roleCommand, Text: "go mod tidy && go build ./pkg/main", Time: when.Add(time.Minute)},
			},
		},
		{
			ID: "k2", Harness: "claude", Project: "w/keep",
			Path: "/home/u/.claude/projects/-w-keep/k2.jsonl", Updated: when.Add(3 * time.Minute),
			Messages: []model.Message{
				{Role: roleToolOutput, Text: "FAIL\texample.com/pkg/main [build failed]", Time: when},
				{Role: roleCommand, Text: "go mod tidy && go build ./pkg/main", Time: when.Add(time.Minute)},
			},
		},
	})
	if got := FixesFor(dir, "FAIL example.com/pkg/main [build failed]", 3, nil); len(got) != 1 {
		t.Fatalf("FixesFor on non-ignored session in mixed project = %+v, want 1 fix", got)
	}
	if got := FixesFor(dir, "FAIL example.com/pkg/sub [build failed]", 3, nil); len(got) != 0 {
		t.Fatalf("FixesFor served an ignored job session: %+v", got)
	}
}

// writeNoteBatch drops a one-record sync batch holding a retracted note.
func writeNoteBatch(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(SyncRecord{
		Harness: "deja", SessionID: "deja-note-claude-a18", Project: "p", Role: "user",
		Text: "[rejected] we no longer invalidate on write (from claude:a18, 2026-08-04)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func importedNoteMeta(t *testing.T, dir string) SessionMeta {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.Harness == "deja" {
			return m
		}
	}
	t.Fatal("no imported note in the index")
	return SessionMeta{}
}

// A rebuild reloads imported sessions out of the index, since no source file
// holds them — and rebuilt the manifest row from scratch, dropping the state
// the import had recorded. The batch is deduped, so nothing brings it back
// (#1049).
func TestImportedNoteStateSurvivesRebuild(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	writeNoteBatch(t, exp)
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	meta := importedNoteMeta(t, dir)
	if meta.Lifecycle != "rejected" {
		t.Errorf("the rebuild dropped the retraction: lifecycle = %q", meta.Lifecycle)
	}
	if meta.OrigID != "deja-note-claude-a18" {
		t.Errorf("the rebuild dropped what the note was: OrigID = %q", meta.OrigID)
	}
	if !IsPromotedNote(meta.Harness, meta.ID, meta.OrigID) {
		t.Errorf("after the rebuild the note is no longer recognised as one: %s", meta.ID)
	}
}

// #984 began recording the state on the manifest row without bumping the index
// format, so a store that imported before it holds a row with none. The state
// is in the note text, and a rebuild re-derives it (#1049).
func TestImportedNoteStateDerivedFromTextWhenRowHasNone(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	writeNoteBatch(t, exp)
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}
	// What a pre-#984 import left behind: the records are there, the row knows
	// nothing about them.
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	for k, meta := range m.Sessions {
		meta.OrigID, meta.Lifecycle, meta.LifecycleNote, meta.LifecycleAt = "", "", "", ""
		m.Sessions[k] = meta
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if got := importedNoteMeta(t, dir).Lifecycle; got != "" {
		t.Fatalf("the row still carries a state, the probe proves nothing: %q", got)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	meta := importedNoteMeta(t, dir)
	if meta.Lifecycle != "rejected" {
		t.Errorf("the retraction was not re-derived: lifecycle = %q", meta.Lifecycle)
	}
	// Without the loader's "(from …, day)" tail, as the note reads where it was
	// filed (#5053).
	if meta.LifecycleNote != "we no longer invalidate on write" {
		t.Errorf("the correction text was not re-derived: %q", meta.LifecycleNote)
	}
}

// The states of promoted notes live in notes.jsonl, which never travels, and
// import renames every session to imported-<hash>. So a decision retracted on
// one machine arrived on the other as an accepted line with the newest
// correction last (#975).
func TestImportedNoteKeepsItsStateAndOrder(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))

	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch []byte
	for _, r := range []SyncRecord{
		{Harness: "deja", SessionID: "deja-note-claude-a18", Project: "p", Role: "user", Text: "[accepted] asked: raise the pool cap · outcome: raised (from claude:a18, 2026-08-04)"},
		{Harness: "deja", SessionID: "deja-note-claude-a18", Project: "p", Role: "user", Text: "[rejected] made it worse (from claude:a18, 2026-08-04)"},
	} {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, append(b, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), batch, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if _, err := Import(dir, exp); err != nil {
		t.Fatal(err)
	}

	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range metas {
		if m.Harness != "deja" {
			continue
		}
		found = true
		if m.OrigID != "deja-note-claude-a18" {
			t.Errorf("the imported note forgot what it was: OrigID = %q", m.OrigID)
		}
		if m.Lifecycle != "rejected" {
			t.Errorf("the retraction did not travel: lifecycle = %q", m.Lifecycle)
		}
		if !IsPromotedNote(m.Harness, m.ID, m.OrigID) {
			t.Errorf("an imported promoted note is not recognised as one: %s", m.ID)
		}
		s, ok, err := FindByPrefix(dir, m.ID)
		if err != nil || !ok {
			t.Fatalf("cannot read the imported note back: %v", err)
		}
		if len(s.Messages) != 2 {
			t.Fatalf("got %d messages, want 2", len(s.Messages))
		}
		if got := s.Messages[0].Text; got[:10] != "[rejected]" {
			t.Errorf("the note leads with %q, not the newest correction", got)
		}
	}
	if !found {
		t.Fatal("no imported note in the index")
	}
}

// The state prefix is the only copy that crosses a machine boundary, so it has
// to be read exactly: anything that is not one of deja's states is text, not a
// state (#975).
func TestNoteStateFromText(t *testing.T) {
	for _, tc := range []struct {
		in, state, note string
		ok              bool
	}{
		{"[rejected] made it worse", "rejected", "made it worse", true},
		{"[accepted] asked: … · outcome: …", "accepted", "asked: … · outcome: …", true},
		{"[superseded] replaced by the other one", "superseded", "replaced by the other one", true},
		{"[stale]", "stale", "", true},
		{"[approved-by-ceo] not a state deja knows", "", "", false},
		{"no bracket at all", "", "", false},
		{"[]", "", "", false},
		{"[unterminated", "", "", false},
	} {
		state, note, ok := noteStateFromText(tc.in)
		if state != tc.state || note != tc.note || ok != tc.ok {
			t.Errorf("noteStateFromText(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.in, state, note, ok, tc.state, tc.note, tc.ok)
		}
	}
}

func TestIndexIngestSkipAndSearch(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"fast opencode needle"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"answer text"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	var first bytes.Buffer
	if err := Ensure(dir, "claude", false, &first); err != nil {
		t.Fatal(err)
	}
	if first.Len() == 0 {
		t.Fatal("first build did not print progress")
	}
	if !HasManifest(dir) {
		t.Fatal("manifest missing after build")
	}
	projectSessions, err := RecentProject(dir, "deja/vu", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectSessions) != 1 || projectSessions[0].ID != "s1" || len(projectSessions[0].Messages) != 2 {
		t.Fatalf("bad project sessions: %#v", projectSessions)
	}
	filteredRecent, err := RecentMatching(dir, 2, search.Options{Harness: "claude", Project: "deja"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredRecent) != 1 || filteredRecent[0].ID != "s1" {
		t.Fatalf("bad filtered recent sessions: %#v", filteredRecent)
	}
	filteredRecent, err = RecentMatching(dir, 2, search.Options{Harness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredRecent) != 0 {
		t.Fatalf("unexpected filtered recent sessions: %#v", filteredRecent)
	}
	var second bytes.Buffer
	if err = Ensure(dir, "claude", false, &second); err != nil {
		t.Fatal(err)
	}
	if second.Len() != 0 {
		t.Fatalf("fresh index rebuilt unexpectedly: %q", second.String())
	}
	ss, err := Search(dir, search.Options{Query: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" || ss[0].Messages[0].Text != "fast opencode needle" {
		t.Fatalf("bad search sessions: %#v", ss)
	}
}

func TestReadRecordsAndGeneration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	want := Record{Key: "claude:s", SourcePath: "session.jsonl", Role: "user", Text: "semantic text", Time: time.Unix(10, 20)}
	wtbl := newRecordTables()
	off, err := writeRecord(f, want, wtbl)
	if err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if off != 0 || f.Close() != nil {
		t.Fatal("record write did not start at zero")
	}
	if err = writeManifest(dir, Manifest{Version: version, Sessions: map[string]SessionMeta{}, RecordStrings: wtbl.strs}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecords(dir)
	if err != nil || len(got) != 1 || got[0].Offset != 0 || got[0].Record.Text != want.Text || got[0].Record.Time != want.Time {
		t.Fatalf("records=%#v err=%v", got, err)
	}
	if got[0].Record.Key != want.Key || got[0].Record.SourcePath != want.SourcePath {
		t.Fatalf("interned fields lost: key=%q path=%q", got[0].Record.Key, got[0].Record.SourcePath)
	}
	if err := writeManifest(dir, Manifest{Version: version, BuiltAt: time.Unix(20, 0), Generation: "gen-1", Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	// The stamp plus what decides whether offsets moved: records.bin's length.
	if got, err := Generation(dir); err != nil || !strings.HasPrefix(got, "gen-1+") {
		t.Fatalf("explicit generation=%q err=%v", got, err)
	}
	if err := writeManifest(dir, Manifest{Version: version, BuiltAt: time.Unix(30, 0), Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if got, err := Generation(dir); err != nil || !strings.HasPrefix(got, "1970-01-01T00:00:30Z+") {
		t.Fatalf("built-at generation=%q err=%v", got, err)
	}
	if _, err := ReadRecords(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing records should fail")
	}
	if err := os.WriteFile(filepath.Join(dir, "records.bin"), []byte{4, 0, 0, 0, 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecords(dir); err == nil {
		t.Fatal("truncated record should fail")
	}
}

func TestManifestBuiltAt(t *testing.T) {
	dir := t.TempDir()
	if got := ManifestBuiltAt(dir); !got.IsZero() {
		t.Fatalf("missing manifest = %v, want zero", got)
	}
	builtAt := time.Unix(42, 0)
	mtime := time.Unix(99, 0)
	if err := writeManifest(dir, Manifest{Version: version, BuiltAt: builtAt, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if got := ManifestBuiltAt(dir); !got.Equal(builtAt) {
		t.Fatalf("BuiltAt = %v, want %v", got, builtAt)
	}
	if err := writeManifest(dir, Manifest{Version: version, Sessions: map[string]SessionMeta{}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if got := ManifestBuiltAt(dir); !got.Equal(mtime) {
		t.Fatalf("zero BuiltAt fallback = %v, want mtime %v", got, mtime)
	}
}

func TestSyncExportImportSearchIdempotent(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-sync")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"type":"user","sessionId":"sync1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"syncneedle question"}}` + "\n" +
		`{"type":"assistant","sessionId":"sync1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"syncneedle answer"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "sync1.jsonl"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dirA := filepath.Join(tmp, "a.db")
	if err := EnsureForSearch(dirA, search.Options{Query: "syncneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	batchDir := filepath.Join(tmp, "batches")
	n, err := Export(dirA, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("exported %d, want 2", n)
	}
	// A second export may resend the records sitting exactly on the
	// watermark; what must hold is that importing twice changes nothing.
	if _, err = Export(dirA, batchDir); err != nil {
		t.Fatal(err)
	}
	dirB := filepath.Join(tmp, "b.db")
	imported, err := Import(dirB, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if imported != 2 {
		t.Fatalf("imported %d, want 2", imported)
	}
	ss, err := Search(dirB, search.Options{Query: "syncneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Harness != "claude" || !strings.HasPrefix(ss[0].Project, "imported:") || len(ss[0].Messages) != 2 {
		t.Fatalf("bad imported search result: %#v", ss)
	}
	recent, err := Recent(dirB, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Harness != "claude" {
		t.Fatalf("bad recent imported session: %#v", recent)
	}
	found, ok, err := FindByPrefix(dirB, recent[0].ID[:12])
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(found.Messages) != 2 {
		t.Fatalf("FindByPrefix imported ok=%v found=%#v", ok, found)
	}
	if imported, err = Import(dirB, batchDir); err != nil {
		t.Fatal(err)
	} else if imported != 0 {
		t.Fatalf("re-import added %d records", imported)
	}
	ss, err = Search(dirB, search.Options{Query: "syncneedle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("re-import duplicated records: %#v", ss)
	}
}

func TestSyncImportBadJSONAndEmptyExport(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "idx.db")
	if err := initEmptyIndex(dir); err != nil {
		t.Fatal(err)
	}
	n, err := Export(dir, filepath.Join(tmp, "out"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("empty export=%d", n)
	}
	badDir := filepath.Join(tmp, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "bad.jsonl"), []byte("{bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(filepath.Join(tmp, "new.db"), badDir); err == nil || !strings.Contains(err.Error(), "bad.jsonl") {
		t.Fatalf("bad import err=%v", err)
	}
	if got := ImportedSessionID("claude", "abc"); !strings.HasPrefix(got, "imported-") {
		t.Fatalf("ImportedSessionID=%q", got)
	}
}

func TestMultiWordSearchUsesAllPostingsAndDoesNotFullScan(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"s1": "token comes before jwt and refresh later",
		"s2": "jwt only",
		"s3": "refresh token only",
	}
	for id, text := range fixtures {
		data := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":%q}}`+"\n", id, text)
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "jwt refresh token", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "jwt refresh token", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(ss, search.Options{Query: "jwt refresh token", Harness: "claude", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "s1" {
		t.Fatalf("multi-word AND failed: sessions=%#v hits=%#v", ss, hits)
	}

	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Sessions["claude:unposted"] = SessionMeta{ID: "unposted", Harness: "claude", Project: "deja/vu", Path: "manual", Updated: time.Now()}
	rec, err := os.OpenFile(filepath.Join(dir, "records.bin"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	utbl := mustTables(t, dir)
	_, err = writeRecord(rec, Record{Key: "claude:unposted", Role: "user", Text: "jwt only refresh would appear during a full scan", Time: time.Now()}, utbl)
	m.RecordStrings = utbl.strs
	if closeErr := rec.Close(); err != nil {
		t.Fatal(err)
	} else if closeErr != nil {
		t.Fatal(closeErr)
	}
	// Manifest committed after the append: the size stamp covers the record,
	// but no posting points at it — the full-scan bait stays in place.
	if err = writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(dir, search.Options{Query: "jwt only refresh", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	// The stem fallback may drop a token and return posted sessions at the
	// close tier; the invariant this test guards is narrower — a record with
	// no postings must never surface, because that would mean a full scan.
	for _, s := range ss {
		if s.ID == "unposted" {
			t.Fatalf("search fell back to full scan despite indexed query tokens: %#v", ss)
		}
	}
}

func TestIncrementalOnlyReingestsChangedFile(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1 := filepath.Join(proj, "s1.jsonl")
	s2 := filepath.Join(proj, "s2.jsonl")
	if err := os.WriteFile(s1, []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"alpha needle"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s2, []byte(`{"type":"user","sessionId":"s2","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"beta stable"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s1, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:06:05Z","message":{"role":"assistant","content":"gamma appended"}}` + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(s1, now, now)
	var log bytes.Buffer
	if err = Ensure(dir, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "deja: updated 1 file (1 new message)") {
		t.Fatalf("incremental log missing partial ingest line: %q", log.String())
	}
	if strings.Contains(log.String(), "indexing sessions") {
		t.Fatalf("incremental path printed scary full-index line: %q", log.String())
	}
	if lastIngestFiles.Load() != 1 {
		t.Fatalf("incremental ingest touched %d files, want 1", lastIngestFiles.Load())
	}
	ss, err := Search(dir, search.Options{Query: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s2" {
		t.Fatalf("unchanged file was not preserved: %#v", ss)
	}
	ss, err = Search(dir, search.Options{Query: "appended"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("changed file was not reingested: %#v", ss)
	}
}

func TestIncrementalAppendOneFileBenchmarkStyle(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-large")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	const fileCount = 30
	for i := range fileCount {
		p := filepath.Join(proj, fmt.Sprintf("s%02d.jsonl", i))
		var b strings.Builder
		for j := range 30 {
			fmt.Fprintf(&b, `{"type":"user","sessionId":"s%02d","timestamp":"2026-01-02T03:%02d:05Z","message":{"role":"user","content":"fixture stable-%02d message-%02d"}}`+"\n", i, j%60, i, j)
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "stable", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if lastIngestFiles.Load() != int64(fileCount) {
		t.Fatalf("full ingest touched %d files, want %d", lastIngestFiles.Load(), fileCount)
	}
	changed := filepath.Join(proj, "s17.jsonl")
	f, err := os.OpenFile(changed, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		fmt.Fprintf(f, `{"type":"assistant","sessionId":"s17","timestamp":"2026-01-02T04:%02d:05Z","message":{"role":"assistant","content":"one-file incremental-needle new-%02d"}}`+"\n", i, i)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(changed, now, now)
	var log bytes.Buffer
	if err = EnsureForSearch(dir, search.Options{Query: "incremental-needle", Harness: "claude"}, false, &log); err != nil {
		t.Fatal(err)
	}
	if lastIngestFiles.Load() != 1 {
		t.Fatalf("incremental ingest touched %d files, want exactly 1", lastIngestFiles.Load())
	}
	if got, want := log.String(), "deja: updated 1 file (12 new messages)"; !strings.Contains(got, want) {
		t.Fatalf("incremental log = %q, want %q", got, want)
	}
	if strings.Contains(log.String(), "indexing sessions") {
		t.Fatalf("incremental path printed full-index line: %q", log.String())
	}
	ss, err := Search(dir, search.Options{Query: "incremental-needle", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Run(ss, search.Options{Query: "incremental-needle", Harness: "claude", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "s17" || hits[0].Count != 12 {
		t.Fatalf("bad incremental search hits: %#v", hits)
	}
}

func TestIndexRecentFindRecordsAndBranches(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	idx := filepath.Join(tmp, "custom-index")
	t.Setenv("DEJA_INDEX_DIR", idx)
	if DefaultDir() != idx {
		t.Fatalf("DefaultDir env ignored")
	}
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(proj, "s1.jsonl")
	line1 := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"alpha needle"}}` + "\n"
	line2 := `{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"beta answer"}}` + "\n"
	if err := os.WriteFile(p, []byte(line1+line2), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	if err := Ensure(idx, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := Recent(idx, 1); err != nil || len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("Recent=%#v err=%v", got, err)
	}
	if got, ok, err := FindByPrefix(idx, "s"); err != nil || !ok || len(got.Messages) != 2 {
		t.Fatalf("FindByPrefix=%#v ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := FindByIdentity(idx, "claude", "s1"); err != nil || !ok || len(got.Messages) != 2 {
		t.Fatalf("FindByIdentity=%#v ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := FindByIdentity(idx, "codex", "s1"); err != nil || ok || got.ID != "" {
		t.Fatalf("FindByIdentity missing=%#v ok=%v err=%v", got, ok, err)
	}
	if got, ok, err := FindByIdentity("", "claude", "s1"); err != nil || !ok || got.ID != "s1" {
		t.Fatalf("FindByIdentity default=%#v ok=%v err=%v", got, ok, err)
	}
	recs, err := recordsForKey(filepath.Join(idx, "records.bin"), mustTables(t, idx), "claude:s1")
	if err != nil || len(recs) != 2 {
		t.Fatalf("records=%#v err=%v", recs, err)
	}
	if got, err := parseAppendedFile("", p, FileState{Size: int64(len(line1))}, false); err != nil || len(got) != 1 || got[0].Messages[0].Text != "beta answer" {
		t.Fatalf("parse appended=%#v err=%v", got, err)
	}
	if got, err := parseChangedFile("", p, FileState{}); err != nil || len(got) != 1 {
		t.Fatalf("parse changed=%#v err=%v", got, err)
	}
	if !strings.HasPrefix(bucket("a"), "x") || bucket("ab") != "ab" {
		t.Fatalf("short bucket failed")
	}

	// Shrinking a file forces the non-append replacement path.
	if err := os.WriteFile(p, []byte(line1), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, time.Now().Add(3*time.Second), time.Now().Add(3*time.Second))
	var log bytes.Buffer
	if err := Ensure(idx, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), replacementPassMarker) {
		t.Fatalf("want non-append incremental log, got %q", log.String())
	}
	if got, ok, err := FindByPrefix(idx, "s1"); err != nil || !ok || len(got.Messages) != 1 {
		t.Fatalf("after shrink=%#v ok=%v err=%v", got, ok, err)
	}

	// Removing the file also takes the rebuild/replacement path and drops records.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(proj, time.Now().Add(4*time.Second), time.Now().Add(4*time.Second))
	log.Reset()
	if err := Ensure(idx, "claude", false, &log); err != nil {
		t.Fatal(err)
	}
	// The file is gone and its directory is not: that is the client's
	// cleanup, and the session is kept on purpose (#2970).
	if !strings.Contains(log.String(), "still searchable") {
		t.Fatalf("want the kept-transcript line, got %q", log.String())
	}
	if got, err := Recent(idx, 10); err != nil || len(got) != 1 {
		t.Fatalf("recent after remove=%#v err=%v", got, err)
	}
}

func TestSetOpencodeLastUpdated(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "opencode.db")
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_OPENCODE_DB", db)
	files := map[string]FileState{db: {Path: db}}
	now := time.Now()
	setDatabaseStoreWatermarks(files, map[string]SessionMeta{"opencode:s": {Harness: "opencode", Updated: now}})
	if files[db].LastUpdated != now.UnixNano() {
		t.Fatalf("LastUpdated=%d", files[db].LastUpdated)
	}
}

func TestIndexHelperBranchCoverage(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_INDEX_DIR", "")
	if !strings.Contains(DefaultDir(), filepath.Join(".cache", "deja", "index.db")) {
		t.Fatalf("DefaultDir default=%q", DefaultDir())
	}
	if title := sessionTitle(modelSession("<local-command>x", "Caveat: y", "real title here")); title != "real title here" {
		t.Fatalf("sessionTitle=%q", title)
	}
	meta := SessionMeta{Harness: "claude", Project: "deja-vu", Updated: time.Now()}
	if !sessionMetaMatches(meta, search.Options{Harness: "claude", Project: "deja", Since: time.Hour}) || sessionMetaMatches(meta, search.Options{Harness: "codex"}) || sessionMetaMatches(meta, search.Options{Project: "missing"}) || sessionMetaMatches(SessionMeta{Updated: time.Now().Add(-48 * time.Hour)}, search.Options{Since: time.Hour}) {
		t.Fatalf("sessionMetaMatches branches failed")
	}

	codexRoot := filepath.Join(tmp, "codex")
	t.Setenv("DEJA_CODEX_ROOT", codexRoot)
	hist := filepath.Join(codexRoot, "history.jsonl")
	if err := os.MkdirAll(filepath.Dir(hist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hist, []byte(`{"session_id":"h","text":"history","ts":"2026-01-02T03:04:05Z"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	roll := filepath.Join(codexRoot, "sessions", "2026", "01", "02", "rollout-2026-01-02T03-04-05-r.jsonl")
	if err := os.MkdirAll(filepath.Dir(roll), 0o755); err != nil {
		t.Fatal(err)
	}
	rollLine := `{"timestamp":"2026-01-02T03:04:05Z","payload":{"role":"user","message":"roll msg"}}` + "\n"
	if err := os.WriteFile(roll, []byte(rollLine), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{hist, roll} {
		if got, err := parseChangedFile("", p, FileState{}); err != nil || len(got) != 1 {
			t.Fatalf("parseChanged %s=%#v %v", p, got, err)
		}
		if got, err := parseAppendedFile("", p, FileState{Size: 0}, false); err != nil || len(got) != 1 {
			t.Fatalf("parseAppended %s=%#v %v", p, got, err)
		}
	}
	db := filepath.Join(tmp, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	if got, err := parseChangedFile("", db, FileState{LastUpdated: time.Now().UnixNano()}); err != nil || got != nil {
		t.Fatalf("opencode changed missing=%#v %v", got, err)
	}
	if got, err := parseAppendedFile("", db, FileState{}, false); err != nil || got != nil {
		t.Fatalf("opencode appended missing=%#v %v", got, err)
	}
	if got, err := parseChangedFile("", filepath.Join(tmp, "unknown.txt"), FileState{}); err != nil || got != nil {
		t.Fatalf("unknown=%#v %v", got, err)
	}
}

func modelSession(texts ...string) model.Session {
	s := model.Session{Harness: "claude", ID: "s"}
	for _, txt := range texts {
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: txt})
	}
	return s
}

func BenchmarkColdEnsureSynthetic(b *testing.B) {
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-synthetic")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	const fileCount = 200
	const messagesPerFile = 200
	for i := range fileCount {
		p := filepath.Join(proj, fmt.Sprintf("s%03d.jsonl", i))
		var sb strings.Builder
		for j := range messagesPerFile {
			role := "user"
			if j%2 == 1 {
				role = "assistant"
			}
			fmt.Fprintf(&sb, `{"type":%q,"sessionId":"s%03d","timestamp":"2026-01-02T03:%02d:%02dZ","message":{"role":%q,"content":"synthetic cold index token file-%03d msg-%03d shared needle alpha beta gamma delta repeated words for tokenizer throughput"}}`+"\n", role, i, (j/60)%60, j%60, role, i, j)
		}
		if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		dir := filepath.Join(tmp, fmt.Sprintf("index-%d.db", i))
		start := time.Now()
		if err := Ensure(dir, "claude", false, nil); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(start).Milliseconds()), "ensure_ms")
	}
}

func BenchmarkWarmSearchSynthetic(b *testing.B) {
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-warm")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	const fileCount = 1250
	const messagesPerFile = 12
	for i := range fileCount {
		p := filepath.Join(proj, fmt.Sprintf("s%04d.jsonl", i))
		var sb strings.Builder
		for j := range messagesPerFile {
			role := "user"
			if j%2 == 1 {
				role = "assistant"
			}
			needle := "common filler"
			if i%125 == 0 && j == 3 {
				needle = "warm-single-digit-needle"
			}
			fmt.Fprintf(&sb, `{"type":%q,"sessionId":"s%04d","timestamp":"2026-01-02T03:%02d:%02dZ","message":{"role":%q,"content":"synthetic warm search corpus file-%04d msg-%02d %s alpha beta gamma delta repeated tokenizer words"}}`+"\n", role, i, (j/60)%60, j%60, role, i, j, needle)
		}
		if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "warm-single-digit-needle", Harness: "claude", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		b.Fatal(err)
	}
	bench := func(name string, o search.Options, wantHits int) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				start := time.Now()
				ss, err := Search(dir, o)
				if err != nil {
					b.Fatal(err)
				}
				hits, err := search.Run(ss, o)
				if err != nil {
					b.Fatal(err)
				}
				if len(hits) != wantHits {
					b.Fatalf("hits=%d, want %d", len(hits), wantHits)
				}
				b.ReportMetric(float64(time.Since(start).Microseconds())/1000, "warm_ms")
			}
		})
	}
	bench("selective", o, 10)
	bench("fat-top15", search.Options{Query: "synthetic", Harness: "claude"}, 15)
}

func TestEachRecordIgnoresTruncatedTail(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "records.bin")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	ttbl := newRecordTables()
	if _, err := writeRecord(f, Record{Key: "claude:s1", SourcePath: "s1.jsonl", Role: "user", Text: "complete"}, ttbl); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{99, 0, 0, 0, '{'}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var got []Record
	if err := eachRecord(p, ttbl, func(r Record) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "complete" {
		t.Fatalf("bad records: %#v", got)
	}
}

func TestCurrentFilesSkipsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "project")
	outside := filepath.Join(tmp, "outside.jsonl")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(proj, "linked.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	files := currentFiles("claude")
	if _, ok := files[link]; ok {
		t.Fatalf("symlink was indexed: %#v", files[link])
	}
}

func TestOldJSONManifestRebuildsTransparently(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"old manifest rebuild needle"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := Manifest{Version: version - 1, Files: map[string]FileState{}, Sessions: map[string]SessionMeta{}, BuiltAt: time.Now(), Scope: "h:claude"}
	if err := writeManifest(dir, old); err != nil {
		t.Fatal(err)
	}
	if err := EnsureForSearch(dir, search.Options{Query: "needle", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, search.Options{Query: "needle", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("old index was not rebuilt: %#v", ss)
	}
}

func TestRedactsSecretsAtIngest(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	secrets := []string{
		"AKIA" + "ABCDEFGHIJKLMNOP",
		"aws_secret_access_key=abcdEFGH12345678abcdEFGH12345678",
		"api_key=0123456789abcdef",
		"password=abcdefabcdefabcd",
		"Authorization: abcdefabcdefabcd",
		"Bearer eyJhbGciOiJIUzI1NiJ9abcdef",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEAfakefakefakefakefake\n-----END RSA PRIVATE KEY-----",
		"ghp_" + "1234567890abcdefghijklmnop",
		"gho_1234567890abcdefghijklmnop",
		"gith" + "ub_pat_1234567890abcdefghijklmnop",
		"sk-1" + "2345678901234567890",
		"npm_" + "123456789012345678901234567890",
		"xoxb" + "-123456789012-123456789012-abcdefghijkl",
		"xoxp" + "-123456789012-123456789012-abcdefghijkl",
		"AIza123456789012345678901234567890",
		"postgres://user:pass1234567890@localhost/db",
	}
	text := "redactionmarker before " + strings.Join(secrets, " middle ") + " after"
	data := fmt.Sprintf(`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":%q}}`+"\n", text)
	p := filepath.Join(proj, "s1.jsonl")
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "no-codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "no-opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(tmp, "no-gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "no-cursor"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "no-cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(tmp, "no-antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(tmp, "no-aider"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "redactionmarker", Harness: "claude"}, false, nil); err != nil {
		t.Fatal(err)
	}
	recordBytes, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if bytes.Contains(recordBytes, []byte(secret)) {
			t.Fatalf("records.bin contains secret %q", secret)
		}
		// Searching for a secret must never hand the secret back. The
		// relevance tier may legitimately surface the session via the
		// non-secret words around a masked value (postgres, localhost), so
		// the invariant is value absence in results, not result emptiness.
		ss, searchErr := Search(dir, search.Options{Query: secret, Harness: "claude"})
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		for _, sess := range ss {
			for _, m := range sess.Messages {
				if strings.Contains(m.Text, secret) {
					t.Fatalf("search for %q returned the raw secret", secret)
				}
			}
		}
	}
	// The secret VALUES themselves must stay unfindable: no session should
	// match a query made of the masked value alone.
	for _, value := range []string{"pass1234567890", "1234567890abcdefghijklmnop", "MIIEpAIBAAKCAQEAfakefakefakefakefake"} {
		ss, searchErr := Search(dir, search.Options{Query: value, Harness: "claude"})
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		if len(ss) != 0 {
			t.Fatalf("masked value %q still findable: %#v", value, ss)
		}
	}
	ss, err := Search(dir, search.Options{Query: "redactionmarker", Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || !strings.Contains(ss[0].Messages[0].Text, "[redacted:") || !strings.Contains(ss[0].Messages[0].Text, "before") || !strings.Contains(ss[0].Messages[0].Text, "after") {
		t.Fatalf("surrounding words not searchable/redacted: %#v", ss)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Redacted < len(secrets) || m.Files[p].Redactions < len(secrets) {
		t.Fatalf("redaction counters not reported: total=%d file=%d", m.Redacted, m.Files[p].Redactions)
	}
	stats, err := Redactions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != m.Redacted || stats.Files[p] != m.Files[p].Redactions {
		t.Fatalf("Redactions()=%#v manifest=%#v", stats, m.Files[p])
	}
}

func TestDecodeRecordStubRoleAndStubToolOutputAll(t *testing.T) {
	tab := newRecordTables()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		text string
		ts   time.Time
	}{
		{text: "short tool output", ts: now},
		{text: strings.Repeat("compressible tool output line\n", 40), ts: now},
		{text: "unstamped output", ts: time.Time{}},
	} {
		enc := encodeRecord(Record{
			Key:        "claude:s1",
			SourcePath: "/tmp/s1.jsonl",
			Role:       roleToolOutput,
			Time:       tc.ts,
			Text:       tc.text,
		}, tab)
		rec, err := decodeRecordStubRole(enc, tab)
		if err != nil {
			t.Fatalf("decodeRecordStubRole: %v", err)
		}
		if rec.Key != "claude:s1" || rec.Role != roleToolOutput || rec.Text != "" || !rec.Time.Equal(tc.ts) {
			t.Fatalf("unexpected stub record: %+v", rec)
		}
	}

	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-Users-me-deja-vu")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	s1Path := filepath.Join(proj, "s1.jsonl")
	data := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"` + strings.Repeat("tool output line ", 30) + `"}]}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"answer text"}}` + "\n"
	if err := os.WriteFile(s1Path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	var out bytes.Buffer
	if err := Ensure(dir, "claude", false, &out); err != nil {
		t.Fatal(err)
	}
	ss, err := RecentMatching(dir, 2, search.Options{Harness: "claude", StubToolOutput: true})
	if err != nil || len(ss) != 1 {
		t.Fatalf("RecentMatching(StubToolOutput): len=%d err=%v", len(ss), err)
	}
	s2Path := filepath.Join(proj, "s2.jsonl")
	if err := os.WriteFile(s2Path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if unread := HarnessUnreadCountsFor(dir, map[string][]string{"claude": {s1Path, s2Path}}); unread["claude"] != 1 {
		t.Fatalf("HarnessUnreadCountsFor = %v, want 1", unread)
	}
	if err := EachRecordOfRoleWhere(dir, "user", func(m SessionMeta) bool { return m.Harness == "claude" }, func(SessionMeta, Record) {}); err != nil {
		t.Fatalf("EachRecordOfRoleWhere: %v", err)
	}
	if _, _, err := SpanInventoryWhere(dir, func(m SessionMeta) bool { return m.Harness == "claude" }); err != nil {
		t.Fatalf("SpanInventoryWhere: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, "recordoffsets.bin"))
	if ss2, err := RecentMatching(dir, 2, search.Options{Harness: "claude", StubToolOutput: true}); err != nil || len(ss2) != 1 {
		t.Fatalf("RecentMatching(StubToolOutput walk): len=%d err=%v", len(ss2), err)
	}
}

func writeClaudeLine(t *testing.T, path, sid, text string, partial bool) {
	t.Helper()
	line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}`
	if !partial {
		line += "\n"
	} else {
		line = line[:len(line)/2] // torn mid-write
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A session file caught mid-write must not lose the torn line: the next
// index pass picks it up exactly once.
func TestTornTailLineIndexedOnce(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-torn")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(proj, "t1.jsonl")
	writeClaudeLine(t, file, "t1", "first tornneedle message", false)
	// torn second line: writer got interrupted mid-json
	writeClaudeLine(t, file, "t1", "second tornneedle message", true)
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	o := search.Options{Query: "tornneedle", All: true}
	if err := EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err := Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("initial index: %#v", ss)
	}

	// writer finishes the line: complete the torn json and append a newline
	full := `{"type":"user","sessionId":"t1","timestamp":"2026-01-02T03:06:05Z","message":{"role":"user","content":"second tornneedle message"}}`
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	safe := 0
	for i, v := range slices.Backward(b) {
		if v == '\n' {
			safe = i + 1
			break
		}
	}
	if err = os.WriteFile(file, append(b[:safe], []byte(full+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = EnsureForSearch(dir, o, false, nil); err != nil {
		t.Fatal(err)
	}
	ss, err = Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("after completing torn line: want 2 messages exactly once, got %#v", ss)
	}
}

// Subagent transcripts are skipped by default and included with the env flag.
func TestSubagentTranscriptsSkipped(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-sub")
	sub := filepath.Join(proj, "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	main := `{"type":"user","sessionId":"m1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"mainneedle question"}}` + "\n"
	agent := `{"type":"user","sessionId":"a1","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"subneedle question"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "m1.jsonl"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a1.jsonl"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	setHome(t, t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	dir := filepath.Join(tmp, "index.db")
	if err := EnsureForSearch(dir, search.Options{Query: "subneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	// A child run is in the index by default now, as its task and its answer
	// (#3009): a quarter of a real machine's Claude messages were in these
	// files, including the turns that read like a settled answer.
	if ss, err := Search(dir, search.Options{Query: "subneedle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("subagent not indexed by default: %#v err=%v", ss, err)
	}
	if ss, err := Search(dir, search.Options{Query: "mainneedle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("main session missing: %#v err=%v", ss, err)
	}

	// And the opt-out puts it back.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	dirOff := filepath.Join(tmp, "index-off.db")
	if err := EnsureForSearch(dirOff, search.Options{Query: "subneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dirOff, search.Options{Query: "subneedle", All: true}); err != nil || len(ss) != 0 {
		t.Fatalf("the opt-out indexed the subagent anyway: %#v err=%v", ss, err)
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	dir2 := filepath.Join(tmp, "index2.db")
	if err := EnsureForSearch(dir2, search.Options{Query: "subneedle"}, false, nil); err != nil {
		t.Fatal(err)
	}
	if ss, err := Search(dir2, search.Options{Query: "subneedle", All: true}); err != nil || len(ss) != 1 {
		t.Fatalf("opt-in did not index subagent: %#v err=%v", ss, err)
	}
}

// PR 1220 raised the sync-vs-ingest divergence question around issue #492:
// the route a message takes into an index must not change its per-session term
// frequencies. Offsets and numeric session ordinals are intentionally excluded
// because independently built indexes are free to assign both differently.
func TestIngestAndSyncProduceSamePostings(t *testing.T) {
	tmp := t.TempDir()

	claudeA := filepath.Join(tmp, "claude-a")
	projectA := filepath.Join(claudeA, "-tmp-parity")
	if err := os.MkdirAll(projectA, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config-a"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeA)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes-a.jsonl"))

	records := []struct {
		name string
		line string
	}{
		{
			name: "cjk.jsonl",
			line: `{"type":"user","message":{"role":"user","content":"關係 and 关系 describe the same relationship"},"timestamp":"2026-01-02T03:04:05Z","sessionId":"cjk-session","cwd":"/tmp/parity"}` + "\n",
		},
		{
			name: "english.jsonl",
			line: `{"type":"user","message":{"role":"user","content":"plain english message about durable indexes"},"timestamp":"2026-02-03T04:05:06Z","sessionId":"english-session","cwd":"/tmp/parity"}` + "\n",
		},
		{
			// Term frequency counts one posting per record, so a tf above one
			// needs the word in two turns of the same session, not twice in
			// one turn.
			name: "repeat.jsonl",
			line: `{"type":"user","message":{"role":"user","content":"repeat this indexed word"},"timestamp":"2026-03-04T05:06:07Z","sessionId":"repeat-session","cwd":"/tmp/parity"}` + "\n" +
				`{"type":"assistant","message":{"role":"assistant","content":"the repeat happened again"},"timestamp":"2026-03-04T05:07:07Z","sessionId":"repeat-session","cwd":"/tmp/parity"}` + "\n",
		},
	}
	for _, record := range records {
		if err := os.WriteFile(filepath.Join(projectA, record.name), []byte(record.line), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A session whose only turn is one long tool result pins the other two
	// halves of the invariant. The text has no signal-marker lines, so
	// signalLines keeps its head and tail and drops the middle: headonlyword
	// sits inside the first 8 KiB, midonlyword between the head and the final
	// 4 KiB, tailonlyword in the tail. Ingest indexes head and tail with the
	// tool bit set and never indexes the middle; the imported copy must do
	// exactly the same.
	pad := strings.Repeat("plain filler that stays clear of every signal marker ", 200)
	longTool := "headonlyword " + pad[:9000] + " midonlyword " + pad[:5000] + " tailonlyword"
	toolJSON, err := json.Marshal(longTool)
	if err != nil {
		t.Fatal(err)
	}
	toolLine := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":` + string(toolJSON) + `}]},"timestamp":"2026-04-05T06:07:08Z","sessionId":"tool-session","cwd":"/tmp/parity"}` + "\n"
	if err = os.WriteFile(filepath.Join(projectA, "tool.jsonl"), []byte(toolLine), 0o644); err != nil {
		t.Fatal(err)
	}

	dirA := filepath.Join(tmp, "index-a.db")
	if err = Ensure(dirA, "", false, nil); err != nil {
		t.Fatal(err)
	}

	sourceManifest, err := readManifest(dirA)
	if err != nil {
		t.Fatal(err)
	}
	wantSourceIDs := map[string]bool{
		"cjk-session":     false,
		"english-session": false,
		"repeat-session":  false,
		"tool-session":    false,
	}
	if len(sourceManifest.Sessions) != len(wantSourceIDs) {
		t.Fatalf("ingest produced %d sessions, want %d", len(sourceManifest.Sessions), len(wantSourceIDs))
	}

	// Local Claude metadata carries the original JSON sessionId in ID, while
	// imported metadata uses ImportedSessionID. Derive both spellings from the
	// source manifest and normalize them to that original sessionId.
	identityAliases := make(map[string]string, len(sourceManifest.Sessions)*2)
	for _, meta := range sourceManifest.Sessions {
		if _, ok := wantSourceIDs[meta.ID]; !ok {
			t.Fatalf("ingest produced unexpected session identity %q", meta.ID)
		}
		wantSourceIDs[meta.ID] = true
		identityAliases[meta.ID] = meta.ID
		identityAliases[ImportedSessionID(meta.Harness, meta.ID)] = meta.ID
	}
	for id, found := range wantSourceIDs {
		if !found {
			t.Fatalf("ingest did not produce session %q", id)
		}
	}

	batchDir := filepath.Join(tmp, "batch")
	if err = os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exported, err := Export(dirA, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if exported == 0 {
		t.Fatal("export produced no records")
	}

	// Use a separate config root so the destination represents another machine
	// and does not reject the exported batch as its own returning outbox.
	claudeB := filepath.Join(tmp, "claude-b")
	if err = os.MkdirAll(claudeB, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config-b"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeB)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes-b.jsonl"))

	dirB := filepath.Join(tmp, "index-b.db")
	if err = Ensure(dirB, "", false, nil); err != nil {
		t.Fatal(err)
	}
	imported, err := Import(dirB, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if imported != exported {
		t.Fatalf("imported %d records from an export of %d", imported, exported)
	}

	collect := func(dir string) map[string]map[string]int {
		t.Helper()

		manifest, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		ordinalIdentity := make(map[int]string, len(manifest.Sessions))
		for _, meta := range manifest.Sessions {
			identity, ok := identityAliases[meta.ID]
			if !ok {
				t.Fatalf("%s contains unexpected session identity %q", dir, meta.ID)
			}
			ordinal := int(meta.Ord)
			if previous, exists := ordinalIdentity[ordinal]; exists {
				t.Fatalf("%s assigns ordinal %d to both %q and %q", dir, ordinal, previous, identity)
			}
			ordinalIdentity[ordinal] = identity
		}

		out := make(map[string]map[string]int)
		bucketsDir := filepath.Join(dir, "buckets")
		entries, err := os.ReadDir(bucketsDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".bin" {
				continue
			}
			bucketPostings, err := readBucket(filepath.Join(bucketsDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for token, postings := range bucketPostings {
				bySession := out[token]
				if bySession == nil {
					bySession = make(map[string]int)
					out[token] = bySession
				}
				for _, posting := range postings {
					identity, ok := ordinalIdentity[int(posting.Sid)]
					if !ok {
						t.Fatalf("%s token %q refers to unknown session ordinal %d", dir, token, posting.Sid)
					}
					bySession[identity]++
				}
			}
		}
		return out
	}

	ingested := collect(dirA)
	synced := collect(dirB)

	// The simplified key is emitted several times for the one CJK message —
	// once by the whole-run token of the Simplified spelling and once by the
	// folded bigrams of each spelling — and all of them collapse to a single
	// posting. That collapse is exactly what the sync route has to reproduce
	// for the DeepEqual below to hold.
	if got := ingested["t关系"]["cjk-session"]; got != 1 {
		t.Fatalf("fold-collision fixture produced tf %d for t关系, want 1", got)
	}

	repeatedTF := false
	for _, bySession := range ingested {
		if bySession["repeat-session"] > 1 {
			repeatedTF = true
			break
		}
	}
	if !repeatedTF {
		t.Fatal("repeated-word fixture did not produce a token with tf greater than one")
	}

	// The tokenizedPart half: the middle of the long tool output earns no
	// posting on either route, while its head and tail do.
	if _, ok := ingested["tmidonlyword"]; ok {
		t.Fatal("ingest indexed the middle of a long tool output; the fixture no longer exercises signalLines")
	}
	if _, ok := synced["tmidonlyword"]; ok {
		t.Fatal("import indexed the middle of a long tool output that ingest drops")
	}

	// The tool-bit half: postings for the tool-only session carry Tool on
	// both routes.
	for _, dir := range []string{dirA, dirB} {
		for _, tok := range []string{"theadonlyword", "ttailonlyword"} {
			posts, err := postingsFor(dir, tok)
			if err != nil {
				t.Fatal(err)
			}
			if len(posts) == 0 {
				t.Fatalf("%s holds no postings for %q", dir, tok)
			}
			for _, p := range posts {
				if !p.Tool {
					t.Fatalf("%s posting for %q lost the tool bit", dir, tok)
				}
			}
		}
	}

	tokenSet := make(map[string]struct{}, len(ingested)+len(synced))
	for token := range ingested {
		tokenSet[token] = struct{}{}
	}
	for token := range synced {
		tokenSet[token] = struct{}{}
	}
	tokens := make([]string, 0, len(tokenSet))
	for token := range tokenSet {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)

	for _, token := range tokens {
		if !reflect.DeepEqual(ingested[token], synced[token]) {
			t.Errorf("posting counts differ for token %q: ingest=%v sync=%v", token, ingested[token], synced[token])
		}
	}
}

// A session still being written is appendable, and an append blocks the
// answer: a search for last month's history sat through the tail of a live
// transcript first — seconds per query, scaling with the file rather than with
// the question (#3021). Past the cap the tail goes to the detached warmup, the
// way rewrite-grade work already does.
func TestASearchDoesNotWaitOnALongLiveSession(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	proj := filepath.Join(claude, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "live.jsonl")
	turn := func(n, text string) string {
		return `{"type":"user","sessionId":"live","cwd":"/tmp/app","timestamp":"2026-01-0` + n +
			`T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(turn("1", "why does the quokkabloom retry")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// A small append is still read before answering: the wait is the point of
	// having a current index at all.
	small, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = small.WriteString(turn("2", "the retry is capped at three")); err != nil {
		t.Fatal(err)
	}
	_ = small.Close()
	stale, err := EnsureForSearchStale(dir, query.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Error("an ordinary turn was handed to the warmup instead of being read")
	}

	// And a session that has been writing all day is not something every
	// search waits on.
	inlineAppendMax = 1 << 10
	t.Cleanup(func() { inlineAppendMax = 8 << 20 })
	big, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = big.WriteString(turn("3", strings.Repeat("tool output ", 400))); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()
	stale, err = EnsureForSearchStale(dir, query.Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Error("the search waited on the tail of a live session")
	}
}

// InspectionCommand decides whether "you have run this before" is worth saying.
// Two shapes were reaching it as changes: git's own options sit before the
// subcommand, so `git -C dir status` was read as the subcommand `-C`, and a
// line that starts with `cd` was read as `cd`.
func TestInspectionCommandReadsPastGitOptionsAndCd(t *testing.T) {
	for _, cmd := range []string{
		"git status --short",
		"git -C /tmp/wt status --short",
		"git --no-pager -C /tmp/wt log -1",
		"git -c user.name=x -C /tmp/wt show HEAD",
		"cd /tmp && git -C /tmp/wt grep -n foo",
		"cd /tmp; ls -la",
	} {
		if !InspectionCommand(cmd) {
			t.Errorf("looking at state read as changing it: %q", cmd)
		}
	}
	for _, cmd := range []string{
		"git checkout -- cmd/deja/mcp.go",
		"git -C /tmp/wt checkout HEAD -- x.go",
		"cd /tmp && rm -rf build",
		"brew install coreutils",
		"ls -la && rm -rf build",
	} {
		if InspectionCommand(cmd) {
			t.Errorf("changing state read as looking at it: %q", cmd)
		}
	}
}

// The miner asks a wider question than the hook: could this have been the
// remedy at all? Running the suite is how a fix is checked, and a grep for the
// symbol the compiler named is what an agent does before fixing it — the rule
// that keeps a pair is satisfied by construction there.
func TestInvestigationCommandCoversTheStepsBeforeAFix(t *testing.T) {
	for _, cmd := range []string{
		"go test ./internal/index/ -count=1",
		"go vet ./...",
		"grep -rn 'undefined' internal/",
		"sed -n '10,20p' internal/index/ingest.go",
		"rg -n reduceToTerms",
		"gofmt -l internal",
	} {
		if !investigationCommand(cmd) {
			t.Errorf("a step taken before the fix read as the fix: %q", cmd)
		}
	}
	for _, cmd := range []string{
		"sed -i '' 's/a/b/' f.go",
		"go mod tidy",
		"brew install gnu-sed",
		"rm internal/index/zz_probe_test.go",
	} {
		if investigationCommand(cmd) {
			t.Errorf("a change read as investigation: %q", cmd)
		}
	}
	// The hook's question is narrower and must stay that way: an agent runs
	// `go test` in every session, and that is worth saying nothing about
	// while still being an ordinary thing to do rather than an inspection.
	if InspectionCommand("go test ./... -count=1") {
		t.Error("the miner's rule leaked into the hook's")
	}
}

// allHarnessEnv points every source at a sandbox subdir and returns the
// index dir. Missing stores are simply empty.
func allHarnessEnv(t *testing.T) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	setHome(t, filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(root, "opencode.db"))
	t.Setenv("DEJA_GEMINI_ROOT", filepath.Join(root, "gemini"))
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(root, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(root, "cursor-cli"))
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", filepath.Join(root, "antigravity"))
	t.Setenv("DEJA_AIDER_ROOTS", filepath.Join(root, "aiderroot"))
	t.Setenv("DEJA_GROK_ROOT", filepath.Join(root, "grok"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "index.db")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedFileHarnesses(t *testing.T, root string) {
	t.Helper()
	// claude
	write(t, filepath.Join(root, "claude", "-p-app", "c1.jsonl"),
		`{"type":"user","sessionId":"c1","timestamp":"2026-02-01T10:00:00Z","message":{"role":"user","content":"claundneedle unicode тест 日本語"}}`+"\n")
	// codex rollout + history
	write(t, filepath.Join(root, "codex", "sessions", "2026", "02", "01", "rollout-2026-02-01T10-00-00-x1.jsonl"),
		`{"type":"session_meta","timestamp":"2026-02-01T10:00:00Z","payload":{"session_id":"x1","cwd":"/p/app"}}`+"\n"+
			`{"timestamp":"2026-02-01T10:00:01Z","payload":{"role":"user","content":"codexneedle question"}}`+"\n")
	write(t, filepath.Join(root, "codex", "history.jsonl"),
		`{"session_id":"h1","ts":1769940000,"text":"historyneedle entry"}`+"\n")
	// gemini dual format, same session id
	gj := `{"sessionId":"g1","startTime":"2026-02-01T10:00:00.000Z","lastUpdated":"2026-02-01T10:00:00.000Z","messages":[{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":"geminineedle dup"}]}`
	write(t, filepath.Join(root, "gemini", "tmp", "s", "chats", "session-g1.json"), gj)
	write(t, filepath.Join(root, "gemini", "tmp", "s", "chats", "session-g1.jsonl"),
		`{"sessionId":"g1","startTime":"2026-02-01T10:00:00.000Z","lastUpdated":"2026-02-01T10:05:00.000Z"}`+"\n"+
			`{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":"geminineedle dup"}`+"\n")
	// antigravity transcript
	write(t, filepath.Join(root, "antigravity", "brain", "traj-1", ".system_generated", "logs", "transcript.jsonl"),
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-02-01T10:00:00Z","content":"<USER_REQUEST>\nantineedle request\n</USER_REQUEST>"}`+"\n"+
			`{"step_index":1,"source":"SYSTEM","type":"EPHEMERAL_MESSAGE","created_at":"2026-02-01T10:00:01Z","content":"noise skipped"}`+"\n"+
			`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-02-01T10:00:02Z","content":"antineedle answer","thinking":"hidden"}`+"\n")
	// aider
	write(t, filepath.Join(root, "aiderroot", ".aider.chat.history.md"),
		"# aider chat started at 2026-02-01 10:00:00\n\n#### aiderneedle question\n\naiderneedle answer\n")
	// grok
	grokDir := filepath.Join(root, "grok", "sessions", "%2Fp%2Fapp")
	write(t, filepath.Join(grokDir, "gr1", "summary.json"),
		`{"info":{"id":"gr1","cwd":"/p/app"},"generated_title":"grok session","created_at":"2026-02-01T10:00:00Z","updated_at":"2026-02-01T10:05:00Z"}`)
	write(t, filepath.Join(grokDir, "gr1", "updates.jsonl"),
		`{"timestamp":1769940000000,"params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"grokneedle question"},"_meta":{"promptIndex":0}},"_meta":{"promptId":"p0"}}}`+"\n")
}

func seedCursorDB(t *testing.T, root string) bool {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return false
	}
	db := filepath.Join(root, "cursor-user", "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `create table cursorDiskKV (key text primary key, value text);
insert into cursorDiskKV values
 ('composerData:cu1', json('{"composerId":"cu1","name":"cursortitle","createdAt":1769940000000,"lastUpdatedAt":1769940300000}')),
 ('bubbleId:cu1:b1', json('{"type":1,"text":"cursorneedle question","timestamp":1769940001000,"workspaceProjectDir":"/p/app"}')),
 ('bubbleId:cu1:b2', json('{"type":2,"text":"cursorneedle answer","timestamp":1769940002000}'));`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("seed cursor db: %v: %s", err, out)
	}
	return true
}

// The regression that keeps biting: every harness must index, search, and
// carry exactly one copy of each message through a full build.
func TestAllHarnessesIndexAndSearch(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	hasCursor := seedCursorDB(t, root)

	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"claundneedle unicode": "claude", "codexneedle question": "codex",
		"historyneedle entry": "codex", "geminineedle dup": "gemini",
		"antineedle request": "antigravity", "aiderneedle question": "aider",
		"grokneedle question": "grok",
	}
	if hasCursor {
		cases["cursorneedle question"] = "cursor"
	}
	for needle, harness := range cases {
		ss, err := Search(dir, search.Options{Query: needle, All: true})
		if err != nil {
			t.Fatalf("%s: %v", needle, err)
		}
		total := 0
		for _, s := range ss {
			for _, m := range s.Messages {
				if strings.Contains(m.Text, needle) {
					total++
				}
			}
			if s.Harness != harness && len(ss) == 1 {
				t.Fatalf("%s: harness=%s want %s", needle, s.Harness, harness)
			}
		}
		if total != 1 {
			t.Fatalf("%s: %d copies, want exactly 1", needle, total)
		}
	}
	// SYSTEM step and gemini thinking must not be indexed
	if ss, _ := Search(dir, search.Options{Query: "noise skipped", All: true}); len(ss) != 0 {
		t.Fatal("antigravity SYSTEM step indexed")
	}
	if ss, _ := Search(dir, search.Options{Query: "hidden", All: true}); len(ss) != 0 {
		t.Fatal("antigravity thinking indexed")
	}
}

// Determinism: two from-scratch rebuilds on identical fixtures give identical
// session and message counts.
func TestRebuildDeterminism(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	count := func() (int, int) {
		if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, true, nil); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		msgs := 0
		_ = eachRecord(filepath.Join(dir, "records.bin"), mustTables(t, dir), func(_ Record) { msgs++ })
		return len(m.Sessions), msgs
	}
	s1, m1 := count()
	s2, m2 := count()
	if s1 != s2 || m1 != m2 {
		t.Fatalf("non-deterministic: %d/%d vs %d/%d", s1, m1, s2, m2)
	}
}

// Incremental append to each file-based harness: the new message is found
// and nothing is duplicated.
func TestIncrementalPerHarnessNoDup(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	appends := []struct{ path, line string }{
		{filepath.Join(root, "claude", "-p-app", "c1.jsonl"),
			`{"type":"user","sessionId":"c1","timestamp":"2026-02-01T11:00:00Z","message":{"role":"user","content":"incrneedle claude"}}` + "\n"},
		{filepath.Join(root, "aiderroot", ".aider.chat.history.md"),
			"\n#### incrneedle aider\n\nmore\n"},
	}
	for _, a := range appends {
		time.Sleep(10 * time.Millisecond)
		f, err := os.OpenFile(a.path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(a.line); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	if err := EnsureForSearch(dir, search.Options{Query: "incrneedle", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"incrneedle claude", "incrneedle aider"} {
		ss, _ := Search(dir, search.Options{Query: needle, All: true})
		n := 0
		for _, s := range ss {
			for _, m := range s.Messages {
				if strings.Contains(m.Text, needle) {
					n++
				}
			}
		}
		if n != 1 {
			t.Fatalf("%s appeared %d times, want 1", needle, n)
		}
	}
}

// Sync roundtrip across the full harness set: export, import elsewhere,
// re-export must not echo, and search results match.
func TestSyncRoundtripAllHarnesses(t *testing.T) {
	root, dir := allHarnessEnv(t)
	seedFileHarnesses(t, root)
	if err := EnsureForSearch(dir, search.Options{Query: "x", All: true}, false, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(root, "batch")
	n, err := ExportFull(dir, batch)
	if err != nil || n == 0 {
		t.Fatalf("export n=%d err=%v", n, err)
	}
	dir2 := filepath.Join(root, "index2.db")
	imp, err := Import(dir2, batch)
	if err != nil || imp != n {
		t.Fatalf("import %d, exported %d, err=%v", imp, n, err)
	}
	// re-export from the importing index must not echo imported records
	echo, err := ExportFull(dir2, filepath.Join(root, "batch2"))
	if err != nil {
		t.Fatal(err)
	}
	if echo != 0 {
		t.Fatalf("re-export echoed %d records", echo)
	}
	for _, needle := range []string{"claundneedle", "aiderneedle", "antineedle"} {
		ss, _ := Search(dir2, search.Options{Query: needle, All: true})
		if len(ss) == 0 {
			t.Fatalf("%s missing after sync import", needle)
		}
	}
	// re-import is a no-op
	if again, _ := Import(dir2, batch); again != 0 {
		t.Fatalf("re-import added %d", again)
	}
}

var _ = json.Marshal

// A Japanese session, searched the way its writer would search it. The word in
// the query is in the text verbatim, so the exact tier is what must answer —
// it fell through to the close tier, because the query asked for a token the
// index could never hold (#1319).
func TestJapaneseWordsAnswerOnTheExactTier(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, text := range map[string]string{
		"jp":  "リトライキューがステージングで止まった",
		"jp2": "サーバーのエラーを調べた",
	} {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ステージング", "サーバー", "エラー", "リトライ"} {
		res, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(res.Sessions) == 0 {
			t.Errorf("%q: no session, though the word is in the text", q)
			continue
		}
		if res.Tier != search.TierExact {
			t.Errorf("%q: answered on the %s tier, though the word is in the text verbatim", q, res.Tier)
		}
	}
}

// Chinese and Korean were already right, and must stay so.
func TestOtherScriptsStillAnswerExactly(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, text := range map[string]string{
		"cn": "调度器的重试队列在预发环境卡住了",
		"kr": "재시도 대기열이 스테이징에서 멈췄다",
	} {
		line := `{"type":"user","sessionId":"` + id + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"重试队列", "调度器", "재시도"} {
		res, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(res.Sessions) == 0 || res.Tier != search.TierExact {
			t.Errorf("%q: %d sessions on the %s tier", q, len(res.Sessions), res.Tier)
		}
	}
}

// The posting keys for Japanese change shape with this, so a store built before
// it holds the split keys while a query built after asks for the joined ones —
// the same situation the NFC fold was bumped for (version 24).
func TestTheFormatVersionRidesWithTheKeys(t *testing.T) {
	if version < 27 {
		t.Errorf("index version is %d — the CJK keys changed without the bump that re-derives them", version)
	}
}

func keepFreshStore(t *testing.T) (dir, path string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir = filepath.Join(home, "idx")
	path = filepath.Join(claude, "project", "s.jsonl")
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "heron lantern"))
	if err := EnsureForSearch(dir, query.Options{All: true}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// indexedBytes is how much of the transcripts the manifest says it has read.
func indexedBytes(t *testing.T, dir string) int64 {
	t.Helper()
	unlock, ok, err := tryLockDirFor(dir, keepLockPatience)
	if err != nil || !ok {
		t.Fatalf("lock index: ok=%v err=%v", ok, err)
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (k *freshKeeper) checkedOnce() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return !k.checked.IsZero()
}

func kickKeeper(k *freshKeeper) {
	k.mu.Lock()
	before := k.checked
	k.mu.Unlock()
	k.kick <- struct{}{}
	for {
		k.mu.Lock()
		done := k.checked.After(before)
		k.mu.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// With a keeper running, a query reads the index as it is: the append waits
// for the keeper, which absorbs it on its own goroutine.
func TestKeepFreshTakesTheWalkOffTheQuery(t *testing.T) {
	dir, path := keepFreshStore(t)
	var stale atomic.Int32
	stop := KeepFresh(dir, time.Hour, func() { stale.Add(1) }, io.Discard)
	defer stop()
	k := keptFresh(dir)
	if k == nil {
		t.Fatal("no keeper registered for the index")
	}
	waitUntil(t, "the first check", k.checkedOnce)

	before := indexedBytes(t, dir)
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "heron lantern"), claudeLine("s1", "2026-01-01T00:02:00Z", "quartz meadow"))
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("query: stale=%v err=%v", s, err)
	}
	if got := indexedBytes(t, dir); got != before {
		t.Fatalf("the query read the append itself: %d -> %d bytes", before, got)
	}

	kickKeeper(k)
	if got := indexedBytes(t, dir); got <= before {
		t.Fatalf("the keeper did not absorb the append: %d -> %d bytes", before, got)
	}
	if n := stale.Load(); n != 0 {
		t.Fatalf("an append was handed to a detached build %d times", n)
	}
}

// Work the keeper will not do inline is handed on once, and queries say the
// answer is stale until the index is written again.
func TestKeepFreshHandsOnRewritesAndForgetsThemOnPublish(t *testing.T) {
	dir, path := keepFreshStore(t)
	var handed atomic.Int32
	stop := KeepFresh(dir, time.Hour, func() { handed.Add(1) }, io.Discard)
	defer stop()
	k := keptFresh(dir)
	waitUntil(t, "the first check", k.checkedOnce)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	kickKeeper(k)
	if n := handed.Load(); n != 1 {
		t.Fatalf("a removed transcript was handed on %d times, want 1", n)
	}
	c, err := EnsureForSearchCoverage(dir, query.Options{All: true}, io.Discard)
	if err != nil || !c.Stale {
		t.Fatalf("query after a removal: %+v err=%v", c, err)
	}

	// The build the hand-off asked for, run here in place of a detached one.
	if err := EnsureForSearch(dir, query.Options{All: true}, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("query after the rebuild published: stale=%v err=%v", s, err)
	}
}

// One keeper per process, and only for its own index; stop lets go of it.
func TestKeepFreshIsOnePerProcess(t *testing.T) {
	dir, _ := keepFreshStore(t)
	stop := KeepFresh(dir, time.Hour, nil, io.Discard)
	k := keptFresh(dir)
	if k == nil {
		t.Fatal("no keeper")
	}
	KeepFresh(dir+"-other", time.Hour, nil, io.Discard)()
	if keptFresh(dir) != k || keptFresh(dir+"-other") != nil {
		t.Fatal("a second keeper replaced the first")
	}
	stop()
	stop()
	if keptFresh(dir) != nil {
		t.Fatal("the keeper outlived stop")
	}
}

// A query that finds the verdict old (the keeper idled, the machine slept)
// wakes the keeper rather than walking itself.
func TestKeepFreshOldVerdictWakesTheKeeper(t *testing.T) {
	dir, path := keepFreshStore(t)
	stop := KeepFresh(dir, 20*time.Millisecond, nil, io.Discard)
	defer stop()
	k := keptFresh(dir)
	waitUntil(t, "the first check", k.checkedOnce)
	// Idle: no query yet, so the ticker does not walk.
	before := indexedBytes(t, dir)
	writeLines(t, path, claudeLine("s1", "2026-01-01T00:01:00Z", "heron lantern"), claudeLine("s1", "2026-01-01T00:02:00Z", "quartz meadow"))
	k.queried.Store(time.Now().Add(-2 * keepIdleAfter).UnixNano())
	time.Sleep(100 * time.Millisecond)
	if got := indexedBytes(t, dir); got != before {
		t.Fatalf("an idle keeper walked: %d -> %d bytes", before, got)
	}
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the woken keeper", func() bool { return indexedBytes(t, dir) > before })
}

// A search in the same process holds the lock while it reads. The keeper
// waits it out rather than taking it for a build and calling the index stale.
func TestKeepFreshWaitsOutASearchHoldingTheLock(t *testing.T) {
	dir, _ := keepFreshStore(t)
	var handed atomic.Int32
	stop := KeepFresh(dir, time.Hour, func() { handed.Add(1) }, io.Discard)
	defer stop()
	k := keptFresh(dir)
	waitUntil(t, "the first check", k.checkedOnce)

	unlock, ok, err := tryLockDir(dir)
	if err != nil || !ok {
		t.Fatalf("lock: ok=%v err=%v", ok, err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		unlock()
	}()
	kickKeeper(k)
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("a held lock read as a stale index: stale=%v err=%v", s, err)
	}
	if n := handed.Load(); n != 0 {
		t.Fatalf("a held lock was handed to a build %d times", n)
	}

	// Held past the keeper's patience, by a build in another process: the
	// last verdict stands and nothing more is asked for.
	unlock2, ok2, err2 := tryLockDir(dir)
	if err2 != nil || !ok2 {
		t.Fatalf("lock: ok=%v err=%v", ok2, err2)
	}
	k.check(func() { handed.Add(1) }, io.Discard)
	unlock2()
	if s, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil || s {
		t.Fatalf("a build in flight left a stale verdict behind: stale=%v err=%v", s, err)
	}
	if n := handed.Load(); n != 0 {
		t.Fatalf("a build in flight was asked for again %d times", n)
	}
}

// The index keeps a transcript as it found it, control bytes included, and the
// stripping happens where the text is served. That is not a preference — it is
// what `TestNothingServedCarriesAControlByte` in cmd/deja rests on: those cases
// index a dirty transcript and then drive each writer, so an indexer that
// sanitised on the way in would leave every one of them passing while proving
// nothing.
//
// Which is the failure this file exists to catch, and the same shape as a blame
// case that answered `[]` and passed for a season (#1985).
func TestTheIndexKeepsWhatTheTranscriptBrought(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-tmp-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	dir := filepath.Join(tmp, "index.db")

	// The escape is written the way a harness writes it — as a \u001b escape in
	// the JSON, which decodes to the byte. A raw control byte inside a JSON
	// string is not valid JSON at all, and the line would be dropped before any
	// of this: that is what made the first version of this fixture index
	// nothing.
	// The same string the serving-path cases use, carriage return included: a
	// fixture that drops one of the bytes leaves that byte unguarded there.
	dirty := "the build failed: \x1b[31mERROR\x1b[0m\x07 pgbouncer pool timed out\r and retried"
	line := func(role string) string {
		b, err := json.Marshal(map[string]any{
			"type": role, "sessionId": "s1", "cwd": "/tmp/app",
			"timestamp": "2026-08-20T10:00:00Z",
			"message":   map[string]any{"role": role, "content": dirty},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	body := line("user") + "\n" + line("assistant") + "\n"
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// Through the accessor a reader uses, not the bytes of a file: a gob holds
	// fields this is not about, and finding an escape somewhere in it says
	// nothing about the message text the serving path will strip.
	got, ok, err := FindByIdentity(dir, "claude", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the session did not come back from the index")
	}
	if len(got.Messages) < 2 {
		t.Fatalf("the session came back with %d messages, so this measures nothing", len(got.Messages))
	}
	// Per message and per byte: concatenating them would let a strip that
	// touches one role through, and "any of these bytes" would let a strip that
	// touches one byte through. Both pass a laxer version of this test.
	for _, m := range got.Messages {
		for _, b := range []string{"\x1b", "\x07", "\r"} {
			if !strings.Contains(m.Text, b) {
				t.Errorf("the index stripped %q from a %s message on the way in, so the serving-path "+
					"cases in cmd/deja prove nothing about it: %q", b, m.Role, m.Text)
			}
		}
	}
}

// threeWays brings index A up to date pass by pass through each step and
// rebuilds a copy of it after the last one.
func (h *twoWayEnv) threeWays(label string, steps ...func()) {
	t := h.t
	t.Helper()
	a := filepath.Join(h.tmp, "a.db")
	for _, s := range steps {
		s()
		if err := Ensure(a, "claude", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	b := filepath.Join(h.tmp, "b.db")
	copyDir(t, a, b)
	if err := Ensure(b, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	diffSnaps(t, label, takeSnap(t, a), takeSnap(t, b))
}

// A transcript rewritten and moved from svc to app, rewritten again and moved
// back leaves the app file deleted and owning the row, its path sorting
// first. The pass took the row's conclusion off the file when it was on disk;
// a rebuild took it off the deleted file's records in the order passes wrote
// them, and named the second day's "worker" where the pass had the third
// day's "mutex".
func TestADeletedCopyOwningTheRowKeepsWhatThePassReadOffIt(t *testing.T) {
	h := newTwoWayEnv(t)
	day := func(p string, d int, q, said string) string {
		return hPrompt(p, "s8", d, 0, q) + hBash(p, "s8", d, 1, "b", "go build ./...", "ok", false) +
			hBash(p, "s8", d, 2, "c", "make migrate", "dial tcp 127.0.0.1:5432: connect: connection refused\n", true) + hSay(p, "s8", d, 6, said)
	}
	move := func(from, to string) {
		if err := os.MkdirAll(filepath.Dir(h.path(to, "s8")), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(h.path(from, "s8"), h.path(to, "s8")); err != nil {
			t.Fatal(err)
		}
	}
	steps := []func(){func() { //nolint:prealloc // a fixture literal the loop below grows
		h.put("svc", "s8", day("svc", 8, "start: why does the build fail", "The ledger needs a fix."))
	}, func() {
		h.put("svc", "s8", day("svc", 9, "again: why does the build fail", "The worker needs a fix."))
		move("svc", "app")
	}, func() {
		h.put("app", "s8", day("app", 10, "again: why does the parser fail", "The mutex needs a fix."))
	}, func() {
		move("app", "svc")
	}}
	settled := func(dir string) string {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions["claude:s8"].Settled
	}
	h.threeWays("moved back", steps...)
	// The live copy written again: the pass takes the row back from the
	// deleted file's records, and kept the conclusion it read off it.
	h2 := newTwoWayEnv(t)
	h = h2
	h.threeWays("moved back, then the live copy rewritten", append(steps, func() {
		h.put("svc", "s8", day("svc", 11, "again: why does the outbox fail", "Nothing settled yet."))
	})...)
	if got := settled(filepath.Join(h.tmp, "a.db")); got != "The mutex needs a fix." {
		t.Errorf("Settled after the live copy is rewritten = %q, want the deleted file's", got)
	}
}

// A version bump rebuilds what was read off a deleted transcript from its
// records, as it always did: a bump can change what is redacted from the
// conclusion, or how friction and questions are read.
func TestAVersionBumpRereadsADeletedTranscriptsConclusion(t *testing.T) {
	h := newTwoWayEnv(t)
	h.put("app", "s1", richSession("app", "s1", 2, "pool"))
	h.put("app", "s2", richSession("app", "s2", 3, "queue"))
	a := filepath.Join(h.tmp, "a.db")
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.path("app", "s2")); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(a)
	if err != nil {
		t.Fatal(err)
	}
	row := m.Sessions["claude:s2"]
	if row.Settled == "" {
		t.Fatal("control: the kept session settled nothing")
	}
	row.Settled, row.Hit = "stale", []uint64{42}
	m.Sessions["claude:s2"] = row
	m.Version = version - 1
	if err = writeManifestMeta(a, m); err != nil {
		t.Fatal(err)
	}
	invalidateManifestCache(a)
	if err = Ensure(a, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Sessions["claude:s2"]; got.Settled == "stale" || slices.Equal(got.Hit, []uint64{42}) {
		t.Errorf("the bump kept the old row's text: settled %q, hit %v", got.Settled, got.Hit)
	}
}

// A line the live copy repeats, whose first copy is filed under the deleted
// transcript the rewrite and move left behind: the pass took the repeat for a
// new turn, a rebuild of the file counts it once. A line the live copy takes
// up for the first time, twice in one append, still counts once, and so does
// a line with a secret in it, which the records hold redacted.
func TestARepeatedLineOfAKeptCopysTurnCountsOnce(t *testing.T) {
	for _, tc := range []struct {
		name, text    string
		heldByRewrite bool
		appendTimes   int
	}{
		{"held before", "The queue needs a fix.", true, 1},
		{"new to the file, twice in one append", "The queue needs a fix.", false, 2},
		{"redacted", "The queue needs GITHUB_TOKEN=ghp_16C7e42F292c6912E7710c838347Ae178B4a", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTwoWayEnv(t)
			said := hSay("svc", "s1", 2, 2, tc.text)
			rewrite := hPrompt("svc", "s1", 3, 0, "again: why does the ledger fail") + hSay("svc", "s1", 3, 1, "The ledger needs a fix.")
			if tc.heldByRewrite {
				rewrite = hPrompt("svc", "s1", 3, 0, "again: why does the ledger fail") + said + hSay("svc", "s1", 3, 1, "The ledger needs a fix.")
			}
			h.threeWays(tc.name, func() {
				h.put("svc", "s1", hPrompt("svc", "s1", 2, 0, "why does the pool fail")+said)
			}, func() {
				h.put("svc", "s1", rewrite)
				if err := os.MkdirAll(filepath.Dir(h.path("infra", "s1")), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(h.path("svc", "s1"), h.path("infra", "s1")); err != nil {
					t.Fatal(err)
				}
			}, func() {
				h.add("infra", "s1", strings.Repeat(said, tc.appendTimes)+hSay("infra", "s1", 3, 3, "The outbox needs a fix."))
			})
		})
	}
}

// A rebuild leaves a session's live copy ahead of its deleted, kept one in
// the log. A rewrite of the live copy then files the turns it no longer holds
// under the kept one, appended at the end, while the kept copy's own records
// stay where they were. The offset table listed the moved turns first: the
// right offsets, in another order than a walk of the log gives (#5522).
func TestTurnsMovedToAKeptCopyKeepTheOffsetTableInLogOrder(t *testing.T) {
	h := newTwoWayEnv(t)
	a := filepath.Join(h.tmp, "a.db")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h.tmp, "config"))
	// Enough else in the log that the rewrite appends to it rather than
	// writing it again.
	for _, sid := range []string{"s1", "s2", "s4", "s5"} {
		body := ""
		var bodySb22 strings.Builder
		for i := range 8 {
			bodySb22.WriteString(hPrompt("svc", sid, 1, i, "why does the ledger fail on retry number "+sid))
		}
		body += bodySb22.String()
		h.put("svc", sid, body)
	}
	build := func(rebuild bool) {
		t.Helper()
		if err := Ensure(a, "", rebuild, nil); err != nil {
			t.Fatal(err)
		}
	}
	h.put("infra", "s3", hPrompt("infra", "s3", 2, 0, "why does the pool fail")+
		hBash("infra", "s3", 2, 1, "a1", "make migrate", "ok", false))
	build(false)
	h.put("aa", "s3", hPrompt("aa", "s3", 2, 0, "copy: why does the queue fail")+
		hBash("aa", "s3", 2, 2, "b1", "go test ./...", "ok", false))
	build(true)
	if err := os.Remove(h.path("infra", "s3")); err != nil {
		t.Fatal(err)
	}
	build(true)
	h.put("aa", "s3", hPrompt("aa", "s3", 2, 3, "again: why does the cache fail"))
	build(false)
	if m, err := readManifest(a); err != nil || m.DeadBytes == 0 {
		t.Fatalf("the rewrite wrote the log again (dead bytes 0, %v), so this measures nothing", err)
	}
	offsetTableMatchesAWalk(t, a, true)
}

var farFuture = time.Now().Add(100 * 365 * 24 * time.Hour)

// The week note says how many sessions their agent deleted this week, and the
// manifest knew only that a transcript was gone, not since when. The pass that
// first finds it gone stamps the time, and a rebuild carries the stamp rather
// than restarting it.
func TestKeptSinceDatesADeletedTranscriptFromThePassThatFoundItGone(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	before := time.Now().Add(-time.Second)
	if got, err := KeptSince(dir, before, farFuture); err != nil || len(got) != 0 {
		t.Fatalf("nothing is deleted yet, KeptSince = %v, %v", got, err)
	}
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	got, err := KeptSince(dir, before, farFuture)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "s1" || got[0].Harness != "claude" {
		t.Fatalf("KeptSince = %+v, want the deleted s1", got)
	}
	at := got[0].At
	if at.Before(before) || at.After(time.Now().Add(time.Second)) {
		t.Fatalf("kept at %v, want the pass that found it gone", at)
	}
	if later, _ := KeptSince(dir, time.Now().Add(time.Hour), farFuture); len(later) != 0 {
		t.Errorf("a window after the deletion still counts it: %+v", later)
	}
	if err = Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	again, err := KeptSince(dir, before, farFuture)
	if err != nil || len(again) != 1 || !again[0].At.Equal(at) {
		t.Fatalf("after a rebuild KeptSince = %+v, %v; want s1 still dated %v", again, err, at)
	}
}

// A rebuild can be the first pass to see the file gone, and it dates it too.
func TestKeptSinceDatesATranscriptARebuildFoundGone(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	before := time.Now().Add(-time.Second)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	got, err := KeptSince(dir, before, farFuture)
	if err != nil || len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("KeptSince = %+v, %v; want s1", got, err)
	}
}

// A row kept before the stamp existed has no date, and no window claims it.
func TestKeptSinceLeavesOutARowWithNoDate(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := m.Files[s1]
	f.KeptAt = 0
	m.Files[s1] = f
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	if got, _ := KeptSince(dir, time.Unix(0, 0), farFuture); len(got) != 0 {
		t.Errorf("an undated kept row was counted: %+v", got)
	}
}

// The stamp is whole seconds, so a pass in the second a test runs in proves
// nothing about carrying it. A row dated three days back keeps that date
// through a rebuild and through an incremental pass that rewrites the
// manifest.
func TestKeptSinceKeepsAnOlderDateThroughLaterPasses(t *testing.T) {
	dir, claudeRoot, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-3 * 24 * time.Hour).Unix()
	setKeptAt(t, dir, s1, past)
	check := func(after string) {
		t.Helper()
		got, err := KeptSince(dir, time.Unix(0, 0), farFuture)
		if err != nil || len(got) != 1 || got[0].At.Unix() != past {
			t.Fatalf("after %s KeptSince = %+v, %v; want s1 dated %d", after, got, err, past)
		}
	}
	if err := Ensure(dir, "claude", true, nil); err != nil {
		t.Fatal(err)
	}
	check("a rebuild")
	s3 := filepath.Join(claudeRoot, "-Users-me-deja-vu", "s3.jsonl")
	if err := os.WriteFile(s3, []byte(`{"type":"user","sessionId":"s3","timestamp":"2026-09-02T03:04:05Z","message":{"role":"user","content":"the frobnic cache warmed"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	check("an incremental pass")
}

// A store written before deja dated deletions holds rows for transcripts
// already gone and not marked kept: 0.21 had no Kept at all. The first pass
// of this build finds them gone, and dating them now would report months of
// deletions as this week's. They stay undated; a deletion after that pass is
// dated.
func TestKeptSinceLeavesOutWhatAnOlderStoreLostBeforeIt(t *testing.T) {
	for _, force := range []bool{false, true} {
		dir, _, s1, s2 := seedTwoTranscripts(t)
		if err := os.Remove(s1); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		m.KeptDated = false
		if err := writeManifestOnly(dir, m); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "claude", force, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := KeptSince(dir, time.Unix(0, 0), farFuture); len(got) != 0 {
			t.Fatalf("force=%v: a deletion from before the upgrade was dated: %+v", force, got)
		}
		if got := sessionsFor(t, dir, "zorblax"); len(got) != 1 {
			t.Fatalf("force=%v: the undated deletion was not kept: %v", force, got)
		}
		if err := os.Remove(s2); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(dir, "claude", false, nil); err != nil {
			t.Fatal(err)
		}
		got, _ := KeptSince(dir, time.Unix(0, 0), farFuture)
		if len(got) != 1 || got[0].ID != "s2" {
			t.Fatalf("force=%v: a deletion after the upgrade pass, KeptSince = %+v; want s2", force, got)
		}
	}
}

// The window is whole seconds, from inclusive and to exclusive, so a stamp in
// the second one note is written falls in exactly one window.
func TestKeptSinceWindowsMeetWithoutAGapOrOverlap(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).Unix()
	setKeptAt(t, dir, s1, at)
	edge := time.Unix(at, 0)
	first, _ := KeptSince(dir, edge.Add(-time.Hour), edge)
	second, _ := KeptSince(dir, edge, edge.Add(time.Hour))
	if len(first)+len(second) != 1 || len(second) != 1 {
		t.Errorf("a stamp on the boundary: first window %d, second %d; want it once, in the second", len(first), len(second))
	}
}

func setKeptAt(t *testing.T, dir, path string, at int64) {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := m.Files[path]
	if !f.Kept {
		t.Fatalf("%s is not kept: %+v", path, f)
	}
	f.KeptAt = at
	m.Files[path] = f
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
}

// An upgraded store with nothing new on disk reads as up to date, and the
// marker that starts dating was never written: the first deletion after the
// upgrade went undated. The pass that finds the marker missing writes it.
func TestKeptSinceDatesTheFirstDeletionAfterAnUpgradeWithNothingNew(t *testing.T) {
	dir, _, s1, _ := seedTwoTranscripts(t)
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.KeptDated = false
	if err := writeManifestOnly(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s1); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "claude", false, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := KeptSince(dir, time.Unix(0, 0), farFuture); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("the first deletion after the upgrade, KeptSince = %+v; want s1", got)
	}
}

// The tokenizer as it was before the keyer (#5567), kept as the oracle the
// keyer has to agree with key for key.

func legacyTokens(s string) []string {
	// Fold NFD to NFC so an accented word keys the same whether it was typed or
	// stored decomposed. A combining mark is category Mn, not a letter, so the
	// tokenizer below would otherwise split "café" (NFD) into "cafe" and drop
	// the accent, keying it apart from the precomposed "café" (#1098). Both the
	// stored text (indexKeys) and the query (queryKeys) pass through here, so
	// the fold is symmetric and the two sides always meet on one key.
	s = nfcfold.Compose(s)
	seen := map[string]bool{}
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() >= 2 {
			t := b.String()
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
		b.Reset()
	}
	for _, r := range query.Lower(s) {
		// A combining mark continues the word it sits on. Latin NFD is already
		// composed away above, but Arabic harakat, Hebrew niqqud, Thai vowel
		// signs and the Indic matras have no precomposed form, so the mark
		// ended the token: كَتَبَ was indexed as three one-letter tokens and
		// हिन्दी as two, and the words themselves were not in the index at all
		// (#1941). A mark cannot start a token, so a stray one still
		// separates.
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' ||
			(b.Len() > 0 && isMark(r)) {
			b.WriteRune(r)
			if b.Len() > 64 {
				flush()
			}
		} else {
			flush()
		}
	}
	flush()
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func legacyIdentifierParts(s string) []string {
	var out []string
	var word []rune
	flushWord := func() {
		if len(word) >= 6 {
			legacySplitCompound(word, &out)
		}
		word = word[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			word = append(word, r)
			if len(word) > 64 {
				flushWord()
			}
			continue
		}
		flushWord()
	}
	flushWord()
	return out
}

func legacySplitCompound(word []rune, out *[]string) {
	start := 0
	boundaries := 0
	emit := func(end int) {
		if end-start >= 3 {
			*out = append(*out, query.Lower(string(word[start:end])))
		}
		start = end
	}
	for i := 1; i < len(word); i++ {
		c, p := word[i], word[i-1]
		switch {
		case c == '_' || c == '-':
			emit(i)
			start = i + 1
			boundaries++
		case unicode.IsUpper(c) && (unicode.IsLower(p) || unicode.IsDigit(p)):
			// getUser | getUserById: hump boundary
			emit(i)
			boundaries++
		case unicode.IsLower(c) && unicode.IsUpper(p) && i-1 > start:
			// JSONData -> JSON | Data: break before the last upper
			emit(i - 1)
			boundaries++
		}
	}
	if boundaries > 0 {
		emit(len(word))
	}
}

func legacyCJKIndexKeys(s string, emit func(tok string)) {
	// A CJK rune is never a single UTF-8 byte, so this guard preserves the
	// cjkBigrams fast path without decoding an ASCII-only message.
	ascii := true
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return
	}

	seen := map[uint64]struct{}{}
	emitFolded := func(r1, r2 rune) {
		// Grammar earns no posting. A pair of function runes — 的了, 在哪,
		// 什么 — is the CJK counterpart of a stop word: the query side has
		// dropped it from the term list since it was written, so the postings
		// were carrying the most frequent pairs in the language for nothing
		// (#492). queryKeys drops them from the AND for the same reason.
		if r1 != 0 && query.CJKFunctionRune(r1) && query.CJKFunctionRune(r2) {
			return
		}
		key := (uint64(r1) << 21) | uint64(r2) //nolint:gosec // runes are non-negative
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}

		n := 1 + utf8.RuneLen(r2)
		if r1 != 0 {
			n += utf8.RuneLen(r1)
		}
		tok := make([]byte, n)
		tok[0] = 't'
		pos := 1
		if r1 != 0 {
			pos += utf8.EncodeRune(tok[pos:], r1)
		}
		utf8.EncodeRune(tok[pos:], r2)
		emit(string(tok))
	}

	var previous rune
	runLength := 0
	flush := func() {
		if runLength == 1 {
			// Zero is only the packed-key marker for a unigram. It is never
			// written into the emitted token.
			emitFolded(0, previous)
		}
		runLength = 0
	}

	for _, r := range s {
		if !cjkfold.Unspaced(r) {
			flush()
			continue
		}

		r = cjkfold.Rune(r)
		if runLength == 0 {
			previous = r
			runLength = 1
			continue
		}

		emitFolded(previous, r)
		previous = r
		runLength = 2
	}
	flush()
}

func legacyTextKeys(text string) []string {
	var out []string
	for _, tok := range legacyTokens(text) {
		out = append(out, "t"+tok)
	}
	for _, part := range legacyIdentifierParts(text) {
		out = append(out, "t"+part)
	}
	legacyCJKIndexKeys(text, func(tok string) { out = append(out, tok) })
	return out
}

// keyerTexts covers what the tokenizer branches on: case, ё, NFD and marks
// with no precomposed form, variation selectors, identifiers, words cut at 64
// bytes, CJK runs, fullwidth and halfwidth forms, and bytes that are not UTF-8.
func keyerTexts() []string {
	texts := []string{ //nolint:prealloc // a fixture literal the loop below grows
		"",
		"a",
		"Go build FAILED: undefined: zorbleFrobnicate in getUserProfileById",
		"refresh_token_rotation JSONDataParser HTTPServer2Go x-y-z_abc",
		"Зелёный ЁлкаПраздник café café Ελληνικά",
		"كَتَبَ हिन्दी ไทยภาษา",
		"widget️ widget\U000E0100 x́",
		strings.Repeat("abcdefghij", 9) + " " + strings.Repeat("Ab", 40) + " " + strings.Repeat("ё", 40),
		strings.Repeat("я", 32) + "́́" + strings.Repeat("ж", 3),
		"韓國簽證 韩国签证 这是什么问题 中国个人 日本語のテスト 한국어 텍스트",
		"ｰﾞﾟ ＡＢＣ ｱｲｳ 中a国 𠀀𠀁 中",
		"bad \xff\xfe bytes\xc3 mid\xe4\xb8word ok",
		"--- FAIL: TestFoo (0.00s)\n\tfoo_test.go:12: got 1, want 2\nexit status 1",
		"İstanbul ǅemal ẞtraße ΣΊΣΥΦΟΣ",
	}
	r := rand.New(rand.NewSource(5567)) //nolint:gosec // a seeded fixture, not crypto
	alphabet := []string{"a", "Z", "_", "-", " ", "ё", "Ё", "é", "́", "中", "國", "的", "了", "ｱ", "\xff", "1", "️", "ب", "َ", "\n", "Σ", "ß"}
	for range 2000 {
		var b strings.Builder
		for n := r.Intn(120); n > 0; n-- {
			b.WriteString(alphabet[r.Intn(len(alphabet))])
		}
		texts = append(texts, b.String())
	}
	return texts
}

// The keyer is the tokenizer written to reuse its buffers; it must cut the
// same words, parts and bigrams the string version did, and tokens must come
// out in the same order, since the cooccur sidecar interns them in it.
func TestKeyerEmitsTheKeysTheStringTokenizerDid(t *testing.T) {
	for _, text := range keyerTexts() {
		if got, want := tokens(text), legacyTokens(text); !slices.Equal(got, want) {
			t.Fatalf("tokens(%q) = %q, want %q", text, got, want)
		}
		if got, want := identifierParts(text), legacyIdentifierParts(text); !slices.Equal(got, want) {
			t.Fatalf("identifierParts(%q) = %q, want %q", text, got, want)
		}
		var gotCJK, wantCJK []string
		cjkIndexKeys(text, func(tok string) { gotCJK = append(gotCJK, tok) })
		legacyCJKIndexKeys(text, func(tok string) { wantCJK = append(wantCJK, tok) })
		if !slices.Equal(gotCJK, wantCJK) {
			t.Fatalf("cjkIndexKeys(%q) = %q, want %q", text, gotCJK, wantCJK)
		}
		got, want := distinctSorted(indexKeys(text)), distinctSorted(legacyTextKeys(text))
		if !slices.Equal(got, want) {
			t.Fatalf("indexKeys(%q) = %q, want %q", text, got, want)
		}
	}
}

// One keyer keys a whole worker's share of the store, so nothing a message
// leaves in its buffers may reach the next one's keys.
func TestAKeyerCarriesNothingFromOneMessageToTheNext(t *testing.T) {
	var k keyer
	months := []time.Time{
		time.Date(2026, 5, 31, 23, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 1, 0, 0, 0, time.FixedZone("x", 3*3600)),
		{},
		time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	for i, text := range keyerTexts() {
		when := months[i%len(months)]
		var got []string
		k.keys(text, when, func(b []byte) { got = append(got, string(b)) })
		want := legacyTextKeys(text)
		want = append(want, dateTokens(when)...)
		if g, w := distinctSorted(got), distinctSorted(want); !slices.Equal(g, w) {
			t.Fatalf("keys(%q, %v) = %q, want %q", text, when, g, w)
		}
	}
}

func distinctSorted(keys []string) []string {
	out := slices.Clone(keys)
	sort.Strings(out)
	return slices.Compact(out)
}

// Keying a message used to allocate a string per word, a builder per word, a
// lowered copy of the text and a map: on a 12k-session store the workers spent
// a fifth of their CPU on the heap lock that garbage takes (#5567). A word the
// batch has already seen costs nothing now.
func TestSpillWorkersAllocateNothingPerRepeatedWord(t *testing.T) {
	jobs := make([]tokenJob, 0, 20000)
	when := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	off := int64(0)
	for i := range 20000 {
		text := fmt.Sprintf("The build FAILED on runSpillWorker: undefined zorbleFrobnicate in package main, step %d of the deploy pipeline 日本語のテスト", i%50)
		jobs = append(jobs, tokenJob{text: text, offset: off, sid: uint32(i/40 + 1), when: when})
		off += int64(len(text))
	}
	sp, err := newSpiller(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sp.cleanup()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := sp.run(func(push func(tokenJob)) error {
		for _, j := range jobs {
			push(j)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	// About twenty keys a message; the bound leaves room for the batches, the
	// spill buffers and a copy of each key per batch.
	per := float64(after.Mallocs-before.Mallocs) / float64(len(jobs))
	t.Logf("%.2f allocations per message", per)
	if per > 2 {
		t.Fatalf("%.1f allocations per message keyed", per)
	}
}

// The same index has to answer the same way twice. last_error was whichever
// failing file Go's map iteration reached last, so `doctor --json` reported a
// different error each run and a script diffing the report saw a change where
// nothing changed (#2245).
func TestTheReportedErrorIsTheSameOnEveryRun(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	proj := filepath.Join(root, "projects", "-work-a")
	files := map[string]FileIngest{
		filepath.Join(proj, "aaa.jsonl"): {Error: "open aaa.jsonl: permission denied"},
		filepath.Join(proj, "bbb.jsonl"): {Error: "unexpected end of JSON input"},
		filepath.Join(proj, "ccc.jsonl"): {Error: "open ccc.jsonl: is a directory"},
	}

	first := healthFromFiles(files)["claude"]
	// The premise: three failures folded into one store, or one answer proves
	// nothing.
	if first.FailedFiles != 3 {
		t.Fatalf("%d failed files counted, want 3", first.FailedFiles)
	}
	for i := range 200 {
		if got := healthFromFiles(files)["claude"].LastError; got != first.LastError {
			t.Fatalf("run %d reported %q where the first run reported %q", i+2, got, first.LastError)
		}
	}
	// And it is the one the field claims: the first failing path, so two
	// machines reading the same index quote the same file.
	if want := "open aaa.jsonl: permission denied"; first.LastError != want {
		t.Errorf("reported %q, want the first failing path's error %q", first.LastError, want)
	}
}

// forkLineageStore indexes a cline source "a" that went on after its declared
// fork "f", a fork "g" of a that holds a's last turn, a sub-agent "x" of f, a
// sub-agent "y" of g, an undeclared copy "u" of a, a fork "h" of a copied with
// new times, and a dsh root with a child.
func forkLineageStore(t *testing.T) (string, func(role, text string, i int) model.Message) {
	t.Helper()
	at := time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)
	turn := func(role, text string, i int) model.Message {
		return model.Message{Role: role, Text: text, Time: at.Add(time.Duration(i) * time.Second)}
	}
	open := []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1)}
	with := func(extra ...model.Message) []model.Message {
		return append(append([]model.Message{}, open...), extra...)
	}
	ss := []model.Session{
		{Harness: "cline", ID: "a", Project: "p", Messages: with(turn("user", "source work after the fork: add jitter", 5))},
		{Harness: "cline", ID: "f", Project: "p", Kind: "fork", Parent: "a", Messages: with(turn("user", "fork work", 6))},
		{Harness: "cline", ID: "g", Project: "p", Kind: "fork", Parent: "a", Messages: with(turn("user", "source work after the fork: add jitter", 5), turn("user", "later fork work", 9))},
		{Harness: "cline", ID: "u", Project: "p", Messages: with(turn("user", "undeclared fork work", 8))},
		{Harness: "cline", ID: "x", Project: "p", Kind: "subagent", Parent: "f", Messages: []model.Message{turn("user", "read the code", 7)}},
		{Harness: "cline", ID: "y", Project: "p", Kind: "subagent", Parent: "g", Messages: []model.Message{turn("user", "read the tests", 10)}},
		// A Hermes /branch copies with new times, after the source's last turn.
		{Harness: "cline", ID: "h", Project: "p", Kind: "fork", Parent: "a", Messages: []model.Message{
			turn("user", "the retry loop spins", 12), turn("assistant", "backoff never resets", 13),
			turn("user", "source work after the fork: add jitter", 14), turn("user", "branch work", 15)}},
		{Harness: "dsh", ID: "root", Project: "p", Messages: []model.Message{turn("user", "plan the release", 20)}},
		{Harness: "dsh", ID: "child", Project: "p", Kind: "subagent", Parent: "root", Messages: []model.Message{turn("user", "draft the notes", 21)}},
	}
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir, turn
}

// A declared fork leaves out its source only when it holds the source's last
// turn, the rule an undeclared copy already gets: a source that went on after
// the fork has work the fork never saw (#5293).
func TestLineageKeepsADeclaredForksSourceThatWentOn(t *testing.T) {
	dir, turn := forkLineageStore(t)
	cases := []struct {
		name string
		got  map[string]bool
		want []string
	}{
		{"declared fork f, source went on", Lineage(dir, map[string]bool{"f": true}), []string{"f", "x"}},
		{"declared fork g, holds the source's end", Lineage(dir, map[string]bool{"g": true}), []string{"a", "g", "y"}},
		{"fork h copied with new times after a's last turn", Lineage(dir, map[string]bool{"h": true}), []string{"a", "h"}},
		{"control, undeclared copy u", Lineage(dir, map[string]bool{"u": true}), []string{"u"}},
		// Read whole before any build: the same rule.
		{"whole fork of a, source went on", LineageWhole(dir, map[string]bool{"w": true}, model.Session{Harness: "cline", ID: "w", Kind: "fork", Parent: "a",
			Messages: []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1), turn("user", "whole fork work", 11)}}), []string{"w"}},
		{"whole fork of a, holds its end", LineageWhole(dir, map[string]bool{"w": true}, model.Session{Harness: "cline", ID: "w", Kind: "fork", Parent: "a",
			Messages: []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1), turn("user", "source work after the fork: add jitter", 5), turn("user", "whole fork work", 11)}}), []string{"a", "w"}},
	}
	for _, c := range cases {
		if got := lineageKeys(c.got); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The ancestor walk goes on through a fork and starts from a parent only an
// unindexed asker names: a sub-agent of a fork gets what the fork gets, and a
// new dsh grandchild leaves out the root the way its indexed parent does
// (#5305).
func TestLineageWalksThroughAForkAndAnUnindexedParent(t *testing.T) {
	dir, turn := forkLineageStore(t)
	grand := model.Session{Harness: "dsh", ID: "grand", Kind: "subagent", Parent: "child", Messages: []model.Message{turn("user", "check the links", 30)}}
	cases := []struct {
		name string
		got  map[string]bool
		want []string
	}{
		{"sub-agent y of g, which holds a's end", Lineage(dir, map[string]bool{"y": true}), []string{"a", "g", "y"}},
		{"sub-agent x of f, whose source went on", Lineage(dir, map[string]bool{"x": true}), []string{"f", "x"}},
		{"unindexed grandchild", Lineage(dir, map[string]bool{"grand": true}, grand), []string{"child", "grand", "root"}},
		{"control, indexed child", Lineage(dir, map[string]bool{"child": true}), []string{"child", "root"}},
	}
	for _, c := range cases {
		if got := lineageKeys(c.got); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A source left on the page for a fork is cut at the last turn they share,
// and one left out is not cut at all (#5335).
func TestLineageCutsASourceThatWentOnAtTheForkPoint(t *testing.T) {
	dir, turn := forkLineageStore(t)
	shared := turn("assistant", "", 1).Time
	for _, c := range []struct {
		name string
		ids  map[string]bool
	}{{"declared fork f", map[string]bool{"f": true}}, {"undeclared copy u", map[string]bool{"u": true}}} {
		out, cut := LineageCut(dir, c.ids)
		if out["a"] || !cut["a"].Equal(shared) {
			t.Errorf("%s: a hidden %v, cut %v, want kept and cut at %v", c.name, out["a"], cut["a"], shared)
		}
	}
	w := model.Session{Harness: "cline", ID: "w", Kind: "fork", Parent: "a",
		Messages: []model.Message{turn("user", "the retry loop spins", 0), turn("assistant", "backoff never resets", 1), turn("user", "whole fork work", 11)}}
	if _, cut := LineageWholeCut(dir, map[string]bool{"w": true}, w); !cut["a"].Equal(shared) {
		t.Errorf("whole fork: cut %v, want %v", cut["a"], shared)
	}
	if out, cut := LineageCut(dir, map[string]bool{"g": true}); !out["a"] || !cut["a"].IsZero() {
		t.Errorf("g holds a's end: a hidden %v, cut %v, want hidden and no cut", out["a"], cut["a"])
	}
}

// A fork copies its source's turns with their times, so the two open alike;
// a session that asked the same thing at another moment does not (#4549).
func TestSessionOpeningMatchesACopyOnly(t *testing.T) {
	at := time.Date(2026, 10, 2, 7, 0, 0, 123e6, time.UTC)
	open := func(id string, t time.Time, text string) model.Session {
		return model.Session{Harness: "claude", ID: id, Messages: []model.Message{
			{Role: "assistant", Text: "hello", Time: t.Add(-time.Second)},
			{Role: "user", Text: text, Time: t},
		}}
	}
	src := SessionOpening(open("a", at, "the retry loop spins"))
	if src == 0 {
		t.Fatal("no opening for a session with a timed user turn")
	}
	if got := SessionOpening(open("f", at, "the retry loop spins")); got != src {
		t.Errorf("a copy opens differently: %x vs %x", got, src)
	}
	if got := SessionOpening(open("b", at.Add(time.Millisecond), "the retry loop spins")); got == src {
		t.Error("the same question a millisecond later reads as a copy")
	}
	if got := SessionOpening(open("c", at, "another question")); got == src {
		t.Error("another question at the same moment reads as a copy")
	}
	if got := SessionOpening(model.Session{Harness: "claude", Messages: []model.Message{{Role: "user", Text: "untimed"}}}); got != 0 {
		t.Errorf("an untimed turn has an opening: %x", got)
	}
}

// dsh delegates three deep and a grandchild names the child between them: the
// root's lineage takes in every descendant, a grandchild's every ancestor, and
// a cycle in bad data ends.
func TestSpawnedFamilyWalksEveryDepth(t *testing.T) {
	m := Manifest{Sessions: map[string]SessionMeta{
		"root":  {ID: "root"},
		"child": {ID: "child", Kind: "subagent", Parent: "root"},
		"grand": {ID: "grand", Kind: "subagent", Parent: "child"},
		"fork":  {ID: "fork", Kind: "fork", Parent: "root"},
		"loopa": {ID: "loopa", Kind: "subagent", Parent: "loopb"},
		"loopb": {ID: "loopb", Kind: "subagent", Parent: "loopa"},
		"self":  {ID: "self", Kind: "subagent", Parent: "self"},
	}}
	out := map[string]bool{}
	own := spawnedFamily(m, map[string]bool{"root": true}, out, nil)
	if !own["child"] || !own["grand"] || own["fork"] || own["root"] || len(out) != 0 {
		t.Errorf("root: own %v, ancestors %v", own, out)
	}
	out = map[string]bool{}
	own = spawnedFamily(m, map[string]bool{"grand": true}, out, nil)
	if !out["child"] || !out["root"] || len(own) != 0 {
		t.Errorf("grand: own %v, ancestors %v", own, out)
	}
	out = map[string]bool{}
	own = spawnedFamily(m, map[string]bool{"loopa": true, "self": true}, out, nil)
	if !out["loopb"] || !own["loopb"] || own["self"] {
		t.Errorf("cycle: own %v, ancestors %v", own, out)
	}
}

// Read paths must never wait out a rebuild. The session-start hook goes
// through these, and an index-format upgrade puts every user behind exactly
// this rebuild on their first session — twelve seconds of an agent that looks
// hung. A blocking lock here is a regression even though nothing fails.
func TestReadsDoNotBlockWhileTheIndexIsLocked(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Logf("build: %v", err) // an empty machine is fine: the lock is what matters
	}
	unlock, err := lockDir(dir)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = FindByPrefix(dir, "nothing")
		_, _, _, _, _ = ProjectRelevant(dir, []string{"tmp/p"}, []string{"term"}, 3)
		_, _ = RecentProject(dir, "p", 3)
		_, _, _ = FirstMatch(dir, []string{"term"}, 3)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a read waited for the index lock: on a real corpus that is the length of a rebuild")
	}
}
