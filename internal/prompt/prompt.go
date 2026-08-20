// Package prompt turns what a person typed into the terms auto-recall searches
// on.
//
// It lives here rather than beside the hook because the benchmark that measures
// the hook is a separate command and could not call it. It was reimplementing
// the extraction instead — on relevance terms, with neither the identifier test
// nor the six-term cap — so the numbers it reported for the auto-recall gate
// were measured against a different rule from the one that runs.
package prompt

import (
	"strings"
	"unicode"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/query"
)

// relevanceTerms is the ranking's own tokenisation, used for the scripts that
// carry no ASCII identifier to find.
func relevanceTerms(q string) []string { return index.RelevanceTerms(q) }

// promptSearchTerms extracts the informative tokens from a natural-language
// prompt: stop words and short fragments dropped, capped so the query stays
// specific.
func Terms(prompt string) []string {
	fields := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		wordy := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r >= 0x400
		return !wordy
	})
	var out []string
	seen := map[string]bool{}
	add := func(f string) bool {
		if seen[f] {
			return false
		}
		seen[f] = true
		out = append(out, f)
		return len(out) == 6
	}
	for _, f := range fields {
		// A word at the end of a sentence arrives with its full stop attached,
		// and the dot then does two things at once: it marks the token as an
		// identifier, so "score." counts as one, and it stops the token from
		// ever matching the text, which says "score". Measured on live prompts:
		// "quality score. с чего начать" yielded the term "score.", which
		// matched nothing anywhere while still earning the recall its place.
		//
		// Only the edges are trimmed. A dot inside a token is what makes an IP
		// address, a version or a filename one word.
		f = strings.Trim(f, "._-/")
		if (len(f) < 3 && !shortSubject(f)) || query.IsStopWord(f) || seen[f] || !techTerm(f) {
			continue
		}
		if add(f) {
			return out
		}
	}
	// CJK carries no spaces, so FieldsFunc hands back a whole phrase as one
	// field, and techTerm rejects every rune above 127 — a Chinese, Japanese
	// or Korean prompt therefore yields no terms at all and auto-recall can
	// never fire for it. Fall back to the terms the relevance tier already
	// ranks on: per-run bigrams with pure-grammar pairs dropped. A bigram is
	// as specific as the identifiers techTerm looks for, and the caller still
	// demands two of them overlap before claiming a déjà vu.
	if hasCJKRune(prompt) {
		for _, t := range relevanceTerms(prompt) {
			if !hasCJKRune(t) {
				continue
			}
			if add(t) {
				break
			}
		}
	}
	// Cyrillic hits the same wall for the same reason: techTerm rejects every
	// rune above 127, so a Russian prompt without an ASCII identifier yields
	// nothing and auto-recall stays silent. Unlike CJK it is space-separated,
	// so the words are already there — they just need a length floor and the
	// closed class removed, and the index matches their inflected forms.
	for _, f := range fields {
		if seen[f] || !cyrPromptTerm(f) {
			continue
		}
		if add(f) {
			break
		}
	}
	return out
}

