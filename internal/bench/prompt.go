package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The context corpus gives every chain the same vocabulary, which makes it
// useless for scoring per-prompt recall: a term that appears in all thirty
// chains identifies none of them, so the relevance pass counts zero matches
// no matter how the extractor behaves.
//
// This corpus gives each chain its own topic, and deliberately uses the kind
// of short identifiers people type — etag, ttl, mutex — because the question
// under test is what the extractor keeps.
type PromptChain struct {
	ID       string
	Project  string
	Topic    string
	Question string
	// Fact is what the session concluded, so an arm can ask whether the block
	// carries the conclusion rather than merely the subject: a passing mention
	// says the subject too.
	Fact string
	// Paraphrase is Question asked in someone else's words.
	Paraphrase string
	// Tied says this chain's store also holds the sentence that ties the
	// paraphrase's ordinary words to the term of art — the one every real
	// store has and this corpus did not. Measured: for all five paraphrases
	// the arm fails on, no message anywhere in the corpus says both, so no
	// lexical method can reach them and the arm's 15 of 20 is a ceiling
	// rather than a defect. A chain marked Tied is the reachable case (#2331).
	Tied     bool
	Sessions []model.Session
	Negative bool
	// Kind names what this chain is here to measure: "" for the plain case,
	// "marathon" for a chain whose sessions are long enough to be skipped
	// wholesale, "fresh" for one worked on minutes ago, "bucket" for filler
	// sharing a catch-all scope and "bucket-answer" for the question whose
	// answer sits outside that scope.
	Kind string
}

// promptBucketProject is the shared scope an agent gets when it is started
// from a directory that is not a project: everything launched from there lands
// in it, and the repository being worked on does not.
const promptBucketProject = "promptbenchbucket"

// PromptBucketProject is that scope, exported so the benchmark can ask its
// question against the catch-all rather than against the right answer.
const PromptBucketProject = promptBucketProject

// promptHaystackProject holds one very long session and the small ones that
// actually decided each question, so the benchmark can see which wins.
const promptHaystackProject = "promptbenchhaystack"

// promptBackgroundCount is how many ordinary sessions sit behind the cases, so
// that a word common in working talk is common here too.
const promptBackgroundCount = 300

// promptWorkingTalk is the vocabulary of ordinary work — the words that fill a
// long session without identifying anything in it. Sessions draw two each, so
// no single phrase becomes so common that it falls out of the query entirely.
var promptWorkingTalk = []string{
	"pushed the branch and ran the suite again",
	"the suite passed after the last fix",
	"rebased onto main and forced the check to rerun",
	"the build failed on the first attempt",
	"reverted that change and measured again",
	"opened a draft and left it for review",
	"the test was flaky so I ran it twice",
	"merged it once the checks went green",
	"looked at the diff and reduced the scope",
	"the numbers came out the same as before",
	"added a case that covers the empty input",
	"renamed it for consistency with the caller",
	"the linter complained about an unused value",
	"split the change into two smaller commits",
	"checked it on the other machine as well",
	"the output looked right on the second pass",
	"dropped the extra logging before pushing",
	"waited for the run and read the summary",
	"tried it locally and it behaved the same",
	"tagged the release after the last merge",
	// The negative controls are built from the words that live in every
	// session's working noise, so the noise here has to hold them too —
	// otherwise the benchmark calls "open the file and read it" a rare match
	// and measures the fixture instead of the product.
	"open the file and read it again",
	"paste the output of that command here",
	"walk me through the trace once more",
	"adjust the patch and try it again",
	"add a log line around that call",
	"write the code for it and run the tests",
}

// promptRussianProject holds every Russian chain, so they compete.
const promptRussianProject = "promptbenchru"

// PromptHaystackProject is that project, exported so the benchmark can ask it
// a question it never answered.
const PromptHaystackProject = promptHaystackProject

type PromptCorpus struct {
	Chains []PromptChain
	Hash   string
}

type promptTopic struct {
	word     string
	fact     string
	question string
	// paraphrase asks the same thing in words the fixture does not use. Every
	// other arm asks with the fixture's own wording, so none of them can see
	// the failure this exists for: a person coming back a month later and
	// asking about the same decision differently.
	paraphrase string
}

