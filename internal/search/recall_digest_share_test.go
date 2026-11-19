package search

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/prompt"
)

func TestAutoRecallDigestShowingSharesBudgetAcrossSessions(t *testing.T) {
	now := time.Now().UTC()
	ss := []model.Session{
		{
			ID:      "sh-1",
			Project: "app",
			Updated: now.Add(-48 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "check backup rotation replica status shard one", Time: now.Add(-48 * time.Hour)},
				{Role: "assistant", Text: "Backup rotation replica status SH_ONE verified on primary vault.", Time: now.Add(-47 * time.Hour)},
				{Role: "user", Text: "confirm backup rotation replica status across secondary vault", Time: now.Add(-47 * time.Hour).Add(5 * time.Minute)},
				{Role: "assistant", Text: "Secondary backup rotation replica status confirms SH_ONE ledger synced.", Time: now.Add(-47 * time.Hour).Add(10 * time.Minute)},
				{Role: "user", Text: "check backup rotation replica status checksums", Time: now.Add(-47 * time.Hour).Add(15 * time.Minute)},
				{Role: "assistant", Text: strings.Repeat("Backup rotation replica status checksum nominal for SH_ONE. ", 6), Time: now.Add(-47 * time.Hour).Add(20 * time.Minute)},
			},
		},
		{
			ID:      "sh-2",
			Project: "app",
			Updated: now.Add(-24 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "what is the backup rotation replica status?", Time: now.Add(-24 * time.Hour)},
				{Role: "assistant", Text: "Backup rotation replica SH_TWO added new standby vault.", Time: now.Add(-23 * time.Hour)},
			},
		},
		{
			ID:      "sh-3",
			Project: "app",
			Updated: now.Add(-2 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "what is the backup rotation replica status?", Time: now.Add(-2 * time.Hour)},
				{Role: "assistant", Text: "Backup rotation replica SH_THREE promoted to active leader.", Time: now.Add(-time.Hour)},
			},
		},
	}

	terms := []string{"backup", "rotation", "replica", "status"}
	got, shown := AutoRecallDigestShowing(ss, 950, terms, "what is the backup rotation replica status?")
	if len(got) > 950 {
		t.Fatalf("digest exceeded budget 950: got %d bytes", len(got))
	}
	if len(shown) != 3 {
		t.Fatalf("expected all 3 sessions to share budget, got %d:\n%s", len(shown), got)
	}
	for _, marker := range []string{"SH_ONE", "SH_TWO", "SH_THREE"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %s in shared digest:\n%s", marker, got)
		}
	}

	small, smallShown := AutoRecallDigestShowing(ss, 140, terms, "what is the backup rotation replica status?")
	if len(small) > 140 {
		t.Fatalf("small digest exceeded budget 140: got %d bytes", len(small))
	}
	if len(smallShown) != 1 {
		t.Fatalf("expected 1 session under 140-byte cap, got %d:\n%s", len(smallShown), small)
	}
}

func TestAutoRecallDigestShowingUnclippedSingleSessionFallback(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{
			ID:      "single-fit-1",
			Project: "app",
			Updated: now.Add(-2 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "what storage ledger detail was verified?"},
				{Role: "assistant", Text: "Storage ledger checkpoint alpha confirmed on primary vault. " + strings.Repeat("alpha ", 35)},
				{Role: "assistant", Text: "Storage ledger checkpoint beta verified on replica node. " + strings.Repeat("beta ", 35)},
				{Role: "assistant", Text: "Storage ledger checkpoint gamma TAIL_FACT_MARKER confirmed. " + strings.Repeat("gamma ", 30)},
			},
		},
		{
			ID:      "single-fit-2",
			Project: "mod-" + strings.Repeat("header-", 42),
			Updated: now.Add(-24 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "storage ledger"},
				{Role: "assistant", Text: "Storage ledger secondary note confirmed cleanly."},
			},
		},
	}
	got, shown := AutoRecallDigestShowing(ss, 900, []string{"storage", "ledger", "checkpoint"}, "what storage ledger detail was verified?")
	if len(shown) != 1 {
		t.Fatalf("expected 1 shown session when second header cannot fit, got %d:\n%s", len(shown), got)
	}
	if !strings.Contains(got, "TAIL_FACT_MARKER") {
		t.Fatalf("expected single shown session to reclaim full budget with TAIL_FACT_MARKER, got %d bytes:\n%s", len(got), got)
	}
}

