//go:build realcorpus492

// Real-corpus instrumentation for upstream issue #492.
//
// Opt-in via the realcorpus492 build tag so a normal `go test ./...` never
// touches a live store. Everything here is read-only: it parses transcripts
// and runs the two key emitters against them. It writes exactly one file, the
// sampled message cache, and only to the path given on the command line.
//
// The pipeline reproduced here is the one a real build walks before
// indexKeys() sees a byte (ingest.go:404,448-468):
//
//	ParseClaudeFile -> preRedactSessions (stripSelfRecall -> NFC -> redact
//	  -> 64 KiB cap) -> skip messages that trim to empty
//	  -> tokenizedPart(role, text) -> indexKeys(text)
//
// Skipping any of those measures a different string than the index does. The
// cross-file duplicate filter (seenMsgs.dup) is the one step left out; it
// removes messages, it does not rewrite them.
package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/sources"
)

type sampleMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(t testing.TB, key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q: %v", key, v, err)
	}
	return n
}

// jsonlFiles lists every transcript under root, sorted, so two runs walk the
// corpus in the same order.
func jsonlFiles(t testing.TB, root string) []string {
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(p, ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// eachIndexedText replays the ingestion pipeline and hands fn the exact string
// indexKeys() would receive for every message the index would store.
func eachIndexedText(t testing.TB, root string, fn func(role, text string)) (files, sessions, messages int) {
	for _, p := range jsonlFiles(t, root) {
		ss, err := sources.ParseClaudeFile(p)
		if err != nil {
			continue
		}
		files++
		preRedactSessions(nil, ss)
		for si := range ss {
			sessions++
			for _, msg := range ss[si].Messages {
				if strings.TrimSpace(msg.Text) == "" {
					continue
				}
				messages++
				fn(msg.Role, tokenizedPart(msg.Role, msg.Text))
			}
		}
	}
	return files, sessions, messages
}

// cjkRunOps counts the emitFolded calls cjkIndexKeys makes before its seen-map
// check: one per adjacent pair inside a CJK run, one for a lone CJK rune. It is
// also the number of seen-map operations the legacy path performs, so it is the
// denominator repetition rate is measured against.
func cjkRunOps(s string) int {
	ops := 0
	runLen := 0
	for _, r := range s {
		if !cjkfold.IsCJK(r) {
			if runLen == 1 {
				ops++
			}
			runLen = 0
			continue
		}
		if runLen == 0 {
			runLen = 1
			continue
		}
		ops++
		runLen = 2
	}
	if runLen == 1 {
		ops++
	}
	return ops
}

func countCJKIndexKeys(s string) int {
	n := 0
	cjkIndexKeys(s, func(string) { n++ })
	return n
}

func distinct(keys []string) int {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return len(set)
}

// TestRealCorpusStats492 answers the repetition-rate question the thread could
// not reason its way to, and caches a deterministic message sample for the
// benchmarks below.
//
//	DEJA492_CLAUDE_ROOT=/path/to/claude-root \
//	DEJA492_SAMPLE_OUT=/path/sample.json \
//	go test -tags realcorpus492 -run TestRealCorpusStats492 -v -timeout 60m ./internal/index/
func TestRealCorpusStats492(t *testing.T) {
	root := os.Getenv("DEJA492_CLAUDE_ROOT")
	if root == "" {
		t.Skip("DEJA492_CLAUDE_ROOT unset")
	}
	sampleOut := os.Getenv("DEJA492_SAMPLE_OUT")
	sampleEvery := envInt(t, "DEJA492_SAMPLE_EVERY", 200)
	sampleCap := envInt(t, "DEJA492_SAMPLE_CAP", 4000)

	var (
		cjkMsgs      int
		runOps       int64
		newKeys      int64
		legacyRaw    int64 // len(cjkfold.Bigrams) — unique unfolded bigrams
		legacyFolded int64 // distinct folded keys the legacy path yields
		// allTokens sums distinct(indexKeys(text)) per message, which is a lower
		// bound on the bucket() calls a build makes and not the count itself:
		// eachIndexKey also hands bucket() the three dateTokens of every
		// timestamped message (ingest.go:944,1076), and those never reach this
		// counter. On a 365k-message store that is at most ~1.1M calls, ~2.6%,
		// and less wherever a year or an ISO date in the text already produced
		// the same key. Any decomposition checked against this number carries
		// that much slack by construction.
		allTokens  int64
		cjkChars   int64
		totalChars int64
	)
	var sampleAll, sampleCJK []sampleMsg
	seenMsg, seenCJK := 0, 0
	cjkEvery := envInt(t, "DEJA492_SAMPLE_EVERY_CJK", 100)

	files, sessions, messages := eachIndexedText(t, root, func(role, text string) {
		totalChars += int64(len([]rune(text)))
		allTokens += int64(distinct(indexKeys(text)))

		ops := cjkRunOps(text)
		if ops > 0 {
			cjkMsgs++
			runOps += int64(ops)
			newKeys += int64(countCJKIndexKeys(text))
			bg := cjkfold.Bigrams(text)
			legacyRaw += int64(len(bg))
			legacyFolded += int64(distinct(legacyCJKKeys(text)))
			cjkChars += int64(cjkfold.CountCJK(text))
		}

		// Two independent strides so neither sample is front-loaded: the
		// CJK one counts only CJK-bearing messages, which are a minority.
		seenMsg++
		if seenMsg%sampleEvery == 0 && len(sampleAll) < sampleCap {
			sampleAll = append(sampleAll, sampleMsg{Role: role, Text: text})
		}
		if ops > 0 {
			seenCJK++
			if seenCJK%cjkEvery == 0 && len(sampleCJK) < sampleCap {
				sampleCJK = append(sampleCJK, sampleMsg{Role: role, Text: text})
			}
		}
	})

	f := func(a, b int64) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	t.Logf("corpus            files=%d sessions=%d indexed_messages=%d", files, sessions, messages)
	t.Logf("cjk               messages_with_cjk=%d (%.1f%%) cjk_runes=%d of %d runes (%.1f%%)",
		cjkMsgs, 100*f(int64(cjkMsgs), int64(messages)), cjkChars, totalChars, 100*f(cjkChars, totalChars))
	t.Logf("emitter ops       run_ops=%d  new_unique_keys=%d  legacy_unique_bigrams=%d  legacy_distinct_folded=%d",
		runOps, newKeys, legacyRaw, legacyFolded)
	t.Logf("REPETITION RATE   run_ops/new_unique_keys      = %.3f", f(runOps, newKeys))
	t.Logf("REPETITION RATE   run_ops/legacy_unique_bigram = %.3f", f(runOps, legacyRaw))
	t.Logf("fold collapse     legacy_unfolded -> distinct_folded = %.4f", f(legacyFolded, legacyRaw))
	t.Logf("per message       run_ops=%.1f new_keys=%.1f (CJK messages only)", f(runOps, int64(cjkMsgs)), f(newKeys, int64(cjkMsgs)))
	t.Logf("bucket() calls    distinct_index_keys_total=%d  per_message=%.1f", allTokens, f(allTokens, int64(messages)))

	if sampleOut != "" {
		blob := map[string][]sampleMsg{"all": sampleAll, "cjk": sampleCJK}
		b, err := json.Marshal(blob)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sampleOut, b, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("sample written    %s  all=%d cjk=%d", sampleOut, len(sampleAll), len(sampleCJK))
	}
}

// TestFixtureStats492 prints the same repetition figures for the fixtures the
// PR benchmarked, so the real-corpus rate has something to be compared against.
func TestFixtureStats492(t *testing.T) {
	cases := []struct{ name, text string }{
		{"PR benchJAText", benchJAText},
	}
	if gen := os.Getenv("DEJA492_GEN_ROOT"); gen != "" {
		var ops, keys int64
		var n int
		eachIndexedText(t, gen, func(_, text string) {
			o := cjkRunOps(text)
			if o == 0 {
				return
			}
			n++
			ops += int64(o)
			keys += int64(countCJKIndexKeys(text))
		})
		if n > 0 {
			t.Logf("%-18s messages=%d run_ops/unique_keys=%.3f per_msg_ops=%.1f",
				"gen_corpus.py ja", n, float64(ops)/float64(keys), float64(ops)/float64(n))
		}
	}
	for _, c := range cases {
		ops := cjkRunOps(c.text)
		keys := countCJKIndexKeys(c.text)
		bg := len(cjkfold.Bigrams(c.text))
		t.Logf("%-18s run_ops=%d unique_keys=%d unfolded=%d  repetition=%.3f",
			c.name, ops, keys, bg, float64(ops)/float64(keys))
	}
}

var (
	loadedAll  []string
	loadedCJK  []string
	loadedToks []string
	sinkInt    int
	sinkStr    string
)

func loadSample(b *testing.B) {
	if loadedAll != nil {
		return
	}
	path := os.Getenv("DEJA492_SAMPLE_FILE")
	if path == "" {
		b.Skip("DEJA492_SAMPLE_FILE unset — run TestRealCorpusStats492 with DEJA492_SAMPLE_OUT first")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	var blob map[string][]sampleMsg
	if err := json.Unmarshal(raw, &blob); err != nil {
		b.Fatal(err)
	}
	for _, m := range blob["all"] {
		loadedAll = append(loadedAll, m.Text)
	}
	for _, m := range blob["cjk"] {
		loadedCJK = append(loadedCJK, m.Text)
	}
	if len(loadedAll) == 0 || len(loadedCJK) == 0 {
		b.Fatalf("empty sample: all=%d cjk=%d", len(loadedAll), len(loadedCJK))
	}
	// The bucket() token stream is what eachIndexKey hands it: every distinct
	// index key of a message, ASCII identifiers included, in emission order.
	for _, s := range loadedCJK {
		seen := map[string]bool{}
		for _, tok := range indexKeys(s) {
			if seen[tok] {
				continue
			}
			seen[tok] = true
			loadedToks = append(loadedToks, tok)
		}
		if len(loadedToks) > 200000 {
			break
		}
	}
}

// ns/op and allocs/op below are per message, the same unit the PR quoted.
func BenchmarkCJKKeysLegacyRealCJK(b *testing.B) {
	loadSample(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt += len(legacyCJKKeys(loadedCJK[i%len(loadedCJK)]))
	}
}

func BenchmarkCJKKeysNewRealCJK(b *testing.B) {
	loadSample(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		cjkIndexKeys(loadedCJK[i%len(loadedCJK)], func(string) { n++ })
		sinkInt += n
	}
}

func BenchmarkCJKKeysLegacyRealAll(b *testing.B) {
	loadSample(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt += len(legacyCJKKeys(loadedAll[i%len(loadedAll)]))
	}
}

func BenchmarkCJKKeysNewRealAll(b *testing.B) {
	loadSample(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		cjkIndexKeys(loadedAll[i%len(loadedAll)], func(string) { n++ })
		sinkInt += n
	}
}

// ns/op below is per bucket() call, over the real ASCII/CJK token mix.
func BenchmarkBucketLegacyReal(b *testing.B) {
	loadSample(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkStr = legacyBucket(loadedToks[i%len(loadedToks)])
	}
}

func BenchmarkBucketNewReal(b *testing.B) {
	loadSample(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkStr = bucket(loadedToks[i%len(loadedToks)])
	}
}

// TestRealCorpusEquivalence492 is the acceptance leg: on real text the two
// emitters must agree on the key set, not merely on speed.
func TestRealCorpusEquivalence492(t *testing.T) {
	path := os.Getenv("DEJA492_SAMPLE_FILE")
	if path == "" {
		t.Skip("DEJA492_SAMPLE_FILE unset")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var blob map[string][]sampleMsg
	if err := json.Unmarshal(raw, &blob); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, m := range append(append([]sampleMsg{}, blob["all"]...), blob["cjk"]...) {
		got := collectCJKIndexKeys492(m.Text)
		want := legacyCJKKeys(m.Text)
		assertSameKeySet492(t, envOr("DEJA492_LABEL", "real message"), got, want)
		checked++
	}
	t.Logf("key sets identical on %d real messages", checked)
}

// TestSampleRepresentativeness492 checks the one assumption that lets a
// per-message benchmark rate be multiplied by a corpus-wide message count: the
// sampled messages must carry the same bigram load as the population they were
// drawn from. It uses the real cjkfold.IsCJK, not a re-derived predicate.
func TestSampleRepresentativeness492(t *testing.T) {
	path := os.Getenv("DEJA492_SAMPLE_FILE")
	if path == "" {
		t.Skip("DEJA492_SAMPLE_FILE unset")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var blob map[string][]sampleMsg
	if err := json.Unmarshal(raw, &blob); err != nil {
		t.Fatal(err)
	}
	for _, which := range []string{"cjk", "all"} {
		var ops, keys, bucketCalls int64
		n := 0
		for _, m := range blob[which] {
			o := cjkRunOps(m.Text)
			if which == "cjk" && o == 0 {
				continue
			}
			n++
			ops += int64(o)
			keys += int64(countCJKIndexKeys(m.Text))
			bucketCalls += int64(distinct(indexKeys(m.Text)))
		}
		if n == 0 {
			continue
		}
		t.Logf("sample[%s] n=%d  mean_run_ops=%.1f  mean_cjk_keys=%.1f  mean_bucket_calls=%.1f  repetition=%.3f",
			which, n, float64(ops)/float64(n), float64(keys)/float64(n),
			float64(bucketCalls)/float64(n), float64(ops)/float64(keys))
	}
}
