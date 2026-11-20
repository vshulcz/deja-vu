package index

import (
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A Russian corpus exercises the widest fold: every inflected form of a term
// shares its opening runes, so they all land in one bucket.
func benchRussianIndex(b *testing.B) string {
	b.Helper()
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	b.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	stems := []string{"миграц", "репликац", "конфигурац", "авторизац", "оптимизац", "документац", "интеграц", "валидац", "сериализац", "агрегац"}
	ends := []string{"ия", "ии", "ию", "ией", "иями", "иях"}
	other := []string{"сервер", "клиент", "запрос", "ответ", "ошибка", "таблица", "индекс", "кластер", "очередь", "кеш"}
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // a seeded fixture, not crypto
	for i := range 3000 {
		text := ""
		var textSb29 strings.Builder
		for range 40 {
			if rng.Intn(3) == 0 {
				textSb29.WriteString(stems[rng.Intn(len(stems))] + ends[rng.Intn(len(ends))] + " ")
			} else {
				textSb29.WriteString(other[rng.Intn(len(other))] + " ")
			}
		}
		text += textSb29.String()
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"%s"}}`, i, text) + "\n" //nolint:gocritic // JSON quoting, not Go's
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i)), []byte(line), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		b.Fatal(err)
	}
	return dir
}

func BenchmarkRussianRelevance(b *testing.B) {
	dir := benchRussianIndex(b)
	terms := []string{"миграция", "репликация", "конфигурация", "оптимизация"}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, _, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, terms, 10); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRelevanceWithWorkRecords measures ProjectRelevant on a corpus whose
// sessions hold the work and compaction records (files, command, edit, wrote,
// summary) that real coding sessions carry alongside speech.
func BenchmarkRelevanceWithWorkRecords(b *testing.B) {
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	b.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	topics := []string{"pgbouncer", "keepalive", "backpressure", "walreceiver", "checkpoint", "autovacuum", "logicalslot", "deadlock"}
	filler := strings.Repeat("func handleConn(ctx context.Context, pool *ConnPool) error { return pool.Acquire(ctx) }\n", 24)
	rng := rand.New(rand.NewSource(11)) //nolint:gosec // seeded benchmark fixture
	for i := range 400 {
		t1 := topics[rng.Intn(len(topics))]
		t2 := topics[rng.Intn(len(topics))]
		var sb strings.Builder
		fmt.Fprintf(&sb, `{"type":"user","sessionId":"w%d","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"investigate %s and %s latency spike under load"}}`+"\n", i, t1, t2)
		fmt.Fprintf(&sb, `{"type":"assistant","sessionId":"w%d","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Checking %s pool saturation and %s timeout settings."},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go test -v ./internal/%s/... -run Test%s"}},{"type":"tool_use","id":"tu2","name":"Edit","input":{"file_path":"/tmp/app/%s.go","old_string":%q,"new_string":%q}},{"type":"tool_use","id":"tu3","name":"Write","input":{"file_path":"/tmp/app/%s_test.go","content":%q}}]}}`+"\n",
			i, t1, t2, t1, t2, t1, filler+"// "+t1, filler+"// "+t2, t1, filler)
		fmt.Fprintf(&sb, `{"type":"summary","sessionId":"w%d","timestamp":"2026-01-02T03:04:07Z","summary":"Session tuned %s and %s configuration parameters: %s"}`+"\n", i, t1, t2, filler)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("w%d.jsonl", i)), []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		b.Fatal(err)
	}
	terms := []string{"pgbouncer", "keepalive", "backpressure", "latency"}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, _, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, terms, 10); err != nil {
			b.Fatal(err)
		}
	}
}

