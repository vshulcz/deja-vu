package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestPromptPickerFiltersFramingModifiersLikeWeek(t *testing.T) {
	// Ensures temporal framing words such as "week" do not block a candidate
	// whose speech carries all domain subjects ("cocktail-making", "class").
	day := time.Date(2025, 3, 10, 14, 0, 0, 0, time.UTC)
	sess := model.Session{
		ID:      "cocktail-class",
		Project: "personal",
		Updated: day,
		Messages: []model.Message{
			{Role: "user", Text: "I signed up for a cocktail-making class on Wednesdays at 7pm."},
			{Role: "assistant", Text: "Noted: your cocktail-making class is on Wednesday evening."},
		},
	}
	terms := []string{"day", "week", "take", "cocktail-making", "class"}
	idf := map[string]float64{
		"week":            4.4,
		"cocktail-making": 5.2,
		"class":           4.1,
		"take":            4.0,
		"day":             1.5,
	}
	pk := &PromptPicker{
		Pol:          policy.Policy{},
		Terms:        terms,
		OrderedTerms: ByIdentifying(terms, idf),
		LeadTerms:    []string{"cocktail-making", "week", "class"},
		Asked:        "What day of the week do I take a cocktail-making class?",
		IDFOf:        idf,
		TopIDF:       5.5,
		Unscoped:     false,
	}
	if !pk.LeadTermsMeet(sess) {
		t.Fatalf("expected LeadTermsMeet=true when domain subjects match and only framing word week is absent")
	}
	pk.Pick([]model.Session{sess}, []int{2}, []int{2}, []int{2})
	if len(pk.Picked()) != 1 || pk.Picked()[0].ID != "cocktail-class" {
		t.Fatalf("expected cocktail-class to be picked, got %+v", pk.Picked())
	}
	if !pk.Confident() || !pk.WorthDigest() {
		t.Fatalf("expected confident=true and worthDigest=true, got confident=%v worthDigest=%v", pk.Confident(), pk.WorthDigest())
	}
}

func TestPromptPickerCoreLifecycleAndForkMerge(t *testing.T) {
	t0 := time.Date(2025, 2, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	t2 := t0.Add(48 * time.Hour)
	terms := []string{"pgbouncer", "pool", "mode", "timeout"}
	idf := map[string]float64{"pgbouncer": 4.8, "pool": 3.2, "mode": 2.9, "timeout": 2.6}

	selfSess := model.Session{ID: "self", Project: "infra", Updated: t2, Messages: []model.Message{{Role: "user", Text: "pgbouncer pool mode"}}}
	cronSess := model.Session{ID: "cron", Project: "infra", Kind: "cron", Updated: t2, Messages: []model.Message{{Role: "user", Text: "pgbouncer pool mode"}}}
	toolOnly := model.Session{ID: "tool-only", Project: "infra", Updated: t2, Messages: []model.Message{{Role: "tool", Text: "pgbouncer pool mode timeout"}}}
	longSess := model.Session{
		ID:      "long",
		Project: "infra",
		Updated: t2,
		Held:    350,
		Messages: []model.Message{
			{Role: "user", Text: "Checking pgbouncer pool mode for transaction pooling."},
			{Role: "assistant", Text: "Set pgbouncer pool mode to transaction with 30s server timeout."},
		},
	}
	dupSess := model.Session{
		ID:      "dup",
		Project: "infra",
		Updated: t1,
		Messages: []model.Message{
			{Role: "user", Text: "Checking pgbouncer pool mode for transaction pooling."},
			{Role: "assistant", Text: "Set pgbouncer pool mode to transaction with 30s server timeout."},
		},
	}
	second := model.Session{
		ID:      "second",
		Project: "infra",
		Updated: t1,
		Messages: []model.Message{
			{Role: "user", Text: "Should pgbouncer pool stay in session mode on staging?"},
			{Role: "assistant", Text: "Yes, staging uses session pool mode because prepared statements run there."},
		},
	}
	third := model.Session{
		ID:      "third",
		Project: "infra",
		Updated: t0,
		Messages: []model.Message{
			{Role: "user", Text: "Audit noted pgbouncer pool mode migration plan for read replicas."},
			{Role: "assistant", Text: "Documented pgbouncer pool mode settings for replica nodes."},
		},
	}
	emptyFocus := model.Session{
		ID:      "empty-focus",
		Project: "infra",
		Updated: t0,
		Held:    350,
		Messages: []model.Message{
			{Role: "user", Text: "pgbouncer pool check"},
		},
	}

	pk := &PromptPicker{
		Terms:            terms,
		Asked:            "what did we decide about pgbouncer pool mode?",
		IDFOf:            idf,
		TopIDF:           5.0,
		Self:             map[string]bool{"self": true},
		SelfProjectWords: map[string]bool{"infra": true},
		MaxMessages:      50,
		Focus: func(s model.Session, _ []string) model.Session {
			if s.ID == "empty-focus" {
				s.Messages = nil
			}
			return s
		},
		DigestBudget: func(confident bool) int {
			if confident {
				return 600
			}
			return 300
		},
		ProjectMatches: func(project string, ts []string) bool {
			return slices.Contains(ts, project)
		},
	}

	pk.Pick(
		[]model.Session{selfSess, cronSess, toolOnly, emptyFocus, longSess, dupSess, second, third},
		[]int{3, 3, 2, 2, 3, 3, 2, 2},
		[]int{1, 1, 1, 1, 1, 1, 1, 1},
		[]int{1, 1, 1, 1, 1, 1, 1, 1},
	)
	if len(pk.Picked()) != 3 {
		t.Fatalf("expected 3 distinct-date picks, got %d (%+v)", len(pk.Picked()), pk.Picked())
	}
	if pk.CrowdedOut() == 0 {
		t.Fatalf("expected CrowdedOut > 0 from duplicate/tool-only filtering")
	}
	pk.AddIDF(map[string]float64{"pgbouncer": 9.9, "replica": 3.4})
	if pk.IDFOf["pgbouncer"] != 4.8 || pk.IDFOf["replica"] != 3.4 {
		t.Fatalf("unexpected AddIDF result: %+v", pk.IDFOf)
	}
	var nilMapPicker PromptPicker
	nilMapPicker.AddIDF(map[string]float64{"k": 1.2})
	if nilMapPicker.IDFOf["k"] != 1.2 {
		t.Fatalf("expected AddIDF to initialize nil IDFOf")
	}
	trimmed := pk.TrimWeakThirdSession()
	if len(trimmed) != 3 {
		t.Fatalf("expected TrimWeakThirdSession to keep all 3 multi-term sessions, got %d", len(trimmed))
	}

	forked := pk.Fork(false, map[string]float64{"billing": 4.2, "ledger": 3.9}, []string{"billing", "ledger"})
	other := model.Session{
		ID:      "fourth",
		Project: "billing",
		Updated: t0.Add(-24 * time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "Use billing ledger snapshots for pgbouncer audit logs."},
			{Role: "assistant", Text: "Stored pgbouncer billing ledger mapping."},
		},
	}
	forked.Pick([]model.Session{other}, []int{2}, []int{1}, []int{1})
	pk.MergeUnscoped(&forked, terms)
	if pk.ExtraMatches() == 0 {
		t.Fatalf("expected MergeUnscoped to increment ExtraMatches when 3 distinct-date slots already filled")
	}

	// Verify open advice suppression and deadline abort and scope helpers.
	if !pk.HasScopedTarget() || pk.ScopeFilterNames([]string{"a"})[0] != "a" {
		t.Fatalf("unexpected scoped target behavior")
	}
	unscoped := &PromptPicker{Unscoped: true, CwdPlain: "app", Self: map[string]bool{}}
	if !unscoped.HasScopedTarget() || unscoped.ScopeFilterNames(nil)[0] != "app" {
		t.Fatalf("unexpected unscoped CwdPlain scope behavior")
	}
	unscoped.Deadline = time.Now().Add(-time.Second)
	unscoped.picked = []model.Session{second}
	unscoped.Pick([]model.Session{third}, []int{2}, []int{1}, []int{1})
	if len(unscoped.Picked()) != 1 {
		t.Fatalf("expected expired deadline to stop Pick after 1 item")
	}
}

func TestPromptPickerMediumStoreWhenClausesAndOrdering(t *testing.T) {
	d1 := time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)
	d2 := time.Date(2025, 1, 20, 12, 0, 0, 0, time.UTC)

	// WhenClausePairTerms + UncoveredWhenClause EN and RU.
	askedEN := "How long between buying the Espresso grinder and installing the PID kit?"
	termsEN := []string{"buying", "espresso", "grinder", "installing", "pid", "kit"}
	idfEN := map[string]float64{"espresso": 3.5, "grinder": 3.4, "pid": 3.8, "kit": 2.9, "buying": 1.1, "installing": 1.2}
	lEN, rEN, okEN := WhenClausePairTerms(askedEN, termsEN, idfEN)
	if !okEN || len(lEN) == 0 || len(rEN) == 0 {
		t.Fatalf("expected EN between/and clause split, got ok=%v l=%v r=%v", okEN, lEN, rEN)
	}

	askedRU := "Какой интервал между покупкой кофемолки Eureka и настройкой давления PID?"
	termsRU := []string{"какой", "интервал", "между", "покупкой", "кофемолки", "eureka", "настройкой", "давления", "pid"}
	idfRU := map[string]float64{"кофемолки": 3.4, "eureka": 4.2, "настройкой": 2.6, "давления": 3.1, "pid": 4.0, "между": 1.0}
	lRU, rRU, okRU := WhenClausePairTerms(askedRU, termsRU, idfRU)
	if !okRU || len(lRU) == 0 || len(rRU) == 0 {
		t.Fatalf("expected RU paired clause split, got ok=%v l=%v r=%v", okRU, lRU, rRU)
	}

	sLeft := model.Session{
		ID:      "left",
		Project: "coffee",
		Updated: d1,
		Messages: []model.Message{
			{Role: "user", Text: "Bought the Espresso grinder yesterday."},
			{Role: "assistant", Text: "Enjoy your Espresso grinder setup."},
		},
	}
	sRight := model.Session{
		ID:      "right",
		Project: "coffee",
		Updated: d2,
		Messages: []model.Message{
			{Role: "user", Text: "Installing the PID kit took an hour."},
			{Role: "assistant", Text: "Nice work installing the PID kit."},
		},
	}
	pk := &PromptPicker{
		Terms:    termsEN,
		Asked:    askedEN,
		IDFOf:    idfEN,
		TopIDF:   3.8,
		CwdPlain: "coffee",
	}
	pk.picked = []model.Session{sLeft}
	uncov := pk.UncoveredWhenClause(lEN, rEN)
	if !slices.Equal(uncov, rEN) {
		t.Fatalf("expected uncovered right clause %v, got %v", rEN, uncov)
	}
	if !pk.LeadTermsMeet(sRight) {
		t.Fatalf("expected whenClauseMatched to accept right-clause session")
	}

	// Medium-store speech match + TrimWeakThirdSession drops 1-term third session.
	sRightEspresso := model.Session{
		ID:      "right-espresso",
		Project: "coffee",
		Updated: d2,
		Messages: []model.Message{
			{Role: "user", Text: "Installing the espresso PID kit took an hour."},
			{Role: "assistant", Text: "Nice work installing the espresso PID kit."},
		},
	}
	weak := model.Session{
		ID:      "weak",
		Project: "coffee",
		Updated: d2.Add(24 * time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "Remember espresso beans need dry storage."},
		},
	}
	pkTrim := &PromptPicker{
		Terms:  []string{"espresso", "grinder", "pid"},
		IDFOf:  idfEN,
		picked: []model.Session{sLeft, sRightEspresso, weak},
	}
	kept := pkTrim.TrimWeakThirdSession()
	if len(kept) != 2 {
		t.Fatalf("expected TrimWeakThirdSession to drop weak third session, got %d", len(kept))
	}

	// OrderDistinctDateUpdates sorts newest distinct-date match first.
	ordered := OrderDistinctDateUpdates([]model.Session{
		{
			ID:      "old",
			Updated: d1,
			Messages: []model.Message{
				{Role: "user", Text: "Using Espresso grinder with stock burr."},
			},
		},
		{
			ID:      "new",
			Updated: d2,
			Messages: []model.Message{
				{Role: "user", Text: "Switched Espresso grinder to SSP flat burrs."},
			},
		},
	}, []string{"espresso", "grinder"}, idfEN)
	if ordered[0].ID != "new" {
		t.Fatalf("expected newest update first, got %q", ordered[0].ID)
	}
	if HasDistinctDate([]model.Session{{Updated: d1}, {Updated: d1}}) {
		t.Fatalf("same date should return HasDistinctDate=false")
	}
	if !HasDistinctDate([]model.Session{{}, {Updated: d1}, {Updated: d2}}) {
		t.Fatalf("distinct dates with leading zero date should return true")
	}

	// SpokenSubjectTerms + SkipWithPicked.
	pkSubj := &PromptPicker{
		Terms:  termsEN,
		IDFOf:  idfEN,
		picked: []model.Session{sLeft},
	}
	subjs := pkSubj.SpokenSubjectTerms(2.5)
	if !slices.Contains(subjs, "espresso") {
		t.Fatalf("expected espresso in SpokenSubjectTerms, got %v", subjs)
	}
	skip := SkipWithPicked(map[string]bool{"x": true}, []model.Session{sLeft})
	if !skip["x"] || !skip["left"] {
		t.Fatalf("SkipWithPicked missing IDs: %+v", skip)
	}

	// UserCarriesRareSubjectUpdate on Cyrillic mid-sentence proper noun.
	ruPicked := []model.Session{{
		ID:      "ru-1",
		Updated: d1,
		Messages: []model.Message{
			{Role: "user", Text: "Мы перенесли сервис Омега на новый кластер."},
		},
	}}
	ruCand := model.Session{
		ID:      "ru-2",
		Updated: d2,
		Messages: []model.Message{
			{Role: "user", Text: "Сегодня сервис Омега получил обновлённый лимит."},
		},
	}
	if !UserCarriesRareSubjectUpdate(ruCand, ruPicked, []string{"омега", "лимит"}, map[string]float64{"омега": 2.6, "лимит": 1.8}) {
		t.Fatalf("expected RU mid-sentence proper noun update match")
	}
}

