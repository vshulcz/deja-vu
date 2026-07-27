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
func GeneratePrompt(seed int64) PromptCorpus {
	rng := rand.New(rand.NewSource(seed))
	topics := promptTopics()
	base := time.Date(2099, time.February, 1, 0, 0, 0, 0, time.UTC)
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
	b, _ := json.Marshal(chains)
	h := sha256.Sum256(b)
	return PromptCorpus{Chains: chains, Hash: hex.EncodeToString(h[:])}
}
