package index

import (
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The swap rung reads each word's postings once and intersects sorted lists
// instead of rebuilding a map per word per neighbour (#5540). It has to hand
// the scan exactly what the maps did: the same records, in record order, with
// the first word's posting for each, the last read when a record repeats.
func TestSwapIntersectionMatchesTheMapIntersection(t *testing.T) {
	rng := rand.New(rand.NewSource(5562)) //nolint:gosec // a seeded fixture, not crypto
	randomPostings := func() []posting {
		n := rng.Intn(40)
		out := make([]posting, n)
		for i := range out {
			out[i] = posting{Off: int64(rng.Intn(60)), Sid: uint32(rng.Intn(1000)), Tool: rng.Intn(2) == 0} //nolint:gosec // Intn bounds both values
		}
		return out
	}
	for round := range 2000 {
		terms := make([][]posting, 1+rng.Intn(4))
		for j := range terms {
			terms[j] = randomPostings()
		}
		neighbour := randomPostings()
		swap := rng.Intn(len(terms))

		sets := make([]map[int64]posting, 0, len(terms))
		for j, posts := range terms {
			if j == swap {
				posts = neighbour
			}
			set := map[int64]posting{}
			for _, p := range posts {
				set[p.Off] = p
			}
			sets = append(sets, set)
		}
		want := intersectPostingMaps(sets)

		byTerm := make([][]posting, len(terms))
		for j, posts := range terms {
			byTerm[j] = uniqueByOffset(posts)
		}
		swapped := uniqueByOffset(neighbour)
		kept, from := keptIntersection(byTerm, swap)
		got := withSwapped(kept, from, swap, swapped)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d, swap %d of %d:\n got %v\nwant %v", round, swap, len(terms), got, want)
		}
	}
}

// Tokenising every message of every session was 6 s of a build and the last
// single-threaded pass in the sidecar phase. Each session's distinct tokens are
// collected in parallel and interned afterwards in session order, so the ids,
// the document frequencies and the neighbour map are what one core produced.
func TestNeighbourMapIsTheSameOnOneCoreAndMany(t *testing.T) {
	// A word has to appear in at least three sessions and in at most a quarter
	// of them to be banded at all, so the corpus is twelve clusters of five
	// sessions, each cluster with its own pair of words.
	ss := make([]model.Session, 0, 60)
	for cluster := range 12 {
		for n := range 5 {
			ss = append(ss, model.Session{
				Harness: "claude", ID: fmt.Sprintf("s%02d-%d", cluster, n), Project: "app",
				Messages: []model.Message{
					{Role: "user", Text: fmt.Sprintf("quokkabloom%02d keeps losing rows", cluster)},
					{Role: "assistant", Text: fmt.Sprintf("snorblewidget%02d rotation fixed quokkabloom%02d for good", cluster, cluster)},
					{Role: "assistant", Text: fmt.Sprintf("run %d of the same routine", n)},
				},
			})
		}
	}

	build := func(workers int) map[string][]string {
		rerankWorkers = func() int { return workers }
		dir := t.TempDir()
		buildCooccur(dir, ss)
		return readCooccur(dir)
	}
	one := build(1)
	many := build(8)
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	if len(one) == 0 {
		t.Fatal("no neighbours were mined, so the comparison proves nothing")
	}
	if len(one) != len(many) {
		t.Fatalf("one core mined %d tokens, eight mined %d", len(one), len(many))
	}
	for tok, want := range one {
		got, ok := many[tok]
		if !ok {
			t.Fatalf("%q has neighbours on one core and none on eight", tok)
		}
		if len(got) != len(want) {
			t.Fatalf("%q: %d neighbours on one core, %d on eight", tok, len(want), len(got))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q neighbour %d: one core says %q, eight say %q", tok, i, want[i], got[i])
			}
		}
	}
}

