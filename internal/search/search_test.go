package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSearchRanksAndFilters(t *testing.T) {
	now := time.Now()
	ss := []model.Session{{ID: "a", Harness: "claude", Project: "p", Updated: now, Messages: []model.Message{{Role: "user", Text: "needle needle", Time: now}}}, {ID: "b", Harness: "codex", Project: "p", Updated: now.Add(-24 * time.Hour), Messages: []model.Message{{Role: "assistant", Text: "needle", Time: now}}}}
	hits, err := Run(ss, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Session.ID != "a" || hits[0].Count != 2 {
		t.Fatalf("bad hits: %#v", hits)
	}
	hits, err = Run(ss, Options{Query: "needle", Harness: "codex", Role: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "b" {
		t.Fatalf("bad filter: %#v", hits)
	}
}

func TestRunBM25Signals(t *testing.T) {
	now := time.Now()
	session := func(id, role, text string, updated time.Time) model.Session {
		return model.Session{ID: id, Harness: "claude", Project: "p", Updated: updated, Messages: []model.Message{{Role: role, Text: text}}}
	}
	tests := []struct {
		name  string
		query string
		ss    []model.Session
		want  string
	}{
		{"term frequency", "needle", []model.Session{
			session("padding", "user", "needle "+strings.Repeat("padding ", 100), now),
			session("frequency", "user", "needle needle needle", now),
		}, "frequency"},
		{"rare term", "common rare", []model.Session{
			session("common", "user", "common rare common common", now),
			session("rare", "user", "common rare", now),
			session("noise", "user", "common common", now),
		}, "rare"},
		{"user role", "needle", []model.Session{
			session("assistant", "assistant", "needle", now),
			session("user", "user", "needle", now),
		}, "user"},
		{"recency", "needle", []model.Session{
			session("new", "user", "needle", now),
			session("old", "user", "needle", now.Add(-24*time.Hour)),
		}, "new"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := Run(tc.ss, Options{Query: tc.query, All: true})
			if err != nil || len(hits) < 2 || hits[0].Session.ID != tc.want {
				t.Fatalf("hits=%#v err=%v", hits, err)
			}
		})
	}
}

func TestRunBM25DeterministicIDTieBreak(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ss := []model.Session{
		{ID: "z", Updated: when, Messages: []model.Message{{Role: "user", Text: "needle"}}},
		{ID: "a", Updated: when, Messages: []model.Message{{Role: "user", Text: "needle"}}},
	}
	for i := 0; i < 5; i++ {
		hits, err := Run(ss, Options{Query: "needle", All: true})
		if err != nil || len(hits) != 2 || hits[0].Session.ID != "a" || hits[1].Session.ID != "z" {
			t.Fatalf("iteration %d hits=%#v err=%v", i, hits, err)
		}
	}
}

func TestBM25HelperSignals(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if got := freshnessDecay(time.Time{}, now); got != 0 {
		t.Fatalf("zero decay=%v", got)
	}
	if got := freshnessDecay(now.Add(time.Hour), now); got != 1 {
		t.Fatalf("future decay=%v", got)
	}
	if got := freshnessDecay(now.Add(-24*time.Hour), now); got != 0.5 {
		t.Fatalf("one-day decay=%v", got)
	}
	counts := make([]int, 2)
	userCounts := make([]int, 2)
	if got := countDocumentWords("one needle, needle-two", []string{"needle", "needle-two"}, counts, userCounts, true); got != 3 || counts[0] != 1 || counts[1] != 1 || userCounts[1] != 1 {
		t.Fatalf("word counts len=%d counts=%v user=%v", got, counts, userCounts)
	}
}

func TestRunInvalidRegexAndResultLimit(t *testing.T) {
	if _, err := Run(nil, Options{Query: "(", Regex: true}); err == nil {
		t.Fatal("expected invalid regex error")
	}
	var ss []model.Session
	for i := 0; i < 20; i++ {
		ss = append(ss, model.Session{ID: string(rune('a' + i)), Harness: "claude", Project: "p", Updated: time.Now().Add(time.Duration(i) * time.Minute), Messages: []model.Message{{Role: "user", Text: "needle"}}})
	}
	hits, err := Run(ss, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 15 {
		t.Fatalf("limited hits = %d", len(hits))
	}
	hits, err = Run(ss, Options{Query: "needle", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 20 {
		t.Fatalf("all hits = %d", len(hits))
	}
}

func TestRunFilterSkipsRegexAndTieBranches(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "old", Harness: "claude", Project: "proj", Updated: now.Add(-48 * time.Hour), Messages: []model.Message{{Role: "user", Text: "needle 123"}}},
		{ID: "project-skip", Harness: "claude", Project: "other", Updated: now, Messages: []model.Message{{Role: "user", Text: "needle 123"}}},
		{ID: "role-skip", Harness: "claude", Project: "proj", Updated: now, Messages: []model.Message{{Role: "assistant", Text: "needle 123"}}},
		{ID: "hit-a", Harness: "claude", Project: "proj", Updated: now, Messages: []model.Message{{Role: "user", Text: "needle 123"}}},
		{ID: "hit-b", Harness: "claude", Project: "proj", Updated: now, Messages: []model.Message{{Role: "user", Text: "needle 456"}}},
	}
	hits, err := Run(ss, Options{Query: `needle \d+`, Regex: true, Project: "proj", Role: "user", Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Session.Updated.Before(hits[1].Session.Updated) {
		t.Fatalf("filtered regex hits = %#v", hits)
	}
	if _, ok := FindByPrefix(ss, "missing"); ok {
		t.Fatal("unexpected prefix match")
	}
}

func TestMergeSessionsHistoryProjectReplacement(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "same", Harness: "codex", Project: "history", Updated: now, Messages: []model.Message{{Role: "user", Text: "first needle"}}},
		{ID: "same", Harness: "codex", Project: "real-project", Updated: now.Add(time.Hour), Messages: []model.Message{{Role: "assistant", Text: "second needle"}}},
	}
	got, ok := FindByPrefix(ss, "sam")
	if !ok || got.Project != "real-project" || len(got.Messages) != 2 || !got.Updated.Equal(now.Add(time.Hour)) {
		t.Fatalf("merged = %#v ok=%v", got, ok)
	}
}

func TestPrintJSONSessionContextAndHelpers(t *testing.T) {
	now := time.Now()
	s := model.Session{ID: "abcdef1234567890", Harness: "claude", Project: "proj", Updated: now, Messages: []model.Message{
		{Role: "user", Text: "hello needle world", Time: now},
		{Role: "assistant", Text: strings.Repeat("tool_use ", 80), Time: now},
		{Role: "assistant", Text: "```go\nfmt.Println(needle)\n```", Time: now},
	}}
	hits := []Hit{{Session: s, Count: 1, Snippets: []string{"hello needle"}}}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "needle", JSON: true})
	var decoded []Hit
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("bad json %q err=%v", b.String(), err)
	}
	b.Reset()
	PrintSession(&b, s)
	if !strings.Contains(b.String(), "# claude") || !strings.Contains(b.String(), "[tool/local output collapsed]") {
		t.Fatalf("bad session: %q", b.String())
	}
	b.Reset()
	PrintContext(&b, s, "needle")
	if !strings.Contains(b.String(), "# deja context:") || !strings.Contains(b.String(), "fmt.Println") {
		t.Fatalf("bad context: %q", b.String())
	}
	if got, ok := FindByPrefix([]model.Session{s}, "abcdef"); !ok || got.ID != s.ID {
		t.Fatalf("FindByPrefix got %#v ok=%v", got, ok)
	}
	if got := Recent([]model.Session{s, {ID: "old", Updated: now.Add(-time.Hour)}}, 1); len(got) != 1 || got[0].ID != s.ID {
		t.Fatalf("Recent=%#v", got)
	}
}