func TestSearchNamedTermsOrderingAndRussianQuantityAndHeaderDate(t *testing.T) {
	// ByIdentifying, NamedLeadTerms, SymbolShaped, LatinWord.
	terms := []string{"1234", "v2", "обычный", "pg_bouncer", "alpha"}
	idf := map[string]float64{"1234": 5.0, "v2": 3.0, "обычный": 3.0, "pg_bouncer": 4.2, "alpha": 3.0}
	by := ByIdentifying(terms, idf)
	if by[0] != "1234" || by[1] != "pg_bouncer" {
		t.Fatalf("unexpected ByIdentifying order: %v", by)
	}
	named := NamedLeadTerms(terms, idf, 5.0)
	if slices.Contains(named, "1234") || !slices.Contains(named, "pg_bouncer") {
		t.Fatalf("unexpected NamedLeadTerms: %v", named)
	}
	if !SymbolShaped("a/b") || SymbolShaped("plain") {
		t.Fatalf("SymbolShaped check failed")
	}
	if !LatinWord("k8s") || LatinWord("пароль") {
		t.Fatalf("LatinWord check failed")
	}

	// carriesConcreteQuantityOrUpdate EN + RU.
	for _, yes := range []string{
		"Moved to Seattle last week.",
		"I run 15km twice weekly.",
		"Я переехал в Казань прошлой весной.",
		"Сейчас у меня три тестовых стенда и половина диска свободна.",
		"Подключил 20м патчкорд.",
	} {
		if !carriesConcreteQuantityOrUpdate(yes) {
			t.Fatalf("expected carriesConcreteQuantityOrUpdate=true for %q", yes)
		}
	}
	if carriesConcreteQuantityOrUpdate("Обычная проверка логов без изменений.") {
		t.Fatalf("did not expect plain log line to match carriesConcreteQuantityOrUpdate")
	}

	// recall_header_date helpers.
	if got := extractHeaderDay("[2025-04-12 app]"); got != "2025-04-12" {
		t.Fatalf("extractHeaderDay = %q, want 2025-04-12", got)
	}
	if got := extractHeaderDay("no date 2025-04-129 here"); got != "" {
		t.Fatalf("expected empty date for malformed trailing digit, got %q", got)
	}
	if got := annotateEarlierHeader("[2025-04-10 app]\n", "2025-04-12"); !strings.HasSuffix(got, "(earlier than 2025-04-12 above)\n") {
		t.Fatalf("unexpected annotated header with newline: %q", got)
	}
	if got := annotateEarlierHeader("[2025-04-10 app] (earlier than 2025-04-12 above)", "2025-04-12"); strings.Count(got, "earlier than") != 1 {
		t.Fatalf("expected idempotent header annotation, got %q", got)
	}
	if got := annotateEarlierHeader("[2025-04-10 app]", "2025-04-12"); !strings.HasSuffix(got, "(earlier than 2025-04-12 above)") {
		t.Fatalf("unexpected annotated header without newline: %q", got)
	}

	// cutSectionPreservingConclusion multi-conclusion fallback branches.
	lines := []string{
		"[2025-04-10 app]\n",
		"  - User: Long explanation " + strings.Repeat("context ", 12) + "\n",
		"  - Assistant: First decision for cluster staging.\n",
		"  - Assistant: Second decision for canary rollout.\n",
		"  - Assistant: Final decision settled on pooled connections.\n",
	}
	fullText := strings.Join(lines, "")
	cut := cutSectionPreservingConclusion(lines, fullText, 150)
	if !strings.Contains(cut, "First decision") || !strings.Contains(cut, "Final decision settled") {
		t.Fatalf("expected cutSectionPreservingConclusion to keep opening and final conclusions, got %q", cut)
	}
	tight := cutSectionPreservingConclusion(lines, fullText, 90)
	if !strings.Contains(tight, "Final decision settled") {
		t.Fatalf("expected tight cutSectionPreservingConclusion to keep final conclusion, got %q", tight)
	}
	clippedTight := cutSectionPreservingConclusion(lines, fullText, 55)
	if clippedTight == "" {
		t.Fatalf("expected non-empty fallback under very tight limit")
	}
}

func TestPromptPickerLeadTermsMediumStoreAndUnscopedBranches(t *testing.T) {
	t0 := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)

	// Unscoped unscoped-nil vs CwdPlain branches, UserCarriesTerm, and Cyrillic rare terms.
	cwdSessTogether := model.Session{
		ID:      "cwd-together",
		Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "check retry budget and queue depth"},
		},
	}
	cwdSessRareUser := model.Session{
		ID:      "cwd-rare",
		Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "adjust semaphore quota today"},
		},
	}
	cwdSessThree := model.Session{
		ID:      "cwd-three",
		Project: "app",
		Messages: []model.Message{
			{Role: "user", Text: "throttle pipeline flush"},
		},
	}
	ruMulti := model.Session{
		ID:      "ru-multi",
		Project: "other-proj",
		Messages: []model.Message{
			{Role: "user", Text: "настроили шлюз и витрина данных обновилась"},
		},
	}

	pkUnscoped := &PromptPicker{
		Terms:    []string{"semaphore", "quota", "queue", "depth"},
		IDFOf:    map[string]float64{"semaphore": 2.9, "quota": 2.0, "queue": 1.8, "depth": 1.7},
		TopIDF:   2.9,
		Unscoped: true,
		CwdPlain: "app",
	}
	if !pkUnscoped.LeadTermsMeet(cwdSessTogether) {
		t.Fatalf("expected unscoped CwdPlain session with 2 terms together to match")
	}
	pkSingleNonGen := &PromptPicker{
		Terms:    []string{"semaphore", "today"},
		IDFOf:    map[string]float64{"semaphore": 2.9, "today": 1.0},
		TopIDF:   2.9,
		Unscoped: true,
		CwdPlain: "app",
	}
	if !pkSingleNonGen.LeadTermsMeet(cwdSessRareUser) {
		t.Fatalf("expected unscoped CwdPlain session with single rare >=2.8 user term to match")
	}
	pkShortHigh := &PromptPicker{
		Terms:    []string{"throttle", "flush", "daily", "batch"},
		IDFOf:    map[string]float64{"throttle": 3.1, "flush": 2.3, "daily": 1.0, "batch": 1.0},
		TopIDF:   3.2,
		Unscoped: true,
		CwdPlain: "app",
	}
	if !pkShortHigh.LeadTermsMeet(cwdSessThree) {
		t.Fatalf("expected short unscoped CwdPlain query with IDF>=3.0 user term to match")
	}
	if pkShortHigh.LeadTermsMeet(model.Session{ID: "no", Project: "app", Messages: []model.Message{{Role: "user", Text: "daily"}}}) {
		t.Fatalf("expected non-matching CwdPlain session to return false")
	}

	pkNoCwd := &PromptPicker{
		Terms:    []string{"шлюз", "витрина", "роутер"},
		IDFOf:    map[string]float64{"шлюз": 3.6, "витрина": 3.1, "роутер": 2.0},
		TopIDF:   3.8,
		Unscoped: true,
	}
	if !pkNoCwd.LeadTermsMeet(ruMulti) {
		t.Fatalf("expected large unscoped Cyrillic session with >=2 rare terms (>=2.8) to match")
	}
	if pkNoCwd.LeadTermsMeet(cwdSessTogether) {
		t.Fatalf("expected unscoped unrelated session without rare terms to return false")
	}
	if pkNoCwd.HasScopedTarget() || pkNoCwd.ScopeFilterNames([]string{"x"}) != nil {
		t.Fatalf("expected unscoped empty CwdPlain to have no scoped target")
	}

	// Scoped fallback with effectiveLeadTerms, ProjectMatches, and Focus=nil default.
	pkScoped := &PromptPicker{
		Terms: []string{"чистка", "логов", "кеша", "скрипт"},
		IDFOf: map[string]float64{"чистка": 1.5, "логов": 1.4, "кеша": 1.3, "скрипт": 1.2},
		ProjectMatches: func(project string, ts []string) bool {
			return project == "кеша" && slices.Contains(ts, "кеша")
		},
	}
	if !pkScoped.LeadTermsMeet(model.Session{Project: "кеша", Messages: []model.Message{{Role: "user", Text: "чистка прошла"}}}) {
		t.Fatalf("expected scoped projectMatches + single lead term in speech to match")
	}
	pkNilProj := &PromptPicker{Terms: []string{"чистка", "логов"}}
	if pkNilProj.LeadTermsMeet(model.Session{Project: "чистка", Messages: []model.Message{{Role: "user", Text: "чистка"}}}) {
		t.Fatalf("expected nil ProjectMatches without two terms together to return false")
	}

	// Medium-store speech match branches: 2 nonGen, 1 nonGen + nonGenPresent, OR query, single rare >= TopIDF-0.9.
	pkMed := &PromptPicker{
		Terms:    []string{"кофемолка", "помол", "эспрессо", "обжарка"},
		Asked:    "кофемолка или обжарка?",
		IDFOf:    map[string]float64{"кофемолка": 2.7, "помол": 2.4, "эспрессо": 1.5, "обжарка": 1.3},
		TopIDF:   2.8,
		CwdPlain: "barista",
		Unscoped: true,
	}
	if !pkMed.mediumStoreSpeechMatch(model.Session{
		Project:  "other",
		Messages: []model.Message{{Role: "user", Text: "новая кофемолка улучшила помол"}},
	}) {
		t.Fatalf("expected 2 nonGen terms together in speech to match mediumStoreSpeechMatch")
	}
	pkMedPresent := &PromptPicker{
		Terms:    []string{"фильтр", "вода", "накипь"},
		IDFOf:    map[string]float64{"фильтр": 2.6, "вода": 1.4, "накипь": 0.5},
		TopIDF:   2.8,
		CwdPlain: "barista",
	}
	if !pkMedPresent.mediumStoreSpeechMatch(model.Session{
		Messages: []model.Message{{Role: "user", Text: "мягкая вода и фильтр"}},
	}) {
		t.Fatalf("expected 1 nonGen + nonGenPresent>=1.0 pair in short query to match")
	}
	pkMedOR := &PromptPicker{
		Terms:  []string{"капучино", "латте", "сироп"},
		Asked:  "капучино or латте?",
		IDFOf:  map[string]float64{"капучино": 2.3, "латте": 1.2, "сироп": 0.9},
		TopIDF: 2.8,
	}
	if !pkMedOR.mediumStoreSpeechMatch(model.Session{
		Messages: []model.Message{{Role: "user", Text: "капучино и сироп утром"}},
	}) {
		t.Fatalf("expected disjunctive OR in caller scope to match")
	}
	pkMedSingle := &PromptPicker{
		Terms:  []string{"кофемашина", "сегодня"},
		IDFOf:  map[string]float64{"кофемашина": 3.1, "сегодня": 0.2},
		TopIDF: 3.4,
	}
	if !pkMedSingle.mediumStoreSpeechMatch(model.Session{
		Messages: []model.Message{{Role: "user", Text: "моя кофемашина шумит"}},
	}) {
		t.Fatalf("expected single nonGen >= 3.0 in caller scope to match")
	}
	pkMedSingleOutScope := &PromptPicker{
		Terms:    []string{"бойлер", "сегодня"},
		IDFOf:    map[string]float64{"бойлер": 2.7, "сегодня": 0.2},
		TopIDF:   3.2,
		Unscoped: true,
		CwdPlain: "other",
	}
	if !pkMedSingleOutScope.mediumStoreSpeechMatch(model.Session{
		Project:  "kitchen",
		Messages: []model.Message{{Role: "user", Text: "бойлер нагрелся"}},
	}) {
		t.Fatalf("expected single nonGen >= TopIDF-0.9 outside caller scope to match")
	}
	if (&PromptPicker{TopIDF: 1.5}).mediumStoreSpeechMatch(cwdSessTogether) {
		t.Fatalf("expected TopIDF < mediumStoreIDFFloor to return false")
	}

	// Fixed/named long session, disjunctive fixed OR, and 2-named partial fallback.
	pkFixed := &PromptPicker{
		Terms:  []string{"kestrel", "escrow", "postgres", "redis", "timeout"},
		Asked:  "kestrel or escrow timeout",
		IDFOf:  map[string]float64{"kestrel": 5.0, "escrow": 4.9, "postgres": 2.8, "redis": 2.7, "timeout": 1.5},
		TopIDF: 5.5,
	}
	longMissingTogether := model.Session{
		Messages: make([]model.Message, 305),
	}
	longMissingTogether.Messages[0] = model.Message{Role: "user", Text: "kestrel config"}
	longMissingTogether.Messages[10] = model.Message{Role: "user", Text: "escrow config"}
	if pkFixed.LeadTermsMeet(longMissingTogether) {
		t.Fatalf("expected long session with fixed terms split across distant messages to fail")
	}
	if !pkFixed.LeadTermsMeet(model.Session{
		Messages: []model.Message{{Role: "user", Text: "kestrel had a postgres timeout yesterday"}},
	}) {
		t.Fatalf("expected disjunctive OR query with 1 fixed term + 2 terms together to match")
	}

	pkNamedPartial := &PromptPicker{
		Terms:  []string{"alpha", "beta", "rare1", "rare2"},
		Asked:  "alpha or beta setup?",
		IDFOf:  map[string]float64{"alpha": 4.2, "beta": 4.1, "rare1": 2.6, "rare2": 2.7},
		TopIDF: 4.5,
	}
	if !pkNamedPartial.LeadTermsMeet(model.Session{
		Messages: []model.Message{{Role: "user", Text: "alpha works with rare1 and rare2"}},
	}) {
		t.Fatalf("expected named partial fallback via 2 rare>=2.5 terms together to match")
	}

	// markDigestWorth open-advice suppression, single-named, MergeUnscoped append & carriesPrimarySubjects.
	pkAdv := &PromptPicker{
		Terms:  []string{"postgres", "advice"},
		Asked:  "any advice on postgres tuning?",
		IDFOf:  map[string]float64{"postgres": 4.5, "advice": 1.0},
		TopIDF: 5.0,
		Self:   map[string]bool{},
	}
	pkAdv.Pick([]model.Session{{
		ID:       "adv-1",
		Updated:  t0,
		Messages: []model.Message{{Role: "user", Text: "postgres tuning notes"}, {Role: "assistant", Text: "postgres shared_buffers set to 4GB."}},
	}}, []int{1}, []int{1}, []int{1})
	if pkAdv.WorthDigest() {
		t.Fatalf("expected open advice query without recall cue to keep WorthDigest=false")
	}

	pkMerge := &PromptPicker{Terms: []string{"alpha", "beta"}, IDFOf: map[string]float64{"alpha": 4.0, "beta": 4.0}}
	otherFork := pkMerge.Fork(false, pkMerge.IDFOf, pkMerge.Terms)
	otherFork.Pick([]model.Session{{
		ID:       "fork-s1",
		Updated:  t0,
		Messages: []model.Message{{Role: "user", Text: "alpha and beta configured"}, {Role: "assistant", Text: "Decision: alpha beta active."}},
	}}, []int{2}, []int{2}, []int{2})
	pkMerge.MergeUnscoped(&otherFork, pkMerge.Terms)
	pkMerge.MergeUnscoped(&otherFork, pkMerge.Terms) // duplicate ID should not be added twice
	pkMerge.MergeUnscoped(nil, pkMerge.Terms)
	if len(pkMerge.Picked()) != 1 || pkMerge.Picked()[0].ID != "fork-s1" {
		t.Fatalf("expected MergeUnscoped to add fork-s1 once, got %+v", pkMerge.Picked())
	}

	// carriesPrimarySubjects rejects single-subject 3rd session and UncoveredWhenClause covers left/both.
	pkPrim := &PromptPicker{
		Terms:  []string{"alpha", "beta", "gamma"},
		Asked:  "when alpha started and beta finished",
		IDFOf:  map[string]float64{"alpha": 2.6, "beta": 2.5, "gamma": 2.4},
		picked: []model.Session{{ID: "p1", Messages: []model.Message{{Role: "user", Text: "alpha and beta setup"}}}},
	}
	if pkPrim.carriesPrimarySubjects(model.Session{ID: "p3", Messages: []model.Message{{Role: "user", Text: "only alpha here"}}}) {
		t.Fatalf("expected candidate carrying only 1 primary subject to be rejected for 3rd slot")
	}

	// OrderDistinctDateUpdates Started timestamp fallback and non-matching 2nd session.
	sStartedOld := model.Session{ID: "s-old", Started: t0, Messages: []model.Message{{Role: "user", Text: "alpha one"}}}
	sStartedNew := model.Session{ID: "s-new", Started: t1, Messages: []model.Message{{Role: "user", Text: "other topic"}}}
	unchanged := OrderDistinctDateUpdates([]model.Session{sStartedOld, sStartedNew}, []string{"alpha"}, map[string]float64{"alpha": 2.8})
	if unchanged[0].ID != "s-old" {
		t.Fatalf("expected OrderDistinctDateUpdates to preserve order when second session omits subject")
	}
	if got := sessionTimestamp(sStartedOld); !got.Equal(t0) {
		t.Fatalf("expected Started timestamp fallback, got %v", got)
	}

	// Edge helpers: isHexHashLike, isAllDigitsWord, ByIdentifying, SameAnswerAs, UncoveredWhenClause left/nil, RareTerms.
	if isHexHashLike("123") || isHexHashLike("xyz12345") || !isHexHashLike("deadbeefcafe") {
		t.Fatalf("isHexHashLike edge check failed")
	}
	if isAllDigitsWord("") || !isAllDigitsWord("42") || isAllDigitsWord("4a") {
		t.Fatalf("isAllDigitsWord edge check failed")
	}
	if got := ByIdentifying([]string{"only"}, nil); len(got) != 1 {
		t.Fatalf("ByIdentifying single term failed")
	}
	if got := RareTerms([]string{"a", "b"}, map[string]float64{"a": 3.0, "b": 1.0}, 2.0); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("RareTerms failed: %v", got)
	}
	if _, _, ok := WhenClausePairTerms("only three terms", []string{"a", "b", "c"}, nil); ok {
		t.Fatalf("expected <4 terms to return false")
	}
	if _, _, ok := WhenClausePairTerms("alpha and beta setup done", []string{"alpha", "beta", "setup", "done"}, nil); ok {
		t.Fatalf("expected non-between 'and' to return false")
	}
	// UncoveredWhenClause returning left or nil.
	askedW := "when alpha started and beta finished"
	lW := []string{"alpha", "started"}
	rW := []string{"beta", "finished"}
	pkW := &PromptPicker{
		Asked:  askedW,
		IDFOf:  map[string]float64{"alpha": 3.0, "beta": 3.0, "started": 1.2, "finished": 1.2},
		picked: []model.Session{{Messages: []model.Message{{Role: "user", Text: "beta finished today"}}}},
	}
	if got := pkW.UncoveredWhenClause(lW, rW); !slices.Equal(got, lW) {
		t.Fatalf("expected UncoveredWhenClause to return left %v, got %v", lW, got)
	}
	pkW.picked = append(pkW.picked, model.Session{Messages: []model.Message{{Role: "user", Text: "alpha started today"}}})
	if got := pkW.UncoveredWhenClause(lW, rW); got != nil {
		t.Fatalf("expected both clauses covered -> nil, got %v", got)
	}
}