// The rescue is for a query whose words are right but never appear together —
// "login" next to "jwks". That is the state where the postings intersect to
// nothing, and the chain called the rescue only in the other zero-result
// branch, so the case it was written for never reached it (#1786).
func TestCooccurRescueRunsWhenThePostingsDoNotIntersect(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	root := t.TempDir()
	writeSession := func(i int, text string) {
		line := func(role, body string) string {
			b := mustJSON(map[string]any{
				"type": role, "sessionId": "s" + string(rune('a'+i%26)) + string(rune('a'+i/26)), //nolint:gosec // i is a small loop index
				"cwd": "/w", "timestamp": "2026-08-20T01:00:00Z",
				"message": map[string]any{"role": role, "content": body},
			})
			return string(b)
		}
		body := line("user", text) + "\n" + line("assistant", "resolved: "+text) + "\n"
		if err := os.WriteFile(filepath.Join(root, "s"+string(rune('a'+i%26))+string(rune('a'+i/26))+".jsonl"), []byte(body), 0o644); err != nil { //nolint:gosec // i is a small loop index
			t.Fatal(err)
		}
	}
	filler := []string{"alpha bravo charlie", "delta echo foxtrot", "golf hotel india", "juliet kilo lima"}
	for i := range 160 {
		writeSession(i, filler[i%len(filler)])
	}
	for i := 160; i < 170; i++ {
		writeSession(i, "keytab renewal failed on the build agent")
	}
	for i := 170; i < 182; i++ {
		writeSession(i, "kerberos keytab rotation on the runner")
	}
	setHome(t, t.TempDir())
	t.Setenv("DEJA_CLAUDE_ROOT", root)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(t.TempDir(), "opencode.db"))
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The map has to know them, or this proves nothing about the chain.
	if n := readCooccur(dir)["kerberos"]; len(n) == 0 {
		t.Fatalf("the fixture did not teach the map: %v", readCooccur(dir))
	}
	// The branch this is about is the one where the ANDed postings do not
	// intersect — both words are in the index, no session holds both.
	o := query.Options{Query: "kerberos renewal", All: true}
	both := map[string]bool{}
	for _, tok := range []string{"kerberos", "renewal"} {
		posts, perr := postingsFor(dir, "t"+tok)
		if perr != nil || len(posts) == 0 {
			t.Fatalf("%s has no postings, so this is not the branch under test (%v)", tok, perr)
		}
		hits, herr := Search(dir, query.Options{Query: tok, All: true})
		if herr != nil {
			t.Fatal(herr)
		}
		for _, h := range hits {
			key := h.Harness + ":" + h.ID
			if both[key] {
				t.Fatalf("%s is in a session that holds both words, so the postings do intersect", key)
			}
			both[key] = true
		}
	}
	direct, err := func() (SearchResult, error) {
		m, merr := readManifest(dir)
		if merr != nil {
			return SearchResult{}, merr
		}
		return cooccurSearch(dir, m, o)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if len(direct.Sessions) == 0 {
		t.Fatal("the rescue itself found nothing, so the fixture is wrong")
	}
	ss, err := Search(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) == 0 {
		t.Errorf("the rescue finds %d sessions and the search that a user runs returns none", len(direct.Sessions))
	}
	// And an ordinary query is unaffected.
	if hit, err := Search(dir, query.Options{Query: "keytab renewal", All: true}); err != nil || len(hit) == 0 {
		t.Errorf("a query that matches directly returned %d (%v)", len(hit), err)
	}
	_ = model.Session{}
}

// A neighbour swap is not a word form, and the two are narrated differently:
// "login" answered by "jwks" is the corpus keeping company, not a spelling.
func TestANeighbourSwapSaysItIsOne(t *testing.T) {
	if !(SearchResult{Neighbour: true}).Neighbour {
		t.Fatal("the flag does not survive a copy")
	}
	if (SearchResult{Stemmed: true}).Neighbour {
		t.Error("a stem result claims to be a neighbour swap")
	}
}