func TestPrintSessionContextAndDigestBudgetEdges(t *testing.T) {
	long := strings.Repeat("context prose ", 900) + "é"
	s := model.Session{ID: "short", Harness: "codex", Project: "p", Messages: []model.Message{
		{Role: "assistant", Text: "ignored when unmatched"},
		{Role: "user", Text: long},
		{Role: "assistant", Text: "needle " + long},
	}}
	var b bytes.Buffer
	PrintSession(&b, model.Session{ID: "empty", Messages: []model.Message{{Role: "user", Text: "   "}}})
	if strings.Contains(b.String(), "user:") {
		t.Fatalf("blank message printed: %q", b.String())
	}
	b.Reset()
	PrintContext(&b, s, "needle")
	if b.Len() > 8050 || !strings.Contains(b.String(), "# deja context:") {
		t.Fatalf("context budget bad len=%d out=%q", b.Len(), b.String()[:min(b.Len(), 80)])
	}
	digest := AutoRecallDigest([]model.Session{s}, 79)
	if len(digest) > 79 || !utf8.ValidString(digest) {
		t.Fatalf("digest budget invalid len=%d valid=%v", len(digest), utf8.ValidString(digest))
	}
}

func TestHighlightDateSnippetAndColorBranches(t *testing.T) {
	if got := highlight("Needle and thread", "needle", false, true); !strings.Contains(got, cMatch+"Needle"+cReset) {
		t.Fatalf("highlight literal=%q", got)
	}
	if got := highlight("abc 123", `\d+`, true, true); !strings.Contains(got, cMatch+"123"+cReset) {
		t.Fatalf("highlight regex=%q", got)
	}
	if got := highlight("jwt and refresh", "jwt refresh", false, true); strings.Count(got, cMatch) != 2 {
		t.Fatalf("highlight tokens=%q", got)
	}
	if got := highlight("x", "x", false, false); got != "x" {
		t.Fatalf("highlight no color=%q", got)
	}
	for _, h := range []string{"claude", "codex", "opencode", "other"} {
		if !strings.Contains(harnessTag(h, true), "["+h+"]") || harnessTag(h, false) != "["+h+"]" {
			t.Fatalf("bad harness tag %q", h)
		}
	}
	now := time.Now()
	for _, tt := range []time.Time{now, now.AddDate(0, 0, -3), now.AddDate(0, -1, 0), now.AddDate(-1, 0, 0)} {
		if relativeDate(tt) == "" {
			t.Fatalf("empty relative date")
		}
	}
	if got := snippet("needle at start "+strings.Repeat("x", 300), "needle", nil); !strings.HasPrefix(got, "needle") || !strings.HasSuffix(got, "…") {
		t.Fatalf("snippet start=%q", got)
	}
	if got := snippet(strings.Repeat("x", 300)+" needle", "needle", nil); !strings.HasPrefix(got, "…") || !strings.Contains(got, "needle") {
		t.Fatalf("snippet end=%q", got)
	}
	if got := snippet("no direct tokens but has jwt", "jwt refresh", nil); !strings.Contains(got, "jwt") {
		t.Fatalf("snippet token=%q", got)
	}
	if got := snippet(strings.Repeat("x", 100)+" 123 "+strings.Repeat("y", 200), "\\d+", regexp.MustCompile(`\d+`)); !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("snippet regex middle=%q", got)
	}
	if got := highlight("abc", "(", true, true); got != "abc" {
		t.Fatalf("highlight bad regex=%q", got)
	}
	if got := highlight("abc", "a", false, true); !strings.Contains(got, cMatch+"a"+cReset) {
		t.Fatalf("highlight short token literal=%q", got)
	}
	t.Setenv("NO_COLOR", "1")
	if colorOK(os.Stdout) {
		t.Fatalf("NO_COLOR ignored")
	}
}