// A corrupt or unreadable postings bucket on the relevance path used to be
// swallowed as "the term is absent", silently short-ranking the answer. It must
// surface as a corruption error so the recovery path rebuilds — the same
// self-heal the exact tier already triggers.
func TestRelevanceSurfacesACorruptBucket(t *testing.T) {
	dir := t.TempDir()
	ss := []model.Session{
		{Harness: "claude", ID: "a", Project: "p", Messages: []model.Message{
			{Role: "user", Text: "how do we handle jwt refresh rotation reconciler"},
		}},
		{Harness: "claude", ID: "b", Project: "p", Messages: []model.Message{
			{Role: "user", Text: "the reconciler webhook retry cap needs raising"},
		}},
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, ss, nil, ""); err != nil {
		t.Fatal(err)
	}
	// Corrupt every bucket so any postings read fails with errCorruptIndex.
	entries, err := os.ReadDir(filepath.Join(dir, "buckets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no buckets to corrupt")
	}
	for _, e := range entries {
		if err = os.WriteFile(filepath.Join(dir, "buckets", e.Name()), []byte("XXXXXXXX garbage"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A natural-language query that reaches the relevance tier.
	_, rerr := relevanceSearch(dir, m, query.Options{Query: "how do we handle the reconciler retry"})
	if rerr == nil {
		t.Fatal("a corrupt bucket was swallowed as an empty relevance result")
	}
	if !errors.Is(rerr, errCorruptIndex) {
		t.Errorf("relevance returned %v, want a corruption error the recovery path heals", rerr)
	}
}

// rolesIndex lays down one session whose speech and whose work records say
// different things, so a result can be traced to which of the two carried it.
func rolesIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := []model.Session{{
		ID: "a", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{
			{Role: "user", Text: "the deploy pipeline kept timing out on the staging cluster"},
			{Role: "assistant", Text: "raised the readiness probe budget and it settled"},
			{Role: roleCommand, Text: "kubectl rollout restart deployment/frobnicator"},
			{Role: roleFiles, Text: "charts/frobnicator/values.yaml"},
		},
	}}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The relevance tier never scans records, so it served every record of every
// session it ranked. Exact search hides file lists and commands on purpose — a
// path is a note of what a turn touched, not something said — and the two paths
// disagreed about the same session.
func TestRelevanceDoesNotServeWorkRecords(t *testing.T) {
	dir := rolesIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{Query: "frobnicator values rollout", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Sessions {
		for _, msg := range s.Messages {
			if msg.Role == roleCommand || msg.Role == roleFiles {
				t.Errorf("relevance served a %s record: %q", msg.Role, msg.Text)
			}
		}
	}
}

// And with an explicit role, the other side of the conversation is not an
// answer to it.
func TestRelevanceHonoursTheRequestedRole(t *testing.T) {
	dir := rolesIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := relevanceSearch(dir, m, query.Options{
		Query: "readiness probe budget settled", Role: "user", All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Sessions {
		for _, msg := range s.Messages {
			if msg.Role != "user" {
				t.Errorf("--role=user returned a %s message: %q", msg.Role, msg.Text)
			}
		}
	}
}

// The same rule reaches the auto-recall path, which loads sessions the same way
// and had the same hole: a hook that pastes a file list into the model's
// context is pasting the wrong thing.
func TestProjectRelevantDoesNotServeWorkRecords(t *testing.T) {
	dir := rolesIndex(t)
	ss, _, _, _, err := ProjectRelevant(dir, []string{"tmp/p"}, []string{"frobnicator", "values", "rollout"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		for _, msg := range s.Messages {
			if msg.Role == roleCommand || msg.Role == roleFiles {
				t.Errorf("auto-recall served a %s record: %q", msg.Role, msg.Text)
			}
		}
	}
}

// Nothing above should have cost the ordinary answer.
func TestSpeechStillComesBack(t *testing.T) {
	dir := rolesIndex(t)
	result, err := SearchDetailed(dir, search.Options{Query: "deploy pipeline timing out", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("the session that answers this in plain speech did not come back")
	}
}

// seedFiller writes n sessions of unrelated chatter, so the store is bigger
// than the window the relevance tail is drawn from. Below that size the tail
// stays off by design and none of this applies.
func seedFiller(t *testing.T, proj string, n int) {
	t.Helper()
	for i := range n {
		sid := fmt.Sprintf("filler-%d", i)
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"routine standup chatter number ` + strconv.Itoa(i) + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeSession(t *testing.T, proj, sid, text string) {
	t.Helper()
	line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMessages puts one session on disk carrying a message per line, for the
// tests that need many postings rather than many sessions.
func writeMessages(t *testing.T, proj, sid, body string) {
	t.Helper()
	var b strings.Builder
	for line := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		b.WriteString(`{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + line + `"}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedStore(t *testing.T, filler int) string {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// The session that answers the question, worded the way people actually
	// write: it never says "own", so an AND over the question's words cannot
	// reach it. It does carry two of them, which is the bar relevance sets —
	// one lucky word is noise.
	writeSession(t, proj, "answer", "not that many bikes really, both of mine live in the hallway")
	// An incidental session that does satisfy the AND, on a completely
	// different subject. This is what a strict match lands on.
	writeSession(t, proj, "incidental", "many courier bikes own their routes downtown")
	seedFiller(t, proj, filler)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestThinANDGetsRelevanceTailBeneathIt(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) < 2 {
		t.Fatalf("tail did not run: %d session(s), tier %q", len(r.Sessions), r.Tier)
	}
	// The strict match keeps the first position. Relevance ranked alone scores
	// worse there, so the tail must never push it down.
	if r.Sessions[0].ID != "incidental" {
		t.Fatalf("strict hit lost the top spot to %q", r.Sessions[0].ID)
	}
	found := false
	for _, s := range r.Sessions {
		if s.ID == "answer" {
			found = true
		}
	}
	if !found {
		ids := make([]string, 0, len(r.Sessions))
		for _, s := range r.Sessions {
			ids = append(ids, s.ID)
		}
		t.Fatalf("the answer the AND excluded is still unreachable: %v", ids)
	}
	// Order is the contract: RelevanceHits scores by arrival, so the merged
	// list has to be labelled as the tier that preserves it.
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want relevance so the merged order survives", r.Tier)
	}
	// The strict hit is inside the pool the ranking scored, so counting it
	// again would tell the caller there is more behind the window than there
	// is. The total may never be smaller than what was handed back either.
	if r.Total < len(r.Sessions) {
		t.Fatalf("total %d is under the %d sessions returned", r.Total, len(r.Sessions))
	}
	if r.Total > len(r.Sessions) != r.Capped {
		t.Fatalf("capped=%v disagrees with total %d over %d sessions", r.Capped, r.Total, len(r.Sessions))
	}
}

// The label says nothing matched; the answer holds a session that matched
// every word. Which one, and how many, has to survive to the caller or every
// surface above repeats the label's claim (#3815).
func TestTheStrictHeadIsCountedAndNamedUnderTheRelevanceLabel(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want the relabelled answer this is about", r.Tier)
	}
	if r.Strict != 1 {
		t.Fatalf("strict = %d, want the one session that satisfied the AND", r.Strict)
	}
	var strict, ranked int
	for _, s := range r.Sessions {
		switch {
		case s.ID == "incidental" && r.IsStrict(s):
			strict++
		case s.ID == "answer" && !r.IsStrict(s):
			ranked++
		case r.IsStrict(s):
			t.Fatalf("%q was ranked into the answer but is named as a match", s.ID)
		}
	}
	if strict != 1 {
		t.Fatal("the session that holds every query word is not named as one")
	}
	if ranked != 1 {
		t.Fatal("the session the tail brought in is missing, or is named a match")
	}
}

// A quoted phrase that matches nothing is retried without its quotes, and the
// retry is published under the relevance label so the loosening is visible.
// The words can all be there — only the phrase was not — and then the label
// says nothing matched about an answer that did (#3815).
func TestAQuotedQueryRetriedWithoutItsQuotesSaysWhatMatched(t *testing.T) {
	dir := seedStore(t, 3)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: `courier bikes "downtown routes"`, All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want the relabelled retry this is about", r.Tier)
	}
	if len(r.Sessions) == 0 {
		t.Fatal("the retry found nothing, so there is nothing to label")
	}
	if r.Strict != len(r.Sessions) {
		t.Fatalf("strict = %d over %d sessions, want every one of them", r.Strict, len(r.Sessions))
	}
	for _, s := range r.Sessions {
		if !r.IsStrict(s) {
			t.Fatalf("%q matched the loosened query but is not named as a match", s.ID)
		}
	}
}

func TestWordFormFallbackKeepsItsAnnotationUnderTheTail(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	// "biked" has no postings of its own; the ladder reaches the corpus
	// through the stem fold, and that fallback is worth telling the user
	// about even once relevance owns the order.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many biked hallway", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) == 0 {
		t.Skip("no word-form fallback for this corpus")
	}
	if r.Stemmed && len(r.Variants) == 0 {
		t.Fatal("the stemmed notice survived but the word forms behind it did not")
	}
}

func TestSmallStoreKeepsItsStrictAnswerAlone(t *testing.T) {
	dir := seedStore(t, 3)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Few results out of few sessions is an answer, not a symptom. Ranking a
	// store smaller than the window returns most of it, which would attach the
	// rest of the history to a precise query.
	for _, s := range r.Sessions {
		if s.ID != "incidental" {
			t.Fatalf("small store got a tail: %q came back too", s.ID)
		}
	}
}

func TestWideANDIsLeftAlone(t *testing.T) {
	dir := seedStore(t, relevanceWindow+10)

	// A query the filler sessions all satisfy: the intersection is describing
	// a real cluster, so nothing should be appended to it.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "routine standup chatter", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) < thinAND {
		t.Fatalf("expected a wide AND, got %d sessions", len(r.Sessions))
	}
	// Strict counts the head of a relabelled answer. This answer kept its own
	// tier, so there is nothing for it to disambiguate and it stays zero.
	if r.Strict != 0 || r.StrictIDs != nil {
		t.Fatalf("an answer on tier %q reported %d strict sessions", r.Tier, r.Strict)
	}
	for _, s := range r.Sessions {
		if s.ID == "answer" {
			t.Fatal("a wide AND was widened further by the tail")
		}
	}
}

// A store of fifty sessions is the size a LongMemEval haystack has, and the
// size a real store has for the first weeks after an install. It used to get no
// tail at all: the strict answer came back alone, and the session that actually
// answered the question was never returned. That cost 1.8pp of hit@5 on
// LongMemEval-S and 5.2pp on LoCoMo.
func TestAStoreTheSizeOfAHaystackGetsATail(t *testing.T) {
	dir := seedStore(t, relevanceWindow-5)

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Sessions[0].ID != "incidental" {
		t.Fatalf("strict hit lost the top spot to %q", r.Sessions[0].ID)
	}
	found := false
	for _, s := range r.Sessions {
		if s.ID == "answer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the session that answers the question never came back: %v", sessionIDs(r.Sessions))
	}
}

// The tail is a handful of places, not the store: a fifth of it, capped. Without
// a cap a precise query on a small store comes back with most of the history
// attached, which is what the old all-or-nothing guard was protecting against.
func TestTheTailOnASmallStoreIsAFifthOfIt(t *testing.T) {
	const (
		unrelated = 35
		rivals    = 12
	)
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSession(t, proj, "incidental", "many courier bikes own their routes downtown")
	// Sessions the ranking will score: each carries two of the question's
	// words, and there are more of them than the cap allows, so the cap is the
	// only thing deciding how much of the pool is served.
	for i := range rivals {
		writeSession(t, proj, fmt.Sprintf("rival-%d", i),
			fmt.Sprintf("the bikes i own live in hallway number %d", i))
	}
	seedFiller(t, proj, unrelated)
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := unrelated + rivals + 1
	want := 1 + min(thinAND, store/5) // the strict head plus its tail
	if len(r.Sessions) > want {
		t.Errorf("tail of %d sessions over a store of %d, want at most %d",
			len(r.Sessions)-1, store, want-1)
	}
	if len(r.Sessions) < 2 {
		t.Errorf("no tail at all over a store of %d", store)
	}
}

// A LoCoMo conversation holds nineteen sessions, and two of them do. Under the
// old floor of twenty those 302 questions got the strict answer alone, so the
// session that answered was never returned at all.
func TestAStoreOfNineteenSessionsGetsATail(t *testing.T) {
	dir := seedStore(t, 17) // plus the two named sessions

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Sessions {
		if s.ID == "answer" {
			return
		}
	}
	t.Fatalf("the session that answers the question never came back: %v", sessionIDs(r.Sessions))
}

// Below the floor the tail stands down: on a handful of sessions the one it
// would add arrives on filler words, not on the subject.
func TestBelowTheFloorThereIsNoTail(t *testing.T) {
	dir := seedStore(t, tailFloor-4) // plus the two named sessions: under the floor

	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "how many bikes do I own", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Sessions {
		if s.ID != "incidental" {
			t.Fatalf("a store under the floor got a tail: %q came back", s.ID)
		}
	}
}

func TestLadderFallsBackToRelevance(t *testing.T) {
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-w-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The relevance tier exists for queries whose indexed words are spread
	// across sessions: no single session holds even a dropped-down AND.
	write("a.jsonl", "graduate", "quetzal figment bartleby snorkel discussed at length")
	write("b.jsonl", "other", "marzipan wombat cufflink review notes")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// A natural question: no session contains every word, so exact/stem/fuzzy
	// all miss — the relevance tier must surface the graduation session.
	r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "quetzal figment bartleby snorkel marzipan wombat cufflink", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tier != query.TierRelevance {
		t.Fatalf("tier = %q, want relevance (sessions=%d)", r.Tier, len(r.Sessions))
	}
	if len(r.Sessions) != 2 {
		t.Fatalf("both partially-matching sessions must surface, got %d", len(r.Sessions))
	}
	if r.Sessions[0].ID != "graduate" { // 4 informative hits outrank 3
		t.Fatalf("relevance order wrong: %s first", r.Sessions[0].ID)
	}
	// Single informative word still prefers silence over noise.
	r2, err := SearchWithRecoveryDetailed(dir, query.Options{Query: "zzznothing", All: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Sessions) != 0 {
		t.Fatalf("nonsense query must stay empty, got %d sessions", len(r2.Sessions))
	}
}

// The relevance tier ranks the whole candidate pool and serves the top
// relevanceWindow of it, so the sessions it returns are the window rather than
// the match. Counting them and calling that the total reported the window's own
// size on every query deeper than it, with capped: false alongside — a missing
// signal sends a consumer to check, a wrong one tells it to stop.
func TestRelevanceReportsThePoolItsWindowTrimmed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates int
		wantTotal  int
		wantServed int
		wantCapped bool
	}{
		// One session per candidate plus the one holding the fourth term,
		// which the ranking scores too: it is the pool, not the answer set.
		{"pool deeper than the window", 60, 61, relevanceWindow, true},
		{"pool inside the window", 10, 11, 11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := relevancePoolFixture(t, tc.candidates)
			const q = "kubernetes autoscaler telemetry dashboards"
			r, err := SearchWithRecoveryDetailed(dir, query.Options{Query: q, All: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Guard the fixture, not the fix: if the ladder stops falling
			// through to relevance the numbers below mean nothing.
			if r.Tier != query.TierRelevance {
				t.Fatalf("fixture resolved through tier %q, want the relevance tier", r.Tier)
			}
			if len(r.Sessions) != tc.wantServed {
				t.Fatalf("served %d sessions, want %d", len(r.Sessions), tc.wantServed)
			}
			if r.Total != tc.wantTotal {
				t.Fatalf("total = %d, want %d — every session the ranking scored, before the window", r.Total, tc.wantTotal)
			}
			if r.Capped != tc.wantCapped {
				t.Fatalf("capped = %v with %d of %d sessions served", r.Capped, len(r.Sessions), r.Total)
			}
			// The invariant the reporting exists for: capped false has to mean
			// the caller is holding everything that matched.
			if !r.Capped && r.Total != len(r.Sessions) {
				t.Fatalf("capped is false while %d of %d matches were withheld", r.Total-len(r.Sessions), r.Total)
			}
		})
	}
}

// relevancePoolFixture indexes n sessions carrying three of the four query
// terms and one carrying only the fourth, so every term resolves in the corpus
// while no session holds them all — which is what drops the ladder to the
// relevance tier — and the scored pool is n+1.
func relevancePoolFixture(t *testing.T, n int) string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, sid, text string) {
		line := `{"type":"user","sessionId":"` + sid + `","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"` + text + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(proj, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range n {
		id := fmt.Sprintf("c%d", i)
		write(id+".jsonl", id, fmt.Sprintf("kubernetes autoscaler telemetry notes %d", i))
	}
	write("d1.jsonl", "d1", "dashboards")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// toolDumpIndex lays down the store from #4780: one session that is megabytes
// of JSON tool output, every dump holding most words of any question, beside
// short sessions where someone says the answer.
func toolDumpIndex(t *testing.T) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	now := time.Now()
	huge := model.Session{ID: "huge", Harness: "opencode", Project: "home", Updated: now,
		Messages: []model.Message{
			{Role: "user", Text: "check what the swarm plugin stored for its agents"},
			{Role: "assistant", Text: "reading the plugin storage now"},
		}}
	words := []string{"pgx", "upgrade", "pr", "reviewer", "owner", "review", "export", "logger",
		"structured", "logging", "command", "payouts", "service", "naming", "setup", "main.go"}
	// Two thousand dumps, one in eight carrying the question's words.
	for i := range 2000 {
		var b strings.Builder
		b.WriteString("[")
		for j := range 40 {
			w := fmt.Sprintf("module%d", j)
			if i%8 == 0 {
				w = words[(i+j)%len(words)]
			}
			fmt.Fprintf(&b, `{"type": "tool", "tool": "read", "callID": "call_%d_%d", "state": {"status": "completed", "input": {"filePath": "/u/src/%s/part%d.go"}, "title": "%s"}},`, i, j, w, j, w)
		}
		b.WriteString("{}]")
		huge.Messages = append(huge.Messages, model.Message{Role: roleToolOutput, Text: b.String()})
	}
	sessions := []model.Session{ //nolint:prealloc // a fixture literal the loop below grows
		huge,
		{ID: "answer", Harness: "claude", Project: "ledgerd", Updated: now.Add(-48 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "who should review the pgx upgrade PR?"},
				{Role: "assistant", Text: "Dana is the owner of pgx, so she is the reviewer for the upgrade."},
			}},
		{ID: "logger", Harness: "claude", Project: "ledgerd", Updated: now.Add(-72 * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: "the export command should use the structured logger"},
				{Role: "assistant", Text: "cmd/export now logs through the structured logging setup."},
			}},
	}
	for i := range 30 {
		sessions = append(sessions, model.Session{ID: fmt.Sprintf("other%d", i), Harness: "claude", Project: "misc",
			Updated: now.Add(-time.Duration(100+i) * time.Hour),
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("tune the cache eviction for shard %d", i)},
				{Role: "assistant", Text: "lowered the ttl and the hit rate held"},
			}})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A match inside tool output is not someone saying it. The session that read
// megabytes of JSON took the top place for questions it never discussed,
// above the two-turn session that answers them (#4780).
func TestAToolOutputDumpDoesNotOutrankTheAnswer(t *testing.T) {
	dir := toolDumpIndex(t)
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]string{
		"pgx upgrade PR reviewer owner review":                "answer",
		"cmd/export structured logging logger export command": "logger",
	} {
		result, err := relevanceSearch(dir, m, query.Options{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) == 0 {
			t.Fatalf("%q: nothing ranked", q)
		}
		if got := result.Sessions[0].ID; got != want {
			t.Errorf("%q: first is %s, want %s", q, got, want)
		}
	}
}

func buildTestClaudeIndex(t *testing.T, docs map[string][]string) (string, Manifest) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, msgs := range docs {
		var sb strings.Builder
		for i, text := range msgs {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			fmt.Fprintf(&sb, `{"type":%q,"sessionId":%q,"timestamp":"2026-01-02T03:%02d:05Z","message":{"role":%q,"content":%q}}`+"\n", role, id, i, role, text)
		}
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

func TestAQuotedQueryWithThinStrictHeadStillAttachesRelevanceTail(t *testing.T) {
	docs := map[string][]string{
		"strict-head": {
			`Need to inspect "wal checkpoint" stall during snapshot flush.`,
			`Reproduced wal checkpoint stall while flushing snapshot buffers.`,
		},
		"tail-one": {
			`How does snapshot flush interact with checkpoint and wal recovery?`,
			`Delayed wal checkpoint flushing stalls the background snapshot writer.`,
		},
		"tail-two": {
			`Snapshot flush stalls whenever the wal checkpoint lock waits on sync.`,
			`Verified checkpoint and wal snapshot locking order fixes the stall.`,
		},
		"multiline-target": {
			`Investigating tokenBucket refill regression when burst capacity underflows`,
			`Fixed tokenBucket refill underflow by clamping negative elapsed deltas.`,
		},
	}
	for i := range 16 {
		docs[fmt.Sprintf("bg-%02d", i)] = []string{
			fmt.Sprintf("Routine maintenance task %d on unrelated module.", i),
			fmt.Sprintf("Completed routine check %d with no errors.", i),
		}
	}
	dir, _ := buildTestClaudeIndex(t, docs)

	// Embedded quotes with a thin strict head (< thinAND = 3) must still attach
	// relevance tail sessions via withRelevanceTail quote loosening.
	thinRes, err := SearchDetailed(dir, query.Options{Query: `snapshot "wal checkpoint" flush`})
	if err != nil {
		t.Fatal(err)
	}
	if len(thinRes.Sessions) < 2 {
		t.Fatalf("expected embedded-quote search with thin strict head to attach relevance tail, got %d session(s)", len(thinRes.Sessions))
	}

	// Unmatched or distant quotes across lines in a pasted issue must not wipe out
	// the issue body via cross-line quotedSpanRE matching.
	issueQ := "tokenBucket \"refill regression\"\nwhen burst capacity underflows\nand negative elapsed deltas wrap\nclosing quote later \"here"
	issueRes, err := SearchDetailed(dir, query.Options{Query: issueQ})
	if err != nil {
		t.Fatal(err)
	}
	if len(issueRes.Sessions) == 0 || issueRes.Sessions[0].ID != "multiline-target" {
		t.Fatalf("expected multiline quoted issue to match multiline-target, got %+v", issueRes.Sessions)
	}

	// Bare single-line quoted phrases without extra context words stay exact/silent.
	bareRes, err := SearchDetailed(dir, query.Options{Query: `"nonexistent exact phrase"`})
	if err != nil {
		t.Fatal(err)
	}
	if len(bareRes.Sessions) != 0 {
		t.Fatalf("expected bare unmatched quoted phrase to stay silent, got %d sessions", len(bareRes.Sessions))
	}
}

func TestDiagonalMatrixCompoundPartsRankAndRescue(t *testing.T) {
	docs := map[string][]string{
		"answer": {
			"Debugging determinant fast path when is_diagonal property holds on MatrixSymbol.",
			"Updated MatrixSymbol is_diagonal dispatch so sparse eigenvalues skip dense fallback.",
		},
		"distractor": {
			"Unrelated failure report in parser initialization path failure logs.",
			"Added generic failure assertion for parser failure recovery.",
		},
	}
	for i := range 18 {
		docs[fmt.Sprintf("noise-%02d", i)] = []string{
			fmt.Sprintf("General cleanup and unrelated failure check #%d.", i),
			fmt.Sprintf("Cleanup pass #%d completed cleanly.", i),
		}
	}
	dir, m := buildTestClaudeIndex(t, docs)

	res, err := relevanceSearch(dir, m, query.Options{Query: "why does DiagonalMatrix failure happen"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("expected compound-part rescue to return sessions")
	}
	if got := res.Sessions[0].ID; got != "answer" {
		t.Fatalf("expected answer (is_diagonal + MatrixSymbol) #1, got %s", got)
	}
}

func TestMultiLineIssueOutranksShortProseBrush(t *testing.T) {
	docs := map[string][]string{
		"prose-brush": {
			"Quick question about timeout handling in worker queue.",
			"We only noted timeout retries need backoff.",
		},
		"real-fix": {
			"Investigating walSegmentCheck panic during follower catchup after snapshot truncation.",
			"Root cause in walSegmentCheck: follower catchup computes segmentChecksum from truncated tail.",
			"Also audited segmentChecksum bounds and raftLogOffset replay after follower catchup.",
			"Added regression test verifying walSegmentCheck and raftLogOffset survive truncated snapshot catchup.",
		},
	}
	for i := range 24 {
		docs[fmt.Sprintf("corp-%02d", i)] = []string{
			fmt.Sprintf("Worker queue timeout maintenance note %d.", i),
			fmt.Sprintf("Verified worker note %d.", i),
		}
	}
	dir, m := buildTestClaudeIndex(t, docs)

	issueQ := strings.Join([]string{
		"walSegmentCheck panics during follower catchup after snapshot truncation",
		"When a follower receives a truncated snapshot during catchup,",
		"walSegmentCheck reads stale raftLogOffset and triggers segmentChecksum mismatch.",
		"Expected worker queue timeout retries should recover cleanly after walSegmentCheck.",
	}, "\n")
	res, err := relevanceSearch(dir, m, query.Options{Query: issueQ})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("expected multiline issue search to return sessions")
	}
	if got := res.Sessions[0].ID; got != "real-fix" {
		t.Fatalf("expected real-fix #1 over short prose brush, got %s", got)
	}
}

func TestQuotedMultilineIssueReachesRelevanceDespiteCooccurNeighbor(t *testing.T) {
	docs := map[string][]string{
		"gold": {
			"Debugging sparseEigenSolver when pivotScale \"exact\" threshold mismatches runtime config.",
			"Fixed sparseEigenSolver pivotScale calibration and runtime threshold validation.",
		},
		"cooccur-bait": {
			"Adjusting pivotScale threshold during startup calibration and runtime config check.",
			"Updated calibration log messages for runtime config check.",
		},
	}
	for i := range 16 {
		docs[fmt.Sprintf("fill-%02d", i)] = []string{
			fmt.Sprintf("Calibration and runtime threshold note %d with generic config details.", i),
			fmt.Sprintf("Closed calibration check %d.", i),
		}
	}
	dir, _ := buildTestClaudeIndex(t, docs)

	issueQ := strings.Join([]string{
		`sparseEigenSolver fails on "pivotScale" calibration`,
		`When passing runtime threshold config with sparseEigenSolver,`,
		`exact phrase "mismatches config runtime" is never printed verbatim.`,
	}, "\n")
	res, err := SearchDetailed(dir, query.Options{Query: issueQ})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != query.TierRelevance {
		t.Fatalf("Tier = %q, want %q (must not be hijacked by cooccur tier)", res.Tier, query.TierRelevance)
	}
	if len(res.Sessions) == 0 || res.Sessions[0].ID != "gold" {
		t.Fatalf("expected gold #1 after quote loosening to relevance, got %+v", res.Sessions)
	}
}

func TestStackTraceAndDottedCompoundPartsDisambiguateLongIssue(t *testing.T) {
	if parts := queryCompoundParts("203.0.113.51", []string{"203.0.113.51"}); len(parts) != 0 {
		t.Fatalf("expected IP address to produce no compound parts, got %v", parts)
	}

	docs := map[string][]string{
		"gold": {
			"Debugging sparse expression conversion with as_explicit in sympy/matrices/expressions/matmul.py.",
			"Fixed as_explicit indexing in sympy/matrices/expressions/matmul.py so symbolic blocks align cleanly.",
		},
		"distractor": {
			"Investigating MatrixSymbol block partitioning in sympy/matrices/expressions/blockmatrix.py.",
			"ValueError when Matrix Symbol dimensions mismatch inside blockmatrix helpers.",
		},
	}
	for i := range 10 {
		docs[fmt.Sprintf("noise-%02d", i)] = []string{
			fmt.Sprintf("Matrix Symbol ValueError troubleshooting note %d for general expression evaluation.", i),
			fmt.Sprintf("Verified expression check %d passed.", i),
		}
	}
	dir, _ := buildTestClaudeIndex(t, docs)

	issueQ := strings.Join([]string{
		"Conversion fails on product expressions during symbolic evaluation",
		`File "/Users/alex/venv/lib/python3.11/site-packages/sympy/matrices/expressions/matmul.py", line 42, in evaluate`,
		"Triggering expr_tmp.as_explicit() raises ValueError when Matrix and Symbol dimensions interact",
		"while otherMatrixSymbol checks report generic ValueError messages on Matrix inputs.",
	}, "\n")
	res, err := SearchDetailed(dir, query.Options{Query: issueQ})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tier != query.TierRelevance {
		t.Fatalf("Tier = %q, want %q", res.Tier, query.TierRelevance)
	}
	if len(res.Sessions) == 0 || res.Sessions[0].ID != "gold" {
		t.Fatalf("expected gold (as_explicit + matmul) #1 on long traceback issue, got %+v", res.Sessions)
	}
}

func TestIssueQueryStripsCommentsURLsAndPopulatesSessionIDF(t *testing.T) {
	extraMsgs := make([]string, 0, 25)
	for i := range 25 {
		extraMsgs = append(extraMsgs, fmt.Sprintf("Checked archive message %d about forums/topic/thread alignment.", i))
	}
	docs := map[string][]string{
		"gold": {
			"Investigating queryPlanner.defer() failure when deferred columns alias outer table.",
			"Fixed defer() alias tracking in planner columns projection so deferred fields load properly.",
		},
		"url-bait": append([]string{
			"Discussion from https://forums.example.org/archive/#!topic/dev-list/abc123 about forums topic layout.",
			"See release notes and fixes template references for contributing guide columns.",
		}, extraMsgs...),
	}
	for i := range 10 {
		docs[fmt.Sprintf("cols-%02d", i)] = []string{
			fmt.Sprintf("General columns maintenance check %d for standard tables.", i),
			fmt.Sprintf("Routine maintenance message %d.", i),
		}
	}
	for i := range 20 {
		docs[fmt.Sprintf("other-%02d", i)] = append([]string{
			fmt.Sprintf("Unrelated storage check %d without table projection.", i),
		}, extraMsgs[:8]...)
	}
	dir, m := buildTestClaudeIndex(t, docs)

	issueQ := strings.Join([]string{
		"queryPlanner.defer() failure when deferred columns compute per table instead per alias",
		"<!-- Please search existing issues and CONTRIBUTING.md before filing -->",
		"Calling outer_qs.defer('title') fails when columns use select_related failure path.",
		"More background in https://forums.example.org/archive/#!topic/dev-list/xyz987.",
	}, "\n")
	if parts := queryCompoundParts(cleanIssueQuery(issueQ), RelevanceTerms(cleanIssueQuery(issueQ))); containsAny(parts, "forums", "topic", "contributing") {
		t.Fatalf("expected URL/HTML-comment tokens excluded from compound parts, got %v", parts)
	}

	res, err := relevanceSearch(dir, m, query.Options{Query: issueQ})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 || res.Sessions[0].ID != "gold" {
		t.Fatalf("expected gold #1 on cleaned multiline issue, got %+v", res.Sessions)
	}
	sessIDF, ok := res.SessionIDF["columns"]
	if !ok {
		t.Fatalf("expected SessionIDF entry for \"columns\", got %v", res.SessionIDF)
	}
	wantSessIDF := rankIDF(len(m.Sessions), 12)
	if math.Abs(sessIDF-wantSessIDF) > 1e-6 {
		t.Fatalf("SessionIDF[columns] = %v, want %v (whole-session IDF)", sessIDF, wantSessIDF)
	}
	if res.TermIDF["columns"] <= sessIDF {
		t.Fatalf("expected message-capped TermIDF[columns] (%v) > SessionIDF[columns] (%v)", res.TermIDF["columns"], sessIDF)
	}
}

func TestTitleRepeatedModerateDomainTermsLiftMatchingSession(t *testing.T) {
	docs := map[string][]string{
		"gold": {
			"Updating GridSolver shape handling inside gridmod.py during sparse broadcast.",
			"Fixed GridSolver shape validation so broadcast matches gridmod conventions.",
		},
		"template-distractor": {
			"Updated release notes and fixes list with references and other minor comments.",
			"Added release notes and references section for changelog cleanup.",
		},
	}
	// Make gridsolver/shape appear across enough sessions that 0.45 <= rankIDF < 2.0.
	for i := range 8 {
		docs[fmt.Sprintf("domain-%02d", i)] = []string{
			fmt.Sprintf("Reviewing GridSolver shape behavior for routine scenario %d.", i),
			fmt.Sprintf("Recorded GridSolver shape note %d.", i),
		}
	}
	for i := range 12 {
		docs[fmt.Sprintf("misc-%02d", i)] = []string{
			fmt.Sprintf("Release notes and fixes checklist %d with unrelated references.", i),
			fmt.Sprintf("Completed notes checklist %d.", i),
		}
	}
	dir, m := buildTestClaudeIndex(t, docs)

	issueQ := strings.Join([]string{
		"GridSolver shape mismatch during broadcast in gridmod",
		"Notes: release fixes references comments below describe reproduction.",
		"Repeated GridSolver check shows GridSolver computes wrong shape after GridSolver resize.",
	}, "\n")
	res, err := relevanceSearch(dir, m, query.Options{Query: issueQ})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 || res.Sessions[0].ID != "gold" {
		t.Fatalf("expected gold #1 via title/repeated moderate-IDF domain terms, got %+v", res.Sessions)
	}
}

func containsAny(items []string, targets ...string) bool {
	for _, it := range items {
		if slices.Contains(targets, it) {
			return true
		}
	}
	return false
}