// Each topic is one short word plus the sentence a session would record and
// the question someone would ask about it months later.
// promptRussianTopics are the same shape as the English ones, with the filler
// a person actually types around the subject. The session they answer opens
// with filler too, so a filler term would match the wrong line as well as the
// wrong session.
func promptRussianTopics() []promptTopic {
	return []promptTopic{
		{"вебхук", "повторную доставку вебхука ограничили тремя попытками",
			"напомни, сколько попыток у повторной доставки вебхука, и что делать дальше",
			"сколько раз мы пробуем достучаться, если получатель не ответил"},
		{"шардирование", "шардирование по клиенту заменили на шардирование по региону",
			"погоди, а по чему у нас в итоге шардирование, покажи ещё раз",
			"по какому признаку данные разложены между узлами"},
		{"индексация", "индексацию перенесли в фоновый воркер после записи",
			"подожди, где именно теперь происходит индексация, объясни снова",
			"в какой момент строится поисковый указатель после сохранения"},
		// A compound written with a hyphen, which is how people name things in
		// Russian as readily as in English. Measured on a real store, one direct
		// question in seven that got no answer at all was this: the subject word
		// never became a search term, so nothing could match it.
		{"коорд-сообщение", "коорд-сообщение теперь шлём одним пакетом на всю группу",
			"напомни, что мы решали про коорд-сообщение",
			"как теперь рассылаем оповещение всей группе"},
		// Four letters, which is where Russian keeps its short subjects — сеть,
		// порт, диск, кеш. The floor for Cyrillic stands at five, so none of
		// them can become a search term at all, the same way ttl and dns could
		// not before the English floor was named rather than measured.
		{"кеш", "кеш инвалидируем по версии схемы, а не по времени",
			"напомни, что там было с кеш",
			"по какому признаку мы сбрасываем сохранённые данные"},
	}
}

func promptTopics() []promptTopic {
	return []promptTopic{
		{"etag", "the stale etag reuse was replaced with generation checks", "why did we replace the stale etag reuse?", "why did we stop reusing the cached validator header"},
		{"ttl", "the ttl for cached tokens was settled at 14 minutes", "what ttl did we settle on?", "how long do cached tokens stay valid now"},
		{"jitter", "retry backoff got jitter so retries stop arriving together", "did we add jitter to the backoff?", "how did we stop retries from arriving all at once"},
		{"mutex", "the mutex around the writer was replaced with a channel", "what did we do about the writer mutex?", "what replaced the lock around the writer"},
		{"quota", "the upload quota check moved before the multipart parse", "where does the quota check happen now?", "at what point is the upload size limit checked"},
		{"gzip", "gzip was disabled for streaming responses because it buffered", "why is gzip off for streaming?", "why is compression off for streamed responses"},
		{"utf8", "invalid utf8 in filenames is now replaced rather than rejected", "how do we handle invalid utf8 in names?", "what happens to filenames with broken encoding"},
		{"cron", "the nightly cron moved to 03:17 to miss the backup window", "when does the nightly cron run?", "what time does the nightly job start"},
		{"oauth", "oauth refresh tokens are rotated on every use", "are oauth refresh tokens rotated?", "do we issue a new refresh token each time"},
		{"dns", "dns lookups are cached for 30 seconds inside the pool", "how long do we cache dns lookups?", "for how long is a name lookup kept in the pool"},
		{"panic", "a panic in the parser is recovered and logged as a corrupt file", "what happens when the parser panics?", "what does the parser do when it crashes on a file"},
		{"wal", "wal mode is enabled so readers stop blocking the writer", "why did we turn on wal mode?", "why did readers stop blocking the writer"},
		// Asked with nothing but the subject and filler around it. The other
		// three-letter topics here are asked in sentences carrying words the
		// answering session also uses — "settle", "mode" — so they pass on
		// those and the floor never shows. Measured: "what ttl did we settle
		// on" reduces to [settle], and the ttl case is answered by a word that
		// has nothing to do with ttl.
		{"tls", "tls verification stays on for the internal mesh", "напомни, что там было с tls", "верификация сертификатов во внутренней сети включена"},
	}
}

const PromptNegativeCount = 3