func TestMultiWordSearchRequiresAllTokensAndCountsOccurrences(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "both", Harness: "claude", Project: "p", Updated: now, Messages: []model.Message{{Role: "user", Text: "refresh the jwt access token, then jwt again", Time: now}}},
		{ID: "one", Harness: "claude", Project: "p", Updated: now, Messages: []model.Message{{Role: "user", Text: "jwt only", Time: now}}},
	}
	hits, err := Run(ss, Options{Query: "jwt refresh token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Session.ID != "both" || hits[0].Count != 4 {
		t.Fatalf("bad multi-word hits: %#v", hits)
	}
}

func TestQuotedPhrasesRequireContiguousCaseInsensitiveText(t *testing.T) {
	now := time.Now()
	ss := []model.Session{
		{ID: "phrase", Updated: now, Messages: []model.Message{{Text: "Connection POOL exhausted again"}}},
		{ID: "apart", Updated: now, Messages: []model.Message{{Text: "connection pool was completely exhausted"}}},
	}
	hits, err := Run(ss, Options{Query: `"connection pool exhausted"`})
	if err != nil || len(hits) != 1 || hits[0].Session.ID != "phrase" {
		t.Fatalf("phrase hits = %#v err=%v", hits, err)
	}
	hits, err = Run(ss, Options{Query: `"connection pool exhausted" again`})
	if err != nil || len(hits) != 1 || hits[0].Session.ID != "phrase" {
		t.Fatalf("mixed phrase hits = %#v err=%v", hits, err)
	}
	if got, _ := Run(ss, Options{Query: `"connection pool`}); len(got) != 2 {
		t.Fatalf("unbalanced quote should be ordinary terms: %#v", got)
	}
}

