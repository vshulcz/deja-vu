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
			"напомни, сколько попыток у повторной доставки вебхука, и что делать дальше"},
		{"шардирование", "шардирование по клиенту заменили на шардирование по региону",
			"погоди, а по чему у нас в итоге шардирование, покажи ещё раз"},
		{"индексация", "индексацию перенесли в фоновый воркер после записи",
			"подожди, где именно теперь происходит индексация, объясни снова"},
	}
}

func promptTopics() []promptTopic {
	return []promptTopic{
		{"etag", "the stale etag reuse was replaced with generation checks", "why did we replace the stale etag reuse?"},
		{"ttl", "the ttl for cached tokens was settled at 14 minutes", "what ttl did we settle on?"},
		{"jitter", "retry backoff got jitter so retries stop arriving together", "did we add jitter to the backoff?"},
		{"mutex", "the mutex around the writer was replaced with a channel", "what did we do about the writer mutex?"},
		{"quota", "the upload quota check moved before the multipart parse", "where does the quota check happen now?"},
		{"gzip", "gzip was disabled for streaming responses because it buffered", "why is gzip off for streaming?"},
		{"utf8", "invalid utf8 in filenames is now replaced rather than rejected", "how do we handle invalid utf8 in names?"},
		{"cron", "the nightly cron moved to 03:17 to miss the backup window", "when does the nightly cron run?"},
		{"oauth", "oauth refresh tokens are rotated on every use", "are oauth refresh tokens rotated?"},
		{"dns", "dns lookups are cached for 30 seconds inside the pool", "how long do we cache dns lookups?"},
		{"panic", "a panic in the parser is recovered and logged as a corrupt file", "what happens when the parser panics?"},
		{"wal", "wal mode is enabled so readers stop blocking the writer", "why did we turn on wal mode?"},
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

// promptShapeChain builds one chain with a given session length and start
// time, so a gate can be measured instead of argued about.
func promptShapeChain(rng *rand.Rand, i int, kind string, topic promptTopic, start time.Time, turns int) PromptChain {
	chain := PromptChain{
		ID:       fmt.Sprintf("prompt-chain-%02d", i),
		Project:  fmt.Sprintf("promptbench%02d", i),
		Topic:    topic.word,
		Question: topic.question,
		Kind:     kind,
	}
	msgs := []model.Message{{Role: "user", Text: fmt.Sprintf("we decided %s", topic.fact), Time: start}}
	for k := 0; k < turns; k++ {
		msgs = append(msgs,
			model.Message{Role: "user", Text: fillerText(rng, "ran it again and pasted the output"), Time: start.Add(time.Duration(2*k+2) * time.Minute)},
			model.Message{Role: "assistant", Text: fillerText(rng, "walked the trace and adjusted the patch"), Time: start.Add(time.Duration(2*k+3) * time.Minute)},
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
	chains := make([]PromptChain, 0, len(topics)+PromptNegativeCount)
	for i, topic := range topics {
		chain := PromptChain{
			ID:       fmt.Sprintf("prompt-chain-%02d", i),
			Project:  fmt.Sprintf("promptbench%02d", i),
			Topic:    topic.word,
			Question: topic.question,
		}
		for j := 0; j < ContextPriorCount; j++ {
			t := base.Add(time.Duration(i*10+j) * time.Minute)
			msgs := []model.Message{{Role: "user", Text: fmt.Sprintf("we decided %s", topic.fact), Time: t}}
			for k := 0; k < 4+rng.Intn(6); k++ {
				msgs = append(msgs,
					model.Message{Role: "user", Text: fillerText(rng, "ran it again and pasted the output"), Time: t.Add(time.Duration(2*k+2) * time.Minute)},
					model.Message{Role: "assistant", Text: fillerText(rng, "walked the trace and adjusted the patch"), Time: t.Add(time.Duration(2*k+3) * time.Minute)},
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
	}, base, 400))
	chains = append(chains, promptShapeChain(rng, len(topics)+1, "fresh", promptTopic{
		"idempotency", "the import path became idempotent by keying on the source hash", "how did we make the import idempotent?",
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
		{"tarpit", "the crawler tarpit was left in place deliberately", ""},
		{"statement", "the statement cache is disabled on the read mirror after those failures", ""},
		{"quotas", "the per-tenant quota was moved into the gateway", ""},
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
		msgs = append(msgs, model.Message{
			Role: "assistant", Text: "решили: " + ru.fact,
			Time: t.Add(200 * time.Minute),
		})
		chains = append(chains, PromptChain{
			ID: id, Project: project, Kind: "russian",
			Topic: ru.word, Question: ru.question,
			Sessions: []model.Session{{
				ID: id + "-session", Harness: "claude", Project: project,
				Started: t, Updated: t.Add(200 * time.Minute), Messages: msgs,
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
		{"kestrel", "the kestrel timeout was raised to ninety seconds", "why is the kestrel timeout ninety seconds?"},
		{"escrow", "escrow release waits on the second signature", "what does escrow release wait for?"},
		{"parquet", "parquet writes are batched per hour, not per row", "how often do we write parquet?"},
	}
	var noise []model.Message
	for k := 0; k < 200; k++ {
		t := base.Add(time.Duration(1200+k) * time.Minute)
		noise = append(noise,
			model.Message{Role: "user", Text: fillerText(rng, "another pass over the same branch"), Time: t},
			model.Message{Role: "assistant", Text: fillerText(rng, "adjusted it and ran the suite again"), Time: t.Add(time.Minute)},
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
	return PromptCorpus{Chains: chains, Hash: promptCorpusHash(chains)}
}
