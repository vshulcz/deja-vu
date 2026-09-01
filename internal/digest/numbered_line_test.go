package digest

import "testing"

// The rule was the number alone — `^\s*\d{1,6}\s` — so every sentence that
// opens with a count was dropped from every digest. Measured on a real store,
// 916 of the assistant lines it matched are sentences.
func TestASentenceThatStartsWithANumberIsKept(t *testing.T) {
	prose := []string{
		"12 зелёных. Жду ревьюера перед мержем.",
		"505 отказов записи шаблонов за 16 часов — это то, что я вчера «починил» ретраем. Проверяю в первую очередь.",
		"21 стора, 1.26 мс и 0.39 мс. Локально дёшево — но проверяю утверждение про сетевой стор.",
		"3 sessions read it before the fix. None of them saw the pair.",
		"已修复 3 个测试。现在全部通过。",
	}
	for _, l := range prose {
		if numberedDumpLine(l) {
			t.Errorf("a sentence was dropped as a numbered dump: %q", l)
		}
	}
}

// And the dumps it exists for are still dumps. `cat -n` and the file readers
// put a tab after the number; the space-separated ones are fragments with no
// sentence in them.
func TestANumberedDumpLineIsStillDropped(t *testing.T) {
	dumps := []string{
		"1\tdiff --git a/internal/index/ingest.go b/internal/index/ingest.go",
		// A numbered source line whose content is a sentence: the tab is what
		// says it is a listing, and without it the sentence test would keep it.
		"   42\t// Raised the pool to 40. It held.",
		"3 files changed, 9 insertions(+)",
		"1 file changed, 1 insertion(+), 1 deletion(-)",
		"300 index.js",
		"1564 nonpass=0",
		// A rendered table row that happens to contain a sentence end. The
		// pipe is what says it is a row.
		"61 |            [claude] goprojects/d3fvxl · today · Summary: 1. done",
	}
	for _, l := range dumps {
		if !numberedDumpLine(l) {
			t.Errorf("a numbered dump got through: %q", l)
		}
	}
}

// A line that does not open with a number is not this rule's business.
func TestTheNumberedRuleOnlyLooksAtNumberedLines(t *testing.T) {
	for _, l := range []string{"raised the pool to 40", "\tindented but no number", ""} {
		if numberedDumpLine(l) {
			t.Errorf("matched a line with no leading number: %q", l)
		}
	}
}

// hasSentenceEnd follows firstSentences: a stop needs a space after it, so a
// version number does not end a sentence, and the CJK stops take none.
func TestHasSentenceEndAgreesWithTheSentenceSplitter(t *testing.T) {
	for _, s := range []string{"pinned pgx. it held", "done.", "готово! дальше", "已修复。"} {
		if !hasSentenceEnd(s) {
			t.Errorf("%q ends a sentence", s)
		}
	}
	for _, s := range []string{"v5.4.3 held", "300 index.js", "files changed, 9 insertions(+)", ""} {
		if hasSentenceEnd(s) {
			t.Errorf("%q does not end a sentence", s)
		}
	}
}