// promptCorpusHash fingerprints what the corpus asks, not when it was built.
// The fresh chain is dated relative to now by design, so hashing the sessions
// wholesale gave a different hash on every run — and a hash that changes
// without the inputs changing cannot be used to compare two runs, which is
// the only reason it is reported.
func promptCorpusHash(chains []PromptChain) string {
	type stable struct {
		ID, Project, Topic, Question, Kind string
		Negative                           bool
		Texts                              []string
	}
	out := make([]stable, 0, len(chains))
	for _, c := range chains {
		s := stable{ID: c.ID, Project: c.Project, Topic: c.Topic, Question: c.Question, Kind: c.Kind, Negative: c.Negative}
		for _, sess := range c.Sessions {
			for _, m := range sess.Messages {
				s.Texts = append(s.Texts, m.Role+":"+m.Text)
			}
		}
		out = append(out, s)
	}
	b, _ := json.Marshal(out)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// cooccurTieCopies is how many sessions repeat the tying sentence. The map
// links two words only once they have shared three sessions.
const cooccurTieCopies = 3

// PromptTiedCount is how many tied chains the corpus carries.
const PromptTiedCount = 6

// tiedTopics are the tied arm's own subjects. Separate from the plain topics
// on purpose: sharing them would let a plain chain's sessions answer the tied
// paraphrase, and the arm would measure the corpus rather than the bridge.
func tiedTopics() []tiedTopic {
	return []tiedTopic{
		{promptTopic{"quorum", "quorum reads were turned off for the status endpoint", "what did we do about quorum reads?", "why does the status endpoint no longer ask every replica"},
			"the quorum read is the one that asks every replica"},
		{promptTopic{"debounce", "the debounce on the search box was set to 250ms", "what debounce did we settle on?", "how long do we wait before sending what someone typed"},
			"the debounce is how long we wait before sending"},
		{promptTopic{"replay", "a replay guard was added to the payment retry path", "what did we add to the payment retry path?", "what stops a retried charge from taking the money twice"},
			"the replay guard is what stops a retried charge"},
		{promptTopic{"vacuum", "autovacuum was tuned per table for the events table", "what did we change about vacuum?", "how did we stop dead rows piling up in the biggest table"},
			"vacuum is what clears the dead rows piling up in a table"},
		{promptTopic{"sharding", "sharding was keyed on tenant rather than on user", "what is the shard key now?", "by what are the rows split across the machines"},
			"sharding is how the rows are split across machines"},
		{promptTopic{"watermark", "a watermark was added so the queue drops nothing", "what did we add so the queue stops dropping?", "how did we stop the queue from throwing work away"},
			"the watermark is what keeps the queue from throwing work away"},
	}
}

// tiedTopic is a subject plus the sentence a team writes when it explains its
// own word in passing. Not a restatement of the question: a session repeating
// the question verbatim is the best lexical match there can be, and an arm it
// always wins measures the fixture rather than the search.
type tiedTopic struct {
	promptTopic
	tie string
}

// tiedChains repeat the plain shape with one addition: a short session where
// somebody writes the sentence that names the thing and describes it in the
// same breath. That is what a real store holds — nobody writes only the term
// of art or only the ordinary words — and it is the material the
// co-occurrence map is built from. Without it the map has nothing to learn
// and a question in other words cannot be bridged by anything lexical.
func tiedChains(rng *rand.Rand, base time.Time) []PromptChain {
	topics := tiedTopics()
	out := make([]PromptChain, 0, PromptTiedCount)
	for i := 0; i < PromptTiedCount && i < len(topics); i++ {
		topic := topics[i]
		chain := PromptChain{
			ID:         fmt.Sprintf("prompt-tied-%02d", i),
			Project:    fmt.Sprintf("promptbenchtied%02d", i),
			Topic:      topic.word,
			Question:   topic.question,
			Paraphrase: topic.paraphrase,
			Tied:       true,
		}
		t := base.Add(time.Duration(500+i*10) * time.Minute)
		// The session that settled it, in the term's own words.
		chain.Sessions = append(chain.Sessions, model.Session{
			ID: chain.ID + "-answer", Harness: "claude", Project: chain.Project,
			Updated: t.Add(20 * time.Minute),
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("we decided %s", topic.fact), Time: t},
				{Role: "assistant", Text: fillerText(rng, "wrote it down and moved on"), Time: t.Add(time.Minute)},
			},
		})
		// The sentence that ties the two vocabularies, in a session of its own
		// — it explains, it does not decide, so an arm that asks for the
		// decision must not be satisfied by it.
		// Said in three sessions, which is the evidence the co-occurrence map
		// asks for by construction (cooccurMinPair, "a pattern, not a
		// one-off"). One sentence teaches it nothing, and a store where a
		// team's own vocabulary appears exactly once does not exist.
		tie := topic.tie
		for copyN := 1; copyN < cooccurTieCopies; copyN++ {
			chain.Sessions = append(chain.Sessions, model.Session{
				ID:      fmt.Sprintf("prompt-tievocab-%02d-%d", i, copyN),
				Harness: "claude", Project: chain.Project,
				Updated: t.Add(time.Duration(10+copyN) * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: tie, Time: t.Add(time.Duration(5+copyN) * time.Minute)},
					{Role: "assistant", Text: fillerText(rng, "same thing, different words"), Time: t.Add(time.Duration(6+copyN) * time.Minute)},
				},
			})
		}
		chain.Sessions = append(chain.Sessions, model.Session{
			// Deliberately outside the chain's id prefix: the probe counts a
			// hit only for the chain's own sessions, and a tie that explains
			// the words while settling nothing must not be able to pass the
			// arm on its own. It is there to be learned from, not returned.
			ID: fmt.Sprintf("prompt-tievocab-%02d", i), Harness: "claude", Project: chain.Project,
			Updated: t.Add(10 * time.Minute),
			Messages: []model.Message{
				{Role: "user", Text: tie, Time: t.Add(5 * time.Minute)},
				{Role: "assistant", Text: fillerText(rng, "noted, that is the same thing"), Time: t.Add(6 * time.Minute)},
			},
		})
		out = append(out, chain)
	}
	return out
}

