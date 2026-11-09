package digest

import (
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestLearnDiscourseMarkersSeparatesOutcomeVerbsFromDomainNouns(t *testing.T) {
	defer SetLearnedDecisionMarkers(nil)

	// Build a synthetic corpus where:
	// - "quicksilver" and "scheduler" appear in both exploration preambles (C-)
	//   and verified outcomes (C+), so their log-odds z-score is ~0.
	// - "модернизировал" (RU) and "stabilized" (EN) appear specifically in
	//   trajectory-verified outcome turns (RoleEdit + RoleCommand -> exit 0),
	//   while neither is in the static decisionMarkers list.
	ss := make([]model.Session, 0, 8)
	for i := range 8 {
		ss = append(ss, model.Session{
			ID: "s" + string(rune('a'+i)), Harness: "claude", Project: "app",
			Messages: []model.Message{
				{Role: "user", Text: "check quicksilver scheduler latency"},
				{Role: "assistant", Text: "проверяю логи quicksilver scheduler перед правкой"},
				{Role: sources.RoleFiles, Text: "internal/quicksilver/scheduler.go"},
				{Role: "assistant", Text: "читаю конфигурацию quicksilver scheduler дальше"},
				{Role: sources.RoleFiles, Text: "internal/quicksilver/config.go"},
				{Role: sources.RoleEdit, Text: "internal/quicksilver/scheduler.go"},
				{Role: sources.RoleCommand, Text: "go test ./internal/quicksilver/... → exit 0"},
				{Role: "assistant", Text: "Модернизировал и stabilized `internal/quicksilver/scheduler.go` для батчинга."},
			},
		})
	}

	markers := LearnDiscourseMarkers(ss)
	if !slices.Contains(markers, "модернизировал") {
		t.Fatalf("expected Monroe log-odds to learn 'модернизировал' from verified outcomes, got %v", markers)
	}
	if !slices.Contains(markers, "stabilized") {
		t.Fatalf("expected Monroe log-odds to learn 'stabilized' from verified outcomes, got %v", markers)
	}
	if slices.Contains(markers, "quicksilver") || slices.Contains(markers, "scheduler") {
		t.Fatalf("expected shared domain nouns to cancel out in log-odds, but got %v", markers)
	}

	SetLearnedDecisionMarkers(markers)
	line := "Модернизировал пайплайн в `internal/quicksilver/scheduler.go`."
	if !CarriesDecision(line) {
		t.Fatalf("expected CarriesDecision(%q) to succeed via learned Monroe log-odds marker", line)
	}
	if carriesLearnedDecision(strings.ToLower(line), line, []string{"модернизировал"}) {
		t.Fatalf("expected carriesLearnedDecision to skip marker echoed in asked terms")
	}
	if LearnDiscourseMarkers(ss[:1]) != nil {
		t.Fatal("expected nil markers when corpus has fewer than logOddsMinPosCount sessions")
	}
}

func TestMultilingualRulesAndHandoffUnwrapHelpers(t *testing.T) {
	if !trivialContinuation("はい 続けて") {
		t.Error("expected Japanese continuation nudge to be recognized as trivialContinuation")
	}
	if trivialContinuation("fix internal/index/cooccur.go") {
		t.Error("expected file path prompt not to be trivialContinuation")
	}
	rules := turnPairRules("1. Enable redis cache\n2. Drop legacy migration table in `schema.sql`?", "2. не надо, таблица еще нужна")
	if len(rules) == 0 {
		t.Fatal("expected numbered refusal rule from turnPairRules")
	}
	rules2 := turnPairRules("1. Rewrite the entire billing pipeline from scratch — this will take three weeks and break existing webhooks in production if we are not careful", "1 не буду, слишком долго")
	if len(rules2) == 0 {
		t.Fatal("expected summary refusal rule from turnPairRules")
	}
	rules3 := turnPairRules("Should we run `docker compose down -v`?", "2 нет, удалит базу")
	if len(rules3) == 0 {
		t.Fatal("expected fallback single-entity numbered refusal rule from turnPairRules")
	}
	if !onlyStandingRules("Never use force push on main. Always run tests before merge.") {
		t.Error("expected onlyStandingRules to return true for pure standing rules")
	}
	if onlyStandingRules("Look at the logs first.") {
		t.Error("expected onlyStandingRules to return false for non-rule")
	}
	if got := stripTagBlocks("<system_reminder>ignore</system_reminder>keep this <bad>unclosed</other> text"); !strings.Contains(got, "keep this") {
		t.Fatalf("stripTagBlocks = %q", got)
	}
	cmds := proposedSlashCommands("Check /Users/alex/app and /tmp/test or run /compact after /deploy/prod")
	if !slices.Contains(cmds, "/compact") || slices.Contains(cmds, "/Users") {
		t.Fatalf("unexpected proposedSlashCommands: %v", cmds)
	}
	handoff := "You are picking up work handed off from a previous session.\n\nObjective\n- Session started with: build zonal BM25F scorer\n- Latest request before compaction: fix negative control benchmark\n" + handoffQuoteClose + "\ncontinue from the last failing test"
	if got := unwrapHandoffPrompt(handoff, true); got != "build zonal BM25F scorer" {
		t.Fatalf("unwrapHandoffPrompt(wantGoal=true) = %q", got)
	}
	if got := unwrapHandoffPrompt(handoff, false); got != "continue from the last failing test" {
		t.Fatalf("unwrapHandoffPrompt(wantGoal=false) = %q", got)
	}
	handoffNoTail := "You are picking up work handed off from a previous session.\n\nObjective\n- Session started with: build zonal BM25F scorer\n- Latest request: verify CI shards"
	if got := unwrapHandoffPrompt(handoffNoTail, false); got != "verify CI shards" {
		t.Fatalf("unwrapHandoffPrompt(no tail, wantGoal=false) = %q", got)
	}
}
