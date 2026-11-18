package prompt

import (
	"slices"
	"testing"
)

func TestTermsKeepsShortSubjectWG(t *testing.T) {
	got := Terms("что с wg 10.10.0.10 и кластером")
	if !slices.Contains(got, "wg") {
		t.Fatalf("expected Terms to retain short technical subject 'wg', got %v", got)
	}
}

func TestTermsFiltersTemporalFillerAndRetainsAnchoredProperNouns(t *testing.T) {
	got := Terms("How many days passed between the service at the Episcopal Church and the evening lecture?")
	for _, bad := range []string{"many", "ago"} {
		if slices.Contains(got, bad) {
			t.Fatalf("unexpected filler term %q in %v", bad, got)
		}
	}
	for _, want := range []string{"episcopal", "church"} {
		if !slices.Contains(got, want) {
			t.Fatalf("expected anchored proper noun %q in %v", want, got)
		}
	}
	ago := Terms("How long ago did the culinary baking class happen?")
	if slices.Contains(ago, "ago") {
		t.Fatalf("expected short temporal word 'ago' filtered, got %v", ago)
	}
}

func TestTermsSingleLineAnchoredAndClauseBalanced(t *testing.T) {
	multiEntity := Terms("How many weeks did it take me to start watching the AlphaSeries marathon and finish the BetaUniverse movies?")
	for _, want := range []string{"alphaseries", "betauniverse", "movies"} {
		if !slices.Contains(multiEntity, want) {
			t.Fatalf("expected %q retained in single-line multi-entity query, got %v", want, multiEntity)
		}
	}

	whenBalanced := Terms("How many days ago did I attend the ceramic workshop at the local pottery studio when I glazed a stoneware bowl for my cousin?")
	leftHits := 0
	for _, w := range []string{"ceramic", "workshop", "pottery", "studio"} {
		if slices.Contains(whenBalanced, w) {
			leftHits++
		}
	}
	rightHits := 0
	for _, w := range []string{"glazed", "stoneware", "bowl", "cousin"} {
		if slices.Contains(whenBalanced, w) {
			rightHits++
		}
	}
	if leftHits < 2 || rightHits < 2 {
		t.Fatalf("expected >=2 left and >=2 right content terms for single-line when-query, got %v", whenBalanced)
	}

	short := Terms("how many weeks ago")
	if !slices.Contains(short, "weeks") {
		t.Fatalf("expected short prompt to keep framing term 'weeks', got %v", short)
	}
}

func TestTermsSetupSentenceBeforeGenericFollowupAsk(t *testing.T) {
	slowCooker := Terms("I've been struggling with my slow cooker recipes. Any advice on getting better results?")
	if slices.Contains(slowCooker, "been") {
		t.Fatalf("expected filler 'been' filtered, got %v", slowCooker)
	}
	for _, want := range []string{"slow", "cooker", "recipes"} {
		if !slices.Contains(slowCooker, want) {
			t.Fatalf("expected %q retained from setup sentence, got %v", want, slowCooker)
		}
	}

	colleagues := Terms("I've been thinking about ways to stay connected with my colleagues. Any suggestions?")
	for _, want := range []string{"colleagues", "connected"} {
		if !slices.Contains(colleagues, want) {
			t.Fatalf("expected %q retained before generic followup ask, got %v", want, colleagues)
		}
	}

	kitchen := Terms("My kitchen's becoming a bit of a mess again. Any tips for keeping it clean?")
	for _, want := range []string{"kitchen", "mess", "clean"} {
		if !slices.Contains(kitchen, want) {
			t.Fatalf("expected %q retained from setup+ask prompt, got %v", want, kitchen)
		}
	}
}