func TestPromptPickerPickLimitsPoliciesAndTiebreaks(t *testing.T) {
	t0 := time.Date(2025, 4, 1, 9, 0, 0, 0, time.UTC)

	// Pol policy denial, empty digest rejection, Focus==nil on long session, and extraMatches>=5 break.
	terms := []string{"kestrel", "broker"}
	idf := map[string]float64{"kestrel": 5.0, "broker": 4.8}
	makeSess := func(id string, dayOffset int, body string) model.Session {
		return model.Session{
			ID:      id,
			Project: "core",
			Updated: t0.Add(time.Duration(dayOffset) * 24 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "kestrel broker discussion " + body},
				{Role: "assistant", Text: "kestrel broker configured for " + body},
			},
		}
	}
	denied := makeSess("denied", 0, "blocked")
	denied.Project = "imported:team/proj"
	emptyDigest := model.Session{
		ID:      "empty-digest",
		Project: "core",
		Updated: t0,
		Messages: []model.Message{
			{Role: "user", Text: "kestrel broker " + strings.Repeat("noise ", 3)},
		},
	}
	longNilFocus := model.Session{
		ID:      "long-nil-focus",
		Project: "core",
		Updated: t0.Add(2 * time.Hour),
		Held:    350,
		Messages: []model.Message{
			{Role: "user", Text: "kestrel broker shard setup alpha"},
			{Role: "assistant", Text: "kestrel broker shard alpha active"},
		},
	}
	cands := []model.Session{
		denied,
		emptyDigest,
		longNilFocus,
		makeSess("s2", 1, "bravo"),
		makeSess("s3", 1, "charlie"),
		makeSess("s4", 1, "delta"),
		makeSess("s5", 1, "echo"),
		makeSess("s6", 1, "foxtrot"),
		makeSess("s7", 1, "golf"),
		makeSess("s8", 1, "hotel"),
	}
	pk := &PromptPicker{
		Pol:    policy.Policy{Activations: map[string]map[string]bool{policy.ActivationAuto: {"imported": false}}},
		Terms:  terms,
		IDFOf:  idf,
		TopIDF: 5.2,
		DigestBudget: func(_ bool) int {
			return 600
		},
	}
	// Empty substantive digest (smoke-test / no-quote session) crowds out candidate.
	pkZero := &PromptPicker{
		Terms: terms,
		IDFOf: idf,
	}
	noMemSess := model.Session{
		ID:      "no-mem",
		Project: "core",
		Updated: t0,
		Messages: []model.Message{
			{Role: "user", Text: "Reply with the word OK about kestrel broker."},
			{Role: "assistant", Text: "OK"},
		},
	}
	pkZero.Pick([]model.Session{noMemSess}, []int{2}, []int{1}, []int{1})
	if len(pkZero.Picked()) != 0 || pkZero.CrowdedOut() != 1 {
		t.Fatalf("expected empty substantive digest to crowd out candidate, got picked=%d crowded=%d", len(pkZero.Picked()), pkZero.CrowdedOut())
	}

	m := make([]int, len(cands))
	for i := range cands {
		m[i] = 2
	}
	pk.Pick(cands, m, m, m)
	if pk.ExtraMatches() != 5 {
		t.Fatalf("expected Pick to stop once ExtraMatches hits 5, got %d", pk.ExtraMatches())
	}

	// rareSubjectUpdateWorth with distinct date + named<0 strongNative markDigestWorth.
	pkRare := &PromptPicker{
		Terms:    []string{"омега", "лимит"},
		Asked:    "что решили про омега лимит?",
		IDFOf:    map[string]float64{"омега": 3.5, "лимит": 1.5},
		TopIDF:   3.6,
		CwdPlain: "infra",
		Unscoped: true,
	}
	ruFirst := model.Session{
		ID:      "ru-a",
		Project: "infra",
		Updated: t0,
		Messages: []model.Message{
			{Role: "user", Text: "обсудили сервис Омега и лимит запросов"},
			{Role: "assistant", Text: "Установили лимит сервиса Омега на 100 RPS."},
		},
	}
	ruSecond := model.Session{
		ID:      "ru-b",
		Project: "infra",
		Updated: t0.Add(24 * time.Hour),
		Messages: []model.Message{
			{Role: "user", Text: "проверка сервис Омега сегодня"},
		},
	}
	pkRare.Pick([]model.Session{ruFirst, ruSecond}, []int{2, 1}, []int{0, 0}, []int{-1, -1})
	if len(pkRare.Picked()) != 2 || !pkRare.WorthDigest() {
		t.Fatalf("expected rareSubjectUpdateWorth to rescue ru-b and mark WorthDigest, got picked=%d worth=%v", len(pkRare.Picked()), pkRare.WorthDigest())
	}

	// Long named need >= 2 together in speech vs apart, and named partial in caller scope.
	pkNamed := &PromptPicker{
		Terms:    []string{"alpha", "beta", "gamma", "delta"},
		IDFOf:    map[string]float64{"alpha": 3.2, "beta": 3.2, "gamma": 2.4, "delta": 2.3},
		TopIDF:   3.2,
		CwdPlain: "infra",
		Unscoped: true,
	}
	longSplit := model.Session{
		Project:  "infra",
		Messages: make([]model.Message, 305),
	}
	longSplit.Messages[0] = model.Message{Role: "user", Text: "alpha here"}
	longSplit.Messages[10] = model.Message{Role: "user", Text: "beta here"}
	if pkNamed.LeadTermsMeet(longSplit) {
		t.Fatalf("expected long session with named pair apart to return false")
	}
	if !pkNamed.LeadTermsMeet(model.Session{
		Project:  "infra",
		Messages: []model.Message{{Role: "user", Text: "alpha paired with gamma and delta in infra"}},
	}) {
		t.Fatalf("expected named>=2 caller-scope fallback with 2 nonGen>=2.2 together to match")
	}

	// fixedLeadTermsMeet when fixedNamed has >=2 but after filtering framing modifiers has <2.
	pkFixedFraming := &PromptPicker{
		Terms:  []string{"week", "month", "cocktail"},
		IDFOf:  map[string]float64{"week": 5.0, "month": 4.9, "cocktail": 3.8},
		TopIDF: 4.0,
	}
	if decided, _ := pkFixedFraming.fixedLeadTermsMeet(ruFirst, false); decided {
		t.Fatalf("expected <2 non-generic fixed terms to leave decided=false")
	}

	// ByIdentifying oki!=okj tiebreak and DistinctSessionDates / sameWords / UserCarriesRareSubjectUpdate edge guards.
	sorted := ByIdentifying([]string{"unknown", "known"}, map[string]float64{"known": 0})
	if sorted[0] != "known" {
		t.Fatalf("expected known term before unknown term when IDF values tie at 0, got %v", sorted)
	}
	if DistinctSessionDates(nil, model.Session{}) {
		t.Fatalf("expected empty Updated date to return DistinctSessionDates=false")
	}
	if SameAnswerAs([]model.Session{{}}, model.Session{Messages: []model.Message{{Role: "user", Text: "one"}}}, []string{"one", "two"}) {
		t.Fatalf("expected empty matchedWords to return false")
	}
	if sameWords(nil, map[string]bool{"a": true}) {
		t.Fatalf("expected sameWords with empty map to return false")
	}
	if UserCarriesRareSubjectUpdate(ruSecond, nil, terms, idf) {
		t.Fatalf("expected empty picked slice to return false")
	}
	if (&PromptPicker{}).rareSubjectUpdateWorth(ruSecond, 0) {
		t.Fatalf("expected matched=0 to return rareSubjectUpdateWorth=false")
	}
	if (&PromptPicker{}).mediumStoreWorth(ruSecond, 0) {
		t.Fatalf("expected matched=0 to return mediumStoreWorth=false")
	}
	if len((&PromptPicker{}).SpokenSubjectTerms(2.0)) != 0 {
		t.Fatalf("expected empty picked SpokenSubjectTerms=nil")
	}
}