// neighbourStore is a Claude store of 16 sessions where four hold word beside
// pelicanrotor, so the neighbour map pairs them. inOutput puts word in the
// middle of a long command output, a part of the record the postings leave
// out. rewrite writes the four again without word and indexes them.
func neighbourStore(t *testing.T, word string, inOutput bool) (inc string, rewrite func()) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{"DEJA_CLAUDE_ROOT": "claude"})
	proj := filepath.Join(tmp, "claude", "-tmp-proj")
	body := func(id string, with bool) string {
		w := ""
		if with && !inOutput {
			w = word + " "
		}
		b := parityClaudeLine(id, "00:00Z", "user", fmt.Sprintf(`"why does %s spin on ledger, %s"`, id, map[bool]string{true: "pelicanrotor " + w, false: ""}[id < "s004"]))
		out := strings.Repeat("compiling package ok\n", 1200)
		if with && inOutput {
			out = strings.Repeat("compiling package ok\n", 600) + "linking " + word + " ok\n" + strings.Repeat("compiling package ok\n", 600)
		}
		b += parityClaudeLine(id, "01:01Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make build"}}]`)
		b += parityClaudeLine(id, "01:02Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":`+fmt.Sprintf("%q", out)+`}]`)
		return b
	}
	for i := range 16 {
		id := fmt.Sprintf("s%03d", i)
		parityWrite(t, filepath.Join(proj, id+".jsonl"), body(id, i < 4), false)
	}
	inc = filepath.Join(tmp, "inc")
	parityPass(t, inc, false)
	if !slices.Contains(readCooccur(inc)["pelicanrotor"], word) {
		t.Fatalf("control: the neighbour map does not pair %q: %v", word, readCooccur(inc)["pelicanrotor"])
	}
	return inc, func() {
		later := time.Now().Add(2 * time.Second)
		for i := range 4 {
			p := filepath.Join(proj, fmt.Sprintf("s%03d.jsonl", i))
			parityWrite(t, p, body(fmt.Sprintf("s%03d", i), false), false)
			_ = os.Chtimes(p, later, later)
		}
		parityPass(t, inc, false)
	}
}

// A word taken out of every transcript that held it stayed in cooccur.gob,
// the one index file a pass did not write, until a full build (#5429).
func TestRemovedTextLeavesTheNeighbourMapOnTheReplacingPass(t *testing.T) {
	for _, tc := range []struct {
		name             string
		inOutput, append bool
	}{
		{"appended", false, true},
		{"written anew", false, false},
		{"in an output's middle, appended", true, true},
		{"in an output's middle, written anew", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const word = "zzquokkamarmot"
			if !tc.append {
				appendReplacing = false
				defer func() { appendReplacing = true }()
			}
			inc, rewrite := neighbourStore(t, word, tc.inOutput)
			if tc.inOutput && filesHold(t, filepath.Join(inc, "buckets"), word) {
				t.Fatal("control: the postings hold the word, so the unposted part is not under test")
			}
			inode := indexFiles(t, inc)["records.bin"]
			rewrite()
			if tc.append != os.SameFile(inode, indexFiles(t, inc)["records.bin"]) {
				t.Fatalf("the pass did not take the path under test (append %v)", tc.append)
			}
			if got := indexFilesHolding(t, inc, word); len(got) > 0 {
				t.Errorf("the removed word is still in %v", got)
			}
		})
	}
}

