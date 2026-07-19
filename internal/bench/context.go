package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	ContextChainCount    = 30
	ContextNegativeCount = 5
	ContextPriorCount    = 3
)

type ContextChain struct {
	ID       string
	Sessions []model.Session
	Task     string
	Terms    []string
	Facts    []string
	Negative bool
}

type ContextCorpus struct {
	Chains []ContextChain
	Hash   string
}

// GenerateContext creates chains whose fact text is the benchmark ground truth.
func GenerateContext(seed int64) ContextCorpus {
	rng := rand.New(rand.NewSource(seed))
	chains := make([]ContextChain, 0, ContextChainCount+ContextNegativeCount)
	base := time.Date(2099, time.January, 3, 0, 0, 0, 0, time.UTC)
	for i := 0; i < ContextChainCount+ContextNegativeCount; i++ {
		id := fmt.Sprintf("context-chain-%02d", i)
		project := fmt.Sprintf("context-project-%02d", i)
		chain := ContextChain{ID: id, Negative: i >= ContextChainCount}
		if chain.Negative {
			chain.Terms = []string{fmt.Sprintf("independent-task-%02d", i)}
			chain.Task = fmt.Sprintf("Review the independent %s task.", chain.Terms[0])
		} else {
			noise := rng.Intn(900) + 100
			chain.Facts = []string{
				fmt.Sprintf("%s fact: error fixed by replacing stale etag reuse with generation checks", id),
				fmt.Sprintf("%s fact: option chosen was bounded refresh with jitter because retries must spread load", id),
				fmt.Sprintf("%s fact: config value settled at context_ttl=%dm", id, 10+i%7),
			}
			chain.Task = fmt.Sprintf("Handle %s using the prior error, chosen option, and settled context_ttl value.", id)
			chain.Terms = []string{id}
			chain.Facts[0] += fmt.Sprintf("; incident %d", noise)
		}
		for j := 0; j < ContextPriorCount; j++ {
			text := "routine update with no prior fact"
			if !chain.Negative {
				text = chain.Facts[j]
			}
			t := base.Add(time.Duration(i*10+j) * time.Minute)
			chain.Sessions = append(chain.Sessions, model.Session{
				ID: fmt.Sprintf("%s-session-%d", id, j), Harness: "claude", Project: project,
				Started: t, Updated: t, Messages: []model.Message{
					{Role: "user", Text: text, Time: t},
					{Role: "assistant", Text: "Recorded the decision and verified the rollout." + strings.Repeat(" supporting detail", rng.Intn(20)), Time: t.Add(time.Minute)},
				},
			})
		}
		t := base.Add(time.Duration(i*10+ContextPriorCount) * time.Minute)
		chain.Sessions = append(chain.Sessions, model.Session{ID: id + "-task", Harness: "claude", Project: project, Started: t, Updated: t, Messages: []model.Message{{Role: "user", Text: chain.Task, Time: t}}})
		chains = append(chains, chain)
	}
	b, _ := json.Marshal(chains)
	h := sha256.Sum256(b)
	return ContextCorpus{Chains: chains, Hash: hex.EncodeToString(h[:])}
}