func TestPromptPickerConclusionOrderAndEdgePaths(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	// LeadWithConclusion + ConcludedAbout + matchedLines (assistant conclusion + user problem).
	single := []model.Session{{ID: "only"}}
	if got := LeadWithConclusion(single, []string{"retry", "budget"}); len(got) != 1 {
		t.Fatalf("expected single slice back unchanged")
	}
	mention := model.Session{
		ID:      "mention",
		Project: "core",
		Updated: t0,
		Messages: []model.Message{
			{Role: "assistant", Text: "We discussed the retry budget during triage."},
		},
	}
	decidedAssistant := model.Session{
		ID:      "decided-asst",
		Project: "core",
		Updated: t0.Add(time.Hour),
		Messages: []model.Message{
			{Role: "assistant", Text: "Use retry budget of 3 attempts because exponential backoff timed out."},
		},
	}
	gotLead := LeadWithConclusion([]model.Session{mention, decidedAssistant}, []string{"retry", "budget"})
	if gotLead[0].ID != "decided-asst" {
		t.Fatalf("expected assistant conclusion to move to slot 0, got %s", gotLead[0].ID)
	}
	decidedUser := model.Session{
		ID:      "decided-user",
		Project: "core",
		Updated: t0,
		Messages: []model.Message{
			{Role: "user", Text: "Switch retry budget to 5 attempts."},
		},
	}
	gotUser := LeadWithConclusion([]model.Session{decidedUser, mention}, []string{"retry", "budget"})
	if gotUser[0].ID != "decided-user" {
		t.Fatalf("expected user conclusion already at slot 0 to remain, got %s", gotUser[0].ID)
	}
	noConc := LeadWithConclusion([]model.Session{
		{ID: "plain-1", Project: "core"},
		{ID: "plain-2", Project: "core"},
	}, []string{"retry", "budget"})
	if noConc[0].ID != "plain-1" {
		t.Fatalf("expected sessions without conclusions to preserve order, got %s", noConc[0].ID)
	}

	// AddIDF empty + nil initialization + merge.
	pkIDF := &PromptPicker{}
	pkIDF.AddIDF(nil)
	pkIDF.AddIDF(map[string]float64{"retry": 4.0, "budget": 3.5})
	pkIDF.AddIDF(map[string]float64{"retry": 1.0, "backoff": 4.2})
	if pkIDF.IDFOf["retry"] != 4.0 || pkIDF.IDFOf["backoff"] != 4.2 {
		t.Fatalf("unexpected AddIDF merge result: %+v", pkIDF.IDFOf)
	}

	// effectiveLeadTerms truncating to LeadTermsKept (>3 terms) when LeadTerms unset.
	pkTrunc := &PromptPicker{
		Terms: []string{"alpha", "beta", "gamma", "delta"},
		IDFOf: map[string]float64{"alpha": 4.1, "beta": 4.0, "gamma": 3.9, "delta": 2.0},
	}
	if eff := pkTrunc.effectiveLeadTerms(); len(eff) != LeadTermsKept {
		t.Fatalf("expected effectiveLeadTerms len=%d, got %d (%v)", LeadTermsKept, len(eff), eff)
	}

	// TrimWeakThirdSession: drops single-term distractor when 2 picked carry 2+ terms.
	strongA := model.Session{
		ID: "s1", Project: "core", Updated: t0,
		Messages: []model.Message{{Role: "user", Text: "retry budget config"}},
	}
	strongB := model.Session{
		ID: "s2", Project: "core", Updated: t0.Add(24 * time.Hour),
		Messages: []model.Message{{Role: "user", Text: "retry budget limit"}},
	}
	weakC := model.Session{
		ID: "s3", Project: "core", Updated: t0.Add(48 * time.Hour),
		Messages: []model.Message{{Role: "user", Text: "only retry mentioned"}},
	}
	pkTrim := &PromptPicker{
		Terms:  []string{"retry", "budget"},
		IDFOf:  map[string]float64{"retry": 4.0, "budget": 3.5},
		picked: []model.Session{strongA, strongB, weakC},
	}
	trimmed := pkTrim.TrimWeakThirdSession()
	if len(trimmed) != 2 {
		t.Fatalf("expected TrimWeakThirdSession to drop single-term distractor, got %d", len(trimmed))
	}
	pkNoTrim := &PromptPicker{
		Terms:  []string{"retry", "budget"},
		picked: []model.Session{strongA, weakC, {ID: "s4", Messages: []model.Message{{Role: "user", Text: "budget only"}}}},
	}
	if len(pkNoTrim.TrimWeakThirdSession()) != 3 {
		t.Fatalf("expected TrimWeakThirdSession to keep 3 when <2 multi-term")
	}

	// OrderDistinctDateUpdates with Started fallback + newer promotion.
	olderUpdated := model.Session{
		ID: "old", Project: "core", Updated: t0,
		Messages: []model.Message{{Role: "user", Text: "retry config v1"}},
	}
	zeroUpdatedStarted := model.Session{
		ID: "started-mid", Project: "core", Started: t0.Add(24 * time.Hour),
		Messages: []model.Message{{Role: "user", Text: "retry config mid"}},
	}
	newerUpdate := model.Session{
		ID: "new", Project: "core", Updated: t0.Add(72 * time.Hour),
		Messages: []model.Message{{Role: "user", Text: "retry config v2"}},
	}
	orderedUpdates := OrderDistinctDateUpdates(
		[]model.Session{olderUpdated, zeroUpdatedStarted, newerUpdate},
		[]string{"retry", "config"},
		map[string]float64{"retry": 4.0, "config": 1.2},
	)
	if len(orderedUpdates) != 3 || orderedUpdates[0].ID != "new" || orderedUpdates[1].ID != "started-mid" || orderedUpdates[2].ID != "old" {
		t.Fatalf("expected [new started-mid old], got %+v", []string{orderedUpdates[0].ID, orderedUpdates[1].ID, orderedUpdates[2].ID})
	}

	// WhenClausePairTerms with "between A and B" (2+ terms each side) and Russian equivalent.
	left, right, ok := WhenClausePairTerms(
		"compare latency between sqlite embedded store and postgres connection pool",
		[]string{"compare", "latency", "sqlite", "embedded", "store", "postgres", "connection", "pool"},
		map[string]float64{"sqlite": 4.2, "embedded": 2.4, "postgres": 4.3, "connection": 2.3},
	)
	if !ok || len(left) < 2 || len(right) < 2 {
		t.Fatalf("expected between-clause split, got ok=%v left=%v right=%v", ok, left, right)
	}
	rLeft, rRight, rOK := WhenClausePairTerms(
		"сравни нагрузку между сервисом омега и модулем альфа",
		[]string{"нагрузку", "сервисом", "омега", "модулем", "альфа"},
		map[string]float64{"нагрузку": 1.2, "сервисом": 2.4, "омега": 4.2, "модулем": 2.3, "альфа": 4.1},
	)
	if !rOK || len(rLeft) < 2 || len(rRight) < 2 {
		t.Fatalf("expected Russian between-clause split, got ok=%v left=%v right=%v", rOK, rLeft, rRight)
	}

	// carriesPrimarySubjects: rejects 3rd session missing primary salient pair unless disjunctive.
	pkPrim := &PromptPicker{
		Terms: []string{"kestrel", "broker"},
		IDFOf: map[string]float64{"kestrel": 4.5, "broker": 4.4},
		picked: []model.Session{
			{ID: "p1", Messages: []model.Message{{Role: "user", Text: "kestrel broker tuned"}}},
		},
	}
	if pkPrim.carriesPrimarySubjects(model.Session{Messages: []model.Message{{Role: "user", Text: "kestrel only"}}}) {
		t.Fatalf("expected candidate missing second salient primary subject to return false")
	}
	if !pkPrim.carriesPrimarySubjects(model.Session{Messages: []model.Message{{Role: "user", Text: "kestrel broker both"}}}) {
		t.Fatalf("expected candidate covering both primary subjects to return true")
	}

	// Edge guards for WhenClausePairTerms, OrderDistinctDateUpdates, MergeUnscoped, Pick Deadline & cron.
	if _, _, ok := WhenClausePairTerms("before x after", []string{"before", "x", "after"}, nil); ok {
		t.Fatalf("expected <4 terms to fail WhenClausePairTerms")
	}
	if _, _, ok := WhenClausePairTerms("foo and bar", []string{"foo", "bar", "baz", "qux"}, nil); ok {
		t.Fatalf("expected short clause sides to fail WhenClausePairTerms")
	}
	if got := OrderDistinctDateUpdates([]model.Session{olderUpdated, {ID: "unrelated", Updated: t0.Add(48 * time.Hour), Messages: []model.Message{{Role: "user", Text: "other"}}}}, []string{"retry", "config"}, map[string]float64{"retry": 4.0}); got[0].ID != "old" {
		t.Fatalf("expected OrderDistinctDateUpdates to keep order when subject missing")
	}
	pkMerge := &PromptPicker{Terms: []string{"kestrel", "broker"}, picked: []model.Session{strongA}, extraMatches: 4}
	pkMerge.MergeUnscoped(nil, pkMerge.Terms)
	otherMerge := &PromptPicker{picked: []model.Session{strongA, {ID: "dup-ans", Project: "core", Updated: t0, Messages: []model.Message{{Role: "user", Text: "kestrel broker tuned"}}}, strongB}, extraMatches: 4}
	pkMerge.MergeUnscoped(otherMerge, []string{"kestrel", "broker"})
	if pkMerge.ExtraMatches() != 5 {
		t.Fatalf("expected extraMatches capped at 5, got %d", pkMerge.ExtraMatches())
	}
	pkDeadline := &PromptPicker{
		Pol:      policy.Policy{},
		Terms:    []string{"retry", "budget"},
		IDFOf:    map[string]float64{"retry": 4.0, "budget": 3.5},
		TopIDF:   4.0,
		Deadline: t0,
	}
	pkDeadline.Pick([]model.Session{{ID: "cron-1", Kind: "cron"}, strongA, strongB}, []int{2, 2, 2}, []int{1, 1, 1}, []int{1, 1, 1})
	if len(pkDeadline.Picked()) != 1 {
		t.Fatalf("expected expired deadline after 1st pick to stop before 2nd, got %d", len(pkDeadline.Picked()))
	}
}

func TestPromptPickerMediumStoreSoleSubjectWithUnseenAcquisitionVerb(t *testing.T) {
	sess := model.Session{
		ID:      "garden-planter",
		Project: "garden",
		Updated: time.Date(2025, 5, 25, 16, 0, 0, 0, time.UTC),
		Messages: []model.Message{
			{Role: "user", Text: "Repotted the ceramic planter I got last month."},
			{Role: "assistant", Text: "Use fresh potting mix with drainage pebbles."},
		},
	}
	pk := &PromptPicker{
		Terms:    []string{"planter", "acquire", "last", "month"},
		IDFOf:    map[string]float64{"planter": 3.26, "acquire": 0, "last": 1.72, "month": 1.72},
		TopIDF:   3.51,
		CwdPlain: "garden",
	}
	if !pk.LeadTermsMeet(sess) {
		t.Fatalf("expected LeadTermsMeet=true when unseen word is acquisition modifier 'acquire'")
	}
	pk.Pick([]model.Session{sess}, []int{1}, []int{0}, []int{0})
	if len(pk.Picked()) != 1 || pk.Picked()[0].ID != "garden-planter" {
		t.Fatalf("expected garden-planter picked, got %+v", pk.Picked())
	}
	if !pk.WorthDigest() {
		t.Fatalf("expected worthDigest=true via mediumStoreWorth rescue, got false")
	}

	pk2 := &PromptPicker{
		Terms:    []string{"planter", "frobnicator", "last", "month"},
		IDFOf:    map[string]float64{"planter": 3.26, "frobnicator": 0, "last": 1.72, "month": 1.72},
		TopIDF:   3.51,
		CwdPlain: "garden",
	}
	pk2.Pick([]model.Session{sess}, []int{1}, []int{0}, []int{0})
	if len(pk2.Picked()) != 0 {
		t.Fatalf("expected unknown domain identifier 'frobnicator' to block single-term rescue, picked %+v", pk2.Picked())
	}
	pkUnscoped := &PromptPicker{
		Terms:    []string{"planter", "frobnicator", "last", "month"},
		IDFOf:    map[string]float64{"planter": 3.26, "frobnicator": 0, "last": 1.72, "month": 1.72},
		TopIDF:   3.51,
		Unscoped: true,
	}
	if pkUnscoped.LeadTermsMeet(sess) {
		t.Fatalf("expected LeadTermsMeet=false in unscoped fallback when unseen word is unknown domain identifier 'frobnicator'")
	}
}