// A word that another session still holds stays, as that session's.
func TestANeighbourAnotherSessionHoldsStaysInTheMap(t *testing.T) {
	const word = "zzquokkamarmot"
	inc, _ := neighbourStore(t, word, false)
	m, err := readManifest(inc)
	if err != nil {
		t.Fatal(err)
	}
	// One of the four loses the word; three still hold it.
	proj := filepath.Join(filepath.Dir(inc), "claude", "-tmp-proj")
	p := filepath.Join(proj, "s000.jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	parityWrite(t, p, strings.ReplaceAll(string(b), word+" ", ""), false)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(p, later, later)
	parityPass(t, inc, false)
	if after, _ := readManifest(inc); len(after.DeadRecords) == len(m.DeadRecords) {
		t.Fatal("the pass killed no record, so the scrub is not under test")
	}
	if !slices.Contains(readCooccur(inc)["pelicanrotor"], word) {
		t.Errorf("a word three sessions still hold left the map: %v", readCooccur(inc)["pelicanrotor"])
	}
}

// A word a replaced session held only in an output's middle stays while other
// sessions' prompts still hold it: the postings are asked, not assumed.
func TestAnUnpostedWordOthersStillHoldStaysInTheMap(t *testing.T) {
	for _, appends := range []bool{true, false} {
		t.Run(fmt.Sprintf("append %v", appends), func(t *testing.T) {
			if !appends {
				appendReplacing = false
				defer func() { appendReplacing = true }()
			}
			const word = "zzquokkamarmot"
			inc, _ := neighbourStore(t, word, false)
			p := filepath.Join(filepath.Dir(inc), "claude", "-tmp-proj", "x999.jsonl")
			body := func(with bool) string {
				out := strings.Repeat("compiling package ok\n", 600)
				if with {
					out += "linking " + word + " ok\n"
				}
				out += strings.Repeat("compiling package ok\n", 600)
				return parityClaudeLine("x999", "00:00Z", "user", `"unrelated prompt here"`) +
					parityClaudeLine("x999", "01:01Z", "assistant", `[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make build"}}]`) +
					parityClaudeLine("x999", "01:02Z", "user", `[{"type":"tool_result","tool_use_id":"t1","content":`+fmt.Sprintf("%q", out)+`}]`)
			}
			parityWrite(t, p, body(true), false)
			parityPass(t, inc, false)
			parityWrite(t, p, body(false), false)
			later := time.Now().Add(3 * time.Second)
			_ = os.Chtimes(p, later, later)
			parityPass(t, inc, false)
			if !slices.Contains(readCooccur(inc)["pelicanrotor"], word) {
				t.Errorf("a word four prompts still hold left the map: %v", readCooccur(inc)["pelicanrotor"])
			}
		})
	}
}

// The co-occurrence map holds the link a reworded question needs, and the tier
// that reads it required the whole AND to match after one substitution — so it
// could only ever fire on a query short enough not to need it. A sentence,
// which is what an agent and a person both type, never matched (#2331).
func TestTheNeighbourRescueFiresOnASentence(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 64)
	// Ordinary work, so the question's ordinary words are not rare by accident.
	for i := range 60 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("noise%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("the go service on shard %d was restarted after the weekly deploy", i)}},
		})
	}
	// The sentence that ties the reader's word to the project's, said in three
	// sessions, which is the evidence the map asks for.
	for i := range 3 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("explains%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: "pgbouncer is the connection proxy we put in front of postgres"}},
		})
	}
	// What the question is really after, in the project's own word.
	sessions = append(sessions, model.Session{
		ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user",
			Text: "the pgbouncer pool ran out of connections and the go service could not open one"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}

	// A sentence, not two words: the question the issue was written about.
	// Asked of the tier itself, because the ranked path finds the session by
	// other means and would hide whether this rung fired at all.
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	o := query.Options{Query: "why would a go service exhaust its postgres pool behind a connection proxy", All: true}
	res, err := cooccurNarrowedSearch(dir, m, o)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		ids = append(ids, s.ID)
	}
	if !strings.Contains(strings.Join(ids, " "), "answer") {
		t.Errorf("the neighbour rescue did not fire on a sentence: tier=%q %v", res.Tier, ids)
	}
	if res.Variants["proxy"] == nil {
		t.Errorf("the swap is not narrated: %v", res.Variants)
	}
}

// The narrowing takes the words that identify something, not the first three
// it sees: a question is mostly ordinary words and they are what the AND was
// failing on.
func TestTheNarrowedAndKeepsOnlyIdentifyingWords(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 61)
	for i := range 60 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("noise%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("the service was restarted after the deploy on shard %d", i)}},
		})
	}
	sessions = append(sessions, model.Session{
		ID: "rare", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user", Text: "the zephyrine quarto ledger was rebuilt"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	m, err := readManifestCached(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := identifyingTerms(dir, m, []string{"the", "service", "was", "restarted", "zephyrine", "quarto"}, nil)
	for _, ordinary := range []string{"service", "restarted"} {
		for _, g := range got {
			if g == ordinary {
				t.Errorf("an ordinary word was kept: %v", got)
			}
		}
	}
	if len(got) == 0 {
		t.Fatalf("every word was dropped: %v", got)
	}
}

// And the rung is wired where it belongs: below the ranked tier, which answers
// better wherever it answers at all. Asked through the ladder, a sentence
// whose subject the project calls something else comes back with the session
// that settled it.
func TestTheLadderReachesTheNeighbourRescue(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	sessions := make([]model.Session, 0, 64)
	for i := range 60 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("noise%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: fmt.Sprintf("weekly deploy notes for shard %d, nothing unusual", i)}},
		})
	}
	for i := range 3 {
		sessions = append(sessions, model.Session{
			ID: fmt.Sprintf("explains%02d", i), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user",
				Text: "zephyrine is the quarto scheduler we run the nightly jobs on"}},
		})
	}
	sessions = append(sessions, model.Session{
		ID: "answer", Harness: "claude", Project: "p", Updated: time.Now(),
		Messages: []model.Message{{Role: "user",
			Text: "quarto was moved to the first of the month"}},
	})
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	res, err := SearchDetailed(dir, query.Options{
		Query: "when did we last change the zephyrine schedule", All: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(res.Sessions))
	for _, s := range res.Sessions {
		ids = append(ids, s.ID)
	}
	if !strings.Contains(strings.Join(ids, " "), "answer") {
		t.Errorf("the ladder never reached the rescue: tier=%q %v", res.Tier, ids)
	}
}

