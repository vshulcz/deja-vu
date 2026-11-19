package search

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
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