func twoSessionsWithWork(now time.Time) []model.Session {
	return []model.Session{
		{
			Harness: "grok", Project: "app", ID: "01a00feb", Updated: now,
			Messages: []model.Message{
				{Role: "user", Text: "why does the build fail"},
				{Role: "tool-output", Text: "go build ./... : undefined parseThing"},
			},
		},
		{
			Harness: "grok", Project: "app", ID: "77bb2211", Updated: now,
			Messages: []model.Message{
				{Role: "user", Text: "why does the build fail here too"},
				{Role: "tool-output", Text: "go build ./... : undefined otherThing"},
			},
		},
	}
}

// Finding a session by what was said and then searching inside it for what was
// done is the second step deja had no way to take: the id had to be turned back
// into a file path and grepped by hand (#1321).
func TestSearchNarrowsToOneSession(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	hits, err := Run(ss, Options{Query: "build", All: true, Role: "tool", Session: "01a00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want only the named session: %#v", len(hits), hits)
	}
	if hits[0].Session.ID != "01a00feb" {
		t.Errorf("hit is session %s", hits[0].Session.ID)
	}
}

// Without the flag both sessions answer, so the test above is measuring the
// flag rather than a fixture that only had one match.
func TestSearchWithoutSessionSeesBoth(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	hits, err := Run(ss, Options{Query: "build", All: true, Role: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want both: %#v", len(hits), hits)
	}
}

// An id that names nothing answers with nothing rather than falling back to the
// whole store.
func TestSearchWithAnUnknownSessionFindsNothing(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	hits, err := Run(ss, Options{Query: "build", All: true, Session: "nosuchid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits for an id that names no session", len(hits))
	}
}

// The id a session was synced under resolves too, the way show and ctx take it
// (#1316).
func TestSearchNarrowsByTheIdASessionCameWith(t *testing.T) {
	ss := twoSessionsWithWork(time.Now())
	ss[0].ID = "imported-9f3c"
	ss[0].OrigID = "01a00feb"
	hits, err := Run(ss, Options{Query: "build", All: true, Session: "01a00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "imported-9f3c" {
		t.Errorf("got %d hits: %#v", len(hits), hits)
	}
}

func quoted(lines []string, want string) bool {
	for _, ln := range lines {
		if strings.Contains(ln, want) {
			return true
		}
	}
	return false
}

// Two slots go to the agent's lines: one for the line with the highest query
// word density, and one for the concluding line that settled the question.
func TestBlockKeepsTheLineThatSettledIt(t *testing.T) {
	// Three lines carry every word of the query; the conclusion carries one of
	// them. By word count alone it would not reach the two slots.
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool метрики сняли"},
		{Role: "assistant", Text: "pgbouncer pool метрики сравнили"},
		{Role: "assistant", Text: "pgbouncer pool метрики построили"},
		{Role: "assistant", Text: "в итоге pgbouncer остановились на 40"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if !quoted(lines, "остановились на 40") {
		t.Errorf("the line that settled it was dropped for word count: %q", lines)
	}
	if !quoted(lines, "метрики") {
		t.Errorf("the line naming what the session was about was dropped: %q", lines)
	}
}

// The two slots answer different questions: one line says what the session was
// about, the other what it settled, so the line naming the subject keeps its
// slot whatever else is quoted beside it.
func TestBlockKeepsTheSubjectBesideTheConclusion(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "в итоге решили: cron переехал на 03:17"},
		{Role: "assistant", Text: "pgbouncer pool держим на 40, pgbouncer перезапущен"},
		{Role: "assistant", Text: "pgbouncer метрики сняли, pgbouncer графики построены"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "решили"}, "что там с pgbouncer")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if !quoted(lines, "pgbouncer") {
		t.Errorf("the subject vanished from the block: %q", lines)
	}
}

// A conclusion rarely repeats all of the question's words: the line that named
// the subject comes first, and the answer follows it immediately.
func TestBlockTakesTheAnswerThatFollowsTheMention(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool метрики сняли"},
		{Role: "assistant", Text: "в итоге остановились на 40"},
		{Role: "assistant", Text: "pgbouncer pool метрики сравнили"},
		{Role: "assistant", Text: "pgbouncer pool метрики построили"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if !quoted(lines, "остановились на 40") {
		t.Errorf("the answer following the mention was never a candidate: %q", lines)
	}
}

// It has to follow a line that matched, though: a conclusion from another part
// of the session settles another question.
func TestBlockIgnoresAFarAwayConclusion(t *testing.T) {
	msgs := make([]model.Message, 0, 13)
	msgs = append(msgs, model.Message{Role: "user", Text: "что там с pgbouncer"})
	for range 3 {
		msgs = append(msgs, model.Message{Role: "assistant",
			Text: "pgbouncer pool метрики сняли"})
	}
	for range 8 {
		msgs = append(msgs, model.Message{Role: "assistant",
			Text: "правим совсем другое место"})
	}
	msgs = append(msgs, model.Message{Role: "assistant",
		Text: "в итоге cron переехал на 03:17"})
	_, lines := matchedLinesAsked(model.Session{Messages: msgs},
		[]string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if quoted(lines, "cron") {
		t.Errorf("a conclusion from elsewhere in the session was quoted: %q", lines)
	}
}

// The line taken from beside a match has to be a conclusion. Any next line
// qualifying would fill the second slot with whatever the agent said next.
func TestBlockDoesNotTakeAnOrdinaryNextLine(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "что там с pgbouncer"},
		{Role: "assistant", Text: "pgbouncer pool метрики сняли"},
		{Role: "assistant", Text: "гоню тесты дальше"},
		{Role: "assistant", Text: "pgbouncer pool метрики сравнили"},
		{Role: "assistant", Text: "pgbouncer pool метрики построили"},
	}}
	_, lines := matchedLinesAsked(s, []string{"pgbouncer", "pool", "метрики"},
		"что там с pgbouncer")
	if quoted(lines, "гоню тесты") {
		t.Errorf("an ordinary next line took the conclusion slot: %q", lines)
	}
}

// A flat 12-character cut printed `deja-note-cl` for every promoted note and
// `00000000-000` for UUID sessions — twelve identical rows in one answer
// (#707).
func TestShortKeepsIDsDistinguishable(t *testing.T) {
	notes := []string{"deja-note-claude-s0089", "deja-note-claude-s0023", "deja-note-claude-s0139"}
	seen := map[string]bool{}
	for _, id := range notes {
		got := short(id)
		if seen[got] {
			t.Errorf("%q collides with an earlier id: %q", id, got)
		}
		seen[got] = true
		if !strings.HasSuffix(got, id[len(id)-4:]) {
			t.Errorf("short(%q) = %q — the distinguishing tail is gone", id, got)
		}
	}
	uuid := "00000000-0003-4a1b-9c2d-000000000003"
	if got := short(uuid); got == short("00000000-0002-4a1b-9c2d-000000000002") {
		t.Errorf("two UUIDs shortened to the same string: %q", got)
	}
	// Short ids are printed whole.
	for _, id := range []string{"", "s0001", "abc-123-xyz"} {
		if got := short(id); got != id {
			t.Errorf("short(%q) = %q, want it unchanged", id, got)
		}
	}
	// The line stays narrow: that is what the function is for.
	if got := short(strings.Repeat("x", 80)); len([]rune(got)) > 20 {
		t.Errorf("short() returned %d runes", len([]rune(got)))
	}
	// Runes, not bytes: cutting mid-character would print a replacement glyph.
	cyr := strings.Repeat("сессия", 6)
	if got := short(cyr); !strings.ContainsRune(got, '…') || strings.ContainsRune(got, '�') {
		t.Errorf("short(%q) = %q", cyr, got)
	}
}

// #1090 stripped the bytes a terminal acts on from the headers these two
// functions print. PrintContext got it; PrintSession — what `deja show` calls
// — did not, so the same session printed a clean header through one and a
// header carrying an escape, a carriage return or a line break through the
// other.
func TestPrintSessionHeaderIsOneSafeLine(t *testing.T) {
	for _, tc := range []struct {
		name    string
		project string
		id      string
	}{
		{"escape", "w/\x1b[31mred\x1b[0m-app", "abcdef"},
		{"carriage return", "w/app", "abc\rdef"},
		{"newline", "w/app\nFAKE ROW pretending to be deja output", "abcdef"},
		{"bell", "w/app\a", "abc\adef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			PrintSession(&b, model.Session{
				Harness: "claude", Project: tc.project, ID: tc.id,
				Messages: []model.Message{{Role: "user", Text: "the retry queue stalls on staging"}},
			})
			header, _, _ := strings.Cut(b.String(), "\n")
			for _, r := range header {
				if unicode.IsControl(r) {
					t.Errorf("a control character reached the header: %q in %q", r, header)
					break
				}
			}
			// One line, and the whole header on it: a break in the project
			// name used to start a row that reads as deja's own output.
			lines := strings.Split(b.String(), "\n")
			if len(lines) < 2 || lines[1] != "" {
				t.Errorf("the header spilled onto a second line: %q", b.String())
			}
			if !strings.HasSuffix(header, tc.id[len(tc.id)-3:]) {
				t.Errorf("the header lost its tail: %q", header)
			}
		})
	}
}

// The two headers agree on the same session, which is the point.
func TestShowAndContextHeadersAgree(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "w/\x1b[31mred\x1b[0m-app", ID: "abc\rdef",
		Messages: []model.Message{{Role: "user", Text: "the retry queue stalls on staging"}},
	}
	var show, ctx strings.Builder
	PrintSession(&show, s)
	PrintContext(&ctx, s, "retry")
	showHead, _, _ := strings.Cut(show.String(), "\n")
	ctxHead, _, _ := strings.Cut(ctx.String(), "\n")
	if !strings.Contains(ctxHead, strings.TrimPrefix(showHead, "# ")) {
		t.Errorf("the two headers scrub differently:\n  show    %q\n  context %q", showHead, ctxHead)
	}
}

// A plain session is untouched.
func TestPrintSessionHeaderUnchangedForPlainText(t *testing.T) {
	var b strings.Builder
	PrintSession(&b, model.Session{
		Harness: "claude", Project: "w/app", ID: "abcdef",
		Messages: []model.Message{{Role: "user", Text: "hello"}},
	})
	if got, _, _ := strings.Cut(b.String(), "\n"); got != "# claude · w/app · abcdef" {
		t.Errorf("plain header changed: %q", got)
	}
}

// A harness check states the task first and how to answer last. Matching only
// the prompt's opening let those through: on a real store, two of the three
// sessions handed to an agent at session start were this exact shape.
func TestSmokeTestAskCanBeTheLastSentence(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "smoke", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "Use the deja recall tool to search for: openclaw deja harness live test alpha. Reply with the harness name of the top match only."},
			{Role: "assistant", Text: "openclaw"},
		},
	}
	if got := autoRecallSession(s, time.Now(), true); got != "" {
		t.Fatalf("smoke test reached an agent's context:\n%s", got)
	}
}

// And work that merely talks about replying must survive, however short the
// answer is.
func TestProseAboutRepliesIsNotASmokeTest(t *testing.T) {
	for _, text := range []string{
		"the gateway should reply with 404 when the token is stale, right?",
		"we answer with the cached value if the upstream is down",
	} {
		s := model.Session{
			Harness: "claude", ID: "work", Project: "goprojects/deja-vu",
			Updated: time.Now(),
			Messages: []model.Message{
				{Role: "user", Text: text},
				{Role: "assistant", Text: "yes, fixed"},
			},
		}
		if got := autoRecallSession(s, time.Now(), true); got == "" {
			t.Fatalf("real work was dropped as a smoke test: %q", text)
		}
	}
}

// The narrow case the filter was written for still has to go.
func TestClassicSmokeTestStillDropped(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "smoke2", Project: "goprojects/deja-vu",
		Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "Reply with the single word OK"},
			{Role: "assistant", Text: "OK"},
		},
	}
	if got := autoRecallSession(s, time.Now(), true); !strings.EqualFold(got, "") {
		t.Fatalf("classic smoke test survived:\n%s", got)
	}
}

// On the relevance tier a few sessions hold every word and the rest hold
// some. Dated in one run, newer partial matches pushed the real ones off the
// page (#5577); they stay ahead, each part newest first.
func TestByDateKeepsStrictHitsAhead(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	hit := func(id string, daysAgo int, strict bool) Hit {
		return Hit{Session: model.Session{ID: id, Updated: now.AddDate(0, 0, -daysAgo)}, Strict: strict}
	}
	hits := []Hit{hit("partial-new", 0, false), hit("strict-old", 9, true), hit("partial-old", 5, false), hit("strict-new", 3, true)}
	ByDate(hits)
	want := []string{"strict-new", "strict-old", "partial-new", "partial-old"}
	for i, h := range hits {
		if h.Session.ID != want[i] {
			t.Fatalf("order %d is %s, want %v", i, h.Session.ID, want)
		}
	}
}

