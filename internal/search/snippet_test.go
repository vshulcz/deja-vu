package search

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"strings"
	"testing"
	"time"
	"unicode"
)

// quotableCorpus is a pile of sessions that all match, so the difference
// between "quote everything that matched" and "quote what is served" is
// visible.
func quotableCorpus(n int) []model.Session {
	now := time.Now()
	ss := make([]model.Session, 0, n)
	for i := range n {
		text := fmt.Sprintf("session %d: the billing exporter drops every third retry, and the backoff counted from zero", i)
		ss = append(ss, model.Session{
			ID: fmt.Sprintf("s%03d", i), Harness: "claude", Project: fmt.Sprintf("proj%d", i),
			Updated:  now.AddDate(0, 0, -i),
			Messages: []model.Message{{Role: "user", Text: text, Time: now.AddDate(0, 0, -i)}},
		})
	}
	return ss
}

// Quoting every session that matched made a search cost the same at --limit 1
// and at --limit 100 — 806 ms either way on a 31 MB store, a third of the
// bytes it allocated (#3544). What a reader sees must not change with it.
func TestTheQuotesAreTheSameWhateverTheCap(t *testing.T) {
	ss := quotableCorpus(120)
	all, err := Run(ss, Options{Query: "billing exporter retry", All: true})
	if err != nil {
		t.Fatal(err)
	}
	few, err := Run(ss, Options{Query: "billing exporter retry", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(few) != 3 {
		t.Fatalf("capped search returned %d hits, want 3", len(few))
	}
	for i, h := range few {
		if len(h.Snippets) == 0 {
			t.Fatalf("served hit %d (%s) came back with nothing quoted", i, h.Session.ID)
		}
		if h.Session.ID != all[i].Session.ID {
			t.Fatalf("hit %d is %s capped and %s uncapped", i, h.Session.ID, all[i].Session.ID)
		}
		if len(h.Snippets) != len(all[i].Snippets) {
			t.Fatalf("hit %d quotes %d passages capped and %d uncapped", i, len(h.Snippets), len(all[i].Snippets))
		}
		for j := range h.Snippets {
			if h.Snippets[j] != all[i].Snippets[j] {
				t.Errorf("hit %d quote %d differs:\n capped:   %q\n uncapped: %q", i, j, h.Snippets[j], all[i].Snippets[j])
			}
		}
	}
	// And every hit an uncapped search returns is quoted too — the deferral
	// must not leave the tail of a `--all` answer blank.
	for _, h := range all {
		if len(h.Snippets) == 0 {
			t.Fatalf("uncapped hit %s came back with nothing quoted", h.Session.ID)
		}
	}
}

// The work itself: below the window markEarlierAttempts compares, a hit that
// the cap will drop carries its chosen passages and no rendered quote.
func TestABigSearchDoesNotQuoteWhatItWillNotServe(t *testing.T) {
	ss := quotableCorpus(120)
	hits, err := runScored(ss, Options{Query: "billing exporter retry"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < earlierAttemptWindow+10 {
		t.Fatalf("corpus produced %d hits, too few to show the difference", len(hits))
	}
	for i := range earlierAttemptWindow {
		if len(hits[i].Snippets) == 0 {
			t.Fatalf("hit %d is inside the comparison window and was not quoted", i)
		}
	}
	quoted := 0
	for i := earlierAttemptWindow; i < len(hits); i++ {
		if len(hits[i].Snippets) > 0 {
			quoted++
		}
		if len(hits[i].snipTexts) == 0 {
			t.Fatalf("hit %d kept no passage to quote later", i)
		}
	}
	if quoted != 0 {
		t.Errorf("%d hits below the window were quoted before anything asked for them", quoted)
	}
}

// Picking the right message is only half of showing the answer: the excerpt is
// a 300-rune window cut out of it. Centring that window on the first word of the
// query to appear put the reader four paragraphs from the passage they searched
// for, in the same message that matched.
func TestSnippetCentresWhereTheWordsMeet(t *testing.T) {
	text := "gift " + strings.Repeat("filler ", 80) + "the gift my dad gave me for the trip"
	if got := Snippet(text, "dad gift"); !strings.Contains(got, "my dad gave me") {
		t.Errorf("excerpt %q does not show where the query words meet", got)
	}
}

// When the words never come close, there is no meeting to point at, and the
// excerpt goes back to the first mention — otherwise a fix could drag the window
// to an arbitrary midpoint that shows neither word.
func TestSnippetFallsBackToTheFirstMentionWhenWordsStayApart(t *testing.T) {
	// The tightest span here starts in the middle, at "gift", and is still far
	// too wide to be a meeting. Pointing the excerpt at it would show a word
	// from the middle of the message and call it the match.
	text := "dad " + strings.Repeat("filler ", 400) + "gift " + strings.Repeat("filler ", 35) + "dad"
	got := Snippet(text, "dad gift")
	if !strings.HasPrefix(got, "dad") {
		t.Errorf("excerpt %q does not start at the first mention", got)
	}
}

// A quoted phrase is still shown where the phrase itself is, not where its
// separate words happen to meet elsewhere.
func TestSnippetKeepsTheWholeQueryMatchFirst(t *testing.T) {
	text := strings.Repeat("filler ", 60) + "dad gift" + strings.Repeat(" filler", 60)
	if got := Snippet(text, "dad gift"); !strings.Contains(got, "dad gift") {
		t.Errorf("excerpt %q lost the exact query match", got)
	}
}

// A JSON dump in tool output holds every word of the query, so it won the
// excerpt slots of a session that was mostly dumps, and recall quoted
// `},\n {\n "type": "tool"` under it (#4780). What someone said comes first.
func TestExcerptsPreferSpeechOverToolOutputDumps(t *testing.T) {
	now := time.Now().UTC()
	// Rows the way sqlite3 prints a JSON column: long values, few keys, so the
	// key density alone does not call it data.
	var rows strings.Builder
	for i := range 6 {
		fmt.Fprintf(&rows, "2026-05-24 20:02:%02d|{\"type\":\"tool\",\"tool\":\"bash\",\"state\":{\"output\":\"%s the reviewer asked the owner about the pgx upgrade\"}}\n",
			i, strings.Repeat("ordinary build output line ", 12))
	}
	s := model.Session{
		Harness: "opencode", ID: "s", Project: "p", Updated: now,
		Messages: []model.Message{
			{Role: sources.RoleToolOutput, Text: rows.String(), Time: now},
			{Role: "assistant", Text: "The pgx upgrade goes to Dana: she is the owner and the reviewer.", Time: now},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "pgx upgrade reviewer owner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || len(hits[0].Snippets) == 0 {
		t.Fatalf("no excerpt: %+v", hits)
	}
	if first := hits[0].Snippets[0]; !strings.Contains(first, "Dana") {
		t.Errorf("exact tier: first excerpt is the dump, not what was said: %q", first)
	}
	// The relevance tier picks its excerpts on its own.
	rel := RelevanceHitsWeighted([]model.Session{s}, []string{"pgx", "upgrade", "reviewer", "owner"}, nil)
	if len(rel) != 1 || len(rel[0].Snippets) == 0 {
		t.Fatalf("relevance tier: no excerpt: %+v", rel)
	}
	if first := rel[0].Snippets[0]; !strings.Contains(first, "Dana") {
		t.Errorf("relevance tier: first excerpt is the dump, not what was said: %q", first)
	}
}

// The excerpt window starts a fixed distance before the match, so on a long line
// it used to open in the middle of whatever word sat there. Over 400 real
// messages that was 35% of excerpts; the first words are the ones a reader uses
// to decide whether to read on.
func TestAnExcerptDoesNotOpenInTheMiddleOfAWord(t *testing.T) {
	// A long line with no sentence break anywhere near the left edge, so the
	// word snap is what has to do the work.
	lead := strings.Repeat("configuration reconciliation telemetry ", 12)
	text := lead + "the exporter retried without any backoff at all " + strings.Repeat("and more words after it ", 20)

	sn := snippet(text, "backoff", nil)
	body := strings.TrimSuffix(strings.TrimPrefix(sn, "… "), " …")
	if !strings.Contains(body, "backoff") {
		t.Fatalf("the excerpt lost its own match: %q", sn)
	}
	first := strings.Fields(body)[0]
	if !strings.Contains(lead+" ", " "+first+" ") && !strings.HasPrefix(text, first) {
		t.Errorf("the excerpt opened on a word fragment %q:\n%s", first, sn)
	}
	last := strings.Fields(body)[len(strings.Fields(body))-1]
	if !strings.Contains(text, last+" ") {
		t.Errorf("the excerpt closed on a word fragment %q:\n%s", last, sn)
	}
}

// A sentence start within reach of the left edge is worth more than a word
// start: it is where the thought begins.
func TestAnExcerptPrefersToOpenOnASentence(t *testing.T) {
	lead := "Вчера мы долго ходили вокруг этого места и ничего не поняли толком совсем. " +
		"Причина оказалась в том, что бэкофф считался от нуля, поэтому четвёртая попытка шла сразу же после третьей."
	text := lead + " " + strings.Repeat("дальше идёт ещё текст про другое ", 20)

	sn := snippet(text, "четвёртая", nil)
	body := strings.TrimSuffix(strings.TrimPrefix(sn, "… "), " …")
	if !strings.HasPrefix(body, "Причина оказалась") {
		t.Errorf("the excerpt did not open on the sentence that carries the answer:\n%s", sn)
	}
	if r := []rune(body); len(r) > 0 && !unicode.IsUpper(r[0]) {
		t.Errorf("the excerpt opened mid-sentence: %q", body)
	}
}

// The snippet path had its own two filters — a numbered-line check and a
// tool-dump check — and no notion of a listing, so `wc -l` output and a run of
// paths were quoted as the passage that explains a file. The digest side has
// had the rule all along; this is the same question asked on the other path.
//
// On a real store, 29 of the 60 most-worked-on files got a different blame
// answer once the rule was shared.
func TestASnippetDoesNotQuoteALineCount(t *testing.T) {
	text := "Counted the package:\n" +
		"128 domain/apiaccess/apiaccess_test.go 210 domain/apiaccess/apiaccess.go 51 domain/apiaccess/errors.go 70 domain/apiaccess/ledger.go 35 domain/apiaccess/ports.go\n" +
		"The split is fine, so I left the layout alone."
	got := proseForSnippet(text)
	if strings.Contains(got, "apiaccess_test.go") {
		t.Errorf("a wc -l dump was kept as prose:\n%s", got)
	}
	if !strings.Contains(got, "The split is fine") {
		t.Errorf("the sentence around it was dropped too:\n%s", got)
	}
}

// The filters it already had still do their work, and a sentence is still a
// sentence — the listing rule must not start eating prose on this path either.
func TestTheSnippetPathKeepsItsOtherFilters(t *testing.T) {
	got := proseForSnippet(
		"42:  func main() {\n" +
			"Raised pgbouncer default_pool_size to 40 and the timeouts stopped.\n" +
			"12 зелёных. Жду ревьюера перед мержем.")
	if strings.Contains(got, "func main") {
		t.Errorf("a numbered source line was kept:\n%s", got)
	}
	for _, want := range []string{"default_pool_size to 40", "Жду ревьюера"} {
		if !strings.Contains(got, want) {
			t.Errorf("prose was dropped, %q is gone:\n%s", want, got)
		}
	}
}

// Everything filtered leaves the old fallback standing: a snippet of the raw
// text beats an empty one.
func TestASnippetOfNothingButAListingIsStillSomething(t *testing.T) {
	got := proseForSnippet("main.go util.go parser.go writer.go reader.go index.go search.go digest.go")
	if strings.TrimSpace(got) == "" {
		t.Error("a record that is only a listing produced no snippet at all")
	}
}

// The excerpt is positioned by searching the lowercased text and then cutting
// the original. Lowercasing maps rune to rune but not byte to byte — "İ" is two
// bytes and lowercases to one — so a message with enough capital İ before the
// match had its window slide backwards until the match fell outside it.
func TestSnippetSurvivesLowercasingThatChangesByteLength(t *testing.T) {
	text := strings.Repeat("İ ", 400) + "the needle is here"
	if got := Snippet(text, "needle"); !strings.Contains(got, "needle") {
		t.Errorf("excerpt does not contain the match it was cut around: %q", got[:min(60, len(got))])
	}
}

// The ordinary case must keep working: no length-changing runes, match in the
// middle, excerpt centred on it.
func TestSnippetStillCentresOnPlainText(t *testing.T) {
	text := strings.Repeat("filler ", 200) + "the needle is here" + strings.Repeat(" filler", 200)
	if got := Snippet(text, "needle"); !strings.Contains(got, "the needle is here") {
		t.Errorf("excerpt %q lost the match", got)
	}
}

// Ranking excerpts by how tightly the query's words meet only makes sense when
// the query is words. A regex is not: QueryParts splits `dad|gift` into two
// words a message can hold together without the pattern matching them that way,
// so the excerpt must still be chosen by how often the pattern actually hit.
func TestRegexSnippetsStillRankByMatchCount(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "dad gift"},
			{Role: "user", Text: "gift, gift and one more gift"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "dad|gift", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no snippet, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "one more gift") {
		t.Errorf("first snippet is %q, not the passage the pattern hit most", hits[0].Snippets[0])
	}
}

// A match found by folding scripts is a real match. Measuring how its words meet
// against the unfolded text finds nothing, which would rank a genuine hit behind
// passages the query never met in at all.
func TestFoldedMatchIsNotRankedAsWordsThatNeverMeet(t *testing.T) {
	scattered := strings.Repeat("連接池", 6) + strings.Repeat("其他內容", 200) + "修復"
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: scattered},
			{Role: "user", Text: "修復連接池洩漏的辦法"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "修复 连接池"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("a Simplified query did not reach Traditional text")
	}
	if !strings.Contains(hits[0].Snippets[0], "修復連接池洩漏") {
		t.Errorf("first snippet is %q, not the passage where the folded words meet", hits[0].Snippets[0])
	}
}

// The first snippet is the line a reader — or an agent with one line of budget —
// actually reads. A message repeating one query word beat the message that said
// both words together, so the excerpt showed noise and the answer came second.
func TestSnippetLeadsWithThePassageWhereWordsMeet(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: strings.Repeat("gift ", 6) + strings.Repeat("filler ", 300) + "dad"},
			{Role: "user", Text: "the gift my dad gave me for the trip"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "dad gift"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no snippet, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "my dad gave me") {
		t.Errorf("first snippet is %q, not the passage where the words meet", hits[0].Snippets[0])
	}
}

// Among passages that are equally tight, the one carrying more of the query
// still leads — otherwise a fix could pass the test above by ranking on
// tightness alone and losing the signal count was there for.
func TestSnippetStillPrefersTheDenserOfTwoTightPassages(t *testing.T) {
	s := model.Session{
		Harness: "claude", Project: "p", ID: "s1",
		Messages: []model.Message{
			{Role: "user", Text: "dad gift"},
			{Role: "user", Text: "dad gift, and another dad gift"},
		},
	}
	hits, err := Run([]model.Session{s}, Options{Query: "dad gift"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || len(hits[0].Snippets) == 0 {
		t.Fatal("no snippet, so the test measured nothing")
	}
	if !strings.Contains(hits[0].Snippets[0], "another dad gift") {
		t.Errorf("first snippet is %q, not the passage carrying more of the query", hits[0].Snippets[0])
	}
}

// An excerpt is offered as something someone said about the subject. Measured
// over twelve queries an agent would plausibly ask — 77 quoted lines — 6% were
// a `<teammate-message …>` envelope and 6% were deja's own credit sentence
// coming back as evidence.
func TestAnExcerptIsWhatSomeoneSaid(t *testing.T) {
	withEnvelope := "the retry budget stays at four " +
		`<teammate-message teammate_id="team-lead" summary="review">Review one commit</teammate-message>` +
		" and the cold path warms the index first"
	got := proseForSnippet(withEnvelope)
	if strings.Contains(got, "teammate-message") {
		t.Errorf("the harness's envelope is quoted as evidence:\n%s", got)
	}
	for _, want := range []string{"retry budget stays at four", "cold path warms the index"} {
		if !strings.Contains(got, want) {
			t.Errorf("the words around the envelope were lost: %q not in\n%s", want, got)
		}
	}
}

// The credit line deja asks an agent to write is written into the middle of a
// reply — "No files changed. No benchmarks run. deja-vu recalled: …" — so it is
// removed where it sits rather than by dropping the line it shares.
func TestDejaIsNotItsOwnWitness(t *testing.T) {
	text := "No files changed. No benchmarks run. deja-vu recalled: prior benchmark research. " +
		"The suite runs with -p 1 because the fixture is shared."
	got := proseForSnippet(text)
	if strings.Contains(strings.ToLower(got), "deja-vu recalled") {
		t.Errorf("deja is quoted back as evidence:\n%s", got)
	}
	if !strings.Contains(got, "-p 1 because the fixture is shared") {
		t.Errorf("the sentence after the credit was lost:\n%s", got)
	}
	if !strings.Contains(got, "No files changed") {
		t.Errorf("the sentence before the credit was lost:\n%s", got)
	}
}

// A person writing about the tag is not the tag.
func TestSomeoneNamingTheTagKeepsTheirSentence(t *testing.T) {
	text := "I removed the <teammate-message> handling from the parser, is that right?"
	if got := proseForSnippet(text); !strings.Contains(got, "removed the") {
		t.Errorf("a sentence about the tag was cut:\n%s", got)
	}
}