func TestQuotedPunctuationOnlyIsIgnored(t *testing.T) {
	terms, phrases := QueryParts(`"---" needle`)
	if len(phrases) != 0 || len(terms) != 1 || terms[0] != "needle" {
		t.Fatalf("parts = %#v %#v", terms, phrases)
	}
}

func TestQueryPartsDropsStopWordsButKeepsAllStopQueries(t *testing.T) {
	terms, phrases := QueryParts(`have we fixed "the jwt" before`)
	if strings.Join(terms, ",") != "fixed,jwt" || len(phrases) != 1 || phrases[0] != "the jwt" {
		t.Fatalf("parts = %#v %#v", terms, phrases)
	}
	terms, phrases = QueryParts("have we before")
	if strings.Join(terms, ",") != "have,we,before" || len(phrases) != 0 {
		t.Fatalf("all-stop parts = %#v %#v", terms, phrases)
	}
}

func TestFuzzyJSONEnvelope(t *testing.T) {
	var b bytes.Buffer
	Print(&b, []Hit{{Count: 1}}, Options{JSON: true, Fuzzy: true})
	if !strings.Contains(b.String(), `"fuzzy":true`) || !strings.Contains(b.String(), `"hits"`) {
		t.Fatalf("fuzzy json = %q", b.String())
	}
}

func TestStemmedJSONEnvelope(t *testing.T) {
	var b bytes.Buffer
	Print(&b, []Hit{{Count: 1}}, Options{JSON: true, Stemmed: true, FuzzyVariants: map[string][]string{"rotation": {"rotated"}}})
	if !strings.Contains(b.String(), `"stemmed":true`) || !strings.Contains(b.String(), `"rotation":["rotated"]`) {
		t.Fatalf("stemmed json = %q", b.String())
	}
}