// promptShapeChain builds one chain with a given session length and start
// time, so a gate can be measured instead of argued about.
func promptShapeChain(rng *rand.Rand, i int, kind string, topic promptTopic, start time.Time, turns int) PromptChain {
	chain := PromptChain{
		ID:         fmt.Sprintf("prompt-chain-%02d", i),
		Project:    fmt.Sprintf("promptbench%02d", i),
		Topic:      topic.word,
		Question:   topic.question,
		Paraphrase: topic.paraphrase,
		Kind:       kind,
	}
	msgs := []model.Message{{Role: "user", Text: fmt.Sprintf("we decided %s", topic.fact), Time: start}}
	for k := 0; k < turns; k++ {
		msgs = append(msgs,
			model.Message{Role: "user", Text: fillerText(rng, "started it over and watched what happened"), Time: start.Add(time.Duration(2*k+2) * time.Minute)},
			model.Message{Role: "assistant", Text: fillerText(rng, "followed it down and rewrote the middle part"), Time: start.Add(time.Duration(2*k+3) * time.Minute)},
		)
	}
	last := start.Add(time.Duration(2*turns+3) * time.Minute)
	if kind == "fresh" {
		last = time.Now().Add(-1 * time.Minute).UTC()
	}
	chain.Sessions = append(chain.Sessions, model.Session{
		ID: chain.ID + "-session-0", Harness: "claude", Project: chain.Project,
		Started: start, Updated: last, Messages: msgs,
	})
	return chain
}