func TestMatchedLinesAskedUsersPreservesTwoChronologicalUserTurns(t *testing.T) {
	s := model.Session{
		ID:      "multi-user-1",
		Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "I currently own three bikes in the garage for commuting."},
			{Role: "assistant", Text: "Got it, noted your three bikes."},
			{Role: "user", Text: "Since I will have four bikes with me next month, I need another rack."},
			{Role: "assistant", Text: "A vertical wall rack fits four bikes cleanly."},
		},
	}
	users, concl := matchedLinesAskedUsers(s, []string{"bikes", "rack", "commuting"}, "how many bikes do I own?")
	if len(users) != 2 {
		t.Fatalf("expected 2 chronological user turns, got %d (%v)", len(users), users)
	}
	if !strings.Contains(users[0], "three bikes") || !strings.Contains(users[1], "four bikes") {
		t.Fatalf("expected both turns preserved in order, got %v", users)
	}
	if len(concl) == 0 {
		t.Fatal("expected assistant conclusions")
	}
}

func TestContainsWordAllowsEVerbInflectionsAndKeepsNonEGates(t *testing.T) {
	if !containsWord("she recently moved back to the suburbs", "move") {
		t.Fatal("expected containsWord to match moved for 4-letter -e word move")
	}
	if !containsWord("he regularly bakes sourdough loaves", "bake") {
		t.Fatal("expected containsWord to match bakes for 4-letter -e word bake")
	}
	if containsWord("set the minimum pool size", "mini") {
		t.Fatal("expected containsWord not to match minimum for non-e word mini")
	}
}

func TestLineAroundAnchorsOnFirstIdentifyingTerm(t *testing.T) {
	msg := "I have been organizing all my weekend watching habits across spreadsheets and notes, and by the way I watched all 22 Marvel Cinematic Universe films in two weeks flat."
	got := lineAround(msg, 120, []string{"marvel", "cinematic", "watching"})
	if !strings.Contains(got, "Marvel Cinematic") {
		t.Fatalf("expected lineAround to center on rare leading term 'marvel', got %q", got)
	}
}

func TestRegularSingularInflectionInRecall(t *testing.T) {
	if !TextCarriesTerm("rode a road bike and a hybrid bike all summer", "bikes") {
		t.Fatal("expected plural query term 'bikes' to match singular 'bike'")
	}
	if !TextCarriesTerm("watched everything in a week and a half", "weeks") {
		t.Fatal("expected plural query term 'weeks' to match singular 'week'")
	}
	for _, tc := range []struct {
		text string
		term string
	}{
		{text: "rode a motorbike yesterday", term: "bikes"},
		{text: "check cl" + "as alignment", term: "class"},
		{text: "check statu code", term: "status"},
		{text: "check redi server", term: "redis"},
	} {
		if TextCarriesTerm(tc.text, tc.term) {
			t.Fatalf("did not expect %q to match %q", tc.term, tc.text)
		}
	}
}

func TestSelectMatchedUserLinesPrefersUncoveredTerms(t *testing.T) {
	terms := []string{"weeks", "marathon", "films", "franchise", "sequel", "trilogy"}
	cands := []matchedScoredLine{
		{idx: 4, text: "Finished the whole trilogy sequel marathon of films last weekend.", hits: 4},
		{idx: 2, text: "Added more trilogy films into our marathon watchlist today.", hits: 3},
		{idx: 0, text: "Started the franchise series and watched it in a week.", hits: 2},
	}
	got := selectMatchedUserLines(cands, terms)
	if len(got) != 2 {
		t.Fatalf("expected 2 kept user lines, got %d (%v)", len(got), got)
	}
	if got[0] != cands[2].text {
		t.Fatalf("expected uncovered early user line %q first, got %v", cands[2].text, got)
	}
	if got[1] != cands[0].text {
		t.Fatalf("expected top-hit line %q second, got %v", cands[0].text, got)
	}
}

