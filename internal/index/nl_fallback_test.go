package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func nlIndex(t *testing.T, texts ...string) string {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	var sessions []model.Session
	for i, text := range texts {
		sessions = append(sessions, model.Session{
			ID: string(rune('a' + i)), Harness: "claude", Project: "p", Updated: time.Now(),
			Messages: []model.Message{{Role: "user", Text: text}},
		})
	}
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStemFallbackIngToSPair(t *testing.T) {
	dir := nlIndex(t, "staging domain fails CORS but prod works")
	result, err := SearchDetailed(dir, search.Options{Query: "cors failing staging", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || !result.Stemmed {
		t.Fatalf("failing->fails not recovered: sessions=%d stemmed=%v", len(result.Sessions), result.Stemmed)
	}
	if vs := result.Variants["failing"]; len(vs) == 0 {
		t.Fatalf("variants missing failing: %v", result.Variants)
	}
}

func TestStemFallbackShortTermPlural(t *testing.T) {
	dir := nlIndex(t, "rate limiter lets short bursts through the bucket")
	result, err := SearchDetailed(dir, search.Options{Query: "limiter let bursts", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 {
		t.Fatalf("let->lets not recovered: %+v", result)
	}
}

func TestStemFallbackDropsFillerToken(t *testing.T) {
	dir := nlIndex(t,
		"rate limiter lets short bursts through the bucket",
		"why do refresh tokens die after rotation",
	)
	// "why" matches only the other session; no session holds all tokens.
	result, err := SearchDetailed(dir, search.Options{Query: "why limiter lets bursts through", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "a" {
		t.Fatalf("filler token still constrains the AND: %+v", result.Sessions)
	}
	if vs, ok := result.Variants["why"]; !ok || len(vs) != 1 || vs[0] != "" {
		t.Fatalf("dropped token not marked optional: %v", result.Variants)
	}
}

func TestStemFallbackKeepsAndWhenFullMatchExists(t *testing.T) {
	dir := nlIndex(t,
		"rate limiter lets short bursts through the bucket",
		"why does the rate limiter lets bursts pass in this trace",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "why limiter lets bursts", All: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Sessions {
		if s.ID == "b" {
			return
		}
	}
	t.Fatalf("full-coverage session missing from results: %+v", result.Sessions)
}

func TestStemFallbackNeedsTwoAnchors(t *testing.T) {
	dir := nlIndex(t, "rate limiter lets short bursts through")
	// Only one token exists in the corpus — dropping the rest would leave a
	// single-term query the user did not ask for.
	result, err := SearchDetailed(dir, search.Options{Query: "zzqx wwvv limiter", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 0 {
		t.Fatalf("single anchor produced results: %+v", result.Sessions)
	}
}

func TestCloseTierDisagreeFallsThroughToRelevance(t *testing.T) {
	// Session a answers the question but lacks "birthday" in any form, so the
	// substring intersection lands only on b — an incidental session whose
	// long words contain every query token. The ladder used to stop at b.
	dir := nlIndex(t,
		"picked a scarf as the gift for my sister celebration",
		"gifted birthdays sisterhood newsletter volume nine",
	)
	result, err := SearchDetailed(dir, search.Options{Query: "sister birthday gift", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) < 2 || result.Sessions[0].ID != "a" {
		t.Fatalf("relevance must lead when substring hits disagree: %+v", idsOf(result.Sessions))
	}
	if result.Sessions[1].ID != "b" {
		t.Fatalf("substring hit must survive as the tail: %+v", idsOf(result.Sessions))
	}
}

func idsOf(ss []model.Session) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}