func TestFuzzyMatchingUsesVariants(t *testing.T) {
	if !MatchesQuery("needle", "needle") || MatchesQuery("other", "needle") {
		t.Fatal("literal query matcher failed")
	}
	hits, err := Run([]model.Session{{ID: "fuzzy", Updated: time.Now(), Messages: []model.Message{{Text: "connection exhausted"}}}}, Options{
		Query: "connecton exhaustd", All: true, FuzzyVariants: map[string][]string{"connecton": {"connection"}, "exhaustd": {"exhausted"}},
	})
	if err != nil || len(hits) != 1 || hits[0].Count == 0 {
		t.Fatalf("fuzzy hits=%#v err=%v", hits, err)
	}
	if hits[0].Tier != TierExact {
		t.Fatalf("direct search tier=%q", hits[0].Tier)
	}
	hits, err = Run([]model.Session{{ID: "close", Updated: time.Now(), Messages: []model.Message{{Text: "connection exhausted"}}}}, Options{
		Query: "connecton exhaustd", All: true, Tier: TierClose,
		FuzzyVariants: map[string][]string{"connecton": {"connection"}, "exhaustd": {"exhausted"}},
	})
	if err != nil || len(hits) != 1 || hits[0].TierDetail != "connecton->connection" {
		t.Fatalf("close tier=%#v err=%v", hits, err)
	}
}

func TestPrintAlwaysEmitsTierAndLabelsFallback(t *testing.T) {
	var b bytes.Buffer
	Print(&b, []Hit{{Count: 1}}, Options{JSON: true})
	if !strings.Contains(b.String(), `"tier":"exact"`) {
		t.Fatalf("exact JSON=%q", b.String())
	}
	b.Reset()
	Print(&b, []Hit{{Count: 1, Tier: TierClose, TierDetail: "rotaton->rotation"}}, Options{})
	if !strings.Contains(b.String(), "close (rotaton->rotation)") {
		t.Fatalf("close output=%q", b.String())
	}
}

func BenchmarkPhraseVerification(b *testing.B) {
	text := strings.Repeat("prefix words ", 200) + "connection pool exhausted" + strings.Repeat(" suffix words", 200)
	terms, phrases := QueryParts(`"connection pool exhausted"`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !MatchesParts(text, terms, phrases, nil) {
			b.Fatal("phrase did not match")
		}
	}
}

func TestPrintPlainWhenNotTTY(t *testing.T) {
	now := time.Now()
	hits := []Hit{{Session: model.Session{ID: "abcdef1234567890", Harness: "opencode", Project: "deja", Updated: now}, Count: 1, Snippets: []string{"hello needle"}}}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "needle"})
	out := b.String()
	if strings.Contains(out, "\x1b[") || !strings.Contains(out, "[opencode]") || !strings.Contains(out, "1 matches") {
		t.Fatalf("bad plain output: %q", out)
	}
}

func TestPrintZeroDateAndContextHelpers(t *testing.T) {
	hits := []Hit{{Session: model.Session{ID: "id", Harness: "x", Project: "p"}, Count: 2}}
	var b bytes.Buffer
	Print(&b, hits, Options{Query: "needle"})
	if !strings.Contains(b.String(), " · - · ") {
		t.Fatalf("zero date print = %q", b.String())
	}
	longLines := strings.Repeat("line with prose words\n", 12)
	if got := contextText(longLines, false); strings.Count(got, "line") > 8 {
		t.Fatalf("contextText did not cap lines: %q", got)
	}
	for _, p := range []string{"<command-x", "<task-notification", "<teammate-message", "<bash-x", "Caveat:", "<system-reminder"} {
		if !noiseMessage(p + " noise") {
			t.Fatalf("noiseMessage missed %q", p)
		}
	}
}

func TestAutoRecallDigestCappedMarkdown(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ss := []model.Session{{ID: "abcdef123456", Harness: "claude", Project: "project", Updated: now, Messages: []model.Message{
		{Role: "user", Text: "Find frobnicator bug\nwith extra detail", Time: now},
		{Role: "assistant", Text: "The frobnicator bug is in parser.go and the fix is to trim tokens.", Time: now},
		{Role: "assistant", Text: "Add a regression test for parser.go.", Time: now},
	}}}
	digest := AutoRecallDigest(ss, 2000)
	if !strings.Contains(digest, "**project**") || !strings.Contains(digest, "Find frobnicator bug") || !strings.Contains(digest, "parser.go") {
		t.Fatalf("bad digest: %q", digest)
	}
	short := AutoRecallDigest(ss, 80)
	if len(short) > 80 || strings.TrimSpace(short) == "" {
		t.Fatalf("bad capped digest len=%d %q", len(short), short)
	}
	if got := AutoRecallDigest([]model.Session{{ID: "empty"}}, 100); got != "" {
		t.Fatalf("empty digest=%q", got)
	}
	if got := AutoRecallDigest(append(ss, ss...), 0); got == "" {
		t.Fatal("default budget digest empty")
	}
	if got := AutoRecallDigest(append(ss, ss...), 10); len(got) > 10 {
		t.Fatalf("tiny digest len=%d", len(got))
	}
}