func TestSelectMatchedUserLinesSkipsRedundantEarlyTurn(t *testing.T) {
	terms := []string{"bikes", "currently"}
	cands := []matchedScoredLine{
		{idx: 4, text: "Since I'll have four bikes currently in storage.", hits: 2},
		{idx: 0, text: "Buying my first bikes currently sounds fun.", hits: 1},
	}
	wantFirst := cands[0].text
	got := selectMatchedUserLines(cands, terms)
	if len(got) != 1 || got[0] != wantFirst {
		t.Fatalf("expected only later higher-hit turn, got %v", got)
	}

	tieCands := []matchedScoredLine{
		{idx: 0, text: "I brought a road bike for the trip.", hits: 1},
		{idx: 2, text: "I brought another mountain bike for trails.", hits: 1},
		{idx: 4, text: "Since I will have four bikes with me now.", hits: 2},
	}
	wantTie := tieCands[2].text
	gotTie := selectMatchedUserLines(tieCands, []string{"bikes"})
	if len(gotTie) != 1 || gotTie[0] != wantTie {
		t.Fatalf("expected quantity turn to beat earlier chatter, got %v", gotTie)
	}
	if !carriesConcreteQuantityOrUpdate("Since I'll have four bikes with me.") {
		t.Fatal("expected cardinal quantity phrase to match")
	}
	if carriesConcreteQuantityOrUpdate("How do I repair the drivetrain?") {
		t.Fatal("did not expect substring inside 'repair' to match")
	}
}

