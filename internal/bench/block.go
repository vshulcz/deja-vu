package bench

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	// BlockChainCount is how many questions the corpus asks.
	BlockChainCount = 30
	// BlockPriorCount is how many sessions in the project discuss the subject.
	// Enough that the block has to choose one: with two or three, choosing
	// badly and choosing well both land the answer, which is how the context
	// bench's coverage column ended up unable to move (#2931).
	BlockPriorCount = 8
	// BlockSettledAt is which prior session settled the question. Not the last
	// one: "take the newest" would then score full marks without choosing.
	BlockSettledAt = 3
)

// BlockChain is one question, the sessions that discuss it, and the sentence
// that settled it. The sentence is the ground truth: a block is right when it
// carries that, not when it carries the question's words.
type BlockChain struct {
	ID       string
	Terms    []string
	Settled  string
	Sessions []model.Session
}

// BlockCorpus is the seeded corpus for the block benchmark.
type BlockCorpus struct {
	Chains []BlockChain
	Hash   string
}

// GenerateBlock builds chains where exactly one session settles the question
// and the rest discuss it without settling anything.
//
// Three things make the corpus discriminating, each answering a way a bench can
// fail to see its subject:
//
//   - The settled sentence is in an assistant turn, late in its session, after
//     the diagnosis. A block that quotes the opening of the right session gets
//     it wrong, which is the failure #2906 fixed.
//   - The session that settled it is not the newest, so recency alone scores no
//     better than chance.
//   - The other sessions say the subject's words at length. A block that ranks
//     on how often the words appear picks one of them.
func GenerateBlock(seed int64) BlockCorpus {
	rng := rand.New(rand.NewSource(seed))
	base := time.Date(2099, time.March, 2, 9, 0, 0, 0, time.UTC)
	chains := make([]BlockChain, 0, BlockChainCount)
	for i := 0; i < BlockChainCount; i++ {
		id := fmt.Sprintf("block-chain-%02d", i)
		project := fmt.Sprintf("block-project-%02d", i)
		subject := fmt.Sprintf("%s-subject", id)
		settled := fmt.Sprintf(
			"The fix was pinning the %s pool at %d connections, decided after the replica lag test.",
			subject, 20+i%9)
		chain := BlockChain{ID: id, Terms: []string{subject}, Settled: settled}
		for j := 0; j < BlockPriorCount; j++ {
			t := base.Add(time.Duration(i*24+j) * time.Hour)
			msgs := []model.Message{
				{Role: "user", Text: fmt.Sprintf("the %s keeps timing out, take %d", subject, j+1), Time: t},
			}
			// The sessions that settle nothing say the subject far more often
			// than the one that does. That is the case ranking cannot see on
			// its own — a session that mentioned the subject sixty times
			// outranks the one that answered it in a sentence — so a bench
			// where the settled session is also the top hit measures nothing.
			says := 9 + rng.Intn(4)
			if j == BlockSettledAt {
				says = 1
			}
			for k := 0; k < says; k++ {
				msgs = append(msgs,
					model.Message{
						Role: "assistant",
						Text: fmt.Sprintf("Looking at %s again. The trace shows the same wait on %s, so I am reading the pool metrics before changing anything.\n%s",
							subject, subject, fillerText(rng, "ran the reproduction and pasted the output")),
						Time: t.Add(time.Duration(2*k+1) * time.Minute),
					},
					model.Message{
						Role: "user",
						Text: fmt.Sprintf("and does %s look any different under load", subject),
						Time: t.Add(time.Duration(2*k+2) * time.Minute),
					})
			}
			if j == BlockSettledAt {
				// The answer sits in the middle of its session, after the
				// diagnosis and before more of it. Put last, "take the newest
				// turn" scores full marks and the bench measures recency; put
				// first, it never has to be found. Both are how a block can
				// look right while choosing nothing.
				// Two sentences of diagnosis before the one that concludes, so
				// a block that quotes a message's opening gets the diagnosis
				// and drops the answer — the failure #2906 fixed. A block that
				// takes the whole message passes either way, which is why the
				// conclusion cannot be the first sentence.
				msgs = append(msgs, model.Message{
					Role: "assistant",
					Text: fmt.Sprintf(
						"Read the replica lag over the whole window. Both pools showed the same wait, and the metrics agreed with the trace. %s",
						settled),
					Time: t.Add(30 * time.Minute),
				})
				// And something newer that settles nothing but is kept for its
				// code fence, competing for the slot the conclusion needs —
				// the failure #2913 fixed.
				// Two of them, because the block asks for two lines: one fence
				// leaves the second slot free and the conclusion still lands.
				for k := 0; k < 2; k++ {
					msgs = append(msgs, model.Message{
						Role: "assistant",
						Text: fmt.Sprintf("Here is the pool config as it stands, pass %d:\n```ini\n%s_pool_size = 40\nmax_client_conn = 200\n```\n%s",
							k+1, subject, fillerText(rng, "printed the effective config")),
						Time: t.Add(time.Duration(31+k) * time.Minute),
					})
				}
				for k := 0; k < 4; k++ {
					msgs = append(msgs,
						model.Message{
							Role: "user",
							Text: fmt.Sprintf("makes sense — anything else worth watching on %s", subject),
							Time: t.Add(time.Duration(31+2*k) * time.Minute),
						},
						model.Message{
							Role: "assistant",
							Text: fmt.Sprintf("Watching %s for another window, nothing new to report.\n%s",
								subject, fillerText(rng, "tailed the logs again")),
							Time: t.Add(time.Duration(32+2*k) * time.Minute),
						})
				}
			}
			msgs = append(msgs, model.Message{
				Role: "assistant",
				Text: fmt.Sprintf("Still reading %s. Nothing settled here — I will pick this up next session.", subject),
				Time: t.Add(time.Hour),
			})
			chain.Sessions = append(chain.Sessions, model.Session{
				ID: fmt.Sprintf("%s-session-%d", id, j), Harness: "claude", Project: project,
				Started: t, Updated: t.Add(time.Hour), Messages: msgs,
			})
		}
		chains = append(chains, chain)
	}
	return BlockCorpus{Chains: chains, Hash: blockHash(chains)}
}

// blockHash identifies the corpus a report was produced from, including the
// filler the seed varies — two runs that disagree on a number have to be able
// to say whether they disagreed about the corpus first.
func blockHash(chains []BlockChain) string {
	h := sha256.New()
	for _, c := range chains {
		fmt.Fprintf(h, "%s\n%s\n%s\n", c.ID, strings.Join(c.Terms, " "), c.Settled)
		for _, s := range c.Sessions {
			for _, m := range s.Messages {
				fmt.Fprintf(h, "%s\x00%s\n", m.Role, m.Text)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// SettledMarker is the part of the settled sentence a block has to carry for
// the answer to have arrived. The whole sentence would fail on a legitimate
// cut; this is the decision itself.
func (c BlockChain) SettledMarker() string {
	_, after, ok := strings.Cut(c.Settled, "The fix was ")
	if !ok {
		return c.Settled
	}
	if i := strings.Index(after, ", decided"); i > 0 {
		return after[:i]
	}
	return after
}