func TestBuildAutoRecallPolicy(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	session := func(id, project, text string, updated time.Time) model.Session {
		return model.Session{ID: id, Harness: "claude", Project: project, Updated: updated, Messages: []model.Message{{Role: "user", Text: text}}}
	}
	duplicate := "parser failure needs the same token trimming regression fix"
	large := strings.Repeat("distinct relevant context ", 160)
	tests := []struct {
		name     string
		mode     string
		sessions []model.Session
		want     []string
		notWant  []string
		maxBytes int
		wantN    int
	}{
		{name: "off", mode: RecallOff, sessions: []model.Session{session("off", "org/app", large, now)}, notWant: []string{"app"}, maxBytes: 0, wantN: 0},
		{name: "safe scope and floor", mode: RecallSafe, sessions: []model.Session{
			session("current", `org\app`, "app parser regression fixed safely", now),
			session("other", "org/other", "other parser regression fixed safely", now),
			session("weak", "org/app", "too short", now),
		}, want: []string{"`current`"}, notWant: []string{"`other`", "`weak`"}, maxBytes: 2048, wantN: 1},
		{name: "safe dedupe", mode: RecallSafe, sessions: []model.Session{
			session("first", "org/app", duplicate, now),
			session("second", "org/app", duplicate+".", now.Add(-time.Hour)),
		}, want: []string{"`first`"}, notWant: []string{"`second`"}, maxBytes: 2048, wantN: 1},
		{name: "safe prefers recent", mode: RecallSafe, sessions: []model.Session{
			session("old", "org/app", "old unique migration details remain useful", now.AddDate(0, 0, -100)),
			session("new", "org/app", "new unique parser details remain useful", now.AddDate(0, 0, -2)),
		}, want: []string{"`new`", "`old`"}, maxBytes: 2048, wantN: 2},
		{name: "aggressive cross project and cap", mode: RecallAggressive, sessions: []model.Session{
			session("other", "org/other", large, now),
			session("current", "org/app", large+" unique", now.Add(-time.Hour)),
		}, want: []string{"org/other"}, maxBytes: 4096, wantN: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildAutoRecall(tc.sessions, AutoRecallOptions{Mode: tc.mode, ProjectNames: []string{"org/app"}, Now: now})
			if len(got.Text) > tc.maxBytes || got.Sessions != tc.wantN {
				t.Fatalf("result len=%d sessions=%d, want <=%d/%d: %q", len(got.Text), got.Sessions, tc.maxBytes, tc.wantN, got.Text)
			}
			for _, want := range tc.want {
				if !strings.Contains(got.Text, want) {
					t.Fatalf("result missing %q: %q", want, got.Text)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(got.Text, notWant) {
					t.Fatalf("result contains %q: %q", notWant, got.Text)
				}
			}
		})
	}

	ordered := BuildAutoRecall([]model.Session{
		session("old-order", "org/app", "old unique migration details remain useful", now.AddDate(0, 0, -100)),
		session("new-order", "org/app", "new unique parser details remain useful", now.AddDate(0, 0, -2)),
	}, AutoRecallOptions{Mode: RecallSafe, ProjectNames: []string{"org/app"}, Now: now}).Text
	if strings.Index(ordered, "`new-order`") > strings.Index(ordered, "`old-order`") {
		t.Fatalf("recent result was not first: %q", ordered)
	}

	multibyte := "relevant parser context " + strings.Repeat("界", 300)
	capSession := model.Session{ID: "cap", Harness: "claude", Project: "org/app", Updated: now, Messages: []model.Message{
		{Role: "user", Text: multibyte},
		{Role: "assistant", Text: multibyte},
		{Role: "assistant", Text: multibyte},
	}}
	for _, tc := range []struct {
		mode string
		cap  int
	}{{RecallSafe, 2048}, {RecallAggressive, 4096}} {
		got := BuildAutoRecall([]model.Session{capSession, capSession, capSession}, AutoRecallOptions{Mode: tc.mode, ProjectNames: []string{"org/app"}, Now: now})
		if len(got.Text) > tc.cap || !utf8.ValidString(got.Text) {
			t.Fatalf("%s cap result len=%d valid=%v", tc.mode, len(got.Text), utf8.ValidString(got.Text))
		}
	}
}