func TestSelectMatchedUserLinesKeepsMidSentenceProperNounComplement(t *testing.T) {
	cands := []matchedScoredLine{
		{idx: 2, text: "I use the Cartwheel app from Target for grocery coupons.", hits: 1},
		{idx: 4, text: "I actually redeemed a $5 coupon on coffee creamer last Sunday.", hits: 4},
	}
	got := selectMatchedUserLines(cands, []string{"redeem", "coupon", "coffee", "creamer"})
	if len(got) != 2 {
		t.Fatalf("expected 2 user lines (coupon + proper noun complement), got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Target") || !strings.Contains(got[1], "$5 coupon") {
		t.Fatalf("expected Target complement then redemption line in chronological order, got %v", got)
	}
	if prompt.CarriesMidSentenceEntity("I redeemed a $5 coupon last Sunday.") {
		t.Fatal("did not expect weekday Sunday or opening pronoun I to count as proper noun complement")
	}
	if !prompt.CarriesMidSentenceEntity("Try checking at Target tomorrow.") {
		t.Fatal("expected mid-sentence Target to count as proper noun complement")
	}
}

func TestAutoRecallDigestShowingAnnotatesEarlierSameProjectHeader(t *testing.T) {
	newer := time.Date(2023, 5, 27, 15, 0, 0, 0, time.Local)
	older := time.Date(2023, 5, 24, 15, 0, 0, 0, time.Local)
	ss := []model.Session{
		{
			ID:      "reloc-new",
			Project: "personal-notes",
			Updated: newer,
			Messages: []model.Message{
				{Role: "user", Text: "My cousin moved to Seattle last week.", Time: newer},
				{Role: "assistant", Text: "Noted the move to Seattle.", Time: newer.Add(time.Minute)},
			},
		},
		{
			ID:      "reloc-old",
			Project: "personal-notes",
			Updated: older,
			Messages: []model.Message{
				{Role: "user", Text: "My cousin moved to the suburbs earlier this spring.", Time: older},
				{Role: "assistant", Text: "Got it, suburbs.", Time: older.Add(time.Minute)},
			},
		},
	}
	got, _ := AutoRecallDigestShowing(ss, 1200, []string{"cousin", "moved"}, "where did my cousin move?")
	if !strings.Contains(got, "(earlier than 2023-05-27 above)") {
		t.Fatalf("expected earlier-date warning on older same-project session, got:\n%s", got)
	}
}

func TestAutoRecallDigestShowingStripsBoilerplateAndPlanChatter(t *testing.T) {
	now := time.Now().UTC()
	ss := []model.Session{
		{
			ID:      "boiler-1",
			Project: "sample-app",
			Updated: now.Add(-time.Hour),
			Messages: []model.Message{
				{
					Role: "user",
					Text: "OptionPicker filter_items duplicates entries in dropdowns.",
					Time: now.Add(-65 * time.Minute),
				},
				{
					Role: "assistant",
					Text: "I'll inspect the OptionPicker code next.",
					Time: now.Add(-64 * time.Minute),
				},
				{
					Role: "user",
					Text: "Okay, please go ahead and apply the fix for OptionPicker filter_items.",
					Time: now.Add(-63 * time.Minute),
				},
				{
					Role: "assistant",
					Text: "Perfect!\n\nLet me provide a summary of what was done:\n\n## Summary\nFixed OptionPicker filter_items by deduplicating keys in picker.py.",
					Time: now.Add(-62 * time.Minute),
				},
			},
		},
	}
	got, _ := AutoRecallDigestShowing(ss, 1200, []string{"optionpicker", "filter_items"}, "Fix OptionPicker filter_items duplicates")
	if !strings.Contains(got, "Fixed OptionPicker filter_items by deduplicating keys in picker.py") {
		t.Fatalf("expected substantive assistant conclusion, got:\n%s", got)
	}
	for _, noise := range []string{"Let me provide a summary", "I'll inspect the OptionPicker code next"} {
		if strings.Contains(got, noise) {
			t.Fatalf("expected %q to be stripped from digest, got:\n%s", noise, got)
		}
	}
}

func TestAutoRecallSurfacesSingleTaskTrajectoryConclusionWithoutCrossTopicBleed(t *testing.T) {
	now := time.Now().UTC()
	t.Run("single-task trajectory surfaces final summary past long tool trace", func(t *testing.T) {
		msgs := make([]model.Message, 0, 40)
		msgs = append(msgs,
			model.Message{
				Role: "user",
				Text: "Fix WidgetRegistryConflict when merging widget media assets across formsets.",
				Time: now.Add(-60 * time.Minute),
			},
			model.Message{
				Role: "assistant",
				Text: "I'll help you fix this bug related to widget asset ordering.",
				Time: now.Add(-59 * time.Minute),
			},
		)
		for i := range 36 {
			msgs = append(msgs, model.Message{
				Role: "tool-output",
				Text: "inspected asset list step " + string(rune('a'+i%26)),
				Time: now.Add(-58*time.Minute + time.Duration(i)*time.Second),
			})
		}
		msgs = append(msgs,
			model.Message{
				Role: "command",
				Text: "pytest -q → exit 0",
				Time: now.Add(-2 * time.Minute),
			},
			model.Message{
				Role: "assistant",
				Text: "Perfect! I've successfully fixed the bug. Here's a summary:\n\n## Bug Summary\nMerged order lists with a stable topological sort to eliminate false warnings.",
				Time: now.Add(-time.Minute),
			},
		)
		ss := []model.Session{{
			ID:       "traj-single",
			Project:  "sample-app",
			Updated:  now.Add(-time.Minute),
			Messages: msgs,
		}}
		got, _ := AutoRecallDigestShowing(ss, 1200, []string{"widgetregistryconflict", "merging", "widget"}, "Fix WidgetRegistryConflict")
		if !strings.Contains(got, "topological sort") {
			t.Fatalf("expected final trajectory summary in digest, got:\n%s", got)
		}
		if !strings.Contains(got, "WidgetRegistryConflict") {
			t.Fatalf("expected opening task subject kept paired past dist>30, got:\n%s", got)
		}
	})

	t.Run("two-topic session stops before second user question", func(t *testing.T) {
		ss := []model.Session{{
			ID:      "traj-two-topic",
			Project: "sample-app",
			Updated: now.Add(-time.Minute),
			Messages: []model.Message{
				{
					Role: "user",
					Text: "Fix WidgetRegistryConflict when merging widget media assets.",
					Time: now.Add(-40 * time.Minute),
				},
				{
					Role: "assistant",
					Text: "Let me start by checking the repository layout.",
					Time: now.Add(-39 * time.Minute),
				},
				{
					Role: "user",
					Text: "How should we configure connection pooling for read replicas?",
					Time: now.Add(-10 * time.Minute),
				},
				{
					Role: "command",
					Text: "pytest -q → exit 0",
					Time: now.Add(-2 * time.Minute),
				},
				{
					Role: "assistant",
					Text: "Configured replica connection pool size 32 for read workloads.",
					Time: now.Add(-time.Minute),
				},
			},
		}}
		got, _ := AutoRecallDigestShowing(ss, 1200, []string{"widgetregistryconflict", "media"}, "Fix WidgetRegistryConflict")
		if strings.Contains(got, "pool size 32") {
			t.Fatalf("expected second-topic conclusion not to bleed into first topic, got:\n%s", got)
		}
	})
}

func TestMatchedLinesPrefersFinalTrajectoryConclusionOverMidSessionGuess(t *testing.T) {
	now := time.Now().UTC()
	msgs := []model.Message{
		{
			Role: "user",
			Text: "Saving a child model with an unsaved related parent triggers SaveRelatedCacheError in field cache.",
			Time: now.Add(-40 * time.Minute),
		},
		{
			Role: "assistant",
			Text: "Now I see the problem! Because set_cached_value in descriptors.py handles SaveRelatedCacheError, we should update descriptors.py.",
			Time: now.Add(-35 * time.Minute),
		},
		{
			Role: "assistant",
			Text: "Actually descriptors.py has SaveRelatedCacheError only for logging, let me check base.py instead.",
			Time: now.Add(-25 * time.Minute),
		},
		{
			Role: "edit",
			Text: "/repo/models/base.py\n_prepare_related_fields_for_save()",
			Time: now.Add(-10 * time.Minute),
		},
		{
			Role: "command",
			Text: "pytest -q → exit 0",
			Time: now.Add(-2 * time.Minute),
		},
		{
			Role: "assistant",
			Text: "## Problem\nSaving a child model with an unsaved parent triggers SaveRelatedCacheError.\n\n## Root Cause\nIn models/base.py, _prepare_related_fields_for_save() failed to refresh the foreign key cache before validation.\n\n## The Fix\nUpdated _prepare_related_fields_for_save() in models/base.py.",
			Time: now.Add(-time.Minute),
		},
	}
	s := model.Session{
		ID:       "mid-vs-final",
		Project:  "sample-app",
		Updated:  now.Add(-time.Minute),
		Messages: msgs,
	}
	got, _ := AutoRecallDigestShowing([]model.Session{s}, 1200, []string{"saverelatedcacheerror", "descriptors", "parent"}, "Fix SaveRelatedCacheError")
	if !strings.Contains(got, "_prepare_related_fields_for_save") {
		t.Fatalf("expected final verified root cause in digest, got:\n%s", got)
	}
	if strings.Contains(got, "Now I see the problem") && strings.Contains(got, "set_cached_value") {
		t.Fatalf("expected mid-session guess not to displace final trajectory conclusion, got:\n%s", got)
	}
}

func TestAutoRecallDigestPreservesConclusionUnderTightHalfBudget(t *testing.T) {
	now := time.Now().UTC()
	longProblem := "Please check why MatrixFormatter fails when rendering nested subscripted composite symbols across multiple product terms inside LaTeX output when order preservation is enabled and zero terms are skipped during canonical simplification steps."
	s := model.Session{
		ID:      "tight-480",
		Project: "sample-app",
		Updated: now.Add(-time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: longProblem, Time: now.Add(-10 * time.Minute)},
			{Role: "edit", Text: "/repo/printing/latex.py\n_print_Mul", Time: now.Add(-5 * time.Minute)},
			{Role: "command", Text: "pytest -q → exit 0", Time: now.Add(-2 * time.Minute)},
			{Role: "assistant", Text: "Fixed MatrixFormatter by updating _print_Mul to keep original factor order.", Time: now.Add(-time.Minute)},
		},
	}
	got, shown := AutoRecallDigestShowing([]model.Session{s}, 480, []string{"matrixformatter", "latex"}, "Fix MatrixFormatter")
	if len(shown) == 0 {
		t.Fatal("expected session to be shown under 480B budget")
	}
	if !strings.Contains(got, "_print_Mul") {
		t.Fatalf("expected assistant conclusion line to survive 480B cut beside long user prompt, got:\n%s", got)
	}
}

