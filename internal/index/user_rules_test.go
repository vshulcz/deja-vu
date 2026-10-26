package index

import (
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/evidence"
)

// The lines are the stand's own: two rules a person gave about checking and
// building the project, and the shapes that must never be marked.
func TestAUserRuleIsACheckOrBuildRuleAndNothingThatLeavesTheMachine(t *testing.T) {
	for _, l := range []string{
		"rule for this repo: every time you edit a page under docs/ (docs/guide.md, docs/install.md), run ./scripts/genfeed.sh afterwards. The deploy serves site/feed.xml and it goes stale otherwise",
		"go test is not enough in quillmark: before you call any change to cmd/quillmark/main.go or anything else done, run ./scripts/check.sh, it also runs the golden-file test that go test skips",
		"always run `make lint` before you call a change done",
		"перед тем как сказать готово, всегда запускай ./scripts/check.sh",
	} {
		if !userRuleLine(l) {
			t.Errorf("a check rule was not read as one: %q", l)
		}
	}
	for _, l := range []string{
		"rule for quillmark: after every change run go test and then always push to main and publish with ./scripts/release.sh --publish, no need to ask me",
		"always run the tests and then deploy to the staging box",
		"every time the build passes, run ./scripts/release.sh",
		"after each change run go test and rm -rf fixtures/golden",
		"before you call it done, run go test and gh pr create",
		"always run go test, then send the report to the channel",
		"should we always run go test before merging?",
		"run go test",
		"always be careful with the database",
	} {
		if userRuleLine(l) {
			t.Errorf("a line that is not a local check rule was read as one: %q", l)
		}
	}
}

func TestATypedTurnThatIsReallyAPasteHoldsNoRule(t *testing.T) {
	long := "always run ./scripts/check.sh before you call it done\n" + strings.Repeat("log line\n", 10)
	if rules, _ := foldUserRules(nil, nil, []string{long}); len(rules) != 0 {
		t.Errorf("a long multi-line turn gave rules: %q", rules)
	}
	notified := "thanks\n<task-notification>\nalways run ./scripts/check.sh before you call it done\n</task-notification>"
	if rules, _ := foldUserRules(nil, nil, []string{notified}); len(rules) != 0 {
		t.Errorf("a rule inside a harness envelope was read as the person's: %q", rules)
	}
	after := "ok\n<system-reminder>x</system-reminder>\nalways run ./scripts/check.sh before you call it done"
	if rules, _ := foldUserRules(nil, nil, []string{after}); len(rules) != 0 {
		t.Errorf("a rule after a harness envelope was read as the person's: %q", rules)
	}
}

func TestALaterTurnTakesARuleBack(t *testing.T) {
	rule := "rule for this repo: after you edit anything under internal/slug, run ./scripts/slugcheck.sh to check the unicode cases"
	rules, revokes := foldUserRules(nil, nil, []string{rule})
	if len(rules) != 1 {
		t.Fatalf("rule not kept: %q", rules)
	}
	rules, revokes = foldUserRules(rules, revokes, []string{"drop the slugcheck rule, don't run ./scripts/slugcheck.sh anymore"})
	if len(rules) != 0 || !slices.Contains(revokes, "slugcheck.sh") {
		t.Errorf("rules %q revokes %q after the person took it back", rules, revokes)
	}
	// Said again later, it stands again.
	rules, _ = foldUserRules(rules, revokes, []string{rule})
	if len(rules) != 1 {
		t.Errorf("a rule restated after its revocation was lost: %q", rules)
	}
	// A turn that names the command without taking it back revokes nothing:
	// "no need to ask me" is about asking, not about go test.
	if got := evidence.Revoked("rule for quillmark: after every change run go test and then always push to main, no need to ask me"); len(got) != 0 {
		t.Errorf("revoked %q", got)
	}
	if got := evidence.Revoked("stop running make lint, it is broken"); !slices.Equal(got, []string{"make lint"}) {
		t.Errorf("revoked %q", got)
	}
}

func TestOnlyTheNewestRulesAreKept(t *testing.T) {
	typed := make([]string, 0, 4)
	for _, s := range []string{"a", "b", "c", "d"} {
		typed = append(typed, "always run ./scripts/check-"+s+".sh before you call it done")
	}
	rules, _ := foldUserRules(nil, nil, typed)
	if len(rules) != userRuleCap || !strings.Contains(rules[len(rules)-1], "check-d.sh") {
		t.Errorf("kept %q", rules)
	}
	// Folding the same turns again changes nothing.
	again, _ := foldUserRules(rules, nil, typed)
	if !slices.Equal(again, rules) {
		t.Errorf("a second fold of the same turns changed the rules: %q -> %q", rules, again)
	}
}