// referenceCooccur is the neighbour map computed the obvious way: every pair of
// every session's kept tokens in one map, no sharding. Slow and memory-hungry by
// design — it exists to say what the answer is.
func referenceCooccur(ss []model.Session) map[string][]string {
	if len(ss) < cooccurMinDF || len(ss) > cooccurMaxSessions {
		return nil
	}
	df := map[string]int{}
	perSession := make([][]tokMask, 0, len(ss))
	for _, s := range ss {
		seen := map[string]int{}
		var list []tokMask
		nMsgs := len(s.Messages)
		for mIdx, m := range s.Messages {
			bit := turnWindowBit(mIdx, nMsgs)
			for _, tok := range tokens(m.Text) {
				if len(tok) < 4 || query.IsStopWord(tok) {
					continue
				}
				if idx, ok := seen[tok]; ok {
					list[idx].mask |= bit
					continue
				}
				seen[tok] = len(list)
				list = append(list, tokMask{tok: tok, mask: bit})
			}
		}
		for _, tm := range list {
			df[tm.tok]++
		}
		perSession = append(perSession, list)
	}
	maxDF := max(len(ss)/4, 8)
	type pair struct{ a, b string }
	type pairCounts struct{ c, turnC int }
	pairs := map[pair]pairCounts{}
	for _, list := range perSession {
		var kept []tokMask
		for _, tm := range list {
			if df[tm.tok] >= cooccurMinDF && df[tm.tok] <= maxDF {
				kept = append(kept, tm)
			}
		}
		sort.Slice(kept, func(i, j int) bool {
			if df[kept[i].tok] == df[kept[j].tok] {
				return kept[i].tok < kept[j].tok
			}
			return df[kept[i].tok] < df[kept[j].tok]
		})
		if len(kept) > cooccurTokensPerSn {
			kept = kept[:cooccurTokensPerSn]
		}
		for i := range kept {
			for j := i + 1; j < len(kept); j++ {
				a, b := kept[i], kept[j]
				if a.tok > b.tok {
					a, b = b, a
				}
				pc := pairs[pair{a.tok, b.tok}]
				pc.c++
				if (a.mask & (b.mask | (b.mask << 1) | (b.mask >> 1))) != 0 {
					pc.turnC++
				}
				pairs[pair{a.tok, b.tok}] = pc
			}
		}
	}
	type nc struct {
		t     string
		score float64
		c     int
	}
	cand := map[string][]nc{}
	totalDocs := float64(len(ss))
	for p, pc := range pairs {
		if pc.c < cooccurMinPair {
			continue
		}
		npmi := pairNPMI(totalDocs, df[p.a], df[p.b], pc.c)
		if len(ss) >= 10 && npmi <= cooccurMinNPMI {
			continue
		}
		turnRatio := float64(pc.turnC) / float64(pc.c)
		support := float64(pc.c) / float64(pc.c+cooccurMinPair)
		baseScore := (npmi + cooccurTurnWeight*turnRatio) * support
		cand[p.a] = append(cand[p.a], nc{t: p.b, score: baseScore * float64(pc.c) / float64(df[p.a]), c: pc.c})
		cand[p.b] = append(cand[p.b], nc{t: p.a, score: baseScore * float64(pc.c) / float64(df[p.b]), c: pc.c})
	}
	out := map[string][]string{}
	for tok, list := range cand {
		sort.Slice(list, func(i, j int) bool {
			if math.Abs(list[i].score-list[j].score) > 1e-9 {
				return list[i].score > list[j].score
			}
			if list[i].c != list[j].c {
				return list[i].c > list[j].c
			}
			return list[i].t < list[j].t
		})
		if len(list) > cooccurNeighbors {
			list = list[:cooccurNeighbors]
		}
		names := make([]string, len(list))
		for i, e := range list {
			names[i] = e.t
		}
		out[tok] = names
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shardTestCorpus draws each session's tokens from a vocabulary far larger than
// any one session, so pairs land on both sides of cooccurMinPair rather than all
// surviving or all being dropped. Measured on 300 sessions: 13903 pairs seen
// once, 19302 twice (both dropped), 18074 exactly at the threshold of three, and
// 23475 above it.
func shardTestCorpus(sessions int) []model.Session {
	ss := make([]model.Session, 0, sessions)
	for i := range sessions {
		var b strings.Builder
		x := uint32(i*2654435761 + 999) //nolint:gosec // wraps on purpose, it seeds a hash
		for range 40 {
			x = x*1664525 + 1013904223
			fmt.Fprintf(&b, "token%05d ", x%400)
		}
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("s%04d", i), Project: "app",
			Messages: []model.Message{{Role: "user", Text: b.String()}},
		})
	}
	return ss
}

