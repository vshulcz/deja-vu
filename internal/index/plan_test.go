package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// planWall is a friction line by isFriction's rules: it names something
// specific, carries no quote or comment marker, and sits inside the length
// window. Its identifier is the only term the plan and the wall share.
const planWall = "ModuleNotFoundError: No module named pytest_asyncio"

// planCorpus builds a store where term frequency is set deliberately, because
// the two-tier floor is a claim about frequency and shape together:
//
//	pytest_asyncio  identifier, 20 of 59 sessions -> idf 1.05
//	cache, queue    plain,      20 of 59 sessions -> idf 1.05
//	stale, flush    plain,       4 of 59 sessions -> idf 2.48
//
// So the identifier and the plain pair at 20 differ only in shape, and the
// plain pair at 4 shows a plain term still has a route through.
//
// The wall is in the first four sessions, which is FrictionMinSessions plus
// one, and commands is how many of those also log a command record.
func planCorpus(t *testing.T, commands int) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "plan-index")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	var sessions []model.Session
	for i := range 59 {
		s := model.Session{
			ID:      "s" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
			Harness: "claude",
			Project: "p",
			Updated: base.Add(time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "session about unrelated frontend styling work"},
			},
		}
		if i < 20 {
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "pytest_asyncio cache queue setup for the suite",
			})
		}
		if i >= 20 {
			// hydration never shares a session with stale, and purge_cache is
			// an identifier that only these four hold — both are here so a
			// term's reach can be checked against sessions that carry no wall.
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "hydration pass on the client",
			})
		}
		if i >= 20 && i < 24 {
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "purge_cache wipe between the runs",
			})
		}
		if i < 4 {
			s.Messages = append(s.Messages,
				model.Message{Role: "user", Text: "stale flush before the run"},
				model.Message{Role: "tool-output", Text: planWall},
			)
		}
		if i < commands {
			s.Messages = append(s.Messages, model.Message{
				Role: "command", Text: "pip install pytest_asyncio",
			})
		}
		sessions = append(sessions, s)
	}

	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

func planSteps(terms ...string) [][]string { return [][]string{terms} }

func TestPlanFrictionMatchesReportsAWallSharingAPlanTerm(t *testing.T) {
	dir := planCorpus(t, 0)
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if len(out) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(out), out)
	}
	if out[0].Wall.Text != planWall {
		t.Fatalf("wall text = %q", out[0].Wall.Text)
	}
	if len(out[0].Wall.Sessions) != 4 {
		t.Fatalf("carrier sessions = %d, want 4", len(out[0].Wall.Sessions))
	}
	// No command record exists here, and the finding still stands. The command
	// clause is a strengthening, not a precondition — 84 of 110 carrier
	// sessions in the census that motivated this hold no command at all.
	if out[0].Command != "" {
		t.Fatalf("command invented from a store with none: %q", out[0].Command)
	}
}

// The wall naming a plan term is the whole claim. A plan term that is in the
// index but absent from the wall text has nothing to say about that wall.
func TestPlanFrictionMatchesStaysSilentWhenTheWallNamesNothingInThePlan(t *testing.T) {
	dir := planCorpus(t, 0)
	// "stale flush" is in all four carrier sessions and clears the plain
	// floor, so the step survives selection and the sessions are candidates.
	// Only the wall text check stands between it and a finding.
	if steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("stale", "flush")); len(steps) != 1 {
		t.Fatalf("premise broken: step did not survive selection (%d steps)", len(steps))
	}
	if out := PlanFrictionMatches(dir, planSteps("stale", "flush"), nil, 0); out != nil {
		t.Fatalf("reported a wall that names no plan term: %+v", out)
	}
}

// keep runs before any session is materialized, so a denied project's text
// must not reach the caller.
func TestPlanFrictionMatchesRespectsTheActivationKeep(t *testing.T) {
	dir := planCorpus(t, 0)
	seen := 0
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), func(SessionMeta) bool {
		seen++
		return false
	}, 0)
	if out != nil {
		t.Fatalf("denied project crossed the boundary: %+v", out)
	}
	if seen == 0 {
		t.Fatal("keep was never consulted")
	}
}

