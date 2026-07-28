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
	// wholesale, "fresh" for one worked on minutes ago.
	Kind string
}

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

// GeneratePrompt builds one chain per topic: three prior sessions carrying the
// fact under working noise, and no task session — the question comes from the
// caller, the way a prompt does.
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
