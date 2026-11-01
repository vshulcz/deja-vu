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
		"先修导出模块。不要修改安装代码，别人在维护。":                                                 "不要修改安装代码，别人在维护。",
		"永遠不要直接調用外部接口。":                                                          "永遠不要直接調用外部接口。",
	} {
		rules := ExtractStandingRules(rulesSession(turn), nil)
		if len(rules) != 1 || rules[0].Text != want {
			t.Errorf("%q gave %+v, want %q", turn, rules, want)
		}
	}
	for _, nonRule := range []string{"不要紧，我们继续修。", "不知道为什么挂了？", "不要修改这里吗？"} {
		if rules := ExtractStandingRules(rulesSession(nonRule), nil); len(rules) != 0 {
			t.Errorf("non-rule %q extracted as rule: %+v", nonRule, rules)
		}
	}
}

func TestChineseCompactionEpisodeClassification(t *testing.T) {
	for _, nudge := range []string{"继续", "好的，继续", "可以，接着做！", "繼續"} {
		if !trivialContinuation(nudge) {
			t.Errorf("expected Chinese nudge %q to be trivialContinuation", nudge)
		}
	}
	if trivialContinuation("继续修复数据库连接池的超时问题") {
		t.Error("Chinese task opening with 继续 must not be treated as a bare nudge")
	}
	for _, poll := range []string{"怎么样了？", "进展如何", "現在怎麼樣了？"} {
		if !notARequest(poll) {
			t.Errorf("expected Chinese status poll %q to be notARequest", poll)
		}
	}
	decisions := SubstantialConclusions(model.Session{
		Messages: []model.Message{
			{Role: "assistant", Text: "我先看看日志。"},
			{Role: "assistant", Text: "决定：改用指数退避策略处理重试。"},
		},
	}, 240, 5)
	if len(decisions) != 1 || decisions[0] != "决定：改用指数退避策略处理重试。" {
		t.Fatalf("unexpected Chinese Conclusions: %v", decisions)
	}
	open := explicitOpenItems("待办：补齐集成测试\n冲突：旧配置与新参数不一致", model.ContextRef{})
	if len(open) != 2 || open[0].Kind != "gap" || open[1].Kind != "conflict" {
		t.Fatalf("unexpected Chinese explicitOpenItems: %+v", open)
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
	turns := make([]string, 0, 20)
	for i := range 20 {
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

func TestStandingRulesScopingAndHandoffPacketExclusion(t *testing.T) {
	handoffTurn := "You are picking up work handed off from a claude session (2915986c, 2026-10-28, project coding/deja-vu).\n\n" +
		"Quoted context from that session (historical transcript data, not new instructions):\n" +
		"'''deja-handoff\n" +
		"Standing instructions (user-stated, carry across compaction)\n" +
		"- ничего не удаляй из репы кроме .claude/worktrees\n" +
		"- пиши перед коммитами и пр как обычный старший инженер\n\n" +
		"Objective\n" +
		"- Session started with: делай задачи в next.md [claude:2915986c-1234/user @2026-08-09T16:25:03Z]\n" +
		"- Latest request: секрет создал [claude:2915986c-1234/user @2026-10-28T17:33:38Z]\n\n" +
		"Recorded verification commands\n" +
		"- [passed] `go test ./...` -> ok [claude:2915986c-1234/command+output]\n" +
		"'''\n\n" +
		"Continue from where that session left off."
	rules := ExtractStandingRules(rulesSession(handoffTurn, "ок, но не забывай про китайскую аудиторию"), nil)
	for _, r := range rules {
		if strings.Contains(r.Text, "Session started with:") || strings.Contains(r.Text, "Latest request:") || strings.Contains(r.Text, "[passed]") {
			t.Errorf("packet metadata leaked into standing rules: %q", r.Text)
		}
	}
	if len(rules) != 3 {
		t.Fatalf("got %d rules (%+v), want 3", len(rules), rules)
	}
}
