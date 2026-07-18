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

const (
	Seed         int64 = 1
	SessionCount       = 500
	QueryCount         = 50
)

type Query struct {
	Text     string
	Relevant []string
}

type Corpus struct {
	Sessions []model.Session
	Queries  []Query
	Hash     string
}

type topic struct {
	name      string
	exact     string
	rephrased string
	typo      string
	phrase    string
	code      string
}

var topics = []topic{
	{name: "cache", exact: "cache invalidation", rephrased: "refreshing stale cache entries", typo: "invaldation", phrase: "cache invalidation race", code: "cache/refresh.go"},
	{name: "migration", exact: "idempotent migration", rephrased: "safe repeatable schema upgrade", typo: "idempotnt", phrase: "idempotent migration", code: "db/migrate.go"},
	{name: "pool", exact: "connection pool exhausted", rephrased: "database clients ran out", typo: "exhaustd", phrase: "connection pool exhausted", code: "db/pool.go"},
	{name: "oauth", exact: "refresh token rotation", rephrased: "renewing credentials after rotation", typo: "rotaton", phrase: "refresh token rotation", code: "auth/tokens.go"},
	{name: "queue", exact: "duplicate job delivery", rephrased: "the worker received one task twice", typo: "delivry", phrase: "duplicate job delivery", code: "jobs/worker.go"},
	{name: "parser", exact: "parser boundary error", rephrased: "the decoder crossed a record boundary", typo: "boundry", phrase: "parser boundary error", code: "internal/parse.go"},
	{name: "timeout", exact: "request deadline timeout", rephrased: "the upstream call exceeded its deadline", typo: "deadine", phrase: "request deadline timeout", code: "net/client.go"},
	{name: "locking", exact: "file lock contention", rephrased: "writers waited on the same lock", typo: "contetion", phrase: "file lock contention", code: "internal/lock.go"},
	{name: "redaction", exact: "credential redaction", rephrased: "secrets are removed before indexing", typo: "redacton", phrase: "credential redaction", code: "security/redact.go"},
	{name: "recovery", exact: "crash recovery replay", rephrased: "rebuilding after an interrupted write", typo: "replai", phrase: "crash recovery replay", code: "index/recover.go"},
}

// Generate is the benchmark fixture. Every value, including timestamps and
// IDs, comes from the fixed seed or the constants above.
func Generate(seed int64) Corpus {
	rng := rand.New(rand.NewSource(seed))
	base := time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC)
	sessions := make([]model.Session, 0, SessionCount)
	queries := make([]Query, 0, QueryCount)
	for i := 0; i < SessionCount; i++ {
		t := topics[i%len(topics)]
		id := fmt.Sprintf("session-%03d", i)
		project := fmt.Sprintf("project-%02d", i%25)
		text := fmt.Sprintf("routine %s session %d recorded a harmless status update", t.name, i)
		if i < QueryCount {
			variant := i % 5
			switch variant {
			case 0:
				text = fmt.Sprintf("decision: investigate %s; code reference %s", t.exact, t.code)
			case 1:
				text = fmt.Sprintf("error log: %s; decision: %s", t.exact, t.rephrased)
			case 2:
				text = fmt.Sprintf("error log mentions %s; the correct term is %s", t.typo, t.exact)
			case 3:
				text = fmt.Sprintf("decision recorded for %q in %s", t.phrase, t.code)
			case 4:
				text = fmt.Sprintf("code review for %s found the %s behavior", t.code, t.exact)
			}
			queries = append(queries, Query{Text: queryText(t, variant), Relevant: []string{id}})
		}
		noise := rng.Intn(1000)
		sessions = append(sessions, model.Session{
			ID: id, Harness: "claude", Project: project,
			Started: base.Add(time.Duration(i) * time.Minute),
			Updated: base.Add(time.Duration(i) * time.Minute),
			Messages: []model.Message{
				{Role: "user", Text: text, Time: base.Add(time.Duration(i) * time.Minute)},
				{Role: "assistant", Text: fmt.Sprintf("reviewed result %d and kept the change local", noise), Time: base.Add(time.Duration(i)*time.Minute + time.Minute)},
			},
		})
	}
	b, _ := json.Marshal(struct {
		Sessions []model.Session
		Queries  []Query
	}{sessions, queries})
	h := sha256.Sum256(b)
	return Corpus{Sessions: sessions, Queries: queries, Hash: hex.EncodeToString(h[:])}
}

func queryText(t topic, variant int) string {
	switch variant {
	case 0:
		return t.exact
	case 1:
		return t.rephrased
	case 2:
		return t.typo
	case 3:
		return fmt.Sprintf("%q", t.phrase)
	default:
		return t.code
	}
}