// A plan is submitted at a moment when nothing may block on indexing, so a
// missing index is a silent miss and never a build.
func TestPlanFrictionMatchesNeverBuildsAnIndex(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "empty-index")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0); out != nil {
		t.Fatalf("findings from an empty index: %+v", out)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("index directory was written to: %v", entries)
	}
}

func TestPlanFrictionMatchesReportsACommandWhenOneWasLogged(t *testing.T) {
	dir := planCorpus(t, 2)
	// Two steps, and only the first one reaches the wall's carriers or is
	// named by the wall text. The second must contribute nothing rather than
	// borrow the first step's command.
	steps := [][]string{{"pytest_asyncio"}, {"purge_cache"}}
	out := PlanFrictionMatches(dir, steps, nil, 0)
	if len(out) != 1 {
		t.Fatalf("findings = %d, want 1", len(out))
	}
	if out[0].Command != "pip install pytest_asyncio" {
		t.Fatalf("command = %q", out[0].Command)
	}
	if len(out[0].CommandSessions) != 2 {
		t.Fatalf("command sessions = %d, want the 2 that logged it", len(out[0].CommandSessions))
	}
}

// A plan term that reaches only sessions carrying no wall is not a finding:
// the join is wall-to-plan, and half of it missing is silence.
func TestPlanFrictionMatchesStaysSilentWhenNoWallCarrierMatches(t *testing.T) {
	dir := planCorpus(t, 0)
	// purge_cache is indexed and clears the floor, but the four sessions that
	// hold it are not the four that hit the wall.
	if steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("purge_cache")); len(steps) != 1 {
		t.Fatalf("premise broken: purge_cache did not survive selection")
	}
	if out := PlanFrictionMatches(dir, planSteps("purge_cache"), nil, 0); out != nil {
		t.Fatalf("wall reported for a term no carrier holds: %+v", out)
	}
}

// A step's terms have to land on one session together, not merely exist.
func TestPlanIndexedStepsNeedsTermsInTheSameSession(t *testing.T) {
	dir := planCorpus(t, 0)
	// Both clear the plain floor at 4 sessions each, and neither session set
	// overlaps the other, so no session holds the two the step needs.
	steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("stale", "wipe"))
	if len(steps) != 0 {
		t.Fatalf("step survived with its terms in disjoint sessions: %+v", steps)
	}
}

func TestPlanFrictionMatchesIgnoresEmptyInput(t *testing.T) {
	dir := planCorpus(t, 0)
	if out := PlanFrictionMatches(dir, nil, nil, 0); out != nil {
		t.Fatalf("no steps produced %+v", out)
	}
	if out := PlanFrictionMatches(dir, planSteps("", "   "), nil, 0); out != nil {
		t.Fatalf("blank terms produced %+v", out)
	}
}

// Wall texts for the ordering corpus. All five name the same identifier, so
// which ones come back — and in what order — is decided by the clustering
// alone.
const (
	wallFive     = planWall
	wallLater    = "connection refused fetching pytest_asyncio from the mirror"
	wallTieOne   = "ImportError: cannot find module pytest_asyncio in the venv"
	wallTieTwo   = "fatal: pytest_asyncio plugin refused to load under the runner"
	wallTooRare  = "permission denied while installing pytest_asyncio locally"
	planWallSeen = 5
)