func TestPromptEntityAndLinguisticPrimitivesENAndRU(t *testing.T) {
	if !CarriesMidSentenceEntity("My friend Miriam moved to Denver.") {
		t.Fatal("expected mid-sentence Latin entity detected")
	}
	if !CarriesMidSentenceEntity("Вчера мы перенесли шлюз в Москву.") {
		t.Fatal("expected mid-sentence Cyrillic entity detected")
	}
	for _, plain := range []string{
		"",
		"Denver is cold in winter. Tuesday was sunny.",
		"I've scheduled the deploy for Friday in January.",
		"Понедельник начинается в январе.",
		"deployed API v2 ... nothing failed",
	} {
		if CarriesMidSentenceEntity(plain) {
			t.Fatalf("expected sentence-initial, acronym, or calendar word ignored, got true for %q", plain)
		}
	}

	if !MidSentenceEntityTerm("We visited Rachel's studio.", "rachel") {
		t.Fatal("expected possessive mid-sentence entity term match")
	}
	if !MidSentenceEntityTerm("We audited API v2 after visiting Seattle.", "Seattle") {
		t.Fatal("expected Seattle matched after acronym and digit token")
	}
	if MidSentenceEntityTerm("We visited Seattle.", "Chicago") {
		t.Fatal("did not expect non-matching entity term")
	}
	if MidSentenceEntityTerm("In Seattle.", "...") {
		t.Fatal("expected punctuation-only term rejected")
	}
	if MidSentenceEntityTerm("Rachel moved here. We visited later.", "rachel") {
		t.Fatal("expected sentence-start capitalized word not counted as mid-sentence entity")
	}
	if MidSentenceEntityTerm("", "rachel") {
		t.Fatal("expected empty text to return false")
	}

	if !HasBetweenCue("Сколько дней прошло между релизом и откатом?") {
		t.Fatal("expected Russian between cue detected")
	}
	if HasBetweenCue("What changed yesterday?") {
		t.Fatal("did not expect between cue")
	}

	if l, r, isTemporal, ok := PairedClauseSplit("Сколько прошло времени когда включили кеш?"); !ok || !isTemporal || l == "" || r == "" {
		t.Fatalf("expected Russian temporal clause split, got (%q, %q, %v, %v)", l, r, isTemporal, ok)
	}
	if _, _, isTemporal, ok := PairedClauseSplit("Между миграцией схемы и перезапуском сервиса"); !ok || isTemporal {
		t.Fatalf("expected Russian coordination clause split, got (%v, %v)", isTemporal, ok)
	}
	// Multi-byte Turkish İ tests length-mismatch path safely without panic.
	if _, _, _, ok := PairedClauseSplit("İstanbul when Ankara"); !ok {
		t.Fatal("expected safe clause split when lowercase length differs from source")
	}
	if _, _, _, ok := PairedClauseSplit("No clause marker here"); ok {
		t.Fatal("expected false when no paired clause separator present")
	}

	if !IsDisjunctiveQuery("Did we deploy sentinel_a or sentinel_b?") {
		t.Fatal("expected English disjunctive query detected")
	}
	if !IsDisjunctiveQuery("Мы выбрали кеш или очередь?") {
		t.Fatal("expected Russian disjunctive query detected")
	}
	if IsDisjunctiveQuery("Did this happen before or after the release?") || IsDisjunctiveQuery("Это было до или после релиза?") {
		t.Fatal("expected temporal 'before or after' / 'до или после' excluded from disjunctive check")
	}

	if !IsOpenAdviceQuery("Any advice on tuning connection pools?", []string{"advice", "tuning", "connection", "pools"}) {
		t.Fatal("expected open English advice query detected")
	}
	if !IsOpenAdviceQuery("Дай совет по настройке пула?", []string{"совет", "настройке", "пула"}) {
		t.Fatal("expected open Russian advice query detected")
	}
	if IsOpenAdviceQuery("How do I tune worker_pool timeout?", []string{"tune", "worker_pool", "timeout"}) {
		t.Fatal("expected non-advice query to return false")
	}
	if IsOpenAdviceQuery("I am here again, what advice did you suggest earlier?", []string{"again", "advice", "suggest", "earlier"}) {
		t.Fatal("expected historical-cue query excluded from open advice")
	}
	if IsOpenAdviceQuery("Какие советы мы уже обсуждали раньше?", []string{"советы", "обсуждали", "раньше"}) {
		t.Fatal("expected Russian historical-cue query excluded from open advice")
	}
	// Anchored followup question or empty left clause exercises askAnchoredTerms & right-clause guards.
	if got := Terms("Setup worker config. Any advice for foo_bar/baz?"); !slices.Contains(got, "foo_bar/baz") {
		t.Fatalf("expected anchored followup term retained, got %v", got)
	}
	if got := Terms("When did the latency spike start in checkout or billing?"); !slices.Contains(got, "latency") {
		t.Fatalf("expected clause fallback handled cleanly, got %v", got)
	}

	for _, w := range []string{"week", "неделю", "much", "много", "acquire", "acquired", "purchase", "bought", "купил", "приобрел", "приобрели", "fixed", "fixes", "fixing", "исправили", "починили"} {
		if !IsFramingModifier(w) {
			t.Fatalf("expected framing modifier %q detected", w)
		}
	}
	wantAsked := []string{"marmotquill", "exporter", "drops", "rows", "past", "4096"}
	if got := Terms("the marmotquill exporter drops rows past 4096 again, what fixed the marmotquill exporter?"); !slices.Equal(got, wantAsked) {
		t.Fatalf("expected Terms to preserve anchored tokens over fixed, got %v want %v", got, wantAsked)
	}
	if IsFramingModifier("pgbouncer") || IsFramingModifier("маршрутизатор") {
		t.Fatal("did not expect domain nouns flagged as framing modifiers")
	}
}