// cyrPromptTerm reports whether a field is a Cyrillic word specific enough to
// search on. Short words carry no signal and the closed class is noise, so
// both are dropped; what is left is roughly what techTerm keeps for ASCII.
func cyrPromptTerm(f string) bool {
	// A hyphen joins two words into one name as readily in Russian as in
	// English. Requiring every rune to be Cyrillic threw the whole compound
	// away, and the ASCII path would not take it either, so "коорд-сообщение"
	// could not become a search term at all — measured on a real store, one
	// direct question in seven that got no answer was exactly this.
	letters := 0
	for _, r := range f {
		if r == '-' {
			continue
		}
		if r < 0x400 || r > 0x4ff {
			return false
		}
		letters++
	}
	// Three, because Russian keeps short subjects — кеш, хук, бот, сеть, порт,
	// диск — and a length cannot tell them from про or вот. Measured over live
	// prompts, dropping the floor from five to three added nine distinct words
	// across forty-one questions, and six of the nine were closed-class words
	// missing from the list below; naming them is what makes the floor safe.
	if letters < 3 || cyrPromptStop[f] {
		return false
	}
	// A compound made only of closed-class words is still filler: "то-то-сё"
	// names nothing. One part that carries meaning is enough.
	for _, part := range strings.Split(f, "-") {
		if len([]rune(part)) >= 5 && !cyrPromptStop[part] {
			return true
		}
	}
	return !strings.Contains(f, "-")
}
func hasCJKRune(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// shortWord is the three-letter English words that are work, not subjects. A
// question is about "ttl" or "dns"; it is never about "log" on its own.
var shortWord = map[string]bool{
	"log": true, "run": true, "fix": true, "add": true, "set": true,
	"get": true, "put": true, "new": true, "old": true, "the": true,
	"and": true, "for": true, "but": true, "not": true, "all": true,
	"any": true, "one": true, "two": true, "out": true, "off": true,
	"top": true, "end": true, "use": true, "see": true, "way": true,
	"day": true, "now": true, "how": true, "why": true, "who": true,
	"its": true, "has": true, "was": true, "are": true, "can": true,
	"did": true, "let": true, "try": true, "job": true, "bug": true,
	"yes": true, "may": true, "our": true, "his": true, "her": true,
	"you": true, "too": true, "own": true, "per": true, "via": true,
}

// promptFiller is the short English filler a four-character floor lets
// through. search.IsStopWord is deliberately not extended: it governs search
// queries too, where "about" typed on purpose should still match.
var promptFiller = map[string]bool{
	"about": true, "again": true, "after": true, "before": true, "still": true,
	"there": true, "their": true, "these": true, "those": true, "which": true,
	"would": true, "could": true, "should": true, "thing": true, "things": true,
	"really": true, "maybe": true, "into": true, "from": true, "with": true,
	"that": true, "this": true, "what": true, "when": true, "were": true,
	"have": true, "does": true, "just": true, "like": true, "make": true,
	"made": true, "want": true, "need": true, "know": true, "tell": true,
	"show": true, "look": true, "some": true, "more": true, "most": true,
	"then": true, "than": true, "here": true, "your": true, "ours": true,
	"going": true, "doing": true, "being": true, "used": true, "using": true,
	"give": true, "take": true, "come": true, "seen": true, "said": true,
}

// techTerm keeps tokens that can actually identify past work: identifiers,
// error codes, paths, or long plain-ASCII words. Ordinary prose — any
// language — matches by theme, not by task, and theme matches are what made
// déjà vu fire on every prompt.
func techTerm(f string) bool {
	if promptFiller[f] {
		return false
	}
	if shortSubjectWord[f] {
		return true
	}
	long := 0
	for _, r := range f {
		if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
			return true
		}
		if r > 127 {
			return false
		}
		long++
	}
	// Seven was a proxy for "looks like an identifier" and the wrong one:
	// etag, ttl, mutex, gzip, oauth and most of what people actually type is
	// shorter. Measured on `deja bench prompt`, dropping to four takes real
	// questions answered from 2/12 to 7/12 with precision unchanged at 1.00
	// and no false fire on any negative control; three scores higher still
	// but starts keeping words like "log" and "run", so four is where the
	// evidence stops being comfortable.
	if long == 3 {
		// Three letters is where the acronyms live — ttl, dns, wal, api, ssh,
		// mcp, tls — and the reason the floor stood at four is that it is also
		// where "log" and "run" live. Naming those instead of measuring their
		// length keeps both: the list is closed-class English, the same kind
		// the Russian half of this file already carries.
		return !shortWord[f]
	}
	return long >= 4
}

// shortSubject keeps the two-character words that name a thing rather than
// describe one. The floor at three exists to stop "in", "on" and "to" from
// earning a recall, and it also drops the way people name versions and pull
// requests: measured on the questions the user actually typed, five of the nine
// that recalled nothing named their subject in two characters — "ну что там v3
// показал", "как там pr, смержился?" — leaving the question holding a working
// verb and nothing to search for.
func shortSubject(f string) bool {
	if len(f) != 2 {
		return false
	}
	// A version label: one letter and one digit.
	if f[0] >= 'a' && f[0] <= 'z' && f[1] >= '0' && f[1] <= '9' {
		return true
	}
	return shortSubjectWord[f]
}