// planOrderCorpus spreads five walls over overlapping session sets so the
// ordering rule is observable: more carriers first, then the newest carrier,
// then the hash. wallTooRare is under FrictionMinSessions and must not appear
// at all.
//
//	wallFive     sessions 0-4   5 carriers
//	wallLater    sessions 3-5   3 carriers, newest is session 5
//	wallTieOne   sessions 0-2   3 carriers, newest is session 2
//	wallTieTwo   sessions 0-2   3 carriers, the same sessions as wallTieOne
//	wallTooRare  sessions 0-1   2 carriers
func planOrderCorpus(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "plan-order-index")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	walls := map[string][2]int{
		wallFive: {0, 5}, wallLater: {3, 6},
		wallTieOne: {0, 3}, wallTieTwo: {0, 3}, wallTooRare: {0, 2},
	}

	var sessions []model.Session
	for i := range 59 {
		s := model.Session{
			ID:      "o" + string(rune('A'+i/26)) + string(rune('a'+i%26)),
			Harness: "claude",
			Project: "p",
			Updated: base.Add(time.Duration(i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "session about unrelated frontend styling work"},
			},
		}
		if i < 20 {
			s.Messages = append(s.Messages, model.Message{
				Role: "user", Text: "pytest_asyncio setup for the suite",
			})
		}
		// Sorted so the records land in a stable order run to run.
		for _, text := range []string{wallFive, wallLater, wallTieOne, wallTieTwo, wallTooRare} {
			span := walls[text]
			if i >= span[0] && i < span[1] {
				s.Messages = append(s.Messages, model.Message{Role: "tool-output", Text: text})
			}
		}
		sessions = append(sessions, s)
	}

	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPlanFrictionMatchesOrdersWallsAndDropsTheRareOne(t *testing.T) {
	dir := planOrderCorpus(t)
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if len(out) != 4 {
		t.Fatalf("findings = %d, want 4 (wallTooRare excluded): %+v", len(out), out)
	}
	for _, hit := range out {
		if hit.Wall.Text == wallTooRare {
			t.Fatal("a wall in 2 sessions was reported as recurring")
		}
	}
	if out[0].Wall.Text != wallFive || len(out[0].Wall.Sessions) != planWallSeen {
		t.Fatalf("most-carried wall not first: %q with %d sessions",
			out[0].Wall.Text, len(out[0].Wall.Sessions))
	}
	// Equal carrier counts, so the newer cluster wins.
	if out[1].Wall.Text != wallLater {
		t.Fatalf("newest of the three-carrier walls not second: %q", out[1].Wall.Text)
	}
	// wallTieOne and wallTieTwo share their carriers exactly, so only the hash
	// separates them — the order is fixed, which one wins is not asserted.
	rest := map[string]bool{out[2].Wall.Text: true, out[3].Wall.Text: true}
	if !rest[wallTieOne] || !rest[wallTieTwo] {
		t.Fatalf("tied walls missing: %q, %q", out[2].Wall.Text, out[3].Wall.Text)
	}
	again := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 0)
	if again[2].Wall.Text != out[2].Wall.Text {
		t.Fatal("tie between identical clusters is not stable")
	}
}

func TestPlanFrictionMatchesStopsAtTheLimit(t *testing.T) {
	dir := planOrderCorpus(t)
	out := PlanFrictionMatches(dir, planSteps("pytest_asyncio"), nil, 2)
	if len(out) != 2 {
		t.Fatalf("limit 2 returned %d", len(out))
	}
	// A limit keeps the front of the order, so it drops the weakest walls.
	if out[0].Wall.Text != wallFive || out[1].Wall.Text != wallLater {
		t.Fatalf("limit changed the order: %q, %q", out[0].Wall.Text, out[1].Wall.Text)
	}
}

// An empty dir means the default index, which the hermetic env points at a
// path that was never built.
func TestPlanFrictionMatchesFallsBackToTheDefaultDir(t *testing.T) {
	_ = hermeticIndexEnv(t)
	if out := PlanFrictionMatches("", planSteps("pytest_asyncio"), nil, 0); out != nil {
		t.Fatalf("findings from an unbuilt default index: %+v", out)
	}
}

func TestPlanTermSessionsIntersectsEveryKey(t *testing.T) {
	dir := planCorpus(t, 0)
	queue := planTermSessions(dir, "queue")
	if len(queue) == 0 {
		t.Fatal("premise broken: queue is not in the index")
	}
	// styling is in every session, so the pair is bounded by the narrower key
	// rather than the union of the two.
	if both := planTermSessions(dir, "styling queue"); len(both) != len(queue) {
		t.Fatalf("pair reached %d sessions, queue alone reaches %d", len(both), len(queue))
	}
	// hydration and stale are each indexed, and never in one session.
	if got := planTermSessions(dir, "hydration stale"); len(got) != 0 {
		t.Fatalf("terms that never co-occur intersected to %d sessions", len(got))
	}
	if got := planTermSessions(dir, "-"); got != nil {
		t.Fatalf("a term with no searchable key reached %d sessions", len(got))
	}
}

