package search

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestBlamePathResolutionAndMatching(t *testing.T) {
	target, err := ResolveBlamePath("cmd/deja/main.go")
	if err != nil || target.Base != "main.go" || target.Stem != "main" || !strings.HasSuffix(filepath.ToSlash(target.FullPath), "/cmd/deja/main.go") {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	if _, err := ResolveBlamePath(""); err == nil {
		t.Fatal("empty path accepted")
	}
	now := time.Now()
	ss := []model.Session{
		{ID: "bare", Project: "other", Updated: now, Messages: []model.Message{{Role: "user", Text: "main.go needs a test"}}},
		{ID: "suffix", Project: "other", Updated: now, Messages: []model.Message{{Role: "user", Text: "we changed cmd/deja/main.go carefully"}}},
		{ID: "false", Project: "other", Updated: now, Messages: []model.Message{{Role: "user", Text: "domain.got is unrelated"}}},
	}
	hits := Blame(ss, target, BlameOptions{All: true})
	if len(hits) != 2 || hits[0].Session.ID != "suffix" || hits[1].Session.ID != "bare" {
		t.Fatalf("hits=%#v", hits)
	}
	if hits[0].Title != "we changed cmd/deja/main.go carefully" || len(hits[0].Snippets) != 1 {
		t.Fatalf("hit=%#v", hits[0])
	}
	if got := Blame([]model.Session{{ID: "x", Updated: now, Messages: []model.Message{{Text: "domain.got"}}}}, BlameTarget{FullPath: "/repo/domain.go", Base: "domain.go", Stem: "domain"}, BlameOptions{}); len(got) != 0 {
		t.Fatalf("false positive=%#v", got)
	}
}

func TestBlameFiltersProjectBoostLimitAndJSON(t *testing.T) {
	now := time.Now()
	ss := make([]model.Session, 0, 12)
	for i := 0; i < 12; i++ {
		ss = append(ss, model.Session{ID: string(rune('a' + i)), Harness: "claude", Project: "/repo", Updated: now, Messages: []model.Message{{Role: "user", Text: "parser.go"}}})
	}
	target := BlameTarget{FullPath: "/repo/parser.go", Base: "parser.go", Stem: "parser"}
	hits := Blame(ss, target, BlameOptions{Harness: "claude", Project: "repo"})
	if len(hits) != 10 {
		t.Fatalf("default limit=%d", len(hits))
	}
	if got := Blame(ss, target, BlameOptions{Harness: "codex", All: true}); len(got) != 0 {
		t.Fatalf("harness filter=%#v", got)
	}
	var b bytes.Buffer
	PrintBlame(&b, hits[:1], true)
	var decoded []BlameHit
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("json=%q err=%v", b.String(), err)
	}
	b.Reset()
	PrintBlame(&b, hits[:1], false)
	if !strings.Contains(b.String(), "parser.go") || !strings.Contains(b.String(), "claude") {
		t.Fatalf("plain=%q", b.String())
	}
}

func TestBlameHelpersAndSince(t *testing.T) {
	if got := blameForms("/repo/cmd/main.go"); len(got) != 6 || got[0] != "/repo/cmd/main.go" || got[1] != "repo/cmd/main.go" || got[4] != "/main.go" {
		t.Fatalf("forms=%v", got)
	}
	if count, _ := mentionScore("nothing here", "main.go", nil); count != 0 {
		t.Fatalf("unexpected mention count=%d", count)
	}
	if pathFormCount("xcmd/main.go", "cmd/main.go") != 0 || pathFormCount("cmd/main.go", "cmd/main.go") != 1 {
		t.Fatal("path form boundary checks failed")
	}
	if !pathComponentOrWord("/main.go/", 1, 8) || pathComponentOrWord("xmain.go", 1, 8) || pathComponentOrWord("main.got", 0, 7) {
		t.Fatalf("boundary checks failed: slash=%v prefix=%v suffix=%v", pathComponentOrWord("/main.go/", 1, 8), pathComponentOrWord("xmain.go", 1, 8), pathComponentOrWord("main.got", 0, 7))
	}
	repo := t.TempDir()
	inRepo := filepath.Join(repo, "main.go")
	other := filepath.Join(repo, "..", "elsewhere")
	if !projectContainsFile(repo, inRepo) || projectContainsFile("relative", inRepo) || projectContainsFile(filepath.Clean(other), inRepo) {
		t.Fatal("project root checks failed")
	}
	now := time.Now()
	old := model.Session{ID: "old", Updated: now.Add(-48 * time.Hour), Messages: []model.Message{{Role: "user", Text: "main.go"}}}
	if got := Blame([]model.Session{old}, BlameTarget{FullPath: "/main.go", Base: "main.go"}, BlameOptions{Since: time.Hour, All: true}); len(got) != 0 {
		t.Fatalf("since filter=%#v", got)
	}
	if sessionTitle(model.Session{Title: "explicit"}) != "explicit" || sessionTitle(model.Session{}) != "" {
		t.Fatal("title fallback failed")
	}
	long := strings.Repeat("x", 70)
	if got := sessionTitle(model.Session{Messages: []model.Message{{Role: "user", Text: long}}}); len(got) != 63 {
		t.Fatalf("long title length=%d", len(got))
	}
}

func BenchmarkBlameVerification1000Candidates(b *testing.B) {
	now := time.Now()
	ss := make([]model.Session, 1000)
	for i := range ss {
		ss[i] = model.Session{ID: string(rune(i + 1000)), Updated: now, Messages: []model.Message{{Text: "prefix parser.go suffix"}}}
	}
	target := BlameTarget{FullPath: "/repo/internal/parser.go", Base: "parser.go", Stem: "parser"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := Blame(ss, target, BlameOptions{All: true}); len(got) != len(ss) {
			b.Fatalf("hits=%d", len(got))
		}
	}
}