// Closed class, the same shape as shortWord below it: named rather than
// measured, because two letters cannot be told apart by length.
var shortSubjectWord = map[string]bool{
	"pr": true, "mr": true, "ci": true, "db": true, "ui": true, "qa": true,
}

// The closed class plus the handful of verbs that open half of all questions.
var cyrPromptStop = map[string]bool{
	"который": true, "которая": true, "которые": true, "потому": true,
	"чтобы": true, "нужно": true, "надо": true, "можно": true, "нельзя": true,
	"когда": true, "почему": true, "зачем": true, "какой": true, "какая": true,
	"какие": true, "этот": true, "эта": true, "это": true, "тот": true,
	"такой": true, "также": true, "тоже": true, "очень": true, "просто": true,
	"сейчас": true, "сделать": true, "делать": true, "сделай": true,
	"работает": true, "работать": true, "показать": true, "посмотреть": true,
	"давай": true, "было": true, "были": true, "будет": true, "быть": true,
	"есть": true, "нет": true, "если": true, "или": true, "как": true,
	"что": true, "где": true, "там": true, "тут": true, "уже": true, "ещё": true,
	// Measured on live prompts from this machine: about a third of the terms
	// extracted from a Russian question were words like these, and in the worst
	// case four of five — `напомни, что мы уже выясняли про can шину на omoda
	// через adb` yielded [omoda напомни выясняли через дальше]. A filler term
	// is not harmless: it matches a filler line, and that line then takes the
	// slot the reader sees first.
	//
	// The English half of this list covers its own language well — the negative
	// controls built from "can you run the tests again" fire on nothing. These
	// are the Russian equivalents of exactly those words.
	"погоди": true, "подожди": true, "смотри": true, "слушай": true,
	"напомни": true, "покажи": true, "ответь": true, "скажи": true,
	"объясни": true, "проверь": true, "дальше": true, "через": true,
	"снова": true, "опять": true, "потом": true, "теперь": true,
	"вообще": true, "своего": true, "свои": true, "текущую": true,
	"текущий": true, "хочу": true, "можешь": true, "нашел": true,
	"нашёл": true, "выясняли": true, "разбирались": true,
	// The closed class a three-letter floor lets through. Measured rather than
	// guessed: these are the words that appeared when the floor came down over
	// live questions from this machine.
	"про": true, "вот": true, "раз": true, "еще": true, "чтоб": true,
	// "в итоге" is how a question asks for the outcome, not a thing to search
	// for; it also sits in the decision markers, so keeping it made a line
	// count as a conclusion because the asker had used the phrase.
	"итоге": true, "итог": true, "конце": true, "концов": true,
	"для": true, "при": true, "над": true, "под": true, "без": true,
	"она": true, "они": true, "оно": true, "был": true, "была": true,
	"мне": true, "нам": true, "вам": true,
	"его": true, "чем": true, "чём": true, "том": true, "тем": true,
	"так": true, "нас": true, "вас": true, "все": true, "всё": true,
	"два": true, "три": true, "оба": true, "иначе": true,
}

// Candidates is how many ranked sessions the hook asks for before
// filtering, against the two it will ever inject.
//
// It was eight, sized against two exclusions — the session being written and
// anything too fresh — and its comment said so. Three more arrived afterwards:
// the trust policy withholds a project, a weak match is dropped, and a session
// already injected in this conversation is skipped. Any of them can fill the
// window, and when they do the answer sitting below is never looked at and the
// hook says nothing at all.
//
// Already-injected is the one that makes this ordinary rather than contrived:
// it accumulates as a conversation goes on, so the failure arrives after a
// handful of prompts, on the sessions the user asks about most.
//
// The cost of a wider window is a longer list to walk, not a longer search: the
// ranking already scored every session in the project, and this only takes more
// of what it produced.
const Candidates = 32
