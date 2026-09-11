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

const Seed int64 = 1

const (
	SessionCount = 500
	// Five variants for each topic in the table, which is what makes every
	// query name one session (#3481).
	QueryCount = 100
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
	// ru marks a topic written the way this store is mostly written. The
	// occasions a query names are in the topic's own language; a question that
	// mixes two is nobody's question.
	ru bool
}

var topics = []topic{
	{name: "cache", exact: "cache invalidation", rephrased: "refreshing stale cache entries", typo: "invaldation", phrase: "cache invalidation race", code: "cache/refresh.go"},
	{name: "migration", exact: "idempotent migration", rephrased: "safe repeatable schema upgrade", typo: "idempotnt", phrase: "idempotent migration", code: "db/migrate.go"},
	{name: "pool", exact: "connection pool exhausted", rephrased: "database clients ran out", typo: "exhaustd", phrase: "connection pool exhausted", code: "db/pool.go"},
	{name: "oauth", exact: "refresh token rotation", rephrased: "renewing credentials after rotation", typo: "rotaton", phrase: "refresh token rotation", code: "auth/tokens.go"},
	{name: "queue", exact: "duplicate job delivery", rephrased: "the worker received one task twice", typo: "delivry", phrase: "duplicate job delivery", code: "jobs/worker.go"},
	{name: "parser", exact: "parser boundary error", rephrased: "the decoder crossed a record boundary", typo: "boundray", phrase: "parser boundary error", code: "internal/parse.go"},
	{name: "timeout", exact: "request deadline timeout", rephrased: "the upstream call exceeded its deadline", typo: "deadine", phrase: "request deadline timeout", code: "net/client.go"},
	{name: "locking", exact: "file lock contention", rephrased: "writers waited on the same lock", typo: "contetion", phrase: "file lock contention", code: "internal/lock.go"},
	{name: "redaction", exact: "credential redaction", rephrased: "secrets are removed before indexing", typo: "redacton", phrase: "credential redaction", code: "security/redact.go"},
	{name: "recovery", exact: "crash recovery replay", rephrased: "rebuilding after an interrupted write", typo: "replai", phrase: "crash recovery replay", code: "index/recover.go"},
	// The other half of the corpus, because the ranking it gates is used mostly
	// on stores that read like this one: #2734 found the store Russian-dominant
	// and changed the decision recogniser for it, while this bench stayed
	// English — so a ranking regression on Cyrillic passed every column at 1.00.
	// Invented subjects, not this machine's.
	{name: "кэш", exact: "инвалидация кэша", rephrased: "сброс устаревших записей кэша", typo: "инвалидацяи", phrase: "инвалидация кэша", code: "store/cache.go", ru: true},
	{name: "миграция", exact: "идемпотентная миграция", rephrased: "повторяемое обновление схемы", typo: "идемпотнетная", phrase: "идемпотентная миграция", code: "store/migrate.go", ru: true},
	{name: "пул", exact: "пул соединений исчерпан", rephrased: "клиенты базы закончились", typo: "исчерапн", phrase: "пул соединений исчерпан", code: "store/pool.go", ru: true},
	{name: "токен", exact: "ротация refresh-токена", rephrased: "обновление доступа после ротации", typo: "ротацяи", phrase: "ротация refresh-токена", code: "auth/refresh.go", ru: true},
	{name: "очередь", exact: "повторная доставка задачи", rephrased: "воркер получил одну задачу дважды", typo: "достаква", phrase: "повторная доставка задачи", code: "queue/worker.go", ru: true},
	{name: "парсер", exact: "ошибка на границе записи", rephrased: "декодер перешёл границу записи", typo: "граинце", phrase: "ошибка на границе записи", code: "parse/record.go", ru: true},
	{name: "таймаут", exact: "превышен дедлайн запроса", rephrased: "апстрим не ответил в срок", typo: "дедлйан", phrase: "превышен дедлайн запроса", code: "net/deadline.go", ru: true},
	{name: "блокировка", exact: "конкуренция за файловую блокировку", rephrased: "писатели ждали одну блокировку", typo: "конкуренцяи", phrase: "конкуренция за файловую блокировку", code: "lock/file.go", ru: true},
	{name: "редакция", exact: "вырезание секретов", rephrased: "секреты убираются до индексации", typo: "вырезаине", phrase: "вырезание секретов", code: "redact/secrets.go", ru: true},
	{name: "восстановление", exact: "проигрывание журнала после сбоя", rephrased: "пересборка после прерванной записи", typo: "проигрывнаие", phrase: "проигрывание журнала после сбоя", code: "recover/replay.go", ru: true},
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
			// One query, one session. The topic used to come from i%10 and the
			// variant from i%5, which makes the variant a function of the topic:
			// all five gold sessions of a topic got the same text, and the five
			// queries built from them were the same string with five different
			// "correct" answers. A perfect ranker scored recall@1 0.20 and MRR
			// 0.457 — (1 + 1/2 + 1/3 + 1/4 + 1/5)/5 to three places, which is
			// what the bench reported — so the headline ranking numbers measured
			// the tie and could not move. Ten topics and five variants make fifty
			// distinct pairs.
			t = topics[i/5]
			variant := i % 5
			// The occasion, which is what tells one session about a subject from
			// the next four. Without it the topic's phrase sat in four of the
			// five texts — "cache invalidation" is in the exact session, the
			// typo's correction, the quoted one and the code review — so the
			// exact-phrase query had five legitimate matches and the label named
			// one of them. That capped recall@1 at 0.74 after #3481 removed the
			// coarser tie, and the cap was invisible in the number.
			occasion := occasionsFor(t)[variant]
			switch variant {
			case 0:
				text = fmt.Sprintf("decision: investigate %s %s; code reference %s", t.exact, occasion, t.code)
			case 1:
				text = fmt.Sprintf("error log: %s %s; decision: %s", t.exact, occasion, t.rephrased)
			case 2:
				text = fmt.Sprintf("error log mentions %s %s; the correct term is %s", t.typo, occasion, t.exact)
			case 3:
				text = fmt.Sprintf("decision recorded for %q %s in %s", t.phrase, occasion, t.code)
			case 4:
				text = fmt.Sprintf("code review for %s %s found the %s behavior", t.code, occasion, t.exact)
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

// occasions are what a person adds when they actually ask: not "connection pool
// exhausted" but "connection pool exhausted under load". One per variant, shared
// across topics, so every query still competes with nine sessions that name the
// same occasion and ten that name the same subject.
var occasions = [5]string{
	"on cold start", "after a deploy", "under load",
	"in the nightly job", "on replica lag",
}

// ruOccasions are the same five, for the half of the table written in Russian.
var ruOccasions = [5]string{
	"на холодном старте", "после деплоя", "под нагрузкой",
	"в ночном прогоне", "на лаге реплики",
}

func occasionsFor(t topic) [5]string {
	if t.ru {
		return ruOccasions
	}
	return occasions
}

func queryText(t topic, variant int) string {
	switch variant {
	case 0:
		return t.exact + " " + occasionsFor(t)[0]
	case 1:
		return t.rephrased + " " + occasionsFor(t)[1]
	case 2:
		return t.typo + " " + occasionsFor(t)[2]
	case 3:
		return fmt.Sprintf("%q %s", t.phrase, occasionsFor(t)[3])
	default:
		return t.code + " " + occasionsFor(t)[4]
	}
}