func TestAutoRecallDigestTailConclusionNotBlockedByEarlierNearCopyUnderTightBudget(t *testing.T) {
	now := time.Now()
	s := model.Session{
		ID:      "traj-tail-near-copy",
		Project: "/tmp/proj",
		Updated: now.Add(-time.Hour),
		Messages: []model.Message{
			{
				Role: "user",
				Text: "Fix pkg.vector render/pretty.py _render_basis_term omitting separators before basis scalars.",
			},
			{
				Role: "assistant",
				Text: "Reproduced pkg.vector render/pretty.py _render_basis_term bug: separators before basis scalars omitted. Let me explore.",
			},
			{
				Role: "edit",
				Text: "render/pretty.py",
			},
			{
				Role: "command",
				Text: "pytest tests/test_pretty.py → exit 0",
			},
			{
				Role: "assistant",
				Text: "## Summary\n\n### Problem\nSeparators were missing in basis pretty printing.\n\n### Root Cause\nIn pkg.vector render/pretty.py _render_basis_term omitted separators before basis scalars; fixed spacing check.",
			},
		},
	}
	got, shown := AutoRecallDigestShowing([]model.Session{s}, 420, []string{"pkg.vector", "render/pretty.py", "_render_basis_term"}, "Fix _render_basis_term in pkg.vector")
	if len(shown) == 0 {
		t.Fatal("expected session to be shown under 420B budget")
	}
	if !strings.Contains(got, "_render_basis_term") {
		t.Fatalf("expected post-edit root cause naming _render_basis_term, got:\n%s", got)
	}
	if strings.Contains(got, "Let me explore") {
		t.Fatalf("expected early pre-edit exploration message to lose to post-edit root cause, got:\n%s", got)
	}
	if !strings.Contains(got, "fixed spacing check") {
		t.Fatalf("expected post-edit fix detail to appear in digest, got:\n%s", got)
	}
}