func TestAutoRecallProvenanceDates(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		when time.Time
		want string
	}{
		{"today", now, "today"},
		{"yesterday", now.AddDate(0, 0, -1), "yesterday"},
		{"days", now.AddDate(0, 0, -4), "4 days ago"},
		{"future", now.AddDate(0, 0, 1), "today"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := model.Session{ID: tc.name, Harness: "claude", Project: "p", Updated: tc.when, Messages: []model.Message{{Role: "user", Text: "enough useful context"}}}
			got := BuildAutoRecall([]model.Session{s}, AutoRecallOptions{Mode: RecallAggressive, Now: now})
			if !strings.Contains(got.Text, "✓ recalled from claude session · "+tc.want) {
				t.Fatalf("provenance = %q", got.Text)
			}
		})
	}
	if got := relativeDay(time.Time{}, now); got != "unknown date" {
		t.Fatalf("zero date = %q", got)
	}
}

func TestSnippetPrefersProseOverToolDump(t *testing.T) {
	text := "netcat output noise needle\n1: package main\n2: func main() {}\nUser asked about needle migration strategy and we concluded use small batches."
	hits, err := Run([]model.Session{{ID: "s", Harness: "claude", Project: "p", Updated: time.Now(), Messages: []model.Message{{Role: "assistant", Text: text}}}}, Options{Query: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || strings.Contains(hits[0].Snippets[0], "1: package") || strings.Contains(hits[0].Snippets[0], "netcat") {
		t.Fatalf("noisy snippet: %#v", hits)
	}
	if !strings.Contains(hits[0].Snippets[0], "migration strategy") {
		t.Fatalf("missing prose snippet: %#v", hits[0].Snippets[0])
	}
}

func BenchmarkBM25Scoring1000Candidates(b *testing.B) {
	now := time.Now()
	documents := make([]bm25Document, 1000)
	for i := range documents {
		documents[i] = bm25Document{hit: Hit{Session: model.Session{ID: fmt.Sprintf("session-%04d", i), Updated: now}}, termCount: []int{1, 1}, userCount: []int{1, 1}, length: 6}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hits := scoreBM25(documents, []int{1000, 1000}, 1000, 6, false)
		if len(hits) != len(documents) {
			b.Fatalf("hits=%d", len(hits))
		}
	}
}

func TestTierLabelVocabulary(t *testing.T) {
	if got := tierLabel(Hit{}); got != "" {
		t.Fatalf("empty tier label = %q", got)
	}
	if got := tierLabel(Hit{Tier: TierExact}); got != "" {
		t.Fatalf("exact tier label = %q", got)
	}
	if got := tierLabel(Hit{Tier: TierClose}); got != " · close" {
		t.Fatalf("close no-detail label = %q", got)
	}
	if got := tierLabel(Hit{Tier: TierSemantic, TierDetail: "0.71"}); got != " · semantic (0.71)" {
		t.Fatalf("semantic label = %q", got)
	}
}
