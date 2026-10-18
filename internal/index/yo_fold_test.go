package index

import (
	"reflect"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestTokensFoldYo(t *testing.T) {
	want := tokens("зеленый")
	for _, in := range []string{"зелёный", "ЗЕЛЁНЫЙ", "Зеленый"} {
		if got := tokens(in); !reflect.DeepEqual(got, want) {
			t.Errorf("tokens(%q) = %q, want %q", in, got, want)
		}
	}
	if got := indexKeys("Зелёный"); len(got) == 0 || got[0] != "tзеленый" {
		t.Errorf("indexKeys = %q", got)
	}
	if h := tokenHits("Зелёный ёж", map[string]bool{"зеленый": true, "еж": true}); len(h) != 2 {
		t.Errorf("tokenHits = %v", h)
	}
}

func TestDidYouMeanKeepsYoWords(t *testing.T) {
	dir := t.TempDir()
	writeTestStore(t, dir, model.Session{
		Harness: "claude", ID: "a", Project: "p",
		Messages: []model.Message{{Role: "user", Text: "зелёный отчёт готов"}},
	})
	// A word spelt with ё tokenises to its е form; the check must compare the
	// folded spelling or it skips every such word.
	if got := DidYouMean(dir, "зелёныйй"); got == "" {
		t.Fatal("a mistyped ё word got no suggestion")
	}
}

// A query written with ё reads a message written with е at the per-message
// pass too: without the fold the focused turn holds none of the query's words
// and keeps the place the pool gave it.
func TestBestMessageFusionFoldsYo(t *testing.T) {
	terms := []string{"зелёный", "отчёт", "счёт"}
	idf := map[string]float64{"зеленый": 2.0, "отчет": 2.5, "счет": 1.5}
	scattered := model.Session{ID: "scattered", Messages: []model.Message{
		{Role: "assistant", Text: "зеленый цвет подходит"},
		{Role: "assistant", Text: "отчет лежит в папке"},
		{Role: "assistant", Text: "счет закрыт"},
	}}
	thin := model.Session{ID: "thin", Messages: []model.Message{
		{Role: "assistant", Text: "отчет и еще отчет"},
	}}
	focused := model.Session{ID: "focused", Messages: []model.Message{
		{Role: "user", Text: "Зеленый отчет и счет готовы"},
	}}
	got := sessionIDs(rerankByBestMessage([]model.Session{scattered, thin, focused}, terms, idf))
	if got[1] != "focused" {
		t.Fatalf("order = %v, want focused second", got)
	}
}

// A CamelCase word splits into parts filed the way every other word is.
func TestSplitCompoundFoldsYo(t *testing.T) {
	var out []string
	splitCompound([]rune("ЁлкаПраздник"), &out)
	if len(out) == 0 || out[0] != "елка" {
		t.Fatalf("parts = %q, want елка first", out)
	}
}