func mustPlanManifest(t *testing.T, dir string) Manifest {
	t.Helper()
	manifest, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

// The central claim of the two-tier floor: at one frequency, shape decides.
// Wall vocabulary is repeated-work vocabulary and so is common by
// construction, which is why the prompt floor at 2.0 excluded every one of
// these terms.
func TestPlanIndexedStepsGivesIdentifiersALowerFloor(t *testing.T) {
	dir := planCorpus(t, 0)
	manifest := mustPlanManifest(t, dir)

	// Same 20 sessions, so the same idf. Only the shape differs.
	identifier, _, needs := planIndexedSteps(dir, manifest, planSteps("pytest_asyncio"))
	if len(identifier) != 1 || len(identifier[0]) != 1 || identifier[0][0].text != "pytest_asyncio" {
		t.Fatalf("identifier term dropped: %+v", identifier)
	}
	if needs[0] != 1 {
		t.Fatalf("identifier step need = %d, want 1", needs[0])
	}
	if plain, _, _ := planIndexedSteps(dir, manifest, planSteps("cache", "queue")); len(plain) != 0 {
		t.Fatalf("plain terms at the same frequency survived: %+v", plain)
	}

	// And the plain floor is a floor, not a ban: rarer plain terms clear it.
	rare, _, rareNeeds := planIndexedSteps(dir, manifest, planSteps("stale", "flush"))
	if len(rare) != 1 || len(rare[0]) != 2 {
		t.Fatalf("rare plain terms dropped: %+v", rare)
	}
	if rareNeeds[0] != 2 {
		t.Fatalf("plain step need = %d, want 2", rareNeeds[0])
	}
}

func TestPlanIndexedStepsSkipsTermsTheIndexNeverSaw(t *testing.T) {
	dir := planCorpus(t, 0)
	steps, _, _ := planIndexedSteps(dir, mustPlanManifest(t, dir), planSteps("zzz_unseen_identifier"))
	if len(steps) != 0 {
		t.Fatalf("term absent from the index survived: %+v", steps)
	}
}

func TestPlanIsIdentifierTerm(t *testing.T) {
	for _, term := range []string{"pytest", "hermes-agent", "v1.2", "a_b", "go/mod", "x9"} {
		if !planIsIdentifierTerm(term) {
			t.Errorf("%q not identifier-shaped", term)
		}
	}
	for _, term := range []string{"cache", "queue", "stale", "the", ""} {
		if planIsIdentifierTerm(term) {
			t.Errorf("%q read as identifier-shaped", term)
		}
	}
	if !planHasIdentifierTerm([]string{"the", "cache", "pytest_asyncio"}) {
		t.Error("step with one identifier read as plain")
	}
	if planHasIdentifierTerm([]string{"the", "cache", "queue"}) {
		t.Error("plain step read as carrying an identifier")
	}
}

func TestPlanTextHasTerm(t *testing.T) {
	if !planTextHasTerm(planWall, "pytest_asyncio") {
		t.Error("wall does not match its own identifier")
	}
	if planTextHasTerm(planWall, "cache") {
		t.Error("wall matched a term it does not contain")
	}
	if planTextHasTerm(planWall, "   ") {
		t.Error("blank term matched")
	}
}

func TestPlanCommandForStepPrefersTheStrongestThenShortest(t *testing.T) {
	terms := []planIndexedTerm{{text: "pytest"}, {text: "asyncio"}}
	session := model.Session{Messages: []model.Message{
		{Role: "user", Text: "pytest asyncio talk, not a command"},
		{Role: "command", Text: "   \t  "},
		{Role: "command", Text: "pytest -q"},
		{Role: "command", Text: "  pip   install   pytest   asyncio  "},
		{Role: "command", Text: "pip install pytest asyncio --upgrade"},
	}}
	// Two terms beat one, and among equals the shorter command wins. Fields
	// collapsing is what makes the padded line the shorter of the two.
	if got := planCommandForStep(session, terms, 2); got != "pip install pytest asyncio" {
		t.Fatalf("command = %q", got)
	}
	// need is a floor: nothing clears 3 matches here.
	if got := planCommandForStep(session, terms, 3); got != "" {
		t.Fatalf("command %q cleared an impossible need", got)
	}
}