// Under a date pool the sessions read only to check the unread estimate are
// older than the pool, with unread matches between them. On the page they
// skip those, so they count in the total and stay off it.
func TestDatePageLeavesTheSampleOff(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ss := make([]model.Session, 0, 7)
	for i := range 6 {
		ss = append(ss, model.Session{ID: fmt.Sprintf("s%d", i), Harness: "claude", Project: "p",
			Updated:  now.Add(-time.Duration(i) * time.Hour),
			Messages: []model.Message{{Role: "user", Text: "the deploy is slow", Time: now}}})
	}
	sampled := model.Session{ID: "sampled", Harness: "claude", Project: "p", Updated: now.AddDate(0, -2, 0),
		Messages: []model.Message{{Role: "user", Text: "the deploy is slow", Time: now}}}
	ss = append(ss, sampled)
	o := Options{Query: "deploy", Limit: 10, Sort: SortDate,
		Unread: &query.Unread{Sessions: 40, Length: 200, Sample: map[string]float64{"claude:sampled": 5}}}
	r, err := RunDetailed(ss, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 6 || !r.Capped || r.Total != 47 {
		t.Errorf("%d hits, capped %v, total %d; want the 6 of the pool, capped, 47", len(r.Hits), r.Capped, r.Total)
	}
	for _, h := range r.Hits {
		if h.Session.ID == "sampled" {
			t.Error("the sampled session is on a date page")
		}
	}

	// Every unread match read: the sample is an ordinary hit.
	o.Unread = &query.Unread{Sample: map[string]float64{"claude:sampled": 5}}
	if r, _ := RunDetailed(ss, o); len(r.Hits) != 7 {
		t.Errorf("with nothing left unread %d hits, want 7", len(r.Hits))
	}
}

// minSpanBrute is the answer tokenSpan has to give: the tightest window holding
// one occurrence of every token.
func minSpanBrute(low string, toks []string) int {
	places := make([][]int, len(toks))
	for i, tok := range toks {
		for at := 0; ; {
			j := strings.Index(low[at:], tok)
			if j < 0 {
				break
			}
			places[i] = append(places[i], at+j)
			at += j + 1
		}
		if len(places[i]) == 0 {
			return 0
		}
	}
	best := 1 << 30
	for _, p := range places[0] {
		lo, hi := p, p+len(toks[0])
		for i := 1; i < len(toks); i++ {
			bestGap, at := 1<<30, -1
			for _, q := range places[i] {
				gap := q - p
				if gap < 0 {
					gap = -gap
				}
				if gap < bestGap {
					bestGap, at = gap, q
				}
			}
			if at < lo {
				lo = at
			}
			if at+len(toks[i]) > hi {
				hi = at + len(toks[i])
			}
		}
		if hi-lo < best {
			best = hi - lo
		}
	}
	return best
}

// The case sampling could not see: every query word common, and the sentence
// where they meet sitting once, anywhere. Anchoring on the rarest token (#1318)
// answers it when one word is rare; when none is, the sample is chosen without
// knowing where the meeting is, and a 33 KB message measured 229 against a true
// 19 — across the boundary the proximity boost and the excerpt centring live on
// (#1319).
func TestTheTightestClusterIsFoundWhereverItSits(t *testing.T) {
	toks := []string{"retry", "queue", "staging"}
	for _, where := range []float64{0.03, 0.31, 0.5, 0.68, 0.97} {
		var b strings.Builder
		const n = 300
		at := int(float64(n) * where)
		for i := range n {
			switch {
			case i == at:
				b.WriteString("retry queue staging ")
			case i%3 == 0:
				b.WriteString("retry " + strings.Repeat("filler ", 15))
			case i%3 == 1:
				b.WriteString("queue " + strings.Repeat("filler ", 15))
			default:
				b.WriteString("staging " + strings.Repeat("filler ", 15))
			}
		}
		low := b.String()
		got := tokenWindow(low, toks)
		want := minSpanBrute(low, toks)
		if got != want {
			t.Errorf("meeting at %.0f%% of a %d-byte message: span %d, tightest is %d", where*100, len(low), got, want)
		}
		if got > proximityNear {
			t.Errorf("meeting at %.0f%%: span %d is past the %d boundary, so the hit loses its boost and its excerpt",
				where*100, got, proximityNear)
		}
	}
}

// And the answer is the tightest one on ordinary text too, wherever the real
// path runs. Where the first-occurrence bound already reads as one thought the
// cheap answer stands by design — it is an upper bound, it costs 101 ns against
// 122 µs for refining it, and both figures are inside the boost's flat end.
// Every message here is checked for which path it took.
func TestTheSpanMatchesTheBruteForceMinimum(t *testing.T) {
	words := []string{"retry", "queue", "staging", "jitter", "worker", "deploy"}
	toks := []string{"retry", "queue", "staging"}
	exact := 0
	for n := range 100 {
		var b strings.Builder
		seed := n*7 + 3
		for i := range 600 {
			if (i*seed)%23 == 0 {
				b.WriteString(words[(i*seed)%len(words)] + " ")
			} else {
				b.WriteString("filler ")
			}
		}
		low := b.String()
		got, want := tokenWindow(low, toks), minSpanBrute(low, toks)
		if want == 0 {
			continue
		}
		if cheapBound(low, toks) <= proximityNear {
			// The cheap path answered. It must still never claim the words sit
			// closer than they do.
			if got < want {
				t.Fatalf("message %d: the cheap bound %d is tighter than the true %d", n, got, want)
			}
			continue
		}
		exact++
		if got != want {
			t.Fatalf("message %d: span %d, tightest is %d", n, got, want)
		}
	}
	if exact == 0 {
		t.Fatal("no message reached the exact path, so this test proves nothing")
	}
}

// cheapBound is the first-occurrence span tokenSpan uses to decide whether
// refining is worth it.
func cheapBound(low string, toks []string) int {
	first, last := -1, -1
	for _, tok := range toks {
		i := strings.Index(low, tok)
		if i < 0 {
			return 0
		}
		if first < 0 || i < first {
			first = i
		}
		if end := i + len(tok); end > last {
			last = end
		}
	}
	return last - first
}

// A token the text does not carry still means no window, and a single-token
// query has none by definition.
func TestNoWindowWithoutEveryToken(t *testing.T) {
	if got := tokenWindow("the retry queue stalls", []string{"retry", "absent"}); got != 0 {
		t.Errorf("a missing token produced a window of %d", got)
	}
	if got := tokenWindow("the retry queue stalls", []string{"retry"}); got != 0 {
		t.Errorf("a single token produced a window of %d", got)
	}
}

// A version label printed by a failing job, a dependency bump and a pinned
// action is in the session and answers nothing about that version. Measured on
// a real store: of eight questions the per-prompt hook answered with unrelated
// work, six had every match under the role tool-output.
func TestSpeechCarriesAnyTermIgnoresToolOutput(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "прогони пайплайн ещё раз"},
		{Role: "tool-output", Text: "[toil.leader] Job 'WDLStartJob' v41 is completely failed"},
		{Role: "tool-output", Text: "uses: golangci/golangci-lint-action@v41"},
		{Role: "assistant", Text: "перезапустил, дальше смотрю логи"},
	}}
	terms := []string{"v41"}
	if !TextCarriesAnyTerm(s.Messages[1].Text, terms) {
		t.Fatal("the term is in the session; the plain test must still see it")
	}
	if SpeechCarriesAnyTerm(s, terms) {
		t.Error("only a tool printed v41, so nobody there answered anything about it")
	}
	s.Messages = append(s.Messages, model.Message{
		Role: "assistant", Text: "на v41 раскладка ломается, чинит только Ensure"})
	if !SpeechCarriesAnyTerm(s, terms) {
		t.Error("now someone said it, and that must count")
	}
}

func TestSpeechCarriesAnyTermWithoutTerms(t *testing.T) {
	s := model.Session{Messages: []model.Message{{Role: "assistant", Text: "что угодно"}}}
	if !SpeechCarriesAnyTerm(s, nil) {
		t.Error("no terms means nothing to withhold on")
	}
}

func stalenessSession(id, project, text string, updated time.Time) model.Session {
	return model.Session{
		ID: id, Harness: "claude", Project: project, Updated: updated,
		Messages: []model.Message{{Role: "user", Text: text, Time: updated}},
	}
}

func TestMarkEarlierAttempts(t *testing.T) {
	now := time.Now()
	old := stalenessSession("old1", "api", "connection pool exhausted under load, pgx acquire leak", now.AddDate(0, 0, -30))
	fresh := stalenessSession("new1", "api", "connection pool exhausted under load fixed with acquire timeout", now.AddDate(0, 0, -1))
	other := stalenessSession("oth1", "web", "connection pool exhausted under load in the browser", now.AddDate(0, 0, -30))

	hits, err := Run([]model.Session{old, fresh, other}, Options{Query: "connection pool exhausted", All: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.Session.ID] = h
	}
	if byID["old1"].Superseded == "" {
		t.Fatalf("old same-project hit not marked: %+v", byID["old1"])
	}
	if byID["old1"].Superseded != fresh.Updated.Format("2006-01-02") {
		t.Fatalf("superseded date = %q", byID["old1"].Superseded)
	}
	if byID["new1"].Superseded != "" {
		t.Fatalf("newest hit marked superseded: %+v", byID["new1"])
	}
	if byID["oth1"].Superseded != "" {
		t.Fatalf("different-project hit marked superseded: %+v", byID["oth1"])
	}
}

func TestMarkEarlierAttemptsNeedsOverlap(t *testing.T) {
	now := time.Now()
	old := stalenessSession("old2", "api", "jwt refresh rotation broke login jwks cache stale", now.AddDate(0, 0, -30))
	fresh := stalenessSession("new2", "api", "docker jwt oom killed the build runner memory limit", now.AddDate(0, 0, -1))
	hits, err := Run([]model.Session{old, fresh}, Options{Query: "jwt", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Superseded != "" {
			t.Fatalf("low-overlap hit marked superseded: %+v", h)
		}
	}
}

func TestPrintShowsEarlierAttempt(t *testing.T) {
	now := time.Now()
	old := stalenessSession("old3", "api", "rate limiter lets bursts through token bucket clock", now.AddDate(0, 0, -20))
	fresh := stalenessSession("new3", "api", "rate limiter lets bursts through fixed with monotonic token bucket", now.AddDate(0, 0, -1))
	hits, err := Run([]model.Session{old, fresh}, Options{Query: "rate limiter bursts", All: true})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "rate limiter bursts"})
	if !strings.Contains(b.String(), "earlier attempt") {
		t.Fatalf("print output missing earlier-attempt note: %q", b.String())
	}
}

// A day of notes is one session, so two days about the same decision overlap
// by construction — and the reader was told most of their own decision log
// was work the project had moved past (#863).
func TestNotesAreNotEarlierAttempts(t *testing.T) {
	now := time.Now()
	oldNote := stalenessSession("deja-day1-api", "api", "decided to keep the connection pool at 50", now.AddDate(0, 0, -30))
	oldNote.Harness = notesHarness
	newNote := stalenessSession("deja-day2-api", "api", "decided to keep the connection pool at 50 after review", now.AddDate(0, 0, -1))
	newNote.Harness = notesHarness
	// A transcript in the same project still supersedes another transcript:
	// the exclusion is about notes, not about the whole project.
	oldSess := stalenessSession("s-old", "api", "decided to keep the connection pool at 50", now.AddDate(0, 0, -30))
	// A transcript whose only newer rival is a note: writing a note about a
	// decision is not the project moving past the session that made it.
	lonely := stalenessSession("s-lonely", "web", "decided to keep the connection pool at 50", now.AddDate(0, 0, -30))
	lonelyNote := stalenessSession("deja-day2-web", "web", "decided to keep the connection pool at 50 after review", now.AddDate(0, 0, -1))
	lonelyNote.Harness = notesHarness
	newSess := stalenessSession("s-new", "api", "decided to keep the connection pool at 50 after review", now.AddDate(0, 0, -1))

	hits, err := Run([]model.Session{oldNote, newNote, oldSess, newSess, lonely, lonelyNote}, Options{Query: "connection pool", All: true})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.Session.ID] = h
	}
	if got := byID["deja-day1-api"].Superseded; got != "" {
		t.Errorf("note labelled an earlier attempt: %q", got)
	}
	if byID["s-old"].Superseded == "" {
		t.Error("an older transcript stopped being marked")
	}
	if got := byID["s-lonely"].Superseded; got != "" {
		t.Errorf("transcript marked as superseded by a note: %q", got)
	}
}

func TestEarlierAttemptMarkedBySharedTouchedFile(t *testing.T) {
	now := time.Now()
	older := model.Session{
		ID:      "older-edit",
		Harness: "claude",
		Project: "api",
		Updated: now.AddDate(0, 0, -14),
		Touched: []string{"internal/pool/retry.go"},
		Messages: []model.Message{
			{Role: "user", Text: "backoff retry jitter behaves badly under load"},
			{Role: "assistant", Text: "Adjusted the initial cap in internal/pool/retry.go to two seconds."},
		},
	}
	newer := model.Session{
		ID:      "newer-edit",
		Harness: "claude",
		Project: "api",
		Updated: now.AddDate(0, 0, -1),
		Touched: []string{"internal/pool/retry.go"},
		Messages: []model.Message{
			{Role: "user", Text: "backoff schedule still spikes during failover"},
			{Role: "assistant", Text: "Replaced the linear step in internal/pool/retry.go with full jitter."},
		},
	}
	hits := RelevanceHitsWeighted([]model.Session{newer, older}, []string{"backoff", "retry"}, nil)
	byID := map[string]Hit{}
	for _, h := range hits {
		byID[h.Session.ID] = h
	}
	if byID["older-edit"].Superseded == "" {
		t.Fatalf("expected older session touching the same file to be marked superseded, got %+v", byID["older-edit"])
	}
	if got := EarlierAttempts([]model.Session{newer, older}); got["older-edit"] == "" {
		t.Fatalf("expected EarlierAttempts to mark older-edit via shared touched file, got %v", got)
	}
}

