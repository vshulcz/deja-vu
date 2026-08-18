package index

import (
	"github.com/vshulcz/deja-vu/internal/cjkfold"
)

// CJK text carries no spaces, so the base tokenizer collapses whole phrases
// into one giant token and the exact tier never fires for it. The classic
// dictionary-free fix (Lucene's CJKAnalyzer family, proposed in #337) indexes
// overlapping bigrams inside every CJK run: 装订计数 -> 装订, 订计, 计数.
// A single-rune run keeps its unigram. Runs never cross a non-CJK boundary,
// so ASCII and Cyrillic paths are untouched, and bigrams are ordinary tokens
// downstream — postings, ranking and the ladder apply unchanged.

// expandCJKTokens maps each token to itself plus, for every CJK run of 2+
// runes inside it, that run's bigram set — the query-side mirror of the index
// emitter. A phrase query ANDs its bigrams (they are contiguous in the text
// that answers it); a sentence question's grammar bigrams cannot co-occur, so
// its AND simply finds nothing and the ladder falls through to the relevance
// tier, which is where a question belongs.
func expandCJKTokens(toks []string) []string {
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		runes := []rune(t)
		hasCJK := false
		allCJK := true
		for _, r := range runes {
			if cjkfold.IsCJK(r) {
				hasCJK = true
			} else {
				allCJK = false
			}
		}
		if !hasCJK {
			out = append(out, t)
			continue
		}
		if !allCJK {
			// A glued CJK+latin token keeps its whole form as a key: that is
			// exactly what the index emits for such text.
			out = append(out, t)
		}
		i := 0
		for i < len(runes) {
			if !cjkfold.IsCJK(runes[i]) {
				i++
				continue
			}
			j := i
			for j < len(runes) && cjkfold.IsCJK(runes[j]) {
				j++
			}
			out = append(out, cjkfold.Bigrams(string(runes[i:j]))...)
			i = j
		}
	}
	return out
}

// cjkFunctionRunes are Chinese particles, pronouns and question words: the
// closed class that carries no topic. A bigram made of two of them ("在哪",
// "什么", "怎么") is grammar, not content — it is the CJK counterpart of a
// stop word, and nothing else filters it because IsStopWord only knows
// Latin and Cyrillic.
//
// The test is deliberately "both runes", not "either": 中 and 个 are function
// runes on their own but carry meaning in 中国 and 个人, and dropping every
// bigram that merely contains one would delete real words. This filter runs
// at query time only — the index keeps every bigram, so nothing about
// ingestion changes.
//
// The list covers three scripts of the same closed class. Simplified Mandarin
// alone is not enough: a Traditional writer's 哪個 and a Cantonese writer's
// 我點 / 唔用 / 嘅索 are the same grammar, and without them a question ranks on
// its own wording instead of on the entity it asks about.
var cjkFunctionRunes = map[rune]bool{
	// Simplified Mandarin
	'的': true, '了': true, '是': true, '在': true, '和': true, '与': true,
	'或': true, '而': true, '但': true, '就': true, '都': true, '也': true,
	'还': true, '又': true, '再': true, '只': true, '才': true, '很': true,
	'我': true, '你': true, '他': true, '她': true, '它': true, '们': true,
	'这': true, '那': true, '些': true, '什': true, '么': true, '哪': true,
	'怎': true, '呢': true, '吗': true, '吧': true, '啊': true, '之': true,
	'其': true, '此': true, '所': true, '被': true, '把': true, '让': true,
	'给': true, '向': true, '于': true, '以': true, '从': true, '到': true,
	'由': true, '对': true, '因': true, '如': true, '若': true, '则': true,
	'并': true, '且': true, '为': true, '个': true, '有': true, '会': true,

	// Traditional forms of the entries above, for zh-TW / zh-HK writing
	'與': true, '還': true, '們': true, '這': true, '麼': true, '嗎': true,
	'讓': true, '給': true, '於': true, '從': true, '對': true, '則': true,
	'並': true, '為': true, '個': true, '會': true,

	// Cantonese closed class: 嘅=的 係=是 喺=在 唔=不 咩/乜=什麼 點=怎
	// 哋=們 嗰=那 啲=些 樣=樣(如「咁樣」) 冇=沒 嚟=來, plus the
	// sentence-final particles 咪 囉 喎 啦.
	'嘅': true, '係': true, '喺': true, '唔': true, '咩': true, '乜': true,
	'點': true, '哋': true, '嗰': true, '啲': true, '樣': true, '冇': true,
	'嚟': true, '咪': true, '囉': true, '喎': true, '啦': true, '咁': true,
}

// cjkFunctionBigram reports whether every rune of a CJK token is a function
// rune, i.e. the token is pure grammar.
func cjkFunctionBigram(tok string) bool {
	runes := []rune(tok)
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if !cjkfold.IsCJK(r) || !cjkFunctionRunes[r] {
			return false
		}
	}
	return true
}