// GeneratePrompt builds one chain per topic: three prior sessions carrying the
// fact under working noise, and no task session — the question comes from the
// caller, the way a prompt does.
func GeneratePrompt(seed int64) PromptCorpus {
	rng := rand.New(rand.NewSource(seed))
	topics := promptTopics()
	// A fixed date in the past: the freshness gate measures how long ago a
	// session was touched, and a corpus dated in the future reads as newer
	// than now and is withheld wholesale.
	base := time.Date(2024, time.May, 1, 0, 0, 0, 0, time.UTC)
	chains := make([]PromptChain, 0, len(topics)+PromptNegativeCount+PromptTiedCount)
	for i, topic := range topics {
		chain := PromptChain{
			ID:         fmt.Sprintf("prompt-chain-%02d", i),
			Project:    fmt.Sprintf("promptbench%02d", i),
			Topic:      topic.word,
			Question:   topic.question,
			Paraphrase: topic.paraphrase,
		}
		for j := 0; j < ContextPriorCount; j++ {
			t := base.Add(time.Duration(i*10+j) * time.Minute)
			msgs := []model.Message{{Role: "user", Text: fmt.Sprintf("we decided %s", topic.fact), Time: t}}
			for k := 0; k < 4+rng.Intn(6); k++ {
				msgs = append(msgs,
					model.Message{Role: "user", Text: fillerText(rng, "started it over and watched what happened"), Time: t.Add(time.Duration(2*k+2) * time.Minute)},
					model.Message{Role: "assistant", Text: fillerText(rng, "followed it down and rewrote the middle part"), Time: t.Add(time.Duration(2*k+3) * time.Minute)},
				)
			}
			chain.Sessions = append(chain.Sessions, model.Session{
				ID: fmt.Sprintf("%s-session-%d", chain.ID, j), Harness: "claude",
				Project: chain.Project, Started: t, Updated: t, Messages: msgs,
			})
		}
		chains = append(chains, chain)
	}
	// Two shapes the plain chains cannot express, and the two the gates
	// actually turn away: a session too long to be read as one episode, and
	// one worked on minutes ago.
	chains = append(chains, promptShapeChain(rng, len(topics), "marathon", promptTopic{
		"backpressure", "the queue got backpressure so producers block instead of dropping", "what did we do about queue backpressure?",
		"how did we stop the queue from throwing messages away",
	}, base, 400))
	chains = append(chains, promptShapeChain(rng, len(topics)+1, "fresh", promptTopic{
		"idempotency", "the import path became idempotent by keying on the source hash", "how did we make the import idempotent?",
		"what stops a repeated import from writing the same rows twice",
	}, time.Now().Add(-2*time.Minute).UTC(), 3))
	// The bucket case. An agent started from a parent directory — a home
	// directory, a workspace root — gets a project scope that holds everything
	// ever launched from there, and not the repository actually being worked
	// on. Measured on a real machine 2026-08-19: from /Users/shulcz the scope
	// was [shulcz, Users/shulcz], the answer lay in another project entirely,
	// and the hook injected an unrelated session from the bucket instead.
	//
	// The right answer is silence. A neighbour from the bucket is worse than
	// nothing: it spends tokens and teaches the agent to ignore the next one.
	// One of these shares ordinary words with the question below without
	// answering it. That is what the real miss looked like: not a session
	// about nothing, but a neighbour carrying two of the same common words,
	// which is enough to clear a bar meant for rare ones.
	for i, junk := range []promptTopic{
		{"tarpit", "the crawler tarpit was left in place deliberately", "", ""},
		{"statement", "the statement cache is disabled on the read mirror after those failures", "", ""},
		{"quotas", "the per-tenant quota was moved into the gateway", "", ""},
	} {
		id := fmt.Sprintf("prompt-bucket-%02d", i)
		t := base.Add(time.Duration(900+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: promptBucketProject, Kind: "bucket",
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: promptBucketProject,
				Started: t, Updated: t,
				Messages: []model.Message{{Role: "user", Text: "we decided " + junk.fact, Time: t}},
			}},
		})
	}
	// The question asked while scoped to that bucket. Its answer is in a
	// project of its own, which the bucket scope does not contain.
	answer := base.Add(1000 * time.Minute)
	chains = append(chains, PromptChain{
		ID: "prompt-bucket-answer", Project: "promptbenchelsewhere", Kind: "bucket-answer",
		Topic:    "pgbouncer",
		Question: "how did we deal with the pgbouncer prepared statement failures?",
		Sessions: []model.Session{{
			ID: "prompt-bucket-answer-session", Harness: "claude", Project: "promptbenchelsewhere",
			Started: answer, Updated: answer,
			Messages: []model.Message{{Role: "user", Text: "we decided prepared statements go behind pgbouncer in session mode", Time: answer}},
		}},
	})
	// Questions in Russian, asked the way they are actually typed: an
	// interjection, a request verb, and the subject somewhere in the middle.
	// They share one project so the filler-heavy openings of the others
	// compete, and they guard the filler list against being over-extended —
	// one subject word added by mistake and the question goes unanswered.
	for i, ru := range promptRussianTopics() {
		id := fmt.Sprintf("prompt-ru-%02d", i)
		// One project for all three, so the filler-heavy openings of the other
		// two compete: a filler term matches a filler line, and with nothing
		// to compete against it the right session wins anyway and the defect
		// stays invisible.
		project := promptRussianProject
		t := base.Add(time.Duration(2000+i*10) * time.Minute)
		msgs := []model.Message{
			{Role: "user", Text: "погоди, а что там дальше по задаче", Time: t},
			{Role: "assistant", Text: "смотрю, сейчас продолжу", Time: t.Add(time.Minute)},
		}
		for k := 0; k < 40; k++ {
			at := t.Add(time.Duration(k+2) * time.Minute)
			msgs = append(msgs,
				model.Message{Role: "user", Text: "давай дальше, покажи ещё раз", Time: at},
				model.Message{Role: "assistant", Text: "снова прогнал и поправил", Time: at.Add(time.Second)},
			)
		}
		// The subject is said in passing before anything is concluded about it.
		// The decision markers that separate the two are English only, and half
		// the sessions on a real store are not.
		// Three passing mentions and one conclusion, so that the block cannot
		// hold both kinds: the two slots go to whichever lines win, and every
		// way of choosing by where or how often the word fell hands them to the
		// mentions.
		for k := 0; k < 3; k++ {
			msgs = append(msgs, model.Message{
				Role: "assistant",
				Text: "потом посмотрю про " + ru.word + ", пока не трогаю",
				Time: t.Add(time.Duration(150+k) * time.Minute),
			})
		}
		msgs = append(msgs, model.Message{
			Role: "assistant", Text: "в итоге решили: " + ru.fact,
			Time: t.Add(200 * time.Minute),
		})
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "russian",
			Topic: ru.word, Question: ru.question, Fact: ru.fact, Paraphrase: ru.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(200 * time.Minute), Messages: msgs,
			}},
		})
	}
	// Two sessions hold the subject: one only ever mentioned it, the other
	// concluded something about it. Ranking counts term matches and how rare
	// they are, and is blind to which of the two settled anything — so the one
	// that merely talked about it can win on saying it more often.
	for i, c := range []promptTopic{
		{"nightjar", "nightjar batches are flushed every 30 seconds", "what did we decide about nightjar", ""},
		{"lodestone", "lodestone runs on the read replica now", "what did we decide about lodestone", ""},
	} {
		id := fmt.Sprintf("prompt-concluded-%02d", i)
		project := fmt.Sprintf("promptbenchconcluded%02d", i)
		t := base.Add(time.Duration(2700+i*10) * time.Minute)
		var chatter []model.Message
		for k := 0; k < 60; k++ {
			chatter = append(chatter, model.Message{
				Role: "assistant",
				Text: "смотрел " + c.word + " ещё раз, пока ничего не трогаю",
				Time: t.Add(time.Duration(k) * time.Minute),
			})
		}
		chains = append(chains, PromptChain{
			ID: id + "-talk", Project: project, Kind: "concluded-noise",
			Sessions: []model.Session{{
				ID: id + "-talk-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(6 * time.Minute), Messages: chatter,
			}},
		})
		answer := t.Add(-200 * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "concluded",
			Topic: c.word, Question: c.question, Fact: c.fact, Paraphrase: c.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: answer, Updated: answer,
				Messages: []model.Message{
					{Role: "assistant", Text: "в итоге решили: " + c.fact, Time: answer},
				},
			}},
		})
	}
	// The same shape in English: a session says its subject three times in
	// passing before it concludes anything. The decision arm was Russian only,
	// so the English markers — the older half of the list — had nothing
	// guarding them.
	for i, d := range []promptTopic{
		{"kingfisher", "kingfisher retries are capped at four", "what did we decide about kingfisher", ""},
		{"saltmarsh", "saltmarsh writes go to the replica", "what did we decide about saltmarsh", ""},
	} {
		id := fmt.Sprintf("prompt-decen-%02d", i)
		project := fmt.Sprintf("promptbenchdecen%02d", i)
		t := base.Add(time.Duration(2900+i*10) * time.Minute)
		msgs := make([]model.Message, 0, 4)
		for k := 0; k < 3; k++ {
			msgs = append(msgs, model.Message{
				Role: "assistant",
				Text: "will look at " + d.word + " later, not touching it yet",
				Time: t.Add(time.Duration(k) * time.Minute),
			})
		}
		msgs = append(msgs, model.Message{
			Role: "assistant", Text: "the fix: " + d.fact, Time: t.Add(10 * time.Minute),
		})
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "decision-en",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute), Messages: msgs,
			}},
		})
	}
	// The short subject. Measured on a real store over the questions the user
	// actually typed: five of the nine that recalled nothing named their
	// subject in two characters — "как там pr, смержился?", "ну что там v3
	// показал". The extractor's length floor drops those, the question is left
	// holding a working verb, and the gate then correctly refuses to answer on
	// one working word. Correct here means the session that concluded about the
	// short subject is found.
	for i, d := range []promptTopic{
		{"v3", "v3 of the exporter halved the scrape time", "ну что там v3 показал", ""},
		{"pr", "the pr went in after the flake was fixed", "как там pr, смержился?", ""},
	} {
		id := fmt.Sprintf("prompt-short-%02d", i)
		project := fmt.Sprintf("promptbenchshort%02d", i)
		t := base.Add(time.Duration(3100+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "short-subject",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: "смотрим " + d.word, Time: t},
					{Role: "assistant", Text: d.fact, Time: t.Add(5 * time.Minute)},
				},
			}},
		})
	}
	// The echo. A person repeats an instruction, and the session holding the
	// earlier copy of it wins the opening line — it carries every word of the
	// question, being the same sentence. Measured on a real store, 22 of 104
	// injected blocks opened on a near-copy of the message the agent was
	// reading at that moment.
	for i, d := range []promptTopic{
		{"cormorant", "the fix: cormorant retries are capped at four", "start the cormorant retry now", ""},
		{"kittiwake", "the fix: kittiwake writes go to the replica", "start the kittiwake write now", ""},
	} {
		id := fmt.Sprintf("prompt-echo-%02d", i)
		project := fmt.Sprintf("promptbenchecho%02d", i)
		t := base.Add(time.Duration(3300+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "echo-line",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: d.question, Time: t},
					{Role: "assistant", Text: d.fact, Time: t.Add(5 * time.Minute)},
				},
			}},
		})
	}
	// A compound name whose parts are short. The Cyrillic floor is three
	// letters for a single word — "кеш", "хук" — but a hyphenated word needed a
	// part of five, so "стоп-лист" and "прод-бд" were dropped entirely and the
	// question was left holding a verb. Measured on a real store, compound
	// subjects come back on topic 68% of the time against 100% for plain ones.
	for i, d := range []promptTopic{
		{"стоп-лист", "в итоге решили: стоп-лист держим в редисе", "что мы решали про стоп-лист", ""},
		{"прод-бд", "в итоге решили: прод-бд читаем только с реплики", "что мы решали про прод-бд", ""},
	} {
		id := fmt.Sprintf("prompt-compound-%02d", i)
		project := fmt.Sprintf("promptbenchcompound%02d", i)
		t := base.Add(time.Duration(3500+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "compound-subject",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: "смотрим " + d.word, Time: t},
					{Role: "assistant", Text: d.fact, Time: t.Add(5 * time.Minute)},
				},
			}},
		})
	}
	// Two chains for the end-to-end arm, which runs the hook itself.
	for i, d := range []promptTopic{
		{"ptarmigan", "the fix: ptarmigan retries are capped at four", "what did we decide about ptarmigan", ""},
		{"godwit", "в итоге решили: godwit пишет только в реплику", "что мы решали про godwit", ""},
	} {
		id := fmt.Sprintf("prompt-e2e-%02d", i)
		project := fmt.Sprintf("promptbenche2e%02d", i)
		t := base.Add(time.Duration(3700+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "hook-e2e",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: "смотрим " + d.word, Time: t},
					{Role: "assistant", Text: d.fact, Time: t.Add(5 * time.Minute)},
				},
			}},
		})
	}
	// Asked from inside the session that holds the answer: the hook must not
	// recall a session to itself, and with two cases the end-to-end arm could
	// not see that rule at all.
	for i, d := range []promptTopic{
		{"whimbrel", "the fix: whimbrel retries are capped at four", "what did we decide about whimbrel", ""},
	} {
		id := fmt.Sprintf("prompt-e2eself-%02d", i)
		project := fmt.Sprintf("promptbenche2eself%02d", i)
		t := base.Add(time.Duration(3900+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "hook-e2e-self",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: "смотрим " + d.word, Time: t},
					{Role: "assistant", Text: d.fact, Time: t.Add(5 * time.Minute)},
				},
			}},
		})
	}
	// The subject appears in another session only in what a tool printed, and
	// in the part of that output the index keeps: a failing job line, a
	// dependency bump. Nobody there said anything about it, so the answer is
	// silence. Measured on a real store, of the eight questions the hook still
	// answers with unrelated work, six are exactly this shape — every match in
	// the session it showed carried the role tool-output and no other.
	for i, d := range []promptTopic{
		{"v41", "[2026-08-16T20:43:05+0300] [MainThread] [W] [toil.leader] Job 'WDLStartJob' " +
			"kind-WDLStartJob/instance-z1bqhro3 v41 is completely failed",
			"ну что там v41 показал", ""},
	} {
		id := fmt.Sprintf("prompt-toolonly-%02d", i)
		project := fmt.Sprintf("promptbenchtoolonly%02d", i)
		t := base.Add(time.Duration(4300+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "tool-only",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: "\u043f\u0440\u043e\u0433\u043e\u043d\u0438 \u043f\u0430\u0439\u043f\u043b\u0430\u0439\u043d \u0435\u0449\u0451 \u0440\u0430\u0437", Time: t},
					{Role: "tool-output", Text: d.fact, Time: t.Add(time.Minute)},
					{Role: "tool-output", Text: "error: bump github.com/pion/stun/" + d.word +
						" from 3.1.1 to 3.1.5 failed", Time: t.Add(2 * time.Minute)},
					{Role: "assistant", Text: "\u043f\u0435\u0440\u0435\u0437\u0430\u043f\u0443\u0441\u0442\u0438\u043b, \u0434\u0430\u043b\u044c\u0448\u0435 \u0441\u043c\u043e\u0442\u0440\u044e \u043b\u043e\u0433\u0438", Time: t.Add(5 * time.Minute)},
				},
			}},
		})
	}
	// The conclusion and the passing mentions sit in ONE message, which is how
	// an agent actually writes: a paragraph that names the subject twice while
	// putting it off, then the line that settles it. The line is picked by how
	// many query words it holds and only then asked whether it concluded
	// anything, so the denser mention wins and the answer is never quoted.
	// Measured on a real store: 35 of 119 blocks quoted a weaker line than one
	// the same session held.
	for i, d := range []promptTopic{
		{"dunlin", "the fix: dunlin retries are capped at four",
			"what did we decide about dunlin",
			"how many times do we retry that job before giving up"},
	} {
		id := fmt.Sprintf("prompt-decinline-%02d", i)
		project := fmt.Sprintf("promptbenchdecinline%02d", i)
		t := base.Add(time.Duration(4700+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "decision-inline",
			Topic: d.word, Question: d.question, Fact: d.fact, Paraphrase: d.paraphrase,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(10 * time.Minute),
				Messages: []model.Message{
					{Role: "user", Text: "\u0447\u0442\u043e \u0441 " + d.word, Time: t},
					{Role: "assistant", Time: t.Add(5 * time.Minute), Text: "" +
						"looked at " + d.word + " and at the " + d.word + " retries, not touching either yet\n" +
						"still reading the " + d.word + " docs, will decide about " + d.word + " later\n" +
						d.fact},
				},
			}},
		})
	}
	// The decoy line. The session that answers also says an ordinary word the
	// question happens to use, in a place that has nothing to do with the
	// subject. Measured on a real store: "what did we decide about mm_status"
	// returned that session and quoted "1. **Decide**: User settings (global) or
	// project settings?" — the right session, the wrong line, because the
	// digest weighs every query word alike while the ranking knows which one
	// identified the match.
	for i, d := range []promptTopic{
		{"quicksilver", "quicksilver retries are capped at four", "what did we decide about quicksilver", ""},
		{"harbourmaster", "harbourmaster writes its log to stderr", "what did we decide about harbourmaster", ""},
	} {
		id := fmt.Sprintf("prompt-decoy-%02d", i)
		project := fmt.Sprintf("promptbenchdecoy%02d", i)
		t := base.Add(time.Duration(2500+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "decoy",
			Topic: d.word, Question: d.question,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(3 * time.Minute),
				Messages: []model.Message{
					// The ordinary word of the question, said three times, about
					// something else entirely.
					{Role: "user", Text: "decide whether to decide this now or decide it later", Time: t},
					{Role: "assistant", Text: "we decided " + d.fact, Time: t.Add(time.Minute)},
				},
			}},
		})
	}
	// The haystack. Measured on a frozen copy of a real index 2026-08-20: of 45
	// live prompts, 33 were answered by one session of 38131 messages and 11 by
	// one of 29190 — the two largest in the index, in order of size, for
	// questions with nothing in common. A session that touched everything
	// matches everything, and narrowing it to the part that matched leaves it
	// winning.
	//
	// The shape matters: a long session that mentions a topic once does not
	// compete, so an earlier fixture proved nothing. This one repeats each
	// topic the way a long night of work really does.
	hay := []promptTopic{
		{"kestrel", "the kestrel timeout was raised to ninety seconds", "why is the kestrel timeout ninety seconds?", ""},
		{"escrow", "escrow release waits on the second signature", "what does escrow release wait for?", ""},
		{"parquet", "parquet writes are batched per hour, not per row", "how often do we write parquet?", ""},
	}
	var noise []model.Message
	for k := 0; k < 200; k++ {
		t := base.Add(time.Duration(1200+k) * time.Minute)
		noise = append(noise,
			model.Message{Role: "user", Text: fillerText(rng, "another pass over the same branch"), Time: t},
			model.Message{Role: "assistant", Text: fillerText(rng, "tweaked it and ran the suite again"), Time: t.Add(time.Minute)},
		)
		// Every topic comes up again and again, which is what a long session
		// does and what makes it beat the session that actually decided it.
		for _, h := range hay {
			// In passing, the way a long session mentions everything: the
			// topic comes up, the decision does not. A message that carries
			// the fact too is not a haystack — it is a better answer, and an
			// earlier fixture made that mistake.
			noise = append(noise, model.Message{
				Role: "assistant",
				Text: "looked at " + h.word + " again on the way past",
				Time: t.Add(2 * time.Minute),
			})
		}
	}
	chains = append(chains, PromptChain{
		ID: "prompt-haystack-noise", Project: promptHaystackProject, Kind: "haystack-noise",
		Sessions: []model.Session{{
			ID: "prompt-haystack-noise-session", Harness: "claude", Project: promptHaystackProject,
			Started: base.Add(1200 * time.Minute), Updated: base.Add(1500 * time.Minute), Messages: noise,
		}},
	})
	for i, h := range hay {
		id := fmt.Sprintf("prompt-haystack-%02d", i)
		t := base.Add(time.Duration(1800+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: promptHaystackProject, Kind: "haystack",
			Topic: h.word, Question: h.question,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: promptHaystackProject,
				Started: t, Updated: t,
				Messages: []model.Message{{Role: "user", Text: "we decided " + h.fact, Time: t}},
			}},
		})
	}
	// Background: three hundred short sessions of ordinary working talk, in
	// their own projects, holding none of the topics above.
	//
	// They exist so that idf means something. Without them a word living in one
	// session of thirty scores 2.89 and a word living in ten scores 1.19, while
	// on a real index of fifteen hundred sessions the same words score 4.13 and
	// 0.4 — different scales, so a threshold measured here said nothing about
	// there. Measured on the small corpus: raising the informative floor from
	// 2.0 to 3.0 took this benchmark from 11/12 to 0/12 and changed nothing at
	// all on a real store.
	//
	// The vocabulary is the words a long working session is made of, drawn two
	// at a time from a pool, so that each lands in roughly one session in
	// twenty. That ratio is the point: on a real store "branch" sits in 94
	// sessions of about 1500 and scores 2.79, just over the floor meant for
	// words that identify something. A background that repeated one phrase in
	// every session put it in 89% of them instead, dropped it far under the
	// floor, and hid the very defect this corpus exists to reproduce.
	for i := 0; i < promptBackgroundCount; i++ {
		id := fmt.Sprintf("prompt-bg-%03d", i)
		t := base.Add(time.Duration(3000+i) * time.Minute)
		// Both strides are coprime with the pool length, so every phrase gets
		// used about equally. An earlier draft stepped by 13 over 26 phrases,
		// which reaches two of them and leaves the rest rare — and a word the
		// fixture happens to find rare is scored as one that identifies
		// something, which is the very thing being measured.
		a := promptWorkingTalk[(i*7)%len(promptWorkingTalk)]
		b := promptWorkingTalk[(i*11+5)%len(promptWorkingTalk)]
		var msgs []model.Message
		for k := 0; k < 6; k++ {
			at := t.Add(time.Duration(k) * time.Minute)
			msgs = append(msgs,
				model.Message{Role: "user", Text: fillerText(rng, a), Time: at},
				model.Message{Role: "assistant", Text: fillerText(rng, b), Time: at.Add(time.Second)},
			)
		}
		chains = append(chains, PromptChain{
			ID: id, Project: fmt.Sprintf("promptbenchbg%03d", i), Kind: "background",
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: fmt.Sprintf("promptbenchbg%03d", i),
				Started: t, Updated: t.Add(6 * time.Minute), Messages: msgs,
			}},
		})
	}
	// Negative controls share the corpus but hold none of its topics, so a
	// question about them must not recall anything.
	for i := 0; i < PromptNegativeCount; i++ {
		id := fmt.Sprintf("prompt-negative-%02d", i)
		t := base.Add(time.Duration(500+i*10) * time.Minute)
		chains = append(chains, PromptChain{
			ID: id, Project: fmt.Sprintf("promptbenchneg%02d", i), Negative: true,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: fmt.Sprintf("promptbenchneg%02d", i),
				Started: t, Updated: t,
				Messages: []model.Message{{Role: "user", Text: "unrelated maintenance with no durable decision", Time: t}},
			}},
		})
	}
	chains = append(chains, tiedChains(rng, base)...)
	return PromptCorpus{Chains: chains, Hash: promptCorpusHash(chains)}
}
