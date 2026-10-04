package digest

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func rulesSession(turns ...string) model.Session {
	s := model.Session{ID: "rules-fixture", Harness: "opencode"}
	for _, t := range turns {
		s.Messages = append(s.Messages, model.Message{Role: "user", Text: t},
			model.Message{Role: "assistant", Text: "Done with that step."})
	}
	return s
}

const houseRules = `Work in this repository. There are 48 unfinished functions marked TODO(T1) through TODO(T48). Implement all forty-eight.

House rules for this repository. They apply for the whole session:
1. Never modify anything under internal/legacy/.
2. Run tests only with ./check.sh. Do not run ` + "`go test`" + ` yourself.
3. Each time you finish a TODO, append a line to CHANGES.md.
4. Do not edit *_test.go files.
5. Do not use the sort package in code you write; use the slices package instead.
6. When everything is done and ./check.sh passes, add a final line DONE at the end of CHANGES.md.

Start with T1, T2 and T3.`

// A long session worked in steps: the rules were stated once, in the first
// turn, and the newest turn names the next step only. The packet has to carry
// both the rules and what the steps are for.
func TestPacketCarriesTheRulesFromTheFirstTurn(t *testing.T) {
	s := rulesSession(houseRules, "Next: T4, T5 and T6, then run the tests.", "Next: T13, T14 and T15, then run the tests.")
	c := ExtractCompactionContext(s, ExtractOptions{})
	if len(c.Rules) != 6 {
		t.Fatalf("rules = %d, want the six house rules: %+v", len(c.Rules), c.Rules)
	}
	// The list is a rule list, so its one item that opens like a description
	// is kept with the rest.
	if !strings.Contains(c.Rules[5].Text, "add a final line DONE") {
		t.Fatalf("last rule = %q", c.Rules[5].Text)
	}
	out := RenderCompactionContext(c, 3800)
	for _, want := range []string{
		"Standing instructions the user gave earlier",
		"- Never modify anything under internal/legacy/.",
		"Session started with: Work in this repository.",
		"Latest request before compaction: Next: T13, T14 and T15",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("packet lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "T4, T5") {
		t.Fatalf("packet presents an older step:\n%s", out)
	}
}

func TestOneRequestSessionHasNoSeparateGoal(t *testing.T) {
	c := ExtractCompactionContext(rulesSession("Repair the parser so the retry test passes."), ExtractOptions{})
	if c.Goal.Text != "" {
		t.Fatalf("goal = %q, want none: it is the objective", c.Goal.Text)
	}
	if out := RenderCompactionContext(c, 3800); strings.Contains(out, "Session started with") {
		t.Fatalf("packet:\n%s", out)
	}
}

func TestStandingRulesSkipWhatIsNotAnInstruction(t *testing.T) {
	for _, turn := range []string{
		"Never mind, it works now.",
		"Не удалось выполнить, посмотри лог.",
		"не нашлось такой сессии",
		"Preference questions remain the weakest category.",
		"Do not merge this yet?",
		"Why does it always fail on CI?",
		"> Never modify the vendored files.\nthat line is from their README",
		"```\n// Never call this from a hook.\n```\nwhat does this comment mean",
		"<system-reminder>Never use git stash.</system-reminder>",
		// A findings list with one instruction in it is not a rule list.
		"Findings:\n- the index is rebuilt twice\n- the cache is cold\n- do not trust the p95 here\n- recall is 0.9s",
		strings.Repeat("log line ", 800) + "\nNever retry on 4xx.",
	} {
		rules := ExtractStandingRules(rulesSession(turn), nil)
		for _, r := range rules {
			if !strings.Contains(turn, "do not trust") || r.Text != "do not trust the p95 here" {
				t.Errorf("%q gave rule %q", turn, r.Text)
			}
		}
	}
}

func TestStandingRulesInOtherShapes(t *testing.T) {
	for turn, want := range map[string]string{
		"Fix the export. Don't touch the install code, another person is on it.": "Don't touch the install code, another person is on it.",
		"давай дальше. не трогай legacy и не запускай go test сам":               "не трогай legacy и не запускай go test сам",
		"Use slices instead of sort in new code.":                                "Use slices instead of sort in new code.",
		"всегда гоняй тесты через ./check.sh":                                    "всегда гоняй тесты через ./check.sh",
	} {
		rules := ExtractStandingRules(rulesSession(turn), nil)
		if len(rules) != 1 || rules[0].Text != want {
			t.Errorf("%q gave %+v, want %q", turn, rules, want)
		}
	}
}

// The transcript a later compaction reads may start after the turn that
// stated the rules; what the last packet carried stays, once.
func TestStandingRulesMergeWithTheLastPacket(t *testing.T) {
	prev := []model.ContextFact{{Text: "Never modify anything under internal/legacy/."}}
	rules := ExtractStandingRules(rulesSession("Next: T7. never modify anything under internal/legacy/.", "Do not edit *_test.go files."), prev)
	if len(rules) != 2 || rules[0].Text != prev[0].Text || rules[1].Text != "Do not edit *_test.go files." {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestStandingRulesAreBounded(t *testing.T) {
	var turns []string
	for i := 0; i < 20; i++ {
		turns = append(turns, "Never touch generated file number "+strings.Repeat("x", i+1)+" in this repo.")
	}
	rules := ExtractStandingRules(rulesSession(turns...), nil)
	size := 0
	for _, r := range rules {
		size += len(r.Text)
	}
	if len(rules) > standingRulesMax || size > standingRulesBytes {
		t.Fatalf("%d rules, %d bytes", len(rules), size)
	}
}