// Counting the pairs a shard at a time is an accounting change, not a change of
// answer: each pair lands in exactly one shard, and the neighbour lists are
// sorted by count then token, so which shard was walked first cannot show
// through (#1137).
func TestShardedCooccurMatchesTheObviousComputation(t *testing.T) {
	ss := shardTestCorpus(300)
	want := referenceCooccur(ss)
	if len(want) == 0 {
		t.Fatal("the corpus produced no neighbours, so the test compares nothing")
	}
	dir := t.TempDir()
	buildCooccur(dir, ss)
	got := readCooccur(dir)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sharded build differs from the obvious one: %d tokens vs %d", len(got), len(want))
		for tok, list := range want {
			if !reflect.DeepEqual(got[tok], list) {
				t.Errorf("  %q: got %v, want %v", tok, got[tok], list)
				break
			}
		}
	}
}

// Every shard count must give the same answer, which is what makes the constant
// free to tune.
func TestCooccurAnswerDoesNotDependOnShardCount(t *testing.T) {
	if cooccurShards < 2 {
		t.Fatal("sharding is off, so this proves nothing")
	}
	ss := shardTestCorpus(300)
	dir := t.TempDir()
	buildCooccur(dir, ss)
	got := readCooccur(dir)
	if want := referenceCooccur(ss); !reflect.DeepEqual(got, want) {
		t.Errorf("shard count %d changes the answer", cooccurShards)
	}
}

// Four sessions tie "login" to "jwks": the rescue should let a query that
// says login find the session that only says jwks.
func cooccurCorpus(t *testing.T) string {
	t.Helper()
	texts := []string{
		"login broken again, jwks cache stale after rotation",
		"login failure traced to jwks refresh timing",
		"users report login errors, jwks endpoint returned old keys",
		"jwks rotation deployed, monitoring for regressions",
		"docker build cache oom unrelated filler session",
		"another unrelated session about frontend styling",
	}
	return nlIndex(t, texts...)
}

func TestCooccurMapBuiltOnFullRebuild(t *testing.T) {
	dir := cooccurCorpus(t)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("cooccur map missing after full build")
	}
	found := false
	for _, n := range m["login"] {
		if n == "jwks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("login neighbors = %v, want jwks", m["login"])
	}
}

// The same connection string is pasted into five sessions. Its password is a
// secret redaction strips from the record log; it must not survive into
// cooccur.gob, which the builder reads straight off ss.Messages.
func TestCooccurNeverStoresARepeatedSecret(t *testing.T) {
	const secret = "supersecretpw"
	url := "postgres://admin:" + secret + "@db.example.com/prod"
	dir := nlIndex(t,
		"deploy pipeline uses "+url,
		"deploy pipeline again "+url,
		"deploy pipeline retry "+url,
		"deploy pipeline once more "+url,
		"deploy pipeline final "+url,
	)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("cooccur map missing; corpus did not build pairs")
	}
	if _, ok := m[secret]; ok {
		t.Fatalf("secret is a cooccur key: %v", m[secret])
	}
	for tok, ns := range m {
		for _, n := range ns {
			if n == secret {
				t.Fatalf("secret leaked as a neighbor of %q: %v", tok, ns)
			}
		}
	}
}