// A hit is marked as an earlier pass over the same problem when it overlaps
// heavily with a newer session in the same project. Overlap was measured in
// whitespace-separated words, and Chinese, Japanese and Korean write none, so
// two sessions about the same thing shared nothing and the marker never fired.
func TestSupersededMarkerWorksInCJK(t *testing.T) {
	now := time.Now()
	body := "我们把调度器移到了单独的进程并且把重试次数限制为三次这样任务就不会重复执行了"
	older := model.Session{
		Harness: "claude", Project: "app", ID: "older", Updated: now.AddDate(0, 0, -30),
		Messages: []model.Message{{Role: "assistant", Text: body}},
	}
	newer := model.Session{
		Harness: "claude", Project: "app", ID: "newer", Updated: now,
		Messages: []model.Message{{Role: "assistant", Text: body + "后来又调整了一次"}},
	}
	hits, err := Run([]model.Session{older, newer}, Options{Query: "调度器"})
	if err != nil {
		t.Fatal(err)
	}
	marked := false
	for _, h := range hits {
		if h.Session.ID == "older" && h.Superseded != "" {
			marked = true
		}
	}
	if !marked {
		t.Error("the older Chinese session was not marked as an earlier pass")
	}
}

// Two sessions about different things must not be marked, or the label means
// nothing.
func TestSupersededMarkerSkipsUnrelatedCJK(t *testing.T) {
	now := time.Now()
	older := model.Session{
		Harness: "claude", Project: "app", ID: "older", Updated: now.AddDate(0, 0, -30),
		Messages: []model.Message{{Role: "assistant", Text: "我们把调度器移到了单独的进程" + strings.Repeat("并且写了很多说明", 4)}},
	}
	newer := model.Session{
		Harness: "claude", Project: "app", ID: "newer", Updated: now,
		Messages: []model.Message{{Role: "assistant", Text: "调度器的日志格式换成了结构化输出" + strings.Repeat("完全不同的内容在这里", 4)}},
	}
	hits, err := Run([]model.Session{older, newer}, Options{Query: "调度器"})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Session.ID == "older" && h.Superseded != "" {
			t.Error("an unrelated older session was marked as an earlier pass")
		}
	}
}

// The search output honoured NO_COLOR and ignored TERM=dumb, so a terminal
// that cannot render escapes got them anyway (#903).
func TestColorOKHonoursDumbTerminals(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if !colorOK(f) {
		t.Fatal("a character device with a real TERM lost its colour")
	}
	t.Setenv("TERM", "dumb")
	if colorOK(f) {
		t.Error("TERM=dumb still coloured")
	}
	// And the older half of the rule, which nothing covered: removing it
	// passed the whole suite.
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	if colorOK(f) {
		t.Error("NO_COLOR stopped being honoured")
	}
}

type wrapped struct{ w io.Writer }

func (x wrapped) Write(p []byte) (int, error) { return x.w.Write(p) }
func (x wrapped) Unwrap() io.Writer           { return x.w }

// `deja search` prints through a counter so the log records what went out,
// and colorOK saw the counter rather than the terminal under it: every result
// came out plain in a terminal (#4620). A wrapper that says what it wraps is
// looked through; one that does not, or a buffer, still gets plain text.
func TestColorOKLooksThroughAWrapper(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	if !colorOK(wrapped{w: wrapped{w: f}}) {
		t.Error("a terminal behind two wrappers lost its colour")
	}
	if colorOK(wrapped{w: &bytes.Buffer{}}) {
		t.Error("a buffer behind a wrapper was coloured")
	}
	t.Setenv("NO_COLOR", "1")
	if colorOK(wrapped{w: f}) {
		t.Error("NO_COLOR was not honoured through a wrapper")
	}
}

// A term long enough to survive losing its ending matches the inflected forms
// of itself; a short one does not, because a trimmed prefix of it would match
// half the language. The ranking reaches sessions through a stem fold and the
// digest did not, so a question about "индексация" found no line in the session
// that says "индексацию" and the block fell back to the top of the transcript.
func TestTermStemMatchesInflectionAndNotEverything(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		term string
		want bool
	}{
		{"the same form", "индексацию перенесли в воркер", "индексацию", true},
		{"an inflected ending", "индексацию перенесли в воркер", "индексация", true},
		{"another case ending", "решение про индексации приняли вчера", "индексация", true},
		// Short terms keep the exact rule: "index" trimmed to "ind" would sit
		// inside "indeed", "independent" and a hundred others.
		{"a short term is not trimmed", "indeed we shipped it", "index", false},
		{"a short term still matches exactly", "the index was rebuilt", "index", true},
		// Same root, different ending, is the point of the trim: "migrating"
		// and "migration" are one topic.
		{"a long term matches its own root", "the migration ran", "migrating", true},
		// Trimming must not reach so far that a shorter unrelated word inside
		// the same family collides.
		{"a long term does not match a stranger", "документ подписан вчера", "документация", false},
		{"an empty term matches nothing", "anything at all", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextCarriesTerm(tc.text, tc.term); got != tc.want {
				t.Errorf("TextCarriesTerm(%q, %q) = %v, want %v", tc.text, tc.term, got, tc.want)
			}
		})
	}
}

// A term is a mention where a word begins. Traced by instrumenting the matcher
// on a real store: "mini" scored a hit on "cron minimum granularity is 1
// minute", and that row of a table then won the slot the reader sees first.
//
// Short terms must also end the word. Requiring that of everything under seven
// characters was measured and cost more than it saved — the benchmark went
// 14/14 to 13/14 and the live rate 88% to 86%, because "hermes", "score" and
// "tick" belong inside longer words. Four characters is where the trade turns.
func TestTermIsAMentionWhereAWordBegins(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		term string
		want bool
	}{
		{"a short term inside a longer word", "cron minimum granularity is 1 minute", "mini", false},
		{"a short term as its own word", "подними mini и проверь", "mini", true},
		{"a short term before punctuation", "файл mini.yaml на месте", "mini", true},
		// Long enough to be trusted as a prefix: these read as one topic.
		{"a longer term running on", "the scoreboard was rebuilt", "score", true},
		{"a longer term after a separator", "смотри ~/.hermes/config", "hermes", true},
		// The stem may run on to the right; it still has to start a word.
		{"an inflected ending", "индексацию перенесли", "индексация", true},
		{"a stem inside a longer word", "переиндексацию отложили", "индексация", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextCarriesTerm(tc.text, tc.term); got != tc.want {
				t.Errorf("TextCarriesTerm(%q, %q) = %v, want %v", tc.text, tc.term, got, tc.want)
			}
		})
	}
}

// The relevance excerpt asks which of a query's terms each message holds. A
// Russian word arrives as forty surface forms, and every one of them used to be
// searched for over every message (#5378); the forms are now checked where
// their stem was found, and the answer must be the one a search per term gives.
func TestTermSetMatchesASearchPerTerm(t *testing.T) {
	stems := []string{"сервер", "ошибк", "мост", "при"}
	suffixes := []string{"", "а", "у", "ом", "ами", "ах", "ов", "е", "и", "ы", "ей", "ями"}
	terms := make([]string, 0, len(suffixes)*len(stems)+5)
	for _, s := range stems {
		for _, x := range suffixes {
			terms = append(terms, s+x)
		}
	}
	terms = append(terms, "ошибка", "", "mostly", "数据库", "服务")
	texts := []string{
		"",
		"сервера упали, ошибками завален лог",
		"при приеме: самостоятельно мостами и мостов",
		"серверсервером сервер",
		"ошибк ошибкиями",
		"нет совпадений вообще",
		"数据库连接失败 mostly",
		"приприприями",
	}
	set := newTermSet(terms)
	found := make([]bool, len(terms))
	var pos []int
	for _, text := range texts {
		low := lower(text)
		pos, _ = set.match(low, found, pos)
		folded := cjkfold.String(low)
		for i, term := range terms {
			want := strings.Contains(low, term)
			if ft := cjkfold.String(term); !want && (ft != term || cjkfold.HasCJK(term)) {
				want = strings.Contains(folded, ft)
			}
			if found[i] != want {
				t.Errorf("%q in %q: termSet says %v, a search says %v", term, text, found[i], want)
			}
		}
	}
}

// One search over the text per stem, not per form: a message that does not
// hold «сервер» is not searched again for «серверами».
func TestTermSetSearchesOncePerStem(t *testing.T) {
	terms := make([]string, 0, 27)
	for _, stem := range []string{"сервер", "кластер", "запрос"} {
		for _, x := range []string{"", "а", "у", "ом", "ами", "ах", "ов", "е", "ы"} {
			terms = append(terms, stem+x)
		}
	}
	set := newTermSet(terms)
	found := make([]bool, len(terms))
	_, probes := set.match(lower("ничего общего с вопросом"), found, nil)
	if probes != 3 {
		t.Errorf("%d terms over a message holding none of them took %d searches, want 3", len(terms), probes)
	}
}

func PrintSession(w io.Writer, s model.Session) {
	PrintSessionStyled(w, s, false, 0)
}

func scoreBM25(documents []bm25Document, df []int, corpusDocuments int, avgLength float64, queryTokenCount int, worn map[string]int) []Hit {
	return scoreBM25At(documents, df, corpusDocuments, avgLength, queryTokenCount, worn, (Options{}).Clock())
}

func autoRecallSession(s model.Session, now time.Time, provenance bool) string {
	return autoRecallSessionFor(s, now, provenance, nil)
}

func autoRecallSessionFor(s model.Session, now time.Time, provenance bool, terms []string) string {
	return autoRecallSessionForAsked(s, now, provenance, terms, "")
}

var lineNumberRE = regexp.MustCompile(`^\s*\d{1,5}[:|]\s+`)

// AutoRecallDigest renders the session-start block within budget.
func AutoRecallDigest(ss []model.Session, budget int) string {
	return AutoRecallDigestFor(ss, budget, nil)
}

// IsStopWord is query.IsStopWord.
func IsStopWord(term string) bool { return query.IsStopWord(term) }

// MatchesQuery is query.MatchesQuery.
func MatchesQuery(text, q string) bool {
	terms, phrases := query.QueryParts(q)
	return query.MatchesParts(text, terms, phrases, nil)
}

// MatchesParts is query.MatchesParts.
func MatchesParts(text string, terms, phrases []string, variants map[string][]string) bool {
	return query.MatchesParts(text, terms, phrases, variants)
}

// A session that never concludes anything gets no pairing, and two quotes then
// stop before the line that names what the reader needs.
func TestSessionWithoutAConclusionGetsThreeQuotes(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "three", Project: "proj",
		Messages: []model.Message{
			{Role: "user", Text: "что там с pgbouncer"},
			{Role: "assistant", Text: "pgbouncer поднят на стейдже"},
			{Role: "assistant", Text: "pgbouncer смотрю логи"},
			{Role: "assistant", Text: "pgbouncer слушает порт 6432"},
			{Role: "assistant", Text: "pgbouncer и это всё на сегодня"},
		},
	}
	got := AutoRecallDigestForAsked([]model.Session{s}, 4096,
		[]string{"pgbouncer"}, "что там с pgbouncer")
	if !strings.Contains(got, "порт 6432") {
		t.Errorf("the third quote was dropped, and with it the only specific line:\n%s", got)
	}
	// And it stops at three: every further quote costs room a second session
	// would have taken.
	if strings.Contains(got, "это всё на сегодня") {
		t.Errorf("a fourth quote made it into the block:\n%s", got)
	}
}

// Proximity is the signal that the query's words belong to one thought.
// Measuring between the FIRST place each word appears is close to the opposite
// of measuring that: a message that says "connection" in its opening line and
// then discusses "connection pool exhausted" as a phrase four paragraphs down
// was scored on the four paragraphs.
func TestTheWindowIsTheTightestOneNotTheFirstOne(t *testing.T) {
	text := strings.ToLower(
		"connection issues have been on my mind all week. " +
			strings.Repeat("unrelated discussion of scheduling and travel plans. ", 20) +
			"the connection pool exhausted under load")
	got := tokenWindow(text, []string{"connection", "pool", "exhausted"})
	// "connection pool exhausted" is 25 characters; anything near that is the
	// phrase, anything near a thousand is the whole message.
	if got > 40 {
		t.Errorf("window is %d characters: measured across the message rather than across the phrase", got)
	}
}

// A token that is genuinely absent still means no window at all, and one token
// has no window to speak of.
func TestAMissingTokenHasNoWindow(t *testing.T) {
	if got := tokenWindow("the connection pool exhausted", []string{"connection", "kafka"}); got != 0 {
		t.Errorf("a query token absent from the text produced a window of %d", got)
	}
	if got := tokenWindow("the connection pool exhausted", []string{"connection"}); got != 0 {
		t.Errorf("a single token produced a window of %d", got)
	}
}

// The tightest cluster can be anywhere, including behind a long run of one of
// the words — the case the scan has to survive.
func TestTheCapDoesNotHideTheTightestCluster(t *testing.T) {
	text := strings.ToLower(
		strings.Repeat("pool ", 64) + "connection pool exhausted")
	got := tokenWindow(text, []string{"connection", "pool", "exhausted"})
	if got > 40 {
		t.Errorf("window is %d characters: the cap stopped before the cluster", got)
	}
}

// An agent run from inside a session has its stdout captured into that
// session's transcript, so the log of the queries it sent lands in an assistant
// message. A later question then matches the record of that question being
// asked — and it outranks the answer, because it repeats every word of the
// query and the answer repeats one (#2067).
func TestTheLogOfAQueryDoesNotAnswerIt(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "какой вариант описания репозитория выбрали"},
		{Role: "assistant", Text: `claude  goprojects/deja-vu  > builder ⚙ deja_recall {"query":"вариант описания репозитория выбрали"}`},
		{Role: "assistant", Text: "в итоге выбрали второй вариант описания — про восстановление контекста"},
	}}
	_, lines := matchedLinesAsked(s, []string{"описания", "репозитория", "вариант"},
		"какой вариант описания репозитория выбрали")
	if len(lines) == 0 {
		t.Fatal("nothing was quoted at all")
	}
	if quoted(lines, "deja_recall") {
		t.Errorf("the record of the question was quoted as its answer: %q", lines)
	}
	if !quoted(lines, "второй вариант") {
		t.Errorf("the line that answered it was not quoted: %q", lines)
	}
}

