package rustem

import (
	"bufio"
	"compress/gzip"
	"os"
	"strings"
	"testing"
)

func readVocab(t testing.TB) [][2]string {
	f, err := os.Open("testdata/ru-snowball.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][2]string
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) == 2 {
			rows = append(rows, [2]string{parts[0], parts[1]})
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestSnowballVocabulary(t *testing.T) {
	rows := readVocab(t)
	if len(rows) != 49785 {
		t.Fatalf("vocabulary has %d rows, want 49785", len(rows))
	}
	bad := 0
	for _, r := range rows {
		if got := Stem(r[0]); got != r[1] {
			if bad < 25 {
				t.Errorf("Stem(%q) = %q, want %q", r[0], got, r[1])
			}
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d rows differ", bad, len(rows))
	}
}

func TestYoIsE(t *testing.T) {
	if Stem("зелёный") != Stem("зеленый") {
		t.Errorf("ё and е stem apart: %q vs %q", Stem("зелёный"), Stem("зеленый"))
	}
}

func TestNoVowelAndLongWord(t *testing.T) {
	if got := Stem("нн"); got == "" {
		t.Errorf("Stem(нн) is empty")
	}
	long := strings.Repeat("абвгд", 30) + "ами"
	if got := Stem(long); got == "" {
		t.Error("long word stemmed to nothing")
	}
}

func BenchmarkStem(b *testing.B) {
	rows := readVocab(b)
	b.ResetTimer()
	n := 0
	for i := 0; i < b.N; i++ {
		for _, r := range rows {
			_ = Stem(r[0])
			n++
		}
	}
	b.ReportMetric(float64(n)/b.Elapsed().Seconds(), "words/s")
}

func TestRootStopsBeforeABareBeginning(t *testing.T) {
	for word, want := range map[string]string{"удаленная": "удален", "зеленый": "зелен", "телеграме": "телеграм", "лимиты": "лимит", "лимит": "лим"} {
		if got := Root(word); got != want {
			t.Errorf("Root(%q) = %q, want %q", word, got, want)
		}
	}
}

func TestKeysFileAShortStemToo(t *testing.T) {
	if got := Keys("лимиты"); len(got) != 3 || got[0] != "лимит" || got[1] != "лим" || got[2] != "лимиты" {
		t.Errorf("Keys(лимиты) = %q", got)
	}
	if got := Keys("отчет"); len(got) != 1 || got[0] != "отчет" {
		t.Errorf("Keys(отчет) = %q", got)
	}
	if !HasKey("лимитов", "лим") || !HasKey("лимитов", "лимит") || HasKey("лимузин", "лим") {
		t.Error("HasKey mismatch")
	}
}

// A pass of Snowball over a stem stops where it would leave fewer than four
// letters, and may leave exactly four: «аллеям» stays at «алле» (the next pass
// would give «алл»), «андреем» goes on to «андр».
func TestRootFloorIsFourLetters(t *testing.T) {
	for word, want := range map[string]string{"аллеям": "алле", "амалии": "амал", "андреем": "андр", "бараном": "бара"} {
		if got := Root(word); got != want {
			t.Errorf("Root(%q) = %q, want %q", word, got, want)
		}
	}
}