func TestCooccurRescueSwapsOneToken(t *testing.T) {
	dir := cooccurCorpus(t)
	// "login" never appears with "monitoring" in one session; the rescue may
	// swap login -> jwks and land on session d.
	result, err := SearchDetailed(dir, search.Options{Query: "login monitoring regressions", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("cooccur rescue did not fire")
	}
	if vs := result.Variants["login"]; len(vs) != 1 || vs[0] == "login" {
		t.Fatalf("swap not narrated: %v", result.Variants)
	}
}

func TestCooccurRescueStaysQuietWhenMapMissing(t *testing.T) {
	dir := nlIndex(t, "one lonely session about nothing in particular")
	_ = os.Remove(filepath.Join(dir, cooccurFile))
	result, err := SearchDetailed(dir, search.Options{Query: "absent tokens here", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 0 {
		t.Fatalf("rescue invented results: %+v", result.Sessions)
	}
}

func TestCooccurRanksByNPMIOverRawFrequency(t *testing.T) {
	// "oauth" (df=4) co-occurs with specific partner "pkce" (df=3, c=3) and
	// with noisy high-DF token "handler" (df=8, c=4). Under raw count,
	// "handler" (c=4) beat "pkce" (c=3). Under NPMI + turn-window coupling,
	// "pkce" has higher mutual information with "oauth" and must rank first.
	dir := nlIndex(t,
		"oauth handshake pkce verifier challenge handler",
		"oauth flow pkce code challenge handler",
		"oauth token pkce exchange handler",
		"oauth callback state mismatch handler",
		"http router middleware handler logging",
		"grpc interceptor handler panic recovery",
		"webhook signature verification handler",
		"metrics endpoint handler prometheus",
		"database connection pool timeout",
		"background worker queue retry",
		"tls certificate renewal acme",
		"static asset cache compression",
	)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("cooccur map missing")
	}
	ns := m["oauth"]
	if len(ns) == 0 {
		t.Fatalf("expected neighbors for oauth, got none")
	}
	if ns[0] != "pkce" {
		t.Fatalf("expected top NPMI neighbor of oauth to be pkce, got %v", ns)
	}
}

func TestAssociativeActivationBoostsCollocatedSession(t *testing.T) {
	// Session 0 and Sessions 1-3 all mention "login" and "errors" once.
	// Sessions 1-3 also carry "jwks", the corpus-learned NPMI collocation of
	// "login". For a 3-term relevance query ("login errors regressions") where
	// no session holds all 3 terms, 1-hop associative activation should rank a
	// collocated session (s1/s2/s3) above uncollocated s0.
	dir := nlIndex(t,
		"login errors button alignment tweak",
		"login errors broken again, jwks cache stale after rotation",
		"login errors failure traced to jwks refresh timing",
		"users report login errors, jwks endpoint returned old keys",
		"unrelated filler session about database regressions",
		"another filler session about CSS grid layout",
	)
	res, err := SearchDetailed(dir, search.Options{Query: "login errors regressions", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("expected search results for login errors regressions")
	}
	if res.Sessions[0].ID == "s0" {
		t.Fatalf("expected session with NPMI collocation (jwks) to outrank uncollocated s0, got %s first", res.Sessions[0].ID)
	}
}

func TestBM25FZonalWeightingPrefersSettledAndTouchedOverPassingMention(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	ss := []model.Session{
		{
			ID: "passing", Harness: "claude", Project: "work/app", Started: at, Updated: at.Add(time.Minute),
			Messages: []model.Message{
				{Role: "user", Text: "check the dashboard logs", Time: at},
				{Role: "assistant", Text: "saw quicksilver mentioned in a comment, ignoring it", Time: at.Add(time.Minute)},
			},
		},
		{
			ID: "settled", Harness: "claude", Project: "work/app", Started: at, Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "fix quicksilver retry storm", Time: at},
				{Role: "edit", Text: "internal/quicksilver/retry.go\ncap = 4", Time: at.Add(10 * time.Second)},
				{Role: "command", Text: "go test ./internal/quicksilver/... → exit 0", Time: at.Add(20 * time.Second)},
				{Role: "assistant", Text: "Updated `internal/quicksilver/retry.go` to cap quicksilver backoff at 4.", Time: at.Add(30 * time.Second)},
			},
		},
	}
	writeStore(t, dir, ss)
	got, _, _, _, err := ProjectRelevant(dir, []string{"work/app"}, []string{"quicksilver", "backoff"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "settled" {
		t.Fatalf("expected settled session with Touched+Settled+Title zones to rank first, got %+v", got)
	}
}

// remineCooccur rebuilds cooccur.gob and discourse.gob in dir directly from its
// committed records.bin log without re-parsing raw transcripts on disk.
func remineCooccur(dir string) {
	m, err := readManifest(dir)
	if err != nil {
		return
	}
	by := map[string]*model.Session{}
	var order []string
	if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok || foreignPath(r.SourcePath, meta) {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := sessionFromMeta(meta)
			s = &cp
			by[r.Key] = s
			order = append(order, r.Key)
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}); err != nil {
		return
	}
	ss := make([]model.Session, 0, len(order))
	for _, k := range order {
		ss = append(ss, *by[k])
	}
	buildCooccur(dir, ss)
	dropCooccurCache(dir)
}

func TestRemineCooccurRebuildsCooccurAndDiscourseFromRecords(t *testing.T) {
	dir := cooccurCorpus(t)
	_ = os.Remove(filepath.Join(dir, cooccurFile))
	remineCooccur(dir)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("expected remineCooccur to rebuild cooccur.gob from records.bin")
	}
	if _, leaked := m[cooccurDiscourseKey]; leaked {
		t.Fatal("cooccurDiscourseKey leaked into neighbor map")
	}
	found := false
	for _, n := range m["login"] {
		if n == "jwks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("remineCooccur login neighbors = %v, want jwks", m["login"])
	}
}

// The co-occurrence tier is the last thing tried before falling back to
// relevance: when a query's exact words find nothing, it substitutes a term
// for one that kept company with it in this corpus. It is the tier that
// answers "the thing next to the thing you remember".
func TestCooccurTierSubstitutesANeighbouringTerm(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(home, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	dir := filepath.Join(home, "idx")
	// "postgres" and "connection" keep company across the corpus; "psql"
	// never appears with "connection" at all.
	for i, text := range []string{
		"the postgres connection pool ran dry again",
		"postgres connection limits raised to 200",
		"restarted postgres and the connection settled",
		"psql prints the schema",
	} {
		writeLines(t, filepath.Join(claude, "project", "s"+string(rune('1'+i))+".jsonl"),
			claudeLine("s"+string(rune('1'+i)), "2026-01-0"+string(rune('1'+i))+"T00:01:00Z", text))
	}
	if err := Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	// A query needs at least two terms: one lucky word is not a co-occurrence.
	if got, searchErr := cooccurSearch(dir, m, query.Options{Query: "postgres", All: true}); searchErr != nil || len(got.Sessions) != 0 {
		t.Fatalf("single term produced %d sessions (%v)", len(got.Sessions), searchErr)
	}

	// Both words present is the exact tier's job, not this one's — but asking
	// here must not error, and must not invent sessions that lack the terms.
	got, err := cooccurSearch(dir, m, query.Options{Query: "postgres connection", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Sessions {
		var b strings.Builder
		for _, msg := range s.Messages {
			b.WriteString(strings.ToLower(msg.Text))
			b.WriteString(" ")
		}
		if !strings.Contains(b.String(), "postgres") && !strings.Contains(b.String(), "connection") {
			t.Fatalf("session %s has neither term: %q", s.ID, b.String())
		}
	}
}

// The co-occurrence pass used to hold every session's distinct tokens until
// the last session was tokenised: 1.4 GB of a 12k-session cold build's peak.
// Results now go to the consumer in order, and no more than the window are
// produced ahead of it.
func TestParallelInOrderHoldsNoMoreThanTheWindow(t *testing.T) {
	rerankWorkers = func() int { return 8 }
	t.Cleanup(func() { rerankWorkers = defaultRerankWorkers })

	const n, window = 500, 16
	var mu sync.Mutex
	consumed, ahead := 0, 0
	var got []int
	parallelInOrder(n, window, func(i int) int {
		mu.Lock()
		if d := i - consumed + 1; d > ahead {
			ahead = d
		}
		mu.Unlock()
		return i * 3
	}, func(i, v int) {
		if v != i*3 {
			t.Fatalf("result %d handed to index %d", v, i)
		}
		got = append(got, i)
		mu.Lock()
		consumed = i + 1
		mu.Unlock()
	})
	if len(got) != n {
		t.Fatalf("consumed %d of %d", len(got), n)
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("index %d consumed at position %d", v, i)
		}
	}
	if ahead > window {
		t.Fatalf("produced %d results ahead of the consumer, window is %d", ahead, window)
	}
}