func TestCutSectionPreservingConclusionKeepsFinalConclusionOverEarlierMatchUnderTightBudget(t *testing.T) {
	now := time.Now()
	s := model.Session{
		ID:      "cut-last-concl",
		Project: "/tmp/proj",
		Updated: now.Add(-time.Hour),
		Messages: []model.Message{
			{
				Role: "user",
				Text: "Fix render/pretty.py _format_matrix_row alignment issue with BasisScalar vectors.",
			},
			{
				Role: "assistant",
				Text: "Looking at render/pretty.py _format_matrix_row and BasisScalar vector layout right now.",
			},
			{
				Role: "assistant",
				Text: "The bug was in render/pretty.py inside _format_matrix_row where column widths omitted sign padding.",
			},
		},
	}
	got, shown := AutoRecallDigestShowing([]model.Session{s}, 360, []string{"render/pretty.py", "_format_matrix_row", "BasisScalar"}, "Fix _format_matrix_row in render/pretty.py")
	if len(shown) == 0 {
		t.Fatal("expected session to be shown under 360B budget")
	}
	if !strings.Contains(got, "_format_matrix_row") {
		t.Fatalf("expected final conclusion symbol _format_matrix_row under 360B cut, got:\n%s", got)
	}
	if !strings.Contains(got, "omitted sign padding") {
		t.Fatalf("expected final conclusion details to survive 360B cut over earlier match, got:\n%s", got)
	}
}