// An agent run from inside a session writes its stdout into that session's
// transcript, tool-call lines included, and those lines carry the queries it
// sent. Asked which wording option had been chosen, recall led with the log of
// that very question being asked and the agent answered with an invented
// phrase (#2067).
func TestTheLogOfACallDoesNotAnswerTheQuestionItAsked(t *testing.T) {
	now := time.Now()
	// A real working session that happens to contain the log.
	logged := model.Session{
		ID: "logged", Harness: "claude", Project: "deja-vu",
		Path: "/Users/x/.claude/projects/deja-vu/logged.jsonl", Started: now, Updated: now,
		Messages: []model.Message{
			{Role: "assistant", Text: "> builder · gpt-5.6-luna\n" +
				`⚙ deja_recall {"query":"repository description wording","limit":10}` + "\n" +
				"ran the sweep and moved on", Time: now},
		},
	}
	answered := model.Session{
		ID: "answered", Harness: "claude", Project: "deja-vu",
		Path: "/Users/x/.claude/projects/deja-vu/answered.jsonl", Started: now, Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "which repository description wording did we pick", Time: now},
			{Role: "assistant", Text: "You picked the task-first option, so it now opens with Search your past AI coding sessions.", Time: now},
		},
	}

	hits, err := Run([]model.Session{logged, answered}, Options{Query: "repository description wording", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("nothing matched at all")
	}
	if hits[0].Session.ID != "answered" {
		t.Errorf("the log of the call outranked the answer: %s", hits[0].Session.ID)
	}
	for _, h := range hits {
		for _, sn := range h.Snippets {
			if strings.Contains(sn, "deja_recall {") {
				t.Errorf("a call log was quoted back as a snippet: %q", sn)
			}
		}
	}
}

// The fact that a call happened stays in the transcript — `deja how` and
// `deja fix` are built on exactly that — so only matching is affected.
func TestTheCallItselfIsStillInTheTranscript(t *testing.T) {
	line := `⚙ deja_how {"what":"go test"}`
	s := model.Session{
		ID: "kept", Harness: "claude", Project: "p",
		Messages: []model.Message{{Role: "assistant", Text: line}},
	}
	if got := s.Messages[0].Text; got != line {
		t.Fatalf("the transcript was rewritten: %q", got)
	}
	if withoutOwnCallLog(line) != "" {
		t.Errorf("the line was not removed from matching: %q", withoutOwnCallLog(line))
	}
}

// Prose about the tools is ordinary text and has to stay searchable, or a
// session explaining how recall works becomes unfindable.
func TestProseAboutTheToolsIsKept(t *testing.T) {
	for _, text := range []string{
		"recall_context returns a digest of the best matching session",
		"we should call deja_recall before debugging anything",
		"deja_fix takes the failing output verbatim",
	} {
		if got := withoutOwnCallLog(text); got != text {
			t.Errorf("prose was treated as a call log: %q -> %q", text, got)
		}
	}
}

// Only the log lines go; the rest of the message is still matched.
func TestOnlyTheCallLinesAreRemoved(t *testing.T) {
	text := "looked at the release\n" +
		`⚙ deja_recall {"query":"release discussion permission"}` + "\n" +
		`⚙ mcp__deja__deja {"mode":"recall","q":"release discussion permission"}` + "\n" +
		`⚙ deja_session {"id":"ses_123"}` + "\n" +
		"the missing permission was discussions: write"
	got := withoutOwnCallLog(text)
	for _, call := range []string{"deja_recall {", "mcp__deja__deja {", "deja_session {"} {
		if strings.Contains(got, call) {
			t.Errorf("the call line survived (%s): %q", call, got)
		}
	}
	for _, want := range []string{"looked at the release", "discussions: write"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was removed with it", want)
		}
	}
}

// Estimating a term's worth from the answer set has one blind spot, and it is
// not a corner: the answer set was selected for the rare term, so inside it the
// rare term is everywhere. "How many bikes do I own" returns sessions about
// bikes — every one of them holds `bikes` and one of them happens to hold
// `many`, so by the set's own arithmetic `many` is the rarer word and the
// excerpt centres on it.
//
// The ranking already knows what the words are worth against the whole corpus.
// Handing that down is what this checks.
func TestTheRankingsOwnWeightsBeatEstimatingThemFromTheAnswerSet(t *testing.T) {
	ss := make([]model.Session, 0, 5)
	for i := range 4 {
		ss = append(ss, model.Session{
			ID: string(rune('a' + i)), Harness: "claude", Project: "p",
			Messages: []model.Message{{Role: "user", Text: "a review of touring bikes for long rides"}},
		})
	}
	ss = append(ss, model.Session{
		ID: "answer", Harness: "claude", Project: "p",
		Messages: []model.Message{
			{Role: "user", Text: "how many of these are there, generally speaking"},
			{Role: "user", Text: "I keep three bikes in the garage"},
		},
	})
	terms := []string{"many", "bikes"}

	// What the corpus says: bikes is rare in nineteen hundred sessions, many is
	// not. Within these five, the arithmetic is the other way round.
	corpus := map[string]float64{"many": 0.4, "bikes": 4.5}

	estimated := snippetFor(t, RelevanceHits(ss, terms), "answer")
	exact := snippetFor(t, RelevanceHitsWeighted(ss, terms, corpus), "answer")

	if strings.Contains(strings.ToLower(estimated), "bikes") {
		t.Skip("the estimate happened to agree here; this test is about the case where it cannot")
	}
	if !strings.Contains(strings.ToLower(exact), "bikes") {
		t.Errorf("with the ranking's own weights the excerpt is still the filler message:\n  %s", exact)
	}
}

func snippetFor(t *testing.T, hits []Hit, id string) string {
	t.Helper()
	for _, h := range hits {
		if h.Session.ID == id {
			if len(h.Snippets) == 0 {
				t.Fatalf("no snippet for %q", id)
			}
			return h.Snippets[0]
		}
	}
	t.Fatalf("%q is not in the hits", id)
	return ""
}

func widthHit(project, snippet string) Hit {
	return Hit{
		Session: model.Session{
			Harness: "claude", Project: project, ID: "abc123def456",
			Updated: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
		},
		Count:    3,
		Snippets: []string{snippet},
	}
}

// A 60-column pane is what a split editor gives you, and there every hit
// wrapped mid-word — on the first screen a new user sees (#604).
func TestHitsFitTheTerminal(t *testing.T) {
	long := "we decided the retry queue should back off with full jitter rather than a fixed delay, because the fixed one synchronised every worker"
	for _, width := range []int{40, 60, 80, 100} {
		var buf bytes.Buffer
		Print(&buf, []Hit{widthHit("app", long)}, query.Options{Width: width})
		for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
			if strings.HasPrefix(line, "  ") && len([]rune(line)) > width {
				t.Errorf("width %d: a snippet ran to %d runes: %q", width, len([]rune(line)), line)
			}
		}
	}
}

// A pipe gets the whole line: a script reading deja wants the text, and cutting
// it there would lose data rather than layout.
func TestAPipeIsNotTruncated(t *testing.T) {
	long := strings.Repeat("alpha beta ", 30)
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("app", long)}, query.Options{})
	if !strings.Contains(buf.String(), strings.TrimSpace(long)) {
		t.Errorf("piped output was cut:\n%s", buf.String())
	}
}

// The project column gives way first: cutting the line at its end takes the
// match count with it, and that is what a reader scans.
func TestTheProjectGivesWayBeforeTheCount(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("a-very-long-project-directory-name", "short")}, query.Options{Width: 60})
	line, _, _ := strings.Cut(buf.String(), "\n")
	if !strings.Contains(line, "3 matches") {
		t.Errorf("the count was cut off the header: %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("the project was not shortened: %q", line)
	}
	if !strings.Contains(line, "abc123") {
		t.Errorf("the id a reader copies is gone: %q", line)
	}
}

// And a wide terminal keeps everything.
func TestAWideTerminalCutsNothing(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("app", "the retry queue stalls")}, query.Options{Width: 200})
	if strings.Contains(buf.String(), "…") {
		t.Errorf("a wide terminal still elided:\n%s", buf.String())
	}
}

// A CJK line is one rune per two columns, so counting runes cut it to twice the
// terminal's width — worse than the wrap this exists to prevent, because the
// text is gone rather than reflowed.
func TestAWideScriptLineFitsToo(t *testing.T) {
	cjk := strings.Repeat("调度器的重试队列在预发环境卡住", 6)
	var buf bytes.Buffer
	Print(&buf, []Hit{widthHit("app", cjk)}, query.Options{Width: 60})
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		if got := termwidth.Columns(line); got > 60 {
			t.Errorf("a CJK snippet printed %d columns wide: %q", got, line)
		}
	}
}

// And the measure itself, since every budget above depends on it.
func TestColumnsCountsWideRunes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"调度", 4},
		{"a调b", 4},
		{"", 0},
	} {
		if got := termwidth.Columns(tc.in); got != tc.want {
			t.Errorf("termwidth.Columns(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestWorkRecordInvariantAndWroteExclusion(t *testing.T) {
	for _, role := range []string{"tool-output", "files", "command", "edit", "summary", "wrote"} {
		if !IsWorkRecord(role) {
			t.Errorf("IsWorkRecord(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"user", "assistant", ""} {
		if IsWorkRecord(role) {
			t.Errorf("IsWorkRecord(%q) = true, want false", role)
		}
	}

	t0 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	sess := model.Session{
		ID:      "claude:wrote-inv",
		Harness: "claude",
		Project: "deja-vu",
		Title:   "fix retry handler",
		Started: t0,
		Updated: t0.Add(2 * time.Minute),
		Messages: []model.Message{
			{Role: "user", Text: "update internal/retry/handler.go", Time: t0},
			{Role: "wrote", Text: "internal/retry/handler.go\n0123456789abcdef\nfedcba9876543210", Time: t0.Add(time.Minute)},
			{Role: "assistant", Text: "Updated the retry handler backoff.", Time: t0.Add(2 * time.Minute)},
		},
	}

	// 1. PrintContext must never treat "wrote" line-hash records as conversational speech,
	// even when queried for a term inside the "wrote" record or in overview mode.
	for _, q := range []string{"", "0123456789abcdef", "handler.go"} {
		var buf bytes.Buffer
		PrintContext(&buf, sess, q)
		out := buf.String()
		if strings.Contains(out, "0123456789abcdef") || strings.Contains(out, "## wrote") {
			t.Errorf("PrintContext(%q) leaked raw wrote blame hashes:\n%s", q, out)
		}
	}

	// 2. Direct search.Run (--no-index path) must never match raw "wrote" blame hashes.
	hits, err := Run([]model.Session{sess}, Options{Query: "0123456789abcdef"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("Run matched raw wrote hash: got %d hits", len(hits))
	}
}

// The worn count is read from a log written with encoding/json, which replaces
// every byte that is not valid UTF-8 with U+FFFD. The id in the index keeps its
// bytes, so a session whose id holds such a byte was never recognised as one
// deja had already served (#2199).
func TestAWornCountReachesASessionWhoseIDIsNotValidUTF8(t *testing.T) {
	id := "deja-2026-08-27-app-" + string([]byte{0xff, 0xfe})
	// The key the log holds, produced the way the log produces it, rather than
	// asserted by hand.
	raw, err := json.Marshal([]string{id})
	if err != nil {
		t.Fatal(err)
	}
	var back []string
	if err = json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	logged := back[0]
	if logged == id {
		t.Fatalf("the id survived the round trip unchanged, so this measures nothing: %q", logged)
	}

	cold := rankSession("cold", "", "jwt refresh rotation fix applied")
	worn := rankSession(id, "", "jwt refresh rotation fix applied")
	hits, err := Run([]model.Session{cold, worn}, Options{
		Query: "jwt refresh rotation", All: true, RecallWorn: map[string]int{logged: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got Hit
	for _, h := range hits {
		if h.Session.ID == id {
			got = h
		}
	}
	if got.Session.ID != id {
		t.Fatalf("the session is not in the results at all: %+v", hits)
	}
	if got.Reused != 4 {
		t.Errorf("a session deja served four times is reported reused %d times", got.Reused)
	}
	if hits[0].Session.ID != id {
		t.Errorf("the worn session did not win the tie: %v", []string{hits[0].Session.ID, hits[1].Session.ID})
	}
}

// A "wrote" record is a path and line hashes; show printed the hashes bare,
// which read as an unlabelled commit sha.
func TestShowLabelsTheWrittenLineHashes(t *testing.T) {
	var b bytes.Buffer
	PrintSession(&b, model.Session{Harness: "claude", Project: "payments", ID: "bg0000", Messages: []model.Message{
		{Role: "wrote", Text: "/work/payments/internal/pool/pool.go\n77eb9cb669a7491c 1a2b3c4d5e6f7081"},
	}})
	got := b.String()
	if strings.Contains(got, "77eb9cb669a7491c") {
		t.Errorf("the hashes are printed bare:\n%s", got)
	}
	if !strings.Contains(got, "/work/payments/internal/pool/pool.go\n(2 written lines, kept as hashes for `deja blame`)") {
		t.Errorf("the record is not labelled:\n%s", got)
	}
}
