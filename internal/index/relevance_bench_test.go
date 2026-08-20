package index

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// A Russian corpus exercises the widest fold: every inflected form of a term
// shares its opening runes, so they all land in one bucket.
func benchRussianIndex(b *testing.B) string {
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
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 3000; i++ {
		text := ""
		for w := 0; w < 40; w++ {
			if rng.Intn(3) == 0 {
				text += stems[rng.Intn(len(stems))] + ends[rng.Intn(len(ends))] + " "
			} else {
				text += other[rng.Intn(len(other))] + " "
			}
		}
		line := fmt.Sprintf(`{"type":"user","sessionId":"s%d","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"%s"}}`, i, text) + "\n"
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
	for i := 0; i < b.N; i++ {
		if _, _, _, _, err := ProjectRelevant(dir, []string{"app"}, terms, 10); err != nil {
			b.Fatal(err)
		}
	}
}
