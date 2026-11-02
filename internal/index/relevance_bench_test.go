package index

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Russian corpus exercises the widest fold: every inflected form of a term
// shares its opening runes, so they all land in one bucket.
func benchRussianIndex(b *testing.B) string {
	b.Helper()
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	b.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	stems := []string{"миграц", "репликац", "конфигурац", "авторизац", "оптимизац", "документац", "интеграц", "валидац", "сериализац", "агрегац"}
	ends := []string{"ия", "ии", "ию", "ией", "иями", "иях"}
	other := []string{"сервер", "клиент", "запрос", "ответ", "ошибка", "таблица", "индекс", "кластер", "очередь", "кеш"}
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // a seeded fixture, not crypto
	for i := range 3000 {
		text := ""
		var textSb29 strings.Builder
		for range 40 {
			if rng.Intn(3) == 0 {
				textSb29.WriteString(stems[rng.Intn(len(stems))] + ends[rng.Intn(len(ends))] + " ")
			} else {
				textSb29.WriteString(other[rng.Intn(len(other))] + " ")
			}
		}
		text += textSb29.String()
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"%s"}}`, i, text) + "\n" //nolint:gocritic // JSON quoting, not Go's
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("s%d.jsonl", i)), []byte(line), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		b.Fatal(err)
	}
	return dir
}

func BenchmarkRussianRelevance(b *testing.B) {
	dir := benchRussianIndex(b)
	terms := []string{"миграция", "репликация", "конфигурация", "оптимизация"}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, _, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, terms, 10); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRelevanceWithWorkRecords measures ProjectRelevant on a corpus whose
// sessions hold the work and compaction records (files, command, edit, wrote,
// summary) that real coding sessions carry alongside speech.
func BenchmarkRelevanceWithWorkRecords(b *testing.B) {
	tmp := b.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	b.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	dir := filepath.Join(tmp, "index.db")
	b.Setenv("DEJA_INDEX_DIR", dir)
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatal(err)
	}
	topics := []string{"pgbouncer", "keepalive", "backpressure", "walreceiver", "checkpoint", "autovacuum", "logicalslot", "deadlock"}
	filler := strings.Repeat("func handleConn(ctx context.Context, pool *ConnPool) error { return pool.Acquire(ctx) }\n", 24)
	rng := rand.New(rand.NewSource(11)) //nolint:gosec // seeded benchmark fixture
	for i := range 400 {
		t1 := topics[rng.Intn(len(topics))]
		t2 := topics[rng.Intn(len(topics))]
		var sb strings.Builder
		fmt.Fprintf(&sb, `{"type":"user","sessionId":"w%d","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"investigate %s and %s latency spike under load"}}`+"\n", i, t1, t2)
		fmt.Fprintf(&sb, `{"type":"assistant","sessionId":"w%d","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[{"type":"text","text":"Checking %s pool saturation and %s timeout settings."},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go test -v ./internal/%s/... -run Test%s"}},{"type":"tool_use","id":"tu2","name":"Edit","input":{"file_path":"/tmp/app/%s.go","old_string":%q,"new_string":%q}},{"type":"tool_use","id":"tu3","name":"Write","input":{"file_path":"/tmp/app/%s_test.go","content":%q}}]}}`+"\n",
			i, t1, t2, t1, t2, t1, filler+"// "+t1, filler+"// "+t2, t1, filler)
		fmt.Fprintf(&sb, `{"type":"summary","sessionId":"w%d","timestamp":"2026-01-02T03:04:07Z","summary":"Session tuned %s and %s configuration parameters: %s"}`+"\n", i, t1, t2, filler)
		if err := os.WriteFile(filepath.Join(proj, fmt.Sprintf("w%d.jsonl", i)), []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	if err := Ensure(dir, "", true, nil); err != nil {
		b.Fatal(err)
	}
	terms := []string{"pgbouncer", "keepalive", "backpressure", "latency"}
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if _, _, _, _, err := ProjectRelevant(dir, []string{"tmp/app"}, terms, 10); err != nil {
			b.Fatal(err)
		}
	}
}
